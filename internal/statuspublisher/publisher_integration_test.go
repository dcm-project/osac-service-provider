package statuspublisher_test

// Integration scope (per
// .ai/test-plans/osac-sp-m5-status-reporting.test-plan.md, section 5):
// a real, unstubbed collaborator crossing a protocol boundary this SP does
// not control — an embedded, real nats-server/v2 broker with JetStream
// enabled (DD-073's pinned v2.12.5), not a hand-written fake. TC-I-400 is
// the DD-073-mandatory golden-JSON contract test: it decodes the real wire
// bytes into control-plane's own StatusEvent struct, vendored verbatim
// below (see the citation on that type) rather than asserting a
// hand-rolled struct against itself.
//
// This file is package statuspublisher_test (external/black-box, unlike
// publisher_unit_test.go's white-box package statuspublisher) since it only
// needs the public API — but it shares publisher_test.go's single
// TestStatusPublisher/RunSpecs entry point: both Go packages compile into
// one test binary per directory, and Ginkgo's Describe registers into that
// shared suite regardless of which of the two packages declares it (same
// pattern as internal/apiserver's server_integration_test.go).

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/dcm-project/osac-service-provider/internal/statuspublisher"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// controlPlaneStatusEvent is control-plane's real consumer-side payload
// struct, vendored verbatim (field names, types, json tags, and field
// order all preserved) from
// github.com/dcm-project/control-plane@76ca1d7a6639454cebd4b879c3dc8f6560d7572f's
// internal/sp/consumer/consumer.go's StatusEvent — the golden schema
// TC-I-400 (DD-073) asserts this publisher's real wire output against, not
// a hand-rolled struct asserted against itself.
type controlPlaneStatusEvent struct {
	Id        string    `json:"id"`
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// Real deployed values from control-plane's internal/app/config.go NATSConfig
// (envconfig defaults): stream "dcm-status", subject filter "dcm.*",
// durable consumer name "control-plane".
const (
	controlPlaneStreamName   = "dcm-status"
	controlPlaneSubjectAll   = "dcm.*"
	controlPlaneConsumerName = "control-plane"
)

var (
	itClusterType = statuspublisher.ServiceType{Subject: "dcm.cluster", Type: "dcm.status.cluster", Source: "dcm/providers/osac-sp-cluster"}
	itVMType      = statuspublisher.ServiceType{Subject: "dcm.vm", Type: "dcm.status.vm", Source: "dcm/providers/osac-sp-vm"}
)

// startJetStreamServer starts a real, embedded, JetStream-enabled
// nats-server on a random port, using a fresh on-disk store per test.
func startJetStreamServer() (*natsserver.Server, string) {
	opts := natstest.DefaultTestOptions
	opts.Port = -1
	opts.JetStream = true
	opts.StoreDir = GinkgoT().TempDir()
	s := natstest.RunServer(&opts)
	return s, s.ClientURL()
}

// createControlPlaneStreamAndConsumer mirrors control-plane's own
// consumer.Start: creates the "dcm-status" stream over subjects "dcm.*",
// and a durable "control-plane" pull consumer on it.
func createControlPlaneStreamAndConsumer(ctx context.Context, natsURL string) (jetstream.Consumer, func()) {
	nc, err := nats.Connect(natsURL)
	Expect(err).NotTo(HaveOccurred())

	js, err := jetstream.New(nc)
	Expect(err).NotTo(HaveOccurred())

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     controlPlaneStreamName,
		Subjects: []string{controlPlaneSubjectAll},
	})
	Expect(err).NotTo(HaveOccurred())

	cons, err := js.CreateOrUpdateConsumer(ctx, controlPlaneStreamName, jetstream.ConsumerConfig{
		Durable:   controlPlaneConsumerName,
		AckPolicy: jetstream.AckExplicitPolicy,
	})
	Expect(err).NotTo(HaveOccurred())

	return cons, nc.Close
}

// decodeAsCloudEventThenControlPlaneEvent mirrors control-plane's own
// StatusConsumer.handleMessage decode sequence exactly: unmarshal the raw
// message into a cloudevents.Event, then unmarshal that event's Data()
// into controlPlaneStatusEvent.
func decodeAsCloudEventThenControlPlaneEvent(raw []byte) controlPlaneStatusEvent {
	var event cloudeventsEnvelope
	Expect(json.Unmarshal(raw, &event)).To(Succeed())

	var payload controlPlaneStatusEvent
	Expect(json.Unmarshal(event.Data, &payload)).To(Succeed())
	return payload
}

