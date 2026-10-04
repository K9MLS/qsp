package quantar

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// IdleTimeout ends a connection that has gone quiet. A station with no link
// asks twice a second, so half a minute of nothing is a router that has gone
// away without saying so, and a connection held for it would keep the next
// one out.
const IdleTimeout = 30 * time.Second

// writeTimeout bounds one reply. A router that will not take nine bytes in
// this long is not going to.
const writeTimeout = 5 * time.Second

// KeepaliveInterval is how often QSP sends Receive Ready on an open link. The
// published account of this interface gives the limit — a station that hears
// none for about five seconds starts again — and not the interval, so this is
// chosen to fit twice inside it with room to spare.
const KeepaliveInterval = 2 * time.Second

// maxLoggedBytes bounds how much of one frame a log line carries. The record
// file keeps all of it.
const maxLoggedBytes = 64

// Config is what the listener needs.
type Config struct {
	// ListenAddress is the TCP host:port the router's tunnel dials.
	ListenAddress string
	// AllowedRouters names the routers accepted, by address. Empty accepts
	// any.
	AllowedRouters []string
	// RecordDir, when set, receives one text file per connection holding
	// every frame in both directions. Empty records nothing.
	RecordDir string
	// Site is the site number QSP introduces itself with. Zero is
	// DefaultSite.
	Site uint8
	// Keepalive is how often Receive Ready is sent on an open link. Zero is
	// KeepaliveInterval.
	Keepalive time.Duration
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Validate reports why a configuration cannot be used.
func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.ListenAddress); err != nil {
		return fmt.Errorf("quantar: %q is not a host:port address: %w", c.ListenAddress, err)
	}
	for _, r := range c.AllowedRouters {
		if net.ParseIP(strings.TrimSpace(r)) == nil {
			return fmt.Errorf("quantar: allowed router %q is not an address", r)
		}
	}
	if c.Site > MaxSite {
		return fmt.Errorf("quantar: site %d is beyond %d, the most an introduction can carry",
			c.Site, MaxSite)
	}
	return nil
}

// Listener accepts the router's tunnel and answers the station.
type Listener struct {
	log *slog.Logger
	cfg Config
	now func() time.Time

	allowed map[string]bool

	mu    sync.Mutex
	ln    net.Listener
	conns map[string]net.Conn // by router address; one tunnel per router

	running  atomic.Bool
	up       atomic.Int64
	answered atomic.Uint64
	unknown  atomic.Uint64
	refused  atomic.Uint64
	done     sync.WaitGroup
}

// New builds a listener. It binds nothing until Start.
func New(log *slog.Logger, cfg Config) (*Listener, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	l := &Listener{
		log:     log,
		cfg:     cfg,
		now:     cfg.Now,
		allowed: make(map[string]bool, len(cfg.AllowedRouters)),
		conns:   make(map[string]net.Conn),
	}
	if l.now == nil {
		l.now = time.Now
	}
	if l.cfg.Site == 0 {
		l.cfg.Site = DefaultSite
	}
	if l.cfg.Keepalive <= 0 {
		l.cfg.Keepalive = KeepaliveInterval
	}
	for _, r := range cfg.AllowedRouters {
		l.allowed[net.ParseIP(strings.TrimSpace(r)).String()] = true
	}
	return l, nil
}

// Running reports whether the listener is accepting.
func (l *Listener) Running() bool { return l.running.Load() }

// LinksUp is how many stations have an open link now.
func (l *Listener) LinksUp() int { return int(l.up.Load()) }

// Answered counts frames answered.
func (l *Listener) Answered() uint64 { return l.answered.Load() }

// Unknown counts frames recorded and not answered.
func (l *Listener) Unknown() uint64 { return l.unknown.Load() }

// Refused counts connections turned away.
func (l *Listener) Refused() uint64 { return l.refused.Load() }

// Addr is the address bound, or nil before Start.
func (l *Listener) Addr() net.Addr {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ln == nil {
		return nil
	}
	return l.ln.Addr()
}

// Wait returns when every goroutine Start began has ended.
func (l *Listener) Wait() { l.done.Wait() }

