package v24link

import (
	"cmp"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/p25calls"
	"github.com/k9mls/qsp/internal/p25link"
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

// RequestInterval is how often QSP asks the station to open the link until it
// does. It is the station's own interval, measured at 0.51 seconds.
const RequestInterval = 500 * time.Millisecond

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
	// PresentAs names the Identity QSP presents: "repeater" or "console".
	// Empty is "repeater".
	PresentAs string
	// Site is the site number QSP introduces itself with. Zero is the
	// identity's own default.
	Site uint8
	// Request is how often QSP asks the station to open the link until it
	// does. Zero is RequestInterval.
	Request time.Duration
	// Keepalive is how often Receive Ready is sent on an open link. Zero is
	// KeepaliveInterval.
	Keepalive time.Duration
	// Gateways, when set, receives every voice frame a repeater's call
	// carries, and the end of the call. Nil relays between repeaters only.
	Gateways Sink
	// SendHeader sends the captured call header, which says talkgroup 1,
	// ahead of a gateway's call. Off, a call is sent with a start marker and
	// no header.
	SendHeader bool
	// Calls, when set, is told of every transmission a repeater makes, for
	// Last heard and the record. Nil keeps none.
	Calls *p25calls.Tracker
	// Floor is shared with the gateway listener so that one call at a time
	// crosses between the two. Nil is a floor of this listener's own, so
	// repeaters still take turns among themselves.
	Floor *p25link.Floor
	// Now is the clock; nil uses time.Now.
	Now func() time.Time
}

// Sink is where a repeater's voice goes besides other repeaters: the P25
// gateway listener, which takes the frames as they are.
type Sink interface {
	// FromRepeater carries one voice frame, from its type byte on.
	FromRepeater(frame []byte) int
	// EndFromRepeater says the transmission is over.
	EndFromRepeater() int
}

// Validate reports why a configuration cannot be used.
func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.ListenAddress); err != nil {
		return fmt.Errorf("v24link: %q is not a host:port address: %w", c.ListenAddress, err)
	}
	for _, r := range c.AllowedRouters {
		if net.ParseIP(strings.TrimSpace(r)) == nil {
			return fmt.Errorf("v24link: allowed router %q is not an address", r)
		}
	}
	if _, ok := IdentityNamed(c.PresentAs); !ok {
		return fmt.Errorf("v24link: %q is not \"repeater\" or \"console\"", c.PresentAs)
	}
	if c.Site > MaxSite {
		return fmt.Errorf("v24link: site %d is beyond %d, the most an introduction can carry",
			c.Site, MaxSite)
	}
	return nil
}

// Listener accepts the router's tunnel and answers the station.
type Listener struct {
	log *slog.Logger
	cfg Config
	id  Identity
	now func() time.Time

	allowed map[string]bool

	mu sync.Mutex
	ln net.Listener
	// **One tunnel is one repeater, and a router may carry several.** A
	// router with two serial ports dials twice from one address, so tunnels
	// are numbered as they arrive and not named for the router. 0.1.303 to
	// 0.1.306 kept one per router address, and a second repeater on the same
	// router would have closed the first.
	nextID   uint64
	conns    map[uint64]net.Conn
	stations map[uint64]*station
	floor    *p25link.Floor

	running atomic.Bool
	up      atomic.Int64
	voice   atomic.Uint64
	calls   atomic.Uint64
	relayed atomic.Uint64
	held    atomic.Uint64
	sent    atomic.Uint64

	// inbound serialises a gateway's call on its way to the repeaters, so a
	// start, the voice and an end cannot interleave. inboundLast is when its
	// last frame came, and zero when no call is open.
	inbound     sync.Mutex
	inboundLast time.Time
	answered    atomic.Uint64
	unknown     atomic.Uint64
	refused     atomic.Uint64
	done        sync.WaitGroup
}

