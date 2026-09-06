package ipscbridge_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// outboundFixture is what QSP put on the wire once the text path worked.
//
// It is kept beside `testdata/ipsc/ipsc-text-outbound.pcap`, which is the same
// path recorded while it was broken: thirty-three preambles, three data
// headers and no content at all. The pair is the before and after.
const outboundFixture = "../../testdata/ipsc/ipsc-text-rate34-out.pcap"

// TestQSPsOwnRate34DatagramsReadBack parses the datagrams QSP transmitted in
// production and checks them against the layout measured from a repeater's.
//
// **These bytes came off a live network, not out of a test.** A field written
// at the wrong offset survives every round-trip test in this repository,
// because the same constant places it and reads it back; it does not survive
// being compared with what a Motorola repeater sends.
func TestQSPsOwnRate34DatagramsReadBack(t *testing.T) {
	raw, err := os.ReadFile(outboundFixture)
	if err != nil {
		t.Fatalf("%v", err)
	}

	var serials []uint8
	var seen int
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		// Only what QSP sent, which is what this fixture is for.
		if binary.BigEndian.Uint32(ip[12:16]) != 0xc0a801f7 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		d := udp[8:]
		if len(d) != ipsc.TextRate34Total || !ipsc.Kind(d[0]).IsText() {
			continue
		}
		seen++

		if d[ipsc.TextDataTypeAt] != dmrfec.DataTypeRate34 {
			t.Errorf("datagram %d: byte 30 reads %#x", seen, d[ipsc.TextDataTypeAt])
		}
		if got := [6]byte(d[32:38]); got != ipsc.HeaderConstantsRate34 {
			t.Errorf("datagram %d: constants read %x", seen, got)
		}
		slotTypeAt := ipsc.TextSlotTypeFor(len(d))
		if d[slotTypeAt-1] != 0 {
			t.Errorf("datagram %d: the octet before the Slot Type is %#x", seen, d[slotTypeAt-1])
		}
		if d[slotTypeAt]&0x0f != dmrfec.DataTypeRate34 {
			t.Errorf("datagram %d: Slot Type reads %#x", seen, d[slotTypeAt])
		}

		m, err := ipsc.Parse(d)
		if err != nil {
			t.Fatalf("datagram %d did not parse: %v", seen, err)
		}
		txt, ok := m.AsText()
		if !ok {
			t.Fatalf("datagram %d did not read back as text", seen)
		}
		if len(txt.Block) != dmrfec.Rate34BlockBytes {
			t.Fatalf("datagram %d yielded a %d-octet block", seen, len(txt.Block))
		}
		serial, verified := dmrfec.Rate34Serial(txt.Block)
		if verified {
			serials = append(serials, serial)
		}
	}

	if seen != 12 {
		t.Fatalf("found %d Rate 3/4 datagrams from QSP, want 12", seen)
	}
	// **The sequence is 0, 1, 2, 3 and then the last block eight more times.**
	// It is not four blocks repeated three times, which is what this test
	// asserted when it was written from an assumption rather than from the
	// capture. A master owes no acknowledgement — ADR-0045's amendment — so
	// the sending end repeats its final block until it gives up, and that is
	// what a correct network looks like rather than a defect.
	if len(serials) < 4 {
		t.Fatalf("only %d blocks verified their CRC-9", len(serials))
	}
	for i, want := range []uint8{0, 1, 2, 3} {
		if serials[i] != want {
			t.Errorf("block %d carries serial %d, want %d", i, serials[i], want)
		}
	}
	for i, s := range serials[4:] {
		if s != 3 {
			t.Errorf("repeat %d carries serial %d, want 3", i, s)
		}
	}
	// One of the twelve fails its CRC-9 and was transmitted anyway. That is
	// ADR-0047's decision to carry rather than drop, exercised in production
	// rather than in a fixture: QSP does not silently discard a member's
	// message because a bit arrived wrong somewhere upstream.
	if len(serials) != 11 {
		t.Errorf("%d of 12 blocks verified; want 11, one corrupted repeat", len(serials))
	}
}
