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

// Frame classes, byte 30 of a KindVoice message.
//
// The name of each describes where it sat in a transmission, which is all the
// captures show.
const (
	// FrameHeader opens a transmission. Three were sent before any audio.
	FrameHeader byte = 0x01
	// FrameVoice carries vocoder data.
	FrameVoice byte = 0x8a
	// FrameTerminator closes a transmission.
	FrameTerminator byte = 0x02
)

// VocoderLen is the length of the vocoder payload in every voice frame: 19
// bytes, in every one of the fifty-four captured.
//
// **A DMR burst is 33 bytes and this is 19, so IPSC does not carry the burst.**
// 19 bytes is 152 bits, and three AMBE+2 frames at 49 bits each is 147 — so
// this is very likely the vocoder parameters without the FEC and sync that DMR
// wraps them in. That last step is inference from arithmetic and is marked as
// such; what is demonstrated is the length, that it varies frame to frame, and
// that it is nothing like 33.
const VocoderLen = 19

// Payload splits the part of a voice frame after the header.
//
// Layout, from the fifty-four captured frames:
//
//	[30]     frame class: header, voice or terminator
//	[31]     length of everything from byte 32 onward
//	[32]     payload class: 0x40, 0x06 or 0x16 on voice frames
//	[33:52]  vocoder payload, always 19 bytes
//	[52:]    trailer of 0, 5 or 14 bytes
//
// The three trailer lengths cycle with the DMR superframe. The 14-byte one
// carries Link Control; see LinkControl.
func (m Message) Payload() (payloadClass byte, vocoder, trailer []byte, ok bool) {
	if m.Kind != KindVoice || len(m.Body) < 21+2+VocoderLen {
		return 0, nil, nil, false
	}
	b := m.Body
	if b[25] != FrameVoice {
		return 0, nil, nil, false
	}
	length := int(b[26])
	if length != len(b)-27 {
		return 0, nil, nil, false
	}
	return b[27], b[28 : 28+VocoderLen], b[28+VocoderLen:], true
}

// LinkControl is the destination and source a voice frame carries in its
// trailer, independently of the header.
//
// DMR sends Link Control inside the voice superframe so that a radio joining
// mid-transmission learns who is talking to whom, and IPSC carries it through:
// the 14-byte trailer holds the same 24-bit destination and source that bytes 9
// to 11 and 6 to 8 of the header hold.
//
// **Two independent encodings agreeing in one packet is the strongest evidence
// available for the Destination field short of moving it.** They are not
// independent observations of the protocol — one repeater built both — but a
// field that appears twice in two different layouts is a field, and a
// coincidence of position twice over is unlikely.
func (m Message) LinkControl() (destination, source uint32, ok bool) {
	_, _, trailer, valid := m.Payload()
	if !valid || len(trailer) < 14 {
		return 0, 0, false
	}
	d := uint32(trailer[7])<<16 | uint32(trailer[8])<<8 | uint32(trailer[9])
	s := uint32(trailer[10])<<16 | uint32(trailer[11])<<8 | uint32(trailer[12])
	return d, s, true
}
