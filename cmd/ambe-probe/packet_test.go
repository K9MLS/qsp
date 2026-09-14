package main

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/k9mls/qsp/internal/ambe"
)

// TestTheFieldsTheProbeSendsAreTheOnesTheManualNames checks the identifiers
// this program uses against the table they came from.
//
// The identifiers are named locally so that a call site reads the way the
// manual does, but their lengths are not repeated here. Repeating a length
// beside a call site is how the eleven in the old comment came to sit next to
// the twelve in the manual, so the length lives in one place and this test
// confirms the identifier still means what the name says.
func TestTheFieldsTheProbeSendsAreTheOnesTheManualNames(t *testing.T) {
	control, ok := ambe.FieldsFor(ambe.TypeControl)
	if !ok {
		t.Fatal("there is no control field table")
	}
	byID := map[byte]ambe.Field{}
	for _, f := range control {
		byID[f.ID] = f
	}

	for _, tc := range []struct {
		id   byte
		name string
	}{
		{fieldReset, "PKT_RESET"},
		{fieldProdID, "PKT_PRODID"},
		{fieldVersion, "PKT_VERSTRING"},
		{fieldGetCfg, "PKT_GETCFG"},
		{fieldRateIndex, "PKT_RATET"},
		{fieldChannel0, "PKT_CHANNEL0"},
	} {
		f, ok := byID[tc.id]
		if !ok {
			t.Errorf("field %#02x is not in the control table; the probe sends it", tc.id)
			continue
		}
		if f.Name != tc.name {
			t.Errorf("field %#02x is %s in the table and %s here", tc.id, f.Name, tc.name)
		}
	}
}

// TestTheRateIsSetWithTheOneByteIndex is the defect that cost a dongle, from
// the caller's side.
//
// On 2026-09-14 this program sent `61 00 02 00 0a 21`: PKT_RATEP with one
// argument byte where the manual gives twelve. The chip waited for the rest,
// took it from the head of the next packet, and stayed mid-field until its
// power was physically removed — a soft reset could not clear it and neither
// could detaching the USB device in ESXi.
//
// **The gate this replaces was a search of main.go for a string.** It read the
// source for one particular spelling of the call and would have passed on any
// rewording of the same line, and failed on a harmless one. The real gate is
// now that internal/ambe refuses to build a field whose data length disagrees
// with the manual, so what is left to check here is the rate the probe asks
// for.
func TestTheRateIsSetWithTheOneByteIndex(t *testing.T) {
	if fieldRateIndex != 0x09 {
		t.Fatalf("the rate field is %#02x, want 0x09; 0x0a is the twelve-byte "+
			"rate word and sending one byte to it wedges the chip", fieldRateIndex)
	}

	got, err := ambe.Build(ambe.TypeControl, ambe.Val(fieldRateIndex, ambe.RateIndexDMR))
	if err != nil {
		t.Fatalf("building the rate packet: %v", err)
	}
	if want := "6100020009" + "21"; hex.EncodeToString(got) != want {
		t.Errorf("the rate packet is %s, want %s", hex.EncodeToString(got), want)
	}

	// Table 115, printed page 90, and the note beneath it: index 33 is
	// 3600/2450/1150, the rate interoperable with DMR and APCO P25 half rate.
	if ambe.RateIndexDMR != 33 {
		t.Errorf("the DMR rate index is %d, want 33", ambe.RateIndexDMR)
	}
}

