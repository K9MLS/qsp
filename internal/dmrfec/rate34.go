package dmrfec

import "fmt"

// Rate 3/4 confirmed data blocks: the thing a text message is actually made of.
//
// # Why this file exists
//
// A text long enough to be worth sending does not fit in the twelve-octet
// blocks BPTC(196,96) carries. It is broken into Rate 3/4 blocks instead, and
// QSP refused every one of them from the day the text path was written. The
// preamble crossed the bridge, the data header crossed, and no content ever
// did.
//
// # The block, which is measured rather than assumed
//
// ETSI TS 102 361-1 clause 8.2.2.2 gives a confirmed data block as sixteen
// octets of user data and two of control: a seven-bit block serial number and
// a nine-bit CRC. **IP Site Connect delivers those eighteen octets with the
// user data first and the control pair last**, which is the opposite of the
// order figure 8.8 draws, and this is not a reading — it is what the captures
// say:
//
//   - Taking the first sixteen octets of the six blocks of one transmission in
//     testdata/ipsc/ipsc-text-rate34.pcap and concatenating them gives a
//     well-formed IPv4 datagram: total length 88, protocol 17, source
//     0c 2f cd ee and destination 0c 30 25 ad, which are Motorola's radio-IP
//     encoding of the two radio IDs in the envelope. Inside it is a UDP
//     datagram on port 4007 whose UTF-16 payload is a sentence the operator
//     typed. No other offset produces any of that.
//   - The top seven bits of the trailing pair read 0, 1, 2, 3, 4, 5 across
//     those six blocks, and 0, 1, 2, 3 across a four-block one.
//   - [CRC9] over the sixteen user octets followed by the seven-bit serial
//     reproduces the low nine bits of the trailing pair for **42 blocks out of
//     42** across two captures.
//
// # The CRC-9 here is not the one clause B.3.11 describes
//
// B.3.11 puts the serial number first and adds an inversion polynomial. That
// arrangement matches none of the 42 blocks under any nine-bit generator; a
// search over all 256 of them, seven message orderings and both inversions
// found exactly one combination that matches every block, and it is the
// generator B.3.11 gives with the message in the order IP Site Connect
// presents it and no inversion. Two hundred and fifty-six candidate readings
// were tried because the alternative was arguing about which one a 2005 first
// edition meant.
//
// So the block as it arrives is a plain trailing-CRC codeword: 135 bits
// protected by the nine that follow them.
const (
	// Rate34BlockBytes is the information a Rate 3/4 burst carries: 144 bits,
	// where a BPTC block carries 96. A datagram carrying one is 60 bytes
	// where a datagram carrying a BPTC block is 54.
	Rate34BlockBytes = 18
	// Rate34DataBytes is the user data within it. The remaining two octets
	// are the block serial number and the CRC-9.
	Rate34DataBytes = 16
	// Rate34ControlBytes is the size of that control pair.
	Rate34ControlBytes = Rate34BlockBytes - Rate34DataBytes

	// crc9Generator is G9(x) = x⁹ + x⁶ + x⁴ + x³ + 1, ETSI TS 102 361-1
	// clause B.3.11 formula (24), with the x⁹ term included so a set bit 9
	// selects the reduction.
	crc9Generator = 0x259
	// crc9Mask keeps the nine bits the field holds.
	crc9Mask = 0x1ff
	// serialBits is the width of the block serial number.
	serialBits = 7
)

// CRC9 computes the nine-bit check value a confirmed data block carries.
//
// The message is the user data followed by the seven-bit serial number, most
// significant bit first throughout, augmented with nine zero bits and reduced
// modulo [crc9Generator]. There is no inversion: see the note above on why
// this differs from clause B.3.11 as written.
func CRC9(data []byte, serial uint8) uint16 {
	reg := uint16(0)
	push := func(bit uint16) {
		reg = reg<<1 | bit&1
		if reg&0x200 != 0 {
			reg ^= crc9Generator
		}
	}
	for _, o := range data {
		for i := 7; i >= 0; i-- {
			push(uint16(o >> uint(i)))
		}
	}
	for i := serialBits - 1; i >= 0; i-- {
		push(uint16(serial >> uint(i)))
	}
	for i := 0; i < 9; i++ {
		push(0)
	}
	return reg & crc9Mask
}

// Rate34Order names which end of a Rate 3/4 block carries its control pair.
//
// **This exists because the two ends of the bridge disagree and no capture
// settles it.** IP Site Connect delivers the control pair last, proved above.
// ETSI figure 8.8 draws it first, and a hotspot decodes what ETSI says. Which
// of those goes into a burst on air is the one step of the text path that is
// reasoned rather than measured, so it is named, reported and changeable in
// one line rather than buried in a slice expression.
type Rate34Order uint8

const (
	// Rate34OrderUnknown means the CRC-9 verified for neither arrangement.
	// An unconfirmed block, which carries eighteen octets of user data and no
	// control pair at all, reads this way and is perfectly valid.
	Rate34OrderUnknown Rate34Order = iota
	// Rate34ControlLast is the arrangement IP Site Connect uses.
	Rate34ControlLast
	// Rate34ControlFirst is the arrangement ETSI figure 8.8 draws.
	Rate34ControlFirst
)

func (o Rate34Order) String() string {
	switch o {
	case Rate34ControlLast:
		return "control-last"
	case Rate34ControlFirst:
		return "control-first"
	default:
		return "unknown"
	}
}

