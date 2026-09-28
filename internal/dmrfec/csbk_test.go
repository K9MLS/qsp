package dmrfec_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// preambleFixture holds one whole text from a hotspot: sixteen preamble CSBKs,
// a data header, and five Rate 1/2 content blocks.
const preambleFixture = "../../testdata/hbp/hbp-text-preambles.pcap"

// privateFixture holds three private texts from the same hotspot, each
// opening with its own preambles.
const privateFixture = "../../testdata/hbp/hbp-text-rate34.pcap"

// hotspotBursts returns the bursts of one data type from the preamble capture.
func hotspotBursts(tb testing.TB, dataType uint8) [][]byte {
	tb.Helper()
	return hotspotBurstsIn(tb, preambleFixture, dataType)
}

// hotspotBurstsIn returns the bursts of one data type that the hotspot sent,
// from any capture of it.
func hotspotBurstsIn(tb testing.TB, path string, dataType uint8) [][]byte {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out [][]byte
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
		// The hotspot's own frames only.
		if binary.BigEndian.Uint32(ip[12:16]) != 0xc0a8019b {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		d := udp[8:]
		if len(d) != 55 || string(d[:4]) != "DMRD" || d[15]&0x0f != dataType {
			continue
		}
		out = append(out, append([]byte(nil), d[20:20+dmrfec.BurstBytes]...))
	}
	return out
}

// TestEveryPreambleCarriesTheSameOpcode is the measurement the suppression
// rests on.
//
// One text put two rows in Last heard and the longer, more prominent of them
// carried no message: sixteen preamble CSBKs, each with its own stream ID, over
// 1.87 seconds. **Naming a preamble by its opcode rather than by its shape in
// the stream is what lets a radio check, a call alert and a remote monitor keep
// their rows** — and the last of those makes somebody's radio transmit without
// its operator knowing.
func TestEveryPreambleCarriesTheSameOpcode(t *testing.T) {
	bursts := hotspotBursts(t, 0x3)
	if len(bursts) != 16 {
		t.Fatalf("read %d CSBK bursts from the fixture, want 16", len(bursts))
	}
	for i, b := range bursts {
		c, ok := dmrfec.CSBKOf(b)
		if !ok {
			t.Fatalf("preamble %d did not decode", i)
		}
		if c.Opcode != dmrfec.CSBKPreamble {
			t.Errorf("preamble %d carries opcode %d, want %d",
				i, c.Opcode, dmrfec.CSBKPreamble)
		}
		if c.FeatureID != dmrfec.CSBKFeatureStandard {
			t.Errorf("preamble %d carries feature ID %d, want the standard set",
				i, c.FeatureID)
		}
		if !c.IsPreamble() {
			t.Errorf("preamble %d does not read as one", i)
		}
	}
}

// TestAnotherOpcodeIsNotAPreamble is here because the test above only asserts
// that something is recognised, and a function returning true for everything
// would satisfy it.
//
// No capture in this repository holds a radio check, a call alert or a remote
// monitor, so these are constructed: the opcode field is the low six bits of
// the block's first octet, and every value that is not 61 has to fall through.
func TestAnotherOpcodeIsNotAPreamble(t *testing.T) {
	for op := 0; op < 64; op++ {
		c := dmrfec.CSBK{Opcode: uint8(op), FeatureID: dmrfec.CSBKFeatureStandard}
		if got := c.IsPreamble(); got != (uint8(op) == dmrfec.CSBKPreamble) {
			t.Errorf("opcode %d reads as preamble=%v", op, got)
		}
	}
	// **The feature ID is checked too.** An opcode means something only inside
	// its feature set, and treating 61 under a manufacturer's extension as a
	// preamble would hide a message this package has never seen.
	vendor := dmrfec.CSBK{Opcode: dmrfec.CSBKPreamble, FeatureID: 0x10}
	if vendor.IsPreamble() {
		t.Error("opcode 61 under a manufacturer's feature ID was taken for a preamble")
	}
}

