package ipsc

import "encoding/binary"

// KindVoice is a voice frame, sent by a repeater to its master while a radio is
// transmitting. Every sixty milliseconds, for the length of the transmission.
//
// It was captured when a Motorola XPR8300 registered with cmd/ipsc-probe and
// K9MLS keyed a radio through it: four transmissions, sixty-six frames, in
// testdata/ipsc/ipsc-probe-voice.pcap.
const KindVoice Kind = 0x80

// KindVoicePrivate is a voice frame of a private call — one radio to one radio
// rather than to a talkgroup.
//
// **It is KindVoice with a radio ID where the talkgroup goes.** Nothing else
// about the frame changes: testdata/ipsc/ipsc-private-voice.pcap holds two
// private calls in opposite directions with group calls either side, and
// diffing a private header against a group header from the same repeater
// fourteen seconds apart, the envelope differs at the leading byte, the call
// counter, the three destination bytes, the stream ID, and six bytes that vary
// frame to frame within one transmission anyway. The same diff on a second
// repeater model differs at exactly the same offsets.
//
// The call type is encoded twice and the two agree. Byte 38 of a header or
// terminator is the DMR Full Link Control opcode: 0x00 Grp_V_Ch_Usr against
// 0x03 UU_V_Ch_Usr, matching the leading byte on all 32 header and terminator
// frames in that capture.
//
// **QSP refused this byte from the day the listener was written until
// 2026-09-05**, so no private call from a Motorola repeater ever crossed the
// bridge. It hid the same way the timeslot did: this network's traffic is group
// calls on TG 2.
const KindVoicePrivate Kind = 0x81

// IsVoice reports whether a kind carries voice, of either call type.
//
// **Compare with this rather than with KindVoice.** Every reader of a voice
// frame — the header, the payload, the colour code, the slot bit — has to
// accept both, and a comparison against the group constant alone is how one
// call type gets silently refused while the other works.
func (k Kind) IsVoice() bool { return k == KindVoice || k == KindVoicePrivate }

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
// **Destination was the exception and is no longer.** It was a reading rather
// than an observation for as long as every captured transmission went to the
// same place. ipsc-private-voice.pcap moved it three ways in two minutes — TG 2,
// TG 101, and two different radio IDs — and the Link Control in the same frames
// agrees with it every time.
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

	// Destination is bytes 9 to 11, read as a 24-bit ID. A talkgroup when
	// Private is false and a radio when it is true.
	Destination uint32

	// Private reports whether this is a call to one radio rather than to a
	// talkgroup, from the leading byte.
	//
	// **It has to travel with the destination.** A private destination
	// delivered as a group call puts one member's conversation on a talkgroup
	// for everybody, which is the same mistake the text path is written to
	// avoid.
	Private bool

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
	if !m.Kind.IsVoice() || len(m.Body) < VoiceHeaderLen-HeaderLen {
		return Voice{}, false
	}
	b := m.Body
	return Voice{
		CallCounter: b[0],
		SourceID:    uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]),
		Destination: uint32(b[4])<<16 | uint32(b[5])<<8 | uint32(b[6]),
		Private:     m.Kind == KindVoicePrivate,
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
	//
	// **The high bit is the timeslot, not part of the marker.** A voice frame
	// on the slot whose bit is set reads 0x8a and one on the other slot reads
	// 0x0a; use FrameKindOf rather than comparing the byte.
	FrameVoice byte = 0x0a
	// FrameSlotBit is the bit of the frame marker that carries the timeslot.
	//
	// # How this was found
	//
	// The marker was recorded as 0x8a, from captures that were all on one
	// timeslot, and every voice frame on the other slot was refused: 102 of
	// them in ipsc-slot-tg.pcap alone. **A whole timeslot of audio never
	// crossed the bridge**, from the day the listener was written until
	// 2026-09-04, and it went unnoticed because this network carries its
	// traffic on TG 2 timeslot 2.
	//
	// It was found by an operator keying up on talkgroup 11, timeslot 1, and
	// reading the journal: the IPSC listener logged "call started" and the DMR
	// side logged nothing, because AsVoice reads the flags and Payload reads
	// the marker.
	//
	// **The bit agrees with the slot bit in byte 17 on all 528 captured voice
	// frames**, across four captures and two repeater models, and is never set
	// on a header or a terminator — which is why those two were unaffected and
	// the fault looked like a talkgroup problem.
	FrameSlotBit byte = 0x80
)

