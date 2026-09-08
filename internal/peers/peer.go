// Package peers implements HBP peer identity, registration and lifecycle.
//
// QSP acts as a master: hotspots and repeaters connect to it. The master side
// of the handshake is implementable from testdata/hbp/hbp-login-session.pcap
// because that capture contains both halves of a real conversation — a peer
// logging in to BrandMeister, and BrandMeister's replies.
//
// # What is implemented from the specification rather than from a capture
//
// Two paths a complete master needs appear in no capture QSP holds, and were
// implemented from the published Homebrew specification under the interim
// rules in docs/adr/ADR-0008:
//
//   - The clean-disconnect path. A peer signals it with RPTCL, and handleClose
//     removes the registration rather than waiting for the timeout.
//   - Explicit rejection. A refused peer is answered with MSTNAK, so its
//     operator sees a rejection in their own log rather than silence.
//
// **Neither has been seen on a wire.** Their provenance is recorded per message
// type, and docs/architecture/hbp-protocol.md names the captures that would
// confirm them: a hotspot disconnecting, and a login with a wrong password. A
// live "peer disconnected cleanly" is evidence for the first, and one was
// observed on 2026-08-28.
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

	// Received, Sent and Refused count frames each way for this peer.
	//
	// # Why these exist
	//
	// **The Links page printed a dash where an outbound link printed numbers.**
	// A link this server dialled runs through an upstream transport, which
	// counts what it sends and receives; a link that dialled *in* is a peer
	// registration, and the peer table knew only that it was connected and when
	// it was last heard. 0284 made the page print a dash rather than a zero,
	// because not measured is not zero — but a dash is a placeholder, and the
	// two ends of one link could not be compared at all.
	//
	// Counted here rather than in the listener's totals because the question is
	// about one peer. `l.forwarded` and `l.sent` are the whole instance's
	// traffic and cannot answer "is this link carrying".
	//
	// Received counts frames accepted from this peer, after the access checks,
	// so it means what an operator reads it as: traffic this server took from
	// them. Refused counts the ones the subscriber list turned back, which is
	// the difference between a quiet link and a rejected one.
	Received uint64
	Sent     uint64
	Refused  uint64

	// LastTraffic is when a frame was last accepted from this peer, in UTC.
	//
	// **Not LastHeard, which counts keepalives.** A peer sends a ping every few
	// seconds whether or not anybody is talking, so LastHeard answers "is this
	// registration alive" and never grows old on a working link. The Links page
	// used it under a column headed Last heard while an outbound link used its
	// last traffic, so the same link at the same moment read 0s on one console
	// and 44s on the other — two true statements that together say one end has
	// gone deaf.
	LastTraffic time.Time

	// refused remembers the transmission most recently refused by the
	// subscriber access list, so the refusal is logged once rather than once
	// per frame. Not exported: it is bookkeeping, not something an observer of
	// the registry has any use for.
	refused refusedStream
}

// refusedStream is the transmission most recently refused, and how much of it.
//
// **The count is deliberately not part of the identity.** Folding it in would
// make every comparison fail from the second frame onward, and the refusal
// would be logged once per frame — precisely the behaviour this exists to
// prevent, failing in the direction that looks like it is working.
type refusedStream struct {
	id refusedStreamID
	// frames counts how many frames of this transmission were refused,
	// including the first.
	frames int
}

// refusedStreamID identifies one transmission at its origin.
//
// A peer carries one transmission per timeslot at a time, so remembering the
// most recent refusal per peer is enough to tell the opening frame of a refused
// stream from the four hundred that follow it. The slot is part of the identity
// because a peer can be refused on one slot while talking on the other.
type refusedStreamID struct {
	source uint32
	stream hbp.StreamID
	slot   hbp.Timeslot
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
