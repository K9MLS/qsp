package routing

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/access"
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

// SubscriberLookup reports which peer a radio was last heard through.
//
// It is a separate interface from PeerLookup because it answers a different
// kind of question and may not be available: an instance that does not track
// radios still routes group calls perfectly well, and a nil lookup means
// private calls are simply not routed rather than that routing fails.
type SubscriberLookup interface {
	// Locate returns the peer and timeslot a radio was last heard on, and
	// whether that is currently usable. It reports false for a radio never
	// heard, one whose location has aged out, and one whose peer has gone.
	LocateFor(subscriber uint32) (peer hbp.RepeaterID, slot hbp.Timeslot, ok bool)
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

// Subscriptions reports which peers want which talkgroups.
//
// It is separate from PeerLookup because it answers a question about the
// member's wishes rather than the peer's readiness, and because an instance
// that does not use it still routes correctly: a nil Subscriptions delivers
// every talkgroup to every ready peer, which is what QSP did before per-peer
// attachment existed. See ADR-0023.
type Subscriptions interface {
	// Attached reports whether a peer should receive a talkgroup on a slot.
	Attached(peer hbp.RepeaterID, talkgroup uint32, slot hbp.Timeslot) bool
}

// reservation records a destination currently receiving a transmission.
type reservation struct {
	// source identifies the transmission holding this destination.
	source   sourceKey
	lastSeen time.Time
	bridge   string
	// voice records that a voice transmission took this reservation.
	//
	// **Only voice can be abandoned.** A voice transmission that stops
	// without a terminator has gone wrong — a lossy link, a peer that lost
	// power — and freeing its destinations is worth telling an operator
	// about. A run of data bursts always ends this way, because data has no
	// terminator and is not meant to, so the same warning about one is a
	// false alarm. Four of them per text message is how a warning stops being
	// read at all.
	voice bool
	// endpoint is the destination in full, including the talkgroup that the
	// contention key deliberately leaves out. Kept so that a drop can name what
	// is already on the slot, and so Busy stays useful to an operator.
	endpoint Endpoint
}

// contend reduces a destination to the thing that can carry one transmission at
// a time.
//
// **For a peer that is the timeslot, not the talkgroup.** A DMR timeslot is one
// TDMA channel; two talkgroups arriving on it produce interleaved audio nobody
// can understand. Keying reservations on the talkgroup made those two separate
// destinations and delivered both. See ADR-0022.
//
// **A link keeps the talkgroup in its key.** Contention models a physical
// constraint, and an OpenBridge link is an IP socket rather than a radio
// channel — BrandMeister carries several talkgroups concurrently over one. This
// asymmetry is deliberate; the ADR is the reasoning to read before unifying
// them for tidiness.
func contend(e Endpoint) Endpoint {
	if e.Upstream != "" {
		return e
	}
	e.Talkgroup = 0
	return e
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
// The routing decision itself stays pure; this type adds only the state that
// decision cannot have — what is in flight.
//
// # Why this is locked
//
// It was documented as owned by the goroutine that reads the socket, and that
// was true of peer traffic. It was never true of everything: an upstream link
// calls DeliverFromUpstream from the link's own read goroutine, so a frame from
// another network and a frame from a hotspot have been able to enter Route
// concurrently since links were built. The race never fired because no upstream
// has met a real far end and no test ran a link and a peer together under the
// detector. The IPSC listener made it reproducible in a second, which is how it
// was found.
//
// So the invariant is enforced here rather than asserted in prose. The lock
// covers what changes under traffic — the reservations, the table and the
// access lists — and not the collaborators fixed at construction.
type Core struct {
	repeat bool

	// access holds the talkgroup lists. The registration and subscriber lists
	// are in the same value and are not consulted here: they are questions
	// about who is talking, answered at the master where the sender is known.
	access access.Lists
	table  *Table
	peers  PeerLookup
	// subscribers locates a radio for a private call. Nil means private calls
	// are not routed, which is a working configuration rather than a fault.
	subscribers SubscriberLookup
	// attached reports which talkgroups a peer wants. Nil means all of them.
	attached Subscriptions
	timeout  time.Duration

	// mu guards access, table and busy. Every other field is set once in
	// NewCore and only read.
	mu sync.Mutex

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

	// Access holds the talkgroup lists. The zero value permits everything.
	Access access.Lists
	// Table is the routing table. May be nil, meaning nothing is routed.
	Table *Table
	// Peers resolves destinations. Required.
	Peers PeerLookup
	// Subscribers locates a radio for a private call. Optional: nil means
	// private calls are refused with an explanation rather than routed.
	Subscribers SubscriberLookup
	// Attached reports which talkgroups a peer wants. Optional: nil delivers
	// every talkgroup to every ready peer.
	Attached Subscriptions
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
		repeat:      !opts.NoRepeat,
		access:      opts.Access,
		table:       opts.Table,
		peers:       opts.Peers,
		subscribers: opts.Subscribers,
		attached:    opts.Attached,
		timeout:     opts.Timeout,
		busy:        make(map[Endpoint]*reservation),
	}, nil
}

// SetTable swaps in a new routing table atomically.
//
// In-flight transmissions keep their existing reservations: a configuration
// change must not cut somebody off mid-sentence. New frames are routed against
// the new table, so the change takes effect at the next transmission boundary
// (clarification R4).
func (c *Core) SetTable(t *Table) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.table = t
}

