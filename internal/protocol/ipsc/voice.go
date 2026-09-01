package ipsc

import "encoding/binary"

// KindVoice is a voice frame, sent by a repeater to its master while a radio is
// transmitting. Every sixty milliseconds, for the length of the transmission.
//
// It was captured when a Motorola XPR8300 registered with cmd/ipsc-probe and
// K9MLS keyed a radio through it: four transmissions, sixty-six frames, in
// testdata/ipsc/ipsc-probe-voice.pcap.
const KindVoice Kind = 0x80

// Voice is the header of a KindVoice message.
//
// # What is demonstrated and what is not
//
// Every field here changed in a way that was watched, across four transmissions
// from one repeater. Sequence and Timestamp advance by fixed amounts within a
// call, StreamID differs between calls and holds within one, and CallCounter
// counted 1, 2, 3, 4 across four key-ups — including across a restart of the
// probe, so the repeater is counting rather than the session.
//
// **Destination is the exception and is not demonstrated.** It read 455 in all
// four transmissions because all four went to the same place, so its position
// is a reading rather than an observation: nothing has moved it. It is exposed
// because a routing server cannot avoid needing it, and named honestly so that
// nobody mistakes it for a settled field. A capture of two different talkgroups
// settles it in two minutes.
type Voice struct {
	// CallCounter is byte 5: a per-transmission counter kept by the repeater.
	// It is not a timeslot, whatever its position suggests.
	CallCounter uint8

	// SourceID is the 24-bit radio ID of the transmitting radio, bytes 6 to 8.
	//
	// Note the width. The envelope's SenderID is 32 bits and this is 24, which
	// is the width DMR uses on the air. Two fields, two widths, and they held
	// the same number in these captures only because the radio keyed was the
	// repeater's own ID.
	SourceID uint32

	// Destination is bytes 9 to 11, read as a 24-bit ID. **Unverified.**
	Destination uint32

	// StreamID is bytes 15 and 16: constant for every frame of one
	// transmission and different for each, so it identifies a call.
	StreamID uint16

	// Sequence is bytes 20 and 21, advancing by one per frame.
	Sequence uint16

	// Timestamp is bytes 22 to 25, advancing by exactly 480 per frame. Sixty
	// milliseconds is one DMR voice frame and 480 samples at eight kilohertz
	// is sixty milliseconds, so this counts samples.
	Timestamp uint32

	// Flags is bytes 18 and 19, uninterpreted. It reads 0x80dd on the first
	// frame of a transmission, 0x805d on the rest, and 0x805e on the last —
	// so a call has a marked beginning and end, which is what a routing server
	// needs to know when to start and stop relaying.
	Flags uint16
}

// VoiceHeaderLen is the length of the header this package reads. Frames
// observed were 52 to 66 bytes; everything past this is burst payload.
const VoiceHeaderLen = 26

// IsFirstFrame reports whether this frame begins a transmission.
func (v Voice) IsFirstFrame() bool { return v.Flags == 0x80dd }

// IsLastFrame reports whether this frame ends one.
func (v Voice) IsLastFrame() bool { return v.Flags == 0x805e }

// AsVoice reads the voice header out of a message, if it is one.
//
// The remaining bytes are deliberately not interpreted. Whether the DMR burst
// crosses IPSC verbatim is the question that decides whether QSP can bridge
// Motorola without transcoding, and it is not answered by a capture of one
// repeater talking to a program that never replies to voice.
func (m Message) AsVoice() (Voice, bool) {
	if m.Kind != KindVoice || len(m.Body) < VoiceHeaderLen-HeaderLen {
		return Voice{}, false
	}
	b := m.Body
	return Voice{
		CallCounter: b[0],
		SourceID:    uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]),
		Destination: uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6]),
		StreamID:    binary.BigEndian.Uint16(b[10:12]),
		Flags:       binary.BigEndian.Uint16(b[13:15]),
		Sequence:    binary.BigEndian.Uint16(b[15:17]),
		Timestamp:   binary.BigEndian.Uint32(b[17:21]),
	}, true
}
