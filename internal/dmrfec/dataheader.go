package dmrfec

import (
	"encoding/binary"
	"fmt"
)

// The confirmed data header that precedes a text message's blocks.
//
// Measured from three complete transmissions in
// testdata/ipsc/ipsc-text-rate34.pcap and
// testdata/ipsc/ipsc-text-rate34-out.pcap, where the burst carrying DMR data
// type 6 holds twelve octets:
//
//	43 44 30 25 ad 2f cd ee 86 58 fb aa    six blocks, four pad
//	43 4e 30 25 ad 2f cd ee 84 68 44 03    four blocks, fourteen pad
//	43 44 30 25 ad 2f cd ee 84 38 f1 6e    four blocks, four pad
//
// Octet 1 is the field that settles the layout: its low nibble equalled
// blocks×16 − payload − 4 in all three, which is 4, 14 and 4 — so it is the
// pad-octet count beside a service access point of 4, IP based packet data.
// That the payload is an IPv4 datagram and the header says so independently is
// the kind of agreement this project treats as a measurement rather than a
// reading.
//
// # The CRC-16 mask is 0x3333, and ETSI says 0xCCCC
//
// A CRC-16-CCITT over the first ten octets, initial value zero, no reflection
// either way, masked with **0x3333**, reproduces the stored value in all three
// headers. Clause B.3.9 gives 0xCCCC for a data header.
//
// A hundred and twenty-eight combinations were tried — four polynomials, both
// initial values, both reflections, eight masks including 0xCCCC, both stored
// byte orders — and exactly one matches all three. This is the same shape as
// the CRC-9 note in [rate34.go]: the standard's arrangement matched nothing and
// the wire settled it. Recorded rather than argued about.
//
// **Later, 2026-09-28: the wire and ETSI agree after all.** The standard's
// CRC-CCITT is inverted before its mask is applied, and 0xFFFF ⊕ 0xCCCC is
// 0x3333. So 0xCCCC applied to the uninverted value matched nothing, and the
// 0x3333 the search found is that same mask with the inversion folded in. The
// preamble CSBK's CRC, left open after its own search, is ETSI's 0xA5A5
// folded the same way — see [CSBKCRC].
// The measured constant stays, because it is what the wire carries; what
// changes is that it is no longer a disagreement with the standard.
const (
	// dataHeaderBytes is the length of the header block.
	dataHeaderBytes = 12
	// dataHeaderCRCMask is measured. See the note above.
	dataHeaderCRCMask = 0x3333
	// dpfConfirmedData and dpfUnconfirmedData are the Data Packet Formats
	// this project has seen. Every private text capture is confirmed; every
	// group text in the 2026-09-27 calibration capture is unconfirmed.
	dpfConfirmedData   = 0x3
	dpfUnconfirmedData = 0x2
	// headerBitA marks a header that requests a response.
	headerBitA = 0x40
	// headerBitGroup marks a destination that is a talkgroup.
	headerBitGroup = 0x80
	// headerBitFull marks a full message rather than a continuation.
	headerBitFull = 0x80
	// fragmentLast is the fragment sequence number of a final fragment.
	fragmentLast = 0x8

	// SAPIPPacketData is the service access point of IP based packet data,
	// which is what a text message is carried in.
	SAPIPPacketData = 0x4
)

// DataHeader is a confirmed data header.
type DataHeader struct {
	// To and From are radio IDs, or a talkgroup in To when Group is set.
	To, From uint32
	// Group says the destination is a talkgroup. Measured since the
	// 2026-09-27 calibration capture, and since 2026-09-28 a composed group
	// header codes to the hotspot's own burst bit for bit.
	Group bool
	// Response asks the far end to acknowledge. Set in all three captures.
	Response bool
	// SAP is the service access point; [SAPIPPacketData] for text.
	SAP uint8
	// Blocks is how many data blocks follow, one to fifteen.
	Blocks uint8
	// Pad is the pad-octet count, which [PadOctets] derives.
	Pad uint8
	// SendSeq is the three-bit send sequence number. It varied across the
	// private captures — 5, 6 and 3 — and read zero in every group one, and
	// nothing here depends on its value, so QSP owns it as a rolling counter.
	SendSeq uint8
	// Confirmed says each block carries a serial number and a CRC-9, which
	// is what a Rate 3/4 confirmed packet does. An unconfirmed packet is
	// Rate 1/2 blocks of plain user data, which is how every captured group
	// text travelled.
	Confirmed bool
}

