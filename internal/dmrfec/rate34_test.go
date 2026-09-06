package dmrfec_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"
	"unicode/utf16"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// The two captures that hold Rate 3/4 data blocks. Every other IPSC fixture in
// this repository is voice, registration, or text short enough to fit in the
// twelve-octet blocks BPTC carries.
const (
	rate34Fixture = "../../testdata/ipsc/ipsc-text-rate34.pcap"
	textFixture   = "../../testdata/ipsc/ipsc-text.pcap"
)

// rate34Frame is one captured 60-byte text datagram, kept with the fields a
// test needs to talk about it.
type rate34Frame struct {
	counter  uint8
	stream   uint16
	dataType uint8
	block    []byte
}

// rate34Frames reads the Rate 3/4 datagrams out of a capture.
//
// Offsets are whole-datagram coordinates so they read directly against the
// bytes in the .md beside the fixture: byte 30 is the DMR data type, and the
// eighteen-octet block begins at 38.
func rate34Frames(tb testing.TB, path string) []rate34Frame {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out []rate34Frame
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		// Linux cooked v2, then IPv4, then UDP.
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		d := udp[8:]
		if len(d) != 60 || (d[0] != 0x83 && d[0] != 0x84) {
			continue
		}
		out = append(out, rate34Frame{
			counter:  d[5],
			stream:   binary.BigEndian.Uint16(d[15:17]),
			dataType: d[30],
			block:    bytes.Clone(d[38 : 38+dmrfec.Rate34BlockBytes]),
		})
	}
	return out
}

func allRate34Frames(tb testing.TB) []rate34Frame {
	tb.Helper()
	out := append(rate34Frames(tb, rate34Fixture), rate34Frames(tb, textFixture)...)
	// A reader that silently returns nothing would make every assertion below
	// pass by having nothing to assert about.
	if len(out) != 42 {
		tb.Fatalf("read %d Rate 3/4 frames from the two fixtures, want 42; the reader is broken", len(out))
	}
	return out
}

// TestEveryCapturedBlockIsRate34AndCarriesAVerifiedCRC9 is the measurement the
// block structure rests on.
//
// Sixteen octets of user data, then a seven-bit serial number and a nine-bit
// CRC over them. If the split were anywhere else, or the CRC computed over the
// serial first as clause B.3.11 describes, this would fail on the first block.
func TestEveryCapturedBlockIsRate34AndCarriesAVerifiedCRC9(t *testing.T) {
	for i, f := range allRate34Frames(t) {
		if f.dataType != dmrfec.DataTypeRate34 {
			t.Errorf("frame %d has data type %#x, want %#x", i, f.dataType, dmrfec.DataTypeRate34)
		}
		if _, verified := dmrfec.Rate34Serial(f.block); !verified {
			t.Errorf("frame %d: CRC-9 does not verify over block %x", i, f.block)
		}
		if got := dmrfec.Rate34OrderOf(f.block); got != dmrfec.Rate34ControlLast {
			t.Errorf("frame %d: block reads as %s, want control-last", i, got)
		}
	}
}

// TestABlockWithOneBitFlippedFailsItsCRC9 is here because the test above
// asserts that something verifies, and a check that cannot fail is not a check.
func TestABlockWithOneBitFlippedFailsItsCRC9(t *testing.T) {
	frames := allRate34Frames(t)
	for bit := 0; bit < dmrfec.Rate34BlockBytes*8; bit++ {
		broken := bytes.Clone(frames[0].block)
		broken[bit/8] ^= 0x80 >> (bit % 8)
		if _, verified := dmrfec.Rate34Serial(broken); verified {
			t.Fatalf("bit %d flipped and the CRC-9 still verified", bit)
		}
	}
}