// SetAccess swaps in new talkgroup lists.
//
// **Unlike SetTable, this takes effect on the next frame rather than the next
// transmission.** A configuration change must not cut somebody off
// mid-sentence, but an operator removing a talkgroup from a permit list is
// intervening in something happening now, and a refusal that waits politely for
// the offender to stop is not a refusal. ADR-0020 records the difference so it
// does not later read as an inconsistency to be tidied away.
//
// Existing reservations are left alone. A destination that stops being
// permitted mid-transmission simply receives nothing further, and its
// reservation expires on the ordinary timeout.
func (c *Core) SetAccess(l access.Lists) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.access = l
}

// Table returns the active routing table.
//
// It is safe to call from any goroutine, but it is still not an observation
// hook: the value it returns is a snapshot that SetTable may replace a moment
// later. For anything a console renders, use the owner's published snapshot —
// see peers.Listener.EnabledBridges.
func (c *Core) Table() *Table {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.table
}

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
	c.mu.Lock()
	defer c.mu.Unlock()

	from := origin.Peer
	fromUpstream := origin.Upstream != ""

	// Ingress. A talkgroup this instance does not carry on this timeslot goes
	// no further, whether it arrived from a peer or over a link.
	//
	// This is not made redundant by the egress check below. A bridge
	// translates, so a frame arriving on TG 9 and leaving on TG 91 is tested
	// against two different entries — and the arriving talkgroup is the one an
	// operator means when they say which talkgroups their network carries.
	if !c.access.Talkgroups(int(origin.Timeslot)).Allows(origin.Talkgroup) {
		return Result{Reason: fmt.Sprintf(
			"talkgroup %d on TS%d is not permitted by dmr.access.talkgroups",
			origin.Talkgroup, origin.Timeslot)}
	}

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

	// A private call goes to one radio, wherever that radio is.
	//
	// This is the same question repeat answers — who else should hear this? —
	// with a different kind of answer. Repeat resolves a talkgroup to every
	// other peer on it; a private call resolves a subscriber to the single peer
	// it is behind. Both are layer 1, which is why this is not a sixth layer.
	// See ADR-0021.
	//
	// The destination keeps the *called radio's* ID as its target, because that
	// is what makes the receiving radio open its squelch. Only the timeslot is
	// taken from where the radio was last heard, since a peer's two slots are
	// independent paths and the call has to pick the one the radio is using.
	if c.repeat && frame.CallType == hbp.CallPrivate {
		if c.subscribers == nil {
			return Result{Reason: "private calls are not routed by this instance"}
		}
		peer, slot, found := c.subscribers.LocateFor(frame.TargetID)
		if !found {
			// Naming the radio matters: "not heard recently" is something an
			// operator can act on, and silence is not.
			return Result{Reason: fmt.Sprintf(
				"radio %d has not been heard recently, so there is nowhere to send a private call to it",
				frame.TargetID)}
		}
		targets = append(targets, routeTarget{
			Endpoint: Endpoint{Peer: peer, Talkgroup: frame.TargetID, Timeslot: slot},
			repeat:   true,
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
	originKey := contend(origin)
	if opening {
		if held, occupied := c.busy[originKey]; !occupied || now.Sub(held.lastSeen) > c.timeout {
			c.busy[originKey] = &reservation{source: src, lastSeen: now, bridge: bridge,
				endpoint: origin, voice: contendsForSlot(frame)}
		}
	} else if held, ok := c.busy[originKey]; ok && held.source == src {
		held.lastSeen = now
		// A transmission that began as data and then carried audio is a voice
		// transmission: whatever opened the reservation, what can be
		// abandoned is the audio.
		held.voice = held.voice || contendsForSlot(frame)
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

			// Egress. Checked per destination because the destination's
			// talkgroup is not necessarily the one the frame arrived on, and
			// because traffic reaching this point from a bridge or a link
			// never crossed the ingress test at all.
			//
			// The refusal is recorded as a Drop rather than skipped, so the
			// console shows it the way it shows any other refused destination.
			// Nothing is reserved: a destination refused by an access list is
			// not carrying this transmission and must stay free for the next.
			if !c.access.Talkgroups(int(dest.Timeslot)).Allows(dest.Talkgroup) {
				res.Drops = append(res.Drops, Drop{
					To: dest,
					Reason: fmt.Sprintf("talkgroup %d on TS%d is not permitted by "+
						"dmr.access.talkgroups", dest.Talkgroup, dest.Timeslot),
				})
				continue
			}

			// Layer 3. Access control decides what this instance is willing to
			// carry; this decides what the member wants to hear, and it runs
			// second so that nobody can subscribe their way past a refusal.
			//
			// A bridge is a separate route to the same peer and is not subject
			// to it: an operator who bridged a talkgroup to somebody has
			// already said it should arrive. Only repeat consults attachment.
			if target.repeat && c.attached != nil &&
				!c.attached.Attached(peer, dest.Talkgroup, dest.Timeslot) {
				res.Drops = append(res.Drops, Drop{
					To: dest,
					Reason: fmt.Sprintf("peer %d is not attached to talkgroup %d on TS%d",
						peer, dest.Talkgroup, dest.Timeslot),
				})
				continue
			}

			destKey := contend(dest)

			// **A data burst does not contend.** A text message is thirty
			// short bursts, each carrying its own stream ID, so contention
			// treated every one as a different person keying up: the first
			// reserved the destination and the rest were refused until the
			// reservation lapsed two seconds later. Observed on a live network
			// as seventeen frames offered and two delivered, which is why a
			// message needed endless retries and usually failed.
			//
			// Contention exists to stop two people's audio interleaving.
			// Nothing about a data burst interleaves: it is one frame, the
			// radio reassembles the message, and blocking it achieves nothing
			// but losing it.
			held, occupied := c.busy[destKey]
			switch {
			case occupied && held.source != src && sameOrigin(held.source, src) &&
				!contendsForSlot(frame):
				// A data burst from the peer already holding this destination.
				// Delivered without disturbing the reservation and without
				// being refused by it.

			case occupied && held.source != src:
				if now.Sub(held.lastSeen) <= c.timeout {
					// Somebody else is already talking on this slot.
					// Interleaving two transmissions produces audio nobody can
					// understand, so the later one is refused and counted.
					//
					// The reason names the talkgroup already there, which is
					// often a different one from this frame's — that is the
					// whole point of ADR-0022, and an operator seeing only
					// "busy" would have no idea what took the slot.
					res.Drops = append(res.Drops, Drop{
						To: dest,
						Reason: fmt.Sprintf("timeslot already carrying TG %d from peer %d",
							held.endpoint.Talkgroup, held.source.peer),
					})
					continue
				}
				// The previous transmission went silent without a terminator.
				fallthrough
			case !occupied:
				c.busy[destKey] = &reservation{source: src, lastSeen: now, bridge: bridge,
					endpoint: dest, voice: contendsForSlot(frame)}
				res.StartedStreams = append(res.StartedStreams, dest)
			default:
				held.lastSeen = now
				held.voice = held.voice || contendsForSlot(frame)
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

	if frame.IsTerminator() && !opening {
		// A terminator releases every destination this transmission held, so
		// the next person can key up immediately rather than waiting out the
		// timeout.
		//
		// **A text message is not a terminator**, and this tested the frame
		// type alone. Since ADR-0045 every burst of a text after the first
		// released the reservation mid-message, so somebody else could key up
		// and interleave with a transmission still in progress — which is the
		// exact thing contention exists to prevent.
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
	// A link is a destination like any other, and a talkgroup this instance
	// does not carry should not be exported to somebody else's network either.
	if !c.access.Talkgroups(int(target.Timeslot)).Allows(target.Talkgroup) {
		res.Drops = append(res.Drops, Drop{
			To: target,
			Reason: fmt.Sprintf("talkgroup %d on TS%d is not permitted by dmr.access.talkgroups",
				target.Talkgroup, target.Timeslot),
		})
		return
	}

	// A link keeps the talkgroup in its contention key: it is an IP socket
	// rather than a radio channel and can carry several talkgroups at once.
	key := contend(target)
	held, occupied := c.busy[key]
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
		c.busy[key] = &reservation{source: src, lastSeen: now, bridge: bridge,
			endpoint: target, voice: contendsForSlot(frame)}
	default:
		held.lastSeen = now
		held.voice = held.voice || contendsForSlot(frame)
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
	for key, held := range c.busy {
		if held.source == src {
			delete(c.busy, key)
		}
	}
}

// Expire frees destinations whose transmission stopped without a terminator.
//
// Without this, a peer that loses power mid-transmission would hold its
// destinations until the process restarted — the failure mode that welds a
// talkgroup open.
func (c *Core) Expire(now time.Time) []Freed {
	c.mu.Lock()
	defer c.mu.Unlock()

	var freed []Freed
	for key, held := range c.busy {
		if now.Sub(held.lastSeen) > c.timeout {
			// The stored endpoint, not the key: the key omits the talkgroup
			// for a peer, and an operator told a nameless slot was freed
			// learns nothing.
			freed = append(freed, Freed{Endpoint: held.endpoint, Voice: held.voice})
			delete(c.busy, key)
		}
	}
	sort.Slice(freed, func(i, j int) bool {
		return endpointBefore(freed[i].Endpoint, freed[j].Endpoint)
	})
	return freed
}

// Freed is a destination the expiry released, and what was holding it.
//
// **The call type travels with it because the caller has to say something
// different about each.** Audio that stops without a terminator is worth an
// operator's attention; a run of data bursts ending that way is how data always
// ends.
type Freed struct {
	Endpoint Endpoint
	Voice    bool
}

// BusyCount returns the number of destinations currently carrying traffic.
func (c *Core) BusyCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.busy)
}

// Busy returns the destinations currently carrying traffic, ordered.
func (c *Core) Busy() []Endpoint {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Endpoint, 0, len(c.busy))
	for _, held := range c.busy {
		out = append(out, held.endpoint)
	}
	sortEndpoints(out)
	return out
}

// contendsForSlot reports whether a frame competes for a destination.
//
// **Only voice does.** Contention exists so that two people's audio does not
// interleave into something nobody can understand, and that is a property of
// voice alone. A data burst is a single self-contained frame with its own
// stream ID; treating a sequence of them as competing transmissions is what
// made text messages fail.
//
// A voice header is FrameTypeSync and does not reserve here either, so a
// reservation is taken by the first voice frame that follows it — one frame,
// sixty milliseconds, later than it could be. That is the cost of not needing
// to tell a voice header from a data burst, which share a frame type and
// cannot be told apart from one frame alone.
func contendsForSlot(frame hbp.Data) bool {
	return frame.FrameType == hbp.FrameTypeVoice || frame.FrameType == hbp.FrameTypeVoiceSync
}

// sameOrigin reports whether two source keys are the same station, ignoring
// which stream they belong to.
//
// **One radio's successive data bursts are one station, not many.** A text
// message is thirty short bursts, each with its own stream ID, so the
// contention key saw thirty different sources: the first reserved the
// destination and the rest were refused until it lapsed. Observed on a live
// network as seventeen frames offered and two delivered, which is why a message
// needed endless retries and usually failed.
//
// The stream is still part of the key for everything else, because two
// genuinely different transmissions must contend, and two networks choosing the
// same stream ID must not look like one.
func sameOrigin(a, b sourceKey) bool {
	return a.peer == b.peer && a.slot == b.slot && a.upstream == b.upstream
}
