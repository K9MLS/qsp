package v24link

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Voice toward a repeater, held a moment and then sent at the rate it is
// spoken.
//
// **Why.** A repeater transmits a voice record every twenty milliseconds and
// has nothing to transmit when the next one is late. Until 0.1.315 QSP wrote
// each record to the tunnel the moment it arrived, so every late arrival was a
// late record on the air. Across a room nothing is late. Across the internet
// things are, and a call from a hotspot or from another repeater reaches QSP
// with gaps in it.
//
// So the first voice record of a call waits for Config.Hold, and the rest
// follow twenty milliseconds apart on a schedule kept from that moment. A
// record up to Hold late is sent on time.
//
// **What this does not do.** It evens out what arrives at QSP. The tunnel from
// QSP to the router is after it, and what that adds is added. A repeater a
// long way from its QSP depends on how the repeater itself treats a late
// record, which has not been measured.
//
// **Nothing is dropped, and nothing is sent faster to catch up.** Voice fills
// most of a 9600 bit/s line: eighteen records are 308 bytes every 360 ms
// before framing (testdata/p25/p25-voice.pcap), so there is no room to send
// faster than it is spoken. A stall leaves the rest of that call behind by the
// length of the stall; CallTimeout ends a call that stalls for longer, so that
// is under a second, and the next call starts level.

// RecordInterval is how far apart a repeater's voice records are.
const RecordInterval = 20 * time.Millisecond

// MaxHold is the longest Config.Hold accepted. Ten records: past that the
// delay is heard in conversation, and it is not what is wrong with a link
// that needs it.
const MaxHold = 200 * time.Millisecond

// schedule decides when each voice record of a call leaves. It is the pacer
// apart from the waiting, so it can be tested against a clock that is not
// running.
type schedule struct {
	hold time.Duration
	// next is when the next voice record is due, and zero between calls.
	next time.Time
	// last is when the last voice record arrived.
	last time.Time

	// The call in progress, for its log line.
	records  int
	dry      int
	worstGap time.Duration
}

// due reports when a voice record that arrived at arrived should leave.
//
// The first of a call leaves Hold later. Each one after leaves a
// RecordInterval after the one before, unless it arrived after that moment:
// then the repeater has already run dry, the record leaves as it arrived, and
// the schedule starts again from there with no hold, because a second wait
// would only lengthen the silence.
func (s *schedule) due(arrived time.Time) time.Time {
	if !s.next.IsZero() && arrived.Sub(s.last) > CallTimeout {
		// The end of the last call never came through here. This is a new one.
		s.reset()
	}
	at := s.next
	switch {
	case s.next.IsZero():
		at = arrived.Add(s.hold)
	case arrived.After(s.next):
		s.dry++
		at = arrived
	}
	if s.records > 0 {
		s.worstGap = max(s.worstGap, arrived.Sub(s.last))
	}
	s.records++
	s.last = arrived
	s.next = at.Add(RecordInterval)
	return at
}

// reset is the end of a call.
func (s *schedule) reset() { *s = schedule{hold: s.hold} }

// queued is one frame waiting to be written.
type queued struct {
	payload []byte
	kind    RecordKind
	arrived time.Time
}

// pacer is one repeater's way out: a queue, and the goroutine that empties it
// on the schedule.
type pacer struct {
	log   *slog.Logger
	now   func() time.Time
	write func(payload []byte) error

	mu    sync.Mutex
	queue []queued
	wake  chan struct{}
}

func newPacer(log *slog.Logger, now func() time.Time, write func([]byte) error) *pacer {
	return &pacer{log: log, now: now, write: write, wake: make(chan struct{}, 1)}
}

// send queues one frame of a call. It never blocks and never fails: a tunnel
// that cannot be written to is found by run, and closed by its own reader.
func (p *pacer) send(payload []byte) error {
	rec, _ := ReadRecord(payload)
	p.mu.Lock()
	p.queue = append(p.queue, queued{payload: payload, kind: rec.Kind, arrived: p.now()})
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
	return nil
}

// take removes the frame at the head of the queue.
func (p *pacer) take() (queued, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.queue) == 0 {
		return queued{}, false
	}
	q := p.queue[0]
	p.queue[0] = queued{}
	p.queue = p.queue[1:]
	return q, true
}

// discard empties the queue.
func (p *pacer) discard() {
	p.mu.Lock()
	p.queue = nil
	p.mu.Unlock()
}

// run writes the queue to the tunnel until ctx ends or stop closes. Voice
// waits for its moment; markers and headers go as soon as they reach the head
// of the queue, which keeps them in their place among the voice.
func (p *pacer) run(ctx context.Context, stop <-chan struct{}, hold time.Duration) {
	s := schedule{hold: hold}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		q, ok := p.take()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-p.wake:
			}
			continue
		}
		if q.kind == RecordVoice {
			if wait := s.due(q.arrived).Sub(p.now()); wait > 0 {
				timer.Reset(wait)
				select {
				case <-ctx.Done():
					return
				case <-stop:
					return
				case <-timer.C:
				}
			}
		}
		if err := p.write(q.payload); err != nil {
			// The tunnel is going. What is queued for it has nowhere to go.
			p.discard()
			s.reset()
			continue
		}
		if q.kind == RecordEnd {
			if s.records > 0 {
				p.log.Info("a call was sent to the repeater",
					slog.Int("voice_records", s.records),
					slog.Int64("hold_ms", hold.Milliseconds()),
					slog.Int64("worst_gap_ms", s.worstGap.Milliseconds()),
					slog.Int("ran_dry", s.dry))
			}
			s.reset()
		}
	}
}
