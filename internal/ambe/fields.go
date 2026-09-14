// Package ambe builds AMBE-3000F packets and refuses to build malformed ones.
//
// # Why this package exists
//
// On 2026-09-14 a probe sent `61 00 02 00 0a 21` to the operator's DVstick 30:
// field 0x0A with one argument byte. Field 0x0A is PKT_RATEP and takes twelve.
// The chip was told twelve bytes were coming, given one, consumed the head of
// the next packet as the remainder, and sat mid-field permanently. **Recovery
// was a physical unplug** — a soft reset could not do it and neither could
// detaching and re-attaching the USB device in ESXi.
//
// The gate written after that incident checked that the constants were right.
// They were right. The call site passed the wrong one, and every constant
// stayed correct, so the gate passed too. **A gate that proves a constant is
// not a gate that proves the call site**, which is why the only way to emit a
// packet here is through a builder that consults this table.
//
// # Source
//
// AMBE-3000F Vocoder Chip Users Manual, version 3.7, October 2016. Section
// references are to its printed page numbers. The variant matters: the
// operator's board answers PKT_PRODID with "AMBE3000F" and PKT_VERSTRING with
// a version beginning V121, and the AMBE-3000R manual differs from this one in
// places that touch this code — SK_ENABLE and TX_RQST exist only on the F, the
// RESET pin is an I/O on the F, and on the F the echo canceller and echo
// suppressor are documented as unsupported in packet mode.
//
// Where the manual contradicts itself, the contradiction is recorded in the
// table rather than resolved silently. See Field.Disputed.
package ambe

// The packet header. A start byte, a big-endian length, and a type.
//
// **The length counts the field bytes and the parity bytes, and excludes the
// four header bytes** (§6.5.2, printed page 59). The manual states it twice
// and all four worked examples in §6.11 agree with it; see
// testdata/ambe/manual-examples.hex.
const (
	StartByte = 0x61

	TypeControl = 0x00 // configuration and queries
	TypeChannel = 0x01 // compressed audio, the AMBE side
	TypeSpeech  = 0x02 // uncompressed PCM, the codec side
)

// Variable marks a field whose data length is carried inside its own data
// rather than fixed by the field identifier.
const Variable = -1

// Direction records which way a field may travel, per Table 32.
type Direction uint8

// The three directions the manual distinguishes.
const (
	Both     Direction = iota // I/O: sent to the chip, echoed in the response
	ToChip                    // I: accepted by the chip, no response field
	FromChip                  // O: emitted by the chip only
)

// Field is one entry in a packet-type's field table.
//
// Data is the number of data bytes that follow the identifier, so the whole
// field occupies Data+1 bytes. Table 32's own column uses that convention —
// with one exception, recorded in Disputed.
type Field struct {
	Name string
	ID   byte
	Data int
	Dir  Direction

	// Disputed is non-empty when the manual gives more than one answer for
	// this field. It exists so that a future reader cannot mistake a
	// deliberate choice for an oversight and quietly "correct" it. A wrong
	// length here costs hardware, so an ambiguity is worth carrying in the
	// open.
	Disputed string
}

// Total is the whole size of the field including its identifier byte.
func (f Field) Total() int {
	if f.Data == Variable {
		return Variable
	}
	return f.Data + 1
}

