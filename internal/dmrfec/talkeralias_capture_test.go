package dmrfec

import (
	"strings"
	"testing"
)

// A MOTOTRBO's own Talker Alias: testdata/hbp/hbp-talker-alias.pcap,
// md5 73c97deafb588bccad2922976b030aff, taken 2026-09-15.
//
// **These nine-byte PDUs were transmitted by a radio.** Everything 0370 built
// came from TS 102 361-2's tables with nothing but this package's own decoder
// agreeing, and the record said so. This is the comparison that was missing.

// radioAliasHeader and radioAliasBlock are the two PDUs the radio sent, with
// Inband Caller Alias enabled in CPS and the alias set to "K9MLS R7".
var (
	radioAliasHeader = []byte{0x04, 0x00, 0x90, 0x4b, 0x39, 0x4d, 0x4c, 0x53, 0x20}
	radioAliasBlock  = []byte{0x05, 0x00, 0x52, 0x37, 0x00, 0x00, 0x00, 0x00, 0x00}
)

// TestTheTalkerAliasIsTheOneTheRadioSent is the whole point of the capture.
//
// Both directions: the radio's PDUs read back as the alias, and the alias
// builds the radio's PDUs. Byte for byte, both of them.
func TestTheTalkerAliasIsTheOneTheRadioSent(t *testing.T) {
	const want = "K9MLS R7"

	got, ok := TalkerAliasFrom([][]byte{radioAliasHeader, radioAliasBlock})
	if !ok {
		t.Fatal("the radio's own Talker Alias PDUs did not decode")
	}
	if got != want {
		t.Errorf("the radio's alias reads %q, want %q", got, want)
	}

	ours, err := TalkerAliasPDUs(want, TalkerAliasUTF8)
	if err != nil {
		t.Fatalf("building %q: %v", want, err)
	}
	if len(ours) != 2 {
		t.Fatalf("%q built %d PDUs and the radio sent 2", want, len(ours))
	}
	for i, pair := range [][2][]byte{
		{ours[0], radioAliasHeader},
		{ours[1], radioAliasBlock},
	} {
		if string(pair[0]) != string(pair[1]) {
			t.Errorf("PDU %d: we build %x and the radio sent %x", i, pair[0], pair[1])
		}
	}
}

// TestTheReservedBitInTableSevenPointFoursNoteIsWhatTheRadioSends is the
// single most valuable byte in the capture.
//
// Table 7.4 carries a note saying the most significant bit of the 49-bit data
// field is reserved for the 8-bit and 16-bit formats, and that the first valid
// character then starts at octet 3. **That bit was written because a footnote
// said so** — no worked example prints it, and nothing but this package's own
// decoder agreed.
//
// The radio sets it to zero and starts `K` at octet 3.
func TestTheReservedBitInTableSevenPointFoursNoteIsWhatTheRadioSends(t *testing.T) {
	octet2 := radioAliasHeader[2]
	if octet2 != 0x90 {
		t.Fatalf("the radio's header octet 2 is %#02x, want 0x90", octet2)
	}

	// Format, length and the reserved bit, read at the offsets table 7.4
	// gives rather than by any helper in this package.
	if format := TalkerAliasFormat(octet2 >> 6); format != TalkerAliasUTF8 {
		t.Errorf("the radio sent format %d, want %d for UTF-8", format, TalkerAliasUTF8)
	}
	if length := int(octet2>>1) & 0x1F; length != 8 {
		t.Errorf("the radio declared %d characters, want 8", length)
	}
	if reserved := octet2 & 1; reserved != 0 {
		t.Errorf("the radio set the reserved data bit to %d; table 7.4's note "+
			"reserves it for the 8-bit formats and this project wrote it as 0 "+
			"on the strength of that note alone", reserved)
	}

	// And the first character is at octet 3, which is what the reserved bit
	// buys and what the note says.
	if got := string(radioAliasHeader[3:]); got != "K9MLS " {
		t.Errorf("the radio's header carries %q from octet 3, want %q", got, "K9MLS ")
	}
	// A block's data starts at octet 2, with no reserved bit.
	if got := string(radioAliasBlock[2:4]); got != "R7" {
		t.Errorf("the radio's block carries %q from octet 2, want %q", got, "R7")
	}
	// The rest of the block is zero padding, which is what an alias that does
	// not fill its last block produces.
	for i, b := range radioAliasBlock[4:] {
		if b != 0 {
			t.Errorf("block octet %d is %#02x, want zero padding", i+4, b)
		}
	}
}

