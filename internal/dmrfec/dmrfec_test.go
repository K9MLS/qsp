package dmrfec_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

const voiceSession = "../../testdata/hbp/hbp-voice-session.pcap"

// dmrdBurst is one captured burst and the frame type the sender labelled it.
type dmrdBurst struct {
	Burst     []byte
	FrameType byte
	// Source and Destination come from the protocol header, so they can be
	// checked against what a burst's own Link Control says.
	Source      uint32
	Destination uint32
}

// readBursts pulls the DMR bursts out of captured Homebrew traffic.
//
// A DMRD message is a twenty-byte header, thirty-three bytes of burst, and two
// trailing bytes. Only the burst matters here.
func readBursts(tb testing.TB) []dmrdBurst {
	tb.Helper()
	raw, err := os.ReadFile(voiceSession)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != 0xa1b2c3d4 {
		tb.Fatalf("%s: not a little-endian microsecond pcap", voiceSession)
	}
	if link := binary.LittleEndian.Uint32(raw[20:24]); link != 276 {
		tb.Fatalf("%s: link type %d, expected 276 (LINUX_SLL2)", voiceSession, link)
	}

	var out []dmrdBurst
	off := 24
	for off+16 <= len(raw) {
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
		udp := ip[int(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		length := int(binary.BigEndian.Uint16(udp[4:6]))
		if length < 8 || length > len(udp) {
			continue
		}
		p := udp[8:length]
		if len(p) < 53 || !bytes.HasPrefix(p, []byte("DMRD")) {
			continue
		}
		out = append(out, dmrdBurst{
			Burst:       append([]byte(nil), p[20:53]...),
			FrameType:   (p[15] >> 4) & 0x3,
			Source:      uint32(p[5])<<16 | uint32(p[6])<<8 | uint32(p[7]),
			Destination: uint32(p[8])<<16 | uint32(p[9])<<8 | uint32(p[10]),
		})
	}
	if len(out) == 0 {
		tb.Fatalf("%s contained no DMRD frames", voiceSession)
	}
	return out
}

// isVoice reports whether a burst carries audio. Data-sync bursts are voice
// headers and terminators: they carry Link Control where the vocoder frames
// would be, and running the FEC over one is meaningless rather than wrong.
func (b dmrdBurst) isVoice() bool { return b.FrameType == 0 || b.FrameType == 1 }

// TestEveryCapturedVoiceBurstDecodesCleanly is the experiment this package was
// built on, kept as a test.
//
// If the interleave geometry or the scrambler were misread, the Golay syndromes
// would be noise and this number would be near zero. It is near a hundred, over
// thousands of frames of real speech that crossed a real radio link.
func TestEveryCapturedVoiceBurstDecodesCleanly(t *testing.T) {
	var frames, clean, corrected int
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		vf, ok := dmrfec.VocoderFrames(b.Burst)
		if !ok {
			t.Fatalf("a 33-byte burst was rejected")
		}
		for _, f := range vf {
			frames++
			_, n, ok := dmrfec.Decode(f)
			if !ok {
				continue
			}
			if n == 0 {
				clean++
			} else {
				corrected += n
			}
		}
	}
	if frames == 0 {
		t.Fatal("no voice frames")
	}
	pct := clean * 100 / frames
	t.Logf("%d vocoder frames, %d decoded with a zero syndrome (%d%%), %d bit errors corrected",
		frames, clean, pct, corrected)
	if pct < 95 {
		t.Errorf("only %d%% of real vocoder frames decoded cleanly; the interleave or the "+
			"scrambler is being misread, because a wrong reading scores near zero and a right "+
			"one scores near a hundred", pct)
	}
}

// TestTheRoundTripIsBitExact is the property a bridge depends on.
//
// Strip the FEC from a real burst, put it back, and the bytes must be identical.
// If they are not, then carrying Motorola audio onto a Homebrew network changes
// what a radio hears, and under *audio is king* that would end the idea.
func TestTheRoundTripIsBitExact(t *testing.T) {
	var checked, mismatched int
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		vf, _ := dmrfec.VocoderFrames(b.Burst)
		rebuilt := make([][]byte, len(vf))
		skip := false
		for i, f := range vf {
			p, corrected, ok := dmrfec.Decode(f)
			// A frame that arrived with errors is corrected, so re-encoding it
			// produces what the radio *sent* rather than what was captured.
			// That is the FEC working, not a fault, and it cannot be compared.
			if !ok || corrected > 0 {
				skip = true
				break
			}
			rebuilt[i] = dmrfec.Encode(p)
		}
		if skip {
			continue
		}
		middle, _ := dmrfec.Middle(b.Burst)
		out, ok := dmrfec.AssembleBurst(rebuilt, middle)
		if !ok {
			t.Fatal("assembly rejected three 72-bit frames")
		}
		checked++
		if !bytes.Equal(out, b.Burst) {
			mismatched++
			if mismatched == 1 {
				t.Errorf("round trip changed a burst:\n  in  %x\n  out %x", b.Burst, out)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no clean bursts to round trip")
	}
	t.Logf("%d uncorrupted bursts round tripped, %d changed", checked, mismatched)
	if mismatched != 0 {
		t.Errorf("%d of %d bursts changed; the transformation is not lossless", mismatched, checked)
	}
}

// TestTheSyncPatternIsWhereTheStandardSaysItIs checks the burst layout that
// everything else rests on.
//
// A superframe is 360 ms of six 60 ms frames, and only its first burst carries
// the synchronisation pattern. One in six is therefore the number to expect,
// and finding it is what proves the payload is two halves around a middle
// rather than one continuous run.
func TestTheSyncPatternIsWhereTheStandardSaysItIs(t *testing.T) {
	var voice, synced int
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		voice++
		if m, _ := dmrfec.Middle(b.Burst); m == dmrfec.VoiceSyncBS {
			synced++
		}
	}
	if synced == 0 {
		t.Fatal("no burst carried the base-station voice sync pattern at bits 108 to 155")
	}
	ratio := float64(synced) / float64(voice)
	t.Logf("%d of %d voice bursts carry sync, one in %.1f", synced, voice, 1/ratio)
	if ratio < 0.10 || ratio > 0.25 {
		t.Errorf("sync appears in one burst in %.1f; a six-frame superframe should give one in six", 1/ratio)
	}
}

// TestGolayCorrectsUpToThreeErrors exercises the codes directly, because the
// fixtures are clean and a corrector that never corrected would pass every test
// above.
func TestGolayCorrectsUpToThreeErrors(t *testing.T) {
	for data := uint32(0); data < 4096; data += 37 {
		code := dmrfec.EncodeGolay23(data)
		for _, pattern := range []uint32{0, 1, 0b101, 0b1000000010001} {
			got, n := dmrfec.DecodeGolay23(code ^ pattern)
			if got != data {
				t.Fatalf("data %#x with error %#b decoded as %#x", data, pattern, got)
			}
			if want := popcount(pattern); n != want {
				t.Errorf("data %#x: reported %d corrections, want %d", data, n, want)
			}
		}
	}
}

// TestEncodeDecodeRoundTripsEveryParameterFrame covers the parameter space the
// captures do not reach.
func TestEncodeDecodeRoundTripsEveryParameterFrame(t *testing.T) {
	for i := 0; i < 5000; i++ {
		p := dmrfec.Parameters(uint64(i) * 0x1F3D5B79)
		p &= (1 << 49) - 1
		frame := dmrfec.Encode(p)
		if len(frame) != dmrfec.ProtectedBits {
			t.Fatalf("encoded frame is %d bits, want %d", len(frame), dmrfec.ProtectedBits)
		}
		got, corrected, ok := dmrfec.Decode(frame)
		if !ok {
			t.Fatalf("a frame this package encoded was rejected by its own decoder")
		}
		if corrected != 0 {
			t.Errorf("%d corrections on a frame with no errors", corrected)
		}
		if got != p {
			t.Fatalf("round trip changed the parameters: %#x became %#x", uint64(p), uint64(got))
		}
	}
}

func popcount(v uint32) int {
	n := 0
	for v != 0 {
		v &= v - 1
		n++
	}
	return n
}

func ExampleDecode() {
	frame := dmrfec.Encode(dmrfec.Parameters(0x1234567890A))
	p, corrected, ok := dmrfec.Decode(frame)
	fmt.Printf("%#x %d %v\n", uint64(p), corrected, ok)
	// Output: 0x1234567890a 0 true
}
