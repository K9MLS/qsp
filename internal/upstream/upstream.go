// Package upstream carries traffic between QSP and another DMR network over an
// OpenBridge link.
//
// A Link owns one UDP socket. It signs and sends frames to the far end, and
// hands received frames to a callback. It makes no routing decisions of its
// own: what crosses the link, and in which direction, is the routing core's
// business. See docs/adr/ADR-0018-openbridge.md.
//
// # Why time is injected
//
// OpenBridge has no keep-alive, so the only evidence a link is alive is that
// frames have arrived recently. Reporting that means comparing timestamps, and
// a test that had to wait four hours to check a four-hour threshold would never
// be written. Now is a field so the staleness tests take microseconds.
package upstream

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/openbridge"
)

// Config describes one link.
type Config struct {
	// Name identifies the link on the console and in the health report.
	Name string
	// ListenAddress is the local UDP host:port to receive on.
	ListenAddress string
	// TargetAddress is the far end's host:port.
	TargetAddress string
	// NetworkID identifies this server to the far end, stamped into every
	// frame sent.
	NetworkID hbp.RepeaterID
	// Passphrase is the shared secret. Required: an empty one produces
	// signatures anybody else with an empty one can forge.
	Passphrase []byte
	// StaleAfter is how long without a received frame before the link reports
	// itself as possibly broken. Zero disables the warning.
	StaleAfter time.Duration
	// Receive is called for each frame that arrives and verifies.
	//
	// It runs on the link's read goroutine, so it must not block: a slow
	// receiver stops the link reading, and UDP discards what it cannot deliver.
	Receive func(name string, frame hbp.Data)
	// Now supplies the clock. Nil means time.Now.
	Now func() time.Time
}

// Link is one OpenBridge connection.
type Link struct {
	cfg  Config
	log  *slog.Logger
	now  func() time.Time
	conn *net.UDPConn
	dest *net.UDPAddr

	running atomic.Bool

	// closeOnce makes Close idempotent, and closeErr is what every caller
	// after the first is told. Two things close this link: the goroutine
	// serve starts on the context, and the Set closing at shutdown. Whichever
	// lost the race got "use of closed network connection", the Set returned
	// it as its own error, and cmd/qsp reported "shutdown was not clean" and
	// exited 1 — so systemd recorded `Failed with result 'exit-code'` for
	// every ordinary stop, which is exactly the line somebody chases for an
	// hour during a real fault.
	//
	// **Guarded on the close rather than on running**, because serve clears
	// running on its way out: keying idempotence to that flag would let a
	// later Close return nil without ever closing the socket.
	closeOnce sync.Once
	closeErr  error

	mu sync.Mutex
	// lastReceived is when a frame last verified. Zero means none ever has,
	// which is reported differently from "none lately": a link that has never
	// carried traffic is usually misconfigured, while one that has gone quiet
	// is usually a network fault.
	lastReceived time.Time
	lastSent     time.Time
	stats        Stats
}

// Stats counts what has crossed the link.
type Stats struct {
	// Sent is frames successfully written to the far end.
	Sent uint64
	// Received is frames that arrived and verified.
	Received uint64
	// Rejected is datagrams that arrived and did not verify.
	//
	// A steady trickle here almost always means the two ends disagree about
	// the passphrase, which is otherwise indistinguishable from a dead link.
	Rejected uint64
	// SendErrors counts write failures.
	SendErrors uint64
}

