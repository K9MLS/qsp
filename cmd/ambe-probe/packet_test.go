package main

import (
	"encoding/hex"
	"testing"

	"github.com/k9mls/qsp/internal/ambe"
)

// TestTheFieldsTheProbeSendsAreTheOnesTheManualNames checks the identifiers
// this program uses against the table they came from.
//
// The identifiers are named locally so that a call site reads the way the
// manual does, but their lengths are not repeated here. Repeating a length
// beside a call site is how the eleven in the old comment came to sit next to
// the twelve in the manual, so the length lives in one place and this test
// confirms the identifier still means what the name says.
func TestTheFieldsTheProbeSendsAreTheOnesTheManualNames(t *testing.T) {
	control, ok := ambe.FieldsFor(ambe.TypeControl)
	if !ok {
		t.Fatal("there is no control field table")
	}
	byID := map[byte]ambe.Field{}
	for _, f := range control {
		byID[f.ID] = f
	}

	for _, tc := range []struct {
		id   byte
		name string
	}{
		{fieldReset, "PKT_RESET"},
		{fieldProdID, "PKT_PRODID"},
		{fieldVersion, "PKT_VERSTRING"},
		{fieldGetCfg, "PKT_GETCFG"},
		{fieldRateIndex, "PKT_RATET"},
		{fieldChannel0, "PKT_CHANNEL0"},
	} {
		f, ok := byID[tc.id]
		if !ok {
			t.Errorf("field %#02x is not in the control table; the probe sends it", tc.id)
			continue
		}
		if f.Name != tc.name {
			t.Errorf("field %#02x is %s in the table and %s here", tc.id, f.Name, tc.name)
		}
	}
}

// TestTheRateIsSetWithTheOneByteIndex is the defect that cost a dongle, from
// the caller's side.
//
// On 2026-09-14 this program sent `61 00 02 00 0a 21`: PKT_RATEP with one
// argument byte where the manual gives twelve. The chip waited for the rest,
// took it from the head of the next packet, and stayed mid-field until its
// power was physically removed — a soft reset could not clear it and neither
// could detaching the USB device in ESXi.
//
// **The gate this replaces was a search of main.go for a string.** It read the
// source for one particular spelling of the call and would have passed on any
// rewording of the same line, and failed on a harmless one. The real gate is
// now that internal/ambe refuses to build a field whose data length disagrees
// with the manual, so what is left to check here is the rate the probe asks
// for.
func TestTheRateIsSetWithTheOneByteIndex(t *testing.T) {
	if fieldRateIndex != 0x09 {
		t.Fatalf("the rate field is %#02x, want 0x09; 0x0a is the twelve-byte "+
			"rate word and sending one byte to it wedges the chip", fieldRateIndex)
	}

	got, err := ambe.Build(ambe.TypeControl, ambe.Val(fieldRateIndex, ambe.RateIndexDMR))
	if err != nil {
		t.Fatalf("building the rate packet: %v", err)
	}
	if want := "6100020009" + "21"; hex.EncodeToString(got) != want {
		t.Errorf("the rate packet is %s, want %s", hex.EncodeToString(got), want)
	}

	// Table 115, printed page 90, and the note beneath it: index 33 is
	// 3600/2450/1150, the rate interoperable with DMR and APCO P25 half rate.
	if ambe.RateIndexDMR != 33 {
		t.Errorf("the DMR rate index is %d, want 33", ambe.RateIndexDMR)
	}
}

// TestASpeechFrameIsTwentyMillisecondsAtEightKilohertz keeps the frame size
// honest.
//
// 160 samples is what the part takes for one compressed frame. A frame of any
// other length is a different question being asked, and the answer would not
// mean what it appears to.
//
// The resulting packet is 327 bytes, which is the size of the manufacturer's
// own Speech Packet Example 1 — a bare PKT_CHANNEL0 and a 160-sample SPEECHD
// field. That example is a fixture in testdata/ambe, so the arithmetic here
// and the arithmetic there have to agree.
func TestASpeechFrameIsTwentyMillisecondsAtEightKilohertz(t *testing.T) {
	s := sine(1000)
	if len(s) != samplesPerFrame {
		t.Fatalf("a frame is %d samples, want %d", len(s), samplesPerFrame)
	}

	speech, err := ambe.SpeechD(s)
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	pkt, err := ambe.Build(ambe.TypeSpeech, ambe.Val(fieldChannel0), speech)
	if err != nil {
		t.Fatalf("building the speech packet: %v", err)
	}
	// Four header bytes, a bare PKT_CHANNEL0, a SPEECHD identifier, a sample
	// count, and two bytes per sample.
	if got, want := len(pkt), 4+1+1+1+320; got != want {
		t.Errorf("a speech packet is %d bytes, want %d", got, want)
	}
	if got := int(pkt[1])<<8 | int(pkt[2]); got != len(pkt)-4 {
		t.Errorf("the speech packet declares %d field bytes and carries %d",
			got, len(pkt)-4)
	}
}

// TestTheToneStaysInsideTheSampleRange keeps the probe's own signal honest.
//
// A frame that clips is a different experiment: the chip would be encoding
// distortion, and a bad answer would look like a bad reading of the packet
// format rather than a bad input.
func TestTheToneStaysInsideTheSampleRange(t *testing.T) {
	for _, hz := range []float64{300, 1000, 3000} {
		for i, s := range sine(hz) {
			if s > 8000 || s < -8000 {
				t.Fatalf("sample %d of a %g Hz frame is %d, outside ±8000", i, hz, s)
			}
		}
	}
}
