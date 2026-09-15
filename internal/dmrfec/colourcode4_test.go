package dmrfec

import (
	"testing"
)

// The colour code 4 capture: testdata/hbp/hbp-emb-colourcode-4.pcap,
// md5 ad1d8ce57d3fb1835dc19a3d131ebd4b, taken 2026-09-15.
//
// **These are a radio's own bytes.** Every EMB and every Link Control fragment
// below was transmitted by hardware and recorded on the production server —
// not derived, not predicted, not computed by the code they check. That is
// what makes them worth asserting: the generator in emb.go was recovered by
// search and its colour-code axis had never been observed, and annex B's
// embedded Link Control had never been compared against anything but itself.

// TestTheEmbsAtColourCodeFourAreTheOnesTheRadioSent is the prediction, tested.
//
// The EMB generator was found by searching all 256 degree-eight generators
// under a fifteen-bit-plus-parity model, with exactly one surviving every
// captured EMB. **But every capture held colour code 11**, so nothing had ever
// moved those four bits and the colour-code axis was a prediction.
//
// Repeater 3132913 transmits at colour code 4, and the model reproduces all
// four of its EMBs. Four unseen values predicted correctly on the first
// attempt is not a coincidence worth entertaining.
func TestTheEmbsAtColourCodeFourAreTheOnesTheRadioSent(t *testing.T) {
	for _, tc := range []struct {
		cc, lcss uint8
		want     uint16
		note     string
	}{
		// Colour code 11, the Pi-Star at 192.168.1.155. Known before this
		// capture and kept here so the two colour codes sit side by side.
		{11, LCSSSingle, 0xb01a, "burst F, no fragment"},
		{11, LCSSFirst, 0xb269, "burst B"},
		{11, LCSSLast, 0xb4ff, "burst E"},
		{11, LCSSContinuation, 0xb68c, "bursts C and D"},

		// Colour code 4, repeater 3132913 at 192.168.1.1. New, and the whole
		// reason for the capture.
		{4, LCSSSingle, 0x411e, "burst F, no fragment"},
		{4, LCSSFirst, 0x436d, "burst B"},
		{4, LCSSLast, 0x45fb, "burst E"},
		{4, LCSSContinuation, 0x4788, "bursts C and D"},
	} {
		got, err := EMBFor(tc.cc, tc.lcss)
		if err != nil {
			t.Errorf("colour code %d LCSS %d: %v", tc.cc, tc.lcss, err)
			continue
		}
		if got != tc.want {
			t.Errorf("colour code %d LCSS %d gives %04x and the radio sent %04x (%s)",
				tc.cc, tc.lcss, got, tc.want, tc.note)
		}
	}
}

