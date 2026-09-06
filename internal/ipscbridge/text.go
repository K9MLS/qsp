package ipscbridge

import (
	"fmt"

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

	// Two block sizes, two codings, and for eighteen patches only one of them
	// was built. A twelve-octet block is BPTC(196,96); an eighteen-octet one
	// is Rate 3/4 Trellis, and **every content block of every text is one of
	// those**. Refusing them dropped the message and delivered the header, so
	// the far end saw a header promising blocks that never came.
	burst, err := c.burstFor(t.DataType, t.Block)
	if err != nil {
		return hbp.Data{}, false
	}

	// **A Homebrew stream is numbered from zero**, and this used the raw IPSC
	// sequence, which is a free-running counter shared by every transmission
	// on the link. A capture of a text crossing the bridge showed one stream
	// starting at 69 and the next at 67, where a hotspot's own streams all
	// start at 0 and count up.
	//
	// The voice path has always done this — `st.sequence` resets when the
	// stream ID changes — and text was written without it. Whether MMDVM
	// refuses on it is unproven; it is wrong on its own terms either way, and
	// it is the one place text differed from the voice path that works.
	st := &c.slots[slotIndex(c.timeslotFor(t.SlotSet))]
	stream := streamFor(t.StreamID, t.Source)
	if st.textStream != stream || !st.textSeen {
		st.textStream = stream
		st.textSeen = true
		st.textSequence = 0
	} else {
		st.textSequence++
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
		Sequence:   st.textSequence,
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
		StreamID:  stream,
	}
	copy(out.Payload[:], burst)
	return out, true
}

// burstFor codes one information block into the DMR burst that carries it.
//
// **The block's length decides the coding, not the data type.** Byte 30 of the
// datagram agrees on every captured frame, but the block QSP holds is the
// thing being coded, and a length that disagrees with a data type is a
// datagram to refuse rather than a burst to build from whichever of the two we
// happened to trust.
func (c *Converter) burstFor(dataType uint8, block []byte) ([]byte, error) {
	switch len(block) {
	case dmrfec.Rate34BlockBytes:
		if dataType != dmrfec.DataTypeRate34 {
			return nil, fmt.Errorf("ipscbridge: an %d-octet block arrived as data type %#x",
				len(block), dataType)
		}
		return dmrfec.BuildRate34Burst(c.cfg.ColourCode, block)
	case dmrfec.LinkControlBlockBytes:
		if dataType == dmrfec.DataTypeRate34 {
			return nil, fmt.Errorf("ipscbridge: a Rate 3/4 burst carried only %d octets",
				len(block))
		}
		return dmrfec.BuildDataBurstFromBlock(c.cfg.ColourCode, dataType, block)
	default:
		return nil, fmt.Errorf("ipscbridge: no coding carries a %d-octet block", len(block))
	}
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
