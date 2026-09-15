package audio

import (
	"testing"
)

// TestThreeFramesMakeOnePacket is the ratio the whole boundary turns on.
func TestThreeFramesMakeOnePacket(t *testing.T) {
	p := NewPacker()

	for i := 1; i <= 2; i++ {
		block, err := p.Add(tone(1000, 8000, SamplesPerFrame, 8000))
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if block != nil {
			t.Fatalf("frame %d produced a packet; %d frames make one",
				i, FramesPerPacket)
		}
		if p.Pending() != i {
			t.Errorf("after frame %d, %d frames are pending", i, p.Pending())
		}
	}

	block, err := p.Add(tone(1000, 8000, SamplesPerFrame, 8000))
	if err != nil {
		t.Fatalf("frame 3: %v", err)
	}
	if block == nil {
		t.Fatal("the third frame produced no packet")
	}
	if len(block) != ZelloSamplesPerPacket {
		t.Errorf("a packet is %d samples, want %d", len(block), ZelloSamplesPerPacket)
	}
	if p.Pending() != 0 {
		t.Errorf("%d frames are pending after a packet", p.Pending())
	}
}

// TestAFrameOfTheWrongLengthIsRefused, because a packer given 320 samples
// would silently produce packets of the wrong duration and the far side would
// hear time-stretched audio rather than an error.
func TestAFrameOfTheWrongLengthIsRefused(t *testing.T) {
	p := NewPacker()
	for _, n := range []int{0, 1, 159, 161, 320} {
		if _, err := p.Add(make([]int16, n)); err == nil {
			t.Errorf("a frame of %d samples was accepted; a radio frame is %d",
				n, SamplesPerFrame)
		}
	}
}

