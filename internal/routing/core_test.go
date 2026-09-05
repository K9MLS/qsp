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

	// The terminator, which is a data sync frame carrying data type 2. A data
	// sync frame alone is a voice LC header and releases nothing.
	term := voiceFrame(0x3333, 3148, hbp.Timeslot1, hbp.FrameTypeSync)
	term.DataType = hbp.DataTypeTerminator
	c.Route(peerA, term, t0.Add(120*time.Millisecond))
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

	// Three reservations: the origin, the bridged destination, and the peer
	// the master repeats to on the same talkgroup. Repeat takes reservations
	// like any other destination — see ADR-0019.
	c.Route(peerA, voiceFrame(0x5555, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if c.BusyCount() != 3 {
		t.Fatalf("busy = %d, want 3 (origin, bridged destination, repeat)", c.BusyCount())
	}

	if freed := c.Expire(t0.Add(900 * time.Millisecond)); len(freed) != 0 {
		t.Fatalf("released early: %v", freed)
	}
	freed := c.Expire(t0.Add(1100 * time.Millisecond))
	if len(freed) != 3 {
		t.Fatalf("released %d endpoints, want 3 (origin, bridged destination, repeat)", len(freed))
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
	if c.BusyCount() != 3 {
		t.Fatalf("busy = %d, want 3 (origin, bridged destination, repeat)", c.BusyCount())
	}

	// The operator disables the bridge mid-transmission.
	c.SetTable(mustTable(t, routing.Bridge{
		Name: "net", Enabled: false,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 91, hbp.Timeslot2),
		},
	}))

	if c.BusyCount() != 3 {
		t.Error("in-flight reservations were discarded by a configuration change")
	}

	// New frames follow the new table: nothing crosses the disabled bridge.
	//
	// The master still repeats, and that is not the bridge carrying traffic.
	// Disabling a bridge stops TG 3148 reaching TG 91; it does not stop peers
	// on TG 3148 hearing each other, which is the master's own job and is
	// switched off with dmr.forwarding rather than with a bridge.
	res := c.Route(peerA, voiceFrame(0x8888, 3148, hbp.Timeslot1, hbp.FrameTypeVoice), t0.Add(60*time.Millisecond))
	for _, d := range res.Deliveries {
		if d.Frame.TargetID != 3148 {
			t.Errorf("the disabled bridge carried traffic to TG %d", d.Frame.TargetID)
		}
	}
}

