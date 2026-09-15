package ambe

import "fmt"

// SamplesPerFrame is 20 ms at 8 kHz, which is what the part takes for one
// compressed frame.
const SamplesPerFrame = 160

// Transcoding a run of frames.
//
// # What this is and is not
//
// This is the transform the delivery path will call, and it is deliberately
// not the delivery path. **Nothing today can consume the PCM a decode
// produces**: Opus stays outside QSP because every Go binding is cgo
// (ADR-0062), and the Zello API and its channel policy do not exist. Wiring
// the routing core to hand frames to something that threw the audio away would
// be a sink that claims work, which §7 forbids for the same reason it forbids
// a stub that claims success.
//
// So what is built here is the part that can be finished and proved: a run of
// DMR vocoder frames in, PCM out, and frames back. When the Zello side lands,
// it consumes the middle.
//
// # Why a run and not a frame
//
// A frame at a time would be the obvious shape and it would be wrong twice
// over. The encoder and decoder each carry state, so the first frame out of a
// reset one encodes approximately nothing — PROJECT_MEMORY §8r records three
// bench runs spent learning that. And the chip answers one packet at a time,
// so a caller alternating encode and decode on one chip would pay a round trip
// twice per frame of audio. A run makes both facts visible in the signature.

// Transcoding is the result of one run through the vocoder.
type Transcoding struct {
	// Samples is the decoded audio, 160 samples per input frame.
	Samples []int16
	// Frames is the re-encoded audio, one frame per 160 samples. Empty unless
	// the run was asked to re-encode.
	//
	// A decode on its own is what the Zello direction needs. A round trip is
	// what proves the path end to end, and is the only way to hear the result
	// on a radio before Opus exists.
	Frames []ChannelFrame
	// ComfortNoise counts frames the decoder answered with comfort noise
	// rather than voice.
	//
	// **Not an error count.** Table 16's case (a): the decoder reports
	// comfort noise when it *receives* a silence frame, and a transmission
	// with pauses in it produces them legitimately — 40 out of 828 on the
	// bench, which was somebody drawing breath. It is reported because a run
	// that is *mostly* comfort noise means something else is wrong.
	ComfortNoise int
	// Invalid counts frames the decoder rejected: a frame repeat, or comfort
	// noise inserted because of channel errors. Zero out of 828 on the bench,
	// so anything above zero is worth an operator's attention.
	Invalid int
	// Tones counts frames the decoder identified as tone descriptors.
	//
	// Speech at 3600 bps changes every 20 ms and a tone descriptor does not,
	// so a run of these is the encoder describing a tone rather than coding
	// speech — which is what fifty frames of a sine turned out to be.
	Tones int
}

// Peak is the largest absolute sample in the decoded audio.
//
// The quickest check that a run produced something: a peak near zero with the
// decoder reporting valid voice frames means the frames encode silence, which
// is a different problem from frames being rejected.
func (t Transcoding) Peak() int {
	peak := 0
	for _, s := range t.Samples {
		v := int(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	return peak
}

// TranscodeRun sends a run of DMR vocoder frames through the vocoder.
//
// Each frame is the 72 bits QSP already carries: 49 parameter bits with ETSI's
// 23 correction bits behind them, which is the form rate index 33 exists for
// and the form the operator's dongle accepted 828 times out of 828
// (PROJECT_MEMORY §8r).
//
// When reencode is set, the decoded audio is fed back through the encoder and
// the resulting frames are returned — the whole path, in the shape a radio can
// hear. **The decode runs to completion before the first re-encode**, rather
// than alternating, because the chip answers one packet at a time and the
// decoder's state belongs to the stream it is decoding.
//
// The channel must be held. A caller without it has not been told whether
// somebody else is using the chip, and a frame from one is audio that may be
// about to interleave with theirs.
func (c *Client) TranscodeRun(frames [][]byte, reencode bool) (Transcoding, error) {
	if c.holder.Load() == nil {
		return Transcoding{}, ErrNotHeld
	}

	bits, ok := FrameBitsForRate(c.rate)
	if !ok {
		return Transcoding{}, fmt.Errorf("ambe: rate index %d is not in Table 115", c.rate)
	}

	var out Transcoding
	out.Samples = make([]int16, 0, len(frames)*SamplesPerFrame)

	for i, data := range frames {
		// **The frame width is checked against the rate in effect.** An
		// acknowledgement says a field arrived; the width of a frame says
		// what rate the chip is using, and a run once went out at 2400 bps
		// with nothing noticing. A frame of the wrong width here is a caller
		// handing over frames from somewhere else.
		if len(data) != (bits+7)/8 {
			return out, fmt.Errorf(
				"ambe: frame %d is %d byte(s) and rate index %d carries %d bits, "+
					"which is %d", i+1, len(data), c.rate, bits, (bits+7)/8)
		}
		speech, err := c.Decode(ChannelFrame{Bits: bits, Data: data})
		if err != nil {
			return out, fmt.Errorf("ambe: decoding frame %d of %d: %w", i+1, len(frames), err)
		}
		out.Samples = append(out.Samples, speech.Samples...)
		if speech.Reported {
			if speech.Flags&VoiceActive == 0 {
				out.ComfortNoise++
			}
			if speech.Flags&DataInvalid != 0 {
				out.Invalid++
			}
			if speech.Flags&ToneFrame != 0 {
				out.Tones++
			}
		}
	}

	if !reencode {
		return out, nil
	}

	out.Frames = make([]ChannelFrame, 0, len(frames))
	for i := 0; i+SamplesPerFrame <= len(out.Samples); i += SamplesPerFrame {
		frame, err := c.Encode(out.Samples[i : i+SamplesPerFrame])
		if err != nil {
			return out, fmt.Errorf("ambe: re-encoding frame %d: %w",
				i/SamplesPerFrame+1, err)
		}
		if frame.Bits != bits {
			return out, fmt.Errorf(
				"ambe: re-encoded frame %d carries %d bits and rate index %d gives %d; "+
					"the rate stopped being what it was set to",
				i/SamplesPerFrame+1, frame.Bits, c.rate, bits)
		}
		out.Frames = append(out.Frames, frame)
	}
	return out, nil
}

// Rate reports the built-in rate index this client set on its chip.
func (c *Client) Rate() int { return c.rate }
