package routing

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

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

	// Traffic from the chip reaches the network, and does not come back to
	// the chip.
	back := table.Route(transcoder("dvstick", 2, hbp.Timeslot2))
	if !back.Routed() {
		t.Fatalf("a call from the transcoder went nowhere: %s", back.Reason)
	}
	for _, target := range back.Targets {
		if target.Transcoder != "" {
			t.Errorf("a call from the transcoder was routed back to %s", target)
		}
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
