// Package ipsclink serves Motorola IP Site Connect peers.
//
// # What this is, honestly
//
// It answers a repeater with bytes recorded from one XPR8300 master, with the
// sender ID substituted. Nine of the eleven body bytes of the registration
// reply have no known meaning and one of them belongs to the device rather than
// the protocol. See internal/protocol/ipsc/responder.go, and
// docs/adr/ADR-0029-ipsc-from-capture.md for why it is built this way.
//
// A real XPR8300 registered against exactly these bytes on 2026-09-01, held the
// link on fifteen-second keepalives, and sent voice through it. That is the
// only evidence that any of it is right, and it is one repeater on one
// firmware.
//
// # What it does
//
// It carries audio one way: a Motorola repeater's voice is converted by
// internal/ipscbridge and handed to Config.Deliver, which routes it to Homebrew
// peers. Bridging IPSC to DMR means reconstructing a burst rather than copying
// one (docs/adr/ADR-0036), and the vocoder parameters are copied untouched
// through that reconstruction, so the audio a radio reproduces is the audio the
// originating radio encoded.
//
// # What it does not do
//
// **It never carries audio the other way.** Nothing has captured a master
// sending voice to a Motorola repeater, so QSP does not know what such a frame
// contains and will not guess at one. This is enforced by there being no path
// rather than by a rule: a Homebrew frame is delivered by resolving a
// destination in the DMR listener's peer table and writing to the DMR
// listener's socket, and an IPSC repeater is in neither.
//
// It does not produce voice headers or terminators, which need a Link Control
// checksum no capture has pinned down. A receiving radio hears the audio and
// learns who is talking through late entry.
//
// It does not authenticate. The captures contain no authenticated registration
// and no refusal of any kind, so QSP cannot yet turn a peer away in a way a
// repeater would understand — ICMP unreachable is provably ignored. Access is
// therefore a list of radio IDs that are answered, and every other peer is met
// with silence, which is the only refusal that has been observed to exist.
package ipsclink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// Config configures a Listener.
type Config struct {
	// ListenAddress is the UDP host:port to bind.
	ListenAddress string
	// MasterID is the radio ID this master announces as its own.
	//
	// **It must differ from every peer's.** A repeater refuses to register
	// with a master carrying its own ID: an XPR8300 retried thirty-nine times
	// over six minutes against a master announcing the repeater's number, with
	// replies sent promptly and ignored completely. The failure is
	// indistinguishable from a protocol fault, so Validate rejects the
	// collision rather than letting an operator discover it.
	MasterID uint32
	// AllowedPeers is the set of radio IDs answered. Empty means every peer is
	// answered, which is the right default for a bench and the wrong one for a
	// public address.
	AllowedPeers []uint32
	// PeerNames is the callsign an administrator gave each repeater, keyed by
	// radio ID. Display only: it is never consulted when deciding whether to
	// answer a datagram.
	PeerNames map[uint32]string
	// PeerTimeout is how long a registered peer may go without a keepalive
	// before it is dropped. Zero uses DefaultPeerTimeout.
	PeerTimeout time.Duration

	// Parrot records and replays on one talkgroup for Motorola repeaters.
	// Optional; nil disables it.
	//
	// **It is a separate Recorder from the Homebrew listener's**, not the same
	// one shared. Both key recordings by radio ID and the two protocols share
	// the DMR ID space: this network had one ID registered on both listeners
	// at once on 2026-09-02, a hotspot and a repeater. A shared recorder would
	// have merged their recordings and replayed one member's audio to
	// another's radio, silently. Two recorders degrade to two independent
	// parrots instead.
	Parrot *parrot.Recorder

	// Observe records each converted burst as a transmission, whatever becomes
	// of it afterwards. Optional; nil means transmissions are not recorded.
	//
	// **It is separate from Deliver because parrot consumes what it handles.**
	// On the Homebrew side a frame is observed before forwarding, so a member's
	// echo test appears in Last heard like any other over. Here parrot runs
	// before Deliver, so a burst it takes would be recorded nowhere at all —
	// and a Motorola operator keying the parrot talkgroup would leave no trace
	// while a hotspot operator doing the same left one.
	Observe func(from hbp.RepeaterID, frame hbp.Data)

	// Deliver receives each burst converted from a repeater's audio, with the
	// repeater's radio ID as its origin.
	//
	// Nil leaves the listener as it was before bridging existed: transmissions
	// are recorded and the audio goes nowhere. That is the right behaviour for
	// an instance with no Homebrew side to deliver to, and it is honest —
	// nothing claims to have carried a frame it dropped.
	Deliver func(from hbp.RepeaterID, frame hbp.Data)

	// Bridge configures the conversion from Motorola audio to DMR bursts. It
	// is used only when Deliver is set.
	Bridge ipscbridge.Config
}

// DefaultPeerTimeout is three missed keepalives at the observed fifteen-second
// cadence, plus a margin.
//
// Fifteen seconds is the *registered* cadence; an unregistered peer retries at
// ten. Timing one state by the other's clock is the mistake this constant is
// named to avoid.
const DefaultPeerTimeout = 50 * time.Second

// CallTimeout is how long a transmission may be silent before it is presumed
// lost.
//
// **It is the Homebrew listener's constant, imported rather than restated.**
// The two listeners feed one Last-heard panel, and a transmission that is over
// on one side and running on the other is a difference an operator would read
// as a fault in the network rather than in QSP.
const CallTimeout = calls.StreamTimeout

// MaxCallDuration is the longest a transmission may be reported as running,
// however many frames keep arriving.
//
// **Every radio on an amateur network has a time-out timer**, 180 seconds on
// this one, so a transmission longer than that is not a long over: it is a
// stuck record, a replay, or a defect. Four minutes leaves a full minute of
// margin, so a lawful over that runs to its time-out timer is never reported as
// a fault.
//
// It is a second mechanism and it is meant to be dead weight. CallTimeout ends
// every transmission that stops; this one ends the transmission that never
// stops, which is the failure that put seven hours in front of an operator, and
// it is keyed on a different field so that the two cannot fail together.
const MaxCallDuration = 4 * time.Minute

