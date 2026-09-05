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
	// Registered is when the peer's registration was answered.
	Registered time.Time
	// LastHeard is the arrival time of its most recent message of any kind.
	LastHeard time.Time
	// Keepalives counts answered keepalives, so an operator can tell a peer
	// that just arrived from one that has been up for hours.
	Keepalives uint64
	// VoiceFrames counts voice frames received from this peer.
	VoiceFrames uint64
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
	// Frames counts voice frames.
	Frames uint64
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
			"frames", c.Frames, "duration", duration)
	case endSilent:
		l.log.Warn("call ended without a terminator", "radio_id", p.RadioID,
			"source", c.Source, "frames", c.Frames, "duration", duration)
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

	l.mu.Lock()
	for id, p := range l.peers {
		if id == origin || p.Address == "" {
			continue
		}
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
		if msgs := enc.Encode(frame); len(msgs) > 0 {
			batch = append(batch, outbound{addr: p.Address, msgs: msgs})
		}
	}
	l.mu.Unlock()

	// Encoding happens under the lock because an encoder is peer state;
	// writing happens outside it, so a slow socket cannot stall the listener.
	for _, o := range batch {
		addr, err := net.ResolveUDPAddr("udp", o.addr)
		if err != nil {
			continue
		}
		for _, m := range o.msgs {
			if _, err := l.conn.WriteToUDP(m.Marshal(), addr); err != nil {
				l.log.Warn("could not send to an IPSC peer", "address", o.addr, "error", err)
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
	msgs := enc.Encode(frame)
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
		l.log.Warn("unrecognised datagram", "from", from.String(), "sender_id", id,
			"bytes", len(raw), "error", err)
		return
	}
	if allowed := l.allowedSet(); len(allowed) > 0 && !allowed[msg.SenderID] {
		l.ignored.Add(1)
		l.refusedID.Store(msg.SenderID)
		l.refusedAt.Store(now.UnixNano())
		l.log.Warn("ignoring peer not on the allow list", "from", from.String(),
			"sender_id", msg.SenderID, "type", fmt.Sprintf("%#02x", byte(msg.Kind)))
		return
	}

	// Conversion happens under the peer lock because the converter is peer
	// state, but delivery happens outside it: routing reaches into another
	// listener and writes to another socket, and holding this listener's lock
	// across that would make the two mutually blocking.
	frames := l.record(msg, from, now)
	for _, f := range frames {
		// Parrot first, and it consumes what it handles, exactly as on the
		// Homebrew side. A recording answers the member who made it; routing
		// it as well would put somebody's echo test on the network.
		if l.parrotHandles(hbp.RepeaterID(msg.SenderID), f) {
			continue
		}
		if l.cfg.Deliver != nil {
			l.cfg.Deliver(hbp.RepeaterID(msg.SenderID), f)
		}
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

func (l *Listener) record(msg ipsc.Message, from *net.UDPAddr, now time.Time) []hbp.Data {
	l.mu.Lock()
	defer l.mu.Unlock()

	var frames []hbp.Data

	p, known := l.peers[msg.SenderID]
	if !known {
		p = &Peer{RadioID: msg.SenderID}
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
		frames = l.recordVoice(p, msg, now)
	case ipsc.KindTextGroup, ipsc.KindTextPrivate:
		// A text is DMR data already and needs re-wrapping rather than
		// rebuilding, so it needs none of the superframe state voice does.
		// See ADR-0045.
		p.LastHeard = now
		if f, ok := l.converterFor(p.RadioID).ConvertText(msg, hbp.RepeaterID(msg.SenderID)); ok {
			frames = []hbp.Data{f}
		}
	}
	l.publishLocked()
	return frames
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

func (l *Listener) recordVoice(p *Peer, msg ipsc.Message, now time.Time) []hbp.Data {
	v, ok := msg.AsVoice()
	if !ok {
		return nil
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
	if v.IsLastFrame() {
		l.endCall(p, now, endTerminated)
	}

	if l.cfg.Deliver == nil || conv == nil {
		return nil
	}
	return conv.Convert(msg, hbp.RepeaterID(p.RadioID))
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
