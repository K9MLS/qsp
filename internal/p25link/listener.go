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
	// Now is the clock, for tests. Nil selects time.Now.
	Now func() time.Time
}

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
	// Received and Sent count frames each way.
	Received uint64
	Sent     uint64
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

	// unparsed counts datagrams this build does not recognise. Expected to be
	// non-zero over time: three captures are not the whole protocol.
	unparsed atomic.Uint64
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