// CallSweepInterval is how often the sweep runs.
//
// **A sweep is a deadline, not a cadence.** It ran at PeerTimeout/4 — twelve and
// a half seconds — which is fine for dropping a repeater that unplugged and far
// too slow for a two-second stream timeout. It also gates parrot playback for a
// recording that ended in silence, which waited the same twelve seconds.
const CallSweepInterval = 500 * time.Millisecond

// Peer is a repeater the listener has answered.
type Peer struct {
	// RadioID is the peer's own ID, from the envelope of every message.
	RadioID uint32
	// Address is where its datagrams come from. **Not where it says it is**:
	// a peer sources from a different port than the one it addresses, and
	// replies go to the source or they go nowhere.
	Address string
	// FirstHeard is when this listener first heard from the peer.
	//
	// **It is not the same as Registered, and the difference is the point.** A
	// peer record is created by any datagram, so a repeater that registered
	// with a previous process and simply kept sending keepalives is known,
	// answered and passing traffic without this instance ever having seen its
	// registration. Registered stays zero for it; this does not.
	//
	// The console shows how long a peer has been in contact, which is what an
	// operator reads "connected for" as. Reporting only what QSP witnessed a
	// registration for left a dash beside a repeater that had been carrying
	// audio for hours.
	FirstHeard time.Time
	// Registered is when the peer's registration was answered.
	Registered time.Time
	// LastHeard is the arrival time of its most recent message of any kind.
	LastHeard time.Time
	// Keepalives counts answered keepalives, so an operator can tell a peer
	// that just arrived from one that has been up for hours.
	Keepalives uint64
	// VoiceFrames counts voice frames received from this peer.
	VoiceFrames uint64
	// TextFrames counts text datagrams received from this peer.
	//
	// **Separate from VoiceFrames rather than added into it.** That counter is
	// a documented figure meaning audio, the console draws it as "voice
	// frames", and a network whose text works and whose audio does not would
	// have read as healthy. A text is also many datagrams for one message, so
	// summing the two would make a single text look like a long over.
	TextFrames uint64
	// LastCall describes the most recent transmission, if there was one.
	LastCall *Call
	// ColourCode is the DMR colour code this repeater uses, learned from the
	// frames it sends. ColourCodeKnown says whether it has been learned.
	//
	// **It is per repeater.** ipsc-two-peers.pcap has two peers transmitting
	// at the same moment on colour codes 1 and 4, so a master that signs
	// every outbound frame with one number is signing wrongly for one of
	// them. QSP never filters on a colour code; it mirrors each repeater's
	// own back to it.
	ColourCode      uint8
	ColourCodeKnown bool
}

// Call is one transmission seen from a peer.
type Call struct {
	// StreamID identifies the transmission; it holds for every frame of one
	// and differs between them.
	StreamID uint16
	// Source is the transmitting radio's 24-bit ID.
	Source uint32
	// Destination is the 24-bit destination: a talkgroup, or a radio when
	// Private is set.
	Destination uint32
	// Private reports a call to one radio rather than to a talkgroup.
	Private bool
	// Timeslot is the DMR timeslot the transmission arrived on, under the
	// configured slot-bit polarity.
	//
	// **It is recorded so the polarity can be settled from the journal.**
	// Which bit value means which slot was never written down at the radio;
	// keying a known slot and reading this back is a differential, which is
	// how everything else in this protocol was settled.
	Timeslot hbp.Timeslot
	// Started and Ended bound the transmission. Ended is zero while it runs.
	Started, Ended time.Time
	// LastFrame is when the most recent frame arrived.
	//
	// **A transmission ends when its last frame arrived, not when a sweep
	// noticed.** Recording the sweep time would stretch every lost
	// transmission by up to the sweep interval, and the duration is the one
	// thing this record exists to report.
	LastFrame time.Time
	// Frames counts voice frames received from the repeater.
	Frames uint64
	// Converted counts the DMR bursts the converter produced from them, and
	// Delivered the subset handed to the DMR side.
	//
	// **Three counts of one transmission, at three layers.** The console
	// reported an IPSC transmission of 45 frames while the DMR side recorded
	// 22 for the same stream in the same second, and neither number said
	// where the other 23 went. Conversion is one for one — measured against
	// ipsc-private-voice.pcap, five transmissions, two repeater models — so
	// the loss is somewhere these counters can now name instead of somewhere
	// a reader has to guess.
	Converted, Delivered uint64
	// Lost reports that the transmission ended without a terminator.
	Lost bool
}

// endReason says why a transmission was closed, and chooses what the journal
// says about it.
//
// **The three are worth telling apart.** A terminator is a normal over. Silence
// is a lossy link or a peer that vanished, which is worth an operator's
// attention. Running past MaxCallDuration is neither: it is QSP failing to
// notice an end, and it should never appear.
type endReason int

const (
	endTerminated endReason = iota
	endSilent
	endTooLong
)

// endCall closes a peer's current transmission at the given time.
//
// The caller holds l.mu. Calling it on a peer with no call, or one already
// ended, does nothing — every caller would otherwise repeat the same guard.
func (l *Listener) endCall(p *Peer, at time.Time, reason endReason) {
	c := p.LastCall
	if c == nil || !c.Ended.IsZero() {
		return
	}
	c.Ended = at
	c.Lost = reason != endTerminated
	duration := at.Sub(c.Started).Round(time.Millisecond)
	switch reason {
	case endTerminated:
		l.log.Info("call ended", "radio_id", p.RadioID, "source", c.Source,
			"frames", c.Frames, "converted", c.Converted,
			"delivered", c.Delivered, "duration", duration)
	case endSilent:
		l.log.Warn("call ended without a terminator", "radio_id", p.RadioID,
			"source", c.Source, "frames", c.Frames, "converted", c.Converted,
			"delivered", c.Delivered, "duration", duration)
	case endTooLong:
		l.log.Warn("call exceeded the longest transmission QSP will report",
			"radio_id", p.RadioID, "source", c.Source, "frames", c.Frames,
			"duration", duration, "limit", MaxCallDuration.String())
	}
}

