package main

import (
	"encoding/hex"
	"testing"
)

// TestTheControlPacketMatchesTheOneTheDongleAnswered is the exchange that
// actually happened, kept as the definition.
//
// On 2026-09-14 an AMBE3000F on the operator's bench answered
// `61 00 01 00 30` with `61 00 0b 00 30 41 4d 42 45 33 30 30 30 46 00`. The
// first draft of this program emitted `61 00 01 00 33` as `61 00 02 00 33`,
// because the length was written to include the type byte. It does not.
//
// **A dongle refusing a malformed packet looks exactly like a dead dongle**, so
// this is the difference between an hour and a week.
func TestTheControlPacketMatchesTheOneTheDongleAnswered(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  []byte
		want string
	}{
		{"reset", control(fieldReset), "6100010033"},
		{"product id", control(fieldProdID), "6100010030"},
		{"version", control(fieldVersion), "6100010031"},
	} {
		if got := hex.EncodeToString(tc.got); got != tc.want {
			t.Errorf("%s is %s, want %s; the dongle answered the second one on "+
				"2026-09-14 and does not answer the first", tc.name, got, tc.want)
		}
	}
}

// TestTheLengthCountsThePayloadNotTheType states the rule directly, so that a
// change to the framing fails here rather than on a bench.
//
// The observed reply is the other half of the evidence: `00 0b` for a field
// byte, nine characters of "AMBE3000F" and a terminator — eleven, with the
// type byte outside the count.
func TestTheLengthCountsThePayloadNotTheType(t *testing.T) {
	// Three payload bytes, so the length is 0003 and the type sits outside it.
	const want = "6100030030" + "4142"
	if got := hex.EncodeToString(packet(typeControl, []byte{0x30, 0x41, 0x42})); got != want {
		t.Errorf("packet framing is %s, want %s: the length counts the payload "+
			"and the type byte is not part of it", got, want)
	}
}

// TestASpeechFrameIsTwentyMillisecondsAtEightKilohertz keeps the frame size
// honest.
//
// 160 samples is what the part takes for one compressed frame. A frame of any
// other length is a different question being asked, and the answer would not
// mean what it appears to.
func TestASpeechFrameIsTwentyMillisecondsAtEightKilohertz(t *testing.T) {
	s := sine(1000)
	if len(s) != 160 {
		t.Fatalf("a frame is %d samples, want 160", len(s))
	}
	// Header, type, field, count, then two bytes per sample.
	if got, want := len(speech(s)), 4+2+320; got != want {
		t.Errorf("a speech packet is %d bytes, want %d", got, want)
	}
}
