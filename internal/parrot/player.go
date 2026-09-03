package parrot

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Sink delivers one frame of a replay to the member who recorded it.
//
// # Why the delivery is injected rather than written here
//
// A recording is the same object whichever protocol produced it: frames, a
// peer, a time to start. What differs is how the frames get back. A Homebrew
// hotspot is a UDP address and a marshalled frame; a Motorola repeater is a
// registered peer on a different socket that needs the frame rebuilt as IPSC.
//
// **The timing is the delicate part and there must be exactly one copy of it.**
// Frames have to leave sixty milliseconds apart or a radio's jitter buffer
// stops trusting them, and a second implementation of that loop is a second
// place for a drift bug to live and be fixed in only one of them.
type Sink interface {
	// Deliver sends one frame to the peer a recording came from. An error is
	// counted and the replay continues: UDP to a hotspot on a domestic
	// connection drops packets, and abandoning a recording over one of them
	// would make parrot look broken when it is not.
	Deliver(peer hbp.RepeaterID, frame hbp.Data) error
}

// Player replays finished recordings, one goroutine per peer.
//
// It exists apart from any listener because replaying is not routing: it holds
// its own frames, touches no shared state, and answers exactly one member. The
// listener's own sweep runs once a second, sixteen times too slow to release a
// frame every sixty milliseconds.
type Player struct {
	log   *slog.Logger
	sink  Sink
	stats *PlayerStats

	// mu guards running, which is the set of peers being replayed to.
	mu      sync.Mutex
	running map[hbp.RepeaterID]context.CancelFunc
}

// PlayerStats counts what happened, for the health report.
type PlayerStats struct {
	played  uint64
	frames  uint64
	stopped uint64
	errs    uint64
	mu      sync.Mutex
}

// Snapshot returns the counters: recordings played, frames sent, replays
// stopped early, and failed deliveries.
func (s *PlayerStats) Snapshot() (played, frames, stopped, errs uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.played, s.frames, s.stopped, s.errs
}

// NewPlayer returns a player that delivers through sink.
func NewPlayer(log *slog.Logger, sink Sink) *Player {
	return &Player{
		log:     log,
		sink:    sink,
		stats:   &PlayerStats{},
		running: make(map[hbp.RepeaterID]context.CancelFunc),
	}
}

// Stats returns the counters this player is accumulating.
func (p *Player) Stats() *PlayerStats { return p.stats }

// Start replays a recording to one peer.
//
// A replay already running for that peer is stopped first. A member who keys up
// while hearing themselves has started over, and two audio streams on one
// timeslot is what contention exists to prevent.
func (p *Player) Start(ctx context.Context, rec Recording) {
	p.Stop(rec.Peer)

	inner, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.running[rec.Peer] = cancel
	p.mu.Unlock()

	go p.play(inner, rec)
}

// Stop ends a peer's replay, if one is running.
func (p *Player) Stop(peer hbp.RepeaterID) {
	p.mu.Lock()
	cancel := p.running[peer]
	delete(p.running, peer)
	p.mu.Unlock()

	if cancel != nil {
		cancel()
		p.stats.mu.Lock()
		p.stats.stopped++
		p.stats.mu.Unlock()
	}
}

// Active reports how many replays are running.
func (p *Player) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.running)
}

// play sends the frames, then forgets the peer.
func (p *Player) play(ctx context.Context, rec Recording) {
	defer func() {
		p.mu.Lock()
		delete(p.running, rec.Peer)
		p.mu.Unlock()
	}()

	// The gap before the replay begins. A replay that starts instantly is
	// played at a radio that has not finished transmitting and is not
	// listening yet, and the first second is lost.
	if wait := time.Until(rec.PlayAt); wait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	// A ticker rather than sleeping for the interval: sleeping adds the time
	// spent marshalling and writing to every gap, so a long recording would
	// drift slower and slower against the radio's expectations.
	ticker := time.NewTicker(FrameInterval)
	defer ticker.Stop()

	var sent uint64
	for _, frame := range rec.Frames {
		select {
		case <-ctx.Done():
			p.log.Debug("playback stopped",
				slog.Uint64("peer", uint64(rec.Peer)), slog.Uint64("frames_sent", sent))
			return
		case <-ticker.C:
		}

		// The peer's own ID, because the far end registered this link and a
		// frame naming anything else is from a station it has never heard of.
		out := frame
		out.RepeaterID = rec.Peer

		if err := p.sink.Deliver(rec.Peer, out); err != nil {
			p.stats.mu.Lock()
			p.stats.errs++
			p.stats.mu.Unlock()
			continue
		}
		sent++
	}

	p.stats.mu.Lock()
	p.stats.played++
	p.stats.frames += sent
	p.stats.mu.Unlock()

	p.log.Info("parrot replayed",
		slog.Uint64("peer", uint64(rec.Peer)),
		slog.Uint64("frames", sent),
		slog.String("duration", rec.Duration.Truncate(time.Millisecond).String()),
		slog.Bool("truncated", rec.Truncated),
	)
}
