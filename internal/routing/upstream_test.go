package routing_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// A club bridging its local talkgroup to BrandMeister, exporting and importing
// the same one. This is the ordinary configuration, not an exotic one, and it
// is the configuration that loops without a rule against it.
func clubWithUpstream(t *testing.T) *routing.Core {
	t.Helper()

	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "bm-3148",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: routing.AnyPeer, Talkgroup: 3148, Timeslot: hbp.Timeslot2},
			{Upstream: "brandmeister", Talkgroup: 3148, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}

	core, err := routing.NewCore(routing.CoreOptions{
		Table: table,
		Peers: readyPeers{3132910, 3132911},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return core
}

func voice(streamID hbp.StreamID, source uint32, tg uint32) hbp.Data {
	return hbp.Data{
		SourceID:  source,
		TargetID:  tg,
		Timeslot:  hbp.Timeslot2,
		CallType:  hbp.CallGroup,
		FrameType: hbp.FrameTypeVoice,
		StreamID:  streamID,
	}
}

// TestLocalTrafficReachesTheUpstream — the point of the feature.
func TestLocalTrafficReachesTheUpstream(t *testing.T) {
	core := clubWithUpstream(t)

	res := core.Route(3132910, voice(0x1111, 3132910, 3148), time.Now())

	if len(res.Upstreams) != 1 {
		t.Fatalf("%d upstream deliveries, want 1 (%s)", len(res.Upstreams), res.Reason)
	}
	up := res.Upstreams[0]
	if up.Upstream != "brandmeister" {
		t.Errorf("delivered to %q, want brandmeister", up.Upstream)
	}
	if up.Frame.TargetID != 3148 {
		t.Errorf("upstream frame targets TG %d, want 3148", up.Frame.TargetID)
	}
	if up.Frame.SourceID != 3132910 {
		t.Errorf("upstream frame's source is %d; the originating radio must survive the link",
			up.Frame.SourceID)
	}

	// The other local peer hears it, because the master repeats TG 3148 — not
	// because the bridge carries it. That is ADR-0019's layer 1, and before it
	// existed this assertion was for zero deliveries.
	if len(res.Deliveries) != 1 {
		t.Fatalf("%d local deliveries, want 1: peers on a talkgroup hear each other", len(res.Deliveries))
	}
	if got := res.Deliveries[0].Peer; got != 3132911 {
		t.Errorf("repeated to peer %d, want 3132911", got)
	}
}

// TestUpstreamTrafficReachesLocalPeers — the other direction.
func TestUpstreamTrafficReachesLocalPeers(t *testing.T) {
	core := clubWithUpstream(t)

	res := core.RouteFromUpstream("brandmeister", voice(0x2222, 3121380, 3148), time.Now())

	if len(res.Deliveries) != 2 {
		t.Fatalf("%d local deliveries, want 2 (%s)", len(res.Deliveries), res.Reason)
	}
	for _, d := range res.Deliveries {
		if d.Frame.SourceID != 3121380 {
			t.Errorf("local frame's source is %d, want the originating radio 3121380", d.Frame.SourceID)
		}
		if d.Frame.RepeaterID != d.Peer {
			t.Errorf("frame for peer %d carries repeater ID %d; it must be rewritten for the destination",
				d.Peer, d.Frame.RepeaterID)
		}
	}
}

// TestAFrameIsNotEchoedToTheLinkItArrivedOn.
//
// The bridge exports and imports TG 3148 — the ordinary configuration — so
// without a rule every frame from BrandMeister goes straight back to
// BrandMeister, where the copy is indistinguishable from a new transmission.
//
// For the *same* link this is caught by the existing rule that a call is never
// sent back where it came from, which now applies to links because
// Endpoint.Matches distinguishes them. The loop rule in RouteFromUpstream
// covers the case that one cannot see: a *different* link, tested below.
func TestAFrameIsNotEchoedToTheLinkItArrivedOn(t *testing.T) {
	core := clubWithUpstream(t)

	res := core.RouteFromUpstream("brandmeister", voice(0x3333, 3121380, 3148), time.Now())

	for _, u := range res.Upstreams {
		t.Errorf("a frame from brandmeister was sent back to upstream %q", u.Upstream)
	}
}

// TestTwoUpstreamsDoNotRelayToEachOther.
//
// A club bridged to two networks must not become a path between them. That is
// re-bridging, which BrandMeister disconnects bridges for.
func TestTwoUpstreamsDoNotRelayToEachOther(t *testing.T) {
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "two-networks",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: routing.AnyPeer, Talkgroup: 3148, Timeslot: hbp.Timeslot2},
			{Upstream: "brandmeister", Talkgroup: 3148, Timeslot: hbp.Timeslot2},
			{Upstream: "neighbour-qsp", Talkgroup: 3148, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: readyPeers{3132910}})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	res := core.RouteFromUpstream("brandmeister", voice(0x4444, 3121380, 3148), time.Now())

	for _, u := range res.Upstreams {
		t.Errorf("a frame from brandmeister was relayed to upstream %q — that is re-bridging",
			u.Upstream)
	}

	// Refused rather than silently discarded. Constitution §18: every refusal
	// is explained, because an operator wondering why a bridge is quiet needs
	// to be told it is working as designed.
	explained := false
	for _, d := range res.Drops {
		if d.To.Upstream == "neighbour-qsp" {
			explained = true
			if d.Reason == "" {
				t.Error("the refusal carries no reason")
			}
		}
	}
	if !explained {
		t.Error("re-bridging was prevented without recording why")
	}

	if len(res.Deliveries) != 1 {
		t.Errorf("%d local deliveries, want 1; local peers should still hear it", len(res.Deliveries))
	}
}

