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
	"github.com/k9mls/qsp/internal/protocol/homebrew"
)

// PeerConfig configures an outbound homebrew link.
type PeerConfig struct {
	// Name identifies the link on the console and in the health report.
	Name string
	// TargetAddress is the far end's host:port.
	TargetAddress string
	// Link is the handshake state machine this transport drives.
	Link *homebrew.Link
	// Receive is called for each frame that arrives on an established link.
	//
	// It runs on the link's own goroutine, so it must not block: a slow
	// receiver stops the link reading, and UDP discards what it cannot
	// deliver.
	Receive func(name string, frame hbp.Data)
	// TickInterval is how often the state machine is given the chance to act
	// on elapsed time. Zero selects DefaultTickInterval.
	TickInterval time.Duration
	// Resolve looks the far end up. Nil means the system resolver.
	Resolve Resolver
	// ResolveInterval is how often a link that is not connected looks the
	// far end up again. Zero selects DefaultResolveInterval.
	ResolveInterval time.Duration
	// Now supplies the clock. Nil means time.Now.
	Now func() time.Time
}

// DefaultTickInterval is how often the link is given a chance to act on time.
//
// It is much shorter than any timeout it governs. A tick is cheap and does
// nothing in the common case; making it long enough to matter would mean a
// keepalive interval of ten seconds firing up to a tick late, which the far end
// would eventually read as silence.
const DefaultTickInterval = time.Second

// PeerLink drives a homebrew.Link over a UDP socket.
//
// **This is the part that owns the state machine's concurrency.**
// homebrew.Link is deliberately not safe for concurrent use, in the
// single-writer style of ADR-0002, and three things want to touch it: the
// socket reader, the ticker, and whichever goroutine is routing a frame
// outward. Serialising them is this type's job, so the state machine stays a
// pure function of its inputs and remains testable without any of this.
type PeerLink struct {
	cfg PeerConfig
	log *slog.Logger
	now func() time.Time

	// connMu guards conn, remote and resolved. conn is nil until the far
	// end's name has resolved, and is replaced when it resolves to somewhere
	// new; see redial.
	connMu   sync.Mutex
	conn     *net.UDPConn
	remote   *net.UDPAddr
	resolved time.Time
	// lookupFailing is set while the name does not resolve, so that is said
	// once rather than every minute.
	lookupFailing bool

	running atomic.Bool

	// mu guards link and the counters. Held only across a state transition,
	// which is a handful of comparisons and at most one marshalled datagram.
	mu    sync.Mutex
	link  *homebrew.Link
	stats Stats
	// unreachable is the error that says the far end is not answering, while
	// that is the case; empty once a datagram arrives again.
	unreachable string
	// writeFailing is set by the first failed write of an outage and cleared
	// when a datagram arrives, so the keepalives of a far end that is gone say
	// so once rather than every tick.
	writeFailing atomic.Bool
	// lastReceived is when a frame last arrived, for Status.
	lastReceived time.Time
}

// NewPeer creates an outbound link. It does not open a socket; Start does that.
func NewPeer(log *slog.Logger, cfg PeerConfig) (*PeerLink, error) {
	if log == nil {
		return nil, errors.New("upstream: a logger is required")
	}
	if cfg.Name == "" {
		return nil, errors.New("upstream: a name is required")
	}
	if cfg.Link == nil {
		return nil, fmt.Errorf("upstream %q: a homebrew link is required", cfg.Name)
	}
	if cfg.Receive == nil {
		return nil, fmt.Errorf("upstream %q: a Receive callback is required", cfg.Name)
	}
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = DefaultTickInterval
	}
	if cfg.Resolve == nil {
		cfg.Resolve = systemResolver
	}
	if cfg.ResolveInterval <= 0 {
		cfg.ResolveInterval = DefaultResolveInterval
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &PeerLink{
		cfg:  cfg,
		log:  log.With("subsystem", "upstream", "link", cfg.Name),
		now:  now,
		link: cfg.Link,
	}, nil
}