// TestASpeechFrameIsTwentyMillisecondsAtEightKilohertz keeps the frame size
// honest.
//
// 160 samples is what the part takes for one compressed frame. A frame of any
// other length is a different question being asked, and the answer would not
// mean what it appears to.
//
// The resulting packet is 327 bytes, which is the size of the manufacturer's
// own Speech Packet Example 1 — a bare PKT_CHANNEL0 and a 160-sample SPEECHD
// field. That example is a fixture in testdata/ambe, so the arithmetic here
// and the arithmetic there have to agree.
func TestASpeechFrameIsTwentyMillisecondsAtEightKilohertz(t *testing.T) {
	s := sine(1000)
	if len(s) != samplesPerFrame {
		t.Fatalf("a frame is %d samples, want %d", len(s), samplesPerFrame)
	}

	speech, err := ambe.SpeechD(s)
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	pkt, err := ambe.Build(ambe.TypeSpeech, ambe.Val(fieldChannel0), speech)
	if err != nil {
		t.Fatalf("building the speech packet: %v", err)
	}
	// Four header bytes, a bare PKT_CHANNEL0, a SPEECHD identifier, a sample
	// count, and two bytes per sample.
	if got, want := len(pkt), 4+1+1+1+320; got != want {
		t.Errorf("a speech packet is %d bytes, want %d", got, want)
	}
	if got := int(pkt[1])<<8 | int(pkt[2]); got != len(pkt)-4 {
		t.Errorf("the speech packet declares %d field bytes and carries %d",
			got, len(pkt)-4)
	}
}

// TestTheToneStaysInsideTheSampleRange keeps the probe's own signal honest.
//
// A frame that clips is a different experiment: the chip would be encoding
// distortion, and a bad answer would look like a bad reading of the packet
// format rather than a bad input.
func TestTheToneStaysInsideTheSampleRange(t *testing.T) {
	for _, hz := range []float64{300, 1000, 3000} {
		for i, s := range sine(hz) {
			if s > 8000 || s < -8000 {
				t.Fatalf("sample %d of a %g Hz frame is %d, outside ±8000", i, hz, s)
			}
		}
	}
}

// TestAPortUnreachableIsNotSilence is the classification that cost two bench
// runs.
//
// On 2026-09-14 this program sent six packets — four queries, a rate set and a
// 327-byte audio frame — at a host with nothing listening on 2460, and
// reported every one as "the packet was refused or misread". No datagram had
// reached the chip; ECONNREFUSED on a connected UDP socket is an ICMP
// port-unreachable, and it says nothing about AMBE. **A status that does not
// name its subject sends the reader to the wrong layer**, which is what §8f
// records about a health report and what happened here twice in an hour.
//
// The socket behaviour was confirmed by running the program against a closed
// port. What is checked here is the classification, because that is the part
// that decides which sentence gets printed.
func TestAPortUnreachableIsNotSilence(t *testing.T) {
	refusedErr := &net.OpError{
		Op:  "read",
		Net: "udp",
		Err: os.NewSyscallError("read", syscall.ECONNREFUSED),
	}
	if !isUnreachable(refusedErr) {
		t.Error("a wrapped ECONNREFUSED was not recognised as a port " +
			"unreachable; the run would report a closed port as a refused packet")
	}

	for name, err := range map[string]error{
		"a timeout":     os.ErrDeadlineExceeded,
		"a bare string": errors.New("no reply"),
		"host down": &net.OpError{Op: "write", Net: "udp",
			Err: os.NewSyscallError("write", syscall.EHOSTUNREACH)},
	} {
		if isUnreachable(err) {
			t.Errorf("%s was recognised as a port unreachable", name)
		}
	}
}

