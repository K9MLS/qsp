// Package homebrew implements the peer side of the HBP handshake: logging into
// somebody else's master rather than accepting connections.
//
// QSP has spoken the master side since phase 1. This is the same conversation
// from the other end — RPTL, RPTK with the digest, RPTC with the station's
// configuration, then RPTPING for as long as the link lasts — and it is what
// reaches XLX, DMR+, IPSC2 and another QSP. See
// docs/adr/ADR-0024-outbound-peer-mode.md.
//
// **It must not be pointed at BrandMeister**, whose operators define peer
// bridging as prohibited and ask specifically that nobody build software
// without an onboard radio that impersonates these protocols. ADR-0018 records
// that decision and it is unchanged by this package existing. QSP does not
// enforce it; the operator is responsible, as they are for every other
// network's terms.
//
// # Shape
//
// Link.Handle and Link.Tick are pure functions of a datagram, the current time,
// and the link's state. They perform no I/O and own no sockets, which is what
// makes reconnection, backoff and every timeout testable without a network. A
// transport drives them, exactly as one drives peers.Master.
//
// **One exception, stated rather than hidden**: the retry delay is jittered,
// so the length of a backoff is random within a fifth of its nominal value.
// Ten links that lost one hub must not all return on the same tick. Every
// other output remains a function of the inputs, and a test asserts the range
// rather than the value.
package homebrew

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strconv"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Defaults for Config.
const (
	// DefaultKeepalive is how often RPTPING is sent once connected. Ten
	// seconds is the interval observed from real hotspots, and matching it
	// means a master's timeout tuning works for QSP too.
	DefaultKeepalive = 10 * time.Second

	// DefaultTimeout is how long the link waits for any answer before deciding
	// the far end has gone. Five missed keepalives, matching the tolerance
	// QSP's own master extends to its peers.
	DefaultTimeout = 60 * time.Second

	// DefaultMinBackoff and DefaultMaxBackoff bound reconnection (ADR-0051).
	//
	// The first retry is quick, because the common failure is a restart at the
	// far end and waiting a minute for that is needless dead air. It then backs
	// off, because a link retrying every second against a master that is down
	// for a day is a small denial of service on somebody else.
	//
	// **Two minutes rather than five.** A server goes down for maintenance and
	// comes back hours later, and the cap decides how long the network stays
	// holed after it does. Five minutes of silence following a four-hour
	// outage is four hours and five minutes to anybody listening; the saving
	// over two minutes is 24 packets an hour against a host that is not there.
	// At ten servers a link nobody is watching has to close itself quickly.
	DefaultMinBackoff = 5 * time.Second
	DefaultMaxBackoff = 2 * time.Minute
)

// State is where the link is in the handshake.
type State string

// Link states.
const (
	// StateIdle means nothing has been attempted yet.
	StateIdle State = "idle"
	// StateLoggingIn means RPTL was sent and a salt is awaited.
	StateLoggingIn State = "logging-in"
	// StateAuthenticating means the digest was sent and an ack is awaited.
	StateAuthenticating State = "authenticating"
	// StateConfiguring means RPTC was sent and an ack is awaited.
	StateConfiguring State = "configuring"
	// StateConnected means the far end has accepted the link.
	StateConnected State = "connected"
	// StateBackoff means the link failed and is waiting to retry.
	StateBackoff State = "backoff"
)

// CanSend reports whether traffic may be sent in this state.
func (s State) CanSend() bool { return s == StateConnected }

// Identity is what the link tells the far end about itself.
//
// **It is not decoration.** A master shows these fields to its own users, and a
// blank callsign appears there as an unidentified station.
type Identity struct {
	Callsign    string
	RXFrequency uint32
	TXFrequency uint32
	ColourCode  int
	Latitude    float64
	Longitude   float64
	Height      int
	Location    string
	Description string
	URL         string
	Timeslots   int
	// SoftwareID and PackageID identify the implementation to the far end.
	// Saying what QSP actually is matters: an operator looking at an
	// unfamiliar station on their dashboard should be able to find out what it
	// is rather than guess.
	SoftwareID string
	PackageID  string
}

// Config configures a Link.
type Config struct {
	// Name identifies the link in logs and health.
	Name string
	// RepeaterID is the ID presented to the far end. Required.
	RepeaterID hbp.RepeaterID
	// Password is the login secret. Required, and never retained beyond the
	// digest it produces.
	Password []byte
	// Identity is announced in RPTC. A callsign is required.
	Identity Identity

	// Keepalive, Timeout, MinBackoff and MaxBackoff are bounded by the
	// defaults above when zero.
	Keepalive  time.Duration
	Timeout    time.Duration
	MinBackoff time.Duration
	MaxBackoff time.Duration

	// Now supplies the current time. Zero uses time.Now.
	Now func() time.Time
}