// Start binds and returns; serving is on its own goroutine, so a port already
// in use is an error at startup and nothing after this in the daemon waits.
func (l *Listener) Start(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", l.cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("quantar: cannot listen on %s: %w", l.cfg.ListenAddress, err)
	}
	l.mu.Lock()
	l.ln = ln
	l.mu.Unlock()
	l.running.Store(true)

	l.log.Info("listening for a router's serial tunnel",
		slog.String("address", ln.Addr().String()),
		slog.Int("allowed_routers", len(l.allowed)),
		slog.Int("site", int(l.cfg.Site)),
		slog.Bool("recording", l.cfg.RecordDir != ""))

	l.done.Add(2)
	go func() {
		defer l.done.Done()
		<-ctx.Done()
		_ = ln.Close()
		l.mu.Lock()
		for _, c := range l.conns {
			_ = c.Close()
		}
		l.mu.Unlock()
	}()
	go l.accept(ctx, ln)
	return nil
}

func (l *Listener) accept(ctx context.Context, ln net.Listener) {
	defer l.done.Done()
	defer l.running.Store(false)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, net.ErrClosed) {
				l.log.Error("the tunnel listener stopped accepting", slog.String("error", err.Error()))
			}
			return
		}
		router := hostOf(conn.RemoteAddr())
		if len(l.allowed) > 0 && !l.allowed[router] {
			l.refused.Add(1)
			l.log.Warn("a connection was refused: the address is not an allowed router",
				slog.String("from", router))
			_ = conn.Close()
			continue
		}
		// A router that restarts dials again and never closes what it had.
		// The new connection is the live one, so it takes the place.
		l.mu.Lock()
		if old := l.conns[router]; old != nil {
			_ = old.Close()
		}
		l.conns[router] = conn
		l.mu.Unlock()

		l.done.Add(1)
		go l.serve(ctx, conn, router)
	}
}

func hostOf(addr net.Addr) string {
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.String()
	}
	return host
}

// serve reads one router's frames until the connection ends.
func (l *Listener) serve(ctx context.Context, conn net.Conn, router string) {
	defer l.done.Done()
	log := l.log.With(slog.String("router", router))
	opened := l.now()
	rec := l.openRecord(log, router, opened)
	defer func() {
		_ = conn.Close()
		if rec != nil {
			_ = rec.Close()
		}
		l.mu.Lock()
		if l.conns[router] == conn {
			delete(l.conns, router)
		}
		l.mu.Unlock()
	}()
	log.Info("a router's tunnel connected")

	// One lock for the socket and the record, because two things write: this
	// loop answering, and the keepalive below.
	var mu sync.Mutex
	record := func(direction string, f Frame) {
		if rec == nil {
			return
		}
		_, err := fmt.Fprintf(rec, "%.1f\t%s\t%04x\t%02x\t%s\n",
			float64(l.now().Sub(opened).Microseconds())/1000, direction,
			uint16(f.Op), f.Group, hex.EncodeToString(f.Payload))
		if err != nil {
			log.Warn("recording stopped: the file could not be written",
				slog.String("error", err.Error()))
			_ = rec.Close()
			rec = nil
		}
	}
	send := func(group byte, payload []byte) error {
		out := Frame{Op: OpData, Group: group, Payload: payload}
		mu.Lock()
		defer mu.Unlock()
		err := conn.SetWriteDeadline(time.Now().Add(writeTimeout))
		if err == nil {
			_, err = conn.Write(out.Append(nil))
		}
		if err == nil {
			record("tx", out)
		}
		return err
	}

	// The link's state, as the station's own frames report it.
	var (
		state   = linkNone
		address byte
		group   byte
	)
	setState := func(next linkState) {
		mu.Lock()
		was := state
		state = next
		mu.Unlock()
		if (was == linkUp) != (next == linkUp) {
			if next == linkUp {
				l.up.Add(1)
			} else {
				l.up.Add(-1)
			}
		}
	}
	defer setState(linkNone)

	// The keepalive. It speaks only on a link the station has introduced
	// itself on, and stops the moment the station asks again.
	stop := make(chan struct{})
	defer close(stop)
	l.done.Add(1)
	go func() {
		defer l.done.Done()
		tick := time.NewTicker(l.cfg.Keepalive)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
			}
			mu.Lock()
			open, a, g := state >= linkIntroduced, address, group
			mu.Unlock()
			if !open {
				continue
			}
			if err := send(g, Keepalive(a)); err != nil {
				return // the read loop reports the closed tunnel
			}
		}
	}()

	seen := make(map[string]bool) // one line per kind of frame, not per frame
	for {
		if err := conn.SetReadDeadline(time.Now().Add(IdleTimeout)); err != nil {
			log.Warn("the tunnel closed", slog.String("error", err.Error()))
			return
		}
		f, err := ReadFrame(conn)
		if err != nil {
			switch {
			case ctx.Err() != nil, errors.Is(err, net.ErrClosed):
				log.Info("the tunnel closed")
			case errors.Is(err, io.EOF):
				log.Info("the router closed the tunnel")
			case errors.Is(err, os.ErrDeadlineExceeded):
				log.Warn("the tunnel went quiet and was closed",
					slog.Duration("after", IdleTimeout))
			default:
				log.Warn("the tunnel was closed", slog.String("error", err.Error()))
			}
			return
		}
		mu.Lock()
		record("rx", f)
		mu.Unlock()

		if f.Op != OpData {
			if key := fmt.Sprintf("op %04x", uint16(f.Op)); !seen[key] {
				seen[key] = true
				log.Info("the router sent a tunnel message that is not serial data",
					slog.String("type", fmt.Sprintf("%04x", uint16(f.Op))),
					slog.Int("bytes", len(f.Payload)),
					slog.String("payload", clip(f.Payload)))
			}
			continue
		}

		reply, kind := Answer(f.Payload, l.cfg.Site)
		if kind == KindUnknown {
			l.unknown.Add(1)
			key := "len 0"
			if len(f.Payload) >= 2 {
				key = fmt.Sprintf("control %02x len %d", f.Payload[1], len(f.Payload))
			}
			if !seen[key] {
				seen[key] = true
				log.Info("the station sent a frame QSP does not answer yet",
					slog.Int("bytes", len(f.Payload)),
					slog.String("payload", clip(f.Payload)))
			}
			continue
		}

		// The state moves before the answer goes out, so a keepalive cannot
		// follow a link request the station has just restarted with.
		mu.Lock()
		was := state
		address, group = f.Payload[0], f.Group
		mu.Unlock()
		switch kind {
		case KindLinkRequest:
			setState(linkAccepted)
			switch {
			case was == linkUp:
				log.Warn("the link dropped: the station is asking to open it again")
			case was == linkNone:
				log.Info("the station asked for a link and was answered",
					slog.String("request", clip(f.Payload)),
					slog.String("answer", clip(reply)))
			}
		case KindIntroduction:
			if was < linkIntroduced {
				setState(linkIntroduced)
				log.Info("the station introduced itself and was answered",
					slog.Int("station_site", int(f.Payload[3]>>1)),
					slog.String("station_type", fmt.Sprintf("%02x", f.Payload[4])),
					slog.String("introduction", clip(f.Payload)),
					slog.String("answer", clip(reply)))
			}
		case KindReceiveReady:
			if was == linkIntroduced {
				setState(linkUp)
				log.Info("the Quantar's link is up",
					slog.String("keepalive", clip(f.Payload)))
			}
		}

		if reply == nil {
			continue
		}
		if err := send(f.Group, reply); err != nil {
			log.Warn("the answer could not be sent", slog.String("error", err.Error()))
			return
		}
		l.answered.Add(1)
	}
}

