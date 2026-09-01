package ipsc_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// voiceCalls groups the voice frames in the probe capture by their call
// counter, which is the field that separates one transmission from the next.
func voiceCalls(tb testing.TB) map[uint8][]ipsc.Voice {
	tb.Helper()
	cap := readCapture(tb, probeVoice)
	out := map[uint8][]ipsc.Voice{}
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			tb.Fatalf("packet %d: %v", pkt.Index, err)
		}
		v, ok := msg.AsVoice()
		if !ok {
			continue
		}
		out[v.CallCounter] = append(out[v.CallCounter], v)
	}
	if len(out) == 0 {
		tb.Fatal("no voice frames in the probe capture")
	}
	return out
}

// TestTheCallCounterSeparatesTransmissions records the field whose position
// invited the wrong reading.
//
// Byte 5 sits where a timeslot would sit and it is not one: it counted 1, 2, 3,
// 4 across four key-ups, including across a restart of the probe. The repeater
// is counting transmissions, not sessions.
func TestTheCallCounterSeparatesTransmissions(t *testing.T) {
	calls := voiceCalls(t)
	if len(calls) < 3 {
		t.Fatalf("%d distinct call counters, want the three transmissions in this capture", len(calls))
	}
	for id, frames := range calls {
		if id == 0 {
			t.Error("a call counter of zero; the capture starts at 1")
		}
		if len(frames) < 10 {
			t.Errorf("call %d has %d frames, too few to be a five-second transmission", id, len(frames))
		}
	}
}

// TestAStreamIDHoldsWithinACallAndDiffersBetweenThem is what lets a router tell
// two overlapping transmissions apart, and the reason it is safe to say so is
// that three calls in one file agreed.
func TestAStreamIDHoldsWithinACallAndDiffersBetweenThem(t *testing.T) {
	calls := voiceCalls(t)
	seen := map[uint16]uint8{}
	for id, frames := range calls {
		first := frames[0].StreamID
		for i, f := range frames {
			if f.StreamID != first {
				t.Errorf("call %d frame %d: stream %#04x, want %#04x", id, i, f.StreamID, first)
			}
		}
		if other, dup := seen[first]; dup {
			t.Errorf("calls %d and %d share stream %#04x", id, other, first)
		}
		seen[first] = id
	}
}

// TestSequenceAdvancesByOneAndTimestampByFourHundredAndEighty is the timing
// that makes the message a real-time stream rather than a datagram.
//
// 480 samples at eight kilohertz is sixty milliseconds, and sixty milliseconds
// is one DMR voice frame. The frames also arrived sixty milliseconds apart, so
// the timestamp is the media clock rather than an arrival time.
func TestSequenceAdvancesByOneAndTimestampByFourHundredAndEighty(t *testing.T) {
	for id, frames := range voiceCalls(t) {
		for i := 1; i < len(frames); i++ {
			if d := frames[i].Sequence - frames[i-1].Sequence; d != 1 {
				t.Errorf("call %d frame %d: sequence advanced by %d, want 1", id, i, d)
			}
			if d := frames[i].Timestamp - frames[i-1].Timestamp; d != 480 {
				t.Errorf("call %d frame %d: timestamp advanced by %d, want 480", id, i, d)
			}
		}
	}
}

// TestACallHasAMarkedBeginningAndEnd is what a routing server needs in order to
// know when to start relaying and when to stop.
//
// A transmission opens with 0x80dd, runs on 0x805d and closes with 0x805e. QSP
// learned the same lesson on HBP the hard way: a stream with no terminator is a
// stream that never ends.
func TestACallHasAMarkedBeginningAndEnd(t *testing.T) {
	for id, frames := range voiceCalls(t) {
		if !frames[0].IsFirstFrame() {
			t.Errorf("call %d opens with flags %#04x, which is not a first frame", id, frames[0].Flags)
		}
		last := frames[len(frames)-1]
		if !last.IsLastFrame() {
			t.Errorf("call %d closes with flags %#04x, which is not a last frame", id, last.Flags)
		}
		for i, f := range frames[1 : len(frames)-1] {
			if f.IsFirstFrame() || f.IsLastFrame() {
				t.Errorf("call %d frame %d is marked as a boundary mid-transmission: %#04x", id, i+1, f.Flags)
			}
		}
	}
}

// TestTheSourceIsTwentyFourBitsWhileTheEnvelopeIsThirtyTwo pins a distinction
// that is easy to lose because both fields held the same number here.
//
// The radio keyed was the repeater's own ID, so SenderID and SourceID agreed.
// They are different fields at different widths and a radio that is not the
// repeater will separate them.
func TestTheSourceIsTwentyFourBitsWhileTheEnvelopeIsThirtyTwo(t *testing.T) {
	cap := readCapture(t, probeVoice)
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		v, ok := msg.AsVoice()
		if !ok {
			continue
		}
		if v.SourceID > 0xffffff {
			t.Errorf("packet %d: source %d does not fit in 24 bits", pkt.Index, v.SourceID)
		}
		if v.SourceID != msg.SenderID {
			t.Logf("packet %d: source %d differs from sender %d — worth a fixture, the captures never separated them",
				pkt.Index, v.SourceID, msg.SenderID)
		}
	}
}

