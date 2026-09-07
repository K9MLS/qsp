package dmrfec

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
	if len(block) < 2 {
		return CSBK{}, false
	}
	return CSBK{
		Opcode:    block[0] & csbkOpcodeMask,
		FeatureID: block[1],
		LastBlock: block[0]&0x80 != 0,
	}, true
}