// ExpireCallsAt closes transmissions that stopped without a terminator, and
// reports how many.
//
// **Nothing did this until 0223.** A call ended on its last-frame flag and on
// nothing else, and a peer that keeps keepaliving is never dropped, so a
// transmission whose terminator never arrived stayed open for as long as the
// repeater stayed up. One was reported as running for seven hours and fourteen
// minutes, with a frame count of 45, on a console that also said one station was
// transmitting.
func (l *Listener) ExpireCallsAt(now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	var closed int
	for _, p := range l.peers {
		c := p.LastCall
		if c == nil || !c.Ended.IsZero() {
			continue
		}
		switch {
		case now.Sub(c.Started) > MaxCallDuration:
			l.endCall(p, now, endTooLong)
		case now.Sub(c.LastFrame) > CallTimeout:
			l.endCall(p, c.LastFrame, endSilent)
		default:
			continue
		}
		closed++
	}
	if closed > 0 {
		l.publishLocked()
	}
	return closed
}

// Listener serves IPSC peers on a UDP socket.
type Listener struct {
	cfg Config
	log *slog.Logger
	// allowed is the set of radio IDs answered, held behind an atomic pointer
	// so an operator can add or remove a repeater without a restart.
	//
	// **A map read on the serve goroutine and written by a config save is a
	// race**, and one the detector would only sometimes catch because the two
	// are genuinely concurrent. Replacing the whole map rather than mutating
	// it keeps the read side free of locks on the hot path, which is the same
	// trade snapshot makes for the peer list.
	allowed atomic.Pointer[map[uint32]bool]
	// peerNames is display only; see SetPeerNames.
	peerNames atomic.Pointer[map[uint32]string]
	// relayed is the last stream sent to each repeater, so a relayed
	// transmission is reported once rather than once per frame. Guarded by mu.
	relayed map[uint32]hbp.StreamID

	conn    *net.UDPConn
	running atomic.Bool

	mu    sync.Mutex
	peers map[uint32]*Peer
	// bridges holds one converter per peer, created when that peer first
	// sends voice. A converter carries superframe position and stream state,
	// so it belongs to the repeater it is following; sharing one between two
	// repeaters would be the same defect the converter's own slot separation
	// exists to prevent, a level up.
	bridges map[uint32]*ipscbridge.Converter
	// encoders holds one outbound encoder per peer, for voice sent to it.
	// Like the inbound converters they carry per-transmission state and so
	// belong to the repeater they are addressing.
	encoders map[uint32]*ipscbridge.Encoder

	// player replays parrot recordings to repeaters. Nil when parrot is off.
	// It shares parrot.Player with the Homebrew listener so that the sixty
	// millisecond frame timing exists in exactly one place.
	player *parrot.Player
	// ctx is the serving context, so a replay stops when the listener does
	// rather than writing to a closed socket.
	ctx context.Context

	// snapshot holds an immutable peer list for readers on other goroutines,
	// for the same reason internal/peers does it: the serve loop owns the map
	// and an HTTP handler reaching into it would be a race.
	snapshot atomic.Pointer[[]Peer]

	// ignored counts datagrams from radio IDs not in AllowedPeers, so that a
	// misconfigured repeater is visible rather than silently dropped.
	ignored atomic.Uint64
	// refused records the most recent radio ID turned away by the allow list,
	// with when it happened.
	//
	// **A lifetime counter cannot answer the question an operator has.** On
	// 2026-09-02 the health page reported 2144 datagrams from radio IDs not on
	// the allow list and named none of them, and finding out which took a
	// journal search. It was a repeater belonging to a member of the network,
	// retrying every ten seconds for hours, and the only thing standing
	// between the operator and that fact was a number with no subject.
	refusedID atomic.Uint32
	refusedAt atomic.Int64

	// refusalsMu guards refusals, which records when each repeating warning
	// was last logged so that one line stands for the ones after it. Four
	// sites use it; see shouldSay.
	//
	// **Not an atomic like the two above**, because this is a map keyed by
	// sender and message type rather than a single most-recent value.
	refusalsMu sync.Mutex
	refusals   map[string]time.Time

	// unparsed counts datagrams this build does not recognise. It is expected
	// to be non-zero: eight message types are known and IPSC has more.
	unparsed atomic.Uint64
}

// Validate reports whether a configuration can be served.
func (c Config) Validate() error {
	if c.ListenAddress == "" {
		return errors.New("ipsc: a listen address is required, for example \"0.0.0.0:50000\"")
	}
	if c.MasterID == 0 {
		return errors.New("ipsc: a master radio ID is required; a master announces one and 0 is not one")
	}
	for _, p := range c.AllowedPeers {
		if p == c.MasterID {
			return fmt.Errorf("ipsc: peer %d is also the master ID; a repeater refuses to register "+
				"with a master carrying its own ID, and the failure looks like a protocol fault", p)
		}
	}
	return nil
}

// New constructs a Listener. It does not bind; call Start.
func New(log *slog.Logger, cfg Config) (*Listener, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.PeerTimeout == 0 {
		cfg.PeerTimeout = DefaultPeerTimeout
	}
	if cfg.Deliver != nil {
		// Fail here rather than on the first transmission. A colour code
		// rejected at the moment somebody keys up is a fault nobody is
		// watching for, and the audio is already gone by then.
		if _, err := ipscbridge.New(cfg.Bridge); err != nil {
			return nil, err
		}
	}
	l := &Listener{
		cfg:      cfg,
		log:      logging.Subsystem(log, "ipsc"),
		peers:    map[uint32]*Peer{},
		bridges:  map[uint32]*ipscbridge.Converter{},
		encoders: map[uint32]*ipscbridge.Encoder{},
	}
	l.SetAllowedPeers(cfg.AllowedPeers)
	l.SetPeerNames(cfg.PeerNames)
	l.publish()
	return l, nil
}

