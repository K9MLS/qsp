package dmrfec_test

import (
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
