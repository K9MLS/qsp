// Package events provides QSP's internal publish/subscribe bus.
//
// The bus exists so that subsystems can report state changes without knowing
// who consumes them. Its consumers today are the console (via Server-Sent
// Events) and the audit recorder; more will follow.
//
// Three properties drive the design:
//
//  1. Every event carries a monotonically increasing sequence number. A
//     consumer that reconnects can state the last sequence it saw and learn
//     whether it missed anything.
//
//  2. Publishing never blocks. A subsystem handling network traffic must not
//     stall because a browser tab is slow. Delivery to a slow subscriber is
//     dropped and counted, and the subscriber is marked lagged so it can
//     resynchronise deliberately rather than silently losing state.
//
//  3. A bounded history is retained for replay. The history is deliberately
//     finite; Replay reports whether it could cover the requested gap so that a
//     consumer is never misled into believing it has a complete stream.
package events

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/logging"
)

// Type identifies the kind of an event.
//
// Only types declared here may be published. Adding a type is a deliberate act:
// it becomes part of the console's contract and, where applicable, the audit
// trail.
type Type string

const (
	// TypePeerConnected is emitted when a peer completes registration.
	TypePeerConnected Type = "peer.connected"
	// TypePeerDisconnected is emitted when a peer is removed, whether by
	// explicit close, timeout, or administrative action.
	TypePeerDisconnected Type = "peer.disconnected"
	// TypeCallStarted is emitted at the start of a voice stream.
	TypeCallStarted Type = "call.started"
	// TypeCallEnded is emitted when a voice stream terminates or times out.
	TypeCallEnded Type = "call.ended"
	// TypeRouteChanged is emitted when the active routing table changes.
	TypeRouteChanged Type = "route.changed"
	// TypeHealthChanged is emitted when any health check changes status.
	TypeHealthChanged Type = "health.changed"
	// TypeConfigChanged is emitted when a new configuration version becomes active.
	TypeConfigChanged Type = "config.changed"
)

// knownTypes gates Publish. An unknown type is a programming error, not a
// runtime condition, but the bus refuses it rather than propagating it to the
// console where it would appear as an unexplained event.
var knownTypes = map[Type]bool{
	TypePeerConnected:    true,
	TypePeerDisconnected: true,
	TypeCallStarted:      true,
	TypeCallEnded:        true,
	TypeRouteChanged:     true,
	TypeHealthChanged:    true,
	TypeConfigChanged:    true,
}

// IsKnown reports whether t is a declared event type.
func IsKnown(t Type) bool { return knownTypes[t] }

// KnownTypes returns every declared event type. The order is not stable.
func KnownTypes() []Type {
	out := make([]Type, 0, len(knownTypes))
	for t := range knownTypes {
		out = append(out, t)
	}
	return out
}

// Event is a single occurrence on the bus.
//
// Data must be JSON-serialisable; it is delivered verbatim to the console. It
// must never contain credentials.
type Event struct {
	Seq  uint64    `json:"seq"`
	Type Type      `json:"type"`
	Time time.Time `json:"time"`
	Data any       `json:"data,omitempty"`
}

// Options configures a Bus.
type Options struct {
	// HistorySize is the number of past events retained for Replay. Zero
	// selects DefaultHistorySize.
	HistorySize int
	// SubscriberBuffer is the per-subscriber queue depth. Zero selects
	// DefaultSubscriberBuffer.
	SubscriberBuffer int
	// Clock supplies event timestamps. Zero uses time.Now. Injected so that
	// tests are deterministic.
	//
	// Publish may be called concurrently, so an injected clock must be safe
	// for concurrent use.
	Clock func() time.Time
}

// Defaults for Options.
const (
	DefaultHistorySize      = 256
	DefaultSubscriberBuffer = 64
)

// Bus is a fan-out publish/subscribe hub. It is safe for concurrent use.
type Bus struct {
	log   *slog.Logger
	clock func() time.Time

	mu          sync.RWMutex
	seq         uint64
	subscribers map[*Subscription]struct{}
	closed      bool

	// history is a ring buffer of the most recent events.
	history     []Event
	historyNext int
	historyLen  int

	// defaultBuf is the per-subscriber queue depth chosen at construction.
	defaultBuf int
}

// NewBus constructs a Bus. The logger may be nil, in which case a discard
// logger is used.
func NewBus(log *slog.Logger, opts Options) *Bus {
	if opts.HistorySize <= 0 {
		opts.HistorySize = DefaultHistorySize
	}
	if opts.SubscriberBuffer <= 0 {
		opts.SubscriberBuffer = DefaultSubscriberBuffer
	}
	if opts.Clock == nil {
		opts.Clock = time.Now
	}
	return &Bus{
		log:         logging.Subsystem(log, "events"),
		clock:       opts.Clock,
		subscribers: make(map[*Subscription]struct{}),
		history:     make([]Event, opts.HistorySize),
		defaultBuf:  opts.SubscriberBuffer,
	}
}