// Outcome is what the caller should do after an event.
type Outcome struct {
	// Send are datagrams to write to the far end, in order.
	Send [][]byte
	// Data is a frame received from the far end, ready for routing. Nil unless
	// one arrived.
	Data *hbp.Data
	// Changed reports whether the link's state changed, so the caller can log
	// a transition rather than a level.
	Changed bool
	// Note explains what happened, for the operator. Always set when something
	// was refused or a transition occurred.
	Note string
}

// Link is one outbound connection to another master.
//
// It is not safe for concurrent use, and is owned by the goroutine driving its
// socket — the same ownership model as peers.Master, for the same reason.
type Link struct {
	cfg Config

	state State
	// since is when the current state was entered.
	since time.Time
	// lastHeard is when anything was last received from the far end.
	lastHeard time.Time
	// lastPing is when a keepalive was last sent.
	lastPing time.Time
	// farEnd is what the far end said about itself. Empty until it does, and
	// empty forever against a QSP too old to say anything.
	farEnd FarEnd
	// salt is the challenge the far end issued.
	salt [4]byte
	// backoff is the current retry delay, doubling on each failure.
	backoff time.Duration
	// retryAt is when the next login attempt is due.
	retryAt time.Time
	// everConnected distinguishes a link that has never worked from one that
	// has dropped. They usually mean different things — a wrong password
	// against a network problem — and an operator should not have to guess
	// which.
	everConnected bool
}

