package routing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// stubPeers is a fixed set of ready peers.
type stubPeers struct{ ready map[hbp.RepeaterID]bool }

func peersReady(ids ...hbp.RepeaterID) stubPeers {
	m := make(map[hbp.RepeaterID]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return stubPeers{ready: m}
}

func (s stubPeers) Ready(id hbp.RepeaterID) bool { return s.ready[id] }

func (s stubPeers) ReadyPeers() []hbp.RepeaterID {
	out := make([]hbp.RepeaterID, 0, len(s.ready))
	for id := range s.ready {
		out = append(out, id)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

var t0 = time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)

func voiceFrame(stream hbp.StreamID, tg uint32, ts hbp.Timeslot, ft hbp.FrameType) hbp.Data {
	return hbp.Data{
		SourceID: 3132910, TargetID: tg, RepeaterID: peerA,
		Timeslot: ts, CallType: hbp.CallGroup, FrameType: ft,
		StreamID: stream, Trailing: []byte{0x11, 0x22},
	}
}

func newCore(t *testing.T, tab *routing.Table, ready ...hbp.RepeaterID) *routing.Core {
	t.Helper()
	c, err := routing.NewCore(routing.CoreOptions{Table: tab, Peers: peersReady(ready...)})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return c
}

func simpleBridge(t *testing.T) *routing.Table {
	t.Helper()
	return mustTable(t, routing.Bridge{
		Name: "net", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1),
			ep(peerB, 91, hbp.Timeslot2),
		},
	})
}

