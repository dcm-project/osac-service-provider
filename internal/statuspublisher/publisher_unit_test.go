package statuspublisher

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var discardLogger = slog.New(slog.DiscardHandler)

var (
	clusterType = ServiceType{Subject: "dcm.cluster", Type: "dcm.status.cluster", Source: "dcm/providers/osac-sp-cluster"}
	vmType      = ServiceType{Subject: "dcm.vm", Type: "dcm.status.vm", Source: "dcm/providers/osac-sp-vm"}
)

// wireEnvelope decodes just enough of a marshaled CloudEvents envelope for
// assertions, without depending on the cloudevents SDK's own (already
// spec-compliant, per DD-071) struct on the assertion side too.
type wireEnvelope struct {
	SpecVersion     string          `json:"specversion"`
	ID              string          `json:"id"`
	Source          string          `json:"source"`
	Type            string          `json:"type"`
	Subject         string          `json:"subject"`
	Time            string          `json:"time"`
	DataContentType string          `json:"datacontenttype"`
	Data            json.RawMessage `json:"data"`
}

func decodeEnvelope(raw []byte) wireEnvelope {
	var env wireEnvelope
	Expect(json.Unmarshal(raw, &env)).To(Succeed())
	return env
}

// recordedCall is one attempt (successful or not) seen by fakeJS.
type recordedCall struct {
	subject string
	payload []byte
}

// fakeJS is a hand-written fake satisfying jsPublisher (CLAUDE.md: no
// mocking framework). Every attempt, whether it ultimately fails or
// succeeds, is recorded before publishFunc decides the outcome, so tests
// can assert both delivered content and retry counts.
type fakeJS struct {
	mu          sync.Mutex
	attempts    []recordedCall
	publishFunc func(attemptIndex int, subject string, payload []byte) error

	// blockFirst, if non-nil, is closed by the first call once it has
	// recorded itself as "entered" (via enteredFirst) and is then made to
	// wait until blockFirst is closed, letting a test coalesce a second
	// Publish() before the first delivery attempt completes.
	blockFirst   chan struct{}
	enteredFirst chan struct{}

	inFlight    int32
	maxInFlight int32
}

func (f *fakeJS) Publish(_ context.Context, subject string, payload []byte, _ ...jetstream.PublishOpt) (*jetstream.PubAck, error) {
	n := atomic.AddInt32(&f.inFlight, 1)
	defer atomic.AddInt32(&f.inFlight, -1)
	for {
		old := atomic.LoadInt32(&f.maxInFlight)
		if n <= old || atomic.CompareAndSwapInt32(&f.maxInFlight, old, n) {
			break
		}
	}

	f.mu.Lock()
	idx := len(f.attempts)
	f.attempts = append(f.attempts, recordedCall{subject: subject, payload: append([]byte(nil), payload...)})
	fn := f.publishFunc
	block := f.blockFirst
	entered := f.enteredFirst
	f.mu.Unlock()

	if idx == 0 {
		if entered != nil {
			close(entered)
		}
		if block != nil {
			<-block
		}
	}

	if fn != nil {
		if err := fn(idx, subject, payload); err != nil {
			return nil, err
		}
	}
	return &jetstream.PubAck{}, nil
}

func (f *fakeJS) Attempts() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, len(f.attempts))
	copy(out, f.attempts)
	return out
}