// New builds a listener. It binds nothing until Start.
func New(log *slog.Logger, cfg Config) (*Listener, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	l := &Listener{
		log:      log,
		cfg:      cfg,
		now:      cfg.Now,
		allowed:  make(map[string]bool, len(cfg.AllowedRouters)),
		conns:    make(map[uint64]net.Conn),
		stations: make(map[uint64]*station),
		floor:    cfg.Floor,
	}
	if l.now == nil {
		l.now = time.Now
	}
	l.id, _ = IdentityNamed(cfg.PresentAs)
	if l.cfg.Site == 0 {
		l.cfg.Site = l.id.DefaultSite
	}
	if l.floor == nil {
		l.floor = &p25link.Floor{}
	}
	if l.cfg.Request <= 0 {
		l.cfg.Request = RequestInterval
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

// Repeater is one repeater as the console sees it.
type Repeater struct {
	// Router is the address of the router carrying its serial line.
	Router string
	// Connected is when the router's tunnel connected.
	Connected time.Time
	// Site and StationType are what the repeater said in its introduction.
	// Introduced is false, and both zero, until it has.
	Introduced  bool
	Site        uint8
	StationType byte
	// Up reports an open link, and UpSince when it opened.
	Up      bool
	UpSince time.Time
	// Frames is voice frames heard, and Calls transmissions finished, on
	// this tunnel.
	Frames uint64
	Calls  uint64
	// Relayed is voice frames carried onward, to gateways and to other
	// repeaters. Held is transmissions not carried because another station
	// was talking when they began.
	Relayed uint64
	Held    uint64
	// Sent is voice frames from gateways sent to this repeater to transmit.
	Sent uint64
	// Transmitting reports a transmission in progress.
	Transmitting bool
	// Talkgroup, SourceID and LastHeard are the last transmission that said
	// who it was, in progress or finished. Zero until one has.
	Talkgroup uint16
	SourceID  uint32
	LastHeard time.Time
}

// StationTypeName names the type byte of an introduction. **One is known**:
// C2, which the published account gives for a Quantar and the Quantar here
// sent. Anything else is shown as the byte it is.
func StationTypeName(t byte) string {
	if t == 0xC2 {
		return "Quantar"
	}
	return fmt.Sprintf("type %02X", t)
}

// station is one tunnel's repeater: the view, and the transmission in
// progress.
type station struct {
	id     uint64
	router string
	conn   net.Conn
	// holder is this repeater's name on the floor.
	holder string
	mu     sync.Mutex
	// send writes one frame to this repeater, and is nil until its tunnel is
	// being served. Safe to call from any goroutine, without mu held.
	send func(payload []byte) error

	view Repeater
	call *Call
	// group is the tunnel's group byte, copied from the router's own frames,
	// and zero until one has arrived.
	group byte
	// relaying reports that the transmission in progress has the floor and
	// is being carried onward.
	relaying bool
	// receiving reports that this repeater has been sent the start of a
	// gateway's call and is owed its end.
	receiving bool
}

// Repeaters is every repeater with a tunnel open, ordered by router.
func (l *Listener) Repeaters() []Repeater {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Repeater, 0, len(l.stations))
	for _, st := range l.stations {
		st.mu.Lock()
		out = append(out, st.view)
		st.mu.Unlock()
	}
	slices.SortFunc(out, func(a, b Repeater) int {
		return cmp.Or(strings.Compare(a.Router, b.Router), cmp.Compare(a.Site, b.Site),
			a.Connected.Compare(b.Connected))
	})
	return out
}

// Relayed is voice frames carried onward since start, and Held transmissions
// not carried because another station was talking.
func (l *Listener) Relayed() uint64 { return l.relayed.Load() }

// Held is transmissions not carried because another station was talking.
func (l *Listener) Held() uint64 { return l.held.Load() }

// Sent is voice frames from gateways sent to repeaters since start, counted
// once for each repeater reached.
func (l *Listener) Sent() uint64 { return l.sent.Load() }

// VoiceFrames is voice frames heard from every repeater since start.
func (l *Listener) VoiceFrames() uint64 { return l.voice.Load() }

// Calls is transmissions finished since start.
func (l *Listener) Calls() uint64 { return l.calls.Load() }

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
		return fmt.Errorf("v24link: cannot listen on %s: %w", l.cfg.ListenAddress, err)
	}
	l.mu.Lock()
	l.ln = ln
	l.mu.Unlock()
	l.running.Store(true)

	l.log.Info("listening for a router's serial tunnel",
		slog.String("address", ln.Addr().String()),
		slog.Int("allowed_routers", len(l.allowed)),
		slog.String("present_as", l.id.Name),
		slog.Int("site", int(l.cfg.Site)),
		slog.Bool("recording", l.cfg.RecordDir != ""))

	l.done.Add(3)
	go l.watchInbound(ctx)
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
		l.mu.Lock()
		l.nextID++
		st := &station{
			id:     l.nextID,
			router: router,
			conn:   conn,
			holder: fmt.Sprintf("repeater %d", l.nextID),
			view:   Repeater{Router: router, Connected: l.now()},
		}
		l.conns[st.id] = conn
		l.stations[st.id] = st
		l.mu.Unlock()

		l.done.Add(1)
		go l.serve(ctx, conn, router, st)
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
func (l *Listener) serve(ctx context.Context, conn net.Conn, router string, st *station) {
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
		delete(l.conns, st.id)
		delete(l.stations, st.id)
		l.mu.Unlock()
	}()
	log.Info("a router's tunnel connected")

	// asRecord is the transmission as Last heard and the record keep it.
	asRecord := func(c *Call) p25calls.Call {
		st.mu.Lock()
		via := "Motorola repeater"
		if st.view.Introduced {
			via = fmt.Sprintf("%s, site %d", StationTypeName(st.view.StationType), st.view.Site)
		}
		st.mu.Unlock()
		return p25calls.Call{
			Started:   c.Started,
			Ended:     c.Ended,
			Source:    c.SourceID,
			Talkgroup: c.Talkgroup,
			Frames:    int(c.Frames),
			ViaKind:   p25calls.ViaRepeater,
			Via:       via,
			Carried:   c.Carried,
		}
	}
	// finish records a transmission that is over.
	finish := func(c *Call, reason p25calls.EndReason) {
		l.calls.Add(1)
		done := asRecord(c)
		done.EndReason = reason
		l.cfg.Calls.Finished(st.holder, done, c.Ended)
		closed := "by the repeater"
		if !c.Marked {
			closed = "it went quiet"
		}
		log.Info("a transmission ended",
			slog.Int("talkgroup", int(c.Talkgroup)),
			slog.Uint64("source", uint64(c.SourceID)),
			slog.Float64("seconds", c.Duration(c.Ended).Round(100*time.Millisecond).Seconds()),
			slog.Uint64("voice_frames", c.Frames),
			slog.String("closed", closed))
	}
	// expire closes a transmission whose end marker never came.
	expire := func(now time.Time) {
		st.mu.Lock()
		c := st.call
		if !c.stale(now) {
			st.mu.Unlock()
			return
		}
		c.Ended = c.last
		st.call = nil
		st.view.Transmitting = false
		st.view.Calls++
		st.mu.Unlock()
		l.endRelay(st)
		finish(c, p25calls.EndQuiet)
	}
	defer func() {
		// The tunnel is gone and so is whatever was being said through it.
		st.mu.Lock()
		c := st.call
		st.call = nil
		st.mu.Unlock()
		l.endRelay(st)
		if c != nil {
			c.Ended = c.last
			finish(c, p25calls.EndLinkClosed)
		}
	}()

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

	st.mu.Lock()
	st.send = func(payload []byte) error {
		st.mu.Lock()
		g := st.group
		st.mu.Unlock()
		return send(g, payload)
	}
	st.mu.Unlock()

	// The link, as the station's own frames report it. Guarded by mu, because
	// the timers below read it.
	var (
		link     linkState
		group    byte
		groupSet bool // the group byte is copied from the router, never set
	)
	countUp := func(was, is bool) {
		switch {
		case is && !was:
			l.up.Add(1)
		case was && !is:
			l.up.Add(-1)
		default:
			return
		}
		st.mu.Lock()
		st.view.Up = is
		if is {
			st.view.UpSince = l.now()
		}
		st.mu.Unlock()
	}
	defer func() {
		mu.Lock()
		was := link.up
		link = linkState{}
		mu.Unlock()
		countUp(was, false)
	}()

	// Two timers: QSP's own link request until the station accepts it, and
	// the keepalive once both ends have introduced themselves.
	stop := make(chan struct{})
	defer close(stop)
	l.done.Add(1)
	go func() {
		defer l.done.Done()
		request := time.NewTicker(l.cfg.Request)
		defer request.Stop()
		keepalive := time.NewTicker(l.cfg.Keepalive)
		defer keepalive.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				return
			case <-request.C:
				expire(l.now())
				mu.Lock()
				due, g := groupSet && !link.ours, group
				mu.Unlock()
				if !due {
					continue
				}
				if err := send(g, l.id.LinkRequest()); err != nil {
					return // the read loop reports the closed tunnel
				}
			case <-keepalive.C:
				mu.Lock()
				due, g := link.introduced(), group
				mu.Unlock()
				if !due {
					continue
				}
				if err := send(g, l.id.Keepalive()); err != nil {
					return
				}
			}
		}
	}()

	said := make(map[string]bool) // each step of the link is logged once
	once := func(key string) bool {
		if said[key] {
			return false
		}
		said[key] = true
		return true
	}
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
			if once(fmt.Sprintf("op %04x", uint16(f.Op))) {
				log.Info("the router sent a tunnel message that is not serial data",
					slog.String("type", fmt.Sprintf("%04x", uint16(f.Op))),
					slog.Int("bytes", len(f.Payload)),
					slog.String("payload", clip(f.Payload)))
			}
			continue
		}

		// Voice first: on an open link it is nearly every frame.
		if rec, isRecord := ReadRecord(f.Payload); isRecord && rec.Kind != RecordUnknown {
			now := l.now()
			st.mu.Lock()
			current, finished, began := heard(st.call, rec, now)
			st.call = current
			st.view.Transmitting = current != nil
			if rec.Kind == RecordVoice {
				st.view.Frames++
			}
			if finished != nil {
				st.view.Calls++
			}
			// The view keeps the last transmission that said who it was, so a
			// kerchunk too short to say does not blank the one before it.
			if c := cmp.Or(current, finished); c != nil && c.SourceID != 0 {
				st.view.Talkgroup, st.view.SourceID, st.view.LastHeard = c.Talkgroup, c.SourceID, now
			}
			st.group = f.Group
			st.mu.Unlock()
			if rec.Kind == RecordVoice {
				l.voice.Add(1)
			}
			if !l.relay(st, f.Payload, rec, began, finished != nil, now) && began {
				log.Info("a transmission was not carried: another station was talking",
					slog.String("talking", l.floor.Holder(now)))
			}
			if began {
				log.Debug("a transmission began")
			}
			if finished != nil {
				reason := p25calls.EndMarked
				if !finished.Marked {
					// A start interrupted it; its own end never came.
					reason = p25calls.EndQuiet
				}
				finish(finished, reason)
			}
			// After the relay has decided whether it is carried, so the row
			// Last heard draws says so from its first frame.
			st.mu.Lock()
			var live *Call
			if st.call != nil {
				copied := *st.call
				live = &copied
			}
			st.mu.Unlock()
			if live != nil {
				l.cfg.Calls.Heard(st.holder, asRecord(live))
			}
			continue
		}

		reply, kind := Answer(f.Payload)
		if kind == KindUnknown {
			l.unknown.Add(1)
			key := "len 0"
			if len(f.Payload) >= 2 {
				key = fmt.Sprintf("control %02x len %d", f.Payload[1], len(f.Payload))
			}
			if once(key) {
				log.Info("the repeater sent a frame QSP does not read yet",
					slog.Int("bytes", len(f.Payload)),
					slog.String("payload", clip(f.Payload)))
			}
			continue
		}

		// The state moves before anything is sent, so a timer cannot speak
		// for a link the station has just restarted.
		mu.Lock()
		was := link
		group, groupSet = f.Group, true
		next, introduce := link.after(kind)
		link = next
		mu.Unlock()
		countUp(was.up, next.up)
		st.mu.Lock()
		st.group = f.Group
		if kind == KindIntroduction {
			st.view.Introduced = true
			st.view.Site, st.view.StationType = f.Payload[3]>>1, f.Payload[4]
		}
		st.mu.Unlock()
		if kind == KindIntroduction {
			l.supersede(st, f.Group, f.Payload[3]>>1, log)
		}

		switch {
		case kind == KindLinkRequest && was.up:
			log.Warn("the repeater's link dropped: it is asking to open it again")
		case kind == KindLinkRequest && once("request"):
			log.Info("the repeater asked for a link and was answered",
				slog.String("request", clip(f.Payload)),
				slog.String("answer", clip(reply)))
		case kind == KindAcceptance && !was.ours && once("acceptance"):
			log.Info("the repeater accepted QSP's link request",
				slog.String("acceptance", clip(f.Payload)))
		case kind == KindIntroduction && once("introduction"):
			log.Info("the repeater introduced itself",
				slog.Int("site", int(f.Payload[3]>>1)),
				slog.String("type", StationTypeName(f.Payload[4])),
				slog.String("introduction", clip(f.Payload)))
		case next.up && !was.up:
			log.Info("the repeater's link is up", slog.String("keepalive", clip(f.Payload)))
		}

		var out [][]byte
		if reply != nil {
			out = append(out, reply)
		}
		if kind == KindLinkRequest {
			// Asked at once rather than on the next tick: the station gives
			// an introduction a second and a half before starting again.
			out = append(out, l.id.LinkRequest())
		}
		if introduce {
			intro := l.id.Introduction(l.cfg.Site)
			out = append(out, intro)
			if once("introduced") {
				log.Info("QSP introduced itself", slog.String("introduction", clip(intro)))
			}
		}
		for _, payload := range out {
			if err := send(f.Group, payload); err != nil {
				log.Warn("the answer could not be sent", slog.String("error", err.Error()))
				return
			}
			l.answered.Add(1)
		}
	}
}