// TestTheSerialNumbersOfOneTextCountFromZero checks the other half of the
// control pair. Six blocks of one transmission are numbered 0 to 5.
func TestTheSerialNumbersOfOneTextCountFromZero(t *testing.T) {
	type key struct {
		counter uint8
		stream  uint16
	}
	seen := map[key][]uint8{}
	var order []key
	for _, f := range rate34Frames(t, rate34Fixture) {
		k := key{f.counter, f.stream}
		if _, ok := seen[k]; !ok {
			order = append(order, k)
		}
		serial, _ := dmrfec.Rate34Serial(f.block)
		seen[k] = append(seen[k], serial)
	}
	if len(order) == 0 {
		t.Fatal("no transmissions found in the fixture")
	}
	for _, k := range order {
		got := seen[k]
		for i, s := range got {
			if int(s) != i {
				t.Errorf("transmission %v: block %d carries serial %d", k, i, s)
			}
		}
	}
}

// TestTheBlocksOfOneTextReassembleIntoTheDatagramTheRadioSent is the assertion
// that makes the rest of this file more than arithmetic.
//
// Concatenating the user-data halves of one transmission's blocks, in serial
// order, must produce the IPv4 datagram the radio built: Motorola's radio-IP
// addresses derived from the two radio IDs, UDP on port 4007, and a UTF-16
// payload that is the sentence the operator typed. Nothing about that survives
// a wrong offset.
func TestTheBlocksOfOneTextReassembleIntoTheDatagramTheRadioSent(t *testing.T) {
	frames := rate34Frames(t, rate34Fixture)
	var first []rate34Frame
	for _, f := range frames {
		if len(first) > 0 && (f.counter != first[0].counter || f.stream != first[0].stream) {
			break
		}
		first = append(first, f)
	}
	if len(first) != 6 {
		t.Fatalf("the first transmission has %d blocks, want 6", len(first))
	}

	var payload []byte
	for _, f := range first {
		payload = append(payload, f.block[:dmrfec.Rate34DataBytes]...)
	}

	if payload[0]>>4 != 4 {
		t.Fatalf("reassembled payload does not begin with an IPv4 header: %x", payload[:4])
	}
	total := int(binary.BigEndian.Uint16(payload[2:4]))
	if total != 88 {
		t.Errorf("IPv4 total length is %d, want 88", total)
	}
	if payload[9] != 17 {
		t.Errorf("IPv4 protocol is %d, want 17 (UDP)", payload[9])
	}
	// 0x0c followed by the 24-bit radio ID: 3132910 sending, 3155373 receiving.
	if src := binary.BigEndian.Uint32(payload[12:16]); src != 0x0c2fcdee {
		t.Errorf("source address is %#08x, want %#08x", src, 0x0c2fcdee)
	}
	if dst := binary.BigEndian.Uint32(payload[16:20]); dst != 0x0c3025ad {
		t.Errorf("destination address is %#08x, want %#08x", dst, 0x0c3025ad)
	}

	udp := payload[20:total]
	if sp, dp := binary.BigEndian.Uint16(udp[0:2]), binary.BigEndian.Uint16(udp[2:4]); sp != 4007 || dp != 4007 {
		t.Errorf("UDP ports are %d and %d, want 4007 and 4007", sp, dp)
	}
	if l := int(binary.BigEndian.Uint16(udp[4:6])); l != len(udp) {
		t.Errorf("UDP length field is %d, want %d", l, len(udp))
	}

	// The text itself is UTF-16 **little**-endian after a ten-octet Text
	// Messaging Service header. Both of those were first read off the hex by
	// eye as big-endian after twelve octets, and both were wrong; this test
	// failing is what said so. QSP does not parse TMS anywhere in the product
	// — this reads it only to prove the block offsets, which is what a fixture
	// is for.
	const tmsHeaderLen = 10
	body := udp[8+tmsHeaderLen:]
	units := make([]uint16, 0, len(body)/2)
	for i := 0; i+1 < len(body); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(body[i:i+2]))
	}
	const want = "I can't talk right now..."
	if got := string(utf16.Decode(units)); got != want {
		t.Errorf("the message reads %q, want %q", got, want)
	}
}