// Name implements Connection.
func (l *PeerLink) Name() string { return l.cfg.Name }

// Start dials the far end and begins the handshake.
//
// The socket is connected rather than merely bound, which is the difference
// between this and an OpenBridge link: QSP is dialling out, so the kernel can
// discard datagrams from anywhere else before they reach this code.
func (l *PeerLink) Start(ctx context.Context) error {
	// A malformed address is a configuration mistake and stops the start. A
	// name that does not resolve right now is not: the link starts, says so,
	// and looks again every ResolveInterval.
	if err := checkAddress(l.cfg.TargetAddress); err != nil {
		return fmt.Errorf("upstream %q: cannot understand target address %q: %w "+
			"(use host:port, for example \"xlx950.example.org:62030\")",
			l.cfg.Name, l.cfg.TargetAddress, err)
	}
	l.running.Store(true)
	l.log.Info("outbound link starting", "target", l.cfg.TargetAddress)
	l.redial()

	l.mu.Lock()
	out := l.link.Start()
	l.mu.Unlock()
	l.apply(out)

	go l.serve(ctx)
	return nil
}

// current returns the socket in use, nil while the far end's name has not
// resolved.
func (l *PeerLink) current() *net.UDPConn {
	l.connMu.Lock()
	defer l.connMu.Unlock()
	return l.conn
}

// redial looks the far end up and, if it is somewhere new or was never
// found, dials it there. The old socket is closed after the new one is in
// place, and the reader notices the change and carries on with the new one.
func (l *PeerLink) redial() {
	dest, err := l.cfg.Resolve(l.cfg.TargetAddress)

	l.connMu.Lock()
	l.resolved = l.now()
	if err != nil {
		first := !l.lookupFailing
		l.lookupFailing = true
		l.connMu.Unlock()
		if first {
			l.log.Warn("the far end's address cannot be looked up; the link keeps trying",
				"target", l.cfg.TargetAddress, "error", err)
		}
		return
	}
	wasFailing := l.lookupFailing
	l.lookupFailing = false
	if l.conn != nil && sameAddr(l.remote, dest) {
		l.connMu.Unlock()
		return
	}
	conn, err := net.DialUDP("udp", nil, dest)
	if err != nil {
		l.connMu.Unlock()
		l.log.Warn("cannot open a socket to the far end", "target", dest.String(), "error", err)
		return
	}
	old, moved := l.conn, l.remote
	l.conn, l.remote = conn, dest
	l.connMu.Unlock()

	switch {
	case old != nil:
		_ = old.Close()
		l.log.Info("the far end's address changed; dialling the new one",
			"was", moved.String(), "now", dest.String())
	case wasFailing:
		l.log.Info("the far end's address was found", "target", dest.String())
	}
}

// maybeRedial looks the far end up again when the link is down and the last
// lookup is old enough. A connected link is left alone: its address works.
func (l *PeerLink) maybeRedial() {
	l.connMu.Lock()
	have := l.conn != nil
	due := l.now().Sub(l.resolved) >= l.cfg.ResolveInterval
	l.connMu.Unlock()
	if !due || (have && isLiteral(l.cfg.TargetAddress)) {
		return
	}
	if have && l.State() == homebrew.StateConnected {
		return
	}
	l.redial()
}

// Close stops the link, telling the far end first.
//
// Saying so matters: a master that is told stops holding the registration and
// frees the ID immediately, rather than waiting out a timeout during which a
// restart of QSP would be refused for logging in twice.
func (l *PeerLink) Close() error {
	if !l.running.Swap(false) {
		return nil
	}
	l.mu.Lock()
	out := l.link.Close()
	l.mu.Unlock()
	l.apply(out)

	if conn := l.current(); conn != nil {
		return conn.Close()
	}
	return nil
}