// New creates a link. It does not open a socket; Start does that.
func New(log *slog.Logger, cfg Config) (*Link, error) {
	if log == nil {
		return nil, errors.New("upstream: a logger is required")
	}
	if cfg.Name == "" {
		return nil, errors.New("upstream: a name is required")
	}
	if len(cfg.Passphrase) == 0 {
		return nil, fmt.Errorf("upstream %q: a passphrase is required; an empty one "+
			"produces signatures anyone else with an empty passphrase can forge", cfg.Name)
	}
	if cfg.NetworkID == 0 {
		return nil, fmt.Errorf("upstream %q: a network ID is required; it identifies this "+
			"server in every frame sent", cfg.Name)
	}
	if cfg.Receive == nil {
		return nil, fmt.Errorf("upstream %q: a Receive callback is required", cfg.Name)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Link{cfg: cfg, log: log.With("subsystem", "upstream", "link", cfg.Name), now: now}, nil
}

// Name implements Connection.
func (l *Link) Name() string { return l.cfg.Name }

// Start binds the socket and begins receiving.
func (l *Link) Start(ctx context.Context) error {
	dest, err := net.ResolveUDPAddr("udp", l.cfg.TargetAddress)
	if err != nil {
		return fmt.Errorf("upstream %q: cannot understand target address %q: %w "+
			"(use host:port, for example \"3102.master.brandmeister.network:62035\")",
			l.cfg.Name, l.cfg.TargetAddress, err)
	}

	local, err := net.ResolveUDPAddr("udp", l.cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("upstream %q: cannot understand listen address %q: %w "+
			"(use host:port, for example \"0.0.0.0:62035\")",
			l.cfg.Name, l.cfg.ListenAddress, err)
	}

	conn, err := net.ListenUDP("udp", local)
	if err != nil {
		return fmt.Errorf("upstream %q: cannot listen on %s: %w", l.cfg.Name, l.cfg.ListenAddress, err)
	}

	l.conn = conn
	l.dest = dest
	l.running.Store(true)

	l.log.Info("link open",
		"target", l.cfg.TargetAddress,
		"listening", conn.LocalAddr().String(),
		"network_id", uint32(l.cfg.NetworkID))

	go l.serve(ctx)
	return nil
}

// Address reports the local address actually bound, which differs from the
// configured one when port 0 was requested.
func (l *Link) Address() string {
	if l.conn == nil {
		return l.cfg.ListenAddress
	}
	return l.conn.LocalAddr().String()
}

// Close stops the link. It is safe to call more than once, and from more than
// one goroutine; see closeOnce.
func (l *Link) Close() error {
	if l.conn == nil {
		return nil
	}
	l.closeOnce.Do(func() {
		l.running.Store(false)
		l.closeErr = l.conn.Close()
	})
	return l.closeErr
}

// Send signs a frame and writes it to the far end.
func (l *Link) Send(frame hbp.Data) error {
	if l.conn == nil {
		return fmt.Errorf("upstream %q: the link is not open", l.cfg.Name)
	}

	packet, err := openbridge.Encode(frame, l.cfg.NetworkID, l.cfg.Passphrase)
	if err != nil {
		return fmt.Errorf("upstream %q: %w", l.cfg.Name, err)
	}

	if _, err := l.conn.WriteToUDP(packet, l.dest); err != nil {
		l.mu.Lock()
		l.stats.SendErrors++
		l.mu.Unlock()
		return fmt.Errorf("upstream %q: %w", l.cfg.Name, err)
	}

	now := l.now()
	l.mu.Lock()
	l.stats.Sent++
	l.lastSent = now
	l.mu.Unlock()
	return nil
}

// Stats returns a snapshot of the counters.
func (l *Link) Stats() Stats {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}

// Status describes the link for the health report.
type Status struct {
	// Name is the link's configured name.
	Name string
	// Open reports whether the socket is bound.
	Open bool
	// EverReceived reports whether any frame has ever verified.
	EverReceived bool
	// Since is how long since a frame last arrived. Meaningless when
	// EverReceived is false.
	Since time.Duration
	// Stale reports that Since exceeds the configured threshold.
	Stale bool
	// Summary is a sentence for an operator.
	Summary string
	// Advice is what to do about it, empty when there is nothing wrong.
	//
	// **It comes from the link rather than from the health check**, because
	// what to check differs entirely between the two protocols. OpenBridge has
	// no keep-alive, so its advice is about addresses and quiet talkgroups; a
	// homebrew link has one, so silence there is a fault and the advice is
	// about credentials. A single hardcoded string would be wrong for one of
	// them, and wrong advice is worse than none.
	Advice string

	Stats Stats
}

// Status reports what QSP knows about the link, which is deliberately less than
// an operator would like.
//
// OpenBridge has no keep-alive, so a quiet talkgroup and a dead link are
// indistinguishable from here. The summary says so rather than picking one.
// This is the case Constitution §3 exists for: the absence of knowledge is
// reported rather than papered over.
func (l *Link) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()

	st := Status{
		Name:         l.cfg.Name,
		Open:         l.running.Load(),
		EverReceived: !l.lastReceived.IsZero(),
		Stats:        l.stats,
	}

	switch {
	case !st.Open:
		st.Summary = "the link is not open"

	case !st.EverReceived && l.stats.Rejected > 0:
		st.Advice = "check the passphrase file matches what the far end was given; " +
			"both ends must hold the same secret"
		// The most useful thing this package can say. A link that has received
		// only unverifiable datagrams is one where both ends are configured and
		// talking, and disagree about the passphrase — otherwise
		// indistinguishable from silence.
		st.Summary = fmt.Sprintf("nothing has verified, and %d datagrams failed their signature; "+
			"the two ends most likely disagree about the passphrase", l.stats.Rejected)

	case !st.EverReceived:
		st.Summary = "no traffic has ever arrived on this link; " +
			"check the far end has this server's address and that UDP is reaching it"
		st.Advice = "confirm the far end has this server's current public address and that " +
			"UDP reaches this port; an address change breaks an OpenBridge link silently"

	default:
		st.Since = l.now().Sub(l.lastReceived).Truncate(time.Second)
		st.Stale = l.cfg.StaleAfter > 0 && st.Since > l.cfg.StaleAfter
		if st.Stale {
			st.Summary = fmt.Sprintf("no traffic received for %s; "+
				"this may be a quiet talkgroup or a broken link", st.Since)
			st.Advice = "if the talkgroup is genuinely quiet, raise stale_after; if not, " +
				"check the link with the far end's operator"
		} else {
			st.Summary = fmt.Sprintf("last traffic %s ago", st.Since)
		}
	}
	return st
}

// serve reads datagrams until the context ends or the socket closes.
func (l *Link) serve(ctx context.Context) {
	defer l.running.Store(false)

	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	buf := make([]byte, openbridge.PacketSize*2)
	for {
		n, from, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil || !l.running.Load() {
				l.log.Info("link closed")
				return
			}
			l.log.Warn("read failed", "error", err)
			return
		}

		frame, err := openbridge.Parse(buf[:n], l.cfg.Passphrase)
		if err != nil {
			l.mu.Lock()
			l.stats.Rejected++
			count := l.stats.Rejected
			l.mu.Unlock()

			// Logged at warning for the first few only. A misconfigured or
			// hostile sender can produce these as fast as the network allows,
			// and filling an operator's journal with them would obscure the
			// problem rather than report it.
			if count <= 5 {
				l.log.Warn("rejected a datagram", "from", from.String(), "error", err,
					"rejected_total", count)
			}
			continue
		}

		now := l.now()
		l.mu.Lock()
		l.stats.Received++
		l.lastReceived = now
		l.mu.Unlock()

		l.cfg.Receive(l.cfg.Name, frame)
	}
}

// Degraded reports whether the link is working but not well.
//
// It is derived from Advice rather than restated: a link that has something to
// advise is one with a problem, and keeping the two in step by hand is how they
// drift apart.
func (s Status) Degraded() bool { return s.Advice != "" }