// TestNilTableStillRepeats is the club case, and the one QSP could not express
// before ADR-0019.
//
// A master with no bridges configured at all is the ordinary starting point: a
// club stands one up, members point hotspots at it, and they talk to each other
// on one talkgroup. That must work without anybody writing a routing rule.
func TestNilTableStillRepeats(t *testing.T) {
	c := newCore(t, nil, peerA, peerB)

	res := c.Route(peerA, voiceFrame(0x9999, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("%d deliveries with no bridges, want 1 — peers on a talkgroup must hear each other (%s)",
			len(res.Deliveries), res.Reason)
	}
	if got := res.Deliveries[0].Peer; got != peerB {
		t.Errorf("delivered to peer %d, want %d", got, peerB)
	}
	if got := res.Deliveries[0].Frame.TargetID; got != 3148 {
		t.Errorf("repeated on TG %d, want the talkgroup it arrived on, 3148", got)
	}
}

// TestNoRepeatRoutesNothingWithoutBridges. A master that does not repeat is a
// deliberate choice, and it explains itself rather than falling silent.
func TestNoRepeatRoutesNothingWithoutBridges(t *testing.T) {
	c, err := routing.NewCore(routing.CoreOptions{
		Peers: peersReady(peerA, peerB), NoRepeat: true,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	res := c.Route(peerA, voiceFrame(0x9999, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Error("a core with repeat off and no bridges delivered frames")
	}
	if res.Reason == "" {
		t.Error("nothing was delivered and no reason was given")
	}
}

func TestNewCoreRequiresPeerLookup(t *testing.T) {
	if _, err := routing.NewCore(routing.CoreOptions{}); err == nil {
		t.Error("NewCore accepted a nil PeerLookup")
	}
}

// TestATextMessageIsNotThirtyCompetingTransmissions.
//
// A DMR text is a sequence of short data bursts, each carrying its own stream
// ID. The contention key included the stream, so every burst looked like a
// different station keying up: the first reserved the destination and the rest
// were refused until it lapsed two seconds later.
//
// Observed on a live network as seventeen frames offered and two delivered,
// which is why a message needed endless retries and usually failed.
func TestATextMessageIsNotThirtyCompetingTransmissions(t *testing.T) {
	c := newCore(t, nil, peerA, peerB)
	now := time.Date(2026, 8, 29, 13, 25, 0, 0, time.UTC)

	var delivered int
	for i := 0; i < 20; i++ {
		burst := hbp.Data{
			SourceID: 3155413, TargetID: 2, RepeaterID: peerA,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
			// Each burst its own stream, as a radio sends them.
			FrameType: hbp.FrameTypeSync, DataType: 0x2, StreamID: hbp.StreamID(1000 + i),
		}
		res := c.Route(peerA, burst, now)
		delivered += len(res.Deliveries)
		now = now.Add(120 * time.Millisecond)
	}

	if delivered != 20 {
		t.Errorf("%d of 20 data bursts were delivered; the rest were refused as "+
			"contention against the station that sent them", delivered)
	}
}

// TestVoiceStillContends. The fix must not open the door the contention rule
// exists to close: two people's audio interleaving into something nobody can
// understand.
func TestVoiceStillContends(t *testing.T) {
	c := newCore(t, nil, peerA, peerB)
	now := time.Date(2026, 8, 29, 13, 25, 0, 0, time.UTC)

	first := hbp.Data{
		SourceID: 3132910, TargetID: 2, RepeaterID: peerA,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0x1111,
	}
	if res := c.Route(peerA, first, now); len(res.Deliveries) == 0 {
		t.Fatal("the first transmission was not delivered")
	}

	// A different peer, mid-transmission.
	second := first
	second.RepeaterID = peerB
	second.SourceID = 3155413
	second.StreamID = 0x2222
	res := c.Route(peerB, second, now.Add(60*time.Millisecond))

	for _, d := range res.Deliveries {
		if d.Peer == peerA {
			t.Error("a second voice transmission interleaved with the first")
		}
	}
	if len(res.Drops) == 0 {
		t.Error("the competing transmission was not refused")
	}
}

// TestADataBurstDoesNotStealASlotFromVoice. A text arriving while somebody is
// talking must not take the destination from them.
func TestADataBurstDoesNotStealASlotFromVoice(t *testing.T) {
	c := newCore(t, nil, peerA, peerB)
	now := time.Date(2026, 8, 29, 13, 25, 0, 0, time.UTC)

	voice := hbp.Data{
		SourceID: 3132910, TargetID: 2, RepeaterID: peerA,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0x1111,
	}
	c.Route(peerA, voice, now)

	// A data burst from a different station.
	burst := voice
	burst.RepeaterID = peerB
	burst.SourceID = 3155413
	burst.FrameType = hbp.FrameTypeSync
	burst.StreamID = 0x3333
	res := c.Route(peerB, burst, now.Add(60*time.Millisecond))

	for _, d := range res.Deliveries {
		if d.Peer == peerA {
			t.Error("a data burst was delivered into a slot carrying somebody's voice")
		}
	}
}

// TestAFreedDestinationSaysWhatWasHoldingIt is what makes the warning about an
// abandoned transmission worth reading.
//
// A voice transmission that stops without a terminator has gone wrong — a lossy
// link, or a peer that lost power — and freeing its destinations is worth an
// operator's attention. **A run of data bursts always ends this way**, because
// data has no terminator and is not meant to, so the same warning about one is
// a false alarm. Two text messages wrote eight of them on a live network, which
// is how a warning stops being read at all.
func TestAFreedDestinationSaysWhatWasHoldingIt(t *testing.T) {
	c, err := routing.NewCore(routing.CoreOptions{
		Table: simpleBridge(t), Peers: peersReady(peerA, peerB), Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	// A data burst: one frame, its own stream ID, no terminator ever.
	c.Route(peerA, voiceFrame(0x6001, 3148, hbp.Timeslot1, hbp.FrameTypeSync), t0)
	for _, f := range c.Expire(t0.Add(1100 * time.Millisecond)) {
		if f.Voice {
			t.Errorf("%s was freed from a data burst and is reported as voice", f.Endpoint)
		}
	}

	// Audio on the same destinations.
	at := t0.Add(2 * time.Second)
	c.Route(peerA, voiceFrame(0x6002, 3148, hbp.Timeslot1, hbp.FrameTypeVoice), at)
	freed := c.Expire(at.Add(1100 * time.Millisecond))
	if len(freed) == 0 {
		t.Fatal("audio that stopped without a terminator freed nothing")
	}
	for _, f := range freed {
		if !f.Voice {
			t.Errorf("%s was freed from a voice transmission and is not reported as voice", f.Endpoint)
		}
	}

	// **A transmission that opens with a header and then carries audio is
	// voice.** A voice header is a data frame type and takes the reservation
	// one frame before the audio arrives, so the call type has to be able to
	// be corrected upwards or every real transmission would be reported as
	// data.
	at = at.Add(4 * time.Second)
	c.Route(peerA, voiceFrame(0x6003, 3148, hbp.Timeslot1, hbp.FrameTypeSync), at)
	c.Route(peerA, voiceFrame(0x6003, 3148, hbp.Timeslot1, hbp.FrameTypeVoice), at.Add(60*time.Millisecond))
	freed = c.Expire(at.Add(1200 * time.Millisecond))
	if len(freed) == 0 {
		t.Fatal("a transmission that opened with a header freed nothing")
	}
	for _, f := range freed {
		if !f.Voice {
			t.Errorf("%s opened with a header and carried audio, and is reported as data", f.Endpoint)
		}
	}
}