// SetAllowedPeers replaces the set of radio IDs this master answers.
//
// # Why this is live rather than a restart
//
// `ipsc.allowed_peers` was read once when the listener was built. The console
// saves the whole configuration, so an operator could add a repeater, get a
// successful save, no restart warning, and a repeater that went on being
// ignored — the same defect found the same day in the master's registration and
// subscriber lists, and recorded before that in NeedsRestart for parrot.
//
// **An empty list still answers everybody.** That is a setting rather than the
// absence of one, so clearing the list must be savable and must take effect;
// refusing to apply an empty one would make "let every repeater in" impossible
// to express.
func (l *Listener) SetAllowedPeers(ids []uint32) {
	built := make(map[uint32]bool, len(ids))
	for _, id := range ids {
		built[id] = true
	}
	l.allowed.Store(&built)
}

// SetPeerNames replaces the callsigns an administrator gave these repeaters.
//
// **It is live for the same reason the allow list is.** An operator adding a
// repeater names it in the same save, and a name that waited for a restart
// would leave the console showing a bare radio ID after a successful save —
// which is the shape of defect that put SetAllowedPeers here in the first
// place.
//
// Display only. Nothing on the path that decides whether to answer a datagram
// reads this, and it must stay that way: a label is not an admission.
func (l *Listener) SetPeerNames(names map[uint32]string) {
	built := make(map[uint32]string, len(names))
	for id, name := range names {
		built[id] = name
	}
	l.peerNames.Store(&built)
}

// PeerName returns the callsign an administrator gave a repeater, if any.
func (l *Listener) PeerName(id uint32) string {
	if m := l.peerNames.Load(); m != nil {
		return (*m)[id]
	}
	return ""
}

// allowedSet returns the current allow list, never nil.
func (l *Listener) allowedSet() map[uint32]bool {
	if p := l.allowed.Load(); p != nil {
		return *p
	}
	return nil
}

// Start binds the socket and serves in the background.
//
// Binding is synchronous so a port conflict reaches the caller instead of a log
// line nobody reads.
func (l *Listener) Start(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp", l.cfg.ListenAddress)
	if err != nil {
		return fmt.Errorf("ipsc: cannot resolve %s: %w", l.cfg.ListenAddress, err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return fmt.Errorf("ipsc: cannot listen on %s: %w", l.cfg.ListenAddress, err)
	}
	l.conn = conn
	l.ctx = ctx
	if l.cfg.Parrot != nil {
		l.player = parrot.NewPlayer(l.log, ipscSink{l: l})
	}
	l.running.Store(true)
	l.log.Info("listening", "address", conn.LocalAddr().String(), "master_id", l.cfg.MasterID,
		"allowed_peers", len(l.allowedSet()))

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()
	go l.serve(ctx)
	go l.expire(ctx)
	return nil
}

// Address reports the bound address, empty before Start.
func (l *Listener) Address() string {
	if l.conn == nil {
		return ""
	}
	return l.conn.LocalAddr().String()
}

// Running reports whether the serve loop is active, so health can tell "not
// started" from "started and quiet".
func (l *Listener) Running() bool { return l.running.Load() }

// Peers returns the current peer list.
// SendVoice relays a Homebrew frame to every registered IPSC repeater except
// the one it came from.
//
// **A repeater receives everything and decides for itself what to repeat.** It
// has a codeplug naming the talkgroups and timeslots it carries, and QSP has no
// way to learn that — an IPSC peer announces no subscriptions, unlike a
// Homebrew peer which attaches to talkgroups explicitly. Filtering here would
// mean guessing at somebody else's codeplug; sending everything matches what
// the captures show a master's peers receiving and leaves the decision where
// the knowledge is.
//
// Frames are never sent back to their origin, for the same reason a Homebrew
// call is not echoed to the peer that transmitted it.
func (l *Listener) SendVoice(origin uint32, frame hbp.Data) {
	if l.conn == nil {
		return
	}

	type outbound struct {
		addr string
		msgs []ipsc.Message
	}
	var batch []outbound
	// **This path reported nothing, ever.** The Homebrew side logs a line per
	// destination it relays to; the repeaters got their audio in silence, so
	// an operator whose transmission did not arrive could not tell whether QSP
	// had sent it. On 2026-09-06 that turned "KD9EJA did not receive my text"
	// into an hour of reading code, when one line would have said which
	// repeaters were written to and which frames encoded to nothing.
	var relayedTo, encodedNothing []uint32

	l.mu.Lock()
	if l.relayed == nil {
		l.relayed = map[uint32]hbp.StreamID{}
	}
	for id, p := range l.peers {
		if id == origin || p.Address == "" {
			continue
		}
		// Once per transmission per repeater, not once per frame: an over is
		// fifty frames a second and a line for each is a line nobody reads.
		first := l.relayed[id] != frame.StreamID
		l.relayed[id] = frame.StreamID
		enc, ok := l.encoders[id]
		if !ok {
			enc = ipscbridge.NewEncoder(l.cfg.MasterID, l.cfg.Bridge)
			l.encoders[id] = enc
		}
		// Sign the frame with this repeater's own colour code when it has
		// told us one, so that a network carrying several colour codes works
		// for all of them. A peer that has never transmitted keeps the
		// configured default.
		if p.ColourCodeKnown {
			enc.SetColourCode(p.ColourCode)
		}
		msgs, understood := enc.Encode(frame)
		switch {
		case len(msgs) > 0:
			batch = append(batch, outbound{addr: p.Address, msgs: msgs})
			if first {
				relayedTo = append(relayedTo, id)
			}
		case !understood && first:
			// A frame this encoder cannot read is a frame that goes nowhere,
			// and that has to be said out loud. Constitution §18.
			//
			// **Only when it could not read it.** A voice Link Control header
			// is dropped on purpose, because the encoder builds its own, and
			// warning about that fired once per over per repeater on every
			// transmission on the network — a warning on correct behaviour,
			// which is how a warning stops being read.
			encodedNothing = append(encodedNothing, id)
		}
	}
	l.mu.Unlock()

	// The one thing about the text path nobody has measured, reported rather
	// than argued about. A Rate 3/4 block carries a block serial number and a
	// CRC-9; IP Site Connect puts them at the end of the block and ETSI
	// figure 8.8 draws them at the front, and which arrangement a hotspot
	// puts on air decides whether the message QSP transmits can be read at
	// all. **One text from a Pi-Star makes this line say which**, and
	// dmrfec.Rate34AirOrder is the single constant that follows from it.
	//
	// Once per transmission, not once per frame: a text is a run of bursts
	// and a line for each is a line nobody reads.
	rate34 := ""
	if ipscbridge.IsRate34(frame) {
		rate34 = ipscbridge.Rate34OrderOf(frame).String()
	}
	for _, id := range relayedTo {
		attrs := []any{
			"radio_id", id, "source", frame.SourceID,
			"destination", uint32(frame.TargetID),
			"private", frame.CallType == hbp.CallPrivate,
			"stream", fmt.Sprintf("%#08x", uint32(frame.StreamID)),
		}
		if rate34 != "" {
			attrs = append(attrs, "rate34_block", rate34)
		}
		l.log.Info("relaying transmission", attrs...)
	}
	for _, id := range encodedNothing {
		// Once per window per repeater and frame shape: a transmission that
		// cannot be read fails this way for every frame in it, seventeen a
		// second for as long as somebody talks.
		if !l.shouldSay(fmt.Sprintf("norelay|%d|%d|%d", id,
			int(frame.FrameType), int(frame.DataType)), time.Now()) {
			continue
		}
		l.log.Warn("nothing to relay: the frame could not be read",
			"radio_id", id, "source", frame.SourceID,
			"destination", uint32(frame.TargetID),
			"frame_type", int(frame.FrameType), "data_type", int(frame.DataType))
	}

	// Encoding happens under the lock because an encoder is peer state;
	// writing happens outside it, so a slow socket cannot stall the listener.
	for _, o := range batch {
		addr, err := net.ResolveUDPAddr("udp", o.addr)
		if err != nil {
			continue
		}
		for _, m := range o.msgs {
			if _, err := l.conn.WriteToUDP(m.Marshal(), addr); err != nil {
				// A repeater that has gone away fails every frame of every
				// transmission. The address and the error are the fact; the
				// repetition is not.
				if l.shouldSay("send|"+o.addr+"|"+err.Error(), time.Now()) {
					l.log.Warn("could not send to an IPSC peer", "address", o.addr, "error", err)
				}
				break
			}
		}
	}
}

// SendVoiceTo relays a frame to exactly one repeater, and to nobody else.
//
// # Why this is separate from SendVoice rather than a flag on it
//
// SendVoice answers "who else should hear this" and deliberately excludes the
// origin. This answers the opposite question, and parrot is why: a replay goes
// back to the member who recorded it and to no one else on the network.
//
// A boolean that inverted which peers receive a transmission would be one
// parameter away from broadcasting somebody's echo test to every repeater on
// the network, and the two callers would look identical at the call site.
func (l *Listener) SendVoiceTo(target uint32, frame hbp.Data) error {
	if l.conn == nil {
		return errNotServing
	}

	l.mu.Lock()
	p, ok := l.peers[target]
	if !ok || p.Address == "" {
		l.mu.Unlock()
		return errNoSuchPeer
	}
	enc, have := l.encoders[target]
	if !have {
		enc = ipscbridge.NewEncoder(l.cfg.MasterID, l.cfg.Bridge)
		l.encoders[target] = enc
	}
	if p.ColourCodeKnown {
		enc.SetColourCode(p.ColourCode)
	}
	msgs, _ := enc.Encode(frame)
	address := p.Address
	l.mu.Unlock()

	if len(msgs) == 0 {
		return nil
	}
	addr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return err
	}
	for _, m := range msgs {
		if _, err := l.conn.WriteToUDP(m.Marshal(), addr); err != nil {
			return err
		}
	}
	return nil
}

