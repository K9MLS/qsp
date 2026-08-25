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
	// Deliveries are the frames to send.
	Deliveries []Delivery
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
}

// Core applies a routing table to live traffic.
//
// It is not safe for concurrent use and is owned by the goroutine that reads
// the socket, consistent with ADR-0002. The routing decision itself stays pure;
// this type adds only the state that decision cannot have — what is in flight.
type Core struct {
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
	origin := Endpoint{Peer: from, Talkgroup: frame.TargetID, Timeslot: frame.Timeslot}
	decision := c.table.Route(origin)
	if !decision.Routed() {
		return Result{Reason: decision.Reason}
	}

	src := sourceKey{peer: from, stream: frame.StreamID, slot: frame.Timeslot}

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
	for _, target := range decision.Targets {
		for _, peer := range c.resolve(target) {
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

			res.Deliveries = append(res.Deliveries, Delivery{Peer: peer, Frame: out, Bridge: bridge})
		}
	}

	if frame.FrameType == hbp.FrameTypeSync && !opening {
		// A terminator releases every destination this transmission held, so
		// the next person can key up immediately rather than waiting out the
		// timeout.
		c.release(src)
	}

	if len(res.Deliveries) == 0 && res.Reason == "" {
		res.Reason = "every destination refused the frame"
	}
	return res
}

// resolve expands an endpoint into concrete destination peers.
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
