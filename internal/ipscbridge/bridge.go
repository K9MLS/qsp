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
// It produces the voice header that opens a transmission and the terminator
// that closes it. **Both are required rather than decorative.** Clause 5.1.2.2
// of ETSI TS 102 361-1 says a voice transmission shall be preceded by a voice
// LC header, so a stream of voice bursts with nothing in front of them is not a
// valid transmission; and clause 5.1.2.3 makes a data-sync burst the thing that
// ends one, without which a receiver waits out a timeout and the next
// transmission is refused while the destination is still held.
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

	// textStream, textSeen and textSequence number a text message's bursts.
	//
	// **Kept apart from the voice counters** because a text and an over are
	// separate transmissions that can interleave on one timeslot: a text
	// arriving mid-over must not renumber the audio, and vice versa.
	textStream   hbp.StreamID
	textSeen     bool
	textSequence uint8
	// stream is the IPSC stream currently being converted.
	stream uint16
	// seen reports whether any frame has been converted on this slot, which
	// distinguishes "no transmission yet" from "a transmission whose stream
	// ID happens to be zero".
	seen bool
	// opened is the transmission as the repeater first described it: who is
	// talking, to whom, and under which stream ID. Every later frame of the
	// transmission is converted under it. See continues.
	opened ipsc.Voice
	// sentHeader reports whether this transmission's voice LC header has been
	// emitted. It is sent immediately before the first voice burst rather than
	// on the first frame received, because ETSI TS 102 361-1 clause 5.1.2.2
	// requires the header to *immediately precede* burst A — and the converter
	// waits for a superframe boundary before emitting anything, so the first
	// burst it produces is always an A.
	sentHeader bool
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