// Publish records an event and delivers it to every current subscriber.
//
// Publish never blocks. It returns the event as recorded, including its
// assigned sequence number. Publishing an undeclared type, or publishing to a
// closed bus, returns the zero Event and false.
func (b *Bus) Publish(t Type, data any) (Event, bool) {
	if !IsKnown(t) {
		b.log.Error("refusing to publish undeclared event type", slog.String("type", string(t)))
		return Event{}, false
	}

	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return Event{}, false
	}
	b.seq++
	ev := Event{Seq: b.seq, Type: t, Time: b.clock(), Data: data}

	b.history[b.historyNext] = ev
	b.historyNext = (b.historyNext + 1) % len(b.history)
	if b.historyLen < len(b.history) {
		b.historyLen++
	}

	targets := make([]*Subscription, 0, len(b.subscribers))
	for s := range b.subscribers {
		targets = append(targets, s)
	}
	b.mu.Unlock()

	for _, s := range targets {
		s.deliver(ev, b.log)
	}
	return ev, true
}

// Subscribe registers a new subscriber and reports the sequence number current
// at the moment of subscription.
//
// The returned sequence is the caller's synchronisation point: a consumer that
// has taken a state snapshot should subscribe first, then snapshot, then
// discard delivered events at or below the returned sequence. That ordering
// guarantees no gap between snapshot and stream.
//
// The caller must call Close on the subscription when finished.
func (b *Bus) Subscribe() (*Subscription, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := &Subscription{
		ch:  make(chan Event, b.defaultBuf),
		bus: b,
	}
	if b.closed {
		s.closed = true
		close(s.ch)
		return s, b.seq
	}
	b.subscribers[s] = struct{}{}
	return s, b.seq
}

// Replay returns retained events with a sequence number greater than since.
//
// The boolean result reports whether the history was able to cover the whole
// requested range. False means events were evicted before they could be
// replayed and the consumer must resynchronise from a fresh snapshot rather
// than assuming continuity.
func (b *Bus) Replay(since uint64) ([]Event, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.historyLen == 0 {
		// Nothing retained. Complete only if the caller is already current.
		return nil, since >= b.seq
	}

	start := (b.historyNext - b.historyLen + len(b.history)) % len(b.history)
	oldest := b.history[start].Seq

	out := make([]Event, 0, b.historyLen)
	for i := 0; i < b.historyLen; i++ {
		ev := b.history[(start+i)%len(b.history)]
		if ev.Seq > since {
			out = append(out, ev)
		}
	}

	// The range is fully covered when the caller has already seen everything
	// older than our oldest retained event.
	complete := since+1 >= oldest || since >= b.seq
	return out, complete
}

// Seq returns the most recently assigned sequence number.
func (b *Bus) Seq() uint64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.seq
}

// SubscriberCount returns the number of active subscribers.
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}

// Close shuts the bus down and closes every subscriber channel. Subsequent
// publishes are refused. Close is idempotent.
func (b *Bus) Close() {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.closed = true
	subs := make([]*Subscription, 0, len(b.subscribers))
	for s := range b.subscribers {
		subs = append(subs, s)
	}
	b.subscribers = make(map[*Subscription]struct{})
	b.mu.Unlock()

	for _, s := range subs {
		s.shutdown()
	}
}

// Subscription is a single consumer's view of the bus.
//
// The mutex serialises delivery against closure. Without it, Publish could
// observe an open subscription, be preempted while the consumer calls Close,
// and then send on a closed channel. Checking a flag before sending is not
// sufficient: the check and the send must be atomic with respect to closure.
type Subscription struct {
	ch  chan Event
	bus *Bus

	mu     sync.Mutex
	closed bool

	lagged atomic.Uint64
}

// C returns the channel on which events are delivered. The channel is closed
// when the subscription or the bus is closed.
func (s *Subscription) C() <-chan Event { return s.ch }

// Lagged returns the number of events dropped because this subscriber was not
// consuming quickly enough.
//
// A non-zero value means the subscriber's view is incomplete and it must
// resynchronise. It is never reset; callers compare against a previous reading.
func (s *Subscription) Lagged() uint64 { return s.lagged.Load() }

// Close removes the subscription from the bus. It is idempotent and safe to
// call concurrently with delivery.
func (s *Subscription) Close() {
	if s.bus != nil {
		s.bus.mu.Lock()
		delete(s.bus.subscribers, s)
		s.bus.mu.Unlock()
	}
	s.shutdown()
}

func (s *Subscription) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

// deliver enqueues an event without blocking. A full queue means this consumer
// is too slow; the event is dropped and counted.
func (s *Subscription) deliver(ev Event, log *slog.Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- ev:
	default:
		n := s.lagged.Add(1)
		if n == 1 || n%100 == 0 {
			log.Warn("subscriber lagging; events dropped",
				slog.Uint64("dropped_total", n),
				slog.String("type", string(ev.Type)),
			)
		}
	}
}
