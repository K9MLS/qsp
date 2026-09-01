package dmrfec_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

const probeVoice = "../../testdata/ipsc/ipsc-probe-voice.pcap"

// ipscCores returns the vocoder payload of every IPSC voice frame in the probe
// capture: nineteen bytes from offset 33 of each 0x80 message whose frame class
// is voice.
func ipscCores(tb testing.TB) [][]byte {
	tb.Helper()
	raw, err := os.ReadFile(probeVoice)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out [][]byte
	off := 24
	for off+16 <= len(raw) {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 34 || binary.BigEndian.Uint16(rec[12:14]) != 0x0800 {
			continue
		}
		ip := rec[14:]
		if ip[9] != 17 {
			continue
		}
		udp := ip[int(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		p := udp[8:int(binary.BigEndian.Uint16(udp[4:6]))]
		if len(p) < 52 || p[0] != 0x80 || p[30] != 0x8a {
			continue
		}
		out = append(out, append([]byte(nil), p[33:52]...))
	}
	if len(out) == 0 {
		tb.Fatalf("%s contained no IPSC voice payloads", probeVoice)
	}
	return out
}

// TestMotorolaAndHomebrewAgreeOnSilence is the observation that closed the
// audio path, and the strongest evidence in this package.
//
// A Motorola XPR8300 speaking IP Site Connect and an MMDVM hotspot speaking
// Homebrew were captured on different days, on different equipment, in
// different protocols. Unpack the Motorola payload and strip the FEC from the
// Homebrew burst, and both produce the same 49-bit vocoder frame for silence.
//
// One observation confirms three things at once: that the IPSC core is three
// 50-bit slots with the spare bit trailing, that this package's FEC decode is
// right, and that the two protocols carry the same audio. Any of those being
// wrong would break it.
func TestMotorolaAndHomebrewAgreeOnSilence(t *testing.T) {
	fromHomebrew := map[dmrfec.Parameters]int{}
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		vf, _ := dmrfec.VocoderFrames(b.Burst)
		for _, f := range vf {
			if p, n, ok := dmrfec.Decode(f); ok && n == 0 {
				fromHomebrew[p]++
			}
		}
	}

	var matched, total int
	for _, core := range ipscCores(t) {
		frames, ok := dmrfec.UnpackIPSCCore(core)
		if !ok {
			t.Fatalf("a 19-byte core was rejected")
		}
		for _, f := range frames {
			total++
			if fromHomebrew[f] > 0 {
				matched++
			}
		}
	}
	t.Logf("%d of %d Motorola vocoder frames also appear in the Homebrew captures", matched, total)
	if matched == 0 {
		t.Fatal("no Motorola vocoder frame appears in the Homebrew captures; the IPSC packing, " +
			"the FEC, or the claim that both protocols carry the same audio is wrong")
	}
}

// TestTheIPSCCoreRoundTrips is the property a bridge depends on in the
// direction that has no capture to check it against.
func TestTheIPSCCoreRoundTrips(t *testing.T) {
	for i, core := range ipscCores(t) {
		frames, ok := dmrfec.UnpackIPSCCore(core)
		if !ok {
			t.Fatalf("core %d rejected", i)
		}
		out := dmrfec.PackIPSCCore(frames)
		if !bytes.Equal(out, core) {
			t.Fatalf("core %d changed:\n  in  %x\n  out %x", i, core, out)
		}
	}
}

// TestABurstBuiltFromMotorolaAudioSurvivesTheReturnTrip checks the conversion
// both ways over real Motorola audio.
//
// This is what a Homebrew peer would receive when a Motorola repeater
// transmits, taken apart again. Nothing may change on the way.
func TestABurstBuiltFromMotorolaAudioSurvivesTheReturnTrip(t *testing.T) {
	for i, core := range ipscCores(t) {
		burst, ok := dmrfec.BurstFromIPSC(core, dmrfec.VoiceSyncBS)
		if !ok {
			t.Fatalf("core %d: burst assembly failed", i)
		}
		if len(burst) != dmrfec.BurstBytes {
			t.Fatalf("core %d: burst is %d bytes, want %d", i, len(burst), dmrfec.BurstBytes)
		}
		if m, _ := dmrfec.Middle(burst); m != dmrfec.VoiceSyncBS {
			t.Errorf("core %d: sync pattern did not survive assembly", i)
		}
		back, corrected, ok := dmrfec.IPSCFromBurst(burst)
		if !ok {
			t.Fatalf("core %d: the burst this package built was rejected by its own reader", i)
		}
		if corrected != 0 {
			t.Errorf("core %d: %d corrections on a burst with no errors", i, corrected)
		}
		if !bytes.Equal(back, core) {
			t.Fatalf("core %d changed through a burst:\n  in  %x\n  out %x", i, core, back)
		}
	}
}

// TestARealHomebrewBurstConvertsToIPSCAndBack covers the other direction with
// the only real bursts available.
func TestARealHomebrewBurstConvertsToIPSCAndBack(t *testing.T) {
	var checked int
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		core, corrected, ok := dmrfec.IPSCFromBurst(b.Burst)
		if !ok || corrected > 0 {
			// A corrected burst yields what the radio sent rather than what
			// was captured, so it cannot be compared byte for byte.
			continue
		}
		middle, _ := dmrfec.Middle(b.Burst)
		out, ok := dmrfec.BurstFromIPSC(core, middle)
		if !ok {
			t.Fatal("a core this package produced was rejected by its own builder")
		}
		checked++
		if !bytes.Equal(out, b.Burst) {
			t.Fatalf("a real burst changed on the way to IPSC and back:\n  in  %x\n  out %x", b.Burst, out)
		}
	}
	if checked == 0 {
		t.Fatal("no clean bursts to convert")
	}
	t.Logf("%d real Homebrew bursts converted to the Motorola payload and back unchanged", checked)
}

// TestTheSlotIsFiftyBitsNotFortyNine records the measurement that overturned
// the obvious reading.
//
// Three 49-bit frames would pack into 147 bits with a stride of 49. The stride
// is 50: in a silence core, where one vocoder frame repeats, bits 0 to 99 match
// bits 50 to 149 exactly and no other offset matches at all.
func TestTheSlotIsFiftyBitsNotFortyNine(t *testing.T) {
	if dmrfec.IPSCSlotBits != 50 {
		t.Fatalf("slot is %d bits; the capture says 50", dmrfec.IPSCSlotBits)
	}
	// A silence core, where all three frames are the same.
	core := []byte{0xf8, 0x01, 0xa9, 0x9f, 0x8c, 0xe0, 0xbe, 0x00, 0x6a, 0x67,
		0xe3, 0x38, 0x2f, 0x80, 0x1a, 0x99, 0xf8, 0xce, 0x08}
	frames, ok := dmrfec.UnpackIPSCCore(core)
	if !ok {
		t.Fatal("the silence core was rejected")
	}
	if frames[0] != frames[1] || frames[1] != frames[2] {
		t.Errorf("a silence core unpacked to three different frames: %#x %#x %#x",
			uint64(frames[0]), uint64(frames[1]), uint64(frames[2]))
	}
	const homebrewSilence = dmrfec.Parameters(0x1F003533F19C1)
	if frames[0] != homebrewSilence {
		t.Errorf("Motorola silence unpacked to %#x; the Homebrew captures say %#x",
			uint64(frames[0]), uint64(homebrewSilence))
	}
}