// TestASweepIsNotASingleToneAndASineIs is the difference the bench needs.
//
// A reset-state encoder codes a steady 1 kHz sine as a tone descriptor, which
// is what fifty byte-identical frames on 2026-09-14 turned out to be. A sweep
// cannot be one descriptor, so it exercises the path a voice takes without
// changing a single setting on the chip — signal and configuration are two
// variables and this project changes one at a time.
func TestASweepIsNotASingleToneAndASineIs(t *testing.T) {
	const frames = 50

	// The sine holds its frequency, so consecutive frames are the same wave.
	a, b := frameOf("sine", 0, frames), frameOf("sine", 1, frames)
	if len(a) != samplesPerFrame || len(b) != samplesPerFrame {
		t.Fatalf("a frame is %d and %d samples, want %d", len(a), len(b), samplesPerFrame)
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("sample %d of consecutive 1 kHz frames differs (%d, %d)",
				i, a[i], b[i])
		}
	}

	// **Phase continuity has to be checked at a frequency that does not divide
	// the frame**, and 1 kHz does: a 20 ms frame at 8 kHz holds exactly twenty
	// cycles, so a generator that restarts its phase every frame produces the
	// identical bytes and the check above passes either way. Breaking the
	// phase deliberately proved that — the test noticed nothing.
	//
	// Every multiple of 50 Hz is a whole number of cycles in 20 ms, so this
	// uses 1025 Hz, which is twenty and a half. A continuous second frame
	// starts half a cycle out and cannot match the first; a restarted one
	// matches it exactly. Without this, a frame boundary is a click, and a
	// click is a transient the coder would spend bits on.
	c, d := sineFrom(1025, 0), sineFrom(1025, 1)
	same := true
	for i := range c {
		if c[i] != d[i] {
			same = false
			break
		}
	}
	if same {
		t.Error("consecutive 1025 Hz frames are identical, so the generator " +
			"restarts its phase every frame; at twenty and a half cycles a " +
			"continuous frame cannot repeat, and every boundary is a click")
	}

	// The sweep changes frequency across the run, so its first and last
	// frames cannot be the same wave.
	first, last := frameOf("sweep", 0, frames), frameOf("sweep", frames-1, frames)
	sweepSame := true
	for i := range first {
		if first[i] != last[i] {
			sweepSame = false
			break
		}
	}
	if sweepSame {
		t.Error("the first and last frames of a sweep are identical; a sweep " +
			"that does not sweep tests nothing the sine did not")
	}

	// And it stays inside the range, at both ends of the sweep.
	for _, index := range []int{0, frames / 2, frames - 1} {
		for i, v := range frameOf("sweep", index, frames) {
			if v > 8000 || v < -8000 {
				t.Fatalf("sample %d of sweep frame %d is %d, outside ±8000", i, index, v)
			}
		}
	}
}

// TestToneDetectionIsTheOnlyBitSetAtReset pins what the differential changes.
//
// The probe's -tone-detect=false sends an encoder control word with TD_ENABLE
// cleared and everything else as a reset leaves it. On this board that means
// the word goes from 0x1000 to 0x0000, because CFG0 0x05 and CFG1 0x00 leave
// every pin-derived bit clear — so exactly one bit moves, which is what makes
// the run a differential rather than two changes at once.
func TestToneDetectionIsTheOnlyBitSetAtReset(t *testing.T) {
	on := ambe.ECModeAtReset
	off := on &^ ambe.ECToneDetect

	if uint16(on) != 0x1000 {
		t.Errorf("the reset control word is %#04x, want 0x1000", uint16(on))
	}
	if uint16(off) != 0x0000 {
		t.Errorf("clearing tone detection gives %#04x, want 0x0000; anything "+
			"else means a second bit moved and the run is not a differential",
			uint16(off))
	}
	if bits := uint16(on) ^ uint16(off); bits != uint16(ambe.ECToneDetect) {
		t.Errorf("the differential moves %#04x, want only TD_ENABLE %#04x",
			bits, uint16(ambe.ECToneDetect))
	}
}

