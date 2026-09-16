//go:build zello

package opus

import (
	"math"
	"strings"
	"testing"
)

// libopus, measured the same way the pure-Go encoder was condemned.
//
// ADR-0062's revisit rejected a pure-Go encoder because libopus decoded its
// output at peak 32761 against an input of 6149. **The same measurement has to
// be applied here**, or this package is trusted for no better reason than
// being written in C.

// speech returns n samples of a speech-shaped signal at 16 kHz: a pitch buzz
// and two formants, so the codec's model has something to fit.
//
// A pure tone is the wrong test signal for a speech coder, which
// PROJECT_MEMORY §8r records as costing three bench runs on the AMBE side.
func speech(n, base int) []int16 {
	out := make([]int16, n)
	for i := range out {
		t := float64(base+i) / SampleRate
		v := 3000*math.Sin(2*math.Pi*140*t) +
			2000*math.Sin(2*math.Pi*700*t) +
			1200*math.Sin(2*math.Pi*1220*t)
		out[i] = int16(v)
	}
	return out
}

// meanLevel is the mean absolute sample, which is what the pure-Go encoder
// failed on.
func meanLevel(s []int16) int {
	if len(s) == 0 {
		return 0
	}
	var sum int
	for _, v := range s {
		if v < 0 {
			v = -v
		}
		sum += int(v)
	}
	return sum / len(s)
}

// peakLevel is the largest absolute sample.
func peakLevel(s []int16) int {
	peak := 0
	for _, v := range s {
		n := int(v)
		if n < 0 {
			n = -n
		}
		if n > peak {
			peak = n
		}
	}
	return peak
}

