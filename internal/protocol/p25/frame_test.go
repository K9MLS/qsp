package p25

import (
	"bytes"
	"errors"
	"testing"
)

// **Every frame in a real capture round-trips byte for byte.**
//
// This is the assertion the whole package exists to support: ADR-0034 says a
// P25 call crosses QSP without a vocoder, so a frame that came out different
// from the way it went in would be QSP damaging audio it had no reason to
// touch. 565 frames from seven transmissions, and each must survive.
func TestEveryCapturedFrameSurvivesTheRoundTrip(t *testing.T) {
	frames := gatewayFrames(t)
	if len(frames) < 500 {
		t.Fatalf("the fixture holds %d frames; it held 565 when this was written, and a "+
			"much smaller number means the wrong flow is being read", len(frames))
	}

	for i, p := range frames {
		f, err := Parse(p.Payload)
		if err != nil {
			t.Fatalf("frame %d (%s) from a real capture was refused: %v", i, describe(p), err)
		}
		if got := f.Marshal(); !bytes.Equal(got, p.Payload) {
			t.Fatalf("frame %d (%s) changed in transit:\n in  %x\n out %x",
				i, describe(p), p.Payload, got)
		}
	}
}

// **The payload is copied, not aliased.** A frame handed on while the caller
// reuses its read buffer is a frame that changes underneath whoever relays it,
// and the corruption would look like a radio fault rather than a bug here.
func TestAParsedFrameDoesNotAliasTheReadBuffer(t *testing.T) {
	buffer := append([]byte(nil), gatewayFrames(t)[0].Payload...)
	original := append([]byte(nil), buffer...)

	f, err := Parse(buffer)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// A listener reusing its buffer for the next datagram.
	for i := range buffer {
		buffer[i] = 0xFF
	}
	if got := f.Marshal(); !bytes.Equal(got, original) {
		t.Errorf("the frame changed when the read buffer was reused:\n want %x\n got  %x",
			original, got)
	}
}

// The lengths are exact rather than minimums, because every type appeared at
// precisely one length across the whole capture. A decoder accepting a short
// frame would be inventing the missing bytes.
func TestAKnownTypeAtTheWrongLengthIsRefused(t *testing.T) {
	full := gatewayFrames(t)[0].Payload

	for _, tc := range []struct {
		name  string
		frame []byte
	}{
		{"one byte short", full[:len(full)-1]},
		{"one byte long", append(append([]byte(nil), full...), 0x00)},
		{"the type alone", full[:1]},
		{"nothing at all", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.frame); err == nil {
				t.Error("it was accepted")
			}
		})
	}
}

// **A type the capture never showed is refused rather than relayed.** A gateway
// sending something QSP has never seen is doing something QSP does not
// understand, and forwarding it blind puts unknown bytes on somebody's
// repeater.
func TestAnUnknownTypeIsRefusedRatherThanRelayed(t *testing.T) {
	for _, b := range []byte{0x00, 0x61, 0x74, 0x7F, 0x81, 0xFF} {
		_, err := Parse([]byte{b, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
		if !errors.Is(err, ErrUnknownKind) {
			t.Errorf("type 0x%02x gave %v, want ErrUnknownKind", b, err)
		}
	}
}

// Every frame type the capture contains is one this package knows, and every
// type it knows appeared in the capture. **Both directions matter**: the first
// means QSP can relay a real call, and the second means nothing here was
// invented.
func TestTheTypeTableMatchesTheCaptureExactly(t *testing.T) {
	seen := map[Kind]int{}
	for _, p := range gatewayFrames(t) {
		seen[Kind(p.Payload[0])]++
	}

	for kind := range frameLength {
		if kind == KindPoll {
			// Polls travel on the outbound path; asserted separately.
			continue
		}
		if seen[kind] == 0 {
			t.Errorf("this package knows type 0x%02x and the capture never showed it, so it "+
				"was invented rather than observed", byte(kind))
		}
	}
	for kind, n := range seen {
		if _, known := frameLength[kind]; !known {
			t.Errorf("the capture holds type 0x%02x %d times and this package does not know it",
				byte(kind), n)
		}
	}
}

// **Seven transmissions, each opening with 0x62 and closing with 0x80.** This
// is what QSP needs in order to know a call has started and stopped, which is
// the minimum for a call record and a Last-heard entry.
func TestATransmissionStartsAndEndsWhereExpected(t *testing.T) {
	var starts, ends int
	for _, p := range gatewayFrames(t) {
		f, err := Parse(p.Payload)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		switch {
		case f.StartsTransmission():
			starts++
		case f.EndsTransmission():
			ends++
		}
	}
	if ends != 7 {
		t.Errorf("the capture holds %d terminators, want 7 — one per transmission", ends)
	}
	// 0x62 opens each logical data unit, so there are many more starts than
	// transmissions. What matters is that there is at least one per terminator.
	if starts < ends {
		t.Errorf("%d openings for %d terminators", starts, ends)
	}
}

// The terminator carried nothing but zeroes in all seven transmissions. Worth
// asserting because it is the kind of thing a later capture might contradict,
// and a surprise there is a fact about P25 rather than a bug here.
func TestTheTerminatorIsEmptyInThisCapture(t *testing.T) {
	var found int
	for _, p := range gatewayFrames(t) {
		if Kind(p.Payload[0]) != KindTerminator {
			continue
		}
		found++
		for i, b := range p.Payload[1:] {
			if b != 0 {
				t.Errorf("a terminator carries %#02x at offset %d; this capture had only "+
					"zeroes, so a later one carrying data means P25 uses it for something",
					b, i+1)
			}
		}
	}
	if found != 7 {
		t.Fatalf("found %d terminators, want 7", found)
	}
}

// The keepalive is the type byte and a space-padded callsign, in both captures.
func TestAPollCarriesTheCallsignItAnnounces(t *testing.T) {
	var polls int
	for _, p := range readCapture(t, "../../../testdata/p25/p25-voice.pcap") {
		if len(p.Payload) == 0 || Kind(p.Payload[0]) != KindPoll {
			continue
		}
		polls++
		poll, err := ParsePoll(p.Payload)
		if err != nil {
			t.Fatalf("a real poll was refused: %v", err)
		}
		if poll.Callsign != "K9MLS" {
			t.Errorf("the poll announces %q, want K9MLS", poll.Callsign)
		}
		if got := poll.Marshal(); !bytes.Equal(got, p.Payload) {
			t.Errorf("the poll changed in transit:\n in  %x\n out %x", p.Payload, got)
		}
	}
	if polls == 0 {
		t.Fatal("the fixture holds no keepalives, so this test proved nothing")
	}
}