// Send writes a frame to the far end.
//
// A frame offered while the link is not connected is dropped rather than
// queued, which is the state machine's rule and not this type's: one held
// through a reconnection arrives after the transmission it belonged to has
// ended.
func (l *PeerLink) Send(frame hbp.Data) error {
	if !l.running.Load() {
		return fmt.Errorf("upstream %q: the link is not running", l.cfg.Name)
	}

	l.mu.Lock()
	payload := l.link.Send(frame)
	if payload == nil {
		state := l.link.State()
		l.mu.Unlock()
		return fmt.Errorf("upstream %q: not connected (%s)", l.cfg.Name, state)
	}
	l.mu.Unlock()

	conn := l.current()
	if conn == nil {
		return fmt.Errorf("upstream %q: the far end's address has not been found yet", l.cfg.Name)
	}
	if _, err := conn.Write(payload); err != nil {
		l.mu.Lock()
		l.stats.SendErrors++
		l.mu.Unlock()
		return fmt.Errorf("upstream %q: %w", l.cfg.Name, err)
	}
	l.mu.Lock()
	l.stats.Sent++
	l.mu.Unlock()
	return nil
}

// apply writes whatever the state machine decided to send, and logs what it
// decided to say.
func (l *PeerLink) apply(out homebrew.Outcome) {
	for _, payload := range out.Send {
		conn := l.current()
		if conn == nil {
			break
		}
		if _, err := conn.Write(payload); err != nil {
			l.mu.Lock()
			l.stats.SendErrors++
			l.mu.Unlock()
			if !l.writeFailing.Swap(true) {
				l.log.Warn("cannot write to the far end", "error", err)
			}
			return
		}
	}
	// Transitions are logged, steady state is not. A link that is working
	// should be silent; one that is not should say so once per change rather
	// than once per tick.
	if out.Changed && out.Note != "" {
		l.log.Info("link state changed", "detail", out.Note, "state", l.State())
	} else if out.Note != "" {
		l.log.Debug("link", "detail", out.Note)
	}
}

// State returns the handshake state, for logging and health.
func (l *PeerLink) State() homebrew.State {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.link.State()
}

// serve reads datagrams and drives the clock until the context ends.
func (l *PeerLink) serve(ctx context.Context) {
	defer l.running.Store(false)

	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()

	// The ticker runs on its own goroutine and serialises through the same
	// mutex as the reader, so the state machine still sees one caller at a
	// time.
	ticker := time.NewTicker(l.cfg.TickInterval)
	defer ticker.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !l.running.Load() {
					return
				}
				l.mu.Lock()
				out := l.link.Tick()
				l.mu.Unlock()
				l.apply(out)
				l.maybeRedial()
			}
		}
	}()

	buf := make([]byte, 1024)
	for {
		conn := l.current()
		if conn == nil {
			// The far end's name has not resolved yet; the ticker is looking.
			select {
			case <-ctx.Done():
				l.log.Info("outbound link closed")
				return
			case <-time.After(l.cfg.TickInterval):
			}
			if !l.running.Load() {
				l.log.Info("outbound link closed")
				return
			}
			continue
		}
		n, err := conn.Read(buf)
		if err != nil {
			if ctx.Err() != nil || !l.running.Load() {
				l.log.Info("outbound link closed")
				return
			}
			if conn != l.current() {
				// The far end moved and redial replaced the socket.
				continue
			}
			if errors.Is(err, net.ErrClosed) {
				l.log.Info("outbound link closed")
				return
			}
			// **A failed read is the far end blinking, not the end of the
			// link.** On a connected UDP socket, "connection refused" is one
			// datagram bounced because nothing was listening for that moment
			// — a restart on the far side. Returning here ended the link for
			// good, silently, until QSP restarted: it happened to the BCARA
			// link on 2026-09-16. The state machine's own timeout moves a
			// silent link to backoff and logs in again; it only needs this
			// loop to still be reading when the far end comes back.
			if first := l.noteUnreachable(err); first {
				l.log.Warn("the far end is not answering; the link keeps trying", "error", err)
			}
			select {
			case <-ctx.Done():
				l.log.Info("outbound link closed")
				return
			case <-time.After(ReadRetryDelay):
			}
			continue
		}
		if l.clearUnreachable() {
			l.log.Info("the far end is answering again")
		}
		// **An outage ends when the far end is heard, not when a write
		// succeeds.** To a port with nothing listening, UDP writes alternate:
		// one leaves, its bounce fails the next. Resetting on a successful
		// write logged every other keepalive.
		l.writeFailing.Store(false)

		l.mu.Lock()
		out := l.link.Handle(buf[:n])
		if out.Data != nil {
			l.stats.Received++
			l.lastReceived = l.now()
		}
		l.mu.Unlock()

		l.apply(out)
		if out.Data != nil {
			l.cfg.Receive(l.cfg.Name, *out.Data)
		}
	}
}

