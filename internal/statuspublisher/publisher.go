// Package statuspublisher builds CloudEvents v1.0 envelopes describing a
// cluster or VM's current status and delivers them over NATS JetStream,
// coalescing rapid updates for the same resource and retrying indefinitely
// on failure so no update is ever silently dropped.
//
// Implements REQ-PUBLISH-* (see
// .ai/specs/osac-sp-m5-status-reporting.spec.md).
package statuspublisher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	defaultInitialBackoff = 1 * time.Second
	defaultMaxBackoff     = 60 * time.Second
)

// ServiceType binds the CloudEvents subject/type/source attributes for one
// of this SP's service types (cluster or vm). Callers (internal/statuspoll)
// supply the value appropriate to the resource being published.
//
// Implements REQ-PUBLISH-030.
type ServiceType struct {
	// Subject is both the CloudEvents "subject" attribute and the exact
	// JetStream subject events are published to (REQ-PUBLISH-040), e.g.
	// "dcm.cluster".
	Subject string
	// Type is the CloudEvents "type" attribute, e.g. "dcm.status.cluster".
	Type string
	// Source is the CloudEvents "source" attribute, e.g.
	// "dcm/providers/osac-sp-cluster".
	Source string
}

// StatusPayload is the exact shape of a status event's CloudEvents "data"
// field — no more, no fewer keys (REQ-PUBLISH-030).
type StatusPayload struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// jsPublisher is the subset of jetstream.JetStream this package depends on,
// narrowed for testability with a hand-written fake (CLAUDE.md: no mocking
// framework).
type jsPublisher interface {
	Publish(ctx context.Context, subject string, payload []byte, opts ...jetstream.PublishOpt) (*jetstream.PubAck, error)
}

// Option configures a Publisher's retry backoff.
type Option func(*publisherOptions)

type publisherOptions struct {
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

// WithInitialBackoff overrides the default 1s initial retry backoff.
func WithInitialBackoff(d time.Duration) Option {
	return func(o *publisherOptions) { o.initialBackoff = d }
}

// WithMaxBackoff overrides the default 60s max retry backoff.
func WithMaxBackoff(d time.Duration) Option {
	return func(o *publisherOptions) { o.maxBackoff = d }
}

// pendingKey identifies a resource's slot in the coalescing queue.
// Implements the "(st.Subject, resourceID)" key from REQ-PUBLISH-050.
type pendingKey struct {
	subject    string
	resourceID string
}

type pendingValue struct {
	serviceType ServiceType
	status      string
	message     string
}

// Publisher builds and delivers status CloudEvents over NATS JetStream. The
// zero value is not usable; construct via NewPublisher.
//
// Implements REQ-PUBLISH-020, REQ-PUBLISH-050..090.
type Publisher struct {
	js      jsPublisher
	closeFn func()
	logger  *slog.Logger

	initialBackoff time.Duration
	maxBackoff     time.Duration

	mu      sync.Mutex
	pending map[pendingKey]pendingValue

	wake      chan struct{}
	startOnce sync.Once
	done      chan struct{}
}

// NewPublisher dials NATS (configured to retry indefinitely on connection
// loss, never blocking startup — REQ-PUBLISH-020) and wraps it with
// JetStream.
func NewPublisher(natsURL string, logger *slog.Logger, opts ...Option) (*Publisher, error) {
	nc, err := nats.Connect(natsURL, nats.RetryOnFailedConnect(true), nats.MaxReconnects(-1))
	if err != nil {
		return nil, fmt.Errorf("connecting to NATS: %w", err)
	}

	// Coverage exception (documented, not tested): jetstream.New only
	// fails if one of its variadic JetStreamOpt options returns an error;
	// this call passes none, so no input reachable from this codebase can
	// make this branch fire today. Kept as defensive error handling
	// against a future option being added here rather than fabricating a
	// failing fake purely to hit it, per this suite's "test real
	// production types" convention (see
	// .ai/test-plans/osac-sp-m5-status-reporting.test-plan.md's coverage
	// note).
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("creating JetStream context: %w", err)
	}

	return newPublisher(js, nc.Close, logger, opts...), nil
}

func newPublisher(js jsPublisher, closeFn func(), logger *slog.Logger, opts ...Option) *Publisher {
	o := publisherOptions{initialBackoff: defaultInitialBackoff, maxBackoff: defaultMaxBackoff}
	for _, opt := range opts {
		opt(&o)
	}

	return &Publisher{
		js:             js,
		closeFn:        closeFn,
		logger:         logger,
		initialBackoff: o.initialBackoff,
		maxBackoff:     o.maxBackoff,
		pending:        make(map[pendingKey]pendingValue),
		wake:           make(chan struct{}, 1),
		done:           make(chan struct{}),
	}
}