// TestABlockSurvivesTheBurstRoundTrip builds the burst QSP would transmit and
// reads it back.
//
// **This proves the wiring, not the trellis.** Encoding and decoding share the
// same tables, so a transcription error in them satisfies this test exactly as
// well as a correct one — which is how the constellation mapping stayed wrong
// through a whole session. What it does prove is that the block arrives
// unmangled through the rotation, the interleave, the burst layout and back,
// and that its CRC-9 still verifies at the far end, which no arrangement error
// would survive.
func TestABlockSurvivesTheBurstRoundTrip(t *testing.T) {
	const colourCode = 4
	for i, f := range allRate34Frames(t) {
		burst, err := dmrfec.BuildRate34Burst(colourCode, f.block)
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if len(burst) != dmrfec.BurstBytes {
			t.Fatalf("frame %d: burst is %d bytes, want %d", i, len(burst), dmrfec.BurstBytes)
		}
		cc, dt, ok := dmrfec.SlotTypeOf(burst)
		if !ok || cc != colourCode || dt != dmrfec.DataTypeRate34 {
			t.Errorf("frame %d: Slot Type reads colour %d type %#x", i, cc, dt)
		}
		if mid, ok := dmrfec.Middle(burst); !ok || mid != dmrfec.DataSyncBS {
			t.Errorf("frame %d: sync field is %#x, want %#x", i, mid, uint64(dmrfec.DataSyncBS))
		}

		back, order, ok := dmrfec.DecodeRate34Burst(burst)
		if !ok {
			t.Fatalf("frame %d: the burst QSP built did not decode", i)
		}
		if order != dmrfec.Rate34AirOrder {
			t.Errorf("frame %d: decoded as %s, want %s", i, order, dmrfec.Rate34AirOrder)
		}
		if !bytes.Equal(back, f.block) {
			t.Errorf("frame %d: round trip gave %x, want %x", i, back, f.block)
		}
	}
}

// TestACorruptedBurstIsRefusedRatherThanGuessedAt exists so the round-trip test
// above cannot pass by the decoder accepting anything it is handed.
//
// There is deliberately no error correction in the trellis decoder: a burst
// that arrives over IP has already been corrected by the repeater that heard
// it, and searching paths to guess at a member's message is worse than saying
// the burst was unreadable.
func TestACorruptedBurstIsRefusedRatherThanGuessedAt(t *testing.T) {
	f := allRate34Frames(t)[0]
	burst, err := dmrfec.BuildRate34Burst(4, f.block)
	if err != nil {
		t.Fatalf("%v", err)
	}
	refused, wrongOrder := 0, 0
	for bit := 0; bit < 98; bit++ {
		broken := bytes.Clone(burst)
		broken[bit/8] ^= 0x80 >> (bit % 8)
		back, order, ok := dmrfec.DecodeRate34Burst(broken)
		switch {
		case !ok:
			refused++
		case order == dmrfec.Rate34OrderUnknown || !bytes.Equal(back, f.block):
			wrongOrder++
		default:
			t.Fatalf("bit %d flipped and the burst decoded to the same block", bit)
		}
	}
	if refused+wrongOrder != 98 {
		t.Fatalf("only %d of 98 single-bit corruptions were caught", refused+wrongOrder)
	}
}

// TestRate34RotateIsTheOnlyDifferenceBetweenTheTwoArrangements pins the one
// step of the text path that is reasoned rather than measured, so that flipping
// Rate34AirOrder is a one-line change with a test that follows it.
func TestRate34RotateIsTheOnlyDifferenceBetweenTheTwoArrangements(t *testing.T) {
	block := allRate34Frames(t)[0].block
	air, err := dmrfec.Rate34Rotate(block, dmrfec.Rate34ControlLast)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if dmrfec.Rate34OrderOf(air) != dmrfec.Rate34ControlFirst {
		t.Fatal("a rotated block does not read as control-first")
	}
	back, err := dmrfec.Rate34Rotate(air, dmrfec.Rate34ControlFirst)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !bytes.Equal(back, block) {
		t.Errorf("rotating twice gave %x, want %x", back, block)
	}
	if _, err := dmrfec.Rate34Rotate(block[:4], dmrfec.Rate34ControlLast); err == nil {
		t.Error("a short block was rotated rather than refused")
	}
}