// Errors SendVoiceTo returns. They are values rather than strings because
// parrot counts failed deliveries and continues, and a replay to a peer that
// unregistered mid-transmission is ordinary rather than alarming.
var (
	errNotServing = sendError("the IPSC listener is not serving")
	errNoSuchPeer = sendError("no such registered IPSC peer")
)

type sendError string

func (e sendError) Error() string { return string(e) }

func (l *Listener) Peers() []Peer {
	if p := l.snapshot.Load(); p != nil {
		return *p
	}
	return nil
}

// Counters reports datagrams that were not served.
func (l *Listener) Counters() (ignored, unparsed uint64) {
	return l.ignored.Load(), l.unparsed.Load()
}

// RefusalWindow is how recently a peer must have been turned away for the
// listener to still be reporting it.
//
// It is longer than the ten-second retry an unregistered peer uses, so a
// repeater that is genuinely knocking stays reported between attempts, and
// shorter than anything an operator would call history.
const RefusalWindow = 60 * time.Second

// refusalWindow is how long one refusal stands for the ones after it.
//
// Ten seconds, matching routingDropWindow in internal/peers: long enough to
// cover a repeater's poll cycle and a transmission, short enough that an
// operator who fixes the allow list and keys up again is told promptly that it
// is still refused.
const refusalWindow = 10 * time.Second

// maxRefusals bounds how many distinct refusals are remembered at once.
//
// A refusal names one sender and one message type, so a working network
// produces a handful. Sixty-four matches maxRoutingDrops and is small enough
// that the memory is never worth attacking.
const maxRefusals = 64

// refusalKey names one refusal for shouldSay.
//
// Sender **and** message type, so a repeater refused for both its keepalive
// and its voice says so once for each rather than merging them: those are
// different facts about what it is trying to do, and the 2–3 September flood
// carried 0x90 and 0xf0 alternating.
//
// **A key builder rather than a wrapper around shouldSay**, so that every
// rate-limited warning in this file calls shouldSay by name. The gate in
// refusal_test.go reads the source, and a second entry point would mean
// teaching it two names — which is how a check starts collecting exceptions.
func refusalKey(sender uint32, kind byte) string {
	return fmt.Sprintf("refused|%d|%#02x", sender, kind)
}

