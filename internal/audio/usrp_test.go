package audio

import (
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

// USRP, against the reference implementation's own byte layout.
//
// DVSwitch's USRP_Audio builds a keyup as the four bytes "USRP" followed by
// `struct.pack('>iiiiiii', seq, 0, ptt, 0, 0, 0, 0)` — seven big-endian
// 32-bit fields — and reads audio from offset 32. That is the citation these
// tests assert against, transcribed rather than derived from the code under
// test.

// TestTheHeaderIsTheThirtyTwoBytesTheReferenceBuilds.
func TestTheHeaderIsTheThirtyTwoBytesTheReferenceBuilds(t *testing.T) {
	if HeaderBytes != 4+7*4 {
		t.Fatalf("the header is %d bytes; four of magic and seven 32-bit fields "+
			"is %d", HeaderBytes, 4+7*4)
	}
	if FrameBytes != HeaderBytes+SamplesPerFrame*2 {
		t.Fatalf("a frame is %d bytes and the header plus 160 16-bit samples "+
			"is %d", FrameBytes, HeaderBytes+SamplesPerFrame*2)
	}

	// A keyup on sequence 7, talkgroup 2. Built by hand from the reference's
	// own field order: magic, seq, memory, ptt, talkgroup, type, mpxid,
	// reserved.
	want := "55535250" + // "USRP"
		"00000007" + // seq
		"00000000" + // memory
		"00000001" + // ptt
		"00000002" + // talkgroup
		"00000000" + // type: voice
		"00000000" + // mpxid
		"00000000" // reserved

	got, err := Keyup(7, 2)
	if err != nil {
		t.Fatalf("building a keyup: %v", err)
	}
	if hex.EncodeToString(got) != want {
		t.Errorf("a keyup is\n  %s\nand the reference's fields give\n  %s",
			hex.EncodeToString(got), want)
	}
	if len(got) != HeaderBytes {
		t.Errorf("a keyup is %d bytes, want %d with no audio", len(got), HeaderBytes)
	}

	// And a release differs from it in exactly one field.
	rel, err := Release(8, 2)
	if err != nil {
		t.Fatalf("building a release: %v", err)
	}
	if binary.BigEndian.Uint32(rel[12:16]) != 0 {
		t.Error("a release does not clear the PTT field")
	}
	if binary.BigEndian.Uint32(rel[16:20]) != 2 {
		t.Error("a release does not carry the talkgroup")
	}
}

// TestAudioIsLittleEndianAndTheHeaderIsBig is the asymmetry that will trip
// somebody.
//
// The header's fields are big-endian, because the reference packs them with
// `>`. The audio is 16-bit PCM, which is little-endian everywhere it is
// produced or consumed — a WAV file, a sound card, the AMBE-3000's own speech
// packets are the exception at big-endian, and converting between them is
// exactly the kind of thing that works on a bench and sounds like static on
// air.
//
// **So the two live in one datagram with opposite byte orders**, and this test
// exists to make that deliberate rather than discovered.
func TestAudioIsLittleEndianAndTheHeaderIsBig(t *testing.T) {
	samples := make([]int16, SamplesPerFrame)
	samples[0] = 0x0102
	samples[1] = -2

	f, err := Encode(Frame{Sequence: 0x01020304, PTT: true, Talkgroup: 9, Samples: samples})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	// Header: big-endian, so the sequence reads in order.
	if got := hex.EncodeToString(f[4:8]); got != "01020304" {
		t.Errorf("the sequence field is %s, want 01020304 big-endian", got)
	}
	// Audio: little-endian, so 0x0102 is on the wire as 02 01.
	if got := hex.EncodeToString(f[HeaderBytes : HeaderBytes+2]); got != "0201" {
		t.Errorf("sample 0 is %s on the wire, want 0201 little-endian", got)
	}
	if got := hex.EncodeToString(f[HeaderBytes+2 : HeaderBytes+4]); got != "feff" {
		t.Errorf("sample 1 is %s on the wire, want feff for -2 little-endian", got)
	}

	// And it survives the round trip with its sign.
	back, ok := Decode(f)
	if !ok {
		t.Fatal("a frame this package built did not decode")
	}
	if back.Samples[0] != 0x0102 || back.Samples[1] != -2 {
		t.Errorf("samples came back as %d and %d", back.Samples[0], back.Samples[1])
	}
	if back.Sequence != 0x01020304 || !back.PTT || back.Talkgroup != 9 {
		t.Errorf("the header came back as %+v", Frame{
			Sequence: back.Sequence, PTT: back.PTT, Talkgroup: back.Talkgroup,
		})
	}
}

// TestAFrameOfTheWrongLengthIsRefusedRatherThanPadded.
//
// The far side reads a fixed-size payload from offset 32. A short frame is not
// rejected there — it is interpreted, and whatever followed the audio becomes
// samples. So it is refused here, where the error can name the problem.
func TestAFrameOfTheWrongLengthIsRefusedRatherThanPadded(t *testing.T) {
	for _, n := range []int{1, 159, 161, 320} {
		_, err := Encode(Frame{Samples: make([]int16, n)})
		if err == nil {
			t.Errorf("a frame of %d samples was encoded; USRP carries %d",
				n, SamplesPerFrame)
			continue
		}
		if !strings.Contains(err.Error(), "read as a different message") {
			t.Errorf("the refusal for %d samples does not say what goes wrong: %v", n, err)
		}
	}
	// Zero is a keyup or a release, not an error.
	if _, err := Encode(Frame{PTT: true}); err != nil {
		t.Errorf("a keyup with no audio was refused: %v", err)
	}
}

// TestSomethingThatIsNotUsrpVoiceIsRefused.
//
// USRP carries message types this package does not implement, and the package
// comment says why: the only citation it has is the header layout and the
// voice type. **Reading a metadata packet's fields as audio would put whatever
// it contained into somebody's receiver**, which is the loudest possible
// failure.
func TestSomethingThatIsNotUsrpVoiceIsRefused(t *testing.T) {
	good, err := Encode(Frame{Sequence: 1, PTT: true, Samples: make([]int16, SamplesPerFrame)})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	notVoice := append([]byte(nil), good...)
	binary.BigEndian.PutUint32(notVoice[20:24], 2) // a text message

	shortHeader := good[:HeaderBytes-1]

	wrongMagic := append([]byte(nil), good...)
	copy(wrongMagic, "USRQ")

	oddAudio := append([]byte(nil), good[:HeaderBytes+7]...)

	for name, b := range map[string][]byte{
		"a message type this package does not implement": notVoice,
		"a truncated header":                             shortHeader,
		"the wrong magic":                                wrongMagic,
		"an audio length nothing produces":               oddAudio,
		"nothing at all":                                 nil,
	} {
		if _, ok := Decode(b); ok {
			t.Errorf("%s was decoded as a voice frame", name)
		}
	}

	// And the one that must work still does.
	if _, ok := Decode(good); !ok {
		t.Error("a well-formed voice frame was refused")
	}
}

// TestATransmissionIsAKeyupFramesAndARelease is the shape of the exchange.
//
// A release has to be sent: a far side that never sees PTT clear holds its
// channel open until something times out, which on a voice network is a
// channel nobody else can use. The routing core has an abandoned-transmission
// sweep for exactly that failure one boundary in.
func TestATransmissionIsAKeyupFramesAndARelease(t *testing.T) {
	const talkgroup = 2
	var wire [][]byte

	up, err := Keyup(0, talkgroup)
	if err != nil {
		t.Fatalf("keyup: %v", err)
	}
	wire = append(wire, up)
	for i := 1; i <= 3; i++ {
		f, err := Encode(Frame{
			Sequence: uint32(i), PTT: true, Talkgroup: talkgroup,
			Samples: make([]int16, SamplesPerFrame),
		})
		if err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		wire = append(wire, f)
	}
	down, err := Release(4, talkgroup)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	wire = append(wire, down)

	var audioFrames, keyups, releases int
	for i, b := range wire {
		f, ok := Decode(b)
		if !ok {
			t.Fatalf("datagram %d did not decode", i)
		}
		if f.Sequence != uint32(i) {
			t.Errorf("datagram %d carries sequence %d", i, f.Sequence)
		}
		if f.Talkgroup != talkgroup {
			t.Errorf("datagram %d carries talkgroup %d", i, f.Talkgroup)
		}
		switch {
		case len(f.Samples) > 0:
			audioFrames++
			if !f.PTT {
				t.Errorf("audio frame %d has PTT clear", i)
			}
		case f.PTT:
			keyups++
		default:
			releases++
		}
	}
	if keyups != 1 || releases != 1 || audioFrames != 3 {
		t.Errorf("the transmission read as %d keyup(s), %d audio frame(s) and "+
			"%d release(s), want 1, 3 and 1", keyups, audioFrames, releases)
	}
}

// TestTwentyMillisecondsIsTwentyMilliseconds on both sides of the boundary.
//
// A DMR voice frame and a USRP audio frame are each 20 ms at 8 kHz, so nothing
// has to buffer to convert between them — one frame in, one frame out. A
// mismatch here would mean every transmission needed a jitter buffer, and a
// jitter buffer is latency an operator hears.
func TestTwentyMillisecondsIsTwentyMilliseconds(t *testing.T) {
	if SamplesPerFrame != 160 {
		t.Errorf("a frame is %d samples; 20 ms at 8 kHz is 160", SamplesPerFrame)
	}
	const rate = 8000
	if ms := SamplesPerFrame * 1000 / rate; ms != 20 {
		t.Errorf("a frame is %d ms at %d Hz, want 20", ms, rate)
	}
}
