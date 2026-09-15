package dmrfec

import (
	"fmt"
	"strings"
)

// Talker Alias: a text string carried in the embedded Link Control of a voice
// transmission.
//
// # Why QSP generates one
//
// A Zello user has no radio and therefore no radio ID, so [ADR-0064] has them
// transmit under the gateway's own ID with an administrator-set alias. This is
// the alias, on the air. It is display data and not station identification —
// the operator identifies by voice, exactly as on analog and on EchoLink.
//
// Generating one in a gateway is established practice rather than an
// invention: BrandMeister generates Talker Alias for its own gateway
// applications.
//
// # Source
//
// ETSI TS 102 361-2 V2.3.1 (2016-02), which is in the tree's dependency notes
// as the document this was built from. §5.4.3 is the service, §7.1.1.4 and
// §7.1.1.5 are the two PDU layouts (tables 7.4 and 7.5), §7.2.18 and §7.2.19
// are the format and length elements, and table 5.4 lists the four FLCOs.
//
// **Written from the tables, not from recollection.** PROJECT_MEMORY §8r
// records what guessing at a field's length cost on the AMBE side — a wedged
// dongle recovered only by removing its power — and this one transmits on the
// operator's repeater.
//
// # What this does not do
//
// It builds the PDUs. **It does not carry them**, because the embedded Link
// Control that carries a 9-byte LC across the four middle bursts of a voice
// superframe is a BPTC(16,7) with a 5-bit checksum, and that is specified in
// TS 102 361-1 rather than in the part above. The encoder for it is not in
// this package: `EncodeBPTC` here is the 196-bit data-burst code, which is a
// different thing with a similar name.
//
// So these PDUs are complete and unused until that layer exists. That is
// deliberate: half a feature that says so beats half a feature that looks
// finished.

// The Talker Alias FLCOs, from table 5.4.
const (
	// FLCOTalkerAliasHeader is Talker_Alias_hdr: the first PDU, carrying the
	// format, the total length and the first characters.
	FLCOTalkerAliasHeader byte = 0x04
	// FLCOTalkerAliasBlock1 is Talker_Alias_blk1, and blocks 2 and 3 follow
	// consecutively.
	FLCOTalkerAliasBlock1 byte = 0x05
	FLCOTalkerAliasBlock2 byte = 0x06
	FLCOTalkerAliasBlock3 byte = 0x07
)

// TalkerAliasFormat is the Talker Alias Data Format element, table 7.25.
type TalkerAliasFormat uint8

// The four formats the standard defines.
const (
	// TalkerAlias7Bit packs seven bits per character and fits 31 of them,
	// which is the longest alias the standard carries.
	TalkerAlias7Bit TalkerAliasFormat = 0
	// TalkerAliasISO8Bit is one byte per character, 27 characters.
	TalkerAliasISO8Bit TalkerAliasFormat = 1
	// TalkerAliasUTF8 is one byte per character, 27 characters, and is what
	// Motorola's CPS offers alongside UTF-16BE.
	TalkerAliasUTF8 TalkerAliasFormat = 2
	// TalkerAliasUTF16BE is two bytes per character.
	//
	// **Not implemented, and the standard is why.** §5.4.3's own character
	// boundaries for this format go 3, 6, 10, 13 — increments of 3, 4 and 3
	// where a 56-bit block holds three and a half 16-bit characters. The
	// other two formats increment evenly and reconstruct exactly. Refusing is
	// the honest answer until somebody reads how a character straddles a
	// block boundary, and a callsign needs none of it.
	TalkerAliasUTF16BE TalkerAliasFormat = 3
)

// String names a format for an error or a log line.
func (f TalkerAliasFormat) String() string {
	switch f {
	case TalkerAlias7Bit:
		return "7-bit"
	case TalkerAliasISO8Bit:
		return "ISO 8-bit"
	case TalkerAliasUTF8:
		return "UTF-8"
	case TalkerAliasUTF16BE:
		return "UTF-16BE"
	}
	return fmt.Sprintf("format %d", uint8(f))
}

// bitsPerChar returns the width of one character, and whether the format is
// one this builds.
func (f TalkerAliasFormat) bitsPerChar() (int, bool) {
	switch f {
	case TalkerAlias7Bit:
		return 7, true
	case TalkerAliasISO8Bit, TalkerAliasUTF8:
		return 8, true
	}
	return 0, false
}

