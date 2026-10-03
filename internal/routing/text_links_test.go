package routing_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// A text is a run of data bursts, each with its own stream ID, and every one
// of them must reach every kind of destination. Peers have been exempt from
// contention with themselves since texts first worked; links and transcoders
// were not, so a text left for a link as its first burst and nothing else.
//
// To see it fail: remove the sameOrigin case from deliverUpstream (the link
// gets 1 of 17) or from deliverTranscoder (the transcoder gets 1 of 17).
func TestEveryBurstOfATextReachesEveryDestination(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	table := mustTable(t,
		routing.Bridge{Name: "to-bm", Enabled: true, Endpoints: []routing.Endpoint{
			{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			{Upstream: "brandmeister", Talkgroup: 31672, Timeslot: hbp.Timeslot1},
		}},
		routing.Bridge{Name: "to-zello", Enabled: true, Endpoints: []routing.Endpoint{
			{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			{Transcoder: "zello", Talkgroup: 2, Timeslot: hbp.Timeslot2},
		}},
	)
	c, err := routing.NewCore(routing.CoreOptions{
		Table: table, Peers: peersReady(a, b), QSPLinks: []string{"iowa"},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	const bursts = 17
	got := map[string]int{}
	for i := range bursts {
		f := groupCall(hbp.StreamID(0x4000+i), 2, hbp.Timeslot2, hbp.FrameTypeSync)
		f.DataType = 0x7
		if i == 0 {
			f.DataType = 0x6
		}
		res := c.Route(a, f, t0.Add(time.Duration(i)*60*time.Millisecond))
		for _, d := range res.Deliveries {
			got["hotspot"] += btoi(d.Peer == b)
		}
		for _, u := range res.Upstreams {
			got[u.Upstream]++
		}
		for _, tr := range res.Transcoders {
			got[tr.Transcoder]++
		}
	}
	for _, dest := range []string{"hotspot", "iowa", "brandmeister", "zello"} {
		if got[dest] != bursts {
			t.Errorf("%s received %d of %d bursts", dest, got[dest], bursts)
		}
	}

	// And contention still does its job: somebody else's voice on the same
	// link and talkgroup is refused while the first holds it.
	c2, _ := routing.NewCore(routing.CoreOptions{Peers: peersReady(a, b), QSPLinks: []string{"iowa"}})
	c2.Route(a, groupCall(0x5000, 2, hbp.Timeslot2, hbp.FrameTypeVoice), t0)
	res := c2.Route(b, groupCall(0x5001, 2, hbp.Timeslot2, hbp.FrameTypeVoice), t0.Add(60*time.Millisecond))
	if len(res.Upstreams) != 0 {
		t.Errorf("a second station's voice was sent over a link already carrying the first's")
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// A list of the talkgroups a server carries is not a ban on private calls. A
// private call's target is a radio ID, and testing it as a talkgroup refused
// every private call and private text on any server with an allow list.
//
// To see it fail: make talkgroupAllowed ignore the call type.
func TestATalkgroupAllowListDoesNotRefusePrivateCalls(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	lists := talkgroups(t, 2, access.ModePermit, "2", "9")
	c, err := routing.NewCore(routing.CoreOptions{
		Access: lists, Peers: peersReady(a, b),
		Subscribers: stubSubscribers{3121002: {peer: b, slot: hbp.Timeslot2}},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	res := c.Route(a, privateCall(0x2222, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 || res.Deliveries[0].Peer != b {
		t.Fatalf("a private call under a talkgroup allow list: %d deliveries, reason %q", len(res.Deliveries), res.Reason)
	}
	// The list still does what it is for.
	if res := c.Route(a, groupCall(0x2223, 3100, hbp.Timeslot2, hbp.FrameTypeSync), t0); len(res.Deliveries) != 0 {
		t.Errorf("a group call on a talkgroup not listed was carried")
	}
}
