package routing_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// Layer 1: a master repeats. See docs/adr/ADR-0019-master-repeats.md.
//
// Everything QSP tested before this file was the bridging path. These are the
// tests for the thing a DMR network is actually for, and until ADR-0019 there
// was no configuration that expressed it.

func groupCall(stream hbp.StreamID, tg uint32, slot hbp.Timeslot, ft hbp.FrameType) hbp.Data {
	return hbp.Data{
		SourceID: 3132910, TargetID: tg, Timeslot: slot,
		CallType: hbp.CallGroup, FrameType: ft, StreamID: stream,
	}
}

// noBridges is a master with nothing configured — the ordinary starting point
// for a club, and the case that had no answer before.
func noBridges(t *testing.T, peers ...hbp.RepeaterID) *routing.Core {
	t.Helper()
	c, err := routing.NewCore(routing.CoreOptions{Peers: peersReady(peers...)})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return c
}

// TestFourHotspotsOnOneTalkgroupHearEachOther is the requirement in one test.
func TestFourHotspotsOnOneTalkgroupHearEachOther(t *testing.T) {
	const a, b, c, d hbp.RepeaterID = 3100001, 3100002, 3100003, 3100004
	core := noBridges(t, a, b, c, d)

	res := core.Route(a, groupCall(0x1111, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)

	if len(res.Deliveries) != 3 {
		t.Fatalf("%d deliveries, want 3 — every other hotspot on TG 9 (%s)",
			len(res.Deliveries), res.Reason)
	}

	heard := map[hbp.RepeaterID]bool{}
	for _, del := range res.Deliveries {
		heard[del.Peer] = true
		if del.Frame.TargetID != 9 {
			t.Errorf("peer %d received TG %d, want 9", del.Peer, del.Frame.TargetID)
		}
		if del.Frame.Timeslot != hbp.Timeslot2 {
			t.Errorf("peer %d received %s, want TS2", del.Peer, del.Frame.Timeslot)
		}
		if del.Frame.SourceID != 3132910 {
			t.Errorf("the originating radio's ID became %d", del.Frame.SourceID)
		}
		if del.Frame.RepeaterID != del.Peer {
			t.Errorf("frame for peer %d carries repeater ID %d; it must be rewritten",
				del.Peer, del.Frame.RepeaterID)
		}
	}
	for _, want := range []hbp.RepeaterID{b, c, d} {
		if !heard[want] {
			t.Errorf("peer %d did not hear the transmission", want)
		}
	}
	if heard[a] {
		t.Error("the transmitting hotspot heard its own audio come back")
	}
}

// TestRepeatIsPerTalkgroup. A member on TG 9 does not hear TG 91.
func TestRepeatIsPerTalkgroup(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b)

	res := core.Route(a, groupCall(0x2222, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("%d deliveries, want 1", len(res.Deliveries))
	}
	if got := res.Deliveries[0].Frame.TargetID; got != 9 {
		t.Errorf("repeated on TG %d, want the talkgroup it arrived on", got)
	}
}

// TestRepeatIsPerTimeslot. TG 9 on TS1 is not TG 9 on TS2.
func TestRepeatIsPerTimeslot(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b)

	res := core.Route(a, groupCall(0x3333, 9, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("%d deliveries, want 1", len(res.Deliveries))
	}
	if got := res.Deliveries[0].Frame.Timeslot; got != hbp.Timeslot1 {
		t.Errorf("repeated on %s, want the timeslot it arrived on", got)
	}
}

// TestRepeatRefusesASecondSimultaneousTalker.
//
// Contention (ADR-0014) applies to repeat exactly as it does to bridging. Two
// people doubling on a club talkgroup is the most ordinary collision there is.
func TestRepeatRefusesASecondSimultaneousTalker(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := noBridges(t, a, b, c)

	first := core.Route(a, groupCall(0x4444, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(first.Deliveries) != 2 {
		t.Fatalf("A's frame reached %d peers, want 2", len(first.Deliveries))
	}

	second := core.Route(b, groupCall(0x5555, 9, hbp.Timeslot2, hbp.FrameTypeSync),
		t0.Add(100*time.Millisecond))

	if len(second.Deliveries) != 0 {
		t.Errorf("B's audio interleaved with A's, reaching %d peers", len(second.Deliveries))
	}
	if len(second.Drops) == 0 {
		t.Error("the refusal was not recorded")
	}
}

// TestRepeatAndBridgeDeliverOneCopy.
//
// A peer that is both on the talkgroup and reachable through a bridge must hear
// the transmission once. Hearing two of everything is worse than hearing none,
// because it sounds like a fault in the radio rather than the network.
func TestRepeatAndBridgeDeliverOneCopy(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002

	tab := mustTable(t, routing.Bridge{
		Name: "overlap", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(a, 9, hbp.Timeslot2),
			ep(b, 9, hbp.Timeslot2),
		},
	})
	core := newCore(t, tab, a, b)

	res := core.Route(a, groupCall(0x6666, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)

	if len(res.Deliveries) != 1 {
		t.Fatalf("%d deliveries, want 1 — the bridge and repeat both name peer %d", len(res.Deliveries), b)
	}
}

// TestRepeatDoesNotCarryPrivateCalls.
//
// A private call is addressed to one radio. Repeating it to every peer would
// broadcast a conversation intended for one person.
func TestRepeatDoesNotCarryPrivateCalls(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b)

	frame := groupCall(0x7777, 3121380, hbp.Timeslot2, hbp.FrameTypeSync)
	frame.CallType = hbp.CallPrivate

	res := core.Route(a, frame, t0)
	if len(res.Deliveries) != 0 {
		t.Errorf("a private call was repeated to %d peers", len(res.Deliveries))
	}
}

// TestRepeatSkipsPeersThatAreNotReady.
//
// A peer that has logged in but not finished configuring cannot receive
// traffic, and writing to it would be writing into a session that does not
// exist yet.
func TestRepeatSkipsPeersThatAreNotReady(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b) // only a and b are ready

	res := core.Route(a, groupCall(0x8888, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	for _, del := range res.Deliveries {
		if del.Peer != b {
			t.Errorf("delivered to peer %d, which is not registered", del.Peer)
		}
	}
}

// TestNoRepeatLeavesBridgingAlone.
//
// Turning repeat off is a deliberate choice for an instance that only bridges
// between systems. It must not disturb the bridging path.
func TestNoRepeatLeavesBridgingAlone(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002

	tab := mustTable(t, routing.Bridge{
		Name: "net", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(a, 9, hbp.Timeslot2),
			ep(b, 91, hbp.Timeslot2),
		},
	})
	core, err := routing.NewCore(routing.CoreOptions{
		Table: tab, Peers: peersReady(a, b), NoRepeat: true,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	res := core.Route(a, groupCall(0x9999, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("%d deliveries, want 1 across the bridge (%s)", len(res.Deliveries), res.Reason)
	}
	if got := res.Deliveries[0].Frame.TargetID; got != 91 {
		t.Errorf("delivered on TG %d, want 91", got)
	}
}

// TestRepeatScalesToAClub. A hundred hotspots on one talkgroup.
func TestRepeatScalesToAClub(t *testing.T) {
	peers := make([]hbp.RepeaterID, 0, 100)
	for i := range 100 {
		peers = append(peers, hbp.RepeaterID(3100000+i))
	}
	core := noBridges(t, peers...)

	res := core.Route(peers[0], groupCall(0xAAAA, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)

	if len(res.Deliveries) != 99 {
		t.Fatalf("%d deliveries, want 99 — everyone but the talker", len(res.Deliveries))
	}
	seen := map[hbp.RepeaterID]bool{}
	for _, del := range res.Deliveries {
		if seen[del.Peer] {
			t.Fatalf("peer %d received two copies", del.Peer)
		}
		seen[del.Peer] = true
	}
}