// Control fields, packet type 0x00. Table 32, printed pages 61 and 62, each
// entry cross-checked against its own format table.
var Control = []Field{
	{Name: "PKT_CHANNEL0", ID: 0x40, Data: 0, Dir: Both,
		Disputed: "Table 33 says no data bytes, Table 98 says one, and the " +
			"channel-packet prose calls the whole field two. Zero is taken " +
			"from Table 33 and from the worked examples, where 0x40 " +
			"contributes exactly one byte to the length: Table 111 is 0x0143 " +
			"and Table 114 is 0x000F, both of which need 0x40 to be a bare " +
			"identifier."},
	{Name: "PKT_ECMODE", ID: 0x05, Data: 2, Dir: Both},
	{Name: "PKT_DCMODE", ID: 0x06, Data: 2, Dir: Both},
	{Name: "PKT_RATET", ID: 0x09, Data: 1, Dir: Both},
	{Name: "PKT_RATEP", ID: 0x0A, Data: 12, Dir: Both},
	{Name: "PKT_INIT", ID: 0x0B, Data: 1, Dir: Both},
	{Name: "PKT_LOWPOWER", ID: 0x10, Data: 1, Dir: Both},
	{Name: "PKT_CHANFMT", ID: 0x15, Data: 2, Dir: Both},
	{Name: "PKT_SPCHFMT", ID: 0x16, Data: 2, Dir: Both},
	{Name: "PKT_CODECSTART", ID: 0x2A, Data: 1, Dir: Both},
	{Name: "PKT_CODECSTOP", ID: 0x2B, Data: 0, Dir: Both},
	{Name: "PKT_PRODID", ID: 0x30, Data: 0, Dir: Both},
	{Name: "PKT_VERSTRING", ID: 0x31, Data: 0, Dir: Both},
	{Name: "PKT_COMPAND", ID: 0x32, Data: 1, Dir: Both},
	{Name: "PKT_RESET", ID: 0x33, Data: 0, Dir: ToChip},
	{Name: "PKT_RESETSOFTCFG", ID: 0x34, Data: 6, Dir: ToChip},
	{Name: "PKT_HALT", ID: 0x35, Data: 0, Dir: ToChip},
	{Name: "PKT_GETCFG", ID: 0x36, Data: 0, Dir: Both},
	{Name: "PKT_READCFG", ID: 0x37, Data: 0, Dir: Both},
	{Name: "PKT_CODECCFG", ID: 0x38, Data: Variable, Dir: Both},
	{Name: "PKT_READY", ID: 0x39, Data: 0, Dir: FromChip},
	{Name: "PKT_PARITYMODE", ID: 0x3F, Data: 1, Dir: Both},
	{Name: "PKT_WRITEI2C", ID: 0x44, Data: Variable, Dir: Both},
	{Name: "PKT_GAIN", ID: 0x4B, Data: 2, Dir: Both},
	{Name: "PKT_CLRCODECRESET", ID: 0x46, Data: 0, Dir: Both},
	{Name: "PKT_SETCODECRESET", ID: 0x47, Data: 0, Dir: Both},
	{Name: "PKT_DISCARDCODEC", ID: 0x48, Data: 2, Dir: Both},
	{Name: "PKT_DELAYNUS", ID: 0x49, Data: 2, Dir: Both},
	{Name: "PKT_DELAYNNS", ID: 0x4A, Data: 2, Dir: Both},
	{Name: "PKT_RTSTHRESH", ID: 0x4E, Data: 4, Dir: Both,
		Disputed: "Table 32's data-length column says 5, which is the only " +
			"row in that column that is not a data length. Table 94 and the " +
			"prose beside it both spell the field out as five bytes in total: " +
			"the 0x4E identifier, two bytes of thresh_hi and two of " +
			"thresh_lo. Four data bytes is taken from the breakdown."},
}

// Speech fields, packet type 0x02. Table 98, printed page 79.
var Speech = []Field{
	{Name: "PKT_CHANNEL0", ID: 0x40, Data: 0, Dir: Both,
		Disputed: "as PKT_CHANNEL0 in the control table"},
	{Name: "SPEECHD", ID: 0x00, Data: Variable, Dir: Both},
	{Name: "CMODE", ID: 0x02, Data: 2, Dir: Both},
	{Name: "TONE", ID: 0x08, Data: 2, Dir: Both},
}

