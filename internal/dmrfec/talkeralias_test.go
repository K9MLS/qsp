package dmrfec

import (
	"strings"
	"testing"
)

// Talker Alias PDUs, against the standard's own numbers.
//
// Everything asserted here is a value read off TS 102 361-2 V2.3.1 rather than
// derived from the code: the four FLCOs from table 5.4, the field widths from
// tables 7.4 and 7.5, the format codes from table 7.25, and the character
// boundaries from §5.4.3. A test that computed its expectations the way the
// builder does would pass for the wrong reason, which this project has caught
// sixteen times.

// TestTheFlcosAreTheOnesTableFivePointFourGives.
func TestTheFlcosAreTheOnesTableFivePointFourGives(t *testing.T) {
	for _, tc := range []struct {
		got  byte
		want byte
		name string
	}{
		{FLCOTalkerAliasHeader, 0x04, "Talker_Alias_hdr, 000100"},
		{FLCOTalkerAliasBlock1, 0x05, "Talker_Alias_blk1, 000101"},
		{FLCOTalkerAliasBlock2, 0x06, "Talker_Alias_blk2, 000110"},
		{FLCOTalkerAliasBlock3, 0x07, "Talker_Alias_blk3, 000111"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s is %#02x, want %#02x", tc.name, tc.got, tc.want)
		}
	}
	// And the format codes from table 7.25.
	for _, tc := range []struct {
		got  TalkerAliasFormat
		want uint8
		name string
	}{
		{TalkerAlias7Bit, 0, "7 bit character"},
		{TalkerAliasISO8Bit, 1, "ISO 8 bit character"},
		{TalkerAliasUTF8, 2, "Unicode UTF-8"},
		{TalkerAliasUTF16BE, 3, "Unicode UTF-16BE"},
	} {
		if uint8(tc.got) != tc.want {
			t.Errorf("%s is %d, want %d", tc.name, uint8(tc.got), tc.want)
		}
	}
}

// TestTheHeaderCarriesTheFieldsTableSevenPointFourGives reads the PDU back
// bit by bit against the table.
//
// PF 1, reserved 1, FLCO 6, FID 8, format 2, length 5, data 49 — which is 72
// bits, and 72 bits is the nine octets every Link Control PDU occupies. That
// arithmetic closing is the first check that the layout was read correctly.
func TestTheHeaderCarriesTheFieldsTableSevenPointFourGives(t *testing.T) {
	if want := 1 + 1 + 6 + 8 + 2 + 5 + headerDataBits; want != LinkControlBytes*8 {
		t.Fatalf("table 7.4's fields add to %d bits and a Link Control PDU is %d",
			want, LinkControlBytes*8)
	}
	if want := 1 + 1 + 6 + 8 + blockDataBits; want != LinkControlBytes*8 {
		t.Fatalf("table 7.5's fields add to %d bits and a Link Control PDU is %d",
			want, LinkControlBytes*8)
	}

	pdus, err := TalkerAliasPDUs("K9MLS", TalkerAliasUTF8)
	if err != nil {
		t.Fatalf("building an alias: %v", err)
	}
	if len(pdus) != 1 {
		t.Fatalf("a five-character alias needed %d PDU(s); §5.4.3 gives six in "+
			"the header for an 8-bit format", len(pdus))
	}
	h := pdus[0]
	if len(h) != LinkControlBytes {
		t.Fatalf("the header is %d bytes, want %d", len(h), LinkControlBytes)
	}

	bits := h
	if got := readBits(bits, 0, 1); got != 0 {
		t.Errorf("the protect flag is %d; it marks an encrypted Link Control and "+
			"QSP encrypts nothing", got)
	}
	if got := readBits(bits, 1, 1); got != 0 {
		t.Errorf("the reserved bit is %d, want 0", got)
	}
	if got := readBits(bits, 2, 6); got != uint64(FLCOTalkerAliasHeader) {
		t.Errorf("the FLCO reads %#02x, want %#02x", got, FLCOTalkerAliasHeader)
	}
	if got := readBits(bits, 8, 8); got != 0 {
		t.Errorf("the feature set ID reads %#02x, want 0 for the standard set", got)
	}
	if got := readBits(bits, 16, 2); got != uint64(TalkerAliasUTF8) {
		t.Errorf("the format reads %d, want %d", got, TalkerAliasUTF8)
	}
	if got := readBits(bits, 18, 5); got != 5 {
		t.Errorf("the length reads %d, want 5 characters", got)
	}
	if got := readBits(bits, 23, 1); got != 0 {
		t.Errorf("the reserved most significant data bit is %d; table 7.4's note "+
			"reserves it for the 8-bit and 16-bit formats", got)
	}
	// And the first character starts at octet 3, as that note says.
	if got := byte(readBits(bits, 24, 8)); got != 'K' {
		t.Errorf("octet 3 is %#02x, want %#02x — the note says the first valid "+
			"character starts there for an 8-bit format", got, 'K')
	}
}

