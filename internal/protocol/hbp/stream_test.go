package hbp_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// streamKey identifies one traversal of one transmission over one link.
//
// Keying on StreamID alone is wrong and was a real mistake during development:
// a gateway relays the same transmission onto a second link with the same
// StreamID, so a stream-only key merges two traversals and double-counts every
// frame in them. Flow is part of the identity.
type streamKey struct {
	id   hbp.StreamID
	flow string
}

// TestVoiceStreamsHaveExactlyTwoSyncFrames asserts the structural invariant a
// decoder depends on: every transmission opens with a sync frame and closes
// with one.
//
// A stream that loses its terminator is the classic cause of a bridge welded
// open, so this is the check that matters most in the whole fixture.
func TestVoiceStreamsHaveExactlyTwoSyncFrames(t *testing.T) {
	sync := map[streamKey]int{}
	total := map[streamKey]int{}

	for _, p := range readCapture(t, voiceFixture) {
		msg, err := hbp.Parse(p.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", p.Index, err)
		}
		d, ok := msg.(hbp.Data)
		if !ok {
			continue
		}
		k := streamKey{id: d.StreamID, flow: p.Flow()}
		total[k]++
		if d.FrameType == hbp.FrameTypeSync {
			sync[k]++
		}
	}

	if len(total) == 0 {
		t.Fatal("no voice frames found in the fixture")
	}
	for k, n := range sync {
		if n != 2 {
			t.Errorf("stream %s on flow %s has %d sync frames, want exactly 2 (header and terminator); %d frames total",
				k.id, k.flow, n, total[k])
		}
	}

	logical := map[hbp.StreamID]bool{}
	for k := range total {
		logical[k.id] = true
	}
	t.Logf("%d logical streams across %d link traversals", len(logical), len(total))
	if len(total) != len(logical)*2 {
		t.Errorf("expected each of %d streams to traverse exactly 2 links, got %d traversals",
			len(logical), len(total))
	}
}

// TestRepeaterIDFieldPerLink records what the committed fixture actually shows.
//
// # A gap created by sanitisation
//
// The raw capture demonstrated that a gateway rewrites the repeater ID when
// relaying a frame inbound: frames arriving from the master carried the
// station's own ID, while the same frames relayed onward carried 1074180087.
//
// That behaviour is NOT present in this fixture. The only inbound frames in the
// raw capture were third-party transmissions, which had to be removed for
// consent reasons, and the rewrite went with them. Sanitising for privacy
// destroyed the evidence for a protocol behaviour.
//
// The behaviour is still believed real and is recorded in
// testdata/hbp/hbp-voice-session.md as an observation, explicitly marked as not
// fixture-backed. It must not be relied upon in implementation until a capture
// containing the operator's own inbound traffic reproduces it — a talkgroup
// 9990 parrot session would do it.
//
// This test therefore asserts the fixture's real contents, so that the gap is
// visible in the test suite rather than hidden in a comment.
func TestRepeaterIDFieldPerLink(t *testing.T) {
	byFlow := map[string]map[hbp.RepeaterID]int{}

	for _, p := range readCapture(t, voiceFixture) {
		msg, err := hbp.Parse(p.Payload)
		if err != nil {
			continue
		}
		d, ok := msg.(hbp.Data)
		if !ok {
			continue
		}
		if byFlow[p.Flow()] == nil {
			byFlow[p.Flow()] = map[hbp.RepeaterID]int{}
		}
		byFlow[p.Flow()][d.RepeaterID]++
	}

	if len(byFlow) != 2 {
		t.Fatalf("expected frames on 2 flows, got %d", len(byFlow))
	}
	for flow, ids := range byFlow {
		if len(ids) != 1 {
			t.Errorf("flow %s carries %d distinct repeater IDs, want 1: %v", flow, len(ids), ids)
		}
		for id, n := range ids {
			t.Logf("flow %-14s repeater ID %-12d %d frames", flow, id, n)
			if id != 3132910 {
				t.Errorf("flow %s carries repeater ID %d, want the capturing station's 3132910", flow, id)
			}
		}
	}

	t.Log("NOTE: this fixture contains outbound traffic only, so it cannot " +
		"exercise the inbound relay rewrite. See the doc comment above.")
}

// TestVoiceFixtureContainsOnlyTheCapturingStation guards the sanitisation.
//
// Third-party traffic was removed for consent reasons before this fixture was
// committed. If a future re-sanitisation reintroduces it, this fails.
func TestVoiceFixtureContainsOnlyTheCapturingStation(t *testing.T) {
	sources := map[uint32]int{}
	for _, p := range readCapture(t, voiceFixture) {
		msg, err := hbp.Parse(p.Payload)
		if err != nil {
			continue
		}
		if d, ok := msg.(hbp.Data); ok {
			sources[d.SourceID]++
		}
	}
	if len(sources) != 1 {
		t.Fatalf("fixture contains %d distinct source radio IDs, want 1: %v", len(sources), sources)
	}
	if _, ok := sources[3132910]; !ok {
		t.Errorf("expected only radio ID 3132910, got %v", sources)
	}
}

// TestVoiceFrameFieldsAreConsistent checks decoded values against the capture
// notes.
func TestVoiceFrameFieldsAreConsistent(t *testing.T) {
	var n int
	for _, p := range readCapture(t, voiceFixture) {
		msg, err := hbp.Parse(p.Payload)
		if err != nil {
			continue
		}
		d, ok := msg.(hbp.Data)
		if !ok {
			continue
		}
		n++
		if d.TargetID != 9999 {
			t.Fatalf("frame %d targets %d, want 9999", p.Index, d.TargetID)
		}
		if d.Timeslot != hbp.Timeslot2 {
			t.Fatalf("frame %d is on %s, want TS2", p.Index, d.Timeslot)
		}
		if d.CallType != hbp.CallGroup {
			t.Fatalf("frame %d is a %s call, want group", p.Index, d.CallType)
		}
		if len(d.Trailing) != 2 {
			t.Fatalf("frame %d has %d trailing bytes, want 2 (MMDVMHost link quality)", p.Index, len(d.Trailing))
		}
	}
	if n != 916 {
		t.Errorf("decoded %d voice frames, want 916", n)
	}
}

// TestParsedDataDoesNotAliasInput proves a parsed frame is safe to retain after
// the read buffer is reused, which a UDP server will do on every packet.
func TestParsedDataDoesNotAliasInput(t *testing.T) {
	buf := make([]byte, 55)
	copy(buf, "DMRD")
	buf[15] = 0x80 | 0x20 // TS2, sync frame
	for i := 20; i < 55; i++ {
		buf[i] = 0xAB
	}

	msg, err := hbp.Parse(buf)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := msg.(hbp.Data)

	// Simulate the next packet landing in the same buffer.
	for i := range buf {
		buf[i] = 0xFF
	}

	for i, b := range d.Payload {
		if b != 0xAB {
			t.Fatalf("payload byte %d changed to %#x when the input buffer was reused", i, b)
		}
	}
	for i, b := range d.Trailing {
		if b != 0xAB {
			t.Fatalf("trailing byte %d changed to %#x when the input buffer was reused", i, b)
		}
	}
}
