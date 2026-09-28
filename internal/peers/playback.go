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

// playback replays recordings to Homebrew peers.
//
// The timing lives in parrot.Player, which both protocols share; this is the
// part that is specific to a hotspot — a UDP address, and a frame marshalled as
// Homebrew. A Motorola repeater is replayed to by the IPSC listener through the
// same player and a different sink.
type playback struct {
	player *parrot.Player

	// mu guards addrs, the address to replay to for each peer. It is set when
	// a replay starts, because the sink is handed a peer and the socket needs
	// somewhere to write.
	mu    sync.Mutex
	addrs map[hbp.RepeaterID]netip.AddrPort
	conn  playbackWriter
	// gate is the listener's preamble gate, so a replayed or composed text
	// reaches a hotspot with one preamble like any other. Nil passes every
	// frame, which is what a test constructing a playback alone wants.
	gate *preambleGate
}

func newPlayback(log *slog.Logger, conn playbackWriter) *playback {
	return newPlaybackAs(log, conn, "parrot replayed")
}

// newPlaybackAs is a playback whose completions are logged as finished. The
// text sender uses one of its own, so that a composed message is never
// mistaken in the journal for a parrot and never cuts one off.
func newPlaybackAs(log *slog.Logger, conn playbackWriter, finished string) *playback {
	p := &playback{
		addrs: make(map[hbp.RepeaterID]netip.AddrPort),
		conn:  conn,
	}
	p.player = parrot.NewPlayerAs(log, p, finished)
	return p
}

// Busy reports whether a playback to peer is running.
func (p *playback) Busy(peer hbp.RepeaterID) bool { return p.player.Busy(peer) }

// Deliver marshals one frame and writes it to the peer's address.
func (p *playback) Deliver(peer hbp.RepeaterID, frame hbp.Data) error {
	p.mu.Lock()
	to, ok := p.addrs[peer]
	conn := p.conn
	p.mu.Unlock()
	if !ok || conn == nil {
		// The socket is attached when the listener binds. A playback before
		// that cannot happen through the ordinary path, and crashing on it
		// would be a poor trade for an impossible case.
		return errNoSocket
	}
	for _, f := range p.gate.pass(peer, frame, time.Now()) {
		if _, err := conn.WriteToUDPAddrPort(f.Marshal(), to); err != nil {
			return err
		}
	}
	return nil
}

// Start replays a recording to one peer at an address.
func (p *playback) Start(ctx context.Context, rec parrot.Recording, to netip.AddrPort) {
	p.mu.Lock()
	p.addrs[rec.Peer] = to
	p.mu.Unlock()
	p.player.Start(ctx, rec)
}

// Stop ends a peer's playback, if one is running.
func (p *playback) Stop(peer hbp.RepeaterID) { p.player.Stop(peer) }

// Active reports how many playbacks are running.
func (p *playback) Active() int { return p.player.Active() }

// stats returns the counters, for the health report.
func (p *playback) stats() (played, frames, stopped, errs uint64) {
	return p.player.Stats().Snapshot()
}

// errNoSocket is returned when a frame is ready before the listener has bound.
type noSocketError struct{}

func (noSocketError) Error() string {
	return "peers: the playback socket is not attached yet"
}

var errNoSocket = noSocketError{}

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