// TestAnUpstreamTakesPartInContention.
//
// A link carrying one transmission must refuse a second, exactly as a peer
// does. Two streams interleaved on one talkgroup is audio nobody can follow,
// and it is worse when the far end is a network of strangers.
func TestAnUpstreamTakesPartInContention(t *testing.T) {
	core := clubWithUpstream(t)
	now := time.Now()

	first := core.Route(3132910, voice(0xAAAA, 3132910, 3148), now)
	if len(first.Upstreams) != 1 {
		t.Fatalf("the first transmission did not reach the upstream: %s", first.Reason)
	}

	second := core.Route(3132911, voice(0xBBBB, 3132911, 3148), now.Add(60*time.Millisecond))

	if len(second.Upstreams) != 0 {
		t.Error("a second simultaneous transmission was also sent upstream")
	}
	refused := false
	for _, d := range second.Drops {
		if d.To.Upstream == "brandmeister" {
			refused = true
		}
	}
	if !refused {
		t.Error("the upstream did not refuse the colliding transmission")
	}
}

// TestStreamIDsCollidingAcrossNetworksAreDistinguished.
//
// Two networks choose stream IDs independently, so a frame from BrandMeister
// and a local transmission can share one. Treating them as the same stream
// would interleave two people's audio.
func TestStreamIDsCollidingAcrossNetworksAreDistinguished(t *testing.T) {
	core := clubWithUpstream(t)
	now := time.Now()

	const shared hbp.StreamID = 0xC0FFEE

	local := core.Route(3132910, voice(shared, 3132910, 3148), now)
	if len(local.Upstreams) != 1 {
		t.Fatalf("the local transmission did not reach the upstream: %s", local.Reason)
	}

	// The same stream ID arriving from the far end is a different transmission.
	remote := core.RouteFromUpstream("brandmeister", voice(shared, 3121380, 3148), now.Add(60*time.Millisecond))

	refused := false
	for _, d := range remote.Drops {
		if d.To.Peer != 0 || d.To.Talkgroup == 3148 {
			refused = true
		}
	}
	if len(remote.Deliveries) > 0 && !refused {
		t.Error("a colliding stream ID from another network was treated as the same transmission")
	}
}

// TestEndpointStringNamesTheLink, because these appear in drop reasons an
// operator reads.
func TestEndpointStringNamesTheLink(t *testing.T) {
	e := routing.Endpoint{Upstream: "brandmeister", Talkgroup: 3148, Timeslot: hbp.Timeslot2}
	if got := e.String(); got == "" || got == "any TG3148 TS2" {
		t.Errorf("Endpoint.String() = %q; an upstream must not read as a peer", got)
	}
}

// readyPeers is a fixed set of registered peers.
type readyPeers []hbp.RepeaterID

func (r readyPeers) Ready(id hbp.RepeaterID) bool {
	for _, p := range r {
		if p == id {
			return true
		}
	}
	return false
}

func (r readyPeers) ReadyPeers() []hbp.RepeaterID { return r }