// Rate34AirOrder is the arrangement QSP writes into a burst it transmits.
//
// It is [Rate34ControlFirst] because clause 8.2.2.2 is the clause that
// describes the block's layout, and MMDVMHost at the far end implements the
// standard. **Nothing has measured it**: no capture anywhere holds a Rate 3/4
// burst as it goes over the air. [DecodeRate34Burst] reports the arrangement
// it actually finds, so one text from a hotspot settles this from the journal
// rather than from an argument.
const Rate34AirOrder = Rate34ControlFirst

// Rate34OrderOf reports which arrangement of an eighteen-octet block has a
// CRC-9 that verifies, or [Rate34OrderUnknown] if neither does.
func Rate34OrderOf(block []byte) Rate34Order {
	if len(block) != Rate34BlockBytes {
		return Rate34OrderUnknown
	}
	if serial, crc, ok := rate34Control(block, Rate34ControlLast); ok &&
		CRC9(block[:Rate34DataBytes], serial) == crc {
		return Rate34ControlLast
	}
	if serial, crc, ok := rate34Control(block, Rate34ControlFirst); ok &&
		CRC9(block[Rate34ControlBytes:], serial) == crc {
		return Rate34ControlFirst
	}
	return Rate34OrderUnknown
}

// Rate34Serial returns the block serial number of a block in the order IP Site
// Connect delivers it, and reports whether its CRC-9 verifies.
//
// A block whose CRC does not verify is still carried: QSP relays a payload it
// does not interpret, and an unconfirmed block has no CRC to check. The boolean
// is for the journal, not for a decision to drop.
func Rate34Serial(block []byte) (serial uint8, verified bool) {
	s, crc, ok := rate34Control(block, Rate34ControlLast)
	if !ok {
		return 0, false
	}
	return s, CRC9(block[:Rate34DataBytes], s) == crc
}

// rate34Control splits the control pair out of a block held in the named
// arrangement. The pair is two octets in both: the serial number occupies bits
// 7 to 1 of the first and the CRC-9 is the remaining nine bits.
func rate34Control(block []byte, order Rate34Order) (serial uint8, crc uint16, ok bool) {
	if len(block) != Rate34BlockBytes {
		return 0, 0, false
	}
	at := 0
	if order == Rate34ControlLast {
		at = Rate34DataBytes
	}
	pair := uint16(block[at])<<8 | uint16(block[at+1])
	return uint8(pair >> 9), pair & crc9Mask, true
}

// Rate34Rotate moves the control pair from one end of a block to the other,
// which is the whole of the difference between the two arrangements.
//
// It is its own inverse only in the sense that applying it twice returns the
// original block; the caller has to know which way round it started.
func Rate34Rotate(block []byte, from Rate34Order) ([]byte, error) {
	if len(block) != Rate34BlockBytes {
		return nil, fmt.Errorf("dmrfec: a Rate 3/4 block is %d octets, want %d",
			len(block), Rate34BlockBytes)
	}
	out := make([]byte, 0, Rate34BlockBytes)
	if from == Rate34ControlLast {
		out = append(out, block[Rate34DataBytes:]...)
		out = append(out, block[:Rate34DataBytes]...)
		return out, nil
	}
	out = append(out, block[Rate34ControlBytes:]...)
	out = append(out, block[:Rate34ControlBytes]...)
	return out, nil
}

// BuildRate34Burst assembles a 33-byte DMR burst carrying one Rate 3/4 block.
//
// The block goes in as IP Site Connect delivers it — user data first — and is
// rearranged to [Rate34AirOrder] on the way. That is the same division of
// labour BuildDataBurstFromBlock uses for the twelve-octet case: callers hold
// blocks in one arrangement and this package owns what goes on air.
func BuildRate34Burst(colourCode uint8, block []byte) ([]byte, error) {
	if len(block) != Rate34BlockBytes {
		return nil, fmt.Errorf("dmrfec: a Rate 3/4 block is %d octets, want %d",
			len(block), Rate34BlockBytes)
	}
	air := block
	if Rate34AirOrder != Rate34ControlLast {
		rotated, err := Rate34Rotate(block, Rate34ControlLast)
		if err != nil {
			return nil, err
		}
		air = rotated
	}
	coded, ok := EncodeTrellis(air)
	if !ok {
		return nil, fmt.Errorf("dmrfec: Rate 3/4 encoding refused %d octets", len(air))
	}
	return dataBurstFromCoded(colourCode, DataTypeRate34, coded)
}

// DecodeRate34Burst recovers the block from a Rate 3/4 burst, in the order IP
// Site Connect uses, together with the arrangement it was actually found in.
//
// **The arrangement is measured per burst rather than assumed.** A block whose
// CRC-9 verifies one way round says so, and that is the only evidence this
// project will ever get about what a hotspot puts on air short of somebody
// keying a radio. When neither verifies — an unconfirmed block, or a burst with
// an error in it — the burst is still returned, read as [Rate34AirOrder], and
// the caller is told the order is unknown.
func DecodeRate34Burst(burst []byte) (block []byte, order Rate34Order, ok bool) {
	air, ok := DecodeTrellis(burst)
	if !ok {
		return nil, Rate34OrderUnknown, false
	}
	order = Rate34OrderOf(air)
	found := order
	if found == Rate34OrderUnknown {
		found = Rate34AirOrder
	}
	if found == Rate34ControlLast {
		return air, order, true
	}
	link, err := Rate34Rotate(air, Rate34ControlFirst)
	if err != nil {
		return nil, Rate34OrderUnknown, false
	}
	return link, order, true
}