// TalkerAliasMaxLength is the longest alias the Talker Alias Data Length
// element can state.
//
// Five bits, so 31, and table 7.26's note confirms it: the longest alias is 31
// characters carried by one header and three blocks. It is reachable only in
// the 7-bit format; an 8-bit alias stops at 27.
const TalkerAliasMaxLength = 31

// headerDataBits is the Talker Alias data field in the header PDU, table 7.4.
//
// 49 bits, **of which the most significant is reserved for the 8-bit and
// 16-bit formats** — the note under table 7.4 says so, and that the first
// valid character then starts at octet 3. All 49 are data in the 7-bit format.
const headerDataBits = 49

// blockDataBits is the Talker Alias data field in a block PDU, table 7.5.
const blockDataBits = 56

// TalkerAliasPDUs builds the Link Control PDUs carrying an alias.
//
// It returns between one and four nine-byte PDUs: a header, then as many
// blocks as the alias needs. §5.4.3 sets the boundaries, and they follow from
// the field widths rather than being a second table to keep true — 7 then 8
// characters at a time in the 7-bit format, 6 then 7 in the 8-bit ones.
//
// The caller supplies the alias already bounded; this refuses anything longer
// rather than truncating, because an alias truncated on the air is wrong in a
// place the operator cannot see.
func TalkerAliasPDUs(alias string, format TalkerAliasFormat) ([][]byte, error) {
	width, ok := format.bitsPerChar()
	if !ok {
		return nil, fmt.Errorf(
			"dmrfec: talker alias format %s is not built; the standard's own "+
				"character boundaries for it are 3, 6, 10, 13, which a 56-bit "+
				"block cannot produce evenly", format)
	}

	// Non-ASCII in an 8-bit format is refused rather than guessed at.
	//
	// **§7.2.19 contradicts its own table**: the prose calls the length "the
	// length in bytes" and table 7.26 calls it "Length in characters". For
	// ASCII the two agree and nothing turns on it. For anything multi-byte
	// they do not, and a receiving radio told the wrong number shows a
	// truncated or padded alias.
	if width == 8 && !isASCII(alias) {
		return nil, fmt.Errorf(
			"dmrfec: %q is not ASCII, and the standard's length element is "+
				"described as bytes in §7.2.19 and as characters in its own "+
				"table 7.26; the two differ for multi-byte text", alias)
	}

	chars := []rune(alias)
	if len(chars) == 0 {
		return nil, fmt.Errorf("dmrfec: an empty talker alias carries nothing")
	}
	if len(chars) > TalkerAliasMaxLength {
		return nil, fmt.Errorf(
			"dmrfec: a talker alias of %d characters exceeds the %d the length "+
				"element can state", len(chars), TalkerAliasMaxLength)
	}
	if width == 7 {
		for _, r := range chars {
			if r > 0x7F {
				return nil, fmt.Errorf(
					"dmrfec: %q cannot be sent in the 7-bit format; %q needs more "+
						"than seven bits", alias, r)
			}
		}
	}

	// How many characters the header carries, and each block.
	//
	// **The reserved most significant bit needs no arithmetic of its own**,
	// which breaking this proved: 49 bits hold six 8-bit characters whether
	// the reserved bit is counted or not, because integer division discards
	// the remainder either way. An earlier version subtracted it here and the
	// subtraction was dead. What is not dead is writing the bit, which is
	// what moves the first character to octet 3 as table 7.4's note requires.
	headerRoom := headerDataBits / width
	blockRoom := blockDataBits / width

	if room := headerRoom + 3*blockRoom; len(chars) > room {
		return nil, fmt.Errorf(
			"dmrfec: a talker alias of %d characters does not fit the %s format, "+
				"which carries %d in a header and three blocks",
			len(chars), format, room)
	}

	// The header: PF, reserved, FLCO, FID, format, length, data.
	header := newLCWriter(FLCOTalkerAliasHeader)
	header.write(uint64(format), 2)
	header.write(uint64(len(chars)), 5)
	if width == 8 {
		header.write(0, 1) // reserved, table 7.4's note
	}
	used := headerRoom
	if used > len(chars) {
		used = len(chars)
	}
	for i := 0; i < headerRoom; i++ {
		var r rune
		if i < used {
			r = chars[i]
		}
		header.write(uint64(r), width)
	}
	out := [][]byte{header.bytes()}

	// Blocks, in order, each padded with zeros to its full width so that the
	// PDU is always nine bytes.
	for at := used; at < len(chars); at += blockRoom {
		flco := FLCOTalkerAliasBlock1 + byte(len(out)) - 1
		block := newLCWriter(flco)
		for i := 0; i < blockRoom; i++ {
			var r rune
			if at+i < len(chars) {
				r = chars[at+i]
			}
			block.write(uint64(r), width)
		}
		out = append(out, block.bytes())
	}
	return out, nil
}

