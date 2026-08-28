package peers_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Subscriber location. See docs/adr/ADR-0021-private-calls-and-data.md.
//
// A private call's destination is a radio, and a radio's whereabouts is a
// property of where somebody is standing rather than of the configuration. It
// has to be learned from traffic, and it has to age.

func withSubscriberTimeout(d time.Duration) func(*peers.MasterConfig) {
	return func(cfg *peers.MasterConfig) { cfg.SubscriberTimeout = d }
}

func voiceOn(source uint32, tg uint32, slot hbp.Timeslot, stream hbp.StreamID) hbp.Data {
	return hbp.Data{
		SourceID:   source,
		TargetID:   tg,
		RepeaterID: testID,
		Timeslot:   slot,
		CallType:   hbp.CallGroup,
		FrameType:  hbp.FrameTypeSync,
		StreamID:   stream,
	}
}

func TestARadioIsLocatedByTransmitting(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	if _, ok := h.m.Locate(3121001); ok {
		t.Fatal("a radio that has never transmitted was located")
	}

	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0x1111), addrA)

	loc, ok := h.m.Locate(3121001)
	if !ok {
		t.Fatal("a radio that just transmitted could not be located")
	}
	if loc.Peer != testID {
		t.Errorf("located at peer %d, want %d", loc.Peer, testID)
	}
	if loc.Timeslot != hbp.Timeslot2 {
		t.Errorf("located on %s, want TS2", loc.Timeslot)
	}
	if loc.Subscriber != 3121001 {
		t.Errorf("located subscriber %d, want 3121001", loc.Subscriber)
	}
}

// TestRadioIDZeroIsNotLocated stops a malformed frame creating a routable
// destination that is not a station.
func TestRadioIDZeroIsNotLocated(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	h.send(voiceOn(0, 9, hbp.Timeslot2, 0x2222), addrA)
	if _, ok := h.m.Locate(0); ok {
		t.Error("radio ID 0 was recorded as a location")
	}
	if h.m.SubscriberCount() != 0 {
		t.Errorf("radio ID 0 left %d entries behind", h.m.SubscriberCount())
	}
}

// TestTheNewestSightingWins is the driving-to-work case. Preferring the
// incumbent would send private calls to the hotspot somebody has just left,
// which is the failure this whole mechanism exists to prevent.
func TestTheNewestSightingWins(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)

	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0x3333), addrA)
	first, _ := h.m.Locate(3121001)

	// The same radio, later, on the other timeslot.
	h.c.advance(time.Minute)
	h.send(voiceOn(3121001, 9, hbp.Timeslot1, 0x4444), addrA)

	loc, ok := h.m.Locate(3121001)
	if !ok {
		t.Fatal("the radio could not be located after moving")
	}
	if loc.Timeslot != hbp.Timeslot1 {
		t.Errorf("still located on %s; the newest sighting should win", loc.Timeslot)
	}
	if !loc.LastHeard.After(first.LastHeard) {
		t.Error("LastHeard did not advance")
	}
	// FirstSeen records when the radio was first heard anywhere, and does not
	// move when it does.
	if !loc.FirstSeen.Equal(first.FirstSeen) {
		t.Error("FirstSeen moved when the radio did")
	}
	if h.m.SubscriberCount() != 1 {
		t.Errorf("a moving radio left %d entries, want 1", h.m.SubscriberCount())
	}
}

func TestALocationAgesOut(t *testing.T) {
	h := newHarness(t, withSubscriberTimeout(30*time.Minute))
	h.login(addrA)
	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0x5555), addrA)

	h.c.advance(29 * time.Minute)
	if _, ok := h.m.Locate(3121001); !ok {
		t.Error("a location expired before its timeout")
	}

	h.c.advance(2 * time.Minute)
	if _, ok := h.m.Locate(3121001); ok {
		t.Error("a location outlived its timeout")
	}
}