// linkState is how far the link has come. The link is opened from both ends,
// so there is a flag for each end's request and each end's introduction.
type linkState struct {
	theirs bool // QSP accepted the station's request
	ours   bool // the station accepted QSP's
	heard  bool // the station has introduced itself
	said   bool // QSP has introduced itself
	up     bool // and the station's keepalive has been heard since
}

// introduced reports a link on which both ends have said what they are, which
// is when keepalives are owed.
func (s linkState) introduced() bool { return s.heard && s.said }

// after returns the state a station's frame leaves the link in, and whether
// QSP owes its introduction now.
//
// **QSP introduces itself only on a link open from both ends**, in answer to
// the station's introduction — whichever of the two arrives last. An
// introduction sent on a half-open link is what the station ignored.
func (s linkState) after(kind Kind) (linkState, bool) {
	switch kind {
	case KindLinkRequest:
		// The station has started again, and so does everything.
		return linkState{theirs: true}, false
	case KindAcceptance:
		if s.ours {
			return s, false
		}
		s.ours = true
		// Its introduction came first and was held; it is owed now.
		if s.theirs && s.heard && !s.said {
			s.said = true
			return s, true
		}
	case KindIntroduction:
		s.heard = true
		// Answered every time it is sent: a station repeating itself did not
		// take the last answer, and saying nothing would be a third way to
		// be ignored.
		if s.theirs && s.ours {
			s.said = true
			return s, true
		}
	case KindReceiveReady:
		if s.introduced() {
			s.up = true
		}
	}
	return s, false
}

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
	name := fmt.Sprintf("v24-%s-%s.log", opened.UTC().Format("20060102-150405.000"),
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
