package audio

import (
	"encoding/base64"
	"math"
	"testing"
)

// tone returns n samples of a sine at hz, sampled at rate.
func tone(hz, rate float64, n int, amplitude float64) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(amplitude * math.Sin(2*math.Pi*hz*float64(i)/rate))
	}
	return out
}

// energyAt measures how much of a signal sits at one frequency, by the
// Goertzel algorithm.
//
// **A level check cannot see aliasing.** Folding a 6 kHz tone down to 2 kHz
// preserves the level exactly, so the only way to know whether a filter works
// is to ask where the energy went. This is that measurement, and it is why
// these tests assert frequencies rather than amplitudes.
func energyAt(samples []int16, hz, rate float64) float64 {
	k := 2 * math.Cos(2*math.Pi*hz/rate)
	var s1, s2 float64
	for _, v := range samples {
		s := float64(v) + k*s1 - s2
		s2, s1 = s1, s
	}
	return math.Sqrt(s1*s1+s2*s2-k*s1*s2) / float64(len(samples))
}

// TestTheVoiceBandSurvivesUpsampling.
//
// 300 Hz to 3 kHz has to come through at close to its original level, because
// that is the whole of speech. The filter's cutoff is 3.4 kHz, so the top of
// the range is near the corner and allowed to lose a little.
func TestTheVoiceBandSurvivesUpsampling(t *testing.T) {
	for _, hz := range []float64{300, 700, 1500, 3000} {
		in := tone(hz, 8000, 800, 8000)
		out := NewUpsampler().Up(in)

		if len(out) != len(in)*2 {
			t.Fatalf("%g Hz: %d samples in gave %d out, want double", hz, len(in), len(out))
		}
		// Skip the filter's fill-up, which is half its length.
		before := energyAt(in[100:], hz, 8000)
		after := energyAt(out[200:], hz, 16000)
		if ratio := after / before; ratio < 0.8 || ratio > 1.25 {
			t.Errorf("%g Hz came through at %.2f of its level; the voice band must "+
				"survive", hz, ratio)
		}
	}
}

// TestUpsamplingLeavesNoImageAboveTheBand.
//
// Zero-stuffing an 8 kHz stream to 16 kHz creates a mirror of the signal above
// 4 kHz. Left there, the Opus encoder spends bits describing it and the result
// is worse audio at the same bitrate. The low-pass is what removes it, and
// this measures whether it did.
func TestUpsamplingLeavesNoImageAboveTheBand(t *testing.T) {
	const hz = 1000
	in := tone(hz, 8000, 800, 8000)
	out := NewUpsampler().Up(in)

	wanted := energyAt(out[200:], hz, 16000)
	// The image of a 1 kHz tone in an 8 kHz stream sits at 8000-1000 = 7 kHz.
	image := energyAt(out[200:], 8000-hz, 16000)

	if image > wanted/20 {
		t.Errorf("the image at %g Hz is %.1f against %.1f wanted at %g Hz — "+
			"more than 5%%, so the interpolation filter is not working",
			8000.0-hz, image, wanted, float64(hz))
	}
}

// TestDownsamplingDoesNotFoldTheTopBandIntoSpeech is the measurement that
// justifies the filter existing at all.
//
// A 6 kHz component in a 16 kHz stream, decimated by dropping every other
// sample, arrives at |6000 - 8000| = 2 kHz — the middle of speech, and
// indistinguishable from signal once it is there. **Filtering first is what
// prevents it, and nothing downstream can.**
func TestDownsamplingDoesNotFoldTheTopBandIntoSpeech(t *testing.T) {
	const offending = 6000.0
	in := tone(offending, 16000, 1600, 8000)
	out := NewDownsampler().Down(in)

	if len(out) != len(in)/2 {
		t.Fatalf("%d samples in gave %d out, want half", len(in), len(out))
	}

	alias := energyAt(out[100:], math.Abs(offending-8000), 8000)
	// A voice-band reference at the same amplitude, for scale.
	ref := NewDownsampler().Down(tone(1000, 16000, 1600, 8000))
	refEnergy := energyAt(ref[100:], 1000, 8000)

	if alias > refEnergy/20 {
		t.Errorf("a %g Hz tone appeared at %g Hz with energy %.1f, against %.1f "+
			"for a voice-band tone of the same amplitude — that is aliasing, and "+
			"once it is in the voice band nothing can remove it",
			offending, math.Abs(offending-8000), alias, refEnergy)
	}
}

// TestARoundTripThroughBothRatesKeepsTheVoiceBand.
//
// 8 kHz up to 16 and back down, which is what happens to audio that reaches
// Zello and returns. Two filters in series, so a little more loss is expected
// than for one.
func TestARoundTripThroughBothRatesKeepsTheVoiceBand(t *testing.T) {
	for _, hz := range []float64{300, 1000, 2400} {
		in := tone(hz, 8000, 1600, 8000)
		back := NewDownsampler().Down(NewUpsampler().Up(in))

		if len(back) != len(in) {
			t.Fatalf("%g Hz: %d samples became %d", hz, len(in), len(back))
		}
		before := energyAt(in[200:], hz, 8000)
		after := energyAt(back[200:], hz, 8000)
		if ratio := after / before; ratio < 0.7 || ratio > 1.3 {
			t.Errorf("%g Hz survived a round trip at %.2f of its level", hz, ratio)
		}
	}
}

