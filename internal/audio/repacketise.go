package audio

import "fmt"

// Repacketising between a 20 ms radio frame and a 60 ms Zello packet.
//
// # The three-to-one that shapes everything
//
// A DMR voice frame is 20 ms and Zello's codec is fixed at 60 ms per packet
// (ADR-0062). So **three frames make one packet, and one packet makes three
// frames**, and the conversion is not a pass-through in either direction.
//
// Two consequences worth stating rather than discovering:
//
// **It costs 60 ms of latency toward Zello.** Two frames have to be held while
// the third arrives. That is unavoidable at a fixed 60 ms packet size, it is
// the dominant delay in the whole path — the resampler's filter adds under two
// milliseconds — and it is not a defect for somebody to go hunting.
//
// **A transmission is rarely a multiple of three frames.** Whatever is left
// when the operator releases the button is a partial block, and the obvious
// thing to do with it is drop it. **That clips the last word of every
// transmission**, which is a defect an operator hears on every single call and
// would be hard to attribute. So a partial block is padded with silence and
// sent, which loses nothing and costs one packet.

// FramesPerPacket is how many 20 ms radio frames make one Zello packet.
const FramesPerPacket = 3

// ZelloFrameMS is the packet length Zello's Channels API fixes.
//
// From their own example codec header, `gD4BPA==`, which decodes to
// `80 3e 01 3c`: sample rate 16000 little-endian, one frame per packet, and
// 0x3c = 60 milliseconds.
const ZelloFrameMS = 60

// ZelloSampleRate is the rate Zello's codec header declares.
const ZelloSampleRate = 16000

// ZelloSamplesPerPacket is one Zello packet's worth of 16 kHz audio.
const ZelloSamplesPerPacket = ZelloSampleRate * ZelloFrameMS / 1000

// Packer gathers 20 ms frames of 8 kHz audio into 60 ms blocks of 16 kHz.
//
// It owns the upsampler, because the two have to be reset together: a new
// transmission needs both a clear filter and an empty block, and a caller
// remembering one but not the other is exactly the kind of half-reset that
// leaves the previous caller's tail at the front of the next call.
type Packer struct {
	up      *Upsampler
	pending []int16
	frames  int
}

// NewPacker returns a packer ready for the first frame of a transmission.
func NewPacker() *Packer {
	return &Packer{
		up:      NewUpsampler(),
		pending: make([]int16, 0, ZelloSamplesPerPacket),
	}
}

// Reset prepares for a new transmission, discarding any partial block.
//
// **Discarding is right here and wrong in Flush.** A reset happens because a
// new transmission is starting, so a leftover block belongs to a call that has
// already ended and sending it would put one caller's audio at the front of
// another's.
func (p *Packer) Reset() {
	p.up.Reset()
	p.pending = p.pending[:0]
	p.frames = 0
}

// Add takes one 20 ms frame of 8 kHz audio and returns a complete 60 ms block
// of 16 kHz audio when one is ready, or nil.
func (p *Packer) Add(frame []int16) ([]int16, error) {
	if len(frame) != SamplesPerFrame {
		return nil, fmt.Errorf(
			"audio: a radio frame is %d samples at 8 kHz, got %d",
			SamplesPerFrame, len(frame))
	}

	p.pending = append(p.pending, p.up.Up(frame)...)
	p.frames++
	if p.frames < FramesPerPacket {
		return nil, nil
	}

	block := make([]int16, ZelloSamplesPerPacket)
	copy(block, p.pending)
	p.pending = p.pending[:0]
	p.frames = 0
	return block, nil
}

// Flush returns a final partial block padded with silence, or nil if nothing
// is pending.
//
// **It has to be called when the button is released.** Without it the last one
// or two frames of every transmission are never sent, which clips the final
// word of every call — audible on every transmission and hard to attribute to
// a buffer.
func (p *Packer) Flush() []int16 {
	if p.frames == 0 {
		return nil
	}
	block := make([]int16, ZelloSamplesPerPacket)
	copy(block, p.pending)
	p.pending = p.pending[:0]
	p.frames = 0
	return block
}

// Pending reports how many frames are held, for a health report or a test.
func (p *Packer) Pending() int { return p.frames }

// Unpacker splits 60 ms blocks of 16 kHz audio into 20 ms frames of 8 kHz.
//
// It owns the downsampler for the same reason Packer owns the upsampler.
type Unpacker struct {
	down    *Downsampler
	pending []int16
}

// NewUnpacker returns an unpacker ready for the first packet of a
// transmission.
func NewUnpacker() *Unpacker {
	return &Unpacker{
		down:    NewDownsampler(),
		pending: make([]int16, 0, SamplesPerFrame*FramesPerPacket),
	}
}

// Reset prepares for a new transmission.
func (u *Unpacker) Reset() {
	u.down.Reset()
	u.pending = u.pending[:0]
}

// Add takes one decoded 60 ms block of 16 kHz audio and returns the whole
// 20 ms frames of 8 kHz audio it yields.
//
// **A block need not be exactly 60 ms.** Zello's header fixes the packet
// length, but a decoder given a lost or short packet returns what it has, and
// any remainder is carried to the next call rather than padded — padding
// mid-transmission inserts silence into the middle of somebody's sentence,
// where Flush's padding at the end merely extends it.
func (u *Unpacker) Add(block []int16) []int16 {
	u.pending = append(u.pending, u.down.Down(block)...)

	whole := len(u.pending) / SamplesPerFrame
	if whole == 0 {
		return nil
	}
	out := make([]int16, whole*SamplesPerFrame)
	copy(out, u.pending)
	u.pending = append(u.pending[:0], u.pending[whole*SamplesPerFrame:]...)
	return out
}

// Flush returns a final partial frame padded with silence, or nil.
//
// The radio side cannot carry half a frame: a DMR burst is 20 ms or it is not
// a burst.
func (u *Unpacker) Flush() []int16 {
	if len(u.pending) == 0 {
		return nil
	}
	frame := make([]int16, SamplesPerFrame)
	copy(frame, u.pending)
	u.pending = u.pending[:0]
	return frame
}

// Pending reports how many samples are held.
func (u *Unpacker) Pending() int { return len(u.pending) }