// New constructs a Link.
func New(cfg Config) (*Link, error) {
	if cfg.RepeaterID == 0 {
		return nil, errors.New("homebrew: a repeater ID is required")
	}
	if len(cfg.Password) == 0 {
		return nil, errors.New("homebrew: a password is required")
	}
	if cfg.Identity.Callsign == "" {
		// The far end shows this to its users. Refusing here rather than
		// sending a blank one is the difference between a startup error and
		// appearing on somebody else's dashboard as an unidentified station.
		return nil, errors.New("homebrew: a callsign is required; the far end shows it to its users")
	}
	if cfg.Keepalive <= 0 {
		cfg.Keepalive = DefaultKeepalive
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = DefaultMinBackoff
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultMaxBackoff
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		return nil, fmt.Errorf("homebrew: max backoff %s is shorter than min backoff %s",
			cfg.MaxBackoff, cfg.MinBackoff)
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Link{cfg: cfg, state: StateIdle, backoff: cfg.MinBackoff}, nil
}

// State returns the link's current state.
func (l *Link) State() State { return l.state }

// EverConnected reports whether the link has ever completed a handshake.
func (l *Link) EverConnected() bool { return l.everConnected }

// Start begins a login attempt.
func (l *Link) Start() Outcome {
	return l.login(l.cfg.Now().UTC(), "starting")
}

func (l *Link) login(now time.Time, why string) Outcome {
	l.enter(StateLoggingIn, now)
	l.lastHeard = now
	return Outcome{
		Send:    [][]byte{hbp.Login{RepeaterID: l.cfg.RepeaterID}.Marshal()},
		Changed: true,
		Note:    fmt.Sprintf("logging in to %s: %s", l.cfg.Name, why),
	}
}

func (l *Link) enter(s State, now time.Time) {
	l.state = s
	l.since = now
}

// fail drops the link and schedules a retry.
//
// The backoff doubles rather than resetting, because the failures that need
// backing off from are the ones that repeat: a wrong password fails identically
// every time, and retrying it every five seconds forever is noise on somebody
// else's server.
func (l *Link) fail(now time.Time, why string) Outcome {
	l.enter(StateBackoff, now)
	wait := l.jittered(l.backoff)
	l.retryAt = now.Add(wait)
	note := fmt.Sprintf("%s: %s; retrying in %s", l.cfg.Name, why, wait.Round(time.Millisecond))
	if l.backoff < l.cfg.MaxBackoff {
		l.backoff *= 2
		if l.backoff > l.cfg.MaxBackoff {
			l.backoff = l.cfg.MaxBackoff
		}
	}
	return Outcome{Changed: true, Note: note}
}

// jittered spreads a retry over the last fifth of its delay.
//
// **Ten links that lost one hub would otherwise return on the same tick**, and
// keep returning together for as long as the far end stays down — a
// synchronised burst against a server that is probably restarting, which is
// the worst moment to arrive all at once. ADR-0051 asks for jitter for that
// reason.
//
// Subtracted rather than added, so the delay never exceeds the cap.
//
// **The first retry is jittered too**, which is the one that matters: ten
// links lose a hub simultaneously and all sit at the minimum, so clamping the
// result up to MinBackoff — which an earlier version did, meaning to keep a
// first retry from being instant — removed the jitter from precisely the case
// it exists for. A test caught it by asserting that forty runs produce more
// than one delay. Four fifths of five seconds is four seconds, which is not
// instant and needs no floor.
//
// Randomness is the one thing in this package that is not a pure function of
// its inputs, so it is confined to this method and the amount is small enough
// that a test asserts the range rather than the value.
func (l *Link) jittered(d time.Duration) time.Duration {
	spread := d / 5
	if spread <= 0 {
		return d
	}
	return d - time.Duration(rand.Int64N(int64(spread)))
}

// Handle processes one datagram from the far end.
//
// The datagram is treated as hostile: it is parsed and matched against the
// link's state before anything is acted on. A message that does not belong in
// the current state is dropped with an explanation rather than acted on, which
// is what stops a spoofed ack advancing the handshake.
func (l *Link) Handle(datagram []byte) Outcome {
	now := l.cfg.Now().UTC()

	msg, err := hbp.Parse(datagram)
	if err != nil {
		return Outcome{Note: fmt.Sprintf("%s: unparseable datagram: %v", l.cfg.Name, err)}
	}

	// Anything well-formed from the far end counts as it being alive, even a
	// refusal. Otherwise a master that answers every ping with a NAK would look
	// silent and be dropped for the wrong reason.
	l.lastHeard = now

	switch v := msg.(type) {
	case hbp.Ack:
		return l.handleAck(v, now)
	case hbp.Nak:
		// The far end refused. Almost always a wrong password or an ID it does
		// not know, and both fail identically on every retry.
		return l.fail(now, "refused by the far end (MSTNAK); check the password and the repeater ID")
	case hbp.MasterClose:
		return l.fail(now, "the far end closed the link (MSTCL)")
	case hbp.Pong:
		if l.state != StateConnected {
			return Outcome{Note: fmt.Sprintf("%s: keepalive answer arrived while %s", l.cfg.Name, l.state)}
		}
		return Outcome{}
	case hbp.Identity:
		// **What the far end says it is**, which nothing else in this
		// handshake carries: a configuration goes one way and the answer is
		// four bytes and an ID (ADR-0052 rule 3, as amended). Unverified, like
		// every other claim a neighbour makes, and stored so a console can show
		// this link as something better than an address.
		l.farEnd = FarEnd{
			Network:     v.Network,
			Callsign:    v.Callsign,
			Software:    v.Software,
			Description: v.Description,
			Known:       true,
		}
		return Outcome{Note: fmt.Sprintf("%s: the far end is %s", l.cfg.Name, farEndLabel(l.farEnd))}
	case hbp.Data:
		if !l.state.CanSend() {
			// A frame before the handshake finished is not routable: the link
			// is not established and the far end should not be sending yet.
			return Outcome{Note: fmt.Sprintf("%s: frame arrived while %s; ignored", l.cfg.Name, l.state)}
		}
		frame := v
		return Outcome{Data: &frame}
	default:
		return Outcome{Note: fmt.Sprintf("%s: unexpected %s while %s", l.cfg.Name, msg.Kind(), l.state)}
	}
}

func (l *Link) handleAck(ack hbp.Ack, now time.Time) Outcome {
	switch l.state {
	case StateLoggingIn:
		l.salt = ack.Salt()
		l.enter(StateAuthenticating, now)
		digest := hbp.Digest(l.salt, l.cfg.Password)
		return Outcome{
			Send:    [][]byte{hbp.Key{RepeaterID: l.cfg.RepeaterID, Digest: digest}.Marshal()},
			Changed: true,
			Note:    fmt.Sprintf("%s: challenged, answering", l.cfg.Name),
		}
	case StateAuthenticating:
		l.enter(StateConfiguring, now)
		return Outcome{
			Send:    [][]byte{l.config().Marshal()},
			Changed: true,
			Note:    fmt.Sprintf("%s: authenticated, sending configuration", l.cfg.Name),
		}
	case StateConfiguring:
		l.enter(StateConnected, now)
		l.lastPing = now
		// The backoff resets only on a *completed* handshake. Resetting it
		// earlier would let a link that authenticates and then fails at the
		// configuration step retry at the minimum delay forever.
		l.backoff = l.cfg.MinBackoff
		first := !l.everConnected
		l.everConnected = true
		note := fmt.Sprintf("%s: connected", l.cfg.Name)
		if !first {
			note = fmt.Sprintf("%s: reconnected", l.cfg.Name)
		}
		return Outcome{Changed: true, Note: note}
	default:
		return Outcome{Note: fmt.Sprintf("%s: unexpected acknowledgement while %s", l.cfg.Name, l.state)}
	}
}

// config builds the RPTC message announcing this link.
func (l *Link) config() hbp.Config {
	id := l.cfg.Identity
	slots := id.Timeslots
	if slots == 0 {
		slots = 2
	}
	software := id.SoftwareID
	if software == "" {
		software = "QSP"
	}
	pkg := id.PackageID
	if pkg == "" {
		pkg = "QSP"
	}
	return hbp.Config{
		RepeaterID:  l.cfg.RepeaterID,
		Callsign:    id.Callsign,
		RXFreq:      strconv.FormatUint(uint64(id.RXFrequency), 10),
		TXFreq:      strconv.FormatUint(uint64(id.TXFrequency), 10),
		TXPower:     "0",
		ColorCode:   strconv.Itoa(id.ColourCode),
		Latitude:    strconv.FormatFloat(id.Latitude, 'f', 4, 64),
		Longitude:   strconv.FormatFloat(id.Longitude, 'f', 4, 64),
		Height:      strconv.Itoa(id.Height),
		Location:    id.Location,
		Description: id.Description,
		Slots:       strconv.Itoa(slots),
		URL:         id.URL,
		SoftwareID:  software,
		PackageID:   pkg,
	}
}

// Tick advances time: it sends keepalives, gives up on a silent far end, and
// retries after a backoff.
//
// It is separate from Handle because the events it produces are the ones that
// happen when *nothing* arrives, which is exactly the case a link has to get
// right and the one a test driven only by incoming datagrams never reaches.
func (l *Link) Tick() Outcome {
	now := l.cfg.Now().UTC()

	switch l.state {
	case StateIdle:
		return Outcome{}

	case StateBackoff:
		if now.Before(l.retryAt) {
			return Outcome{}
		}
		return l.login(now, "retrying")

	case StateConnected:
		if now.Sub(l.lastHeard) > l.cfg.Timeout {
			return l.fail(now, fmt.Sprintf("no answer for %s", now.Sub(l.lastHeard).Truncate(time.Second)))
		}
		if now.Sub(l.lastPing) < l.cfg.Keepalive {
			return Outcome{}
		}
		l.lastPing = now
		return Outcome{Send: [][]byte{hbp.Ping{RepeaterID: l.cfg.RepeaterID}.Marshal()}}

	default:
		// Mid-handshake. A master that stops answering partway through leaves
		// the link stuck otherwise, which looks identical to a link that is
		// simply slow to connect.
		if now.Sub(l.since) > l.cfg.Timeout {
			return l.fail(now, fmt.Sprintf("no answer while %s", l.state))
		}
		return Outcome{}
	}
}

// Send prepares a frame for the far end.
//
// It returns nil when the link is not connected rather than queueing. A frame
// held while a link reconnects would arrive seconds late, out of order, and
// after the transmission it belonged to had ended — worse than not arriving.
func (l *Link) Send(frame hbp.Data) []byte {
	if !l.state.CanSend() {
		return nil
	}
	// The repeater ID names this link to the far end, which registered it.
	// Leaving the originating peer's ID in place would announce a station the
	// far end has never heard of.
	out := frame
	out.RepeaterID = l.cfg.RepeaterID
	return out.Marshal()
}

// Close asks the far end to drop the link.
func (l *Link) Close() Outcome {
	if l.state == StateIdle {
		return Outcome{}
	}
	payload := hbp.RepeaterClose{RepeaterID: l.cfg.RepeaterID}.Marshal()
	l.enter(StateIdle, l.cfg.Now().UTC())
	return Outcome{
		Send:    [][]byte{payload},
		Changed: true,
		Note:    fmt.Sprintf("%s: closing", l.cfg.Name),
	}
}

// FarEnd is what the server at the other end of this link says it is.
//
// **Every field is a claim.** ADR-0052 rule 4 records that anything a neighbour
// announces may be false, and nothing here is verified — it is displayed, and
// it decides nothing about what this server carries.
type FarEnd struct {
	// Network is the far end's display name (ADR-0053), which is what both
	// consoles head this link with.
	Network string
	// Callsign is the operator's callsign there.
	Callsign string
	// Software is what it says it runs, verbatim.
	Software string
	// Description is free text about the server.
	Description string
	// Known reports whether the far end has said anything at all.
	//
	// **False is not empty.** A link to an older QSP never receives an identity
	// and a link to a new one may announce blank fields; a console that cannot
	// tell those apart says "unknown" where it should say "not announced".
	Known bool
}

// FarEnd reports what the far end said about itself, if anything.
func (l *Link) FarEnd() FarEnd { return l.farEnd }

// farEndLabel is the shortest true description of a far end, for a log line.
func farEndLabel(f FarEnd) string {
	switch {
	case f.Network != "" && f.Callsign != "":
		return f.Network + " (" + f.Callsign + ")"
	case f.Network != "":
		return f.Network
	case f.Callsign != "":
		return f.Callsign
	default:
		return "a QSP server that announced no name"
	}
}
