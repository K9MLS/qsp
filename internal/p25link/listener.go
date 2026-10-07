package p25link

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/p25calls"
	"github.com/k9mls/qsp/internal/protocol/p25"
)

// A P25 listener: gateways register by polling, and voice is relayed verbatim.
//
// # Built from three captures and nothing else
//
// `testdata/p25/p25-voice.pcap` gave the frame shapes.
// `testdata/p25/p25-talkgroups.pcap` gave the talkgroup and the source radio,
// across four talkgroups with one returned to.
// `testdata/p25/p25-register.pcap` gave what this file is mostly about: **there
// is no registration exchange.** A gateway announces itself with a poll and the
// far end echoes the same poll back, byte for byte. 96 out, 96 back, all
// identical, every 5.01 seconds.
//
// That is worth stating because guessing would have been expensive in the
// opposite direction: the obvious assumption is that a login exists, and a
// state machine built for one that does not exist would have been wrong in a
// way no test would have caught.
//
// # Audio is king, so frames are relayed and never rebuilt
//
// ADR-0034: a P25 call between P25 endpoints crosses QSP without a vocoder.
// This listener never looks inside a voice payload — it reads the talkgroup
// from the one frame that carries it, and passes every byte on unchanged.

// PollInterval is how often a gateway polls, measured rather than assumed.
//
// 5.01 seconds across 96 polls in `p25-register.pcap`, with no variance worth
// naming.
const PollInterval = 5 * time.Second

// MissedPollsBeforeGone is how many polls may be missed before a gateway is
// treated as gone.
//
// **Three, which is fifteen seconds.** One missed poll is a dropped datagram
// on a UDP path and says nothing; three in a row is a gateway that has stopped.
// The handover already records the failure this guards against: an interval
// guessed wrong looks right until a gateway drops an hour later and nobody can
// say why.
const MissedPollsBeforeGone = 3

// Config configures a Listener.
type Config struct {
	// ListenAddress is the UDP address to serve. Required.
	ListenAddress string
	// Callsign is what this server announces in its own polls.
	Callsign string
	// AllowedCallsigns names the gateways answered. **Empty admits every
	// gateway that knows the address**, which is the same rule and the same
	// hazard as the IPSC allow list: a poll carries a callsign a gateway
	// asserts about itself and nothing verifies it, so this list is the only
	// thing between the port and anybody who knows one.
	AllowedCallsigns []string
	// MaxGateways is the most gateways registered at once. Zero selects
	// DefaultMaxGateways.
	MaxGateways int
	// Now is the clock, for tests. Nil selects time.Now.
	Now func() time.Time
	// Repeaters, when set, is sent every voice frame of a gateway's call that
	// is carried, and its end, for the Motorola repeaters to transmit.
	Repeaters RepeaterSink
	// Calls, when set, is told of every transmission a gateway makes, for
	// Last heard and the record. Nil keeps none.
	Calls *p25calls.Tracker
	// Floor, when set, is shared with the Motorola repeater link so that one
	// call at a time crosses between the two. Nil is a listener with nothing
	// to take turns with, which behaves as it always has.
	Floor *Floor
}

// DefaultMaxGateways is how many gateways may be registered at once unless
// the configuration says otherwise. A club's P25 side is a handful; this is
// room for a large one, and small enough that sending a voice frame to all of
// them is still nothing.
const DefaultMaxGateways = 250

