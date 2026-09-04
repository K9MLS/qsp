package ipscbridge

import (
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// # Text messages, inbound
//
// See [ADR-0045](../../docs/adr/ADR-0045-ipsc-text-messages.md).
//
// A text burst arrives as DMR data already: a twelve-octet information block
// and the DMR data type that says what kind of block it is. Converting one is
// therefore much less work than converting audio, and the difference is worth
// stating because it explains why this file is short.
//
// **Voice has to be rebuilt. Text has to be re-wrapped.** IP Site Connect
// carries three vocoder frames stripped of their forward error correction, so
// the converter regenerates the FEC, computes an EMB, and places the burst at
// the right position in a superframe — none of which it can do without tracking
// state across frames. A text block is already the 96 bits a data burst
// carries. It needs BPTC coding, a Slot Type, and a sync pattern, all of which
// come from the burst in hand.
//
// # What is deliberately not done here
//
// **The message is not read.** A Rate 3/4 payload is an IPv4 UDP datagram
// carrying Motorola's Text Messaging Service, and QSP neither reassembles it
// nor looks inside it. ADR-0037 settled that principle for audio — a bridge
// carries a payload and rebuilds the wrapper — and nothing about text changes
// it. Reassembly would be a feature nobody asked for, and it would put a parser
// for somebody else's application protocol on the path of every message.
//
// **No superframe position is tracked.** Data bursts have no superframe, no
// LCSS and no embedded signalling, so the state that voice conversion needs has
// no counterpart and inventing one would be state that lies.

// ConvertText turns one IP Site Connect text burst into the Homebrew data burst
// that carries it, or returns false for a message that is not one.
//
// It returns false rather than an error for a burst it cannot place, matching
// Convert: a message QSP does not understand is counted and logged by the
// caller, and this package does not decide what that is worth.
func (c *Converter) ConvertText(m ipsc.Message, repeater hbp.RepeaterID) (hbp.Data, bool) {
	t, ok := m.AsText()
	if !ok {
		return hbp.Data{}, false
	}

	// Only the twelve-octet blocks can be placed in a burst. A Rate 3/4 burst
	// carries twenty-two octets and does not fit the 96-bit information block,
	// so it is refused rather than truncated — half a text message delivered
	// is worse than none, because it looks like it worked.
	if len(t.Block) != dmrfec.LinkControlBlockBytes {
		return hbp.Data{}, false
	}

	burst, err := dmrfec.BuildDataBurstFromBlock(c.cfg.ColourCode, t.DataType, t.Block)
	if err != nil {
		return hbp.Data{}, false
	}

	// The colour code written into the burst is the converter's, not the one
	// the text arrived with. **That is the same choice voice conversion
	// makes**: the burst is going to a hotspot on this network, and the
	// sending repeater's colour code is a fact about somebody else's RF.
	call := hbp.CallGroup
	if t.Private {
		call = hbp.CallPrivate
	}

	out := hbp.Data{
		Sequence:   uint8(t.Sequence),
		SourceID:   t.Source,
		TargetID:   t.Destination,
		RepeaterID: repeater,
		Timeslot:   c.timeslotFor(t.SlotSet),
		CallType:   call,
		// The data synchronisation frame type, with the DMR data type in the
		// low nibble — the same two encodings of one fact that voice
		// signalling uses, and the arrangement the Homebrew captures show.
		FrameType: hbp.FrameTypeSync,
		DataType:  t.DataType,
		StreamID:  streamFor(t.StreamID, t.Source),
	}
	copy(out.Payload[:], burst)
	return out, true
}

// timeslotFor turns the raw slot bit into a timeslot under the configured
// polarity.
//
// **The polarity is configuration and always has been.** `Timeslot` does the
// same for a voice message; this exists because a text carries the bit already
// decoded and there is no ipsc.Voice to hand.
func (c *Converter) timeslotFor(slotSet bool) hbp.Timeslot {
	if slotSet == c.cfg.SlotBitIsTimeslot2 {
		return hbp.Timeslot2
	}
	return hbp.Timeslot1
}
