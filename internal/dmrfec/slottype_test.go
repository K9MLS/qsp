package dmrfec_test

import (
	"encoding/binary"
	"math/bits"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// TestTheSlotTypeNibblesAreTheSameOnAirAndOverIPSC pins the packing that two
// paths now share.
//
// On the air the Slot Type is twenty bits — eight of colour code and data type,
// twelve of Golay parity. Over IP Site Connect it is the eight alone, written
// at byte 51 with no parity. Both must agree about which nibble is which, or
// ADR-0045's finding that byte 51 matches the frame's own data type in 153 of
// 162 captured frames stops meaning anything.
func TestTheSlotTypeNibblesAreTheSameOnAirAndOverIPSC(t *testing.T) {
	for cc := uint8(0); cc <= 15; cc++ {
		for dt := uint8(0); dt <= 15; dt++ {
			octet := dmrfec.SlotTypeInfo(cc, dt)
			if got := octet >> 4; got != cc {
				t.Fatalf("colour code %d came back %d; it belongs in the high nibble", cc, got)
			}
			if got := octet & 0x0f; got != dt {
				t.Fatalf("data type %#x came back %#x; it belongs in the low nibble", dt, got)
			}

			// The air interface must pack the same eight bits above its parity.
			full, err := dmrfec.SlotType(cc, dt)
			if err != nil {
				t.Fatalf("SlotType(%d, %#x): %v", cc, dt, err)
			}
			if got := uint8(full >> 12); got != octet {
				t.Fatalf("the air interface packs %#02x where IPSC packs %#02x "+
					"for colour code %d data type %#x", got, octet, cc, dt)
			}
		}
	}
}

// TestARealCapturedSlotTypeIsReproduced checks the packing against a radio
// rather than against itself.
//
// Byte 51 of the first voice header in ipsc-master-voice.pcap reads 0x41 from
// an XPR8300 on colour code 4, and 0x42 on its terminator.
func TestARealCapturedSlotTypeIsReproduced(t *testing.T) {
	const colourCode = 4
	for _, c := range []struct {
		dataType uint8
		want     uint8
		what     string
	}{
		{0x1, 0x41, "a voice LC header"},
		{0x2, 0x42, "a terminator with Link Control"},
		{0x3, 0x43, "a CSBK, as every text burst in ipsc-text.pcap carries"},
	} {
		if got := dmrfec.SlotTypeInfo(colourCode, c.dataType); got != c.want {
			t.Errorf("%s packs %#02x, and a real repeater sent %#02x",
				c.what, got, c.want)
		}
	}
}

// TestTheSlotTypeCodeHasTheDistanceAGolayCodeMust is a property no capture
// can supply: every nonzero codeword of a Golay (20,8) code differs from zero
// in at least seven places, and this shortened extended one in eight. A wrong
// generator row usually breaks that, and one did — see golay208Rows — while
// the capture test below could not see it for want of bursts using that row.
//
// To see it fail: change any bit of any row of golay208Rows.
func TestTheSlotTypeCodeHasTheDistanceAGolayCodeMust(t *testing.T) {
	least := 20
	for info := 1; info < 256; info++ {
		w, err := dmrfec.SlotType(uint8(info>>4), uint8(info&0x0f))
		if err != nil {
			t.Fatalf("%#02x: %v", info, err)
		}
		least = min(least, bits.OnesCount32(w))
	}
	if least != 8 {
		t.Errorf("minimum distance %d, want 8", least)
	}
}

// TestEveryHotspotDataBurstsSlotTypeIsRebuilt holds SlotType to every data
// burst a hotspot sent in two captures: preambles (3), data headers (6),
// Rate 1/2 blocks (7) and Rate 3/4 blocks (8), 318 bursts in all. Data types
// 6 and 7 are the ones the old table got wrong.
//
// To see it fail: restore the sixth row of golay208Rows to 0b101010100111,
// and the headers and Rate 1/2 blocks fail while the preambles pass.
func TestEveryHotspotDataBurstsSlotTypeIsRebuilt(t *testing.T) {
	counts := map[uint8]int{}
	for _, path := range []string{
		"../../testdata/hbp/hbp-text-preambles.pcap",
		"../../testdata/hbp/hbp-text-rate34.pcap",
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%v", err)
		}
		for off := 24; off+16 <= len(raw); {
			incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
			off += 16
			if off+incl > len(raw) {
				break
			}
			rec := raw[off : off+incl]
			off += incl
			if len(rec) < 48 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
				continue
			}
			ip := rec[20:]
			if ip[9] != 17 || binary.BigEndian.Uint32(ip[12:16]) != 0xc0a8019b {
				continue
			}
			d := ip[int(ip[0]&0x0f)*4+8:]
			// DMRD with the data-sync frame type: the flags byte's bits 5
			// and 4 read 10.
			if len(d) < 53 || string(d[:4]) != "DMRD" || d[15]>>4&0x3 != 0x2 {
				continue
			}
			burst := d[20:53]
			cc, dt, _ := dmrfec.SlotTypeOf(burst)
			built, err := dmrfec.SlotType(cc, dt)
			if err != nil {
				t.Fatalf("%v", err)
			}
			b := dmrfec.BurstBitsFrom(burst)
			var want uint32
			for j := range 10 {
				want = want<<1 | uint32(b[98+j])
			}
			for j := range 10 {
				want = want<<1 | uint32(b[156+j])
			}
			if built != want {
				t.Errorf("%s: colour code %d data type %#x: built %#05x, the hotspot sent %#05x",
					path, cc, dt, built, want)
			}
			counts[dt]++
		}
	}
	want := map[uint8]int{0x3: 240, 0x6: 19, 0x7: 5, 0x8: 54}
	for dt, n := range want {
		if counts[dt] != n {
			t.Errorf("data type %#x: %d bursts, want %d", dt, counts[dt], n)
		}
	}
}
