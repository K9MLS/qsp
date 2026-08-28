package peers

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Defaults for MasterConfig.
const (
	// DefaultPeerTimeout is how long a peer may be silent before it is
	// removed. Observed keepalive interval is 10 s, so this tolerates five
	// consecutive losses before disconnecting a peer that is merely on a poor
	// link.
	DefaultPeerTimeout = 60 * time.Second

	// DefaultLoginTimeout bounds a half-finished handshake. A peer that logs
	// in and never authenticates would otherwise hold a slot indefinitely.
	DefaultLoginTimeout = 30 * time.Second

	// DefaultMaxPeers bounds memory and the size of a routing decision.
	DefaultMaxPeers = 200
)

// PasswordFunc returns the shared secret for a peer.
//
// Returning false rejects the login. Most installations use one password for
// every peer; per-peer secrets are supported because the signature allows them,
// not because anything here requires them.
//
// The returned slice is not retained.
type PasswordFunc func(id hbp.RepeaterID) ([]byte, bool)

// MasterConfig configures a Master.
type MasterConfig struct {
	// Password supplies peer secrets. Required.
	Password PasswordFunc
	// PeerTimeout is silence tolerated from a configured peer. Zero selects
	// DefaultPeerTimeout.
	PeerTimeout time.Duration
	// LoginTimeout bounds an incomplete handshake. Zero selects
	// DefaultLoginTimeout.
	LoginTimeout time.Duration
	// MaxPeers bounds the registry. Zero selects DefaultMaxPeers.
	MaxPeers int
	// Access decides which repeaters may register and which subscribers may
	// transmit.
	//
	// The zero value permits everything, which is what makes an instance with
	// no access block behave as it did before access control existed. The
	// talkgroup lists in this set are not consulted here: a talkgroup is a
	// routing question and is answered where destinations are known.
	Access access.Lists
	// Now supplies the current time. Zero uses time.Now.
	Now func() time.Time
	// Salt generates login challenges. Zero uses crypto/rand.
	//
	// Injected so that tests are deterministic. A predictable salt in
	// production would let an attacker replay a captured digest, so this must
	// never be set outside tests.
	Salt func() ([4]byte, error)
}

// Response is a datagram the caller should send.
type Response struct {
	To      netip.AddrPort
	Payload []byte
}

// Event describes something that happened to a peer, for the caller to publish.
//
// Master does not publish to the event bus itself: it performs no I/O, which is
// what keeps it testable without one.
type Event struct {
	Kind EventKind
	Peer Peer
	// Reason explains a disconnection. Empty otherwise.
	Reason string
}

// EventKind classifies a peer event.
type EventKind string

// Peer event kinds.
const (
	// EventConnected is emitted when a peer completes registration.
	EventConnected EventKind = "connected"
	// EventDisconnected is emitted when a peer is removed.
	EventDisconnected EventKind = "disconnected"
	// EventRebound is emitted when a peer's source address changed and it
	// re-authenticated from the new one.
	EventRebound EventKind = "rebound"
)

// Outcome is the result of handling one datagram.
type Outcome struct {
	// Responses are datagrams to send, in order.
	Responses []Response
	// Events are state changes to publish.
	Events []Event
	// Data is a voice or data frame accepted from a registered peer, ready for
	// routing. Nil unless a frame was accepted.
	Data *hbp.Data
	// From identifies the peer a frame came from. Valid only when Data is set.
	From hbp.RepeaterID
	// Dropped explains why a datagram produced nothing. Empty when the
	// datagram was acted on.
	//
	// It is always populated when a datagram is discarded, because "why didn't
	// this peer connect" is the single most common operator question and
	// answering it must not require a packet capture.
	Dropped string
}

// dropped builds an Outcome that discards a datagram with an explanation.
func dropped(format string, args ...any) Outcome {
	return Outcome{Dropped: fmt.Sprintf(format, args...)}
}

// Master accepts HBP peer connections.
//
// It is not safe for concurrent use. It is designed to be owned by a single
// goroutine, consistent with the single-writer model in ADR-0002, and every
// method that reads state returns copies so that observers never alias it.
type Master struct {
	cfg   MasterConfig
	log   *slog.Logger
	peers map[hbp.RepeaterID]*Peer
}

