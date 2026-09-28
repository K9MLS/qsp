package dmrfec

import (
	"encoding/binary"
	"fmt"
)

// Rate 3/4 confirmed data blocks, assembled and taken apart.
//
// [rate34.go] measured what one block is: sixteen octets of user data, a
// seven-bit block serial number and a nine-bit CRC, with IP Site Connect
// putting the control pair last. This file is the other half of that — turning
// a packet into blocks and blocks back into a packet — and it exists because
// until now QSP has only ever *carried* text. Everything in
// `internal/ipscbridge` translates a message somebody else composed; nothing
// composed one. See
// [ADR-0067](../../docs/adr/ADR-0067-qsp-originates-a-text-message.md).
//
// # What is measured and what is not
//
// The block layout, the serial numbering and the CRC-9 are measured, over
// forty-two blocks in two captures, and [CRC9] reproduces every one of them.
// The pad count is derived below and agrees with the data header in three
// transmissions out of three.
//
// **The four-octet packet CRC at the end of the last block is not solved.** It
// is carried, never computed. [ErrPacketCRCUnverified] and the note on
// [SplitPacket] say what was searched.

// Rate34Blocks cuts a block stream into Rate 3/4 confirmed data blocks.
//
// The input is the whole user-data stream a transmission carries — the packet,
// its pad octets and the four-octet packet CRC — and its length must be a
// multiple of [Rate34DataBytes], because a block carries exactly sixteen
// octets of it and a short final block is not a thing the format has.
//
// Each block comes back as eighteen octets in the order `order` names, so the
// result can be compared against a capture without the caller knowing which
// end the control pair lives on.
func Rate34Blocks(userData []byte, order Rate34Order) ([][]byte, error) {
	switch {
	case len(userData) == 0:
		return nil, fmt.Errorf("rate 3/4 blocks: no user data")
	case len(userData)%Rate34DataBytes != 0:
		return nil, fmt.Errorf("rate 3/4 blocks: %d octets of user data is not a multiple of %d",
			len(userData), Rate34DataBytes)
	}
	count := len(userData) / Rate34DataBytes
	// A seven-bit serial number wraps at 128. A message that long is not
	// something this project has seen and the failure would be silent, so it
	// is refused rather than truncated.
	if count > 1<<serialBits {
		return nil, fmt.Errorf("rate 3/4 blocks: %d blocks exceeds the %d a seven-bit serial can number",
			count, 1<<serialBits)
	}

	blocks := make([][]byte, 0, count)
	for i := range count {
		data := userData[i*Rate34DataBytes : (i+1)*Rate34DataBytes]
		serial := uint8(i)
		// The serial occupies the top seven bits of the pair and the CRC-9
		// the low nine: block 1 of the captured message reads 0x032c, which
		// is serial 1 and CRC 0x12c.
		pair := uint16(serial)<<9 | CRC9(data, serial)

		block := make([]byte, Rate34BlockBytes)
		switch order {
		case Rate34ControlLast:
			copy(block, data)
			binary.BigEndian.PutUint16(block[Rate34DataBytes:], pair)
		case Rate34ControlFirst:
			binary.BigEndian.PutUint16(block, pair)
			copy(block[Rate34ControlBytes:], data)
		default:
			return nil, fmt.Errorf("rate 3/4 blocks: %s is not an arrangement to build in", order)
		}
		blocks = append(blocks, block)
	}
	return blocks, nil
}

// Rate34UserData is the inverse: it verifies each block and concatenates the
// user data.
//
// Every block's CRC-9 must verify and the serial numbers must run from zero
// with no gap. A transmission missing a block in the middle is a real thing —
// captures hold several — and returning the concatenation anyway would produce
// a packet that parses to the wrong bytes rather than failing.
func Rate34UserData(blocks [][]byte, order Rate34Order) ([]byte, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("rate 3/4 user data: no blocks")
	}
	out := make([]byte, 0, len(blocks)*Rate34DataBytes)
	for i, block := range blocks {
		if len(block) != Rate34BlockBytes {
			return nil, fmt.Errorf("rate 3/4 user data: block %d is %d octets, want %d",
				i, len(block), Rate34BlockBytes)
		}
		var data []byte
		var pair uint16
		switch order {
		case Rate34ControlLast:
			data = block[:Rate34DataBytes]
			pair = binary.BigEndian.Uint16(block[Rate34DataBytes:])
		case Rate34ControlFirst:
			pair = binary.BigEndian.Uint16(block[:Rate34ControlBytes])
			data = block[Rate34ControlBytes:]
		default:
			return nil, fmt.Errorf("rate 3/4 user data: %s is not an arrangement to read", order)
		}
		serial := uint8(pair >> 9)
		if int(serial) != i {
			return nil, fmt.Errorf("rate 3/4 user data: block %d carries serial %d; a block is missing or out of order",
				i, serial)
		}
		if want := CRC9(data, serial); pair&crc9Mask != want {
			return nil, fmt.Errorf("rate 3/4 user data: block %d CRC-9 is %#03x, computed %#03x",
				i, pair&crc9Mask, want)
		}
		out = append(out, data...)
	}
	return out, nil
}