// TestExpireForgetsStaleLocations is about the map rather than the answer.
// Without it there is one entry per radio ever heard, for the life of the
// process, and each is a location claim that gets less true with age.
func TestExpireForgetsStaleLocations(t *testing.T) {
	h := newHarness(t, withSubscriberTimeout(30*time.Minute))
	h.login(addrA)
	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0x6666), addrA)

	if h.m.SubscriberCount() != 1 {
		t.Fatalf("the radio was not recorded")
	}
	h.c.advance(31 * time.Minute)
	h.m.Expire()

	if h.m.SubscriberCount() != 0 {
		t.Errorf("Expire left %d stale locations behind", h.m.SubscriberCount())
	}
}

// TestALocationOutlivesAQuietRadio is why the subscriber timeout is separate
// from the peer timeout. A peer that stops sending keepalives is gone; a radio
// that stops transmitting is merely quiet, and quiet is a radio's normal state.
func TestALocationOutlivesAQuietRadio(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)
	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0x7777), addrA)

	// Well past the peer timeout, nowhere near the subscriber timeout. The
	// peer keeps its registration alive with keepalives while the operator
	// says nothing for ten minutes, which is entirely ordinary.
	for i := 0; i < 60; i++ {
		h.c.advance(10 * time.Second)
		h.send(hbp.Ping{RepeaterID: testID}, addrA)
		h.m.Expire()
	}

	if _, ok := h.m.Locate(3121001); !ok {
		t.Error("a radio that had been quiet for ten minutes was forgotten")
	}
}

// TestALocationIsUnusableOnceItsPeerHasGone stops a private call being routed
// to a socket nobody is listening on, where the caller would hear nothing with
// no explanation.
func TestALocationIsUnusableOnceItsPeerHasGone(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)
	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0x8888), addrA)

	if _, ok := h.m.Locate(3121001); !ok {
		t.Fatal("the radio was not located while its peer was connected")
	}

	// The peer times out. The radio's location is still remembered, but it is
	// no longer somewhere a call can be sent.
	h.c.advance(2 * time.Minute)
	h.m.Expire()

	if _, ok := h.m.Locate(3121001); ok {
		t.Error("a radio behind a departed peer was reported as locatable")
	}
	// Still listed, because "this radio was here and its hotspot has left" is
	// information an operator wants rather than something to hide.
	if len(h.m.Locations()) != 1 {
		t.Errorf("the location was discarded entirely; got %d", len(h.m.Locations()))
	}
}

// TestARefusedSubscriberIsNotLocated is the ordering ADR-0021 asked for: one
// list governing both transmission and reachability. A radio refused permission
// to talk must not become a private call destination.
func TestARefusedSubscriberIsNotLocated(t *testing.T) {
	h := newHarness(t, withAccess(access.Lists{
		Subscriber: mustParse(t, access.Subscriber, access.ModeDeny, "3121077"),
	}))
	h.login(addrA)

	h.send(voiceOn(3121077, 9, hbp.Timeslot2, 0x9999), addrA)
	if _, ok := h.m.Locate(3121077); ok {
		t.Error("a refused subscriber was recorded as a routable location")
	}

	// A permitted radio on the same peer is recorded as normal.
	h.send(voiceOn(3121001, 9, hbp.Timeslot2, 0xAAAA), addrA)
	if _, ok := h.m.Locate(3121001); !ok {
		t.Error("a permitted subscriber was not recorded")
	}
}

func TestLocationsAreOrderedAndCopied(t *testing.T) {
	h := newHarness(t)
	h.login(addrA)
	for _, id := range []uint32{3121003, 3121001, 3121002} {
		h.send(voiceOn(id, 9, hbp.Timeslot2, hbp.StreamID(id)), addrA)
	}

	got := h.m.Locations()
	if len(got) != 3 {
		t.Fatalf("got %d locations, want 3", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Subscriber >= got[i].Subscriber {
			t.Errorf("locations are not ordered by subscriber: %v", got)
		}
	}
	// A caller must not be able to mutate the registry through the snapshot.
	got[0].Peer = 999999
	again := h.m.Locations()
	if again[0].Peer == 999999 {
		t.Error("Locations returned a view into master state")
	}
}
