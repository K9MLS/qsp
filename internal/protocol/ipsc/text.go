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
	// TextSlotTypeAt is the DMR Slot Type of a 54-byte burst: colour code
	// high, data type low. Use [TextSlotTypeFor] rather than this constant,
	// because a Rate 3/4 burst carries it six bytes further along.
	TextSlotTypeAt = 51
	// TextRate34Len is the length of the block a Rate 3/4 burst carries: 144
	// bits where BPTC carries 96. Those datagrams are 60 bytes where the rest
	// are 54, and the six-byte difference is the whole of it.
	//
	// **This read 22 for eighteen patches.** Nothing had ever exercised it,
	// because ConvertText refused any block that was not twelve octets and
	// dropped it, so the wrong length was carried by a path that never ran.
	// Twenty-two octets from byte 38 runs to byte 59: the eighteen real ones
	// followed by the zero, the Slot Type and both tail bytes, which is
	// envelope handed out as message content.
	TextRate34Len = 18
	// TextRate34Total is the length of a datagram carrying one, measured
	// across 42 of them in testdata/ipsc/ipsc-text-rate34.pcap and
	// testdata/ipsc/ipsc-text.pcap. Byte 30 reads Rate 3/4 in every one.
	TextRate34Total = 60
)

// TextBlockLenFor gives the length of the information block in a text burst
// whose datagram is n bytes long.
//
// **The datagram length is the discriminator, not the data type.** Byte 30
// agrees on all 42 captured Rate 3/4 frames, but a truncated datagram with a
// plausible byte 30 would make a length read from it an over-read, and this
// function is what stands between a short datagram and a slice out of range.
func TextBlockLenFor(datagramLen int) int {
	if datagramLen >= TextRate34Total {
		return TextRate34Len
	}
	return TextBlockLen
}

// TextSlotTypeFor gives the offset of the Slot Type in such a datagram.
//
// It follows the block: one zero octet, then the Slot Type. On a 54-byte burst
// that is byte 51, which is where ADR-0045 measured it; on a 60-byte burst it
// is byte 57, and reading 51 there gives four bits of somebody's message where
// the colour code should be.
func TextSlotTypeFor(datagramLen int) int {
	return TextBlockAt + TextBlockLenFor(datagramLen) + 1
}

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
	// Block is the twelve octets at TextBlockAt, or the eighteen octets of a
	// Rate 3/4 burst. **Carried, not interpreted.**
	//
	// A Rate 3/4 block is sixteen octets of user data followed by a seven-bit
	// serial number and a nine-bit CRC; dmrfec.Rate34Serial reads them and
	// dmrfec.CRC9 checks them. This package does neither, because a bridge
	// carries a payload it does not understand.
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
	datagramLen := HeaderLen + len(b)
	slotTypeAt := TextSlotTypeFor(datagramLen) - HeaderLen
	if len(b) < slotTypeAt+1 {
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
		ColourCode:  b[slotTypeAt] >> 4,
		SlotSet:     b[12]&FlagSlot != 0,
	}

	// A Rate 3/4 burst carries a longer block, and the extra six octets are
	// more of the same payload rather than a different field.
	blockLen := TextBlockLenFor(datagramLen)
	at := TextBlockAt - HeaderLen
	t.Block = append([]byte(nil), b[at:at+blockLen]...)
	return t, true
}

// IsFirst and IsLast read the flags field, which a text uses exactly as voice
// does.
func (t Text) IsFirst() bool { return t.Flags == 0x80dd }
func (t Text) IsLast() bool  { return t.Flags == 0x805e }