// ReadRetryDelay is how long a link waits after a failed read before reading
// again. Long enough that a refused socket does not spin, short enough that a
// far end which is back is heard within a keepalive.
const ReadRetryDelay = time.Second

// noteUnreachable records a failed read and reports whether it began an
// outage, so the outage is logged once rather than once a second.
func (l *PeerLink) noteUnreachable(err error) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	first := l.unreachable == ""
	l.unreachable = err.Error()
	return first
}

// clearUnreachable ends an outage and reports whether there was one.
func (l *PeerLink) clearUnreachable() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	was := l.unreachable != ""
	l.unreachable = ""
	return was
}

// Status implements Connection.
//
// Unlike OpenBridge, a homebrew link has a keep-alive, so QSP genuinely knows
// whether the far end is answering. That is worth saying differently: silence
// here is a fault rather than the ambiguity OpenBridge leaves.
func (l *PeerLink) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()

	state := l.link.State()
	st := Status{
		Name:         l.cfg.Name,
		Open:         l.running.Load(),
		EverReceived: !l.lastReceived.IsZero(),
		Stats:        l.stats,
	}
	// What the far end announced when it accepted this registration. Nothing
	// else in the handshake carries it: a configuration goes one way and the
	// answer is four bytes and an ID.
	if far := l.link.FarEnd(); far.Known {
		st.FarEndNetwork = far.Network
		st.FarEndCallsign = far.Callsign
		st.FarEndSoftware = far.Software
		st.FarEndLocation = far.Location
		st.FarEndLatitude, st.FarEndLongitude, st.FarEndLocated = far.Latitude, far.Longitude, far.Located
	}
	if st.EverReceived {
		st.Since = l.now().Sub(l.lastReceived).Truncate(time.Second)
	}

	switch {
	case !st.Open:
		st.Summary = "the link is not open"
	case l.unreachable != "" && state != homebrew.StateConnected:
		st.Summary = fmt.Sprintf("the far end is not answering (%s); retrying", l.unreachable)
		st.Advice = "nothing is listening at the far end's address right now — usually its " +
			"server restarting; this link logs in again by itself when it answers"
	case state == homebrew.StateConnected:
		if st.EverReceived {
			st.Summary = fmt.Sprintf("connected; last traffic %s ago", st.Since)
		} else {
			st.Summary = "connected; no traffic yet"
		}
	case !l.link.EverConnected():
		// The distinction the state machine keeps, surfaced. A link that has
		// never worked is usually a credential or an address; one that has
		// worked and stopped is usually the far end.
		st.Summary = fmt.Sprintf("has never connected (%s)", state)
		st.Advice = "check the repeater ID, the password file and the address; a link that " +
			"has never completed a handshake is almost always one of those three"
	default:
		st.Summary = fmt.Sprintf("disconnected (%s)", state)
		st.Advice = "this link has connected before, so the far end or the network between " +
			"is the likelier cause than anything configured here; it retries on its own"
	}
	return st
}