// TestTheCaptureYieldsRealVocoderFramesInBothForms is the extraction, checked
// without a dongle.
//
// **It reads the operator's own capture** —
// testdata/ipsc/ipsc-master-voice.pcap, a Motorola master sending real audio
// off an XPR8300 — because a synthetic signal cannot say how a speech model
// handles a voice, and no amount of reasoning replaces the frames.
//
// The two forms differ only in what occupies the 23 bits behind the 49
// parameter bits, and which one the chip wants is the open question. What this
// checks is that both are well formed, that they differ, and that the
// parameters survive the trip — so a bench run that fails is the chip's
// answer rather than a defect here.
func TestTheCaptureYieldsRealVocoderFramesInBothForms(t *testing.T) {
	const path = "../../testdata/ipsc/ipsc-master-voice.pcap"

	onair, err := vocoderFramesFromCapture(path, "onair")
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	params, err := vocoderFramesFromCapture(path, "params")
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	// The capture's companion document records 288 voice packets, and each
	// carries three vocoder frames.
	if len(onair) != len(params) {
		t.Fatalf("the two forms give %d and %d frames", len(onair), len(params))
	}
	if len(onair)%3 != 0 {
		t.Errorf("%d frames is not a whole number of bursts of three", len(onair))
	}
	// **An exact count, not a floor.** A lower bound passes with the IP
	// header read from the wrong offset: most payloads then fail to parse as
	// IPSC and are skipped, enough survive to clear a loose threshold, and
	// the run reaches the dongle with the wrong bytes. This capture is
	// Ethernet-framed and holds 276 voice frames of three vocoder frames
	// each, so the number is 828 and anything else is a defect in the
	// extraction rather than a difference of opinion.
	if got, want := len(onair), 828; got != want {
		t.Errorf("the capture yielded %d vocoder frames, want %d — %.2f seconds "+
			"of audio against %.2f", got, want,
			float64(got)*0.02, float64(want)*0.02)
	}

	for i, f := range onair {
		if len(f) != 9 {
			t.Fatalf("on-air frame %d is %d bytes, want 9 for 72 bits", i, len(f))
		}
	}

	// **The forms must differ, or one of them is not being built.** They
	// share their first 49 bits and differ in the 23 behind them, so a run
	// where every frame matched would mean the error correction was never
	// added.
	same := 0
	for i := range onair {
		if string(onair[i]) == string(params[i]) {
			same++
		}
	}
	if same == len(onair) {
		t.Error("every frame is identical in both forms, so the error " +
			"correction is not being added and the differential tests nothing")
	}

	// The parameters survive: the first 49 bits of the params form are the
	// parameters themselves, and a frame of all zeros would mean the
	// extraction silently produced nothing.
	nonZero := 0
	for _, f := range params {
		for _, b := range f[:7] {
			if b != 0 {
				nonZero++
				break
			}
		}
	}
	if nonZero*4 < len(params) {
		t.Errorf("only %d of %d frames carry any parameter bits; the extraction "+
			"is producing empty frames", nonZero, len(params))
	}

	// **The first frame is the silence frame this project already knew.** The
	// params form reads f801a99f8ce08..., whose top 49 bits are
	// 0x1F003533F19C1 — the value internal/dmrfec documents as silence,
	// established from a different capture on a different protocol. An
	// extraction landing on known ground is worth asserting: if the packing
	// ever slips, this is the value that moves.
	if got, want := fmt.Sprintf("%x", params[0][:7]), "f801a99f8ce080"; got != want {
		t.Errorf("the capture's first parameter frame is %s, want %s — the "+
			"silence frame 0x1F003533F19C1 that internal/dmrfec documents",
			got, want)
	}

	// Frames the dongle accepted on 2026-09-14, kept so that the frame-form
	// question stays settled by evidence rather than by memory. All 828 came
	// back with DATA_INVALID clear in the on-air form.
	for _, tc := range []struct {
		index int
		want  string
	}{
		{0, "b9e881526173002a6b"},
		{300, "e0f78315242ab44524"},
		{301, "f0d683177558a11507"},
		{400, "9da5e737173b8c9407"},
	} {
		if got := fmt.Sprintf("%x", onair[tc.index]); got != tc.want {
			t.Errorf("on-air frame %d is %s, want %s", tc.index, got, tc.want)
		}
	}

	// Every frame must be one the builder accepts, because a frame this tool
	// cannot send is a frame the bench run would die on rather than report.
	for i, f := range onair {
		if _, err := ambe.Chand(72, f); err != nil {
			t.Fatalf("on-air frame %d cannot be sent: %v", i, err)
		}
	}
}

