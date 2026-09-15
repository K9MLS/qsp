// Package audio carries uncompressed audio between QSP and the programs that
// encode it for a network QSP does not speak.
//
// # What it is for
//
// [ADR-0062] builds as much as possible into QSP, and names the two things
// that stay outside: the vocoder, because AMBEserver owns the serial port and
// that keeps the operator's hardware available to the operator ([ADR-0061]),
// and Opus, because every Go binding for it is cgo — which costs
// `CGO_ENABLED=0`, the ARM cross-build and a dependency-free install.
//
// So there is a boundary, and something has to cross it: 8 kHz PCM, in both
// directions, with a press and release of a button around it.
//
// # Why USRP rather than an interface of QSP's own
//
// USRP is AllStarLink's `chan_usrp` protocol, and it is what the PCM side of
// DVSwitch's Analog_Bridge speaks. It carries uncompressed 8 kHz 16-bit audio
// and push-to-talk signalling over UDP, and it is how Analog_Bridge reaches
// AllStarLink, EchoLink, or a second Analog_Bridge.
//
// **The operator already runs Zello on analog sites.** Whatever carries that
// sits on the far side of plain PCM, so an interface something already speaks
// may mean no new program has to be written at all. Declining to invent a
// private protocol where a well-understood one exists is the same instinct
// that made ADR-0061 choose a socket over a serial device.
//
// # The constraint this was once wrong under, and why it is not now
//
// PROJECT_MEMORY §8 records a recommendation to speak USRP being **rejected**
// on 2026-09-09, filed under "a recommendation that requires what it just
// ruled out is wrong". At that time QSP was to contain no vocoder and touch no
// audio: it copied AMBE payloads and never inspected them, so PCM could not
// exist anywhere in it, and a connector carrying PCM would have had nothing to
// put in it.
//
// **The lesson stands and the conclusion is superseded, because the constraint
// moved.** [ADR-0034] forbids QSP *containing* a vocoder, and it still does not
// contain one: an AMBE-3000F on hardware QSP does not own does the decoding,
// reached over a socket ([ADR-0061]). PCM arrives from a chip. §8r records real
// audio decoded through that path and written to a file, which is PCM in QSP's
// hands already.
//
// The mechanical check the record prescribes — read the recommendation against
// the constraint it was written under — is what says so. The constraint is "QSP
// contains no vocoder", not "no PCM exists", and USRP requires only the second.
//
// # What is built here
//
// The voice path: a frame of PCM with its sequence number and PTT state, in
// both directions. **Nothing else**, and the omissions are deliberate rather
// than pending. USRP carries other message types — metadata, text, DTMF — and
// none of them is implemented, because the only citation this package has for
// the wire format is the header layout and the voice type. A type number
// guessed from a name would be a packet somebody's software silently discards.
package audio

import (
	"encoding/binary"
	"fmt"
)

// The wire shape, from AllStarLink's chan_usrp and DVSwitch's USRP_Audio
// reference implementation: the four bytes "USRP", then seven big-endian
// 32-bit fields, then the audio.
const (
	// HeaderBytes is the fixed header size.
	HeaderBytes = 32
	// SamplesPerFrame is 20 ms at 8 kHz, which is what both sides of this
	// boundary work in — a DMR voice frame and a USRP audio frame are the
	// same 20 ms, so nothing has to buffer to convert between them.
	SamplesPerFrame = 160
	// FrameBytes is a voice frame on the wire.
	FrameBytes = HeaderBytes + SamplesPerFrame*2
)

// magic is the four bytes every USRP datagram starts with.
var magic = [4]byte{'U', 'S', 'R', 'P'}

// TypeVoice is the USRP message type carrying PCM.
//
// **The only type this package sends or accepts.** The others exist and are
// not implemented: see the package comment.
const TypeVoice uint32 = 0