// TestTheEmbeddedLinkControlIsTheOneTheRadioSent promotes annex B from a
// reading to a recording.
//
// 0371 built the embedded Link Control carriage from TS 102 361-1 annex B and
// said plainly that nothing had compared it against a radio. This is that
// comparison. The capture holds 68 complete groups — LCSS 1, 3, 3, 2 across
// four bursts of a superframe — from two radios at two colour codes, and every
// one carries this Link Control and these four fragments.
//
// **Both directions match.** QSP builds the same nine octets the radio built,
// and encodes them into the same four fragments the radio transmitted, and
// decodes the radio's fragments back to the same nine octets. The interleave,
// the eight-by-sixteen matrix, table B.16's Hamming generator and the
// modulo-31 checksum are all confirmed by that.
func TestTheEmbeddedLinkControlIsTheOneTheRadioSent(t *testing.T) {
	// What the radio transmitted, transcribed from the capture.
	radioLC := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x2f, 0xcd, 0xee}
	radioFragments := [EmbeddedLCBursts]uint32{
		0x05060606, 0x0f050303, 0x0f05360a, 0x003a3c39,
	}

	// The LC says group voice on talkgroup 2 from radio 3132910, which is
	// exactly what the DMRD headers in the same capture say — so the two
	// layers agree about who was talking.
	if flco := radioLC[0] & 0x3F; flco != 0x00 {
		t.Errorf("the radio's LC carries FLCO %#02x, want 0x00 for group voice", flco)
	}
	if tg := uint32(radioLC[3])<<16 | uint32(radioLC[4])<<8 | uint32(radioLC[5]); tg != 2 {
		t.Errorf("the radio's LC names talkgroup %d, want 2", tg)
	}
	if src := uint32(radioLC[6])<<16 | uint32(radioLC[7])<<8 | uint32(radioLC[8]); src != 3132910 {
		t.Errorf("the radio's LC names source %d, want 3132910", src)
	}

	// QSP builds the same nine octets.
	ours := LinkControlFor(2, 3132910, false)
	if string(ours) != string(radioLC) {
		t.Errorf("LinkControlFor gives %x and the radio sent %x", ours, radioLC)
	}

	// And encodes them into the same four fragments.
	frags, err := EncodeEmbeddedLC(ours)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if frags != radioFragments {
		t.Errorf("EncodeEmbeddedLC gives %08x and the radio sent %08x",
			frags, radioFragments)
	}

	// And reads the radio's own fragments back to the same Link Control, with
	// the checksum verifying.
	back, ok := DecodeEmbeddedLC(radioFragments)
	if !ok {
		t.Fatal("the radio's own fragments did not decode; the checksum, the " +
			"interleave or the matrix is wrong")
	}
	if string(back) != string(radioLC) {
		t.Errorf("the radio's fragments decoded to %x, want %x", back, radioLC)
	}
}

// TestBurstAsSyncPatternIsNotAnEmb records the trap that cost the first pass at
// this capture.
//
// Reading the middle 48 bits of every burst and treating them as an EMB
// produces three colour codes for a single transmission. Two of the values are
// not EMBs at all:
//
//   - `75f7`, which decodes as colour code 7 LCSS 2, is burst A's
//     synchronisation pattern. It appears **exactly one burst in six** — 9 of
//     54, 5 of 30, 15 of 90 — and that arithmetic is what gave it away.
//   - `df5d`, colour code 13 with the pre-emption bit set, is the voice LC
//     header and the terminator. Twice per transmission.
//
// **Neither is a colour code any radio was using**, and a fixture that recorded
// one as an observation would have poisoned the very generator this capture
// confirms. So the shape is asserted here: anything reading EMBs off a capture
// must skip the bursts that carry sync.
func TestBurstAsSyncPatternIsNotAnEmb(t *testing.T) {
	for _, notAnEMB := range []struct {
		value uint16
		what  string
	}{
		{0x75f7, "burst A's synchronisation pattern"},
		{0xdf5d, "the voice LC header and terminator's sync"},
	} {
		cc := uint8(notAnEMB.value >> 12)
		lcss := uint8(notAnEMB.value>>9) & 3
		got, err := EMBFor(cc, lcss)
		if err != nil {
			t.Errorf("%s: %v", notAnEMB.what, err)
			continue
		}
		if got == notAnEMB.value {
			t.Errorf("%04x is a valid EMB for colour code %d LCSS %d, so %s "+
				"cannot be distinguished from one by its bits alone — the "+
				"filtering has to come from which burst it is",
				notAnEMB.value, cc, lcss, notAnEMB.what)
		}
	}

	// And the pre-emption bit is what marks df5d as not an EMB from an
	// unencrypted network: every real EMB in every capture has it clear.
	if 0xdf5d>>11&1 != 1 {
		t.Error("df5d does not have the pre-emption bit set; the note above is wrong")
	}
	for _, real := range []uint16{0xb01a, 0xb269, 0xb4ff, 0xb68c, 0x411e, 0x436d, 0x45fb, 0x4788} {
		if real>>11&1 != 0 {
			t.Errorf("%04x is a captured EMB with the pre-emption bit set", real)
		}
	}
}