// TestTheCharacterBoundariesAreTheOnesSectionFivePointFourPointThreeGives.
//
// The numbers are transcribed from the standard rather than computed:
// 7-bit goes 7, 15, 23, 31 and 8-bit goes 6, 13, 20, 27. The builder derives
// them from the field widths, so this is the independent check that the widths
// were read right — two routes to the same numbers.
func TestTheCharacterBoundariesAreTheOnesSectionFivePointFourPointThreeGives(t *testing.T) {
	for _, tc := range []struct {
		format TalkerAliasFormat
		want   []int // characters at which the PDU count increases
	}{
		{TalkerAlias7Bit, []int{7, 15, 23, 31}},
		{TalkerAliasUTF8, []int{6, 13, 20, 27}},
		{TalkerAliasISO8Bit, []int{6, 13, 20, 27}},
	} {
		for pdus, boundary := range tc.want {
			// At the boundary, that many PDUs. One past it, one more.
			at, err := TalkerAliasPDUs(strings.Repeat("A", boundary), tc.format)
			if err != nil {
				t.Errorf("%s alias of %d characters: %v", tc.format, boundary, err)
				continue
			}
			if len(at) != pdus+1 {
				t.Errorf("%s: %d characters produced %d PDU(s), want %d",
					tc.format, boundary, len(at), pdus+1)
			}

			over := boundary + 1
			past, err := TalkerAliasPDUs(strings.Repeat("A", over), tc.format)
			if boundary == tc.want[len(tc.want)-1] {
				// Past the last boundary nothing fits.
				if err == nil {
					t.Errorf("%s: %d characters were accepted; the format carries %d",
						tc.format, over, boundary)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s alias of %d characters: %v", tc.format, over, err)
				continue
			}
			if len(past) != pdus+2 {
				t.Errorf("%s: %d characters produced %d PDU(s), want %d",
					tc.format, over, len(past), pdus+2)
			}
		}
	}
}

// TestAnAliasSurvivesItsOwnPdus is the round trip.
//
// It proves the builder and the reader agree, which is not the same as proving
// either matches a radio — **nothing here has been compared against bytes a
// Motorola radio sent**, and until it has, this is a careful reading of a
// table rather than a recording. A capture of a radio with Inband Caller Alias
// enabled is what would settle it.
func TestAnAliasSurvivesItsOwnPdus(t *testing.T) {
	for _, format := range []TalkerAliasFormat{TalkerAlias7Bit, TalkerAliasUTF8, TalkerAliasISO8Bit} {
		for _, alias := range []string{
			"K",
			"K9MLS",
			"K9MLS ZELLO",
			"K9MLS VIA ZELLO GATEWAY",
			strings.Repeat("X", 27),
		} {
			pdus, err := TalkerAliasPDUs(alias, format)
			if err != nil {
				t.Errorf("%s %q: %v", format, alias, err)
				continue
			}
			for i, pdu := range pdus {
				if len(pdu) != LinkControlBytes {
					t.Errorf("%s %q PDU %d is %d bytes, want %d",
						format, alias, i, len(pdu), LinkControlBytes)
				}
			}
			got, ok := TalkerAliasFrom(pdus)
			if !ok {
				t.Errorf("%s %q did not read back", format, alias)
				continue
			}
			if got != alias {
				t.Errorf("%s: %q read back as %q", format, alias, got)
			}
		}
	}

	// 31 characters is the longest the length element can state, and only the
	// 7-bit format reaches it.
	long := strings.Repeat("Y", TalkerAliasMaxLength)
	pdus, err := TalkerAliasPDUs(long, TalkerAlias7Bit)
	if err != nil {
		t.Fatalf("a 31-character 7-bit alias: %v", err)
	}
	if len(pdus) != 4 {
		t.Errorf("31 characters produced %d PDUs, want a header and three blocks",
			len(pdus))
	}
	if got, ok := TalkerAliasFrom(pdus); !ok || got != long {
		t.Errorf("the longest alias read back as %q (ok=%v)", got, ok)
	}
	if _, err := TalkerAliasPDUs(strings.Repeat("Y", 32), TalkerAlias7Bit); err == nil {
		t.Error("32 characters were accepted; the length element is five bits")
	}
}

// TestTheBlocksAreNumberedInOrder, because a receiving radio reassembles by
// FLCO and three blocks in the wrong order is a scrambled alias.
func TestTheBlocksAreNumberedInOrder(t *testing.T) {
	pdus, err := TalkerAliasPDUs(strings.Repeat("Z", TalkerAliasMaxLength), TalkerAlias7Bit)
	if err != nil {
		t.Fatalf("building an alias: %v", err)
	}
	want := []byte{
		FLCOTalkerAliasHeader,
		FLCOTalkerAliasBlock1,
		FLCOTalkerAliasBlock2,
		FLCOTalkerAliasBlock3,
	}
	if len(pdus) != len(want) {
		t.Fatalf("%d PDUs, want %d", len(pdus), len(want))
	}
	for i, pdu := range pdus {
		if got := pdu[0] & 0x3F; got != want[i] {
			t.Errorf("PDU %d carries FLCO %#02x, want %#02x", i, got, want[i])
		}
	}
}

// TestTheFormatsTheStandardContradictsItselfAboutAreRefused.
//
// Two refusals, both because the standard is not self-consistent and guessing
// would be wrong on the air rather than on a bench:
//
//   - **UTF-16BE**: §5.4.3's character boundaries for it are 3, 6, 10, 13 —
//     increments of 3, 4 and 3, where a 56-bit block holds three and a half
//     16-bit characters. The other formats increment evenly.
//   - **Non-ASCII in an 8-bit format**: §7.2.19's prose calls the length
//     element bytes and its own table 7.26 calls it characters. They agree for
//     ASCII and differ for anything else, and a radio told the wrong number
//     shows a truncated alias.
func TestTheFormatsTheStandardContradictsItselfAboutAreRefused(t *testing.T) {
	if _, err := TalkerAliasPDUs("K9MLS", TalkerAliasUTF16BE); err == nil {
		t.Error("a UTF-16BE alias was built; the standard's own boundaries for it " +
			"are 3, 6, 10, 13 and do not reconstruct")
	} else if !strings.Contains(err.Error(), "3, 6, 10, 13") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	if _, err := TalkerAliasPDUs("CAFÉ", TalkerAliasUTF8); err == nil {
		t.Error("a non-ASCII 8-bit alias was built; §7.2.19 and table 7.26 " +
			"disagree about whether the length is bytes or characters")
	} else if !strings.Contains(err.Error(), "7.26") {
		t.Errorf("the refusal does not cite the contradiction: %v", err)
	}

	// A character needing more than seven bits is refused in the 7-bit
	// format, rather than silently losing its top bit.
	if _, err := TalkerAliasPDUs("CAFÉ", TalkerAlias7Bit); err == nil {
		t.Error("a non-ASCII alias was built in the 7-bit format")
	}

	// And an empty alias carries nothing, so it is a caller's mistake rather
	// than a zero-length transmission.
	if _, err := TalkerAliasPDUs("", TalkerAlias7Bit); err == nil {
		t.Error("an empty alias was accepted")
	}
}

// TestAMalformedSetOfPdusDoesNotReadBack keeps the reader from inventing an
// alias out of whatever it was given.
//
// It reads captures as well as its own output, so a burst that is not a Talker
// Alias must not produce a string — an alias assembled from a GPS PDU would be
// displayed to an operator as somebody's identity.
func TestAMalformedSetOfPdusDoesNotReadBack(t *testing.T) {
	good, err := TalkerAliasPDUs("K9MLS ZELLO", TalkerAlias7Bit)
	if err != nil {
		t.Fatalf("building an alias: %v", err)
	}

	for name, pdus := range map[string][][]byte{
		"nothing":     nil,
		"a short PDU": {make([]byte, 4)},
		"a header only when blocks were promised": {good[0]},
		"a block first": {good[1], good[0]},
		"blocks out of order": func() [][]byte {
			long, err := TalkerAliasPDUs(strings.Repeat("A", 23), TalkerAlias7Bit)
			if err != nil {
				t.Fatalf("building a long alias: %v", err)
			}
			return [][]byte{long[0], long[2], long[1]}
		}(),
		"a group voice header": {LinkControlFor(2, 3132911, false)},
	} {
		if got, ok := TalkerAliasFrom(pdus); ok {
			t.Errorf("%s read back as the alias %q", name, got)
		}
	}
}