// TestCorruptionNeverInventsAPreamble pins the direction the doubt falls in.
//
// When QSP cannot tell what a block is, the console shows it: suppressing an
// unreadable burst is the one way this feature could make a command invisible.
// So the property is not that corruption is always detected — the FEC corrects
// much of it, and a burst that decodes to opcode 60 is a burst to record, which
// is the right outcome. **The property is that damage never turns something
// else into a preamble.**
//
// The first version of this test asserted that any corrupted burst failing to
// read as a preamble was a failure, which is the opposite of what the code
// should do, and it duly failed on the first burst the FEC corrected into
// opcode 60. A test written from the shape of the code rather than from the
// property it protects: the third of those today.
func TestCorruptionNeverInventsAPreamble(t *testing.T) {
	// The data header and the message blocks are not control blocks at all.
	// Damaging one must not produce something the tracker would hide.
	var checked int
	for _, dataType := range []uint8{0x6, 0x7} {
		for _, burst := range hotspotBursts(t, dataType) {
			for bit := 0; bit < 98; bit++ {
				broken := append([]byte(nil), burst...)
				broken[bit/8] ^= 0x80 >> (bit % 8)
				if c, ok := dmrfec.CSBKOf(broken); ok && c.IsPreamble() {
					t.Fatalf("a damaged data burst read as a preamble: %x", broken)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("nothing was checked; the fixture reader is broken")
	}

	// And a burst of nothing is not a preamble.
	if c, ok := dmrfec.CSBKOf(make([]byte, dmrfec.BurstBytes)); ok && c.IsPreamble() {
		t.Error("an empty burst read as a preamble")
	}
}

// TestTheMessageBlocksAreNotCSBKs checks the other side of the split: the five
// bursts that carry the text are Rate 1/2 data, not control blocks.
//
// A short message fits the twelve-octet blocks BPTC carries, so both codings
// are in use on this network — ADR-0047's Rate 3/4 work and this.
func TestTheMessageBlocksAreNotCSBKs(t *testing.T) {
	if n := len(hotspotBursts(t, 0x7)); n != 5 {
		t.Errorf("the fixture holds %d Rate 1/2 blocks, want 5", n)
	}
	if n := len(hotspotBursts(t, 0x6)); n != 1 {
		t.Errorf("the fixture holds %d data headers, want 1", n)
	}
}

// TestEveryCapturedPreambleRebuilds is the evidence for [dmrfec.CSBKCRC] and
// [dmrfec.BuildPreamble]: parse each captured preamble, build one from what
// was parsed, and require the same twelve octets and the same coded burst.
//
// The group capture must also count down: 21 bursts to come on the first
// preamble and 6 on the last, which is the data header and five blocks.
//
// To see it fail: change csbkCRCMask to 0xa5a5 (ETSI's mask without the
// inversion) and every row fails; drop csbkPreambleGroup and only the group
// row fails; swap To and From in BuildPreamble and both fail.
func TestEveryCapturedPreambleRebuilds(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		want  int
		group bool
	}{
		{"a group text", preambleFixture, 16, true},
		{"three private texts", privateFixture, 224, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bursts := hotspotBurstsIn(t, tc.path, 0x3)
			if len(bursts) != tc.want {
				t.Fatalf("read %d preamble bursts, want %d", len(bursts), tc.want)
			}
			for i, burst := range bursts {
				payload, _, ok := dmrfec.DecodeBPTC(burst)
				if !ok {
					t.Fatalf("burst %d does not decode", i)
				}
				block := dmrfec.BurstBytesFrom(payload)[:dmrfec.CSBKBytes]
				p, err := dmrfec.ParsePreamble(block)
				if err != nil {
					t.Fatalf("burst %d: %v", i, err)
				}
				if p.Group != tc.group {
					t.Errorf("burst %d: group %v, want %v", i, p.Group, tc.group)
				}
				rebuilt, err := dmrfec.BuildPreamble(p)
				if err != nil {
					t.Fatalf("burst %d: %v", i, err)
				}
				if !bytes.Equal(rebuilt, block) {
					t.Errorf("burst %d rebuilt differs\n have %x\n want %x", i, rebuilt, block)
				}
				cc, _, _ := dmrfec.SlotTypeOf(burst)
				coded, err := dmrfec.BuildDataBurstFromBlock(cc, 0x3, rebuilt)
				if err != nil {
					t.Fatalf("burst %d: coding: %v", i, err)
				}
				if !bytes.Equal(coded, burst) {
					t.Errorf("burst %d coded differs\n have %x\n want %x", i, coded, burst)
				}
				if tc.group {
					if want := uint8(21 - i); p.BlocksToFollow != want {
						t.Errorf("preamble %d counts %d bursts to come, want %d", i, p.BlocksToFollow, want)
					}
				}
			}
		})
	}
}

// TestAPreambleThatDoesNotVerifyIsRefused covers what ParsePreamble must
// turn away. A CRC-16 catches every single-bit error, so each flip is a row
// it cannot miss.
func TestAPreambleThatDoesNotVerifyIsRefused(t *testing.T) {
	good, err := dmrfec.BuildPreamble(dmrfec.Preamble{BlocksToFollow: 6, To: 2, From: 3132910, Group: true})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if _, err := dmrfec.ParsePreamble(good); err != nil {
		t.Fatalf("a freshly built preamble does not parse: %v", err)
	}
	for bit := range dmrfec.CSBKBytes * 8 {
		bad := bytes.Clone(good)
		bad[bit/8] ^= 0x80 >> (bit % 8)
		if _, err := dmrfec.ParsePreamble(bad); err == nil {
			t.Errorf("a flip of bit %d went unnoticed", bit)
		}
	}

	notPreamble := bytes.Clone(good)
	notPreamble[0] = 0x80 | 4 // last block, opcode 4; the CRC is repaired below
	crc, _ := dmrfec.CSBKCRC(notPreamble)
	notPreamble[10], notPreamble[11] = byte(crc>>8), byte(crc)

	for _, tc := range []struct {
		name  string
		block []byte
	}{
		{"a verified CSBK that is not a preamble", notPreamble},
		{"eleven octets", good[:11]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := dmrfec.ParsePreamble(tc.block); err == nil {
				t.Error("parsed without complaint")
			}
		})
	}
	if _, err := dmrfec.BuildPreamble(dmrfec.Preamble{To: 1 << 24}); err == nil {
		t.Error("built a preamble to a 25-bit destination")
	}
}
