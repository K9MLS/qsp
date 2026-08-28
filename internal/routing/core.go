package routing

import (
	"fmt"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// StreamTimeout is how long a destination stays reserved after its last frame.
//
// It must exceed the gap between voice frames (60 ms) by enough to survive
// ordinary loss, while being short enough that a transmission cut mid-stream
// does not block the destination for long. Two seconds matches the call
// tracker's timeout, so an operator sees a call disappear from the console and
// the destination free up at the same moment.
const StreamTimeout = 2 * time.Second

// Delivery is one frame to send to one peer.
type Delivery struct {
	// Peer is the destination.
	Peer hbp.RepeaterID
	// Frame is the frame to send, already rewritten for the destination.
	Frame hbp.Data
	// Bridge names the bridge responsible, for logging.
	Bridge string
}

// UpstreamDelivery is a frame bound for a link to another network.
//
// It is separate from Delivery because the two leave by different doors: a peer
// delivery is a DMRD datagram to a registered repeater, while an upstream
// delivery is signed and sent to a fixed address. Merging them would mean every
// consumer of Deliveries had to check which kind it held.
type UpstreamDelivery struct {
	// Upstream names the link.
	Upstream string
	// Frame is the frame to send, rewritten for the destination talkgroup.
	//
	// The timeslot is *not* forced to TS1 here. That is the OpenBridge
	// encoder's rule, applied where the protocol is spoken, so that this
	// package stays ignorant of which protocol a link uses.
	Frame hbp.Data
	// Bridge names the bridge responsible, for logging.
	Bridge string
}

// routeTarget is a destination and how it was chosen.
//
// Repeat and bridging differ in one rule — whether a call may return to the
// peer that sent it — so the two cannot be flattened into one list of
// endpoints.
type routeTarget struct {
	Endpoint
	repeat bool
}

// Drop explains a frame that was not forwarded.
//
// Constitution §18 forbids silently dropping traffic. Every refusal produces
// one of these so the operator can be told what happened and why.
type Drop struct {
	// To is the destination that refused the frame.
	To Endpoint
	// Reason is operator-facing.
	Reason string
}

// Result is the outcome of routing one frame.
type Result struct {
	// Deliveries are the frames to send to peers.
	Deliveries []Delivery
	// Upstreams are the frames to send over links to other networks.
	Upstreams []UpstreamDelivery
	// Drops explain destinations that were skipped.
	Drops []Drop
	// StartedStreams are destinations that began receiving with this frame.
	StartedStreams []Endpoint
	// Reason explains why nothing was delivered, when nothing was.
	Reason string
}

// PeerLookup reports whether a peer is registered and able to receive traffic.
//
// The core depends on this narrow interface rather than on the peers package,
// which keeps the dependency one-directional and lets the core be tested
// without a registry.
type PeerLookup interface {
	// Ready reports whether the peer may be sent traffic.
	Ready(id hbp.RepeaterID) bool
	// ReadyPeers returns every peer able to receive traffic, ordered by ID.
	// It resolves AnyPeer endpoints.
	ReadyPeers() []hbp.RepeaterID
}

// reservation records a destination currently receiving a transmission.
type reservation struct {
	// source identifies the transmission holding this destination.
	source   sourceKey
	lastSeen time.Time
	bridge   string
}

// sourceKey identifies one transmission at its origin.
type sourceKey struct {
	peer   hbp.RepeaterID
	stream hbp.StreamID
	slot   hbp.Timeslot
	// upstream names the link a frame arrived on, empty for peer traffic.
	//
	// Two networks choose stream IDs independently, so a frame from
	// BrandMeister and a local transmission can share one. Without this, the
	// collision would look like a continuation of the same stream and the
	// second talker's audio would be interleaved with the first.
	upstream string
}

// Core applies a routing table to live traffic.
//
// It is not safe for concurrent use and is owned by the goroutine that reads
// the socket, consistent with ADR-0002. The routing decision itself stays pure;
// this type adds only the state that decision cannot have — what is in flight.
type Core struct {
	repeat bool

	table   *Table
	peers   PeerLookup
	timeout time.Duration

	// busy maps a destination endpoint to the transmission holding it.
	//
	// A destination can carry one transmission at a time. Without this, two
	// people keying up on different repeaters bridged to the same talkgroup
	// would have their audio interleaved into an unintelligible mess.
	busy map[Endpoint]*reservation
}

// CoreOptions configures a Core.
type CoreOptions struct {
	// NoRepeat turns off the master's repeat behaviour.
	//
	// The zero value repeats, because a master that does not is inert and
	// nobody wants one by accident. HBlink spells this the other way round,
	// with REPEAT defaulting true in every published configuration; the
	// negative here means an empty CoreOptions behaves correctly.
	NoRepeat bool

	// Table is the routing table. May be nil, meaning nothing is routed.
	Table *Table
	// Peers resolves destinations. Required.
	Peers PeerLookup
	// Timeout is how long a destination stays reserved after its last frame.
	// Zero selects StreamTimeout.
	Timeout time.Duration
}

// NewCore constructs a Core.
func NewCore(opts CoreOptions) (*Core, error) {
	if opts.Peers == nil {
		return nil, fmt.Errorf("routing: a PeerLookup is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = StreamTimeout
	}
	return &Core{
		repeat:  !opts.NoRepeat,
		table:   opts.Table,
		peers:   opts.Peers,
		timeout: opts.Timeout,
		busy:    make(map[Endpoint]*reservation),
	}, nil
}

// SetTable swaps in a new routing table atomically.
//
// In-flight transmissions keep their existing reservations: a configuration
// change must not cut somebody off mid-sentence. New frames are routed against
// the new table, so the change takes effect at the next transmission boundary
// (clarification R4).
func (c *Core) SetTable(t *Table) { c.table = t }

// Table returns the active routing table.
//
// Like every method on Core, it must be called only from the goroutine that
// owns it. It is not an observation hook: reading it from an HTTP handler or a
// test is a data race against SetTable. Use the owner's published snapshot
// instead — see peers.Listener.EnabledBridges.
func (c *Core) Table() *Table { return c.table }

// Route decides where one accepted frame should be delivered.
//
// The frame is rewritten per destination: its target talkgroup and timeslot
// become the destination's, and its repeater ID becomes the destination peer's.
//
// # On rewriting the repeater ID
//
// The raw capture behind testdata/hbp/ showed a master sending frames to a peer
// with that peer's own repeater ID in the field, and a gateway substituting its
// own value when relaying. The frames demonstrating this were third-party and
// had to be removed before the fixture could be published, so **this behaviour
// is not covered by any test fixture** and is recorded in
// docs/architecture/hbp-protocol.md as unverified.
//
// Setting the destination peer's ID is the behaviour that was observed. It
// needs confirming against real hardware before QSP forwards to a live network.
func (c *Core) Route(from hbp.RepeaterID, frame hbp.Data, now time.Time) Result {
	return c.route(Endpoint{Peer: from, Talkgroup: frame.TargetID, Timeslot: frame.Timeslot}, frame, now)
}

// RouteFromUpstream routes a frame that arrived over a link from another
// network.
//
// It is a separate entry point rather than a flag on Route because the two
// differ in one rule that must not be possible to forget: a frame from an
// upstream is never sent to an upstream.
//
// Without that, a club exporting and importing the same talkgroup — the
// ordinary case, not an exotic one — relays every frame from BrandMeister
// straight back to BrandMeister, where the copy is indistinguishable from a new
// transmission. That is a broadcast storm on somebody else's network produced
// by a configuration that looks entirely reasonable, and BrandMeister
// disconnects bridges found re-bridging.
//
// The rule is blunt rather than clever. A hop count would permit
// QSP-to-QSP-to-elsewhere chains, but it requires every participant to
// cooperate and fails into exactly the storm it was meant to prevent when one
// does not.
func (c *Core) RouteFromUpstream(name string, frame hbp.Data, now time.Time) Result {
	return c.route(Endpoint{Upstream: name, Talkgroup: frame.TargetID, Timeslot: frame.Timeslot}, frame, now)
}

func (c *Core) route(origin Endpoint, frame hbp.Data, now time.Time) Result {
	from := origin.Peer
	fromUpstream := origin.Upstream != ""
	decision := c.table.Route(origin)

	// Repeat: the master's own job, and the reason a DMR network exists.
	//
	// A group call on TG X, TS Y is heard by every other peer on TG X, TS Y.
	// No bridge is involved and none is needed — bridging moves traffic
	// *between* talkgroups, which is a different and additional thing.
	//
	// QSP was built with bridging as its whole model and had no way to express
	// four hotspots on one talkgroup hearing each other. See ADR-0019.
	targets := make([]routeTarget, 0, len(decision.Targets)+1)
	for _, t := range decision.Targets {
		targets = append(targets, routeTarget{Endpoint: t})
	}
	if c.repeat && frame.CallType == hbp.CallGroup {
		targets = append(targets, routeTarget{
			Endpoint: Endpoint{
				Peer:      AnyPeer,
				Talkgroup: frame.TargetID,
				Timeslot:  frame.Timeslot,
			},
			repeat: true,
		})
	}

	if len(targets) == 0 {
		if decision.Reason != "" {
			return Result{Reason: decision.Reason}
		}
		return Result{Reason: "nothing is configured to carry this talkgroup"}
	}

	src := sourceKey{peer: from, stream: frame.StreamID, slot: frame.Timeslot}
	if fromUpstream {
		// Distinguish streams arriving on different links that happen to share
		// a stream ID. Two networks pick stream IDs independently, so a
		// collision is a matter of time rather than malice.
		src.upstream = origin.Upstream
	}

	// A transmission opens and closes with the same frame type: DMR marks both
	// the voice header and the voice terminator as sync frames, and nothing in
	// the frame distinguishes them. Only position within the stream does.
	//
	// Without this check the opening frame would release the reservations it
	// had just taken, and two people keying up simultaneously would have their
	// audio interleaved. The same subtlety appears in internal/calls.
	opening := !c.holdsAnyFor(src)

	bridge := ""
	if len(decision.Bridges) > 0 {
		bridge = decision.Bridges[0]
	}

	// A transmission occupies its origin as well as its destinations.
	//
	// Without this, a colliding transmission is refused at the shared
	// destinations but still delivered to the first talker's own endpoint —
	// partial delivery of a collision, which is worse than either refusing it
	// outright or letting it through. On the air, two people keying the same
	// talkgroup are doubling, and exactly one of them should be relayed.
	if opening {
		if held, occupied := c.busy[origin]; !occupied || now.Sub(held.lastSeen) > c.timeout {
			c.busy[origin] = &reservation{source: src, lastSeen: now, bridge: bridge}
		}
	} else if held, ok := c.busy[origin]; ok && held.source == src {
		held.lastSeen = now
	}

	var res Result

	// One copy per peer, whatever combination of repeat and bridges named it.
	// A member on a talkgroup that is also bridged must not hear two of
	// everything.
	delivered := make(map[hbp.RepeaterID]bool, 8)

	for _, target := range targets {
		if target.Upstream != "" {
			// The loop rule. See RouteFromUpstream.
			if fromUpstream {
				res.Drops = append(res.Drops, Drop{
					To: target.Endpoint,
					Reason: fmt.Sprintf("arrived from upstream %s; a frame from a link is never "+
						"sent to a link", origin.Upstream),
				})
				continue
			}
			c.deliverUpstream(&res, target.Endpoint, frame, src, bridge, now)
			continue
		}

		for _, peer := range c.resolve(target.Endpoint) {
			// Repeat never sends a call back to the peer that transmitted it:
			// a hotspot hearing its own audio sounds exactly like a fault.
			//
			// Bridges are different and must not be given this rule. A member
			// on TG 9 bridged to TG 91 should hear TG 91 on their own hotspot,
			// including traffic they originated on TG 9 — that is the bridge
			// working, and the table already excludes an endpoint identical to
			// the origin.
			if target.repeat && peer == from {
				continue
			}

			dest := Endpoint{Peer: peer, Talkgroup: target.Talkgroup, Timeslot: target.Timeslot}

			held, occupied := c.busy[dest]
			switch {
			case occupied && held.source != src:
				if now.Sub(held.lastSeen) <= c.timeout {
					// Somebody else is already talking here. Interleaving two
					// transmissions produces audio nobody can understand, so
					// the later one is refused and counted.
					res.Drops = append(res.Drops, Drop{
						To: dest,
						Reason: fmt.Sprintf("already carrying a transmission from peer %d",
							held.source.peer),
					})
					continue
				}
				// The previous transmission went silent without a terminator.
				fallthrough
			case !occupied:
				c.busy[dest] = &reservation{source: src, lastSeen: now, bridge: bridge}
				res.StartedStreams = append(res.StartedStreams, dest)
			default:
				held.lastSeen = now
			}

			out := frame
			out.TargetID = target.Talkgroup
			out.Timeslot = target.Timeslot
			out.RepeaterID = peer
			// Trailing bytes are the sender's link-quality report and mean
			// nothing to the destination, but preserving them keeps the frame
			// byte-identical in shape and costs nothing.
			if len(frame.Trailing) > 0 {
				out.Trailing = append([]byte(nil), frame.Trailing...)
			}

			// One copy per peer. A member whose talkgroup is also bridged must
			// not hear two of everything.
			//
			// The reservation above is taken regardless, which matters: if
			// deduplication skipped it, that destination would look free to the
			// next transmission and two people's audio would interleave on it.
			if delivered[peer] {
				continue
			}
			delivered[peer] = true
			res.Deliveries = append(res.Deliveries, Delivery{Peer: peer, Frame: out, Bridge: bridge})
		}
	}

	if frame.FrameType == hbp.FrameTypeSync && !opening {
		// A terminator releases every destination this transmission held, so
		// the next person can key up immediately rather than waiting out the
		// timeout.
		c.release(src)
	}

	if len(res.Deliveries) == 0 && len(res.Upstreams) == 0 && res.Reason == "" {
		res.Reason = "every destination refused the frame"
	}
	return res
}

// resolve expands an endpoint into concrete destination peers.
// deliverUpstream applies the same contention rules to a link that a peer gets.
//
// An upstream is one destination rather than many, so there is no resolve step,
// but everything else holds: a link already carrying somebody else's
// transmission refuses this one, and the refusal is counted rather than silent.
func (c *Core) deliverUpstream(res *Result, target Endpoint, frame hbp.Data, src sourceKey, bridge string, now time.Time) {
	held, occupied := c.busy[target]
	switch {
	case occupied && held.source != src:
		if now.Sub(held.lastSeen) <= c.timeout {
			res.Drops = append(res.Drops, Drop{
				To:     target,
				Reason: fmt.Sprintf("already carrying a transmission from peer %d", held.source.peer),
			})
			return
		}
		fallthrough
	case !occupied:
		c.busy[target] = &reservation{source: src, lastSeen: now, bridge: bridge}
	default:
		held.lastSeen = now
	}

	out := frame
	out.TargetID = target.Talkgroup
	out.Timeslot = target.Timeslot
	// RepeaterID is left alone. On OpenBridge that field names the sending
	// server, and the encoder stamps the configured network ID — this package
	// does not know it and should not guess.
	if len(frame.Trailing) > 0 {
		out.Trailing = append([]byte(nil), frame.Trailing...)
	}

	res.Upstreams = append(res.Upstreams, UpstreamDelivery{
		Upstream: target.Upstream,
		Frame:    out,
		Bridge:   bridge,
	})
}

func (c *Core) resolve(target Endpoint) []hbp.RepeaterID {
	if target.Peer != AnyPeer {
		if c.peers.Ready(target.Peer) {
			return []hbp.RepeaterID{target.Peer}
		}
		return nil
	}
	return c.peers.ReadyPeers()
}

// holdsAnyFor reports whether a transmission already holds any destination.
//
// It distinguishes a transmission's opening sync frame from its terminator,
// which are indistinguishable by frame type alone.
func (c *Core) holdsAnyFor(src sourceKey) bool {
	for _, held := range c.busy {
		if held.source == src {
			return true
		}
	}
	return false
}

// release frees every destination held by a transmission.
func (c *Core) release(src sourceKey) {
	for dest, held := range c.busy {
		if held.source == src {
			delete(c.busy, dest)
		}
	}
}

// Expire frees destinations whose transmission stopped without a terminator.
//
// Without this, a peer that loses power mid-transmission would hold its
// destinations until the process restarted — the failure mode that welds a
// talkgroup open.
func (c *Core) Expire(now time.Time) []Endpoint {
	var freed []Endpoint
	for dest, held := range c.busy {
		if now.Sub(held.lastSeen) > c.timeout {
			freed = append(freed, dest)
			delete(c.busy, dest)
		}
	}
	sortEndpoints(freed)
	return freed
}

// BusyCount returns the number of destinations currently carrying traffic.
func (c *Core) BusyCount() int { return len(c.busy) }

// Busy returns the destinations currently carrying traffic, ordered.
func (c *Core) Busy() []Endpoint {
	out := make([]Endpoint, 0, len(c.busy))
	for dest := range c.busy {
		out = append(out, dest)
	}
	sortEndpoints(out)
	return out
}