// cloudeventsEnvelope decodes only the "data" field, matching what
// event.Data() returns in control-plane's real cloudevents.Event-based
// decode path, without pulling in the full SDK type on the assertion side.
type cloudeventsEnvelope struct {
	Data json.RawMessage `json:"data"`
}

var _ = Describe("Golden-JSON contract against a real embedded broker", func() {
	var (
		srv     *natsserver.Server
		natsURL string
	)

	BeforeEach(func() {
		srv, natsURL = startJetStreamServer()
		DeferCleanup(srv.Shutdown)
	})

	// TC-I-400: real publish-to-consume round trip produces the exact
	// canonical `data` schema.
	It("produces the exact canonical data schema for both service types (TC-I-400)", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		cons, closeConsumerConn := createControlPlaneStreamAndConsumer(ctx, natsURL)
		defer closeConsumerConn()

		p, err := statuspublisher.NewPublisher(natsURL, slog.New(slog.DiscardHandler))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = p.Close() }()

		runCtx, runCancel := context.WithCancel(context.Background())
		defer runCancel()
		p.Start(runCtx)

		p.Publish(itClusterType, "cluster-1", "ACTIVE", "control plane healthy")
		p.Publish(itVMType, "vm-1", "RUNNING", "instance is running")

		batch, err := cons.Fetch(2, jetstream.FetchMaxWait(5*time.Second))
		Expect(err).NotTo(HaveOccurred())

		payloadsByID := map[string]controlPlaneStatusEvent{}
		for msg := range batch.Messages() {
			payload := decodeAsCloudEventThenControlPlaneEvent(msg.Data())
			payloadsByID[payload.Id] = payload
			Expect(msg.Ack()).To(Succeed())
		}
		Expect(batch.Error()).NotTo(HaveOccurred())
		Expect(payloadsByID).To(HaveLen(2))

		cluster := payloadsByID["cluster-1"]
		Expect(cluster.Status).To(Equal("ACTIVE"))
		Expect(cluster.Message).To(Equal("control plane healthy"))
		Expect(cluster.Timestamp.IsZero()).To(BeTrue(), "data must never contain a timestamp field (SC-M5-002/DD-071)")

		vm := payloadsByID["vm-1"]
		Expect(vm.Status).To(Equal("RUNNING"))
		Expect(vm.Message).To(Equal("instance is running"))
		Expect(vm.Timestamp.IsZero()).To(BeTrue(), "data must never contain a timestamp field (SC-M5-002/DD-071)")
	})

	// TC-I-401: a publish issued before the stream exists still eventually
	// succeeds once the stream is created.
	It("eventually delivers a publish issued before the stream existed (TC-I-401)", func() {
		p, err := statuspublisher.NewPublisher(natsURL, slog.New(slog.DiscardHandler),
			statuspublisher.WithInitialBackoff(20*time.Millisecond),
			statuspublisher.WithMaxBackoff(100*time.Millisecond))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = p.Close() }()

		runCtx, runCancel := context.WithCancel(context.Background())
		defer runCancel()
		p.Start(runCtx)

		p.Publish(itVMType, "vm-2", "PROVISIONING", "starting")

		// No stream exists yet for subject "dcm.vm": give the worker a few
		// failed attempts (retrying with backoff) before creating it.
		time.Sleep(150 * time.Millisecond)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cons, closeConsumerConn := createControlPlaneStreamAndConsumer(ctx, natsURL)
		defer closeConsumerConn()

		var payload controlPlaneStatusEvent
		Eventually(func() (controlPlaneStatusEvent, error) {
			batch, err := cons.Fetch(1, jetstream.FetchMaxWait(2*time.Second))
			if err != nil {
				return controlPlaneStatusEvent{}, err
			}
			for msg := range batch.Messages() {
				payload = decodeAsCloudEventThenControlPlaneEvent(msg.Data())
				_ = msg.Ack()
			}
			return payload, batch.Error()
		}, 10*time.Second, 500*time.Millisecond).Should(Equal(controlPlaneStatusEvent{Id: "vm-2", Status: "PROVISIONING", Message: "starting"}))
	})
})