var _ = Describe("Envelope construction", func() {
	// TC-U-400: envelope attributes match the canonical spec exactly
	It("sets envelope attributes exactly per the canonical spec (TC-U-400)", func() {
		raw, err := buildEnvelope(vmType, "vm-1", "RUNNING", "instance is running")
		Expect(err).NotTo(HaveOccurred())

		env := decodeEnvelope(raw)
		Expect(env.Source).To(Equal("dcm/providers/osac-sp-vm"))
		Expect(env.Type).To(Equal("dcm.status.vm"))
		Expect(env.Subject).To(Equal("dcm.vm"))
		Expect(env.DataContentType).To(Equal("application/json"))
		Expect(env.Time).NotTo(BeEmpty())
		_, err = time.Parse(time.RFC3339, env.Time)
		Expect(err).NotTo(HaveOccurred())

		var payload StatusPayload
		Expect(json.Unmarshal(env.Data, &payload)).To(Succeed())
		Expect(payload).To(Equal(StatusPayload{ID: "vm-1", Status: "RUNNING", Message: "instance is running"}))

		var raw2 map[string]json.RawMessage
		Expect(json.Unmarshal(env.Data, &raw2)).To(Succeed())
		Expect(raw2).To(HaveLen(3), "data must contain exactly id/status/message, no more")
	})

	// TC-U-401: envelope id is a fresh, non-resource identifier every call
	It("uses a fresh, non-resource envelope id on every call (TC-U-401)", func() {
		raw1, err := buildEnvelope(vmType, "vm-1", "PROVISIONING", "starting")
		Expect(err).NotTo(HaveOccurred())
		raw2, err := buildEnvelope(vmType, "vm-1", "RUNNING", "running")
		Expect(err).NotTo(HaveOccurred())

		env1, env2 := decodeEnvelope(raw1), decodeEnvelope(raw2)
		Expect(env1.ID).NotTo(BeEmpty())
		Expect(env2.ID).NotTo(BeEmpty())
		Expect(env1.ID).NotTo(Equal(env2.ID))
		Expect(env1.ID).NotTo(Equal("vm-1"))
		Expect(env2.ID).NotTo(Equal("vm-1"))
		_, err = uuid.Parse(env1.ID)
		Expect(err).NotTo(HaveOccurred())
		_, err = uuid.Parse(env2.ID)
		Expect(err).NotTo(HaveOccurred())
	})

	// TC-U-402: cluster and VM service types produce distinct subject/type/source
	DescribeTable("produces the documented distinct subject/type/source per service type (TC-U-402)",
		func(st ServiceType, wantSubject, wantType, wantSource string) {
			raw, err := buildEnvelope(st, "r-1", "ACTIVE", "msg")
			Expect(err).NotTo(HaveOccurred())
			env := decodeEnvelope(raw)
			Expect(env.Subject).To(Equal(wantSubject))
			Expect(env.Type).To(Equal(wantType))
			Expect(env.Source).To(Equal(wantSource))
		},
		Entry("cluster", clusterType, "dcm.cluster", "dcm.status.cluster", "dcm/providers/osac-sp-cluster"),
		Entry("vm", vmType, "dcm.vm", "dcm.status.vm", "dcm/providers/osac-sp-vm"),
	)
})