// FrameKindOf returns the frame marker with the timeslot bit removed.
//
// Compare against FrameHeader, FrameVoice or FrameTerminator; comparing the raw
// byte works on one timeslot and silently fails on the other.
func FrameKindOf(marker byte) byte { return marker &^ FrameSlotBit }

const (
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
	if !m.Kind.IsVoice() || len(m.Body) < 21+2+VocoderLen {
		return 0, nil, nil, false
	}
	b := m.Body
	if FrameKindOf(b[25]) != FrameVoice {
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

// The 54-byte voice header and terminator, byte by byte.
//
// Every offset below was measured across 93 header and terminator frames in
// ipsc-probe-voice.pcap, ipsc-slot-tg.pcap and ipsc-two-peers.pcap, from two
// repeater models. Frames of both kinds are 54 bytes without exception.
//
//	[30]     0x01 for a header, 0x02 for a terminator
//	[31]     bit 0x80 is the timeslot; see HeaderSlotBit
//	[32:38]  00 0a 80 0a 00 60 in every frame captured, meaning unknown
//	[38:50]  the twelve-octet Link Control block, masked for the data type
//	[50]     zero in every frame captured
//	[51]     the DMR Slot Type: colour code in the high nibble, data type low
//	[52:54]  **unresolved.** See HeaderTailLen.
//
// Unlike a voice frame, a header does not carry a length at byte 31: the
// length is fixed and the byte is used for something else.
const (
	// HeaderLenTotal is the length of a whole header or terminator datagram.
	HeaderLenTotal = 54

	// HeaderSlotBit is the bit of byte 31 that carries the timeslot.
	//
	// **It agrees with the slot bit in byte 17 in all 93 captured frames**,
	// which is what makes it a reading rather than a guess: the two fields
	// are independent encodings of one fact and they never disagree.
	//
	// Bit 0x40 of the same byte is set in 87 of the 93 and is not understood.
	// The six exceptions are the second and third repeat headers of an
	// SLR5700; an XPR8300 sets it on all three, and every captured
	// terminator from either model sets it. QSP sets it always, which
	// reproduces both models' first header and every terminator.
	HeaderSlotBit byte = 0x80

	// HeaderConstantBit is bit 0x40 of byte 31, set unconditionally.
	HeaderConstantBit byte = 0x40

	// HeaderTailLen is the length of the tail at bytes 52 and 53, which this
	// package does not understand.
	//
	// **They are not derivable from any capture in the repository.** Across
	// 93 frames they take 87 distinct values. Seven CRC-16 constructions over
	// eight byte ranges in both byte orders match none of them; sum-8, XOR-8
	// and sum-16 over five ranges match at most two. Byte 52 drifts slowly
	// within a transmission and holds constant for one remote repeater, which
	// reads like a signal or timing measurement taken at the sending
	// repeater rather than anything computed from the frame.
	//
	// A master relaying somebody else's audio has no such measurement to
	// report, so QSP writes zero and says so rather than inventing a value.
	HeaderTailLen = 2
)

// HeaderConstants are bytes 32 to 37 of a header or terminator, which read the
// same in every frame captured from either model and are copied rather than
// reasoned about.
var HeaderConstants = [6]byte{0x00, 0x0a, 0x80, 0x0a, 0x00, 0x60}

// ColourCode returns the DMR colour code a voice message carries.
//
// # Two encodings, both measured
//
// A header or terminator carries it in the high nibble of byte 51, alongside
// the data type, which is the DMR Slot Type field. A voice frame carries it in
// the high nibble of the last byte of its trailer, alongside the LCSS, which is
// the EMB field. The two agree for every transmission in ipsc-two-peers.pcap.
//
// A synchronisation frame has no trailer and so carries no colour code, which
// is why ok exists.
//
// # Why a master needs it
//
// It is per repeater, not per network: the two peers transmitting simultaneously
// in ipsc-two-peers.pcap use colour codes 1 and 4. A master relaying audio to a
// repeater signs the frame with a colour code, and the only value that can be
// right for every repeater is the one that repeater itself uses.
func (m Message) ColourCode() (uint8, bool) {
	if !m.Kind.IsVoice() {
		return 0, false
	}
	b := m.Body
	switch {
	case len(b) >= 47 && (FrameKindOf(b[25]) == FrameHeader || FrameKindOf(b[25]) == FrameTerminator):
		return b[46] >> 4, true
	default:
		_, _, trailer, ok := m.Payload()
		if !ok || len(trailer) == 0 {
			return 0, false
		}
		return trailer[len(trailer)-1] >> 4, true
	}
}

// Payload classes, byte 32 of a voice frame.
//
// Each marks a position in the DMR superframe, which is why there are three of
// them in a 1:4:1 ratio across six frames.
const (
	// PayloadSync is the first burst of a superframe. Its trailer is empty
	// because the burst carries a synchronisation pattern rather than
	// signalling, and the pattern is a constant both ends already know.
	PayloadSync byte = 0x40
	// PayloadFragment carries a 32-bit embedded Link Control fragment in a
	// five-byte trailer.
	PayloadFragment byte = 0x06
	// PayloadFragmentWithLC carries a fragment and, after it, the assembled
	// Link Control: destination and source, decoded. Motorola sends the whole
	// thing once per superframe rather than making the far end reassemble the
	// four fragments.
	PayloadFragmentWithLC byte = 0x16
)

// EmbeddedFragment returns the 32-bit Link Control fragment a voice frame
// carries, and whether it has one.
//
// # Why this matters for a bridge
//
// A DMR burst puts 48 bits between its two payload halves: an 8-bit EMB, a
// 32-bit fragment, another 8-bit EMB. The Homebrew captures show exactly that
// shape, and the IPSC trailers carry the same 32-bit fragments — the last burst
// of a superframe has an all-zero fragment in *both* protocols, which is what
// confirms the two are describing the same thing.
//
// Motorola omits the EMB because it knows its own colour code and regenerates
// it. A bridge toward Homebrew has to supply one.
func (m Message) EmbeddedFragment() (uint32, bool) {
	_, _, trailer, ok := m.Payload()
	if !ok || len(trailer) < 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(trailer[:4]), true
}

// SuperframePosition reports where a voice frame sits, as far as the captures
// establish it: the sync burst, a burst carrying a fragment, or neither.
//
// It deliberately does not return a letter A to F. The captures show a 1:4:1
// ratio and the order sync, fragment, fragment, fragment, fragment-with-LC,
// fragment — which pins the first and the shape, but naming the middle four
// individually would be a claim the evidence does not support.
func (m Message) SuperframePosition() (class byte, ok bool) {
	c, _, _, valid := m.Payload()
	if !valid {
		return 0, false
	}
	switch c {
	case PayloadSync, PayloadFragment, PayloadFragmentWithLC:
		return c, true
	default:
		return c, false
	}
}

// Flags carried in byte 17 of a voice frame.
//
// Two independent bits share the byte, which is why an early note that it "is
// always 0x20" was half a finding.
const (
	// FlagSlot distinguishes the two DMR timeslots.
	//
	// **Which value means slot 1 is not recorded**, because nobody wrote it
	// down at the radio. Fifteen transmissions from two channels carrying the
	// same talkgroup split cleanly into two groups by this bit and nothing
	// else differed, so the bit is the slot beyond doubt; its polarity is one
	// sentence from an operator away.
	FlagSlot byte = 0x20
	// FlagTerminator marks the last frame of a transmission. It appears
	// alongside the 0x805e flags value in bytes 18 and 19.
	FlagTerminator byte = 0x40
)

// SlotBit reports the timeslot bit of a voice frame, and whether the message is
// one that carries it.
//
// It returns the raw bit rather than a slot number, deliberately: mapping it to
// "1" or "2" would be a claim, and this package does not make claims it cannot
// demonstrate.
func (m Message) SlotBit() (set bool, ok bool) {
	if !m.Kind.IsVoice() || len(m.Body) < 13 {
		return false, false
	}
	return m.Body[12]&FlagSlot != 0, true
}
