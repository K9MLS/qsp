package routing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// ADR-0051: a link between two QSP servers is a peer, not a bridge.
//
// The day these exist to prevent: two instances linked over OpenBridge, both
// ends configured, both links reporting healthy, and no audio in either
// direction for a day — then a repeater keying on network audio with nothing
// audible, because the slot had been thrown away and everything downstream had
// to guess what to put back.

// qspCore builds a core with peers and links to other QSP servers.
func qspCore(t *testing.T, links []string, peers ...hbp.RepeaterID) *routing.Core {
	t.Helper()
	c, err := routing.NewCore(routing.CoreOptions{
		Peers:    peersReady(peers...),
		QSPLinks: links,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return c
}

// TestAQSPLinkNeedsNoBridge is the whole of "everything crosses by default".
//
// There is no bridge here, no export list, no import list and no endpoint
// carrying a timeslot. A club administrator who has agreed a peering with
// another club has said everything this needs, and the frame crosses.
func TestAQSPLinkNeedsNoBridge(t *testing.T) {
	c := qspCore(t, []string{"blake"}, 3132910)

	res := c.Route(3132910, groupCall(0x1234, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())

	if len(res.Upstreams) != 1 {
		t.Fatalf("a group call reached %d links, want 1: reason %q", len(res.Upstreams), res.Reason)
	}
	if res.Upstreams[0].Upstream != "blake" {
		t.Errorf("delivered to %q, want blake", res.Upstreams[0].Upstream)
	}
}

// TestTheTimeslotCrossesUnchanged is the defect this record was written after.
//
// OpenBridge forces TS1 because BrandMeister needs it to. Two instances of the
// same software have no reason to throw the slot away, and doing so silenced a
// Motorola repeater for a day: the frame crossed as TS1, reached the far end's
// IPSC relay as TS1, and the repeater keyed on the slot nobody was listening
// to while the codeplug had TG 2 on TS 2.
//
// §6b gives talkgroups this rule already — 2 is 2 on both sides of a hotspot.
// The timeslot now has it too.
func TestTheTimeslotCrossesUnchanged(t *testing.T) {
	for _, slot := range []hbp.Timeslot{hbp.Timeslot1, hbp.Timeslot2} {
		c := qspCore(t, []string{"blake"}, 3132910)

		res := c.Route(3132910, groupCall(0x2345, 2, slot, hbp.FrameTypeVoiceSync), time.Now())

		if len(res.Upstreams) != 1 {
			t.Fatalf("TS%d: reached %d links, want 1: %q", slot, len(res.Upstreams), res.Reason)
		}
		if got := res.Upstreams[0].Frame.Timeslot; got != slot {
			t.Errorf("a frame on TS%d left for the link on TS%d", slot, got)
		}
		if got := res.Upstreams[0].Frame.TargetID; got != 2 {
			t.Errorf("talkgroup 2 left for the link as %d", got)
		}
	}
}

// TestEveryTalkgroupCrosses, because the link carries no list of them.
//
// The far end is not a foreign network to be metered. It runs the same
// software and its own access lists decide what it keeps, which is what an
// administrator already means when they say two servers are linked. A server
// carrying TG 2 and TG 4 links to one carrying TG 2 and TG 6: TG 2 works, TG 4
// dies at the far end's ingress, and nothing about that is configured here.
func TestEveryTalkgroupCrosses(t *testing.T) {
	c := qspCore(t, []string{"blake"}, 3132910)

	for _, tg := range []uint32{2, 4, 91, 3148} {
		res := c.Route(3132910, groupCall(hbp.StreamID(tg), tg, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())
		if len(res.Upstreams) != 1 {
			t.Errorf("talkgroup %d reached %d links, want 1: %q", tg, len(res.Upstreams), res.Reason)
			continue
		}
		if got := res.Upstreams[0].Frame.TargetID; got != tg {
			t.Errorf("talkgroup %d arrived at the link as %d", tg, got)
		}
	}
}

// TestAnOpenBridgeLinkIsNotReachedByRepeat scopes the rule.
//
// OpenBridge reaches a foreign network — BrandMeister, DMR+ — where what
// crosses is a decision an operator has to make deliberately, and a bridge is
// how they make it. Repeat reaching every link would export a club's local
// nets to somebody else's network the moment a link was configured.
//
// A link is named in QSPLinks or it is not, and this is the "or it is not"
// half: with an empty list the package routes exactly as it did before.
func TestAnOpenBridgeLinkIsNotReachedByRepeat(t *testing.T) {
	c := qspCore(t, nil, 3132910)

	res := c.Route(3132910, groupCall(0x3456, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())

	if len(res.Upstreams) != 0 {
		t.Errorf("repeat reached %d link(s) that were not named as QSP links", len(res.Upstreams))
	}
}

// TestAFrameFromAQSPLinkReachesThePeers is the direction that carries somebody
// else's members to this network's radios.
func TestAFrameFromAQSPLinkReachesThePeers(t *testing.T) {
	c := qspCore(t, []string{"blake"}, 3132910, 3155373)

	res := c.RouteFromUpstream("blake",
		groupCall(0x4567, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())

	if len(res.Deliveries) != 2 {
		t.Fatalf("a frame from a link reached %d peers, want 2: %q", len(res.Deliveries), res.Reason)
	}
	for _, d := range res.Deliveries {
		if d.Frame.Timeslot != hbp.Timeslot2 {
			t.Errorf("peer %d was sent TS%d for a frame that arrived on TS2", d.Peer, d.Frame.Timeslot)
		}
	}
}

// TestAFrameFromALinkIsRelayedToTheOthers is what lets a club join through one
// neighbour (ADR-0051).
//
// A full mesh of ten servers is forty-five peerings, nine per administrator,
// and an eleventh server means ten more coordinated with ten people. That is
// not simple, it is the opposite. Relaying is what makes a large network
// simple, and it replaced the blunt rule that a frame from a link is never
// sent to a link — a rule that was correct only because nothing recognised a
// duplicate.
func TestAFrameFromALinkIsRelayedToTheOthers(t *testing.T) {
	c := qspCore(t, []string{"blake", "paul", "pete"}, 3132910)

	res := c.RouteFromUpstream("blake",
		groupCall(0x5678, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())

	got := map[string]bool{}
	for _, u := range res.Upstreams {
		got[u.Upstream] = true
		if u.Frame.Timeslot != hbp.Timeslot2 {
			t.Errorf("%s was relayed TS%d for a frame that arrived on TS2", u.Upstream, u.Frame.Timeslot)
		}
	}
	for _, want := range []string{"paul", "pete"} {
		if !got[want] {
			t.Errorf("a frame from blake was not relayed to %q: %q", want, res.Reason)
		}
	}
	if got["blake"] {
		t.Error("a frame from blake was relayed back to blake")
	}
}

// TestTheSameTransmissionByTwoPathsIsCarriedOnce is the guard that makes
// relaying safe, and the reason it could not be enabled before.
//
// Three servers all linked to each other: a frame reaches this one directly
// from blake and again by way of paul. Without deduplication that is an echo
// on every radio, and around a ring it is a storm on somebody else's network.
//
// **First path wins.** The copy arriving second reaches nothing at all — not
// the peers and not the Motorola side, which is why this refusal carries no
// NoHomebrewDestination. Delivering a duplicate to the repeaters and not the
// hotspots would be the worst of both.
func TestTheSameTransmissionByTwoPathsIsCarriedOnce(t *testing.T) {
	c := qspCore(t, []string{"blake", "paul"}, 3132910)
	frame := groupCall(0x9abc, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync)
	at := time.Now()

	first := c.RouteFromUpstream("blake", frame, at)
	if len(first.Deliveries) == 0 {
		t.Fatalf("the first copy was not carried: %q", first.Reason)
	}

	second := c.RouteFromUpstream("paul", frame, at.Add(20*time.Millisecond))
	if len(second.Deliveries) != 0 || len(second.Upstreams) != 0 {
		t.Errorf("the same transmission by a second path was carried again: %d peers, %d links",
			len(second.Deliveries), len(second.Upstreams))
	}
	if second.NoHomebrewDestination {
		t.Error("a duplicate is reported as unjudged, so it would reach the Motorola repeaters")
	}
	if !strings.Contains(second.Reason, "blake") {
		t.Errorf("the refusal does not name the path already carrying it: %q", second.Reason)
	}
}

// TestTheRestOfATransmissionIsNotItsOwnDuplicate, which is every frame after
// the first and therefore the overwhelmingly common case.
//
// Getting this wrong would drop all but the opening burst of every
// transmission on the network, which is a fault that sounds like a broken
// vocoder rather than like routing.
func TestTheRestOfATransmissionIsNotItsOwnDuplicate(t *testing.T) {
	c := qspCore(t, []string{"blake"}, 3132910)
	at := time.Now()

	for i := range 30 {
		res := c.RouteFromUpstream("blake",
			groupCall(0xbcde, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync),
			at.Add(time.Duration(i)*60*time.Millisecond))
		if len(res.Deliveries) == 0 {
			t.Fatalf("frame %d of the same transmission was dropped: %q", i, res.Reason)
		}
	}
}

// TestAStreamIDIsReusableAfterTheTransmissionEnds.
//
// Two networks pick stream IDs independently and a club network runs for
// months, so the pair will come round again. Holding it forever would refuse a
// later transmission for resembling an older one, which on the air is a radio
// that works in the morning and not in the afternoon.
func TestAStreamIDIsReusableAfterTheTransmissionEnds(t *testing.T) {
	c := qspCore(t, []string{"blake", "paul"}, 3132910)
	frame := groupCall(0xcdef, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync)
	at := time.Now()

	if res := c.RouteFromUpstream("blake", frame, at); len(res.Deliveries) == 0 {
		t.Fatalf("the first transmission was not carried: %q", res.Reason)
	}
	// Long enough that the earlier transmission has plainly ended.
	later := at.Add(routing.StreamTimeout * 4)
	res := c.RouteFromUpstream("paul", frame, later)
	if len(res.Deliveries) == 0 {
		t.Errorf("a later transmission reusing the identifiers was refused: %q", res.Reason)
	}
}

// TestTwoRadiosMayShareAStreamID, because the key is the pair.
//
// Stream IDs are chosen independently by every network on a mesh, so a
// collision between two people talking at once is a matter of time rather than
// malice. Keying on the stream alone would silence one of them.
func TestTwoRadiosMayShareAStreamID(t *testing.T) {
	c := qspCore(t, []string{"blake"}, 3132910)
	at := time.Now()

	first := groupCall(0xdead, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync)
	first.SourceID = 3132910
	second := groupCall(0xdead, 2, hbp.Timeslot1, hbp.FrameTypeVoiceSync)
	second.SourceID = 3155373

	if res := c.RouteFromUpstream("blake", first, at); len(res.Deliveries) == 0 {
		t.Fatalf("the first radio was not carried: %q", res.Reason)
	}
	res := c.RouteFromUpstream("blake", second, at.Add(10*time.Millisecond))
	if len(res.Deliveries) == 0 {
		t.Errorf("a second radio sharing a stream ID was refused as a duplicate: %q", res.Reason)
	}
}

// TestAFrameFromALinkIsNeverSentToAForeignNetwork keeps the blunt rule where
// it is still the right answer.
//
// BrandMeister disconnects bridges caught re-bridging, and its deduplication
// is not ours to rely on: a copy we relay there is indistinguishable from a
// new transmission. So an OpenBridge target still refuses anything that
// arrived over a link, and only QSP links relay.
func TestAFrameFromALinkIsNeverSentToAForeignNetwork(t *testing.T) {
	// A bridge to a foreign network, and a QSP link beside it.
	table, err := routing.NewTable([]routing.Bridge{{
		Name:    "brandmeister",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot1},
			{Upstream: "brandmeister", Talkgroup: 2, Timeslot: hbp.Timeslot1},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	c, err := routing.NewCore(routing.CoreOptions{
		Table: table, Peers: peersReady(3132910), QSPLinks: []string{"blake"},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	res := c.RouteFromUpstream("blake",
		groupCall(0xfeed, 2, hbp.Timeslot1, hbp.FrameTypeVoiceSync), time.Now())

	for _, u := range res.Upstreams {
		if u.Upstream == "brandmeister" {
			t.Error("a frame from a QSP link was relayed onto a foreign network")
		}
	}
}

// TestALinkIsNeverSentItsOwnFrame, which is the one part of the loop rule that
// survives deduplication unchanged.
func TestALinkIsNeverSentItsOwnFrame(t *testing.T) {
	c := qspCore(t, []string{"blake"}, 3132910)

	res := c.RouteFromUpstream("blake",
		groupCall(0x6789, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())

	for _, u := range res.Upstreams {
		if u.Upstream == "blake" {
			t.Error("a frame from blake was sent back to blake")
		}
	}
}

// TestTheDecisionIsReproducibleWithSeveralLinks.
//
// ADR-0013 makes the routing decision a pure function of its inputs, and Go
// randomises map iteration deliberately. Ranging a map here would make two
// runs on identical state produce different orders — the kind of difference
// that turns a defect into one nobody can reproduce.
func TestTheDecisionIsReproducibleWithSeveralLinks(t *testing.T) {
	links := []string{"paul", "blake", "pete", "alpha", "bravo"}
	c := qspCore(t, links, 3132910)

	// One stream throughout: a second stream ID would be a second transmission
	// and the links would refuse it as busy, which is contention working
	// correctly and not what this test is about.
	var first []string
	for run := range 20 {
		res := c.Route(3132910,
			groupCall(0x7000, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())
		got := make([]string, 0, len(res.Upstreams))
		for _, u := range res.Upstreams {
			got = append(got, u.Upstream)
		}
		if len(got) != len(links) {
			t.Fatalf("run %d reached %d links, want %d", run, len(got), len(links))
		}
		if first == nil {
			first = got
			continue
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("run %d ordered the links %v, the first run gave %v", run, got, first)
			}
		}
	}
}
