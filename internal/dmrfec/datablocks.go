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
// The four-octet packet CRC at the end of the last block is solved as well —
// see [PacketCRC] for what it is, how it was found, and what it was checked
// against. It was the last thing between a composed message and the air.

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

// Rate12DataBytes is the user data a Rate 1/2 block carries: the ninety-six
// bits of a BPTC(196,96) information block, and all of it payload.
//
// **A short text never reaches Rate 3/4.** The six calibration messages of
// 2026-09-27 — one to four characters, group-addressed — went out as Rate 1/2
// unconfirmed blocks, data type 7, with no serial number and no CRC-9. The
// packet, its pad and the four-octet packet CRC are laid out exactly as they
// are for Rate 3/4, so everything below this point is shared.
const Rate12DataBytes = 12

// Rate12Blocks cuts a block stream into Rate 1/2 blocks.
//
// There is no control pair, so this is chunking with the length check that
// stops a short final block being invented. It exists as a named function
// rather than a slice expression so the Rate 1/2 and Rate 3/4 paths read the
// same way at the call site.
func Rate12Blocks(userData []byte) ([][]byte, error) {
	switch {
	case len(userData) == 0:
		return nil, fmt.Errorf("rate 1/2 blocks: no user data")
	case len(userData)%Rate12DataBytes != 0:
		return nil, fmt.Errorf("rate 1/2 blocks: %d octets of user data is not a multiple of %d",
			len(userData), Rate12DataBytes)
	}
	blocks := make([][]byte, 0, len(userData)/Rate12DataBytes)
	for i := 0; i < len(userData); i += Rate12DataBytes {
		blocks = append(blocks, userData[i:i+Rate12DataBytes:i+Rate12DataBytes])
	}
	return blocks, nil
}

// Rate12UserData concatenates Rate 1/2 blocks.
//
// **Nothing here can detect a missing or reordered block.** A Rate 1/2
// unconfirmed block carries no serial number and no CRC, so the packet CRC at
// the end is the only thing that notices. Check the result with
// [VerifyPacket] before trusting it.
func Rate12UserData(blocks [][]byte) ([]byte, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("rate 1/2 user data: no blocks")
	}
	out := make([]byte, 0, len(blocks)*Rate12DataBytes)
	for i, b := range blocks {
		if len(b) != Rate12DataBytes {
			return nil, fmt.Errorf("rate 1/2 user data: block %d is %d octets, want %d",
				i, len(b), Rate12DataBytes)
		}
		out = append(out, b...)
	}
	return out, nil
}

// PacketCRCBytes is the width of the packet CRC that ends the last block.
const PacketCRCBytes = 4

// PacketCRC computes the packet CRC over everything that precedes it in the
// user-data stream: the IP datagram and its pad octets, both block formats
// alike.
//
// It is CRC-32 with the polynomial 0x04C11DB7, not reflected, an initial value
// of zero and no output mask — **taken over the octets in swapped pairs**, 1,
// 0, 3, 2 and so on — and it is carried in the last four octets
// least-significant first. [JoinPacket] writes it and [VerifyPacket] checks it.
//
// **How it was found, 2026-09-27.** The first search tried this polynomial
// over this region and matched nothing, and neither it nor the second tried
// the octet order. The third stopped listing candidates and solved for the
// polynomial: for two same-length messages the initial value and the mask
// cancel, so the polynomial must divide D(x)·x³² + R(x), where D is the two
// messages XORed and R their CRCs XORed, and the greatest common divisor over
// several pairs *is* the polynomial if one exists. Over every region start,
// four octet orders, both reflections and four CRC byte orders, one
// arrangement gave a degree-32 result, from the seven same-length pairs in the
// group calibration capture — four of them independent, since four messages of
// one length and two of another give three and one. It appeared at region
// starts 0 and 2, because every sample opens 45 00 and those octets XOR away;
// of the usual four pairs of initial value and mask, only zero and zero
// reproduced all six samples, and only from start 0. `scripts/crc-solve.py`
// repeats the search from the fixture.
//
// **What it was checked against.** Ten Rate 3/4 private transmissions in three
// other captures, none used to find it, reproduce to the octet. And the one
// Rate 3/4 block in `ipsc-text-rate34-out.pcap` that fails its own CRC-9 fails
// this as well: the bits that differ are in the packet CRC itself.
//
// The input must be an even number of octets, which it always is when it came
// from whole blocks: both block sizes are even and so is the CRC. An odd
// length has no defined pairing and is refused rather than padded.
func PacketCRC(covered []byte) (uint32, error) {
	if len(covered)%2 != 0 {
		return 0, fmt.Errorf("packet crc: %d octets cannot be taken in pairs", len(covered))
	}
	var reg uint32
	for i := range len(covered) {
		reg ^= uint32(covered[i^1]) << 24
		for range 8 {
			if reg&0x80000000 != 0 {
				reg = reg<<1 ^ packetCRCPoly
			} else {
				reg <<= 1
			}
		}
	}
	return reg, nil
}

