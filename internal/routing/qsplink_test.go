package routing_test

import (
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

// TestAFrameFromALinkIsNotRelayedYet records a deliberate gap rather than a
// decision.
//
// ADR-0051 replaces the never-relay rule with deduplication on source radio ID
// and stream ID, because ten servers meshed is forty-five peerings and relaying
// is what makes a large network simple. **The deduplication is not built**, and
// relaying without it is a broadcast storm on somebody else's network.
//
// So the blunt rule still holds for now, and this test says so out loud. When
// deduplication lands, this test is replaced rather than deleted quietly — a
// gap that closes without anyone noticing is a gap nobody can find again.
func TestAFrameFromALinkIsNotRelayedYet(t *testing.T) {
	c := qspCore(t, []string{"blake", "paul"}, 3132910)

	res := c.RouteFromUpstream("blake",
		groupCall(0x5678, 2, hbp.Timeslot2, hbp.FrameTypeVoiceSync), time.Now())

	for _, u := range res.Upstreams {
		t.Errorf("a frame from blake was relayed to %q with no deduplication in place", u.Upstream)
	}
	// And the drop that stopped it is a rule about links, not a verdict on the
	// transmission, so a Motorola repeater still hears it.
	for _, d := range res.Drops {
		if d.To.Upstream != "" && !d.NotAJudgement {
			t.Errorf("the loop rule for %q is classified as a judgement", d.To.Upstream)
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
