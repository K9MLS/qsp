package peers

import (
	"context"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// playbackWriter is the socket, narrowed to the one thing playback does with
// it.
//
// **Playback is the only part of QSP that writes to the peer socket without
// being the listener**, which is a deliberate exception to ADR-0002 and worth
// naming rather than leaving to be discovered. It is safe for a narrow reason:
// net.UDPConn is safe for concurrent use, and playback touches no routing
// state — it holds its own frames and sends them to one address.
//
// The exception exists because DMR frames must leave 60 milliseconds apart and
// the listener's sweep runs every second, sixteen times too slow for a single
// frame.
type playbackWriter interface {
	WriteToUDPAddrPort(b []byte, addr netip.AddrPort) (int, error)
}

// playback replays recordings.
type playback struct {
	log   *slog.Logger
	conn  playbackWriter
	stats *playbackStats

	// mu guards running, which is the set of peers currently being played
	// back to.
	mu      sync.Mutex
	running map[hbp.RepeaterID]context.CancelFunc
}

// playbackStats counts what happened, for the health report.
type playbackStats struct {
	played  uint64
	frames  uint64
	stopped uint64
	errs    uint64
	mu      sync.Mutex
}

func newPlayback(log *slog.Logger, conn playbackWriter) *playback {
	return &playback{
		log:     log,
		conn:    conn,
		stats:   &playbackStats{},
		running: make(map[hbp.RepeaterID]context.CancelFunc),
	}
}

// Start replays a recording to one peer.
//
// A playback already running for that peer is stopped first. A member who keys
// up while hearing themselves has started over, and two audio streams on one
// timeslot is what contention exists to prevent.
func (p *playback) Start(ctx context.Context, rec parrot.Recording, to netip.AddrPort) {
	p.Stop(rec.Peer)

	inner, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.running[rec.Peer] = cancel
	p.mu.Unlock()

	go p.play(inner, rec, to)
}

// Stop ends a peer's playback, if one is running.
func (p *playback) Stop(peer hbp.RepeaterID) {
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

// Active reports how many playbacks are running.
func (p *playback) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.running)
}

// play sends the frames, then forgets the peer.
func (p *playback) play(ctx context.Context, rec parrot.Recording, to netip.AddrPort) {
	defer func() {
		p.mu.Lock()
		delete(p.running, rec.Peer)
		p.mu.Unlock()
	}()

	// The gap before the replay begins. A replay that starts instantly is
	// played at a radio that has not finished transmitting and is not
	// listening yet, and the first second is lost.
	wait := time.Until(rec.PlayAt)
	if wait > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}

	// A ticker rather than sleeping for the interval: sleeping adds the time
	// spent marshalling and writing to every gap, so a long recording would
	// drift slower and slower against the radio's expectations.
	ticker := time.NewTicker(parrot.FrameInterval)
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

		if p.conn == nil {
			// The socket is attached when the listener binds. A playback
			// before that cannot happen through the ordinary path, and
			// crashing on it would be a poor trade for an impossible case.
			return
		}
		if _, err := p.conn.WriteToUDPAddrPort(out.Marshal(), to); err != nil {
			// One failed write does not end the playback: UDP to a hotspot on
			// a domestic connection drops packets, and abandoning a recording
			// over one of them would make parrot look broken when it is not.
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

// snapshot returns the counters.
func (p *playbackStats) snapshot() (played, frames, stopped, errs uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.played, p.frames, p.stopped, p.errs
}

// expireParrot completes recordings whose transmissions have stopped.
//
// Called from the sweep. A transmission ends in silence rather than in a
// distinguishable frame — the header and the terminator share a frame type —
// so this is where most recordings finish.
func (l *Listener) expireParrot() {
	if l.cfg.Parrot == nil {
		return
	}
	for _, rec := range l.cfg.Parrot.Expire(time.Now()) {
		l.replay(rec)
	}
}