// packetCRCPoly is the generator of [PacketCRC], without its x³² term.
const packetCRCPoly = 0x04c11db7

// VerifyPacket checks the packet CRC that ends a reassembled user-data stream.
//
// For a Rate 1/2 transmission this is the only integrity check there is.
func VerifyPacket(userData []byte) error {
	if len(userData) < PacketCRCBytes {
		return fmt.Errorf("verify packet: %d octets cannot hold a %d-octet CRC", len(userData), PacketCRCBytes)
	}
	cut := len(userData) - PacketCRCBytes
	want, err := PacketCRC(userData[:cut])
	if err != nil {
		return fmt.Errorf("verify packet: %w", err)
	}
	if got := binary.LittleEndian.Uint32(userData[cut:]); got != want {
		return fmt.Errorf("verify packet: carried CRC %#08x, computed %#08x", got, want)
	}
	return nil
}

// PadOctets is how many pad octets a packet of payloadLen needs to fill
// blocks of blockBytes, leaving room for the packet CRC.
//
// **Derived, and it agrees with the data header seven times out of seven.**
// The pad count a header carries equalled blocks×blockBytes − payload − 4 for
// the three Rate 3/4 transmissions, padding 4, 14 and 4 across sixteen-octet
// blocks, and for the four distinct Rate 1/2 calibration headers, padding 8,
// 10, 4 and 2 across twelve-octet blocks. See [DataHeader].
func PadOctets(payloadLen, blocks, blockBytes int) int {
	return blocks*blockBytes - payloadLen - PacketCRCBytes
}

// BlocksFor is how many blocks of blockBytes a packet of payloadLen occupies
// once the packet CRC is allowed for.
func BlocksFor(payloadLen, blockBytes int) int {
	total := payloadLen + PacketCRCBytes
	return (total + blockBytes - 1) / blockBytes
}

// SplitPacket separates a reassembled user-data stream into the packet, its
// pad octets and the packet CRC. The CRC comes back as the value [PacketCRC]
// computes, read from its least-significant-first octets, and is not checked
// here; [VerifyPacket] does that.
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
	crc = binary.LittleEndian.Uint32(userData[len(userData)-PacketCRCBytes:])
	return payload, pad, crc, nil
}

// JoinPacket is the inverse: the payload, zero pad to fill the blocks, and the
// packet CRC computed over both.
func JoinPacket(payload []byte, blocks, blockBytes int) ([]byte, error) {
	pad := PadOctets(len(payload), blocks, blockBytes)
	if pad < 0 {
		return nil, fmt.Errorf("join packet: %d octets of payload and a %d-octet CRC do not fit in %d blocks of %d",
			len(payload), PacketCRCBytes, blocks, blockBytes)
	}
	out := make([]byte, 0, blocks*blockBytes)
	out = append(out, payload...)
	out = append(out, make([]byte, pad)...)
	crc, err := PacketCRC(out)
	if err != nil {
		return nil, fmt.Errorf("join packet: %w", err)
	}
	return binary.LittleEndian.AppendUint32(out, crc), nil
}