// TestTheLastPartialBlockIsSentRatherThanDropped is the defect an operator
// would hear on every single call.
//
// A transmission is rarely a multiple of three frames. Whatever is left when
// the button is released is a partial block, and the obvious thing — dropping
// it — **clips the last word of every transmission**. Padding with silence
// loses nothing and costs one packet.
func TestTheLastPartialBlockIsSentRatherThanDropped(t *testing.T) {
	for _, frames := range []int{1, 2, 4, 5, 7, 8} {
		p := NewPacker()
		var packets int
		for i := 0; i < frames; i++ {
			block, err := p.Add(tone(1000, 8000, SamplesPerFrame, 8000))
			if err != nil {
				t.Fatalf("%d frames, frame %d: %v", frames, i, err)
			}
			if block != nil {
				packets++
			}
		}

		tail := p.Flush()
		if tail == nil {
			t.Errorf("%d frames left nothing to flush; %d of them are unsent and "+
				"that is the end of somebody's sentence", frames, frames%FramesPerPacket)
			continue
		}
		if len(tail) != ZelloSamplesPerPacket {
			t.Errorf("%d frames flushed %d samples, want a full packet of %d",
				frames, len(tail), ZelloSamplesPerPacket)
		}
		packets++

		// Every frame of audio is accounted for in some packet.
		if want := (frames + FramesPerPacket - 1) / FramesPerPacket; packets != want {
			t.Errorf("%d frames produced %d packets, want %d", frames, packets, want)
		}
		if p.Pending() != 0 {
			t.Errorf("%d frames left %d pending after a flush", frames, p.Pending())
		}
	}

	// An exact multiple has nothing to flush, and must not emit a packet of
	// pure silence.
	p := NewPacker()
	for i := 0; i < FramesPerPacket; i++ {
		if _, err := p.Add(tone(1000, 8000, SamplesPerFrame, 8000)); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if tail := p.Flush(); tail != nil {
		t.Error("an exact multiple of three frames flushed a packet of silence")
	}
}

// TestTheFlushedBlockCarriesTheAudioAndThenSilence.
//
// Padding at the end of a transmission extends it; padding in the middle would
// insert a gap into somebody's sentence. So the real audio must be at the
// front of the block, not spread through it.
func TestTheFlushedBlockCarriesTheAudioAndThenSilence(t *testing.T) {
	p := NewPacker()
	if _, err := p.Add(tone(1000, 8000, SamplesPerFrame, 8000)); err != nil {
		t.Fatalf("adding a frame: %v", err)
	}
	tail := p.Flush()
	if tail == nil {
		t.Fatal("one frame flushed nothing")
	}

	// One 8 kHz frame upsampled is 320 samples of the block's 960.
	const audio = SamplesPerFrame * 2
	var loud bool
	for _, v := range tail[:audio] {
		if v != 0 {
			loud = true
		}
	}
	if !loud {
		t.Error("the front of the flushed block is silent; the audio is missing")
	}
	for i, v := range tail[audio:] {
		if v != 0 {
			t.Errorf("sample %d of the padding is %d, want silence", audio+i, v)
			break
		}
	}
}

// TestResettingDiscardsAnotherCallersAudio.
//
// A reset happens because a new transmission is starting, so a leftover block
// belongs to a call that has ended. Sending it would put one caller's audio at
// the front of another's — the worst kind of mix-up on a shared channel.
func TestResettingDiscardsAnotherCallersAudio(t *testing.T) {
	p := NewPacker()
	if _, err := p.Add(tone(1000, 8000, SamplesPerFrame, 8000)); err != nil {
		t.Fatalf("adding a frame: %v", err)
	}
	p.Reset()

	if p.Pending() != 0 {
		t.Errorf("%d frames survived a reset", p.Pending())
	}
	if tail := p.Flush(); tail != nil {
		t.Error("a flush after a reset produced a packet of the previous call")
	}

	// And the filter forgot too, so the next call opens on silence.
	block, err := p.Add(make([]int16, SamplesPerFrame))
	if err != nil {
		t.Fatalf("adding silence: %v", err)
	}
	if block != nil {
		t.Fatal("one frame after a reset produced a packet")
	}
	for i := 0; i < 2; i++ {
		block, err = p.Add(make([]int16, SamplesPerFrame))
		if err != nil {
			t.Fatalf("adding silence: %v", err)
		}
	}
	for i, v := range block {
		if v != 0 {
			t.Fatalf("sample %d of a silent call after a reset is %d — the "+
				"previous caller's audio is still in the filter", i, v)
		}
	}
}

// TestOnePacketBecomesThreeFrames is the other direction.
func TestOnePacketBecomesThreeFrames(t *testing.T) {
	u := NewUnpacker()
	frames := u.Add(tone(1000, 16000, ZelloSamplesPerPacket, 8000))

	if want := SamplesPerFrame * FramesPerPacket; len(frames) != want {
		t.Fatalf("one packet gave %d samples, want %d for %d frames",
			len(frames), want, FramesPerPacket)
	}
	if u.Pending() != 0 {
		t.Errorf("%d samples are pending after a whole packet", u.Pending())
	}
}

// TestAShortPacketCarriesItsRemainderForwardRatherThanPadding.
//
// Zello's header fixes the packet length, but a decoder given a lost or short
// packet returns what it has. **Padding mid-transmission inserts silence into
// the middle of somebody's sentence**, where padding at the end merely extends
// it — so a remainder is carried to the next packet and only Flush pads.
func TestAShortPacketCarriesItsRemainderForwardRatherThanPadding(t *testing.T) {
	u := NewUnpacker()

	// **The length has to leave a remainder, or the case cannot fail.** An
	// earlier version used two thirds of a packet — 640 samples, which
	// downsamples to exactly two frames of 160 — so there was nothing to pad
	// and a packer that padded mid-transmission passed. 500 samples
	// downsample to 250: one whole frame and 90 left over.
	const short = 500
	if short/2%SamplesPerFrame == 0 {
		t.Fatalf("%d samples downsample to a whole number of frames, so this "+
			"test cannot see mid-transmission padding", short)
	}
	frames := u.Add(tone(1000, 16000, short, 8000))

	whole := short / 2 / SamplesPerFrame
	if len(frames) != whole*SamplesPerFrame {
		t.Errorf("a short packet gave %d samples, want %d whole frames' worth",
			len(frames), whole*SamplesPerFrame)
	}
	if got, want := u.Pending(), short/2%SamplesPerFrame; got != want {
		t.Errorf("%d samples are pending and %d are left over; padding here "+
			"would put silence in the middle of a transmission", got, want)
	}

	// The next packet completes it, and nothing is lost.
	more := u.Add(tone(1000, 16000, ZelloSamplesPerPacket, 8000))
	total := len(frames) + len(more) + u.Pending()
	if want := (short + ZelloSamplesPerPacket) / 2; total != want {
		t.Errorf("%d samples accounted for across two packets, want %d",
			total, want)
	}

	// And a flush rounds the last one up, because a burst is 20 ms or it is
	// not a burst.
	if tail := u.Flush(); tail != nil && len(tail) != SamplesPerFrame {
		t.Errorf("the flushed frame is %d samples, want %d", len(tail), SamplesPerFrame)
	}
	if u.Pending() != 0 {
		t.Errorf("%d samples survived a flush", u.Pending())
	}
}

// TestSixtyMillisecondsOfLatencyIsTheCostAndItIsStated.
//
// Two frames are held while the third arrives, so audio toward Zello is 60 ms
// late. That is unavoidable at a fixed packet size and it dominates the path —
// the resampler's filter adds half its length, under two milliseconds. It is
// asserted here so that it is a known cost rather than something somebody goes
// hunting for.
func TestSixtyMillisecondsOfLatencyIsTheCostAndItIsStated(t *testing.T) {
	p := NewPacker()
	held := 0
	for i := 0; i < FramesPerPacket-1; i++ {
		if _, err := p.Add(make([]int16, SamplesPerFrame)); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		held++
	}
	if p.Pending() != held {
		t.Fatalf("%d frames pending, want %d", p.Pending(), held)
	}
	if ms := held * SamplesPerFrame * 1000 / 8000; ms != 40 {
		t.Errorf("the packer holds %d ms before the last frame arrives, want 40 "+
			"— so a packet is 60 ms late", ms)
	}

	// The filter's own delay, for scale: half the taps at 16 kHz.
	if ms := float64(filterTaps/2) * 1000 / filterRate; ms > 2 {
		t.Errorf("the filter delays %.2f ms, which is no longer negligible "+
			"beside the packetisation", ms)
	}
}
