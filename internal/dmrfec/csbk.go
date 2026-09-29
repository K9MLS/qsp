package dmrfec

import "fmt"

// Control Signalling Blocks, and telling a preamble from a command.
//
// # Why this exists
//
// One text message put two rows in the console's Last heard, and the more
// prominent of them carried no message. A hotspot sends **sixteen preamble
// CSBKs**, each with its own stream ID, over 1.87 seconds — then the data
// header and the content blocks share a single stream and arrive in 142 ms. The
// call tracker groups them correctly and there genuinely are two runs, because
// the preambles share nothing with each other or with the message.
//
// A preamble exists so receiving radios wake up. Nobody sent it. It should not
// be a row.
//
// # Identifying one by what it is rather than by how it arrives
//
// The first idea was a heuristic: data type 3, alone in a stream, is a
// preamble. That would also have hidden a radio check, a call alert and a
// **remote monitor** — a command that makes somebody's radio transmit without
// its operator knowing, which is arguably the one thing an administrator most
// needs to see.
//
// Figure 7.8 puts the opcode in the low six bits of the block's first octet and
// the Feature ID in the second. Reading the sixteen preambles out of
// testdata/hbp/hbp-text-preambles.pcap gives **opcode 61 with feature ID 0 on
// all sixteen, and no other opcode present**. So a preamble can be named
// exactly, and everything else keeps its row.
//
// TS 102 361-2 holds the table that would say what 61 is called. This project
// does not have it, so this file says what a preamble carries rather than what
// the number means — measured, and marked as measured.
const (
	// csbkOpcodeMask selects the CSBKO field, figure 7.8 octet 0 bits 5 to 0.
	csbkOpcodeMask = 0x3f

	// CSBKPreamble is the opcode every preamble in the capture carries.
	//
	// **Sixteen of sixteen, with no other opcode present.** It is not read
	// from a published table: see above.
	CSBKPreamble uint8 = 61

	// CSBKFeatureStandard is Feature ID 0, the standard feature set rather
	// than a manufacturer's extension. Checked alongside the opcode because
	// the same number under another vendor's Feature ID is a different
	// message entirely, and hiding somebody else's command by accident is the
	// failure this whole file exists to avoid.
	CSBKFeatureStandard uint8 = 0
)

// CSBK is the header of a Control Signalling Block.
type CSBK struct {
	// Opcode is the CSBKO field.
	Opcode uint8
	// FeatureID names the feature set the opcode belongs to.
	FeatureID uint8
	// LastBlock is the LB bit: whether this block ends a multi-block CSBK.
	LastBlock bool
	// DataFollows is a preamble's data-content bit, octet 2 bit 7: set when
	// the preamble wakes radios for a data transmission, clear when it wakes
	// them for control signalling such as a private call's setup. It is the
	// bit MMDVMHost reads to decide whether to multiply a preamble
	// (DMRCSBK.cpp, m_dataContent). Meaningless for any other opcode.
	DataFollows bool
}

// IsPreamble reports whether this block is a preamble rather than a command.
//
// **Both fields, never the opcode alone.** An opcode is only meaningful within
// its feature set, and treating 61 under a manufacturer's Feature ID as a
// preamble would hide a message this package has never seen and cannot judge.
func (c CSBK) IsPreamble() bool {
	return c.Opcode == CSBKPreamble && c.FeatureID == CSBKFeatureStandard
}

// CSBKOf reads the header out of a CSBK burst.
//
// It reports false for a burst that does not decode, which is a burst to
// record rather than to hide: **when this package cannot tell what something
// is, the console shows it.** Suppressing an unreadable block would be the one
// way this feature could make a remote monitor invisible.
func CSBKOf(burst []byte) (CSBK, bool) {
	payload, _, ok := DecodeBPTC(burst)
	if !ok {
		return CSBK{}, false
	}
	block := BurstBytesFrom(payload)
	if len(block) < 3 {
		return CSBK{}, false
	}
	return CSBK{
		Opcode:      block[0] & csbkOpcodeMask,
		FeatureID:   block[1],
		LastBlock:   block[0]&0x80 != 0,
		DataFollows: block[2]&csbkPreambleData != 0,
	}, true
}

