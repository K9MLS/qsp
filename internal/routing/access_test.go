package routing_test

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// Layer 2 at the routing core. See docs/adr/ADR-0020-access-control.md.
//
// The registration and subscriber lists are enforced at the master, where the
// sender is known. The talkgroup lists are enforced here, where destinations
// are known — and they are enforced twice, which is the substantive decision
// ADR-0020 records.

func talkgroups(t *testing.T, slot int, mode access.Mode, ids ...string) access.Lists {
	t.Helper()
	l, err := access.Parse("dmr.access.talkgroups.test", access.Talkgroup, mode, ids)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var lists access.Lists
	switch slot {
	case 1:
		lists.Talkgroup1 = l
	case 2:
		lists.Talkgroup2 = l
	}
	return lists
}

func coreWithAccess(t *testing.T, lists access.Lists, table *routing.Table, peers ...hbp.RepeaterID) *routing.Core {
	t.Helper()
	c, err := routing.NewCore(routing.CoreOptions{
		Access: lists,
		Table:  table,
		Peers:  peersReady(peers...),
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return c
}

// TestPermissiveListsChangeNothing is the compatibility guarantee at the core.
// Every routing test that predates access control asserts this implicitly; this
// one asserts it on purpose.
func TestPermissiveListsChangeNothing(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := coreWithAccess(t, access.Lists{}, nil, a, b)

	res := core.Route(a, groupCall(0x1111, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("a permissive core delivered %d copies, want 1 (reason: %q)", len(res.Deliveries), res.Reason)
	}
	if len(res.Drops) != 0 {
		t.Errorf("a permissive core dropped destinations: %+v", res.Drops)
	}
}

func TestIngressRefusesAnUnpermittedTalkgroup(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	// TS2 carries TG 3100 only.
	core := coreWithAccess(t, talkgroups(t, 2, access.ModePermit, "3100"), nil, a, b)

	res := core.Route(a, groupCall(0x2222, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Fatalf("a refused talkgroup was delivered to %d peers", len(res.Deliveries))
	}
	if !strings.Contains(res.Reason, "dmr.access.talkgroups") {
		t.Errorf("the reason should name the list that refused: %q", res.Reason)
	}
	// Nothing was reserved, so the next transmission finds the core free.
	if core.BusyCount() != 0 {
		t.Errorf("a refused frame reserved %d destinations", core.BusyCount())
	}

	// The permitted talkgroup on the same slot still flows.
	res = core.Route(a, groupCall(0x3333, 3100, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("a permitted talkgroup was refused: %q", res.Reason)
	}
}

// TestTheListsArePerTimeslot is why the configuration has two of them.
// Repeaters are configured per slot, and an operator carrying statewide traffic
// on one and local traffic on the other cannot say so with a single list.
func TestTheListsArePerTimeslot(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	// TS1 permits 3100 only. TS2 is unconfigured, so it permits everything.
	core := coreWithAccess(t, talkgroups(t, 1, access.ModePermit, "3100"), nil, a, b)

	if res := core.Route(a, groupCall(0x4444, 9, hbp.Timeslot1, hbp.FrameTypeSync), t0); len(res.Deliveries) != 0 {
		t.Error("TG 9 was carried on TS1, which permits 3100 only")
	}
	if res := core.Route(a, groupCall(0x5555, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0); len(res.Deliveries) != 1 {
		t.Errorf("TG 9 was refused on TS2, which has no list: %q", res.Reason)
	}
}

// TestEgressRefusesTrafficThatNeverCrossedIngress is the case ADR-0020 exists
// for. A frame arriving over a link never passes the ingress test for the
// talkgroup it is translated *to*, so an ingress-only check would permit
// exactly the traffic an operator can least vouch for.
func TestEgressRefusesATranslatedTalkgroup(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002

	// A bridge carrying TG 9 on TS2 across to TG 91 on TS2.
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "translate",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: a, Talkgroup: 9, Timeslot: hbp.Timeslot2},
			{Peer: b, Talkgroup: 91, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}

	// TS2 permits 9 but not 91: the arriving talkgroup passes ingress, and the
	// destination talkgroup must still be refused.
	core := coreWithAccess(t, talkgroups(t, 2, access.ModePermit, "9"), table, a, b)

	res := core.Route(a, groupCall(0x6666, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	for _, d := range res.Deliveries {
		if d.Frame.TargetID == 91 {
			t.Error("a translated talkgroup that no list permits was delivered")
		}
	}
	var refused bool
	for _, d := range res.Drops {
		if strings.Contains(d.Reason, "dmr.access.talkgroups") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("the refused destination was not reported as a drop: %+v", res.Drops)
	}
}

// TestARefusedDestinationIsADropRatherThanSilence keeps Constitution §18. The
// console already renders drops, so access control needs no new plumbing to be
// visible to an operator.
func TestARefusedDestinationIsADropRatherThanSilence(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "translate",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: a, Talkgroup: 9, Timeslot: hbp.Timeslot2},
			{Peer: b, Talkgroup: 91, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core := coreWithAccess(t, talkgroups(t, 2, access.ModePermit, "9"), table, a, b)

	res := core.Route(a, groupCall(0x7777, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Drops) == 0 {
		t.Fatal("a destination was refused without a drop being recorded")
	}
	for _, d := range res.Drops {
		if d.Reason == "" {
			t.Errorf("a drop carries no reason: %+v", d)
		}
	}
}

// TestARefusedDestinationIsNotReserved matters more than it looks. Reserving a
// destination the access list refused would make it appear busy to the next
// transmission, so an access list would silently become a denial of service on
// everybody else.
func TestARefusedDestinationIsNotReserved(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "translate",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: a, Talkgroup: 9, Timeslot: hbp.Timeslot2},
			{Peer: b, Talkgroup: 91, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core := coreWithAccess(t, talkgroups(t, 2, access.ModePermit, "9"), table, a, b)

	core.Route(a, groupCall(0x8888, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	for _, busy := range core.Busy() {
		if busy.Talkgroup == 91 {
			t.Errorf("a refused destination was reserved: %s", busy)
		}
	}
}

// TestSetAccessTakesEffectOnTheNextFrame is the deliberate difference from
// SetTable. An operator removing a talkgroup is intervening in something
// happening now, and a refusal that waits for the offender to stop is not a
// refusal.
func TestSetAccessTakesEffectOnTheNextFrame(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := coreWithAccess(t, access.Lists{}, nil, a, b)

	// A transmission is under way and being carried.
	if res := core.Route(a, groupCall(0x9999, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0); len(res.Deliveries) != 1 {
		t.Fatalf("the opening frame was not carried: %q", res.Reason)
	}

	// The operator removes TG 9 mid-transmission.
	core.SetAccess(talkgroups(t, 2, access.ModePermit, "3100"))

	res := core.Route(a, groupCall(0x9999, 9, hbp.Timeslot2, hbp.FrameTypeVoice), t0.Add(60_000_000))
	if len(res.Deliveries) != 0 {
		t.Error("a talkgroup removed mid-transmission was still carried; SetTable's rule was applied")
	}
	if !strings.Contains(res.Reason, "dmr.access.talkgroups") {
		t.Errorf("the reason should name the list: %q", res.Reason)
	}
}

// TestUpstreamExportIsSubjectToTheLists stops a talkgroup this instance does
// not carry being handed to somebody else's network.
func TestUpstreamExportIsSubjectToTheLists(t *testing.T) {
	const a hbp.RepeaterID = 3100001
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "export",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: a, Talkgroup: 9, Timeslot: hbp.Timeslot2},
			{Upstream: "brandmeister", Talkgroup: 91, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core := coreWithAccess(t, talkgroups(t, 2, access.ModePermit, "9"), table, a)

	res := core.Route(a, groupCall(0xAAAA, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Upstreams) != 0 {
		t.Errorf("TG 91 was exported to a link although no list permits it: %+v", res.Upstreams)
	}
	var refused bool
	for _, d := range res.Drops {
		if d.To.Upstream == "brandmeister" && strings.Contains(d.Reason, "dmr.access.talkgroups") {
			refused = true
		}
	}
	if !refused {
		t.Errorf("the refused link was not reported as a drop: %+v", res.Drops)
	}
}

// TestTrafficFromALinkIsSubjectToIngress is the other half of the same
// argument: a frame arriving from another network is exactly the traffic an
// operator most wants their lists to govern.
func TestTrafficFromALinkIsSubjectToIngress(t *testing.T) {
	const a hbp.RepeaterID = 3100001
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "import",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Upstream: "brandmeister", Talkgroup: 91, Timeslot: hbp.Timeslot2},
			{Peer: a, Talkgroup: 9, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	// The local talkgroup is permitted; the one the link carries is not.
	core := coreWithAccess(t, talkgroups(t, 2, access.ModePermit, "9"), table, a)

	res := core.RouteFromUpstream("brandmeister",
		groupCall(0xBBBB, 91, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Errorf("a talkgroup no list permits was accepted from a link: %+v", res.Deliveries)
	}
	if !strings.Contains(res.Reason, "dmr.access.talkgroups") {
		t.Errorf("the reason should name the list: %q", res.Reason)
	}
}

// TestADenyListRefusesOneTalkgroupAndCarriesTheRest covers the mode a club
// actually reaches for first: everything works except the one thing that does
// not.
func TestADenyListRefusesOneTalkgroupAndCarriesTheRest(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := coreWithAccess(t, talkgroups(t, 2, access.ModeDeny, "3100-3199"), nil, a, b)

	if res := core.Route(a, groupCall(0xCCCC, 3150, hbp.Timeslot2, hbp.FrameTypeSync), t0); len(res.Deliveries) != 0 {
		t.Error("a denied talkgroup was carried")
	}
	if res := core.Route(a, groupCall(0xDDDD, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0); len(res.Deliveries) != 1 {
		t.Errorf("a talkgroup outside the deny list was refused: %q", res.Reason)
	}
}

// Timeslot contention. See docs/adr/ADR-0022-timeslot-contention.md.

// TestTwoTalkgroupsCannotShareOneTimeslot is the bug ADR-0022 records. A DMR
// timeslot is one TDMA channel; two talkgroups down it is interleaved audio
// nobody can understand. Reservations were keyed on the talkgroup, so the two
// looked like separate destinations and both were delivered.
func TestTwoTalkgroupsCannotShareOneTimeslot(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := noBridges(t, a, b, c)

	core.Route(a, groupCall(0x1111, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	res := core.Route(b, groupCall(0x2222, 91, hbp.Timeslot2, hbp.FrameTypeSync), t0)

	for _, d := range res.Deliveries {
		if d.Peer == c {
			t.Errorf("peer %d received TG %d on TS2 while TG 9 was already there",
				c, d.Frame.TargetID)
		}
	}
	var named bool
	for _, d := range res.Drops {
		if strings.Contains(d.Reason, "TG 9") {
			named = true
		}
	}
	if !named {
		t.Errorf("no drop named the talkgroup already on the slot: %+v", res.Drops)
	}
}

// TestTheOtherTimeslotIsUnaffected keeps the fix from becoming a blunt refusal.
// A repeater has two slots and they are independent paths.
func TestTheOtherTimeslotIsUnaffected(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := noBridges(t, a, b, c)

	core.Route(a, groupCall(0x3333, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	res := core.Route(b, groupCall(0x4444, 91, hbp.Timeslot1, hbp.FrameTypeSync), t0)

	var reached bool
	for _, d := range res.Deliveries {
		if d.Peer == c && d.Frame.Timeslot == hbp.Timeslot1 {
			reached = true
		}
	}
	if !reached {
		t.Errorf("TS1 was refused because TS2 was busy: %+v", res.Drops)
	}
}

// TestTheSameTransmissionKeepsItsSlot guards against the widened key refusing
// a transmission's own continuation frames.
func TestTheSameTransmissionKeepsItsSlot(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b)

	core.Route(a, groupCall(0x5555, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	for seq := 0; seq < 5; seq++ {
		res := core.Route(a, groupCall(0x5555, 9, hbp.Timeslot2, hbp.FrameTypeVoice), t0)
		if len(res.Deliveries) != 1 {
			t.Fatalf("frame %d of a transmission was refused its own slot: %+v", seq, res.Drops)
		}
	}
}

// TestTheSlotIsReleasedByATerminator keeps ADR-0014's rule under the new key.
// Waiting out the timeout after a clean unkey would make every exchange feel
// broken.
func TestTheSlotIsReleasedByATerminator(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := noBridges(t, a, b, c)

	core.Route(a, groupCall(0x6666, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	core.Route(a, groupTerminator(0x6666, 9, hbp.Timeslot2), t0)

	if core.BusyCount() != 0 {
		t.Fatalf("a terminator left %d reservations behind", core.BusyCount())
	}
	// Another talkgroup can now use the slot immediately.
	res := core.Route(b, groupCall(0x7777, 91, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) == 0 {
		t.Errorf("the slot was not free after a terminator: %+v", res.Drops)
	}
}

// TestBusyStillNamesTheTalkgroup matters because the contention key drops it.
// An operator told a nameless slot is busy learns nothing.
func TestBusyStillNamesTheTalkgroup(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b)

	core.Route(a, groupCall(0x8888, 3148, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	busy := core.Busy()
	if len(busy) == 0 {
		t.Fatal("nothing was reserved")
	}
	for _, e := range busy {
		if e.Talkgroup != 3148 {
			t.Errorf("a reservation reports TG %d, want 3148: %s", e.Talkgroup, e)
		}
	}
}

// TestALinkCarriesSeveralTalkgroupsAtOnce is the deliberate asymmetry.
// Contention models a physical constraint; an OpenBridge link is an IP socket
// rather than a radio channel, and BrandMeister carries several talkgroups over
// one. Applying the timeslot rule here would refuse deliverable traffic.
func TestALinkCarriesSeveralTalkgroupsAtOnce(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	table, err := routing.NewTable([]routing.Bridge{
		{Name: "one", Enabled: true, Endpoints: []routing.Endpoint{
			{Peer: a, Talkgroup: 9, Timeslot: hbp.Timeslot2},
			{Upstream: "brandmeister", Talkgroup: 3148, Timeslot: hbp.Timeslot2},
		}},
		{Name: "two", Enabled: true, Endpoints: []routing.Endpoint{
			{Peer: b, Talkgroup: 91, Timeslot: hbp.Timeslot2},
			{Upstream: "brandmeister", Talkgroup: 3100, Timeslot: hbp.Timeslot2},
		}},
	})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: peersReady(a, b)})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	first := core.Route(a, groupCall(0x9999, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(first.Upstreams) != 1 {
		t.Fatalf("the first talkgroup did not reach the link: %+v", first.Drops)
	}
	second := core.Route(b, groupCall(0xAAAA, 91, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(second.Upstreams) != 1 {
		t.Errorf("a second talkgroup was refused the link, which is an IP socket "+
			"and not a radio channel: %+v", second.Drops)
	}
}

// Private calls. See docs/adr/ADR-0021-private-calls-and-data.md.

// stubSubscribers is a fixed radio-to-peer map.
type stubSubscribers map[uint32]struct {
	peer hbp.RepeaterID
	slot hbp.Timeslot
}

func (s stubSubscribers) LocateFor(id uint32) (hbp.RepeaterID, hbp.Timeslot, bool) {
	loc, ok := s[id]
	return loc.peer, loc.slot, ok
}

func privateCall(stream hbp.StreamID, target uint32, slot hbp.Timeslot, ft hbp.FrameType) hbp.Data {
	return hbp.Data{
		SourceID: 3132910, TargetID: target, Timeslot: slot,
		CallType: hbp.CallPrivate, FrameType: ft, StreamID: stream,
	}
}

func coreWithSubscribers(t *testing.T, subs stubSubscribers, peers ...hbp.RepeaterID) *routing.Core {
	t.Helper()
	c, err := routing.NewCore(routing.CoreOptions{
		Peers:       peersReady(peers...),
		Subscribers: subs,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return c
}

// TestAPrivateCallReachesTheRadiosPeer is the whole feature. Radio-to-radio
// calling is used constantly on DMR and QSP routed none of it.
func TestAPrivateCallReachesTheRadiosPeer(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := coreWithSubscribers(t, stubSubscribers{
		3121002: {peer: b, slot: hbp.Timeslot2},
	}, a, b, c)

	res := core.Route(a, privateCall(0x1111, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)

	if len(res.Deliveries) != 1 {
		t.Fatalf("a private call produced %d deliveries, want 1 (reason: %q)",
			len(res.Deliveries), res.Reason)
	}
	got := res.Deliveries[0]
	if got.Peer != b {
		t.Errorf("delivered to peer %d, want %d — the peer the radio is behind", got.Peer, b)
	}
	// The called radio's ID stays in the target field. That is what makes the
	// receiving radio open its squelch.
	if got.Frame.TargetID != 3121002 {
		t.Errorf("target is %d, want the called radio's ID 3121002", got.Frame.TargetID)
	}
	if got.Frame.CallType != hbp.CallPrivate {
		t.Error("the call type was not preserved")
	}
}

// TestAPrivateCallIsNotBroadcast is the property that made this worth doing
// carefully. A private call reaching every peer would put a private
// conversation on every hotspot on the network.
func TestAPrivateCallIsNotBroadcast(t *testing.T) {
	const a, b, c, d hbp.RepeaterID = 3100001, 3100002, 3100003, 3100004
	core := coreWithSubscribers(t, stubSubscribers{
		3121002: {peer: b, slot: hbp.Timeslot2},
	}, a, b, c, d)

	res := core.Route(a, privateCall(0x2222, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	for _, del := range res.Deliveries {
		if del.Peer != b {
			t.Errorf("a private call reached peer %d, which is not where the radio is", del.Peer)
		}
	}
}

// TestAPrivateCallToAnUnknownRadioSaysSo. Silence would leave an operator with
// no idea whether the call failed or the other person simply did not answer.
func TestAPrivateCallToAnUnknownRadioSaysSo(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := coreWithSubscribers(t, stubSubscribers{}, a, b)

	res := core.Route(a, privateCall(0x3333, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Fatal("a private call to an unlocatable radio was delivered somewhere")
	}
	if !strings.Contains(res.Reason, "3121002") {
		t.Errorf("the reason should name the radio: %q", res.Reason)
	}
	if !strings.Contains(res.Reason, "heard") {
		t.Errorf("the reason should say why: %q", res.Reason)
	}
}

// TestPrivateCallsAreOptional keeps an instance that tracks no radios working.
// It routes group calls perfectly well, and must say so rather than fail.
func TestPrivateCallsAreOptional(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b)

	res := core.Route(a, privateCall(0x4444, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Fatal("a private call was delivered with no subscriber lookup configured")
	}
	if res.Reason == "" {
		t.Error("the refusal carried no explanation")
	}
	// Group calls are unaffected.
	if got := core.Route(a, groupCall(0x5555, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0); len(got.Deliveries) != 1 {
		t.Errorf("group calls broke: %q", got.Reason)
	}
}

// TestAPrivateCallUsesTheSlotTheRadioIsOn. A peer's two timeslots are
// independent paths, and sending down the wrong one reaches nobody.
func TestAPrivateCallUsesTheSlotTheRadioIsOn(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := coreWithSubscribers(t, stubSubscribers{
		3121002: {peer: b, slot: hbp.Timeslot1},
	}, a, b)

	// The caller is on TS2; the called radio was last heard on TS1.
	res := core.Route(a, privateCall(0x6666, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 1 {
		t.Fatalf("no delivery: %q", res.Reason)
	}
	if res.Deliveries[0].Frame.Timeslot != hbp.Timeslot1 {
		t.Errorf("sent on %s, want TS1 where the radio was heard",
			res.Deliveries[0].Frame.Timeslot)
	}
}

// TestAPrivateCallContendsForTheSlot is ADR-0022 paying for itself: group and
// private calls contend identically, with no special case for either.
func TestAPrivateCallContendsForTheSlot(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := coreWithSubscribers(t, stubSubscribers{
		3121002: {peer: b, slot: hbp.Timeslot2},
	}, a, b, c)

	// A group call takes B's TS2.
	core.Route(c, groupCall(0x7777, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)

	// A private call to a radio behind B, on the same slot, must be refused.
	res := core.Route(a, privateCall(0x8888, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Error("a private call was delivered to a slot already carrying a group call")
	}
	if len(res.Drops) == 0 {
		t.Error("the refusal was not recorded as a drop")
	}
}

// TestAPrivateCallIsNotSentBackToItsCaller guards the obvious mistake, which
// would sound to the caller exactly like a fault.
func TestAPrivateCallIsNotSentBackToItsCaller(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	// The called radio is behind the *calling* peer — two radios on one
	// hotspot, which is ordinary.
	core := coreWithSubscribers(t, stubSubscribers{
		3121002: {peer: a, slot: hbp.Timeslot2},
	}, a, b)

	res := core.Route(a, privateCall(0x9999, 3121002, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	for _, del := range res.Deliveries {
		if del.Peer == a {
			t.Error("a private call was echoed back to the peer that sent it")
		}
	}
}

// Layer 3, subscription at the routing core. See ADR-0023.

// stubAttached is a fixed set of peer/talkgroup/slot attachments.
type stubAttached map[attachKey]bool

type attachKey struct {
	peer      hbp.RepeaterID
	talkgroup uint32
	slot      hbp.Timeslot
}

func (s stubAttached) Attached(p hbp.RepeaterID, tg uint32, slot hbp.Timeslot) bool {
	return s[attachKey{p, tg, slot}]
}

func coreWithAttachments(t *testing.T, a stubAttached, table *routing.Table, peers ...hbp.RepeaterID) *routing.Core {
	t.Helper()
	c, err := routing.NewCore(routing.CoreOptions{
		Table:    table,
		Peers:    peersReady(peers...),
		Attached: a,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return c
}

// TestNilSubscriptionsDeliverEverything is the compatibility guarantee at the
// core: an instance not using this behaves exactly as it did before.
func TestNilSubscriptionsDeliverEverything(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := noBridges(t, a, b, c)

	res := core.Route(a, groupCall(0x1111, 3148, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 2 {
		t.Errorf("a core with no subscriptions delivered %d copies, want 2", len(res.Deliveries))
	}
}

// TestRepeatOnlyReachesAttachedPeers is the feature: a member sitting on their
// local talkgroup should not have a statewide net arrive on the same hotspot.
func TestRepeatOnlyReachesAttachedPeers(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	// Only b wants TG 3148.
	core := coreWithAttachments(t, stubAttached{
		{b, 3148, hbp.Timeslot2}: true,
	}, nil, a, b, c)

	res := core.Route(a, groupCall(0x2222, 3148, hbp.Timeslot2, hbp.FrameTypeSync), t0)

	if len(res.Deliveries) != 1 {
		t.Fatalf("delivered to %d peers, want 1", len(res.Deliveries))
	}
	if res.Deliveries[0].Peer != b {
		t.Errorf("delivered to peer %d, want %d", res.Deliveries[0].Peer, b)
	}
	// The unattached peer is refused with a reason, not skipped in silence.
	var explained bool
	for _, d := range res.Drops {
		if d.To.Peer == c && strings.Contains(d.Reason, "not attached") {
			explained = true
		}
	}
	if !explained {
		t.Errorf("the unattached peer was skipped without an explanation: %+v", res.Drops)
	}
}

// TestABridgeIgnoresAttachment. An operator who bridged a talkgroup to somebody
// has already said it should arrive; making them also attach it would mean
// configuring the same thing twice.
func TestABridgeIgnoresAttachment(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "net",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: a, Talkgroup: 9, Timeslot: hbp.Timeslot2},
			{Peer: b, Talkgroup: 91, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	// b is attached to nothing at all.
	core := coreWithAttachments(t, stubAttached{}, table, a, b)

	res := core.Route(a, groupCall(0x3333, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	var reached bool
	for _, d := range res.Deliveries {
		if d.Peer == b && d.Frame.TargetID == 91 {
			reached = true
		}
	}
	if !reached {
		t.Errorf("a bridged talkgroup was refused for want of an attachment: %+v", res.Drops)
	}
}

// TestAccessControlOutranksAttachment. Access decides what the instance is
// willing to carry; attachment decides what a member wants. Nobody may
// subscribe their way past a refusal.
func TestAccessControlOutranksAttachment(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	c, err := routing.NewCore(routing.CoreOptions{
		Access:   talkgroups(t, 2, access.ModePermit, "9"),
		Peers:    peersReady(a, b),
		Attached: stubAttached{{b, 3148, hbp.Timeslot2}: true},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	// b is attached to 3148, but the instance does not carry it.
	res := c.Route(a, groupCall(0x4444, 3148, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	if len(res.Deliveries) != 0 {
		t.Error("an attachment delivered a talkgroup the access list refuses")
	}
	if !strings.Contains(res.Reason, "dmr.access.talkgroups") {
		t.Errorf("the refusal should come from access control: %q", res.Reason)
	}
}

// TestAnUnattachedPeerTakesNoReservation. A peer that is not a destination must
// not hold a slot, or subscription would quietly become a denial of service.
func TestAnUnattachedPeerTakesNoReservation(t *testing.T) {
	const a, b, c hbp.RepeaterID = 3100001, 3100002, 3100003
	core := coreWithAttachments(t, stubAttached{
		{b, 9, hbp.Timeslot2}: true,
	}, nil, a, b, c)

	core.Route(a, groupCall(0x5555, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0)
	for _, busy := range core.Busy() {
		if busy.Peer == c {
			t.Errorf("an unattached peer holds a reservation: %s", busy)
		}
	}
}

// TestAttachmentIsPerTimeslot. A peer's two slots are independent paths.
func TestAttachmentIsPerTimeslot(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := coreWithAttachments(t, stubAttached{
		{b, 9, hbp.Timeslot2}: true,
	}, nil, a, b)

	if res := core.Route(a, groupCall(0x6666, 9, hbp.Timeslot2, hbp.FrameTypeSync), t0); len(res.Deliveries) != 1 {
		t.Errorf("TS2 was attached and did not receive: %+v", res.Drops)
	}
	if res := core.Route(a, groupCall(0x7777, 9, hbp.Timeslot1, hbp.FrameTypeSync), t0); len(res.Deliveries) != 0 {
		t.Error("TS1 received although only TS2 is attached")
	}
}