// TestAudioSurvivesARoundTripAtTheRightLevel is the measurement that decides
// whether this codec works.
//
// The pure-Go encoder that was rejected turned an input of mean 2240 into
// output of mean 23132 — ten times too loud, which is full-scale noise. So the
// test is not "does it encode" but "does the audio come back at the level it
// went in".
func TestAudioSurvivesARoundTripAtTheRightLevel(t *testing.T) {
	enc, err := NewEncoder(16000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	defer enc.Close()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	defer dec.Close()

	const frames = 20 // 1.2 seconds
	var inLevel, outLevel, bytes int
	var lastOut []int16

	for i := 0; i < frames; i++ {
		in := speech(SamplesPerFrame, i*SamplesPerFrame)
		packet, err := enc.Encode(in)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		bytes += len(packet)

		out, err := dec.Decode(packet)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(out) != SamplesPerFrame {
			t.Fatalf("frame %d decoded to %d samples, want %d",
				i, len(out), SamplesPerFrame)
		}
		// Skip the codec's warm-up, as on the AMBE side.
		if i >= 3 {
			inLevel += meanLevel(in)
			outLevel += meanLevel(out)
		}
		lastOut = out
	}

	in, out := inLevel/(frames-3), outLevel/(frames-3)
	if in == 0 {
		t.Fatal("the test signal is silent")
	}
	ratio := float64(out) / float64(in)
	if ratio < 0.5 || ratio > 2.0 {
		t.Errorf("audio came back at %.2f of its level (mean %d in, %d out); the "+
			"pure-Go encoder this replaced managed 10.3", ratio, in, out)
	}
	// And it must not be pinned at full scale, which is what the rejected
	// encoder produced.
	if peak := peakLevel(lastOut); peak > 30000 {
		t.Errorf("the last frame peaks at %d, which is full-scale noise rather "+
			"than speech", peak)
	}

	// The bitrate must be near what was asked for, not the default. Left
	// unset, the pure-Go encoder chose 69 kbit/s for 8 kHz mono.
	bps := float64(bytes*8) / (float64(frames) * FrameMS / 1000)
	if bps < 8000 || bps > 30000 {
		t.Errorf("the stream is %.0f bits/s and 16000 was requested", bps)
	}
}

// TestALostPacketIsConcealedRatherThanFailing.
//
// On a voice channel a concealed 60 ms is far less noticeable than a hole, and
// a hole is what a caller gets if it treats a loss as a failure and sends
// nothing. So a nil packet must return a frame.
func TestALostPacketIsConcealedRatherThanFailing(t *testing.T) {
	enc, err := NewEncoder(16000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	defer enc.Close()
	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	defer dec.Close()

	// Give the decoder some history first, or concealment has nothing to work
	// from.
	for i := 0; i < 5; i++ {
		packet, err := enc.Encode(speech(SamplesPerFrame, i*SamplesPerFrame))
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if _, err := dec.Decode(packet); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}

	concealed, err := dec.Decode(nil)
	if err != nil {
		t.Fatalf("a lost packet returned an error rather than concealment: %v", err)
	}
	if len(concealed) != SamplesPerFrame {
		t.Errorf("concealment produced %d samples, want a whole frame of %d",
			len(concealed), SamplesPerFrame)
	}
	// Concealment continues the signal rather than inserting silence, so it
	// should not be flat zero.
	if meanLevel(concealed) == 0 {
		t.Error("concealment produced silence; a hole is what it exists to avoid")
	}
}

// TestAFrameOfTheWrongLengthIsRefused.
//
// Zello's packet length is fixed. A frame of another size would encode to a
// packet declaring a different duration, and the far side would play it at the
// wrong speed rather than reject it.
func TestAFrameOfTheWrongLengthIsRefused(t *testing.T) {
	enc, err := NewEncoder(16000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	defer enc.Close()

	for _, n := range []int{0, 1, 160, 320, SamplesPerFrame - 1, SamplesPerFrame + 1} {
		if _, err := enc.Encode(make([]int16, n)); err == nil {
			t.Errorf("a frame of %d samples was encoded; a Zello frame is %d",
				n, SamplesPerFrame)
		}
	}
}

// TestAClosedCodecIsRefusedRatherThanCrashing.
//
// Encoding through a destroyed C pointer is a segmentation fault, which takes
// the whole companion down mid-transmission and leaves nothing in a log.
func TestAClosedCodecIsRefusedRatherThanCrashing(t *testing.T) {
	enc, err := NewEncoder(16000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	enc.Close()
	enc.Close() // twice, because a double close must not crash either

	if _, err := enc.Encode(speech(SamplesPerFrame, 0)); err == nil {
		t.Error("a closed encoder accepted a frame")
	} else if !strings.Contains(err.Error(), "closed") {
		t.Errorf("the refusal does not say the encoder is closed: %v", err)
	}

	dec, err := NewDecoder()
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	dec.Close()
	dec.Close()
	if _, err := dec.Decode([]byte{0x01}); err == nil {
		t.Error("a closed decoder accepted a packet")
	}
}

// TestTheParametersAreZellosOwn.
//
// From ADR-0062: the Channels API requires `opus` and fixes 16 kHz mono with
// 60 ms frames, which `gD4BPA==` decodes to. Any drift here produces audio the
// far side plays at the wrong rate.
func TestTheParametersAreZellosOwn(t *testing.T) {
	if SampleRate != 16000 {
		t.Errorf("the rate is %d, and Zello's codec header declares 16000", SampleRate)
	}
	if FrameMS != 60 {
		t.Errorf("the frame is %d ms, and Zello's codec header declares 60", FrameMS)
	}
	if Channels != 1 {
		t.Errorf("the codec is %d channels, and Zello is mono", Channels)
	}
	if SamplesPerFrame != SampleRate*FrameMS/1000 {
		t.Errorf("a frame is %d samples, and %d Hz for %d ms is %d",
			SamplesPerFrame, SampleRate, FrameMS, SampleRate*FrameMS/1000)
	}
	// Opus's own maximum for a single frame, so a loud frame is never
	// truncated.
	if maxPacket != 1275 {
		t.Errorf("the packet buffer is %d bytes and Opus's limit is 1275", maxPacket)
	}
}

// TestTheLibraryVersionIsReportable, because "which libopus" is the first
// question about any codec problem and it is a library the operator installed
// rather than one QSP shipped.
func TestTheLibraryVersionIsReportable(t *testing.T) {
	v := Version()
	if v == "" {
		t.Fatal("the libopus version is empty")
	}
	if !strings.Contains(strings.ToLower(v), "opus") {
		t.Errorf("the version string is %q, which does not mention opus", v)
	}
	t.Logf("libopus: %s", v)
}

// TestTheBitrateIsReadBackRatherThanAssumed.
//
// A control that returns OPUS_OK has been accepted, not necessarily applied as
// asked: libopus clamps a bitrate to what the mode can carry. **A silently
// clamped rate is a stream costing more or sounding worse than the
// configuration says**, and the only way to know is to ask.
func TestTheBitrateIsReadBackRatherThanAssumed(t *testing.T) {
	for _, asked := range []int{8000, 16000, 24000} {
		enc, err := NewEncoder(asked)
		if err != nil {
			t.Errorf("%d bits/s: %v", asked, err)
			continue
		}
		got := enc.Bitrate()
		enc.Close()

		if got == 0 {
			t.Errorf("%d bits/s was requested and the encoder reports 0; the "+
				"read-back is not happening", asked)
			continue
		}
		if got != asked {
			t.Logf("%d bits/s was requested and libopus applied %d", asked, got)
		}
	}

	// An absurd rate must be reported as whatever libopus clamped it to,
	// rather than echoed back as if it had been honoured.
	enc, err := NewEncoder(1)
	if err != nil {
		t.Fatalf("1 bit/s: %v", err)
	}
	defer enc.Close()
	if got := enc.Bitrate(); got == 1 {
		t.Error("the encoder reports 1 bit/s, which libopus cannot have applied " +
			"— the value is being echoed rather than read back")
	}
}

// TestSixtyMillisecondFramesForceTheSpeechModel corrects a claim this package
// made and could not support.
//
// `NewEncoder` passes `OPUS_APPLICATION_VOIP`, and an earlier version of this
// file asserted that the hint is what gets the speech model — VoIP biasing
// toward SILK and audio toward CELT. **Changing the hint to audio passed every
// test, including one written specifically to catch it.**
//
// The reason is better than the claim. RFC 6716 §2 gives CELT frame sizes of
// 2.5 to 20 ms and SILK 10 to 60 ms, so **a 60 ms frame can only be SILK** and
// the application hint cannot override arithmetic. Zello's fixed packet length
// forces the speech model whatever anybody asks for.
//
// Two things follow. The VoIP hint stays because it is right and free, not
// because it is load-bearing. And ADR-0062's rejection of the CELT-only
// pure-Go encoders is sharper than recorded: a CELT-only encoder cannot
// produce a 60 ms frame at all, so it could never serve Zello whatever its
// quality.
//
// RFC 6716 §3.1 puts the mode in the first byte: the top five bits are the
// configuration, 0 to 11 SILK, 12 to 15 hybrid, 16 to 31 CELT.
func TestSixtyMillisecondFramesForceTheSpeechModel(t *testing.T) {
	// Both hints, because the point is that they agree.
	for _, hint := range []int{voipHint, audioHint} {
		enc, err := newEncoderWithHint(16000, hint)
		if err != nil {
			t.Fatalf("encoder: %v", err)
		}

		var seen []int
		for i := 0; i < 8; i++ {
			packet, err := enc.Encode(speech(SamplesPerFrame, i*SamplesPerFrame))
			if err != nil {
				enc.Close()
				t.Fatalf("frame %d: %v", i, err)
			}
			config := int(packet[0] >> 3)
			seen = append(seen, config)
			if config > 11 {
				t.Errorf("hint %d produced configuration %d, which is not SILK; "+
					"a 60 ms frame has no other option (all: %v)", hint, config, seen)
			}
		}
		enc.Close()
	}

	// And the arithmetic the claim rests on: 60 ms is beyond CELT's longest
	// frame.
	const celtLongestMS = 20
	if FrameMS <= celtLongestMS {
		t.Errorf("a frame is %d ms and CELT reaches %d, so the frame size does "+
			"not force SILK and this test proves nothing", FrameMS, celtLongestMS)
	}
}

// twoFramePacket rewrites a single-frame Opus packet (TOC code 0) as a legal
// two-frame packet (code 1: two frames of equal size, RFC 6716 §3.2.2) carrying
// the same frame twice — twice the audio in one packet, which is what the
// Zello app sent on the first real connection.
func twoFramePacket(t *testing.T, single []byte) []byte {
	t.Helper()
	if single[0]&3 != 0 {
		t.Fatalf("the encoder's packet has TOC code %d, not 0; this helper cannot double it", single[0]&3)
	}
	out := []byte{single[0]&^3 | 1}
	out = append(out, single[1:]...)
	return append(out, single[1:]...)
}

// TestADecoderTakesTheLongestLegalPacket.
//
// To see it bite: size the decoder's buffer with SamplesPerFrame again, and
// the 120 ms row fails with "buffer too small" — the error the Zello app's
// packets produced on 2026-09-16.
func TestADecoderTakesTheLongestLegalPacket(t *testing.T) {
	enc, err := NewEncoder(16000)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	pcm := make([]int16, SamplesPerFrame)
	for i := range pcm {
		pcm[i] = int16((i%40)*400 - 8000)
	}
	one, err := enc.Encode(pcm)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name        string
		packet      []byte
		wantSamples int
	}{
		{"one 60 ms frame, as QSP sends", one, SamplesPerFrame},
		{"two 60 ms frames, 120 ms, the longest Opus allows", twoFramePacket(t, one), MaxSamplesPerPacket},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dec, err := NewDecoder()
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			got, err := dec.Decode(tc.packet)
			if err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if len(got) != tc.wantSamples {
				t.Errorf("decoded %d samples, want %d", len(got), tc.wantSamples)
			}
		})
	}
}