// # Building a preamble, and its CRC
//
// A composed text has to open the way a radio's does, so QSP needs to build
// preambles as well as recognise them. Every one captured — sixteen from a
// hotspot's group text, 224 from a hotspot's private ones, seventeen distinct
// from a Motorola repeater — has the same twelve octets:
//
//	bd 00 c0 NN tt tt tt ss ss ss cc cc
//
// `bd` is the last-block bit over opcode 61; `00` is the standard feature set;
// the third octet is `0x80` for data to follow, with `0x40` added when the
// destination is a talkgroup (`c0` on every group preamble, `80` on every
// private one); NN is how many bursts are still to come after this one — the
// remaining preambles, the data header and every data block — counting down
// from 21 to 6 across the hotspot's sixteen; then the destination and the
// source, and the CRC.
//
// **The CRC is CRC-CCITT, initial value zero, output mask 0x5A5A**, over the
// first ten octets, stored most significant first. It reproduces every
// preamble in all three captures. It had been listed as unsolved after 128
// combinations. The mask is ETSI's: the standard's CRC-CCITT is inverted
// before masking, and 0xFFFF ⊕ 0xA5A5, the CSBK mask, is 0x5A5A. The same
// fold explains the data header's measured 0x3333 — see [DataHeaderCRC].
const (
	// CSBKBytes is the length of a CSBK information block.
	CSBKBytes = 12

	csbkLastBlock     = 0x80
	csbkPreambleData  = 0x80
	csbkPreambleGroup = 0x40
	csbkCRCMask       = 0x5a5a
)

// ccitt16 is CRC-CCITT, polynomial 0x1021, initial value zero, not reflected
// and unmasked. Each block type applies its own mask.
func ccitt16(b []byte) uint16 {
	var reg uint16
	for _, o := range b {
		reg ^= uint16(o) << 8
		for range 8 {
			if reg&0x8000 != 0 {
				reg = reg<<1 ^ 0x1021
			} else {
				reg <<= 1
			}
		}
	}
	return reg
}

// CSBKCRC is the check value over the first ten octets of a CSBK block.
func CSBKCRC(block []byte) (uint16, error) {
	if len(block) < CSBKBytes-2 {
		return 0, fmt.Errorf("csbk crc: %d octets, want at least %d", len(block), CSBKBytes-2)
	}
	return ccitt16(block[:CSBKBytes-2]) ^ csbkCRCMask, nil
}

// Preamble is what a preamble CSBK says: who a data transmission is for, who
// it is from, and how many bursts are still to come.
type Preamble struct {
	// BlocksToFollow counts every burst after this one: the preambles still
	// to send, the data header and the data blocks.
	BlocksToFollow uint8
	// To is a radio ID, or a talkgroup when Group is set.
	To, From uint32
	Group    bool
}

// BuildPreamble lays a preamble out as the twelve octets a burst carries.
func BuildPreamble(p Preamble) ([]byte, error) {
	if p.To > 0xffffff || p.From > 0xffffff {
		return nil, fmt.Errorf("build preamble: %d and %d are not both 24-bit identifiers", p.To, p.From)
	}
	b := make([]byte, CSBKBytes)
	b[0] = csbkLastBlock | CSBKPreamble
	b[1] = CSBKFeatureStandard
	b[2] = csbkPreambleData
	if p.Group {
		b[2] |= csbkPreambleGroup
	}
	b[3] = p.BlocksToFollow
	b[4], b[5], b[6] = byte(p.To>>16), byte(p.To>>8), byte(p.To)
	b[7], b[8], b[9] = byte(p.From>>16), byte(p.From>>8), byte(p.From)
	crc, err := CSBKCRC(b)
	if err != nil {
		return nil, fmt.Errorf("build preamble: %w", err)
	}
	b[10], b[11] = byte(crc>>8), byte(crc)
	return b, nil
}

// ParsePreamble reads a preamble block, refusing one whose CRC does not
// verify or which is not a preamble at all.
func ParsePreamble(block []byte) (Preamble, error) {
	if len(block) != CSBKBytes {
		return Preamble{}, fmt.Errorf("parse preamble: %d octets, want %d", len(block), CSBKBytes)
	}
	want, err := CSBKCRC(block)
	if err != nil {
		return Preamble{}, fmt.Errorf("parse preamble: %w", err)
	}
	if got := uint16(block[10])<<8 | uint16(block[11]); got != want {
		return Preamble{}, fmt.Errorf("parse preamble: carried CRC %#04x, computed %#04x", got, want)
	}
	c := CSBK{Opcode: block[0] & csbkOpcodeMask, FeatureID: block[1], LastBlock: block[0]&csbkLastBlock != 0}
	if !c.IsPreamble() {
		return Preamble{}, fmt.Errorf("parse preamble: opcode %d, feature %d is not a preamble", c.Opcode, c.FeatureID)
	}
	return Preamble{
		BlocksToFollow: block[3],
		To:             uint32(block[4])<<16 | uint32(block[5])<<8 | uint32(block[6]),
		From:           uint32(block[7])<<16 | uint32(block[8])<<8 | uint32(block[9]),
		Group:          block[2]&csbkPreambleGroup != 0,
	}, nil
}