// PacketCRCBytes is the width of the packet CRC that ends the last block.
const PacketCRCBytes = 4

// ErrPacketCRCUnverified is returned by [PacketCRC] because the four octets
// that end a confirmed data packet have not been reproduced.
//
// **What was searched, so nobody repeats it.** Three complete transmissions
// hold the value. Six polynomials — the IEEE CRC-32 of ETSI clause B.3.12 and
// five others — against both initial values, both bit reflections, both
// output masks and both stored byte orders, over eight candidate regions: the
// IP datagram alone, the datagram with its pad octets, the datagram with the
// data header prepended at twelve and at ten octets, the UDP datagram, the UDP
// payload, and the whole user-data stream less the CRC itself. Three thousand
// and seventy-two combinations, no match on any of the three.
//
// So it is carried rather than computed, and [ADR-0067] phase 2 cannot put an
// originated message on the air until it is solved. That is the honest state
// of it: the encoder is complete except for four octets, and a value guessed
// there would be a message a radio silently refuses.
var ErrPacketCRCUnverified = fmt.Errorf(
	"the four-octet packet CRC of a confirmed data packet is not solved; see ErrPacketCRCUnverified")

// PacketCRC would compute the packet CRC and does not.
//
// It exists so the gap is a compile-time fact rather than a comment somebody
// has to notice. §7 forbids a stub that claims success, and a function
// returning a plausible wrong number is exactly that.
func PacketCRC([]byte) (uint32, error) {
	return 0, ErrPacketCRCUnverified
}

// PadOctets is how many pad octets a packet of payloadLen needs to fill
// blocks, leaving room for the packet CRC.
//
// **Derived, and it agrees with the data header three times out of three.**
// The pad count a header carries equalled blocks×16 − payload − 4 for a
// six-block message padding four, a four-block message padding fourteen and a
// four-block message padding four. See [DataHeader].
func PadOctets(payloadLen, blocks int) int {
	return blocks*Rate34DataBytes - payloadLen - PacketCRCBytes
}

// BlocksFor is how many Rate 3/4 blocks a packet of payloadLen occupies once
// the packet CRC is allowed for.
func BlocksFor(payloadLen int) int {
	total := payloadLen + PacketCRCBytes
	return (total + Rate34DataBytes - 1) / Rate34DataBytes
}

// SplitPacket separates a reassembled user-data stream into the packet, its
// pad octets and the packet CRC.
//
// payloadLen is how long the packet is, which the caller knows from the packet
// itself — for the text service that is the IPv4 total length field. It is a
// parameter rather than something read here because this package does not know
// what a packet is, and [ADR-0037]'s rule that a bridge does not read the
// payload is why it stays that way.
func SplitPacket(userData []byte, payloadLen int) (payload, pad []byte, crc uint32, err error) {
	if payloadLen < 0 || payloadLen+PacketCRCBytes > len(userData) {
		return nil, nil, 0, fmt.Errorf("split packet: a payload of %d and a %d-octet CRC do not fit in %d octets",
			payloadLen, PacketCRCBytes, len(userData))
	}
	payload = userData[:payloadLen]
	pad = userData[payloadLen : len(userData)-PacketCRCBytes]
	crc = binary.BigEndian.Uint32(userData[len(userData)-PacketCRCBytes:])
	return payload, pad, crc, nil
}

// JoinPacket is the inverse, and it takes the packet CRC as an argument for
// the reason [PacketCRC] gives: nothing here can compute it.
func JoinPacket(payload []byte, blocks int, crc uint32) ([]byte, error) {
	pad := PadOctets(len(payload), blocks)
	if pad < 0 {
		return nil, fmt.Errorf("join packet: %d octets of payload and a %d-octet CRC do not fit in %d blocks",
			len(payload), PacketCRCBytes, blocks)
	}
	out := make([]byte, 0, blocks*Rate34DataBytes)
	out = append(out, payload...)
	out = append(out, make([]byte, pad)...)
	return binary.BigEndian.AppendUint32(out, crc), nil
}
