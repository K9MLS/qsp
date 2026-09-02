// Package ipscbridge turns Motorola IP Site Connect voice into Homebrew bursts.
//
// # What it does and does not do
//
// It converts the voice frames a Motorola repeater sends into the 33-byte DMR
// bursts a Homebrew peer expects, rebuilding the forward error correction, the
// synchronisation pattern and the embedded signalling that IPSC leaves out.
// **The vocoder parameters are copied and never inspected**, so the audio a
// radio reproduces is the audio the originating radio encoded. See
// docs/adr/ADR-0037.
//
// It does not yet produce the voice header and terminator bursts that open and
// close a transmission on air. Those need a Link Control checksum that no
// capture has yet pinned down; internal/dmrfec can build the block once the
// checksum is known. Until then a receiving radio hears audio but learns who is
// talking only from late entry.
//
// It does not route. Handing bursts to peers is the caller's business.
package ipscbridge

import (
	"fmt"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// Config configures a Converter.
type Config struct {
	// ColourCode is written into the embedded signalling of every burst.
	ColourCode uint8
	// SlotBitIsTimeslot2 says which value of the IPSC slot bit means timeslot
	// two.
	//
	// **The polarity was never recorded.** Fifteen transmissions from two
	// channels split cleanly by one bit, so the bit is the timeslot beyond
	// doubt, but which value is which was not written down at the radio. It is
	// configuration rather than a constant so that an operator who knows can
	// say so, and so that getting it wrong is a setting rather than a rebuild.
	SlotBitIsTimeslot2 bool
}

// Converter turns one repeater's voice frames into Homebrew bursts.
//
// It is not safe for concurrent use: one Converter belongs to one peer, and a
// peer sends one transmission at a time per timeslot.
type Converter struct {
	cfg Config

	// position counts bursts since the last synchronisation frame. A
	// superframe is six bursts and the position decides the embedded
	// signalling, so a converter that loses count produces bursts a radio
	// rejects.
	position int
	// started reports whether a synchronisation frame has been seen. Before
	// one has, the position is unknown and guessing it would be worse than
	// waiting: a transmission joined mid-superframe recovers within six
	// frames.
	started bool
	// sequence is the Homebrew frame counter, which is per transmission.
	sequence uint8
	// stream is the IPSC stream currently being converted.
	stream uint16
}

// New constructs a Converter.
func New(cfg Config) (*Converter, error) {
	if cfg.ColourCode > 15 {
		return nil, fmt.Errorf("ipscbridge: colour code %d is out of range; DMR allows 0 to 15",
			cfg.ColourCode)
	}
	return &Converter{cfg: cfg}, nil
}

// Convert turns one IPSC voice message into a Homebrew data frame.
//
// ok is false for messages that are not voice, and for voice frames arriving
// before the first synchronisation frame of a superframe — at that point the
// position in the superframe is unknown, and a burst built at the wrong
// position carries embedded signalling a radio will reject. Waiting costs at
// most six frames, 360 ms, at the start of a transmission joined late.
func (c *Converter) Convert(m ipsc.Message, repeater hbp.RepeaterID) (hbp.Data, bool) {
	v, isVoice := m.AsVoice()
	if !isVoice {
		return hbp.Data{}, false
	}
	class, vocoder, fragment, ok := payloadOf(m)
	if !ok {
		return hbp.Data{}, false
	}

	if v.StreamID != c.stream {
		// A new transmission. Counters start again rather than carrying a
		// previous call's numbering into a new one.
		c.stream = v.StreamID
		c.sequence = 0
		c.started = false
		c.position = 0
	}

	if class == ipsc.PayloadSync {
		c.started = true
		c.position = 0
	} else if c.started {
		c.position++
		if c.position >= dmrfec.SuperframeBursts {
			// A superframe that overruns means a frame was lost. Position is
			// no longer trustworthy, so stop emitting until the next sync
			// frame rather than guess.
			c.started = false
		}
	}
	if !c.started {
		return hbp.Data{}, false
	}

	middle, err := dmrfec.MiddleForPosition(c.position, c.cfg.ColourCode, fragment)
	if err != nil {
		return hbp.Data{}, false
	}
	burst, ok := dmrfec.BurstFromIPSC(vocoder, middle)
	if !ok {
		return hbp.Data{}, false
	}

	slotSet, _ := m.SlotBit()
	slot := hbp.Timeslot1
	if slotSet == c.cfg.SlotBitIsTimeslot2 {
		slot = hbp.Timeslot2
	}

	frameType := hbp.FrameTypeVoice
	if c.position == 0 {
		frameType = hbp.FrameTypeVoiceSync
	}

	out := hbp.Data{
		Sequence:   c.sequence,
		SourceID:   v.SourceID,
		TargetID:   v.Destination,
		RepeaterID: repeater,
		Timeslot:   slot,
		CallType:   hbp.CallGroup,
		FrameType:  frameType,
		// The low nibble of the flags byte carries the position within the
		// superframe for voice frames, which is what a receiver uses to place
		// the burst. It is the same count this converter already keeps.
		DataType: uint8(c.position),
		StreamID: hbp.StreamID(uint32(v.StreamID)<<16 | uint32(v.SourceID&0xFFFF)),
	}
	copy(out.Payload[:], burst)
	c.sequence++
	return out, true
}

// payloadOf pulls the pieces a burst is built from out of a voice message.
func payloadOf(m ipsc.Message) (class byte, vocoder []byte, fragment uint32, ok bool) {
	class, ok = m.SuperframePosition()
	if !ok {
		return 0, nil, 0, false
	}
	_, core, _, valid := m.Payload()
	if !valid || len(core) != dmrfec.IPSCCoreBytes {
		return 0, nil, 0, false
	}
	frag, _ := m.EmbeddedFragment()
	return class, core, frag, true
}