// TestTheDestinationNeverMovedIsWhyItIsUnverified is a test that records the
// absence of evidence rather than evidence.
//
// All four captured transmissions went to the same destination, so nothing has
// ever moved these bytes. Their position is a reading. If a future capture has
// two destinations in it, this test should fail and be replaced by one that
// asserts the field properly.
func TestTheDestinationNeverMovedIsWhyItIsUnverified(t *testing.T) {
	seen := map[uint32]bool{}
	for _, frames := range voiceCalls(t) {
		for _, f := range frames {
			seen[f.Destination] = true
		}
	}
	if len(seen) != 1 {
		t.Errorf("%d distinct destinations in this capture; the field can now be verified properly "+
			"and this test should be replaced", len(seen))
	}
}

// TestBothRegistrationStatesAreInOneFile records a happy accident worth
// keeping: the capture was started before the probe, so it holds the repeater
// being refused and then accepted, minutes apart, on one link.
func TestBothRegistrationStatesAreInOneFile(t *testing.T) {
	cap := readCapture(t, probeVoice)
	var requests, replies int
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		switch msg.Kind {
		case ipsc.KindRegisterRequest:
			requests++
		case ipsc.KindRegisterReply:
			replies++
		}
	}
	if requests < 2 || replies != 1 {
		t.Errorf("%d requests and %d replies; this fixture is meant to hold the unanswered "+
			"retries and the single answer that ended them", requests, replies)
	}
}

// TestTheVocoderPayloadIsNineteenBytesNotThirtyThree is the finding that
// decides how QSP will have to bridge Motorola.
//
// A DMR burst is 33 bytes: vocoder data wrapped in FEC and sync. Every one of
// the fifty-four captured IPSC voice frames carries 19. So IPSC does **not**
// carry the burst verbatim, and a bridge between IPSC and HBP cannot be a copy.
func TestTheVocoderPayloadIsNineteenBytesNotThirtyThree(t *testing.T) {
	cap := readCapture(t, probeVoice)
	var voice, distinct int
	seen := map[string]bool{}
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		class, vocoder, trailer, ok := msg.Payload()
		if !ok {
			continue
		}
		voice++
		if len(vocoder) != ipsc.VocoderLen {
			t.Errorf("packet %d: vocoder payload %d bytes, want %d", pkt.Index, len(vocoder), ipsc.VocoderLen)
		}
		if len(vocoder) == 33 {
			t.Errorf("packet %d: 33 bytes would be a DMR burst; the whole finding is that it is not", pkt.Index)
		}
		switch len(trailer) {
		case 0, 5, 14:
		default:
			t.Errorf("packet %d: trailer %d bytes, want 0, 5 or 14", pkt.Index, len(trailer))
		}
		switch class {
		case 0x40, 0x06, 0x16:
		default:
			t.Errorf("packet %d: payload class %#02x was never captured", pkt.Index, class)
		}
		if !seen[string(vocoder)] {
			seen[string(vocoder)] = true
			distinct++
		}
	}
	if voice == 0 {
		t.Fatal("no voice payloads")
	}
	// If the vocoder bytes never changed they would not be audio.
	if distinct < voice/2 {
		t.Errorf("%d distinct vocoder payloads in %d frames; too few to be speech", distinct, voice)
	}
}

// TestLinkControlAgreesWithTheHeader is the corroboration that makes the
// Destination reading worth more than a position in a struct.
//
// The long frames of a superframe carry Link Control, which DMR sends so a radio
// joining mid-transmission learns who is talking to whom. It encodes the same
// destination and source as the header, in a different layout, in the same
// packet.
func TestLinkControlAgreesWithTheHeader(t *testing.T) {
	cap := readCapture(t, probeVoice)
	var checked int
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		v, isVoice := msg.AsVoice()
		if !isVoice {
			continue
		}
		dst, src, ok := msg.LinkControl()
		if !ok {
			continue
		}
		checked++
		if dst != v.Destination {
			t.Errorf("packet %d: link control destination %d, header %d", pkt.Index, dst, v.Destination)
		}
		if src != v.SourceID {
			t.Errorf("packet %d: link control source %d, header %d", pkt.Index, src, v.SourceID)
		}
	}
	if checked == 0 {
		t.Fatal("no frames carried link control; the superframe should produce one per six")
	}
}