// shouldSay reports whether a repeating warning should be logged this time.
//
// # Four sites, not one
//
// The first version of this covered the allow-list refusal only, and
// `unrecognised datagram` was the line directly above it — flooding harder, at
// about fifty warnings in six seconds from one radio on 2026-09-03. Fixing one
// and walking past its neighbour is the shape §8a keeps recording, so this is
// a helper rather than a special case: every warning a peer can provoke at the
// frame rate goes through it.
//
// **The key carries the fact, not the event.** Two refusals of the same sender
// and message type are one fact repeated; a refusal of a different sender is a
// new one. Callers build a key that distinguishes what an operator would want
// told apart and nothing finer, because a key that includes a timestamp or a
// frame number defeats the whole thing.
func (l *Listener) shouldSay(key string, now time.Time) bool {
	l.refusalsMu.Lock()
	defer l.refusalsMu.Unlock()
	if l.refusals == nil {
		l.refusals = make(map[string]time.Time)
	}
	if at, seen := l.refusals[key]; seen && now.Sub(at) < refusalWindow {
		return false
	}
	// Expired entries go first; if that is not enough the map is emptied
	// rather than trimmed, because the cost is one duplicate line and the
	// alternative is memory a stranger controls.
	if len(l.refusals) >= maxRefusals {
		for k, at := range l.refusals {
			if now.Sub(at) >= refusalWindow {
				delete(l.refusals, k)
			}
		}
		if len(l.refusals) >= maxRefusals {
			clear(l.refusals)
		}
	}
	l.refusals[key] = now
	return true
}

// LastRefused reports a radio ID turned away within RefusalWindow.
//
// **It reports a condition rather than a total**, which is the difference
// between a status an operator acts on and one they learn to ignore. A peer
// that was refused once at startup and never again is not a fault; a peer
// refused ten seconds ago is a repeater asking to join.
func (l *Listener) LastRefused(now time.Time) (id uint32, ok bool) {
	at := l.refusedAt.Load()
	if at == 0 || now.Sub(time.Unix(0, at)) > RefusalWindow {
		return 0, false
	}
	return l.refusedID.Load(), true
}

func (l *Listener) serve(ctx context.Context) {
	defer l.running.Store(false)
	responder := ipsc.Responder{MasterID: l.cfg.MasterID}
	buf := make([]byte, 2048)
	for {
		n, from, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() == nil {
				l.log.Error("read failed", "error", err)
			}
			return
		}
		l.handle(responder, from, buf[:n], time.Now())
	}
}

func (l *Listener) handle(r ipsc.Responder, from *net.UDPAddr, raw []byte, now time.Time) {
	msg, err := ipsc.Parse(raw)
	if err != nil {
		l.unparsed.Add(1)
		id, _ := ipsc.SenderIDOf(raw)
		// **Once per window** (2026-09-14). A repeater sending a message type
		// this build does not know sends it at the frame rate: radio 999998
		// produced about fifty of these in six seconds on 2026-09-03, all
		// `leading byte 0x81`.
		//
		// Keyed on the sender and the error, so a *second* unknown type from
		// the same radio is still reported — that is a new fact, and it is
		// exactly the kind this project learns protocols from.
		if l.shouldSay(fmt.Sprintf("unparsed|%d|%v", id, err), now) {
			l.log.Warn("unrecognised datagram", "from", from.String(), "sender_id", id,
				"bytes", len(raw), "error", err)
		}
		return
	}
	if allowed := l.allowedSet(); len(allowed) > 0 && !allowed[msg.SenderID] {
		l.ignored.Add(1)
		l.refusedID.Store(msg.SenderID)
		l.refusedAt.Store(now.UnixNano())
		// **Once per window, not once per datagram** (2026-09-14).
		//
		// A repeater that is not on the allow list keeps polling, and each
		// poll was a warning: production logged thousands of identical lines
		// from one sender, every ten seconds, for hours across 2–3 September.
		// The operator went looking for five refused datagrams and had to read
		// past all of them.
		//
		// The counter is unaffected — `ignored` still rises per datagram, and
		// LastRefused still names the most recent sender — so nothing an
		// operator reads on the console changes. It is the journal that was
		// lying about how much was happening.
		//
		// `internal/peers` learned this for routing drops and P25 counts
		// refusals rather than logging each; this listener got neither. §8a's
		// recurring shape, in the logging direction.
		if l.shouldSay(refusalKey(msg.SenderID, byte(msg.Kind)), now) {
			l.log.Warn("ignoring peer not on the allow list", "from", from.String(),
				"sender_id", msg.SenderID, "type", fmt.Sprintf("%#02x", byte(msg.Kind)))
		}
		return
	}

	// Conversion happens under the peer lock because the converter is peer
	// state, but delivery happens outside it: routing reaches into another
	// listener and writes to another socket, and holding this listener's lock
	// across that would make the two mutually blocking.
	frames, ended := l.record(msg, from, now)
	var delivered int
	for _, f := range frames {
		// Observed before parrot has a chance to take it, for the reason the
		// Homebrew side observes before forwarding: the record of who has been
		// on the network is not conditional on where their audio went.
		if l.cfg.Observe != nil {
			l.cfg.Observe(hbp.RepeaterID(msg.SenderID), f)
		}
		// Parrot next, and it consumes what it handles, exactly as on the
		// Homebrew side. A recording answers the member who made it; routing
		// it as well would put somebody's echo test on the network.
		if l.parrotHandles(hbp.RepeaterID(msg.SenderID), f) {
			continue
		}
		if l.cfg.Deliver != nil {
			l.cfg.Deliver(hbp.RepeaterID(msg.SenderID), f)
			delivered++
		}
	}
	if msg.Kind.IsVoice() {
		l.noteDelivery(msg.SenderID, delivered, ended, now)
	}

	// Reply to the address the datagram came from, never to the port it was
	// addressed to. A Motorola peer sources from a different port than it
	// dials: an XPR8300 used 50002 against 50000 and another repeater used
	// 50004. Assuming symmetry works against a loopback and fails on air.
	for _, out := range r.Reply(msg) {
		if _, werr := l.conn.WriteToUDP(out.Marshal(), from); werr != nil {
			l.log.Error("send failed", "to", from.String(), "error", werr)
		}
	}
}