// TestFrameIsRewrittenForItsDestination.
//
// A bridge that joins different talkgroups must translate: the frame arrives on
// 3148/TS1 and must leave as 91/TS2, or the destination peer will not recognise
// it as traffic for that talkgroup.
func TestFrameIsRewrittenForItsDestination(t *testing.T) {
	c := newCore(t, simpleBridge(t), peerA, peerB)

	res := c.Route(peerA, voiceFrame(0xAAAA, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("got %d deliveries, want 1 (%s)", len(res.Deliveries), res.Reason)
	}

	d := res.Deliveries[0]
	if d.Peer != peerB {
		t.Errorf("delivered to %d, want %d", d.Peer, peerB)
	}
	if d.Frame.TargetID != 91 {
		t.Errorf("target talkgroup = %d, want 91", d.Frame.TargetID)
	}
	if d.Frame.Timeslot != hbp.Timeslot2 {
		t.Errorf("timeslot = %s, want TS2", d.Frame.Timeslot)
	}
	if d.Frame.RepeaterID != peerB {
		t.Errorf("repeater ID = %d, want the destination peer %d", d.Frame.RepeaterID, peerB)
	}
	// The originating radio must survive: it is the identity the receiving
	// operator sees, and rewriting it would misattribute the transmission.
	if d.Frame.SourceID != 3132910 {
		t.Errorf("source radio ID = %d, want it preserved", d.Frame.SourceID)
	}
	if d.Frame.StreamID != 0xAAAA {
		t.Errorf("stream ID = %s, want it preserved so the destination can group frames", d.Frame.StreamID)
	}
	if len(d.Frame.Trailing) != 2 {
		t.Errorf("trailing bytes = %d, want 2", len(d.Frame.Trailing))
	}
}

// TestFrameIsNeverDeliveredToItsSource, end to end through the core.
func TestFrameIsNeverDeliveredToItsSource(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "wide", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(routing.AnyPeer, 3148, hbp.Timeslot1),
			ep(peerC, 91, hbp.Timeslot2),
		},
	})
	c := newCore(t, tab, peerA, peerB, peerC)

	res := c.Route(peerA, voiceFrame(0xBBBB, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	for _, d := range res.Deliveries {
		if d.Peer == peerA && d.Frame.TargetID == 3148 && d.Frame.Timeslot == hbp.Timeslot1 {
			t.Fatal("a frame was delivered back to its own source endpoint")
		}
	}
}

// TestAnyPeerFansOutToEveryReadyPeer.
func TestAnyPeerFansOutToEveryReadyPeer(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "club", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1),
			ep(routing.AnyPeer, 91, hbp.Timeslot2),
		},
	})
	c := newCore(t, tab, peerA, peerB, peerC)

	res := c.Route(peerA, voiceFrame(0xCCCC, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 3 {
		t.Fatalf("got %d deliveries, want 3 (every ready peer on TG91/TS2)", len(res.Deliveries))
	}
}

// TestUnregisteredDestinationIsSkipped.
func TestUnregisteredDestinationIsSkipped(t *testing.T) {
	c := newCore(t, simpleBridge(t), peerA) // peerB is not connected

	res := c.Route(peerA, voiceFrame(0xDDDD, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Fatalf("delivered to a peer that is not registered: %+v", res.Deliveries)
	}
	if res.Reason == "" {
		t.Error("nothing was delivered and no reason was given")
	}
}

// TestSimultaneousKeyupsDoNotInterleave is the contention property.
//
// Two people keying up on different repeaters bridged to the same talkgroup
// would otherwise have their audio interleaved into something nobody can
// understand. The second is refused and counted, never silently dropped.
func TestSimultaneousKeyupsDoNotInterleave(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "shared", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1),
			ep(peerB, 3148, hbp.Timeslot1),
			ep(peerC, 91, hbp.Timeslot2),
		},
	})
	c := newCore(t, tab, peerA, peerB, peerC)

	// A starts talking.
	res := c.Route(peerA, voiceFrame(0x1111, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 2 {
		t.Fatalf("A's first frame produced %d deliveries, want 2", len(res.Deliveries))
	}

	// B keys up while A is still talking.
	res = c.Route(peerB, voiceFrame(0x2222, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0.Add(100*time.Millisecond))
	if len(res.Deliveries) != 0 {
		t.Fatalf("B's audio was interleaved with A's, reaching %d destination(s): %+v",
			len(res.Deliveries), res.Deliveries)
	}
	if len(res.Drops) == 0 {
		t.Fatal("the refused frame produced no drop record")
	}
	for _, d := range res.Drops {
		if !strings.Contains(d.Reason, "already carrying") {
			t.Errorf("drop reason = %q", d.Reason)
		}
	}

	// A continues, unaffected.
	res = c.Route(peerA, voiceFrame(0x1111, 3148, hbp.Timeslot1, hbp.FrameTypeVoice), t0.Add(120*time.Millisecond))
	if len(res.Deliveries) != 2 {
		t.Errorf("A's continuation was disrupted: %d deliveries", len(res.Deliveries))
	}
}

// TestTerminatorReleasesDestinationsImmediately.
//
// Waiting out the timeout after a clean unkey would make every exchange feel
// broken.
func TestTerminatorReleasesDestinationsImmediately(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "shared", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1),
			ep(peerB, 3148, hbp.Timeslot1),
		},
	})
	c := newCore(t, tab, peerA, peerB)

	c.Route(peerA, voiceFrame(0x3333, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	c.Route(peerA, voiceFrame(0x3333, 3148, hbp.Timeslot1, hbp.FrameTypeVoice), t0.Add(60*time.Millisecond))
	if c.BusyCount() == 0 {
		t.Fatal("nothing was reserved during a transmission")
	}

	// The terminator.
	c.Route(peerA, voiceFrame(0x3333, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0.Add(120*time.Millisecond))
	if c.BusyCount() != 0 {
		t.Fatalf("%d destinations still reserved after the terminator: %v", c.BusyCount(), c.Busy())
	}

	// B can key up straight away.
	res := c.Route(peerB, voiceFrame(0x4444, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0.Add(130*time.Millisecond))
	if len(res.Deliveries) == 0 {
		t.Errorf("B could not transmit immediately after A unkeyed: %s", res.Reason)
	}
}

// TestAbandonedTransmissionReleasesAfterTimeout.
//
// A peer that loses power mid-transmission would otherwise hold its
// destinations until the process restarted, which is how a talkgroup gets
// welded open.
func TestAbandonedTransmissionReleasesAfterTimeout(t *testing.T) {
	c, err := routing.NewCore(routing.CoreOptions{
		Table: simpleBridge(t), Peers: peersReady(peerA, peerB), Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	// Two reservations: the destination, and the transmission's own origin.
	c.Route(peerA, voiceFrame(0x5555, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if c.BusyCount() != 2 {
		t.Fatalf("busy = %d, want 2 (origin plus destination)", c.BusyCount())
	}

	if freed := c.Expire(t0.Add(900 * time.Millisecond)); len(freed) != 0 {
		t.Fatalf("released early: %v", freed)
	}
	freed := c.Expire(t0.Add(1100 * time.Millisecond))
	if len(freed) != 2 {
		t.Fatalf("released %d endpoints, want 2", len(freed))
	}
	if c.BusyCount() != 0 {
		t.Error("a destination is still reserved after the timeout")
	}
}

// TestStaleReservationIsTakenOverByANewTransmission.
func TestStaleReservationIsTakenOverByANewTransmission(t *testing.T) {
	c, err := routing.NewCore(routing.CoreOptions{
		Table: simpleBridge(t), Peers: peersReady(peerA, peerB), Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	c.Route(peerA, voiceFrame(0x6666, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)

	// A different transmission, long after the first went silent. It must not
	// be refused: the previous one is plainly over.
	res := c.Route(peerA, voiceFrame(0x7777, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0.Add(5*time.Second))
	if len(res.Deliveries) != 1 {
		t.Fatalf("a new transmission was refused by a stale reservation: %s", res.Reason)
	}
}

// TestConfigurationChangeDoesNotCutOffAnActiveTransmission.
//
// Clarification R4: a change takes effect at the next transmission boundary,
// not mid-sentence.
func TestConfigurationChangeDoesNotCutOffAnActiveTransmission(t *testing.T) {
	c := newCore(t, simpleBridge(t), peerA, peerB)

	c.Route(peerA, voiceFrame(0x8888, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if c.BusyCount() != 2 {
		t.Fatalf("busy = %d, want 2 (origin plus destination)", c.BusyCount())
	}

	// The operator disables the bridge mid-transmission.
	c.SetTable(mustTable(t, routing.Bridge{
		Name: "net", Enabled: false,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 91, hbp.Timeslot2),
		},
	}))

	if c.BusyCount() != 2 {
		t.Error("in-flight reservations were discarded by a configuration change")
	}
	// New frames follow the new table.
	res := c.Route(peerA, voiceFrame(0x8888, 3148, hbp.Timeslot1, hbp.FrameTypeVoice), t0.Add(60*time.Millisecond))
	if len(res.Deliveries) != 0 {
		t.Error("the disabled bridge kept carrying traffic")
	}
}

// TestNilTableRoutesNothingButExplains.
func TestNilTableRoutesNothingButExplains(t *testing.T) {
	c := newCore(t, nil, peerA, peerB)
	res := c.Route(peerA, voiceFrame(0x9999, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Error("a nil table delivered frames")
	}
	if res.Reason == "" {
		t.Error("a nil table gave no reason")
	}
}

func TestNewCoreRequiresPeerLookup(t *testing.T) {
	if _, err := routing.NewCore(routing.CoreOptions{}); err == nil {
		t.Error("NewCore accepted a nil PeerLookup")
	}
}