// Publish records the given status as the latest pending update for this
// resource and returns immediately, without waiting for network I/O
// (REQ-PUBLISH-050). If a newer update for the same resource arrives before
// the previous one is delivered, only the newer value is ultimately
// delivered (REQ-PUBLISH-080).
func (p *Publisher) Publish(st ServiceType, resourceID, status, message string) {
	key := pendingKey{subject: st.Subject, resourceID: resourceID}

	p.mu.Lock()
	p.pending[key] = pendingValue{serviceType: st, status: status, message: message}
	p.mu.Unlock()

	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Start launches exactly one background delivery worker, idempotently
// (REQ-PUBLISH-060). It returns immediately.
func (p *Publisher) Start(ctx context.Context) {
	p.startOnce.Do(func() {
		go p.run(ctx)
	})
}

// Done returns a channel closed once the delivery worker has returned
// (REQ-PUBLISH-060).
func (p *Publisher) Done() <-chan struct{} {
	return p.done
}

// Close closes the underlying NATS connection (REQ-PUBLISH-090).
func (p *Publisher) Close() error {
	if p.closeFn != nil {
		p.closeFn()
	}
	return nil
}

func (p *Publisher) run(ctx context.Context) {
	defer close(p.done)
	for {
		key, val, ok := p.nextPending()
		if ok {
			p.deliver(ctx, key, val)
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}

		select {
		case <-ctx.Done():
			return
		case <-p.wake:
		}
	}
}

// nextPending pops (removes) an arbitrary pending entry. Only one entry per
// key ever exists at a time, so pop order across distinct keys does not
// affect the per-key delivery guarantees in REQ-PUBLISH-080.
func (p *Publisher) nextPending() (pendingKey, pendingValue, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, v := range p.pending {
		delete(p.pending, k)
		return k, v, true
	}
	return pendingKey{}, pendingValue{}, false
}

// deliver publishes val, retrying with exponential backoff until it
// succeeds or ctx is cancelled. No enqueued update is ever silently dropped
// (REQ-PUBLISH-070).
func (p *Publisher) deliver(ctx context.Context, key pendingKey, val pendingValue) {
	backoff := p.initialBackoff
	for {
		// Coverage exception (documented, not tested): buildEnvelope cannot
		// fail for any input reachable from this codebase today (see its
		// own coverage-exception comments); this branch guards against a
		// future change there rather than being fabricated to hit it.
		raw, err := buildEnvelope(val.serviceType, key.resourceID, val.status, val.message)
		if err != nil {
			p.logger.Error("failed to build status envelope; dropping malformed update",
				"error", err, "resource_id", key.resourceID, "subject", key.subject)
			return
		}

		if _, err := p.js.Publish(ctx, val.serviceType.Subject, raw); err == nil {
			return
		} else {
			p.logger.Warn("status publish failed, retrying",
				"error", err, "resource_id", key.resourceID, "subject", key.subject, "backoff", backoff)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}

		backoff *= 2
		if backoff > p.maxBackoff {
			backoff = p.maxBackoff
		}
	}
}

// buildEnvelope constructs a CloudEvents v1.0 envelope matching the
// canonical spec's worked example byte-for-byte (REQ-PUBLISH-030, DD-071).
func buildEnvelope(st ServiceType, resourceID, status, message string) ([]byte, error) {
	ev := cloudevents.NewEvent()
	ev.SetID(uuid.NewString())
	ev.SetSource(st.Source)
	ev.SetType(st.Type)
	ev.SetSubject(st.Subject)
	ev.SetTime(time.Now().UTC())

	// Coverage exception (documented, not tested): StatusPayload's fields
	// are all plain strings, so SetData's internal json.Marshal cannot fail
	// for any input reachable from this codebase today. Kept as defensive
	// error handling against a future field-type change rather than
	// fabricating a failing fake purely to hit it, per this suite's "test
	// real production types" convention (see
	// .ai/test-plans/osac-sp-m5-status-reporting.test-plan.md's coverage
	// note).
	if err := ev.SetData(cloudevents.ApplicationJSON, StatusPayload{
		ID:      resourceID,
		Status:  status,
		Message: message,
	}); err != nil {
		return nil, fmt.Errorf("setting event data: %w", err)
	}

	// Coverage exception (documented, not tested): same rationale as the
	// SetData check above — a cloudevents.Event whose data was just
	// successfully set from an all-string struct always marshals cleanly.
	raw, err := json.Marshal(ev)
	if err != nil {
		return nil, fmt.Errorf("marshaling event: %w", err)
	}
	return raw, nil
}