// Frame is one USRP datagram.
type Frame struct {
	// Sequence counts datagrams, so a receiver can see a gap.
	Sequence uint32
	// PTT is true while a transmission is in progress.
	//
	// **A keyup and a release are datagrams in their own right**, carrying no
	// audio: the reference implementation sends a header with PTT set before
	// the first frame and one with it clear after the last. So a receiver
	// learns a transmission has started before any audio arrives, which is
	// what lets the far side open a channel in time.
	PTT bool
	// Talkgroup is the talkgroup the audio belongs to, or zero.
	//
	// It is carried so the far side can tell one net from another without a
	// second channel of signalling. QSP always knows it, because a frame
	// reaches a transcoder by being bridged to one (ADR-0063).
	Talkgroup uint32
	// Samples is the audio, empty for a keyup or release.
	Samples []int16
}

// Encode renders a frame for the wire.
//
// A frame with samples must have exactly SamplesPerFrame of them: the far side
// reads a fixed-size payload, and a short frame is a frame it interprets as
// something else rather than rejecting.
func Encode(f Frame) ([]byte, error) {
	if len(f.Samples) != 0 && len(f.Samples) != SamplesPerFrame {
		return nil, fmt.Errorf(
			"audio: a USRP voice frame carries %d samples, got %d; a short frame "+
				"is read as a different message rather than refused",
			SamplesPerFrame, len(f.Samples))
	}

	out := make([]byte, HeaderBytes, HeaderBytes+len(f.Samples)*2)
	copy(out, magic[:])
	binary.BigEndian.PutUint32(out[4:], f.Sequence)
	// Field 2 is "memory", unused by every implementation this package has
	// seen, and zero.
	binary.BigEndian.PutUint32(out[12:], ptt(f.PTT))
	binary.BigEndian.PutUint32(out[16:], f.Talkgroup)
	binary.BigEndian.PutUint32(out[20:], TypeVoice)
	// Fields 6 and 7 are "mpxid" and a reserved word, both zero.

	for _, s := range f.Samples {
		out = binary.LittleEndian.AppendUint16(out, uint16(s))
	}
	return out, nil
}

// ptt renders the keyup field.
func ptt(on bool) uint32 {
	if on {
		return 1
	}
	return 0
}

// Decode reads a frame from the wire, and reports whether it was one.
//
// **A datagram that is not USRP voice is refused rather than interpreted.**
// The other message types are real and unimplemented, and reading a metadata
// packet's fields as audio would put whatever it contained into somebody's
// receiver.
func Decode(b []byte) (Frame, bool) {
	if len(b) < HeaderBytes {
		return Frame{}, false
	}
	if [4]byte(b[0:4]) != magic {
		return Frame{}, false
	}
	if binary.BigEndian.Uint32(b[20:24]) != TypeVoice {
		return Frame{}, false
	}

	f := Frame{
		Sequence:  binary.BigEndian.Uint32(b[4:8]),
		PTT:       binary.BigEndian.Uint32(b[12:16]) != 0,
		Talkgroup: binary.BigEndian.Uint32(b[16:20]),
	}

	audio := b[HeaderBytes:]
	switch len(audio) {
	case 0:
		// A keyup or a release.
		return f, true
	case SamplesPerFrame * 2:
		f.Samples = make([]int16, SamplesPerFrame)
		for i := range f.Samples {
			f.Samples[i] = int16(binary.LittleEndian.Uint16(audio[i*2:]))
		}
		return f, true
	}
	// Any other length is a message this package does not read.
	return Frame{}, false
}

// Keyup is the datagram that opens a transmission.
func Keyup(sequence, talkgroup uint32) ([]byte, error) {
	return Encode(Frame{Sequence: sequence, PTT: true, Talkgroup: talkgroup})
}

// Release is the datagram that closes one.
//
// **It has to be sent.** A far side that never sees PTT clear holds its channel
// open until something times out, which on a voice network is a channel nobody
// else can use — the same failure the routing core's abandoned-transmission
// sweep exists for, one boundary out.
func Release(sequence, talkgroup uint32) ([]byte, error) {
	return Encode(Frame{Sequence: sequence, PTT: false, Talkgroup: talkgroup})
}