// Gateway is a P25 gateway that has polled.
type Gateway struct {
	// Callsign is what it announced. Asserted, never verified.
	Callsign string
	// Address is where its polls came from, which is where frames go back.
	Address *net.UDPAddr
	// FirstSeen and LastPoll bound its session.
	FirstSeen time.Time
	LastPoll  time.Time
	// Talkgroup is the last talkgroup it sent traffic on, or zero.
	Talkgroup uint16
	// SourceID is the last radio heard through it.
	SourceID uint32
	// Polls counts the gateway's registration polls, and Frames the voice
	// frames it has sent. Sent counts frames relayed to it.
	//
	// **These were one field called Received, and it counted both.**
	// `poll()` and `voice()` are separate functions with separate parse
	// paths and both ended in `Received++`, so the health report's "frame(s)
	// received" included the five-second poll — twelve a minute on an idle
	// reflector with one gateway linked, rising for as long as it stayed up
	// with nobody on the air.
	//
	// Measured on the test server on 2026-09-12 rather than reasoned about:
	// 12 in 60 seconds with the radio untouched, and a tcpdump of twenty
	// consecutive datagrams showing one length and one 5.006-second
	// interval. The second defect of that shape found in one day — a counter
	// whose name is wider than what it counts, rising when nothing is
	// happening — and the first was COLLISIONS, four hours earlier.
	Polls  uint64
	Frames uint64
	Sent   uint64

	// call is the transmission this gateway is sending now, or nil. A
	// gateway's own frames say when one begins and its terminator when it
	// ends; nothing identifies it in between, so the gateway is the key.
	call *gatewayCall
}

// gatewayCall is one transmission from a gateway, as it is being heard.
type gatewayCall struct {
	p25calls.Call
	// key names the call to the tracker, and is fixed when the call begins.
	//
	// **Not worked out again when it ends.** It has the gateway's address
	// in it, and a gateway behind a home router can come from a new port
	// between two polls. A call begun under one address and finished under
	// another was finished under a name the tracker had never heard, and
	// the row it did have stayed "in progress" in Last heard until QSP was
	// restarted (found 2026-10-07, D7).
	key  string
	last time.Time
	// toRepeaters is frames sent on to Motorola repeaters, counted once for
	// each repeater reached, and held frames not carried at all.
	toRepeaters int
	held        int
}

// Listener serves P25 gateways.
type Listener struct {
	cfg Config
	log *slog.Logger
	now func() time.Time

	conn    *net.UDPConn
	running atomic.Bool

	mu       sync.Mutex
	gateways map[string]*Gateway

	// snapshot holds an immutable list for readers on other goroutines, for
	// the reason internal/peers and the IPSC listener both do it: the serve
	// loop owns the map and an HTTP handler reaching into it would be a race.
	snapshot atomic.Pointer[[]Gateway]

	// allowed is replaced rather than mutated, so an operator can change it
	// without a restart and without a lock on the hot path.
	allowed atomic.Pointer[map[string]bool]

	// refused counts polls from callsigns not on the allow list, and names
	// the most recent — because a lifetime counter cannot answer the question
	// an operator actually has, which the IPSC listener learned on 2026-09-02.
	refused         atomic.Uint64
	refusedCallsign atomic.Pointer[string]
	// fullSaid is when the log last said the registry was full, so a flood
	// of registrations is one line a minute and not one a datagram.
	fullSaid time.Time

	// unparsed counts datagrams this build does not recognise. Expected to be
	// non-zero over time: three captures are not the whole protocol.
	unparsed atomic.Uint64

	// floor is Config.Floor: shared with the Motorola repeater link when
	// there is one, and nil otherwise. held counts voice frames not carried
	// because a repeater had it.
	floor *Floor
	held  atomic.Uint64
}

// RepeaterSink is where a gateway's call goes besides other gateways: the
// Motorola repeater link.
type RepeaterSink interface {
	// FromGateway carries one voice frame, as it arrived.
	FromGateway(frame []byte) int
	// EndFromGateway says the transmission is over.
	EndFromGateway() int
}

// Held counts voice frames from gateways not carried because a Motorola
// repeater was talking.
func (l *Listener) Held() uint64 { return l.held.Load() }

// terminator is the frame that ends a transmission on this protocol: its type
// byte and sixteen zeroes, in all seven transmissions captured.
var terminator = append([]byte{byte(p25.KindTerminator)}, make([]byte, 16)...)