// TestAliasPdusAreNotConsecutiveInATransmission records a reassembly defect
// that self-generated fixtures cannot show.
//
// The capture holds **23 complete embedded LC groups in 140 bursts: 21 voice
// LC, one alias header, one alias block.** The radio interleaves them, so the
// header and its block are separated by voice LC groups — and the first
// attempt at decoding this capture looked for a header followed immediately by
// its blocks and found nothing.
//
// **Reassembly collects by FLCO across a transmission, never by adjacency.**
// Every fixture this package builds itself puts the PDUs in a row, so nothing
// but real traffic could have caught it.
func TestAliasPdusAreNotConsecutiveInATransmission(t *testing.T) {
	// The transmission as it arrived: voice LC either side of the alias.
	voiceLC := LinkControlFor(2, 3132910, false)
	arrived := [][]byte{
		voiceLC, voiceLC, voiceLC,
		radioAliasHeader,
		voiceLC, voiceLC,
		radioAliasBlock,
		voiceLC,
	}

	// Adjacency: a header with whatever immediately follows it. This is what
	// failed against the capture, and it must still fail here.
	for i, pdu := range arrived {
		if pdu[0]&0x3F != FLCOTalkerAliasHeader {
			continue
		}
		run := [][]byte{pdu}
		for j := i + 1; j < len(arrived); j++ {
			flco := arrived[j][0] & 0x3F
			if flco < FLCOTalkerAliasBlock1 || flco > FLCOTalkerAliasBlock3 {
				break
			}
			run = append(run, arrived[j])
		}
		if _, ok := TalkerAliasFrom(run); ok {
			t.Error("adjacency reassembled an alias from this transmission; the " +
				"capture's header and block are separated by voice LC groups, so " +
				"a test where adjacency works is not testing the real shape")
		}
	}

	// Collecting by FLCO across the whole transmission is what works.
	byFLCO := map[byte][]byte{}
	for _, pdu := range arrived {
		if flco := pdu[0] & 0x3F; flco >= FLCOTalkerAliasHeader && flco <= FLCOTalkerAliasBlock3 {
			byFLCO[flco] = pdu
		}
	}
	collected := [][]byte{byFLCO[FLCOTalkerAliasHeader]}
	for flco := FLCOTalkerAliasBlock1; flco <= FLCOTalkerAliasBlock3; flco++ {
		if pdu, ok := byFLCO[flco]; ok {
			collected = append(collected, pdu)
		}
	}
	got, ok := TalkerAliasFrom(collected)
	if !ok {
		t.Fatal("collecting by FLCO did not reassemble the alias")
	}
	if got != "K9MLS R7" {
		t.Errorf("collecting by FLCO gave %q", got)
	}

	// **And the alias is rare.** One header and one block in 23 groups, so a
	// reader that gave up after a second of audio would usually see nothing.
	// The ratio is recorded because it is the thing that sets how long a
	// reader must wait.
	const (
		groupsInCapture = 23
		aliasPDUs       = 2
	)
	if aliasPDUs*10 > groupsInCapture {
		t.Errorf("%d of %d groups were alias PDUs; the capture's ratio is under "+
			"one in ten and a reader must accumulate across a transmission",
			aliasPDUs, groupsInCapture)
	}
}

// TestTheAliasFormatTheRadioChoseIsOneWeBuild.
//
// Motorola's CPS offers UTF-8 and UTF-16BE. This radio sends format 2, UTF-8,
// which is the format TalkerAliasPDUs implements — so the refusal of UTF-16BE
// costs nothing against this hardware. It is still a refusal rather than a
// gap: §5.4.3's boundaries for UTF-16BE run 3, 6, 10, 13 and do not
// reconstruct.
func TestTheAliasFormatTheRadioChoseIsOneWeBuild(t *testing.T) {
	format := TalkerAliasFormat(radioAliasHeader[2] >> 6)
	if _, ok := format.bitsPerChar(); !ok {
		t.Fatalf("the radio sent format %s, which this package does not build", format)
	}
	if format != TalkerAliasUTF8 {
		t.Errorf("the radio sent %s; the capture notes record UTF-8", format)
	}
	if _, err := TalkerAliasPDUs("K9MLS R7", TalkerAliasUTF16BE); err == nil {
		t.Error("UTF-16BE was built; its boundaries do not reconstruct and no " +
			"capture has ever shown one")
	} else if !strings.Contains(err.Error(), "3, 6, 10, 13") {
		t.Errorf("the UTF-16BE refusal does not say why: %v", err)
	}
}