// Channel fields, packet type 0x01. Table 106, printed page 82.
//
// Table 106's own column is headed "Field Length" and gives totals rather than
// data lengths, unlike Table 32's "Control Field Data Length" — CMODE is three
// bytes there and carries two. The one row that does not fit either reading is
// PKT_CHANNEL0, recorded below.
var Channel = []Field{
	{Name: "PKT_CHANNEL0", ID: 0x40, Data: 0, Dir: Both,
		Disputed: "as PKT_CHANNEL0 in the control table"},
	{Name: "CHAND", ID: 0x01, Data: Variable, Dir: Both},
	{Name: "CHAND4", ID: 0x17, Data: Variable, Dir: Both},
	{Name: "SAMPLES", ID: 0x03, Data: 1, Dir: Both,
		Disputed: "Table 106 gives the identifier as 0x30 and Table 109 gives " +
			"0x03. Channel Packet Example 2 (Table 114) settles it: the " +
			"manufacturer's own bytes are `03 A1`, and 0x30 in a channel " +
			"packet appears nowhere. 0x03 is taken."},
	{Name: "CMODE", ID: 0x02, Data: 2, Dir: Both},
	{Name: "TONE", ID: 0x08, Data: 2, Dir: Both},
}

// RateIndexDMR is the built-in rate for DMR, for use with PKT_RATET.
//
// Table 115, printed page 90: index 33 is 3600 bps total, 2450 bps speech and
// 1150 bps FEC, and the note beneath the table says index 33 is the one
// interoperable with APCO P25 half rate and DMR.
//
// **Prefer this over PKT_RATEP.** The value 0x21 in the packet that wedged the
// dongle was already the right rate; only the field was wrong. A one-byte
// index keeps the twelve-versus-eleven question about PKT_RATEP off the path
// entirely, and that question is not settled: the manual says twelve data
// bytes as six rate control words, while a byte string that circulates with
// other software has eleven. One of them is wrong and the cost of finding out
// the hard way is a device.
const RateIndexDMR = 33

// ParityEnableBit locates PARITY_ENABLE within the three configuration bytes
// that PKT_GETCFG and PKT_READCFG return: CFG2, bit 4. Table 74, printed page
// 73.
const (
	cfgParityByte = 2
	cfgParityBit  = 4
)

// ParityEnabledIn reports whether the three configuration bytes from a
// PKT_GETCFG or PKT_READCFG response say the PARITY_ENABLE pin was set.
//
// This is worth asking rather than assuming. **Parity is enabled by default**
// (§6.5.5), a chip with parity enabled silently discards every packet that
// lacks a valid parity field, and the operator's board answered PKT_RESET,
// PKT_PRODID and PKT_VERSTRING — none of which carried one. So parity is off
// on that board, and this is how to stop inferring it from three replies and
// read it off the hardware instead.
func ParityEnabledIn(cfg [3]byte) bool {
	return cfg[cfgParityByte]&(1<<cfgParityBit) != 0
}

// ConfigFromResponse pulls the three configuration bytes out of a response to
// PKT_GETCFG or PKT_READCFG.
//
// The response is a control packet carrying the query's own identifier
// followed by CFG0, CFG1 and CFG2 (Tables 77 and 79). It reports false for
// anything that is not that, including a packet whose declared length
// disagrees with what it carries — a short read is not a configuration.
//
// **Derived from the manual; no such response has been seen from the
// operator's board yet.** It is here so that the next bench session reads the
// PARITY_ENABLE pin instead of inferring it, per ParityEnabledIn.
func ConfigFromResponse(pkt []byte) (cfg [3]byte, ok bool) {
	if len(pkt) != 8 || pkt[0] != StartByte || pkt[3] != TypeControl {
		return cfg, false
	}
	if int(pkt[1])<<8|int(pkt[2]) != len(pkt)-4 {
		return cfg, false
	}
	if pkt[4] != 0x36 && pkt[4] != 0x37 {
		return cfg, false
	}
	copy(cfg[:], pkt[5:8])
	return cfg, true
}

// FieldsFor returns the field table for a packet type, and whether the type is
// one the manual defines.
func FieldsFor(kind byte) ([]Field, bool) {
	switch kind {
	case TypeControl:
		return Control, true
	case TypeSpeech:
		return Speech, true
	case TypeChannel:
		return Channel, true
	}
	return nil, false
}

// lookup finds a field by identifier within a packet type's table.
func lookup(kind byte, id byte) (Field, bool) {
	table, ok := FieldsFor(kind)
	if !ok {
		return Field{}, false
	}
	for _, f := range table {
		if f.ID == id {
			return f, true
		}
	}
	return Field{}, false
}