// TestACaptureThisToolCannotReadIsRefused keeps a wrong offset from becoming
// plausible frames.
//
// A link type this tool does not know means the IP header is somewhere else,
// and an offset guessed wrong yields payloads that parse as something. That is
// worse than refusing, because the frames would reach the dongle and its
// answer would be about the wrong bytes.
func TestACaptureThisToolCannotReadIsRefused(t *testing.T) {
	dir := t.TempDir()

	for name, header := range map[string][]byte{
		"not a pcap":       {0xde, 0xad, 0xbe, 0xef},
		"unknown linktype": append(pcapHeader(999), 0),
	} {
		path := filepath.Join(dir, "x.pcap")
		if err := os.WriteFile(path, header, 0o644); err != nil {
			t.Fatalf("writing a fixture: %v", err)
		}
		if _, err := readPcapUDP(path); err == nil {
			t.Errorf("a file that is %s was read", name)
		}
	}

	// A well-formed header with no packets is not an error about the format,
	// but it is still nothing to decode.
	path := filepath.Join(dir, "empty.pcap")
	if err := os.WriteFile(path, pcapHeader(1), 0o644); err != nil {
		t.Fatalf("writing a fixture: %v", err)
	}
	if _, err := readPcapUDP(path); err == nil {
		t.Error("an empty capture was accepted; there is nothing in it to decode")
	}
}

// pcapHeader builds a little-endian pcap file header for a link type.
func pcapHeader(linkType uint32) []byte {
	h := make([]byte, 24)
	binary.LittleEndian.PutUint32(h[0:], 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], 65535)
	binary.LittleEndian.PutUint32(h[20:], linkType)
	return h
}

// TestTheWavIsAWavThatPlays checks the container rather than the audio.
//
// A file the operator cannot open answers nothing, and the point of writing
// one is that a person listens to it. Forty-four bytes of RIFF header with
// the wrong sample rate would play at the wrong speed and sound like a
// different defect.
func TestTheWavIsAWavThatPlays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")
	samples := sineFrom(1000, 0)
	if err := writeWAV(path, samples); err != nil {
		t.Fatalf("writing a wav: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}

	if got, want := len(raw), 44+len(samples)*2; got != want {
		t.Fatalf("the file is %d bytes, want %d", got, want)
	}
	if string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		t.Fatalf("the file does not begin RIFF/WAVE")
	}
	if got := binary.LittleEndian.Uint16(raw[22:24]); got != 1 {
		t.Errorf("the file claims %d channels, want 1", got)
	}
	if got := binary.LittleEndian.Uint32(raw[24:28]); got != 8000 {
		t.Errorf("the file claims %d Hz, want 8000; the wrong rate plays at the "+
			"wrong speed and sounds like a different defect", got)
	}
	if got := binary.LittleEndian.Uint16(raw[34:36]); got != 16 {
		t.Errorf("the file claims %d bits a sample, want 16", got)
	}
	if got := binary.LittleEndian.Uint32(raw[4:8]); got != uint32(36+len(samples)*2) {
		t.Errorf("the RIFF size is %d, want %d", got, 36+len(samples)*2)
	}
	if got := binary.LittleEndian.Uint32(raw[40:44]); got != uint32(len(samples)*2) {
		t.Errorf("the data size is %d, want %d", got, len(samples)*2)
	}
	// And the samples come back, little-endian as a WAV requires, which is the
	// other way round from every 16-bit value in an AMBE packet.
	for i, want := range samples {
		got := int16(binary.LittleEndian.Uint16(raw[44+i*2:]))
		if got != want {
			t.Fatalf("sample %d reads %d, want %d", i, got, want)
		}
	}
}
