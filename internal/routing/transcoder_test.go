package routing

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// bothReady reports every peer as connected, so that a test about permission
// is about permission and not about whether a peer happens to be logged in.
type bothReady struct{}

func (bothReady) Ready(hbp.RepeaterID) bool { return true }

func (bothReady) ReadyPeers() []hbp.RepeaterID {
	return []hbp.RepeaterID{312345, 315544}
}

// A transcoder endpoint: ADR-0063.
//
// The one thing these tests exist to hold is the contention rule. A vocoder
// channel configures like a link and contends like a peer, and getting the
// second half wrong delivers two talkgroups to one chip — which is the bug
// ADR-0022 was written after, in a new place.

func transcoder(name string, tg uint32, ts hbp.Timeslot) Endpoint {
	return Endpoint{Transcoder: name, Talkgroup: tg, Timeslot: ts}
}

// TestATranscoderContendsLikeAPeerAndNotLikeALink is the decision, as a test.
//
// **One AMBE-3000 is one channel.** BLUEPRINT §7 says four simultaneous
// transcoded talkgroups need four chips, and the chip answers one packet at a
// time. So two talkgroups reaching one name are one destination and the second
// is refused — ADR-0022's physical-constraint case, dropping the talkgroup
// exactly as a peer's timeslot does.
//
// An OpenBridge link keeps its talkgroup because an IP socket carries several
// concurrently. Reusing that rule here would have delivered both.
func TestATranscoderContendsLikeAPeerAndNotLikeALink(t *testing.T) {
	a := transcoder("dvstick", 2, hbp.Timeslot2)
	b := transcoder("dvstick", 11, hbp.Timeslot2)

	if contend(a) != contend(b) {
		t.Errorf("TG2 and TG11 on transcoder %q contend as %s and %s; one chip is "+
			"one channel and both would be delivered",
			a.Transcoder, contend(a), contend(b))
	}

	// And different slots are still one channel, because a vocoder has no
	// slots at all.
	c := transcoder("dvstick", 2, hbp.Timeslot1)
	if contend(a) != contend(c) {
		t.Errorf("TS1 and TS2 on one transcoder contend as %s and %s; a vocoder "+
			"has no timeslots", contend(a), contend(c))
	}

	// Two chips are two channels, which is the whole point of naming them.
	if contend(a) == contend(transcoder("second", 2, hbp.Timeslot2)) {
		t.Error("two differently named transcoders contend as one destination; a " +
			"club with two chips has two channels")
	}

	// The link rule is unchanged, or ADR-0022's asymmetry has been lost.
	l1 := Endpoint{Upstream: "bm", Talkgroup: 2, Timeslot: hbp.Timeslot1}
	l2 := Endpoint{Upstream: "bm", Talkgroup: 11, Timeslot: hbp.Timeslot1}
	if contend(l1) == contend(l2) {
		t.Error("two talkgroups on one link now contend as one destination; an " +
			"OpenBridge link is an IP socket and carries several at once")
	}
}

// TestATranscoderIsNeverAPeerOrALink covers the default that shipped as a
// defect once already.
//
// A non-peer endpoint carries AnyPeer by default, AnyPeer matches everything,
// and a table that compared only peers concluded the link *was* the peer that
// had just transmitted — so it declined to send the frame, on the grounds that
// a call is never sent back where it came from. The bridge appeared configured
// and carried nothing. A transcoder endpoint has exactly the same default.
func TestATranscoderIsNeverAPeerOrALink(t *testing.T) {
	voc := transcoder("dvstick", 2, hbp.Timeslot2)
	peer := Endpoint{Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2}
	link := Endpoint{Upstream: "bm", Talkgroup: 2, Timeslot: hbp.Timeslot2}
	any := Endpoint{Peer: AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2}

	for _, tc := range []struct {
		name string
		a, b Endpoint
	}{
		{"a transcoder and a peer", voc, peer},
		{"a transcoder and any peer", voc, any},
		{"a transcoder and a link", voc, link},
	} {
		if tc.a.Matches(tc.b) || tc.b.Matches(tc.a) {
			t.Errorf("%s match each other; the bridge would appear configured "+
				"and carry nothing", tc.name)
		}
	}

	// And two of the same kind still match on the name.
	if !voc.Matches(transcoder("dvstick", 2, hbp.Timeslot2)) {
		t.Error("a transcoder endpoint does not match itself")
	}
	if voc.Matches(transcoder("second", 2, hbp.Timeslot2)) {
		t.Error("two differently named transcoders match each other")
	}
}

