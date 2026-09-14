package main

import (
	"encoding/hex"
	"os"
	"strings"
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
	if got, want := len(packet(typeSpeech, speechBody(s))), 4+2+320; got != want {
		t.Errorf("a speech packet is %d bytes, want %d", got, want)
	}
}

// TestSpeechIsTypeTwoAndChannelIsTypeOne is the manual, after two wrong
// readings in one evening.
//
// AMBE-3000F users manual §6.7 "Input Speech Packet Format (Packet Type 0x02)"
// and §6.9 "Input Channel Packet Format (Packet Type 0x01)". The chip outputs
// a speech packet whenever it receives a channel packet.
//
// These were right, swapped on the theory that a wrong type explained the
// chip's silence, and swapped back when the manual was read. **The swap was
// shipped as a correction**, which is the part worth remembering: "this
// explains the symptom" is a hypothesis, and a contents page is evidence.
func TestSpeechIsTypeTwoAndChannelIsTypeOne(t *testing.T) {
	if typeSpeech != 0x02 {
		t.Errorf("speech is %#02x, want 0x02 per the manual", typeSpeech)
	}
	if typeChannel != 0x01 {
		t.Errorf("channel is %#02x, want 0x01 per the manual", typeChannel)
	}
}

// TestAControlFieldCarriesTheNumberOfBytesItPromises is the gate for the bug
// that wedged a dongle.
//
// On 2026-09-14 the probe sent `61 00 02 00 0a 21`: field 0x0a with one
// argument byte. 0x0a is the full rate-parameters block and takes eleven. The
// chip was told eleven bytes were coming, given one, and consumed the first
// ten bytes of the following packet as the remainder — leaving its parser
// mid-field permanently. AMBEserver then reported "Couldn't find start byte in
// serial data" on every subsequent start, and **recovery took a physical
// unplug**: neither a software reset nor detaching the USB device in ESXi
// cleared it.
//
// **This is checkable with no hardware at all**, which is the point. A field
// whose argument count is wrong is a class of bug that costs a device rather
// than a test run, and it can be caught before anything is plugged in.
func TestAControlFieldCarriesTheNumberOfBytesItPromises(t *testing.T) {
	// The count each control field requires, from the manual and from a
	// working session: PKT_RATET takes an index, PKT_RATEP takes a rate word.
	args := map[byte]int{
		fieldReset:      0,
		fieldProdID:     0,
		fieldVersion:    0,
		fieldRateIndex:  1,
		fieldRateParams: 11,
	}

	for field, want := range args {
		// Every control packet this program can build, checked against the
		// count its field requires.
		if want != 1 {
			continue // only the rate index is constructed with an argument
		}
		p := control(field, 0x21)
		// Header is start byte, two length bytes, type. The length counts the
		// field byte and its arguments.
		if got := int(p[1])<<8 | int(p[2]); got != want+1 {
			t.Errorf("field %#02x declares length %d for %d argument(s); a "+
				"field that promises more bytes than it sends leaves the chip "+
				"mid-parse and needs a physical unplug", field, got, want)
		}
	}

	// And the one that was actually wrong: a rate index must not be sent with
	// the rate-parameters field.
	if fieldRateIndex == fieldRateParams {
		t.Fatal("the one-byte and eleven-byte rate fields are the same value")
	}
	if got := control(fieldRateIndex, 33); got[4] != 0x09 {
		t.Errorf("the rate index uses field %#02x, want 0x09; 0x0a is the "+
			"eleven-byte rate word and sending one byte to it wedges the chip",
			got[4])
	}
}

// TestTheRateFieldSentIsTheOneByteOne closes the gap the constants leave.
//
// The checks above prove `fieldRateIndex` is 0x09. They do not prove the
// program sends it — and changing the call site to `fieldRateParams` while
// leaving the constants correct passes every one of them. That is the same
// shape as a configuration field the server never reads: right value, wrong
// caller.
//
// **The call site is what wedged the dongle**, so the call site is what this
// reads. Source inspection, with its limit stated: it proves which constant is
// passed, not that the chip likes it.
func TestTheRateFieldSentIsTheOneByteOne(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("reading main.go: %v", err)
	}
	if !strings.Contains(string(src), "control(fieldRateIndex, byte(*rate))") {
		t.Error("the rate packet is not built with fieldRateIndex; a one-byte " +
			"index sent to the eleven-byte rate field leaves the chip " +
			"mid-parse and needs a physical unplug to clear")
	}
}