// record tracks one datagram and returns the DMR frames it produced, together
// with whether it ended a transmission. The caller delivers the frames and then
// calls noteDelivery, which is where a finished call is closed.
func (l *Listener) record(msg ipsc.Message, from *net.UDPAddr, now time.Time) ([]hbp.Data, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	var frames []hbp.Data
	var ended bool

	p, known := l.peers[msg.SenderID]
	if !known {
		p = &Peer{RadioID: msg.SenderID, FirstHeard: now}
		l.peers[msg.SenderID] = p
	}
	p.Address = from.String()
	p.LastHeard = now

	switch msg.Kind {
	case ipsc.KindRegisterRequest:
		// A repeater that re-registers has restarted or lost the link. Its
		// counters start again rather than carrying a previous life's totals
		// into a new one.
		if !known || !p.Registered.IsZero() {
			l.log.Info("peer registered", "radio_id", msg.SenderID, "from", from.String())
		}
		p.Registered = now
		p.Keepalives = 0
	case ipsc.KindKeepaliveRequest:
		p.Keepalives++
	case ipsc.KindVoice, ipsc.KindVoicePrivate:
		p.VoiceFrames++
		frames, ended = l.recordVoice(p, msg, now)
	case ipsc.KindTextGroup, ipsc.KindTextPrivate:
		// A text is DMR data already and needs re-wrapping rather than
		// rebuilding, so it needs none of the superframe state voice does.
		// See ADR-0045.
		p.LastHeard = now
		p.TextFrames++
		l.recordText(p, msg, now)
		if f, ok := l.converterFor(p.RadioID).ConvertText(msg, hbp.RepeaterID(msg.SenderID)); ok {
			frames = []hbp.Data{f}
		}
	}
	l.publishLocked()
	return frames, ended
}

// converterFor returns this peer's converter, creating it on first voice.
//
// The caller holds l.mu. New cannot fail here: the same configuration was
// accepted in New, and a colour code does not change under a running listener.
func (l *Listener) converterFor(id uint32) *ipscbridge.Converter {
	if c, ok := l.bridges[id]; ok {
		return c
	}
	c, err := ipscbridge.New(l.cfg.Bridge)
	if err != nil {
		return nil
	}
	l.bridges[id] = c
	return c
}

// recordText records one text transmission, as an event rather than as a call
// with a duration.
//
// # Why a text is not a call
//
// Nothing recorded a text at all until now: the branch above converted the
// burst and returned, so a text from a repeater produced no `call started`,
// moved no counters, and left Last heard looking as though nothing had
// happened. An operator watching the console could not tell a working text
// path from a broken one, which is the condition that hid ADR-0047's defect
// for eighteen patches.
//
// **A text has a stream ID and no usable end.** The fixture settles the first
// half: 154 datagrams in testdata/ipsc/ipsc-text-rate34.pcap group into 16
// transmissions by stream ID alone, exactly as voice does. The second half it
// refuses to settle. The last datagram of a transmission usually carries
// 0x4000 in the flags field where the others carry 0x2080 — and that holds for
// **nine of the sixteen**. In the other seven the bit is set twice, or on the
// first datagram, or in the middle, all of them short groups carrying the 0x13
// marker whose meaning this project has never claimed to know.
//
// Nine of sixteen is not a reading. It is the same shape as ADR-0045's "nine
// exceptions", which turned out to be the whole defect.
//
// So a text is recorded as an instant: started and ended at the same moment,
// the moment its first datagram arrives. That needs no marker, and it matches
// what a text is — an operator wants to know that K9MLS texted KD9EJA at
// 11:42, not how long the transmission took. A repeated stream ID within one
// message updates the record rather than opening a second, so a text that
// takes twenty datagrams still reads as one event.
//
// # What this deliberately does not do
//
// **It does not put a text in Last heard, because a text is already there.**
// ObserveFromIPSC feeds the shared call tracker, the tracker coalesces data
// bursts inside calls.DataBurstWindow — it learned that when a single text
// produced fifteen entries — and the console already tags anything that is not
// voice with a muted `data` pill. A flag here to mark a text, and a second
// pill to draw it, would be a fourth thing saying what three existing things
// already say.
//
// That was built and removed before this shipped. Three separate items on the
// open list turned out already done the same afternoon, which is what §8a now
// records: **an open item that has survived several sessions is a claim about
// the past.** Check the defect still exists before working it.
//
// What was actually missing is here and is small: a journal line, a counter,
// and the colour code.
func (l *Listener) recordText(p *Peer, msg ipsc.Message, now time.Time) {
	t, ok := msg.AsText()
	if !ok {
		return
	}

	// **The colour code comes from the text's own Slot Type**, not from
	// Message.ColourCode, which returns false for anything that is not voice
	// by its first line. AsText already reads it, at the offset
	// TextSlotTypeFor gives — byte 51 on a 54-byte datagram and byte 57 on a
	// Rate 3/4 one, which is the six-byte shift ADR-0047 measured.
	//
	// Learning it here as well as from voice is the one part of this an
	// operator sees. A repeater that had only ever sent text showed "not heard
	// yet" in the peer table for as long as it stayed connected, and both
	// Motorola peers on the live network read that way while the text path was
	// working perfectly.
	if !p.ColourCodeKnown || p.ColourCode != t.ColourCode {
		if !p.ColourCodeKnown {
			l.log.Info("learned a peer's colour code", "radio_id", p.RadioID,
				"colour_code", int(t.ColourCode), "from", "text")
		}
		p.ColourCode, p.ColourCodeKnown = t.ColourCode, true
	}

	// Already recorded: a text is many datagrams and one event.
	if p.LastCall != nil && p.LastCall.StreamID == t.StreamID && !p.LastCall.Ended.IsZero() {
		p.LastCall.Frames++
		p.LastCall.LastFrame = now
		return
	}

	// A voice transmission still running when a text arrives is closed the way
	// a superseded one is, rather than overwritten. Same reasoning as
	// recordVoice: a call that vanishes without a `call ended` is a call
	// nobody can account for afterwards.
	if prev := p.LastCall; prev != nil && prev.Ended.IsZero() {
		l.endCall(p, prev.LastFrame, endSilent)
	}

	slot := hbp.Timeslot1
	if conv := l.converterFor(p.RadioID); conv != nil {
		slot = conv.Timeslot(msg)
	}

	p.LastCall = &Call{
		StreamID:    t.StreamID,
		Source:      t.Source,
		Destination: t.Destination,
		Private:     t.Private,
		Timeslot:    slot,
		Started:     now,
		Ended:       now,
		LastFrame:   now,
		Frames:      1,
	}
	l.log.Info("text", "radio_id", p.RadioID, "source", t.Source,
		"destination", t.Destination, "private", t.Private,
		"timeslot", int(slot),
		"stream", fmt.Sprintf("%#04x", t.StreamID))
}

