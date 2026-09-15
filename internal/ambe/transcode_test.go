package ambe

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// realFrames returns the frames the operator's dongle accepted on the bench.
//
// **Real DMR audio, not a generated signal.** A synthetic signal cannot test a
// model fitted to human speech, which PROJECT_MEMORY §8r records as costing
// three bench runs: one frame proved a cold encoder, fifty proved the tone
// path, a cleared bit proved the voice path, and only real voice could answer
// what the chip does with a voice.
func realFrames(t *testing.T) [][]byte {
	t.Helper()
	fx := records(t, "observed-exchanges.hex")
	frame, ok := ChannelFrameFromResponse(fx["channel-reply"])
	if !ok {
		t.Fatal("the observed channel reply was not decoded")
	}
	out := make([][]byte, 0, 4)
	for i := 0; i < 4; i++ {
		out = append(out, frame.Data)
	}
	return out
}

// TestARunDecodesEveryFrameAndCountsWhatTheDecoderSaid is the transform doing
// its job.
//
// The counts are not decoration. A run that is mostly comfort noise means the
// frames are being rejected; a run of tone frames means the encoder described
// a tone rather than coding speech. Those need different answers, and neither
// is visible in the samples.
func TestARunDecodesEveryFrameAndCountsWhatTheDecoderSaid(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	if err := c.Acquire(Holder{Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	frames := realFrames(t)
	// The fake answers a channel packet only when told to, so give it a
	// speech reply carrying the tone and the decoder's verdict.
	speech, err := SpeechD(tone())
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	chand, err := Chand(72, frames[0])
	if err != nil {
		t.Fatalf("building CHAND: %v", err)
	}
	request, err := Build(TypeChannel, Val(0x40), chand)
	if err != nil {
		t.Fatalf("building the channel packet: %v", err)
	}
	reply, err := Build(TypeSpeech, speech, Val(0x02, 0x00, 0x02))
	if err != nil {
		t.Fatalf("building the speech reply: %v", err)
	}
	f.answerWith(hex.EncodeToString(request), reply)

	got, err := c.TranscodeRun(frames, false)
	if err != nil {
		t.Fatalf("transcoding a run: %v", err)
	}
	if want := len(frames) * SamplesPerFrame; len(got.Samples) != want {
		t.Errorf("%d frames decoded to %d samples, want %d",
			len(frames), len(got.Samples), want)
	}
	if len(got.Frames) != 0 {
		t.Errorf("a decode-only run returned %d re-encoded frame(s)", len(got.Frames))
	}
	// VOICE_ACTIVE set, DATA_INVALID clear, TONE_FRAME clear — which is what
	// the operator's dongle reported for all 828 real frames.
	if got.ComfortNoise != 0 || got.Invalid != 0 || got.Tones != 0 {
		t.Errorf("a run of valid voice frames counted comfort=%d invalid=%d tones=%d",
			got.ComfortNoise, got.Invalid, got.Tones)
	}
	if got.Peak() == 0 {
		t.Error("the decoded audio has a peak of zero")
	}
}

// TestARoundTripReEncodesEveryDecodedFrame is the path a radio can hear.
//
// Frames in, PCM, frames back. It is the only way to hear the result on air
// before Opus exists, and it exercises exactly the machinery the Zello
// direction will use with the middle replaced.
func TestARoundTripReEncodesEveryDecodedFrame(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	if err := c.Acquire(Holder{Reason: "loopback"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	frames := realFrames(t)
	speech, err := SpeechD(tone())
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	chand, err := Chand(72, frames[0])
	if err != nil {
		t.Fatalf("building CHAND: %v", err)
	}
	request, err := Build(TypeChannel, Val(0x40), chand)
	if err != nil {
		t.Fatalf("building the channel packet: %v", err)
	}
	reply, err := Build(TypeSpeech, speech)
	if err != nil {
		t.Fatalf("building the speech reply: %v", err)
	}
	f.answerWith(hex.EncodeToString(request), reply)

	got, err := c.TranscodeRun(frames, true)
	if err != nil {
		t.Fatalf("transcoding a run: %v", err)
	}
	if len(got.Frames) != len(frames) {
		t.Fatalf("%d frames in produced %d frames out", len(frames), len(got.Frames))
	}
	for i, frame := range got.Frames {
		if frame.Bits != 72 {
			t.Errorf("re-encoded frame %d carries %d bits, want 72", i+1, frame.Bits)
		}
		if frame.Rate() != 3600 {
			t.Errorf("re-encoded frame %d is %d bps, want 3600", i+1, frame.Rate())
		}
	}
}

// TestARunWithoutTheChannelPutsNothingOnTheWire, because a run is many frames
// and the chance of interleaving with somebody else's call is proportionally
// larger than for one.
//
// **The error alone does not prove anything here**, and breaking the code
// showed it: TranscodeRun's own guard is redundant, because Decode refuses
// too, so removing it leaves the same error coming back wrapped and the
// assertion passing. What is not redundant is that no packet reaches the
// vocoder — a frame on the wire from a caller that does not hold the channel
// is audio that may interleave with the call that does. So that is what is
// checked.
//
// The guard stays in TranscodeRun despite being redundant: it states the
// precondition at the boundary a caller reads, rather than leaving it to be
// inferred from a function it happens to call.
func TestARunWithoutTheChannelPutsNothingOnTheWire(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	before := f.requests()

	if _, err := c.TranscodeRun(realFrames(t), false); !errors.Is(err, ErrNotHeld) {
		t.Errorf("a run without the channel gave %v, want a not-held error", err)
	}
	if got := f.requests() - before; got != 0 {
		t.Errorf("a run without the channel sent %d packet(s) to the vocoder; "+
			"that audio may interleave with whoever does hold the channel", got)
	}
}

// TestAFrameOfTheWrongWidthForTheRateIsRefused is the check that would have
// caught a run at the wrong rate.
//
// **An acknowledgement says a field arrived; a frame's width says what rate is
// in effect.** Fifty frames once went out at 2400 bps because a code path
// skipped the rate packet, and the only evidence was the width of the frames.
// A caller handing over frames of another width has frames from somewhere
// else, and the refusal says both numbers.
func TestAFrameOfTheWrongWidthForTheRateIsRefused(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	if err := c.Acquire(Holder{Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	// Six bytes is 48 bits, which is rate index 0 — the board's boot rate.
	_, err := c.TranscodeRun([][]byte{make([]byte, 6)}, false)
	if err == nil {
		t.Fatal("a 48-bit frame was accepted at rate index 33")
	}
	for _, want := range []string{"6 byte", "33", "72"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	if got := c.Rate(); got != RateIndexDMR {
		t.Errorf("the client reports rate index %d, want %d", got, RateIndexDMR)
	}
}

// TestAFailureNamesWhichFrameAndKeepsWhatItHad.
//
// A run of eight hundred frames that fails on frame six hundred has decoded
// twelve seconds of audio, and throwing that away along with the position
// would leave an operator with a failure and no idea where in a transmission
// it happened.
func TestAFailureNamesWhichFrameAndKeepsWhatItHad(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	if err := c.Acquire(Holder{Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	frames := realFrames(t)
	chand, err := Chand(72, frames[0])
	if err != nil {
		t.Fatalf("building CHAND: %v", err)
	}
	request, err := Build(TypeChannel, Val(0x40), chand)
	if err != nil {
		t.Fatalf("building the channel packet: %v", err)
	}
	// Silence: the chip did not accept the packet.
	f.dropRequest(hex.EncodeToString(request))

	got, err := c.TranscodeRun(frames, false)
	if err == nil {
		t.Fatal("a run against a silent chip succeeded")
	}
	if !strings.Contains(err.Error(), "frame 1 of 4") {
		t.Errorf("the failure does not say where in the run it happened: %v", err)
	}
	if len(got.Samples) != 0 {
		t.Errorf("the run returned %d samples from a frame that failed", len(got.Samples))
	}
}