// FromRepeater sends one voice frame heard from a Motorola repeater to every
// registered gateway, and reports how many it reached.
//
// **The frame is the repeater's own bytes.** A repeater's voice record and
// this protocol's voice frame are the same thing — this protocol was made by
// putting those records in datagrams — so nothing is converted and nothing is
// decoded (ADR-0034). Anything that is not a voice frame is refused: a
// gateway has no use for a repeater's header or markers.
func (l *Listener) FromRepeater(raw []byte) int {
	frame, err := p25.Parse(raw)
	if err != nil || !frame.Voice() {
		return 0
	}
	return l.toGateways(raw)
}

// EndFromRepeater tells every gateway the repeater's transmission is over.
// A repeater closes a call with a marker of its own, which a gateway would
// not recognise, so the gateway is sent the terminator it expects.
func (l *Listener) EndFromRepeater() int { return l.toGateways(terminator) }

func (l *Listener) toGateways(raw []byte) int {
	if l.conn == nil {
		return 0
	}
	l.mu.Lock()
	targets := make([]*Gateway, 0, len(l.gateways))
	for _, g := range l.gateways {
		if g.Address != nil {
			targets = append(targets, g)
		}
	}
	l.mu.Unlock()

	sent := 0
	for _, g := range targets {
		if _, err := l.conn.WriteToUDP(raw, g.Address); err != nil {
			l.log.Warn("cannot relay a repeater's frame", "to", g.Callsign, "error", err.Error())
			continue
		}
		sent++
		l.mu.Lock()
		g.Sent++
		l.mu.Unlock()
	}
	if sent > 0 {
		l.mu.Lock()
		l.publish()
		l.mu.Unlock()
	}
	return sent
}

// Validate reports whether a configuration can be served.
func (c Config) Validate() error {
	if c.ListenAddress == "" {
		return errors.New("p25link: a listen address is required")
	}
	if _, err := net.ResolveUDPAddr("udp", c.ListenAddress); err != nil {
		return fmt.Errorf("p25link: listen address %q: %w", c.ListenAddress, err)
	}
	return nil
}

// New constructs a Listener.
func New(log *slog.Logger, cfg Config) (*Listener, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	l := &Listener{
		cfg:      cfg,
		log:      logging.Subsystem(log, "p25"),
		now:      now,
		gateways: make(map[string]*Gateway),
		floor:    cfg.Floor,
	}
	l.SetAllowedCallsigns(cfg.AllowedCallsigns)
	empty := []Gateway{}
	l.snapshot.Store(&empty)
	return l, nil
}

// SetAllowedCallsigns replaces the allow list.
func (l *Listener) SetAllowedCallsigns(list []string) {
	set := make(map[string]bool, len(list))
	for _, c := range list {
		if c = normalise(c); c != "" {
			set[c] = true
		}
	}
	l.allowed.Store(&set)
}

// normalise folds a callsign for comparison.
//
// **Upper case, trimmed.** A gateway announcing `k9mls` and an allow list
// holding `K9MLS` are the same station, and a listener that disagreed would
// refuse somebody for their shift key. This is the defect 0301 shipped one
// layer up — a link whose name was not lowercase could not be sent to — and it
// is the same mistake in a different place.
func normalise(callsign string) string {
	out := make([]byte, 0, len(callsign))
	for i := 0; i < len(callsign); i++ {
		c := callsign[i]
		switch {
		case c >= 'a' && c <= 'z':
			out = append(out, c-32)
		case c > ' ' && c < 0x7F:
			out = append(out, c)
		}
	}
	return string(out)
}

// Gateways returns the gateways currently known.
func (l *Listener) Gateways() []Gateway {
	if s := l.snapshot.Load(); s != nil {
		return *s
	}
	return nil
}

// Refused reports how many polls were turned away, and the most recent
// callsign.
func (l *Listener) Refused() (uint64, string) {
	var who string
	if p := l.refusedCallsign.Load(); p != nil {
		who = *p
	}
	return l.refused.Load(), who
}

// Unparsed reports datagrams this build did not recognise.
func (l *Listener) Unparsed() uint64 { return l.unparsed.Load() }

// Running reports whether the listener is serving.
func (l *Listener) Running() bool { return l.running.Load() }