// NewMaster constructs a Master.
func NewMaster(log *slog.Logger, cfg MasterConfig) (*Master, error) {
	if cfg.Password == nil {
		return nil, errors.New("peers: a Password function is required")
	}
	if cfg.PeerTimeout <= 0 {
		cfg.PeerTimeout = DefaultPeerTimeout
	}
	if cfg.LoginTimeout <= 0 {
		cfg.LoginTimeout = DefaultLoginTimeout
	}
	if cfg.MaxPeers <= 0 {
		cfg.MaxPeers = DefaultMaxPeers
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Salt == nil {
		cfg.Salt = randomSalt
	}
	return &Master{
		cfg:   cfg,
		log:   logging.Subsystem(log, "peers"),
		peers: make(map[hbp.RepeaterID]*Peer),
	}, nil
}

func randomSalt() ([4]byte, error) {
	var s [4]byte
	if _, err := rand.Read(s[:]); err != nil {
		return s, fmt.Errorf("cannot generate a login challenge: %w", err)
	}
	return s, nil
}

// Handle processes one datagram.
//
// The datagram is treated as hostile: it is parsed, validated, and matched
// against the sender's registration state before anything is acted on. Handle
// never panics and never retains the slice.
func (m *Master) Handle(datagram []byte, from netip.AddrPort) Outcome {
	msg, err := hbp.Parse(datagram)
	if err != nil {
		return dropped("unparseable datagram from %s: %v", from, err)
	}

	now := m.cfg.Now().UTC()

	switch v := msg.(type) {
	case hbp.Login:
		return m.handleLogin(v, from, now)
	case hbp.Key:
		return m.handleKey(v, from, now)
	case hbp.Config:
		return m.handleConfig(v, from, now)
	case hbp.Ping:
		return m.handlePing(v, from, now)
	case hbp.Data:
		return m.handleData(v, from, now)
	case hbp.RepeaterClose:
		return m.handleClose(v, from)
	default:
		// Messages a master receives but has no role for, such as MSTPONG
		// arriving at a master rather than a peer.
		return dropped("%s from %s is not a message a master acts on", msg.Kind(), from)
	}
}

// handleLogin issues a challenge.
//
// A login for an already-registered ID restarts the handshake. That is
// deliberate: a hotspot that reboots loses its session state and logs in again,
// and refusing would leave it unable to reconnect until its old registration
// timed out. The existing registration is not discarded until the new login
// completes, so an unauthenticated stranger cannot displace a working peer by
// sending a single packet.
func (m *Master) handleLogin(msg hbp.Login, from netip.AddrPort, now time.Time) Outcome {
	if msg.RepeaterID == 0 {
		return dropped("login from %s carries repeater ID 0, which is not a valid station", from)
	}
	// The registration list is consulted before the password, so that a
	// refused ID never reaches the credential path at all. It also means the
	// log says which of the two refused it, and "wrong password" and "not
	// permitted here" are very different messages to an operator debugging a
	// hotspot that will not connect.
	if !m.cfg.Access.Registration.Allows(uint32(msg.RepeaterID)) {
		m.log.Warn("login refused: not permitted by the registration list",
			logging.PeerID(uint32(msg.RepeaterID)),
			slog.String("from", from.String()),
		)
		return m.reject(msg.RepeaterID, from,
			fmt.Sprintf("repeater ID %d is not permitted by dmr.access.registration", msg.RepeaterID))
	}
	if _, ok := m.cfg.Password(msg.RepeaterID); !ok {
		m.log.Warn("login refused: unknown repeater ID",
			logging.PeerID(uint32(msg.RepeaterID)),
			slog.String("from", from.String()),
		)
		return m.reject(msg.RepeaterID, from,
			fmt.Sprintf("no password is configured for repeater ID %d", msg.RepeaterID))
	}

	existing, known := m.peers[msg.RepeaterID]
	if !known && len(m.peers) >= m.cfg.MaxPeers {
		return m.reject(msg.RepeaterID, from,
			fmt.Sprintf("peer limit of %d reached", m.cfg.MaxPeers))
	}

	salt, err := m.cfg.Salt()
	if err != nil {
		m.log.Error("cannot issue a login challenge", slog.String("error", err.Error()))
		return dropped("cannot generate a challenge: %v", err)
	}

	if known && existing.State == StateConfigured && existing.Addr != from {
		// A configured peer is re-logging in from a different address. Keep the
		// old registration intact until the new one authenticates; see ADR-0011.
		m.log.Info("configured peer is logging in from a new address",
			logging.PeerID(uint32(msg.RepeaterID)),
			slog.String("known_address", existing.Addr.String()),
			slog.String("new_address", from.String()),
		)
	}

	p := &Peer{
		ID:        msg.RepeaterID,
		Addr:      from,
		State:     StateChallenged,
		Salt:      salt,
		FirstSeen: now,
		LastHeard: now,
	}
	if known {
		// Preserve what the old registration knew so that a re-login does not
		// lose the peer's announced identity while it re-authenticates.
		p.FirstSeen = existing.FirstSeen
		p.Config = existing.Config
		p.ConfiguredAt = existing.ConfiguredAt
	}
	m.peers[msg.RepeaterID] = p

	return Outcome{Responses: []Response{{
		To:      from,
		Payload: hbp.Ack{Payload: salt}.Marshal(),
	}}}
}

// handleKey verifies the peer's digest against the challenge it was issued.
func (m *Master) handleKey(msg hbp.Key, from netip.AddrPort, now time.Time) Outcome {
	p, ok := m.peers[msg.RepeaterID]
	if !ok {
		return dropped("authentication from repeater ID %d at %s, which has not logged in", msg.RepeaterID, from)
	}
	if p.State != StateChallenged {
		return dropped("authentication from repeater ID %d while %s, not challenged", msg.RepeaterID, p.State)
	}
	if p.Addr != from {
		// The digest is bound to a salt issued to a specific address. Accepting
		// it from elsewhere would let anyone who observed the exchange
		// authenticate as that peer.
		return dropped("authentication for repeater ID %d arrived from %s but the challenge was issued to %s",
			msg.RepeaterID, from, p.Addr)
	}

	password, ok := m.cfg.Password(msg.RepeaterID)
	if !ok {
		delete(m.peers, msg.RepeaterID)
		return dropped("no password is configured for repeater ID %d", msg.RepeaterID)
	}
	if !hbp.VerifyDigest(p.Salt, password, msg.Digest) {
		// Remove the half-open registration so a wrong password cannot hold a
		// slot, and so retries start cleanly.
		delete(m.peers, msg.RepeaterID)
		m.log.Warn("authentication failed",
			logging.PeerID(uint32(msg.RepeaterID)),
			slog.String("from", from.String()),
		)
		return m.reject(msg.RepeaterID, from,
			fmt.Sprintf("authentication failed for repeater ID %d (wrong password)", msg.RepeaterID))
	}

	p.State = StateAuthenticated
	p.LastHeard = now
	// The salt is spent. Clearing it means a replayed RPTK cannot be checked
	// against it again.
	p.Salt = [4]byte{}

	var id [4]byte
	ack := hbp.Ack{}
	putRepeaterID(&id, msg.RepeaterID)
	ack.Payload = id

	return Outcome{Responses: []Response{{To: from, Payload: ack.Marshal()}}}
}

// handleConfig completes registration.
func (m *Master) handleConfig(msg hbp.Config, from netip.AddrPort, now time.Time) Outcome {
	p, ok := m.peers[msg.RepeaterID]
	if !ok {
		return dropped("configuration from repeater ID %d at %s, which has not logged in", msg.RepeaterID, from)
	}
	if p.Addr != from {
		return dropped("configuration for repeater ID %d arrived from %s but it registered from %s",
			msg.RepeaterID, from, p.Addr)
	}
	if p.State != StateAuthenticated && p.State != StateConfigured {
		return dropped("configuration from repeater ID %d while %s, not authenticated", msg.RepeaterID, p.State)
	}

	first := p.State != StateConfigured
	cfg := msg
	p.Config = &cfg
	p.State = StateConfigured
	p.LastHeard = now
	if first {
		p.ConfiguredAt = now
	}

	var id [4]byte
	putRepeaterID(&id, msg.RepeaterID)
	out := Outcome{Responses: []Response{{To: from, Payload: hbp.Ack{Payload: id}.Marshal()}}}

	if first {
		m.log.Info("peer connected",
			logging.PeerID(uint32(p.ID)),
			logging.Callsign(p.Callsign()),
			slog.String("from", displayAddr(from)),
		)
		out.Events = []Event{{Kind: EventConnected, Peer: p.clone()}}
	}
	return out
}

// handlePing answers a keepalive.
func (m *Master) handlePing(msg hbp.Ping, from netip.AddrPort, now time.Time) Outcome {
	p, ok := m.peers[msg.RepeaterID]
	if !ok {
		// **Tell it to log in again rather than dropping in silence.**
		//
		// This is the case after a master restart: the peer's registration is
		// gone, the peer does not know, and it keeps sending keepalives into
		// nothing. Dropping them costs a minute of dead network on every
		// deploy, because the peer only re-logs in when its own timeout fires.
		// MSTNAK is exactly the message for it — docs/architecture has always
		// said it both refuses a login and tells a stale peer to log in again,
		// and only the first half was used.
		//
		// Only keepalives are answered this way. A stale peer also sends voice
		// frames, at roughly one every 60 ms, and answering each would put five
		// hundred datagrams on the wire for one transmission. A keepalive
		// arrives every ten seconds and is the peer's own liveness check, which
		// makes it the right place to say "you are not registered here".
		return m.reject(msg.RepeaterID, from,
			fmt.Sprintf("keepalive from repeater ID %d at %s, which is not registered; "+
				"answered with MSTNAK so it logs in again", msg.RepeaterID, from))
	}
	if !p.State.CanPassTraffic() {
		return dropped("keepalive from repeater ID %d while %s", msg.RepeaterID, p.State)
	}
	if p.Addr != from {
		// See ADR-0011: a source address change requires re-authentication.
		return dropped("keepalive for repeater ID %d arrived from %s but it registered from %s; it must log in again",
			msg.RepeaterID, from, p.Addr)
	}

	p.LastHeard = now

	// staticcheck suggests hbp.Pong(msg), which compiles only because the two
	// structs happen to share a shape today. They are distinct wire messages —
	// RPTPING and MSTPONG — and that shape is a coincidence, not a contract.
	// A conversion would silently start copying any field later added to both,
	// which is precisely the class of bug the fuzzer cannot catch, since both
	// sides would agree. Naming the one field that crosses the boundary is
	// worth the extra characters.
	//lint:ignore S1016 Ping and Pong are distinct messages that share a shape by coincidence
	pong := hbp.Pong{RepeaterID: msg.RepeaterID}

	return Outcome{Responses: []Response{{
		To:      from,
		Payload: pong.Marshal(),
	}}}
}

// handleData accepts a voice or data frame from a registered peer.
//
// The frame's repeater ID identifies the sender of the packet. It is checked
// against the registry because an unregistered station must not be able to
// inject traffic by sending DMRD without ever logging in.
func (m *Master) handleData(msg hbp.Data, from netip.AddrPort, now time.Time) Outcome {
	p, ok := m.peers[msg.RepeaterID]
	if !ok {
		return dropped("frame from repeater ID %d at %s, which is not registered", msg.RepeaterID, from)
	}
	if !p.State.CanPassTraffic() {
		return dropped("frame from repeater ID %d while %s; it must complete registration first", msg.RepeaterID, p.State)
	}
	if p.Addr != from {
		return dropped("frame for repeater ID %d arrived from %s but it registered from %s", msg.RepeaterID, from, p.Addr)
	}

	// The peer is heard from whether or not the frame is carried. A subscriber
	// refusal is about one radio; the hotspot behind it is working, and timing
	// it out because somebody keyed a banned radio would disconnect innocent
	// users of shared infrastructure.
	p.LastHeard = now

	if !m.cfg.Access.Subscriber.Allows(msg.SourceID) {
		return m.refuseSubscriber(p, msg, now)
	}
	p.refused = refusedStream{}

	frame := msg
	return Outcome{Data: &frame, From: p.ID}
}

// refuseSubscriber drops a frame from a subscriber the access list refuses.
//
// **The refusal is announced once per transmission, not once per frame.** A
// subscriber holding the key for thirty seconds is roughly five hundred frames,
// and five hundred identical lines is not an explanation — it is an operator's
// journal rotated past the evidence they needed. The same reasoning quietened
// the join page's successful polls at 0.1.9.
//
// Constitution §18 still holds: nothing is dropped silently. The first frame of
// the stream says what happened and why, and the rest are counted.
func (m *Master) refuseSubscriber(p *Peer, msg hbp.Data, now time.Time) Outcome {
	current := refusedStreamID{source: msg.SourceID, stream: msg.StreamID, slot: msg.Timeslot}

	if p.refused.id == current {
		p.refused.frames++
		// Silent in the log, but never silent in the Outcome. A caller
		// counting drops still sees every one.
		return dropped("subscriber %d is not permitted by dmr.access.subscribers "+
			"(frame %d of this transmission)", msg.SourceID, p.refused.frames)
	}

	p.refused = refusedStream{id: current, frames: 1}
	m.log.Info("transmission refused: subscriber not permitted",
		logging.PeerID(uint32(p.ID)),
		slog.Uint64("subscriber", uint64(msg.SourceID)),
		slog.Uint64("talkgroup", uint64(msg.TargetID)),
		slog.String("timeslot", msg.Timeslot.String()),
	)
	return dropped("subscriber %d is not permitted by dmr.access.subscribers", msg.SourceID)
}

// reject answers a refused peer with MSTNAK.
//
// Telling a peer why it cannot connect is worth doing even though QSP has no
// way to say which reason: a peer that receives MSTNAK stops retrying blindly,
// and its operator sees a rejection in their own log rather than silence.
func (m *Master) reject(id hbp.RepeaterID, to netip.AddrPort, reason string) Outcome {
	return Outcome{
		Responses: []Response{{To: to, Payload: hbp.Nak{RepeaterID: id}.Marshal()}},
		Dropped:   reason,
	}
}

// handleClose removes a peer that is shutting down cleanly.
//
// The address is checked as strictly as it is for traffic: without that, anyone
// who knows a repeater ID could disconnect it with a single nine-byte datagram.
func (m *Master) handleClose(msg hbp.RepeaterClose, from netip.AddrPort) Outcome {
	p, ok := m.peers[msg.RepeaterID]
	if !ok {
		return dropped("close from repeater ID %d at %s, which is not registered", msg.RepeaterID, from)
	}
	if p.Addr != from {
		return dropped("close for repeater ID %d arrived from %s but it registered from %s",
			msg.RepeaterID, from, p.Addr)
	}

	delete(m.peers, msg.RepeaterID)
	m.log.Info("peer disconnected cleanly",
		logging.PeerID(uint32(p.ID)),
		logging.Callsign(p.Callsign()),
	)

	// Only a peer that had completed registration was ever announced as
	// connected, so only that one is announced as gone.
	if p.State != StateConfigured {
		return Outcome{Dropped: fmt.Sprintf("repeater ID %d closed before completing registration", msg.RepeaterID)}
	}
	return Outcome{Events: []Event{{
		Kind:   EventDisconnected,
		Peer:   p.clone(),
		Reason: "the peer closed the connection",
	}}}
}

// Expire removes peers that have gone silent.
//
// The caller invokes it periodically. A configured peer is removed after
// PeerTimeout; an incomplete handshake after LoginTimeout, which is shorter
// because a half-open registration holds a slot without providing service.
func (m *Master) Expire() []Event {
	now := m.cfg.Now().UTC()

	var stale []hbp.RepeaterID
	for id, p := range m.peers {
		limit := m.cfg.LoginTimeout
		if p.State == StateConfigured {
			limit = m.cfg.PeerTimeout
		}
		if p.Idle(now) > limit {
			stale = append(stale, id)
		}
	}
	// Sorted so that events are emitted deterministically rather than in map
	// iteration order, which makes tests and logs reproducible.
	sort.Slice(stale, func(i, j int) bool { return stale[i] < stale[j] })

	events := make([]Event, 0, len(stale))
	for _, id := range stale {
		p := m.peers[id]
		reason := fmt.Sprintf("no traffic for %s", p.Idle(now).Truncate(time.Second))
		if p.State != StateConfigured {
			reason = fmt.Sprintf("login not completed within %s", m.cfg.LoginTimeout)
		}
		m.log.Info("peer removed",
			logging.PeerID(uint32(id)),
			logging.Callsign(p.Callsign()),
			slog.String("reason", reason),
		)
		delete(m.peers, id)
		if p.State == StateConfigured {
			events = append(events, Event{Kind: EventDisconnected, Peer: p.clone(), Reason: reason})
		}
	}
	return events
}

// Peers returns a snapshot of every registration, ordered by ID.
//
// The result is a copy: callers may hold it without observing later mutation.
func (m *Master) Peers() []Peer {
	out := make([]Peer, 0, len(m.peers))
	for _, p := range m.peers {
		out = append(out, p.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Lookup returns one peer by ID.
func (m *Master) Lookup(id hbp.RepeaterID) (Peer, bool) {
	p, ok := m.peers[id]
	if !ok {
		return Peer{}, false
	}
	return p.clone(), true
}

// Count returns the number of registrations, including incomplete handshakes.
func (m *Master) Count() int { return len(m.peers) }

// ConfiguredCount returns the number of peers able to pass traffic.
func (m *Master) ConfiguredCount() int {
	n := 0
	for _, p := range m.peers {
		if p.State.CanPassTraffic() {
			n++
		}
	}
	return n
}

// displayAddr renders an address without the IPv4-mapped IPv6 wrapper, so a
// hotspot at 192.168.1.155 is logged as that rather than as
// "[::ffff:192.168.1.155]".
func displayAddr(a netip.AddrPort) string {
	if addr := a.Addr(); addr.Is4In6() {
		return netip.AddrPortFrom(addr.Unmap(), a.Port()).String()
	}
	return a.String()
}

func putRepeaterID(dst *[4]byte, id hbp.RepeaterID) {
	dst[0] = byte(uint32(id) >> 24)
	dst[1] = byte(uint32(id) >> 16)
	dst[2] = byte(uint32(id) >> 8)
	dst[3] = byte(uint32(id))
}
