package routing_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestAServerTextReachesEveryPeerOnTheTalkgroup: a transmission QSP composed
// has no peer to leave out, so repeat delivers it to every peer on the
// talkgroup, each copy addressed to its own peer.
//
// To see it fail: make RouteFromServer route from AnyPeer rather than
// ServerOrigin, or from a real peer's ID, and one delivery disappears.
func TestAServerTextReachesEveryPeerOnTheTalkgroup(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := noBridges(t, a, b, c)

	res := core.RouteFromServer(groupCall(0x4444, 2, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 3 {
		t.Fatalf("delivered to %d peers, want all 3: %+v", len(res.Deliveries), res)
	}
	for _, d := range res.Deliveries {
		if d.Frame.RepeaterID != d.Peer {
			t.Errorf("the copy for %d names repeater %d", d.Peer, d.Frame.RepeaterID)
		}
	}
}

// TestAServerTextHonoursAttachment: it is routed like anything else on the
// talkgroup, so a peer not attached to it does not receive it.
func TestAServerTextHonoursAttachment(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := coreWithAttachments(t, stubAttached{{b, 2, hbp.Timeslot2}: true}, nil, a, b)

	res := core.RouteFromServer(groupCall(0x5555, 2, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 || res.Deliveries[0].Peer != b {
		t.Fatalf("deliveries %+v, want only %d", res.Deliveries, b)
	}
}