// linkState is how far a station has come in opening its link.
type linkState int

const (
	linkNone       linkState = iota // nothing heard, or the tunnel just opened
	linkAccepted                    // its link request was accepted
	linkIntroduced                  // it introduced itself and was answered
	linkUp                          // its keepalive has been heard since
)

func clip(b []byte) string {
	if len(b) > maxLoggedBytes {
		return hex.EncodeToString(b[:maxLoggedBytes]) + "..."
	}
	return hex.EncodeToString(b)
}

// openRecord opens this connection's record file, or returns nil. Recording is
// an instrument: failing to open one is reported and the link is served anyway.
func (l *Listener) openRecord(log *slog.Logger, router string, opened time.Time) *os.File {
	if l.cfg.RecordDir == "" {
		return nil
	}
	if err := os.MkdirAll(l.cfg.RecordDir, 0o750); err != nil {
		log.Warn("not recording: the directory could not be made", slog.String("error", err.Error()))
		return nil
	}
	name := fmt.Sprintf("quantar-%s-%s.log", opened.UTC().Format("20060102-150405.000"),
		strings.NewReplacer(":", "-", "%", "-").Replace(router))
	path := filepath.Join(l.cfg.RecordDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		log.Warn("not recording: the file could not be opened", slog.String("error", err.Error()))
		return nil
	}
	_, _ = fmt.Fprintf(f, "# opened %s\n# elapsed_ms\tdirection\ttype\tgroup\tpayload\n",
		opened.UTC().Format(time.RFC3339Nano))
	log.Info("recording every frame", slog.String("file", path))
	return f
}
