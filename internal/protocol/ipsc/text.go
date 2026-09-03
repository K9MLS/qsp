package ipsc

import "encoding/binary"

// Text messages over IP Site Connect.
//
// See [ADR-0045](../../../docs/adr/ADR-0045-ipsc-text-messages.md) and
// testdata/ipsc/ipsc-text.pcap, from which every offset below was measured
// across 163 bursts.
//
// # A text is DMR data in the voice envelope
//
// Everything before byte 30 is the envelope voice already uses: type byte,
// sender ID, call counter, source, destination, stream ID, slot bit, flags,
// sequence and timestamp, followed by the same `00 0a 80 0a 00 60` constants a
// voice header carries. Byte 12 reads 0x01 where voice reads 0x02.
//
// From byte 30 a data burst is laid out exactly as a **voice header**, not as a
// voice frame: a twelve-octet block, a zero, the DMR Slot Type, and the
// two-byte tail ADR-0042 could not derive. Byte 50 was zero in all 150 captured
// 54-byte frames and byte 51 equalled `(colourCode << 4) | dataType` in all 150.
//
// # What QSP does not need to understand
//
// The payload of a Rate 3/4 burst is an IPv4 UDP datagram whose source address
// is `0x0c` followed by the sender's 24-bit radio ID — Motorola's radio-IP
// scheme, carrying its Text Messaging Service. **QSP does not decode it.**
// ADR-0037 settled the principle for audio and it holds here: a bridge carries
// the payload and rebuilds the wrapper around it. Reassembling TMS would be
// inventing a requirement.
const (
	// KindTextGroup is a text addressed to a talkgroup.
	KindTextGroup Kind = 0x83
	// KindTextPrivate is a text addressed to one radio.
	//
	// The two kinds are the call type and nothing else: the envelope
	// destination and the destination inside the payload agree, and both read
	// a talkgroup for 0x83 and a radio ID for 0x84.
	KindTextPrivate Kind = 0x84
)

// Offsets within a data burst, in whole-datagram coordinates so they can be
// read directly against a capture.
const (
	// TextDataTypeAt is the DMR data type — CSBK, Data Header, Rate 1/2 Data
	// and Rate 3/4 Data were all observed. It is the same byte a voice frame
	// uses for its marker, and it agrees with the low nibble of the Slot Type
	// in 153 of 162 captured frames.
	TextDataTypeAt = 30
	// TextBlockAt is where the twelve-octet block begins: the same offset a
	// voice header carries its Link Control block.
	TextBlockAt = 38
	// TextBlockLen is the length of that block, which is the 96-bit
	// information block of a BPTC(196,96) data burst.
	TextBlockLen = 12
	// TextSlotTypeAt is the DMR Slot Type: colour code high, data type low.
	TextSlotTypeAt = 51
	// TextRate34At is where a Rate 3/4 burst's longer payload begins. Those
	// frames are 60 bytes where the rest are 54.
	TextRate34Len = 22
)

// IsText reports whether a message carries a text message burst.
func (k Kind) IsText() bool { return k == KindTextGroup || k == KindTextPrivate }

// Text is the decoded header of a text message burst.
type Text struct {
	// Counter is the call counter, byte 5.
	Counter uint8
	// Source is the radio that sent the text.
	Source uint32
	// Destination is a talkgroup for KindTextGroup and a radio for
	// KindTextPrivate.
	Destination uint32
	// Private reports which of the two it is.
	Private bool
	// StreamID identifies the transmission.
	StreamID uint16
	// Flags is the two-byte field voice uses for first, middle and last.
	Flags uint16
	// Sequence and Timestamp advance across a transmission exactly as they do
	// for voice.
	Sequence  uint16
	Timestamp uint32
	// DataType is the DMR data type from byte 30 — CSBK, Data Header, Rate
	// 1/2 Data or Rate 3/4 Data were all observed.
	DataType uint8
	// ColourCode is the high nibble of the Slot Type.
	ColourCode uint8
	// SlotSet reports the timeslot bit, whose polarity is configuration.
	SlotSet bool
	// Block is the twelve octets at TextBlockAt, or the twenty-two octets of a
	// Rate 3/4 burst. **Carried, not interpreted.**
	Block []byte
}

// AsText decodes a text message burst.
//
// It returns false for any message that is not one, and for a burst too short
// to hold a block — including the 34-byte frame seen once at the end of a
// transmission, whose marker 0x13 is not a DMR data type and whose purpose is
// unknown. Naming it would be a claim this package cannot demonstrate.
func (m Message) AsText() (Text, bool) {
	if !m.Kind.IsText() {
		return Text{}, false
	}
	// Body offsets are five less than datagram offsets: a body begins after
	// the type byte and the 32-bit sender ID.
	b := m.Body
	const need = TextSlotTypeAt - HeaderLen + 1
	if len(b) < need {
		return Text{}, false
	}

	t := Text{
		Counter:     b[0],
		Source:      uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]),
		Destination: uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6]),
		Private:     m.Kind == KindTextPrivate,
		StreamID:    binary.BigEndian.Uint16(b[10:12]),
		Flags:       binary.BigEndian.Uint16(b[13:15]),
		Sequence:    binary.BigEndian.Uint16(b[15:17]),
		Timestamp:   binary.BigEndian.Uint32(b[17:21]),
		DataType:    b[TextDataTypeAt-HeaderLen],
		ColourCode:  b[TextSlotTypeAt-HeaderLen] >> 4,
		SlotSet:     b[12]&FlagSlot != 0,
	}

	// A Rate 3/4 burst carries more, and the extra is the rest of the same
	// payload rather than a different field.
	blockLen := TextBlockLen
	if len(b) >= TextBlockAt-HeaderLen+TextRate34Len {
		blockLen = TextRate34Len
	}
	t.Block = append([]byte(nil), b[TextBlockAt-HeaderLen:TextBlockAt-HeaderLen+blockLen]...)
	return t, true
}

// IsFirst and IsLast read the flags field, which a text uses exactly as voice
// does.
func (t Text) IsFirst() bool { return t.Flags == 0x80dd }
func (t Text) IsLast() bool  { return t.Flags == 0x805e }