// TestAnEndpointIsOneKind refuses the combinations.
//
// An endpoint naming two kinds is a configuration nobody can act on, and
// guessing which one was meant would make the document say one thing and the
// network do another.
func TestAnEndpointIsOneKind(t *testing.T) {
	for _, tc := range []struct {
		name string
		e    Endpoint
		want string
	}{
		{"transcoder and peer",
			Endpoint{Transcoder: "dvstick", Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			"transcoder"},
		{"transcoder and upstream",
			Endpoint{Transcoder: "dvstick", Upstream: "bm", Talkgroup: 2, Timeslot: hbp.Timeslot2},
			"transcoder"},
		{"upstream and peer",
			Endpoint{Upstream: "bm", Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			"upstream"},
	} {
		err := tc.e.Validate()
		if err == nil {
			t.Errorf("an endpoint naming %s was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("the refusal for %s does not name %q: %v", tc.name, tc.want, err)
		}
	}

	// A transcoder endpoint on its own is valid, and still needs a talkgroup
	// and a slot: the talkgroup is what is being mapped, and the slot is the
	// one it arrived on.
	if err := transcoder("dvstick", 2, hbp.Timeslot2).Validate(); err != nil {
		t.Errorf("a plain transcoder endpoint was refused: %v", err)
	}
	if err := transcoder("dvstick", 0, hbp.Timeslot2).Validate(); err == nil {
		t.Error("a transcoder endpoint with talkgroup 0 was accepted")
	}
	if err := transcoder("dvstick", 2, 0).Validate(); err == nil {
		t.Error("a transcoder endpoint with no timeslot was accepted")
	}
}

// TestATalkgroupRoutesToATranscoder is the mapping doing its job.
//
// A bridge joining a talkgroup on a slot to a chip, which is what a
// talkgroup-to-channel mapping is. It inherits the three things ADR-0063 says
// it inherits: the schedule flips Enabled, the reason explains an empty
// result, and the source is never a target.
func TestATalkgroupRoutesToATranscoder(t *testing.T) {
	table, err := NewTable([]Bridge{{
		Name:    "zello",
		Enabled: true,
		Endpoints: []Endpoint{
			{Peer: AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			transcoder("dvstick", 2, hbp.Timeslot2),
		},
	}})
	if err != nil {
		t.Fatalf("building a table: %v", err)
	}

	// A radio transmitting on TG2 TS2 reaches the chip.
	d := table.Route(Endpoint{Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2})
	if !d.Routed() {
		t.Fatalf("a call on TG2 TS2 went nowhere: %s", d.Reason)
	}
	if len(d.Targets) != 1 || d.Targets[0].Transcoder != "dvstick" {
		t.Fatalf("the targets are %v, want the dvstick transcoder alone", d.Targets)
	}

	// **Traffic from the chip does not reach the network by default.** The
	// permission this table carries is empty, so no repeater has opted in —
	// see TestTranscodedAudioReachesARepeaterOnlyByOptIn.
	back := table.Route(transcoder("dvstick", 2, hbp.Timeslot2))
	if back.Routed() {
		t.Errorf("transcoded audio reached %v with no peer opted in", back.Targets)
	}
	if len(back.Withheld) == 0 {
		t.Error("the refused destination was not reported; a repeater nobody " +
			"opted in for looks exactly like a repeater nobody bridged")
	}

	// A different talkgroup is not mapped, and the reason says so rather
	// than leaving an empty list unexplained.
	none := table.Route(Endpoint{Peer: 312345, Talkgroup: 11, Timeslot: hbp.Timeslot2})
	if none.Routed() {
		t.Errorf("TG11 reached %v with no bridge naming it", none.Targets)
	}
	if none.Reason == "" {
		t.Error("an unrouted call came back with no reason")
	}
}

// TestADisabledTranscoderBridgeCarriesNothing is scheduling, inherited.
//
// ADR-0063's claim is that a transcoded link which should run only during a
// net needs no new mechanism, because a bridge already has Enabled and the
// schedule already flips it. This is that claim checked rather than asserted.
func TestADisabledTranscoderBridgeCarriesNothing(t *testing.T) {
	table, err := NewTable([]Bridge{{
		Name:    "zello",
		Enabled: false,
		Endpoints: []Endpoint{
			{Peer: AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			transcoder("dvstick", 2, hbp.Timeslot2),
		},
	}})
	if err != nil {
		t.Fatalf("building a table: %v", err)
	}

	d := table.Route(Endpoint{Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2})
	if d.Routed() {
		t.Errorf("a disabled bridge delivered to %v", d.Targets)
	}
	if !strings.Contains(strings.ToLower(d.Reason), "disabled") {
		t.Errorf("the reason for a disabled bridge is %q; an operator reading it "+
			"needs to know the bridge exists and is off", d.Reason)
	}
}

// TestATranscoderEndpointNamesItselfInAString is what an operator reads.
//
// A drop that says "the vocoder is busy" and cannot say which one is the
// COLLISIONS defect again: a count without a subject cannot answer the
// question anybody actually has.
func TestATranscoderEndpointNamesItselfInAString(t *testing.T) {
	got := transcoder("dvstick", 2, hbp.Timeslot2).String()
	for _, want := range []string{"transcoder", "dvstick", "2"} {
		if !strings.Contains(got, want) {
			t.Errorf("the endpoint reads %q, want it to mention %q", got, want)
		}
	}
	// And it is not mistakable for a link in a log line.
	if strings.Contains(got, "upstream") {
		t.Errorf("a transcoder endpoint reads %q, which names it as a link", got)
	}
}

// TestTranscodedAudioReachesARepeaterOnlyByOptIn is ADR-0062's licensing
// consequence, as a routing rule.
//
// **A Zello user is not necessarily licensed and their audio reaches RF.** An
// unlicensed transmission on a licensed operator's repeater is that operator's
// problem, not the network's, so it cannot be something a default arranges on
// their behalf. The default is nobody.
func TestTranscodedAudioReachesARepeaterOnlyByOptIn(t *testing.T) {
	const (
		optedIn = hbp.RepeaterID(312345)
		hasNot  = hbp.RepeaterID(315544)
	)

	build := func(t *testing.T, perm map[string]Permission) *Table {
		t.Helper()
		table, err := NewTable([]Bridge{{
			Name:    "zello",
			Enabled: true,
			Endpoints: []Endpoint{
				{Peer: optedIn, Talkgroup: 2, Timeslot: hbp.Timeslot2},
				{Peer: hasNot, Talkgroup: 2, Timeslot: hbp.Timeslot2},
				transcoder("dvstick", 2, hbp.Timeslot2),
			},
		}}, WithPermissions(perm))
		if err != nil {
			t.Fatalf("building a table: %v", err)
		}
		return table
	}

	// Nothing configured: nothing delivered, and both refusals named.
	d := build(t, nil).Route(transcoder("dvstick", 2, hbp.Timeslot2))
	if d.Routed() {
		t.Errorf("with no permission configured, transcoded audio reached %v", d.Targets)
	}
	if len(d.Withheld) != 2 {
		t.Errorf("%d destination(s) reported as withheld, want 2", len(d.Withheld))
	}
	for _, want := range []string{"opted in", "unlicensed", "dvstick"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("the reason does not mention %q: %s", want, d.Reason)
		}
	}

	// One peer opts in: that one and only that one.
	d = build(t, map[string]Permission{
		"dvstick": {Peers: []hbp.RepeaterID{optedIn}},
	}).Route(transcoder("dvstick", 2, hbp.Timeslot2))
	if len(d.Targets) != 1 || d.Targets[0].Peer != optedIn {
		t.Fatalf("the targets are %v, want peer %d alone", d.Targets, optedIn)
	}
	if len(d.Withheld) != 1 || d.Withheld[0].Peer != hasNot {
		t.Errorf("the withheld list is %v, want peer %d alone", d.Withheld, hasNot)
	}

	// All permits both, and is a separate boolean so that it cannot happen by
	// accident.
	d = build(t, map[string]Permission{"dvstick": {All: true}}).
		Route(transcoder("dvstick", 2, hbp.Timeslot2))
	if len(d.Targets) != 2 {
		t.Errorf("with every peer permitted the targets are %v, want both", d.Targets)
	}
	if len(d.Withheld) != 0 {
		t.Errorf("with every peer permitted %v was still withheld", d.Withheld)
	}

	// **A permission for one transcoder does not carry to another.** Two
	// chips may serve different things, and a club permitting its own net
	// has not permitted somebody else's gateway.
	d = build(t, map[string]Permission{"other": {All: true}}).
		Route(transcoder("dvstick", 2, hbp.Timeslot2))
	if d.Routed() {
		t.Errorf("a permission named for %q delivered audio from %q: %v",
			"other", "dvstick", d.Targets)
	}
}

// TestAStrayZeroInAPermissionPermitsNobody guards the convention clash.
//
// AnyPeer is 0 in this package and matches every peer. A permission that
// carried that convention would mean a stray zero — a malformed list, a
// missing field in a form, an unparsed string — silently permitted every
// repeater on the network to carry possibly unlicensed audio. Permitting
// everything is a separate boolean for exactly this reason.
func TestAStrayZeroInAPermissionPermitsNobody(t *testing.T) {
	p := Permission{Peers: []hbp.RepeaterID{0}}
	if p.Permits(312345) {
		t.Error("a zero entry permitted an arbitrary peer")
	}
	if p.Permits(AnyPeer) {
		t.Error("a zero entry permitted AnyPeer")
	}
	// And an endpoint naming AnyPeer is withheld unless every peer is
	// permitted, because resolving "wherever it appears" needs the peer list
	// and this package is pure.
	if (Permission{Peers: []hbp.RepeaterID{312345}}).Permits(AnyPeer) {
		t.Error("a specific permission permitted an AnyPeer endpoint")
	}
	if !(Permission{All: true}).Permits(AnyPeer) {
		t.Error("permitting every peer did not permit an AnyPeer endpoint")
	}
}

// TestPermissionDoesNotGovernTrafficIntoTheTranscoder.
//
// The rule is about what leaves the vocoder for RF. A radio transmitting to a
// talkgroup bridged to a chip is a licensed operator putting their own audio
// into a transcoder, which needs nobody's permission — and gating it would
// make the mapping look broken while the configuration said it was right.
func TestPermissionDoesNotGovernTrafficIntoTheTranscoder(t *testing.T) {
	table, err := NewTable([]Bridge{{
		Name:    "zello",
		Enabled: true,
		Endpoints: []Endpoint{
			{Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			transcoder("dvstick", 2, hbp.Timeslot2),
		},
	}}, WithPermissions(nil))
	if err != nil {
		t.Fatalf("building a table: %v", err)
	}

	d := table.Route(Endpoint{Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2})
	if len(d.Targets) != 1 || d.Targets[0].Transcoder != "dvstick" {
		t.Fatalf("a radio's audio reached %v, want the transcoder", d.Targets)
	}
	if len(d.Withheld) != 0 {
		t.Errorf("traffic into the transcoder was withheld: %v", d.Withheld)
	}
}

// TestAPermissionIsCopiedIntoTheTable, so that a caller mutating its map
// afterwards cannot change an active table.
//
// Invariant I3: a call is never routed against a half-applied configuration.
// A permission held by reference would let a configuration reload change what
// a repeater is allowed to receive part-way through a transmission.
func TestAPermissionIsCopiedIntoTheTable(t *testing.T) {
	perm := map[string]Permission{"dvstick": {Peers: []hbp.RepeaterID{312345}}}
	table, err := NewTable([]Bridge{{
		Name:    "zello",
		Enabled: true,
		Endpoints: []Endpoint{
			{Peer: 312345, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			transcoder("dvstick", 2, hbp.Timeslot2),
		},
	}}, WithPermissions(perm))
	if err != nil {
		t.Fatalf("building a table: %v", err)
	}

	// Mutate the caller's map and its slice afterwards.
	perm["dvstick"] = Permission{All: true}
	delete(perm, "dvstick")

	d := table.Route(transcoder("dvstick", 2, hbp.Timeslot2))
	if len(d.Targets) != 1 || d.Targets[0].Peer != 312345 {
		t.Errorf("mutating the caller's permission map changed the table: %v", d.Targets)
	}
}

// TestAWithheldDestinationBecomesADropTheOperatorCanRead closes the hole 0366
// left.
//
// **The withheld list was produced and nothing consumed it**, which made a
// permission refusal invisible outside the routing package — the exact fault
// this project cited from COLLISIONS while building the permission, in the
// same patch. Constitution §18 forbids silently dropping traffic, and a
// repeater whose owner has not opted in looks identical to a repeater nobody
// bridged.
//
// Two properties, and the second is the one that would otherwise have gone
// unnoticed for a week:
//
//  1. The drop exists and names the destination and the setting to change.
//  2. It is a judgement but **not** a collision. The permission refuses on
//     every frame of every transcoded transmission to a peer that has not
//     opted in — fifty a second, by design — and counting those would peg
//     COLLISIONS and turn Traffic amber for a configuration working as
//     written.
func TestAWithheldDestinationBecomesADropTheOperatorCanRead(t *testing.T) {
	const (
		optedIn = hbp.RepeaterID(312345)
		hasNot  = hbp.RepeaterID(315544)
	)
	table, err := NewTable([]Bridge{{
		Name:    "zello",
		Enabled: true,
		Endpoints: []Endpoint{
			{Peer: optedIn, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			{Peer: hasNot, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			transcoder("dvstick", 2, hbp.Timeslot2),
		},
	}}, WithPermissions(map[string]Permission{
		"dvstick": {Peers: []hbp.RepeaterID{optedIn}},
	}))
	if err != nil {
		t.Fatalf("building a table: %v", err)
	}

	core, err := NewCore(CoreOptions{Table: table, Peers: bothReady{}})
	if err != nil {
		t.Fatalf("building a core: %v", err)
	}
	res := core.RouteFromTranscoder("dvstick", hbp.Data{
		SourceID: 3132911, TargetID: 2, Timeslot: hbp.Timeslot2,
		CallType: hbp.CallGroup, FrameType: hbp.FrameTypeVoice, StreamID: 1,
	}, time.Now())

	var found *Drop
	for i, d := range res.Drops {
		if d.To.Peer == hasNot {
			found = &res.Drops[i]
		}
	}
	if found == nil {
		t.Fatalf("no drop named peer %d, which the permission withheld; the "+
			"refusal would be invisible and §18 forbids that. Drops: %v",
			hasNot, res.Drops)
	}
	for _, want := range []string{"opted in", "permit_peers", "unlicensed"} {
		if !strings.Contains(found.Reason, want) {
			t.Errorf("the drop reason does not mention %q: %s", want, found.Reason)
		}
	}
	if found.NotAJudgement {
		t.Error("the drop is marked as deciding nothing; the operator decided " +
			"this repeater does not receive transcoded audio, and a leak to the " +
			"Motorola side would be that decision ignored")
	}
	if !found.NotACollision {
		t.Error("the drop counts as a collision; it refuses on every frame of " +
			"every transcoded transmission by design, and COLLISIONS would peg")
	}

	// And the permitted peer still receives.
	delivered := false
	for _, d := range res.Deliveries {
		if d.Peer == optedIn {
			delivered = true
		}
	}
	if !delivered {
		t.Errorf("the peer that opted in received nothing; deliveries %v, drops %v",
			res.Deliveries, res.Drops)
	}
}