// recordVoice tracks one voice frame and converts it. It reports whether the
// frame ended the transmission, which the caller acts on after delivery.
func (l *Listener) recordVoice(p *Peer, msg ipsc.Message, now time.Time) ([]hbp.Data, bool) {
	v, ok := msg.AsVoice()
	if !ok {
		return nil, false
	}

	if cc, ok := msg.ColourCode(); ok && (!p.ColourCodeKnown || p.ColourCode != cc) {
		if !p.ColourCodeKnown {
			l.log.Info("learned a peer's colour code", "radio_id", p.RadioID,
				"colour_code", int(cc))
		}
		p.ColourCode, p.ColourCodeKnown = cc, true
	}

	conv := l.converterFor(p.RadioID)
	slot := hbp.Timeslot1
	if conv != nil {
		slot = conv.Timeslot(msg)
	}

	if p.LastCall == nil || p.LastCall.StreamID != v.StreamID || !p.LastCall.Ended.IsZero() {
		// **A superseded transmission is ended, not overwritten.** This
		// replaced the record outright, so a call whose terminator never
		// arrived left a `call started` with no `call ended` anywhere in the
		// journal and disappeared from the console without a line about it.
		// Two of them are in the log for 2026-09-04 22:48.
		if prev := p.LastCall; prev != nil && prev.Ended.IsZero() {
			l.endCall(p, prev.LastFrame, endSilent)
		}
		p.LastCall = &Call{
			StreamID:    v.StreamID,
			Source:      v.SourceID,
			Destination: v.Destination,
			Private:     v.Private,
			Timeslot:    slot,
			Started:     now,
			LastFrame:   now,
		}
		// The slot bit is logged raw alongside the slot it was read as, so
		// that a key-up on a known timeslot settles the polarity from the
		// journal. Logging only the interpretation would make the log agree
		// with the setting whether or not the setting is right.
		bit, _ := msg.SlotBit()
		l.log.Info("call started", "radio_id", p.RadioID, "source", v.SourceID,
			"destination", v.Destination, "private", v.Private,
			"timeslot", int(slot), "slot_bit", bit,
			"stream", fmt.Sprintf("%#04x", v.StreamID))
	}
	p.LastCall.Frames++
	p.LastCall.LastFrame = now

	if l.cfg.Deliver == nil || conv == nil {
		return nil, v.IsLastFrame()
	}
	out := conv.Convert(msg, hbp.RepeaterID(p.RadioID))
	p.LastCall.Converted += uint64(len(out))
	// **The end is reported rather than logged here.** Delivery happens after
	// this returns and outside the lock, so a line written now would say a
	// transmission delivered fewer frames than it did, every time, by exactly
	// the last datagram's worth. A warning that cries wolf on every call is
	// worse than no warning at all.
	return out, v.IsLastFrame()
}

// noteDelivery records what reached the DMR side and closes a finished call.
//
// It runs after delivery, which is why the end of a transmission is logged from
// here: this is the first moment all three counts are known.
func (l *Listener) noteDelivery(sender uint32, delivered int, ended bool, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p := l.peers[sender]
	if p == nil || p.LastCall == nil {
		return
	}
	p.LastCall.Delivered += uint64(delivered)
	if ended {
		l.endCall(p, now, endTerminated)
	}
	l.publishLocked()
}

// expire is the sweep: it drops peers that stop keepaliving, closes
// transmissions that stop without a terminator, and finishes parrot recordings.
//
// All three are the same kind of judgement — something ended because nothing
// more arrived — and all three run on the tightest of the deadlines rather than
// the loosest, which is why the interval is named for calls.
//
// A repeater that is unplugged sends nothing and says nothing: there is no
// disconnect message in any capture. Silence is the only signal there is, so a
// peer that has been quiet longer than PeerTimeout is gone, and saying so is
// better than a console that shows a repeater which left an hour ago.
func (l *Listener) expire(ctx context.Context) {
	tick := time.NewTicker(CallSweepInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			l.ExpireAt(now)
			l.ExpireCallsAt(now)
			l.expireParrot()
		}
	}
}

// ExpireAt drops peers silent since before the timeout, and reports how many.
func (l *Listener) ExpireAt(now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	var dropped int
	for id, p := range l.peers {
		if now.Sub(p.LastHeard) <= l.cfg.PeerTimeout {
			continue
		}
		delete(l.peers, id)
		// The converter goes with the peer. A repeater that returns is a new
		// transmission's worth of state, not a resumption of one that ended
		// however long ago, and keeping them would grow without bound on an
		// address that attracts strangers.
		delete(l.bridges, id)
		delete(l.encoders, id)
		dropped++
		l.log.Info("peer timed out", "radio_id", id, "silent_for",
			now.Sub(p.LastHeard).Round(time.Second))
	}
	if dropped > 0 {
		l.publishLocked()
	}
	return dropped
}

func (l *Listener) publish() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.publishLocked()
}

func (l *Listener) publishLocked() {
	out := make([]Peer, 0, len(l.peers))
	for _, p := range l.peers {
		c := *p
		if p.LastCall != nil {
			call := *p.LastCall
			c.LastCall = &call
		}
		out = append(out, c)
	}
	l.snapshot.Store(&out)
}