// DataHeaderCRC is the check value over the first ten octets of a header.
func DataHeaderCRC(head []byte) (uint16, error) {
	if len(head) < dataHeaderBytes-2 {
		return 0, fmt.Errorf("data header CRC: %d octets, want at least %d", len(head), dataHeaderBytes-2)
	}
	return ccitt16(head[:dataHeaderBytes-2]) ^ dataHeaderCRCMask, nil
}

// BuildDataHeader lays a header out as the twelve octets a burst carries.
func BuildDataHeader(h DataHeader) ([]byte, error) {
	switch {
	case h.Blocks == 0 || h.Blocks > 0x0f:
		return nil, fmt.Errorf("data header: %d blocks to follow is outside one to fifteen", h.Blocks)
	case h.Pad > 0x0f:
		return nil, fmt.Errorf("data header: a pad count of %d does not fit four bits", h.Pad)
	case h.SAP > 0x0f:
		return nil, fmt.Errorf("data header: a service access point of %d does not fit four bits", h.SAP)
	case h.SendSeq > 0x7:
		return nil, fmt.Errorf("data header: a send sequence of %d does not fit three bits", h.SendSeq)
	case h.To > 0xffffff || h.From > 0xffffff:
		return nil, fmt.Errorf("data header: %d and %d are not both 24-bit identifiers", h.To, h.From)
	}

	out := make([]byte, dataHeaderBytes)
	out[0] = dpfUnconfirmedData
	if h.Confirmed {
		out[0] = dpfConfirmedData
	}
	if h.Response {
		out[0] |= headerBitA
	}
	if h.Group {
		out[0] |= headerBitGroup
	}
	out[1] = h.SAP<<4 | h.Pad
	out[2], out[3], out[4] = byte(h.To>>16), byte(h.To>>8), byte(h.To)
	out[5], out[6], out[7] = byte(h.From>>16), byte(h.From>>8), byte(h.From)
	out[8] = headerBitFull | h.Blocks
	// **Octet 9 belongs to a confirmed packet only.** Every confirmed
	// private header carries a send sequence and the fragment-last bit —
	// 0x58, 0x68, 0x38 — and every unconfirmed group header reads 0x00. An
	// unconfirmed packet has no acknowledgement to sequence, so there is
	// nothing for the field to say, and writing 0x08 there produced a header
	// that differed from the wire in exactly one octet.
	if h.Confirmed {
		out[9] = h.SendSeq<<4 | fragmentLast
	}

	crc, err := DataHeaderCRC(out)
	if err != nil {
		return nil, err
	}
	binary.BigEndian.PutUint16(out[dataHeaderBytes-2:], crc)
	return out, nil
}

// ParseDataHeader reads a header back, and refuses one whose CRC disagrees.
func ParseDataHeader(block []byte) (DataHeader, error) {
	if len(block) != dataHeaderBytes {
		return DataHeader{}, fmt.Errorf("data header: %d octets, want %d", len(block), dataHeaderBytes)
	}
	crc, err := DataHeaderCRC(block)
	if err != nil {
		return DataHeader{}, err
	}
	if stored := binary.BigEndian.Uint16(block[dataHeaderBytes-2:]); stored != crc {
		return DataHeader{}, fmt.Errorf("data header: CRC is %#04x, computed %#04x", stored, crc)
	}
	dpf := block[0] & 0x0f
	if dpf != dpfConfirmedData && dpf != dpfUnconfirmedData {
		return DataHeader{}, fmt.Errorf(
			"data header: data packet format %#x is neither a confirmed (%#x) nor an unconfirmed (%#x) packet",
			dpf, dpfConfirmedData, dpfUnconfirmedData)
	}
	return DataHeader{
		To:        uint32(block[2])<<16 | uint32(block[3])<<8 | uint32(block[4]),
		From:      uint32(block[5])<<16 | uint32(block[6])<<8 | uint32(block[7]),
		Group:     block[0]&headerBitGroup != 0,
		Response:  block[0]&headerBitA != 0,
		SAP:       block[1] >> 4,
		Blocks:    block[8] & 0x0f,
		Pad:       block[1] & 0x0f,
		SendSeq:   block[9] >> 4 & 0x7,
		Confirmed: dpf == dpfConfirmedData,
	}, nil
}