// isASCII reports whether every rune is below 0x80.
func isASCII(s string) bool {
	for _, r := range s {
		if r > 0x7F {
			return false
		}
	}
	return true
}

// lcWriter builds a 72-bit Link Control PDU a field at a time.
//
// The first two octets are the format every LC PDU shares, per figure 7.1 of
// TS 102 361-1: a protect flag, a reserved bit, the six-bit FLCO, then the
// feature set ID. **The protect flag is zero**: it marks an encrypted LC and
// QSP encrypts nothing.
//
// It writes straight into the nine octets using this package's own writeBits
// rather than keeping a slice of one-bit bytes, because the package already
// has that function and a second way to pack bits is a second thing to get
// right.
type lcWriter struct {
	pdu []byte
	at  int
}

func newLCWriter(flco byte) *lcWriter {
	w := &lcWriter{pdu: make([]byte, LinkControlBytes)}
	w.write(0, 1)            // PF: not protected
	w.write(0, 1)            // reserved
	w.write(uint64(flco), 6) // FLCO
	w.write(0, 8)            // FID: standard feature set
	return w
}

// write appends the low n bits of v, most significant first. Bits beyond the
// PDU are dropped, which cannot happen for the fixed widths above and is not
// worth an error path that no input can reach.
func (w *lcWriter) write(v uint64, n int) {
	if w.at+n > LinkControlBytes*8 {
		return
	}
	writeBits(w.pdu, w.at, n, v)
	w.at += n
}

// bytes returns the finished PDU.
//
// Trailing bits stay zero, which is what the standard's own character counts
// produce for an alias that does not fill its last block.
func (w *lcWriter) bytes() []byte { return w.pdu }

// TalkerAliasFrom reads an alias back out of its PDUs.
//
// It exists to test the builder against itself and to read a capture: a
// Motorola radio sending its own alias is the only source of bytes this
// project has not written, and comparing against one is how the builder stops
// being a reading of a table and becomes a recording.
//
// It returns the alias and whether the PDUs formed a complete one.
func TalkerAliasFrom(pdus [][]byte) (string, bool) {
	if len(pdus) == 0 || len(pdus[0]) != LinkControlBytes {
		return "", false
	}
	if flco := pdus[0][0] & 0x3F; flco != FLCOTalkerAliasHeader {
		return "", false
	}

	head := pdus[0]
	format := TalkerAliasFormat(readBits(head, 16, 2))
	length := int(readBits(head, 18, 5))
	width, ok := format.bitsPerChar()
	if !ok || length == 0 || length > TalkerAliasMaxLength {
		return "", false
	}

	at := 23
	if width == 8 {
		at++ // the reserved most significant bit
	}
	headerRoom := (LinkControlBytes*8 - at) / width

	var sb strings.Builder
	chars := 0
	read := func(pdu []byte, from, room int) {
		for i := 0; i < room && chars < length; i++ {
			sb.WriteRune(rune(readBits(pdu, from+i*width, width)))
			chars++
		}
	}
	read(head, at, headerRoom)

	for i, pdu := range pdus[1:] {
		if len(pdu) != LinkControlBytes {
			return "", false
		}
		if flco := pdu[0] & 0x3F; flco != FLCOTalkerAliasBlock1+byte(i) {
			return "", false
		}
		read(pdu, 16, blockDataBits/width)
	}

	out := sb.String()
	if len([]rune(out)) != length {
		return "", false
	}
	return out, true
}
