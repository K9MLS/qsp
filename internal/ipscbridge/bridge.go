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

// slotState is everything a converter must remember about one timeslot.
//
// **It is per timeslot rather than per repeater, and that is the whole point.**
// A repeater carries two independent transmissions at once, one on each slot,
// and their frames arrive interleaved on the same socket. Held in one set of
// fields, the two streams reset each other's counters on every frame: run the
// 66 real frames of testdata/ipsc/ipsc-probe-voice.pcap twice, once per slot
// and interleaved, and a single-state converter produces 18 bursts where it
// produced 54 from one slot alone. Two thirds of the audio that worked
// disappears the moment a second slot is used.
type slotState struct {
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
	// seen reports whether any frame has been converted on this slot, which
	// distinguishes "no transmission yet" from "a transmission whose stream
	// ID happens to be zero".
	seen bool
}

// Converter turns one repeater's voice frames into Homebrew bursts.
//
// It is not safe for concurrent use: one Converter belongs to one peer. Both of
// that peer's timeslots are handled, each with its own state, so a caller
// cannot forget to separate them.
type Converter struct {
	cfg Config

	// slots is indexed by timeslot, 0 for TS1 and 1 for TS2.
	slots [2]slotState
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

	// The timeslot is resolved before anything is remembered, because which
	// state this frame belongs to is decided by the slot and nothing else.
	slot := c.Timeslot(m)
	st := &c.slots[slotIndex(slot)]

	if v.StreamID != st.stream || !st.seen {
		// A new transmission. Counters start again rather than carrying a
		// previous call's numbering into a new one.
		st.stream = v.StreamID
		st.seen = true
		st.sequence = 0
		st.started = false
		st.position = 0
	}

	if class == ipsc.PayloadSync {
		st.started = true
		st.position = 0
	} else if st.started {
		st.position++
		if st.position >= dmrfec.SuperframeBursts {
			// A superframe that overruns means a frame was lost. Position is
			// no longer trustworthy, so stop emitting until the next sync
			// frame rather than guess.
			st.started = false
		}
	}
	if !st.started {
		return hbp.Data{}, false
	}

	middle, err := dmrfec.MiddleForPosition(st.position, c.cfg.ColourCode, fragment)
	if err != nil {
		return hbp.Data{}, false
	}
	burst, ok := dmrfec.BurstFromIPSC(vocoder, middle)
	if !ok {
		return hbp.Data{}, false
	}

	frameType := hbp.FrameTypeVoice
	if st.position == 0 {
		frameType = hbp.FrameTypeVoiceSync
	}

	out := hbp.Data{
		Sequence:   st.sequence,
		SourceID:   v.SourceID,
		TargetID:   v.Destination,
		RepeaterID: repeater,
		Timeslot:   slot,
		CallType:   hbp.CallGroup,
		FrameType:  frameType,
		// The low nibble of the flags byte carries the position within the
		// superframe for voice frames, which is what a receiver uses to place
		// the burst. It is the same count this converter already keeps.
		DataType: uint8(st.position),
		StreamID: hbp.StreamID(uint32(v.StreamID)<<16 | uint32(v.SourceID&0xFFFF)),
	}
	copy(out.Payload[:], burst)
	st.sequence++
	return out, true
}

// Timeslot reports which DMR timeslot a message belongs to, under this
// converter's configured polarity.
//
// It is exported because a caller that keeps one converter per peer still needs
// the slot for its own bookkeeping — a call tracker, a log line — and reading
// the bit a second time in the caller would be two places to get the polarity
// wrong. A message that carries no slot bit is not voice and never reaches
// Convert; Timeslot1 is the harmless answer for it.
func (c *Converter) Timeslot(m ipsc.Message) hbp.Timeslot {
	set, ok := m.SlotBit()
	if !ok {
		return hbp.Timeslot1
	}
	if set == c.cfg.SlotBitIsTimeslot2 {
		return hbp.Timeslot2
	}
	return hbp.Timeslot1
}

// slotIndex maps a timeslot to its place in the state array.
func slotIndex(slot hbp.Timeslot) int {
	if slot == hbp.Timeslot2 {
		return 1
	}
	return 0
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