// TestTheFilterHasUnitGainAtDcAndLinearPhase.
//
// Unit gain, so resampling changes the band and not the level — a filter that
// quietly attenuates everything is a volume change nobody asked for. And an
// odd tap count with symmetric coefficients, which is what makes the delay a
// delay rather than a frequency-dependent smear.
func TestTheFilterHasUnitGainAtDcAndLinearPhase(t *testing.T) {
	if len(lowPass) != filterTaps {
		t.Fatalf("the filter has %d taps, want %d", len(lowPass), filterTaps)
	}
	if filterTaps%2 == 0 {
		t.Fatalf("the filter has %d taps; an even count has no exact centre and "+
			"therefore no linear phase", filterTaps)
	}

	var sum float64
	for _, t := range lowPass {
		sum += t
	}
	if math.Abs(sum-1) > 1e-9 {
		t.Errorf("the coefficients sum to %.12f, want 1 for unit gain at DC", sum)
	}

	mid := (filterTaps - 1) / 2
	for i := 1; i <= mid; i++ {
		if math.Abs(lowPass[mid-i]-lowPass[mid+i]) > 1e-12 {
			t.Errorf("taps %d and %d differ, so the filter is not symmetric",
				mid-i, mid+i)
		}
	}
	// And the cutoff is inside the 8 kHz side's Nyquist limit, or the filter
	// cannot prevent aliasing however good it is.
	if filterCutoff >= 4000 {
		t.Errorf("the cutoff is %g Hz and the 8 kHz side's limit is 4000",
			filterCutoff)
	}
}

// TestFilterStateIsKeptBetweenFrames.
//
// A filter restarted every frame produces a transient at every frame boundary
// — fifty times a second, which is heard as a buzz at the frame rate rather
// than as distortion. So one frame at a time must give the same answer as the
// whole run at once.
func TestFilterStateIsKeptBetweenFrames(t *testing.T) {
	whole := tone(1000, 8000, SamplesPerFrame*5, 8000)

	atOnce := NewUpsampler().Up(whole)

	u := NewUpsampler()
	var byFrame []int16
	for i := 0; i < len(whole); i += SamplesPerFrame {
		byFrame = append(byFrame, u.Up(whole[i:i+SamplesPerFrame])...)
	}

	if len(atOnce) != len(byFrame) {
		t.Fatalf("whole run gave %d samples and frame by frame gave %d",
			len(atOnce), len(byFrame))
	}
	for i := range atOnce {
		if atOnce[i] != byFrame[i] {
			t.Fatalf("sample %d differs between one call and five: %d against %d — "+
				"the filter is not keeping its state", i, atOnce[i], byFrame[i])
		}
	}

	// And a reset makes it forget, so one caller's tail does not open the next
	// caller's audio.
	u.Reset()
	after := u.Up(make([]int16, SamplesPerFrame))
	for i, v := range after {
		if v != 0 {
			t.Fatalf("sample %d after a reset and silence is %d, want 0", i, v)
		}
	}
}

// TestLoudAudioSaturatesRatherThanWraps.
//
// The interpolator has a gain of two before the filter, so an overshoot on
// loud audio is entirely possible. **Wrapping is the loud failure**: a value
// one above full scale becomes full scale negative, which is a click at
// maximum amplitude in somebody's ear.
func TestLoudAudioSaturatesRatherThanWraps(t *testing.T) {
	if got := clamp(40000); got != math.MaxInt16 {
		t.Errorf("40000 clamped to %d, want %d", got, math.MaxInt16)
	}
	if got := clamp(-40000); got != math.MinInt16 {
		t.Errorf("-40000 clamped to %d, want %d", got, math.MinInt16)
	}

	// Full-scale audio through the interpolator must not change sign
	// abruptly, which is what a wrap looks like.
	full := tone(1000, 8000, 400, 32767)
	out := NewUpsampler().Up(full)
	for i := 1; i < len(out); i++ {
		if (out[i-1] > 30000 && out[i] < -30000) || (out[i-1] < -30000 && out[i] > 30000) {
			t.Fatalf("samples %d and %d jump from %d to %d, which is a wrap",
				i-1, i, out[i-1], out[i])
		}
	}
}

// TestZellosCodecHeaderIsWhatTheRecordSays.
//
// ADR-0062 records Zello's parameters as 16 kHz mono with 60 ms frames, citing
// `gD4BPA==` from their own example. **Decoding it is a better check than
// trusting the sentence**, and it is four bytes: sample rate 16000
// little-endian, one frame per packet, 0x3c = 60 milliseconds.
func TestZellosCodecHeaderIsWhatTheRecordSays(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString("gD4BPA==")
	if err != nil {
		t.Fatalf("decoding the codec header: %v", err)
	}
	if len(raw) != 4 {
		t.Fatalf("the codec header is %d bytes, want 4", len(raw))
	}

	rate := int(raw[0]) | int(raw[1])<<8
	if rate != ZelloSampleRate {
		t.Errorf("the header declares %d Hz and this package uses %d",
			rate, ZelloSampleRate)
	}
	if framesPerPacket := int(raw[2]); framesPerPacket != 1 {
		t.Errorf("the header declares %d frames per packet, want 1", framesPerPacket)
	}
	if ms := int(raw[3]); ms != ZelloFrameMS {
		t.Errorf("the header declares %d ms frames and this package uses %d",
			ms, ZelloFrameMS)
	}

	// And the constants agree with each other.
	if ZelloSamplesPerPacket != ZelloSampleRate*ZelloFrameMS/1000 {
		t.Errorf("a packet is %d samples, and %d Hz for %d ms is %d",
			ZelloSamplesPerPacket, ZelloSampleRate, ZelloFrameMS,
			ZelloSampleRate*ZelloFrameMS/1000)
	}
	// Three radio frames at 8 kHz upsampled must fill one exactly.
	if got := SamplesPerFrame * FramesPerPacket * 2; got != ZelloSamplesPerPacket {
		t.Errorf("%d radio frames upsampled is %d samples and a Zello packet is %d",
			FramesPerPacket, got, ZelloSamplesPerPacket)
	}
}