// Convert turns one IPSC voice message into the Homebrew frames it produces.
//
// It returns **zero, one, two or three frames**, because a burst of audio is
// not always the whole of what a moment in a transmission requires:
//
//   - nothing, for a message that is not voice, or a voice frame arriving
//     before the first synchronisation frame — at that point the position in
//     the superframe is unknown, and a burst built at the wrong position
//     carries embedded signalling a radio rejects. Waiting costs at most six
//     frames, 360 ms, at the start of a transmission joined late.
//   - a voice LC header followed by the burst, at the start of a transmission.
//   - a burst on its own, in the middle of one.
//   - a burst followed by a terminator, at the end.
//
// The header goes immediately before the first burst rather than on the first
// frame received. Clause 5.1.2.2 requires it to immediately precede burst A,
// and since this converter emits nothing until a superframe boundary, the first
// burst it produces is always an A.
func (c *Converter) Convert(m ipsc.Message, repeater hbp.RepeaterID) []hbp.Data {
	v, isVoice := m.AsVoice()
	if !isVoice {
		return nil
	}
	// **A frame whose payload cannot be read may still be the end of the
	// transmission**, and the last frame of one is exactly where an
	// unreadable payload turns up. Reading the flags before the payload is
	// what lets the terminator survive it.
	class, vocoder, fragment, havePayload := payloadOf(m)

	slot := c.Timeslot(m)
	st := &c.slots[slotIndex(slot)]

	if st.continues(m, v) {
		v = st.under(v)
	}
	if v.StreamID != st.stream || !st.seen {
		// A new transmission. Counters start again rather than carrying a
		// previous call's numbering into a new one.
		st.stream = v.StreamID
		st.opened = v
		st.seen = true
		st.sequence = 0
		st.started = false
		st.position = 0
		st.sentHeader = false
	}

	if !havePayload {
		if v.IsLastFrame() && st.sentHeader {
			stream := streamFor(v.StreamID, v.SourceID)
			var out []hbp.Data
			if term, err := c.dataFrame(st, v, repeater, slot, stream,
				dmrfec.DataTypeTerminatorWithLC); err == nil {
				out = append(out, term)
			}
			st.started = false
			st.seen = false
			return out
		}
		return nil
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
	stream := streamFor(v.StreamID, v.SourceID)
	out := make([]hbp.Data, 0, 2)

	// **A transmission that was opened must be closed, even if this frame's
	// audio cannot be placed.** The last frame of a transmission is exactly
	// the one most likely to fall outside a superframe boundary, and treating
	// an unplaceable burst as a reason to skip the terminator leaves the
	// destination held until a timeout — which refuses the next transmission.
	// Losing 60 ms of audio is the smaller harm; losing the terminator costs
	// the whole of the next over.
	if !st.started {
		if v.IsLastFrame() && st.sentHeader {
			if term, err := c.dataFrame(st, v, repeater, slot, stream,
				dmrfec.DataTypeTerminatorWithLC); err == nil {
				out = append(out, term)
			}
			st.seen = false
			return out
		}
		return nil
	}

	middle, err := dmrfec.MiddleForPosition(st.position, c.cfg.ColourCode, fragment)
	burst, ok := dmrfec.BurstFromIPSC(vocoder, middle)
	if err != nil || !ok {
		if v.IsLastFrame() && st.sentHeader {
			if term, terr := c.dataFrame(st, v, repeater, slot, stream,
				dmrfec.DataTypeTerminatorWithLC); terr == nil {
				out = append(out, term)
			}
			st.started = false
			st.seen = false
		}
		return out
	}

	if !st.sentHeader {
		hdr, err := c.dataFrame(st, v, repeater, slot, stream, dmrfec.DataTypeVoiceLCHeader)
		if err != nil {
			return nil
		}
		st.sentHeader = true
		out = append(out, hdr)
	}

	frameType := hbp.FrameTypeVoice
	if st.position == 0 {
		frameType = hbp.FrameTypeVoiceSync
	}
	voice := hbp.Data{
		Sequence:   st.sequence,
		SourceID:   v.SourceID,
		TargetID:   v.Destination,
		RepeaterID: repeater,
		Timeslot:   slot,
		// **The call type comes from the frame.** It was fixed at group for
		// as long as the listener refused 0x81 outright, so a private call
		// could not reach here to be mislabelled. It can now.
		CallType:  callTypeOf(v),
		FrameType: frameType,
		// The low nibble of the flags byte carries the position within the
		// superframe for voice frames, which is what a receiver uses to place
		// the burst. It is the same count this converter already keeps.
		DataType: uint8(st.position),
		StreamID: stream,
	}
	copy(voice.Payload[:], burst)
	st.sequence++
	out = append(out, voice)

	// **The terminator goes after the audio, not instead of it.** The frame
	// that carries the terminator flag still carries a vocoder payload, and
	// dropping it would clip the last 60 ms of every transmission.
	if v.IsLastFrame() {
		term, err := c.dataFrame(st, v, repeater, slot, stream, dmrfec.DataTypeTerminatorWithLC)
		if err == nil {
			out = append(out, term)
		}
		// Whatever happens next on this slot is a new transmission.
		st.started = false
		st.seen = false
	}
	return out
}

// continues reports whether a frame that describes itself differently from the
// transmission open on this slot is nonetheless part of it.
//
// # What a Motorola repeater does with a Talker Alias
//
// testdata/ipsc/ipsc-talker-alias.pcap is an XPR8300 passing on a radio that
// sends the alias "K9MLS Portable". For the six frames of the superframe that
// carries the first alias block, the repeater fills the header of its own
// frames from that block as though it were a group call's Link Control: the
// source reads 0x4C5320 and the destination 0x4B394D, which are the letters
// "LS " and "K9M", the call counter goes up by one and the stream ID changes.
// One superframe later the radio's own IDs come back, under a third stream ID
// and a third call counter. Both key-ups in the capture do it, 1.08 seconds in.
//
// A converter that believed the stream ID cut every such over into three: a
// second of audio with no end, a call from a station that does not exist, and
// the rest as a new call that every destination refused until the first one's
// hold ran out. That lost a second and a half of the over, on every key-up, for
// any radio with an alias switched on.
//
// # What marks a transmission instead
//
// **The repeater marks a beginning, and it marked none of these.** A
// transmission opens with header frames, the first of them flagged; it closes
// on a flagged terminator. All 87 frames after the switch in the first key-up
// carry the flags of the middle of a transmission, and none is a header. So a
// frame is the open transmission's unless the repeater says otherwise, whatever
// its header claims about who is talking.
//
// A transmission whose end was lost is still told from the next one: that one
// arrives with its headers, and a slot that went quiet is forgotten by the
// caller through Forget.
func (st *slotState) continues(m ipsc.Message, v ipsc.Voice) bool {
	if !st.seen || v.StreamID == st.stream {
		return false
	}
	return !v.IsFirstFrame() && !isHeader(m)
}

// under returns a frame's header as the open transmission describes it, keeping
// what belongs to the frame itself: its flags, its sequence and its timestamp.
func (st *slotState) under(v ipsc.Voice) ipsc.Voice {
	v.SourceID = st.opened.SourceID
	v.Destination = st.opened.Destination
	v.Private = st.opened.Private
	v.StreamID = st.opened.StreamID
	return v
}

// isHeader reports whether a voice message is one of the header frames that
// open a transmission.
func isHeader(m ipsc.Message) bool {
	return len(m.Body) > 25 && ipsc.FrameKindOf(m.Body[25]) == ipsc.FrameHeader
}

// Resolve reads a voice message's header as Convert will convert it: under the
// transmission open on its timeslot when the frame is part of one, and as the
// repeater wrote it otherwise.
//
// It changes nothing. It is exported so that a caller keeping its own record of
// calls asks the converter which call a frame belongs to instead of deciding a
// second time from the stream ID, which is the reading this replaced.
func (c *Converter) Resolve(m ipsc.Message) (ipsc.Voice, bool) {
	v, ok := m.AsVoice()
	if !ok {
		return ipsc.Voice{}, false
	}
	st := &c.slots[slotIndex(c.Timeslot(m))]
	if st.continues(m, v) {
		v = st.under(v)
	}
	return v, true
}

// Forget drops the transmission open on a timeslot, so that the next frame
// there starts a new one whatever it carries.
//
// The caller's clock decides when: a slot with no frame for longer than a
// transmission can pause has no transmission on it, and a call that begins
// there afterwards must not be read as the old one continuing.
func (c *Converter) Forget(slot hbp.Timeslot) {
	st := &c.slots[slotIndex(slot)]
	st.seen = false
	st.started = false
	st.sentHeader = false
}

// dataFrame builds a voice header or terminator for the transmission in
// progress.
func (c *Converter) dataFrame(st *slotState, v ipsc.Voice, repeater hbp.RepeaterID,
	slot hbp.Timeslot, stream hbp.StreamID, dataType uint8) (hbp.Data, error) {

	lc := dmrfec.LinkControlFor(v.Destination, v.SourceID, v.Private)
	burst, err := dmrfec.BuildDataBurst(c.cfg.ColourCode, dataType, lc)
	if err != nil {
		return hbp.Data{}, err
	}
	out := hbp.Data{
		Sequence:   st.sequence,
		SourceID:   v.SourceID,
		TargetID:   v.Destination,
		RepeaterID: repeater,
		Timeslot:   slot,
		CallType:   callTypeOf(v),
		// A data burst is announced by the data synchronisation frame type,
		// and which data burst it is comes from the low nibble — the same
		// four-bit data type the Slot Type inside the burst carries. Two
		// encodings of one fact, which is the arrangement the Homebrew
		// captures already show.
		FrameType: hbp.FrameTypeSync,
		DataType:  dataType,
		StreamID:  stream,
	}
	copy(out.Payload[:], burst)
	st.sequence++
	return out, nil
}

// callTypeOf maps a voice frame's call type onto the Homebrew one.
//
// It exists so that the header, the terminator and the audio between them
// cannot disagree: three constructions of hbp.Data, one answer.
func callTypeOf(v ipsc.Voice) hbp.CallType {
	if v.Private {
		return hbp.CallPrivate
	}
	return hbp.CallGroup
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
	return c.timeslotFor(set)
}

// streamFor derives the Homebrew stream ID for one IP Site Connect
// transmission.
//
// A Homebrew stream ID is 32 bits and an IPSC one is 16, so the sender's radio
// ID fills the rest: two repeaters transmitting at the same moment with the
// same 16-bit stream ID would otherwise be one transmission to the routing
// core, and their audio would interleave.
//
// **It exists because the expression was written inline three times**, once for
// voice signalling, once for voice frames and once for text. Three copies of a
// derivation is three places for one of them to drift, and a stream ID that
// disagrees with itself mid-transmission splits an over in two.
func streamFor(stream uint16, source uint32) hbp.StreamID {
	return hbp.StreamID(uint32(stream)<<16 | source&0xFFFF)
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
