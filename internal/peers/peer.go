// Package peers implements HBP peer identity, registration and lifecycle.
//
// QSP acts as a master: hotspots and repeaters connect to it. The master side
// of the handshake is implementable from testdata/hbp/hbp-login-session.pcap
// because that capture contains both halves of a real conversation — a peer
// logging in to BrandMeister, and BrandMeister's replies.
//
// # What is deliberately absent
//
// Two paths a complete master needs were never captured, and per
// docs/adr/ADR-0008 they are not guessed at:
//
//   - There is no clean-disconnect path. A peer signals it with RPTCL, which
//     this build cannot parse. Peers therefore leave only by timing out.
//   - There is no explicit rejection. A master signals it with MSTNAK. QSP
//     drops the datagram instead, which is a real behaviour but a worse one:
//     the peer learns nothing and retries until it gives up.
//
// Both are recorded in docs/architecture/hbp-protocol.md with the capture that
// would close them. Neither is stubbed, and neither silently pretends to work.
//
// # Shape
//
// Master.Handle is a pure function of a datagram, its source address and the
// current time. It performs no I/O and owns no sockets, which is what makes the
// entire lifecycle — including timeouts, replays and NAT rebinding — testable
// deterministically. A transport drives it; see cmd/qsp.
package peers

import (
	"fmt"
	"net/netip"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// State is a peer's position in the login sequence.
//
// The sequence is strictly ordered. A message arriving out of order is not an
// error to recover from but evidence of a confused or hostile peer, and is
// dropped.
type State string

const (
	// StateChallenged means a login request was received and a salt issued.
	// The peer must answer with RPTK. It cannot pass traffic.
	StateChallenged State = "challenged"
	// StateAuthenticated means the digest verified. The peer must still send
	// its configuration before it can pass traffic.
	StateAuthenticated State = "authenticated"
	// StateConfigured means the peer is fully registered and may pass traffic.
	StateConfigured State = "configured"
)

// CanPassTraffic reports whether a peer in this state may send voice frames.
//
// Only a fully configured peer may. Accepting traffic from a merely
// authenticated peer would let a caller route frames from a station whose
// timeslot and colour code are still unknown.
func (s State) CanPassTraffic() bool { return s == StateConfigured }

// Peer is one connected station.
//
// Identity is the repeater ID, not the source address. The address is where
// datagrams currently arrive from and may legitimately change; see
// docs/adr/ADR-0011 for what QSP does when it does.
type Peer struct {
	// ID is the peer's repeater or radio ID, taken from its login request.
	ID hbp.RepeaterID
	// Addr is the source address most recently accepted for this peer.
	Addr netip.AddrPort
	// State is its position in the login sequence.
	State State
	// Config is the configuration it announced. Nil until StateConfigured.
	Config *hbp.Config
	// Salt is the challenge issued to it. Retained only while challenged.
	Salt [4]byte
	// FirstSeen is when its login request arrived, in UTC.
	FirstSeen time.Time
	// LastHeard is when any accepted datagram last arrived, in UTC. Timeouts
	// are measured from here.
	LastHeard time.Time
	// ConfiguredAt is when it completed registration, in UTC. Zero until then.
	ConfiguredAt time.Time
}

// Callsign returns the peer's announced callsign, or the empty string if it has
// not sent its configuration yet.
func (p *Peer) Callsign() string {
	if p.Config == nil {
		return ""
	}
	return p.Config.Callsign
}

// Idle reports how long since the peer was last heard from.
func (p *Peer) Idle(now time.Time) time.Duration { return now.Sub(p.LastHeard) }

// String implements fmt.Stringer for logging.
func (p *Peer) String() string {
	if cs := p.Callsign(); cs != "" {
		return fmt.Sprintf("%s (%d)", cs, p.ID)
	}
	return fmt.Sprintf("peer %d", p.ID)
}

// clone returns a copy safe to hand to a caller.
//
// Peers are owned by a Master and mutated as datagrams arrive. Returning a
// pointer into that state would let a caller observe changes mid-update, or
// mutate registry state from outside.
func (p *Peer) clone() Peer {
	out := *p
	if p.Config != nil {
		cfg := *p.Config
		out.Config = &cfg
	}
	return out
}