var _ = Describe("Publish lifecycle", func() {
	// TC-U-410: Publish returns before delivery completes
	It("returns before delivery completes (TC-U-410)", func() {
		fake := &fakeJS{blockFirst: make(chan struct{}), enteredFirst: make(chan struct{})}
		p := newPublisher(fake, func() {}, discardLogger, WithInitialBackoff(time.Millisecond), WithMaxBackoff(5*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p.Start(ctx)

		done := make(chan struct{})
		go func() {
			p.Publish(vmType, "vm-1", "RUNNING", "instance is running")
			close(done)
		}()

		Eventually(done, time.Second).Should(BeClosed(), "Publish must return without waiting for the fake to be unblocked")
		Expect(fake.Attempts()).To(HaveLen(0), "the fake must not have completed a call yet, proving Publish itself did not wait for it")
		close(fake.blockFirst)
	})

	// TC-U-411: the correct subject is used for delivery
	It("delivers to the exact subject dcm.{service_type} (TC-U-411)", func() {
		fake := &fakeJS{}
		p := newPublisher(fake, func() {}, discardLogger, WithInitialBackoff(time.Millisecond), WithMaxBackoff(5*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p.Start(ctx)
		p.Publish(clusterType, "c-1", "ACTIVE", "control plane healthy")

		Eventually(fake.Attempts, time.Second).Should(HaveLen(1))
		Expect(fake.Attempts()[0].subject).To(Equal("dcm.cluster"))
	})

	// TC-U-412: failed publishes retry with exponential backoff, never dropped
	It("retries with backoff and eventually delivers, never dropping the update (TC-U-412)", func() {
		fake := &fakeJS{
			publishFunc: func(idx int, _ string, _ []byte) error {
				if idx < 2 {
					return context.DeadlineExceeded
				}
				return nil
			},
		}
		initialBackoff := 20 * time.Millisecond
		p := newPublisher(fake, func() {}, discardLogger, WithInitialBackoff(initialBackoff), WithMaxBackoff(100*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		start := time.Now()
		p.Publish(vmType, "vm-1", "RUNNING", "instance is running")
		p.Start(ctx)

		Eventually(fake.Attempts, 2*time.Second).Should(HaveLen(3))
		Expect(time.Since(start)).To(BeNumerically(">=", initialBackoff))

		attempts := fake.Attempts()
		var payload StatusPayload
		env := decodeEnvelope(attempts[2].payload)
		Expect(json.Unmarshal(env.Data, &payload)).To(Succeed())
		Expect(payload).To(Equal(StatusPayload{ID: "vm-1", Status: "RUNNING", Message: "instance is running"}))
	})

	// TC-U-413: a newer update supersedes a stale one still pending delivery
	It("delivers only the latest value when a newer update supersedes a pending one (TC-U-413)", func() {
		fake := &fakeJS{blockFirst: make(chan struct{}), enteredFirst: make(chan struct{})}
		p := newPublisher(fake, func() {}, discardLogger, WithInitialBackoff(time.Millisecond), WithMaxBackoff(5*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		p.Publish(vmType, "vm-1", "PROVISIONING", "starting")
		p.Start(ctx)
		Eventually(fake.enteredFirst, time.Second).Should(BeClosed())

		p.Publish(vmType, "vm-1", "RUNNING", "instance is running")
		close(fake.blockFirst)

		Eventually(fake.Attempts, time.Second).Should(HaveLen(1))
		Consistently(fake.Attempts, 100*time.Millisecond).Should(HaveLen(1), "coalesced: exactly one delivery for this resource, never two")

		env := decodeEnvelope(fake.Attempts()[0].payload)
		var payload StatusPayload
		Expect(json.Unmarshal(env.Data, &payload)).To(Succeed())
		Expect(payload.Status).To(Equal("RUNNING"), "must never deliver the superseded PROVISIONING value")
	})

	// TC-U-414: two different resources are delivered independently
	It("delivers different resources independently, without cross-key coalescing (TC-U-414)", func() {
		fake := &fakeJS{}
		p := newPublisher(fake, func() {}, discardLogger, WithInitialBackoff(time.Millisecond), WithMaxBackoff(5*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		p.Start(ctx)

		p.Publish(vmType, "vm-1", "RUNNING", "r1")
		p.Publish(vmType, "vm-2", "STOPPED", "r2")

		Eventually(fake.Attempts, time.Second).Should(HaveLen(2))

		var statuses []string
		for _, a := range fake.Attempts() {
			env := decodeEnvelope(a.payload)
			var payload StatusPayload
			Expect(json.Unmarshal(env.Data, &payload)).To(Succeed())
			statuses = append(statuses, payload.ID+":"+payload.Status)
		}
		Expect(statuses).To(ConsistOf("vm-1:RUNNING", "vm-2:STOPPED"))
	})

	// TC-U-415: Start/Done are idempotent and mirror Registrar's lifecycle shape
	It("runs exactly one worker regardless of repeated Start calls, and closes Done once (TC-U-415)", func() {
		fake := &fakeJS{}
		p := newPublisher(fake, func() {}, discardLogger, WithInitialBackoff(time.Millisecond), WithMaxBackoff(5*time.Millisecond))
		ctx, cancel := context.WithCancel(context.Background())

		p.Start(ctx)
		p.Start(ctx)
		p.Start(ctx)

		for i := range 10 {
			p.Publish(vmType, "vm-1", "RUNNING", "iteration")
			_ = i
		}
		Eventually(fake.Attempts, time.Second).ShouldNot(BeEmpty())
		Expect(atomic.LoadInt32(&fake.maxInFlight)).To(BeNumerically("<=", 1), "no two workers ever delivered concurrently")

		cancel()
		Eventually(p.Done(), time.Second).Should(BeClosed())
	})

	// TC-U-416: Close closes the underlying NATS connection
	It("closes the underlying NATS connection (TC-U-416)", func() {
		var closed bool
		p := newPublisher(&fakeJS{}, func() { closed = true }, discardLogger)
		p.Close()
		Expect(closed).To(BeTrue())
	})
})
