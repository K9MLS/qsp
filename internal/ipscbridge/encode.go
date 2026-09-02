package ipscbridge

import (
	"encoding/binary"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// # Sending voice to a Motorola repeater
//
// **This direction has never been captured, and every other line in this
// package was measured.** ADR-0029 forbids inventing IPSC behaviour, and this
// is a deliberate, recorded exception taken at the operator's direction: the
// alternative was leaving two repeaters unable to hear the network for want of
// a ten-minute capture that could not be scheduled. See ADR-0041.
//
// What follows is therefore split into what is known and what is assumed, so
// that when a capture does exist the assumptions can be checked one at a time
// rather than rediscovered.
//
// # What is known
//
// The envelope, from ipsc-phase2-registration.pcap: byte 0 is the type and
// bytes 1 to 4 are **the sender's own radio ID**. That capture had two Motorola
// repeaters talking, and the peer's messages carried its ID while the master's
// carried the master's. So a master relaying voice puts its own ID there, not
// the originating repeater's — the thing this file was most likely to get
// wrong is the thing a capture already settled.
//
// The body layout, from ipsc-probe-voice.pcap and ipsc-two-peers.pcap: a call
// counter, a 24-bit source, a 24-bit destination, a stream ID, the timeslot
// bit, the frame flags, a sequence and a sample timestamp advancing by 480.
//
// The transmission shape, from ipsc-two-peers.pcap: three header frames, a
// six-frame superframe cycle, one terminator.
//
// The audio, proved lossless in both directions: dmrfec.IPSCFromBurst recovers
// the 19-byte vocoder core from a Homebrew burst, and 884 real bursts have been
// round-tripped bit-exact.
//
// # What is assumed, and how each fails
//
//   - **That a repeater accepts what a repeater sends.** Every captured frame
//     travels peer to master. If the master's frames differ in some field, a
//     receiving repeater ignores them and the symptom is silence.
//   - **That the constant bytes are constant.** Bytes 12 to 14 read 0x02 0x00
//     0x00 in every captured frame and their meaning is unknown. They are
//     copied.
//   - **That three headers matter.** Motorola sends three; this sends three,
//     because matching observed behaviour is cheaper than discovering it does
//     not matter.
//   - **That the call counter may start at one.** It is per-transmission and
//     kept by the repeater; a master has no repeater to keep it.
//
// If this does not work on air, a capture of a real master is the answer and
// these four are the list to check.

// Encoder turns Homebrew bursts into IPSC voice frames for one repeater.
//
// It is not safe for concurrent use, and like Converter it keeps state per
// timeslot because a repeater carries two transmissions at once.
type Encoder struct {
	masterID uint32
	slots    [2]encodeState
}

type encodeState struct {
	stream    hbp.StreamID
	seen      bool
	sequence  uint16
	timestamp uint32
	counter   uint8
	opened    bool
}

// NewEncoder returns an encoder that sends as the given master radio ID.
func NewEncoder(masterID uint32) *Encoder {
	return &Encoder{masterID: masterID}
}

// Encode turns one Homebrew frame into the IPSC messages a repeater should
// receive: the three headers that open a transmission, the voice frame itself,
// or the terminator that closes it.
//
// It returns nothing for a frame whose audio cannot be recovered, because a
// frame with no vocoder payload is not something to send onward.
func (e *Encoder) Encode(frame hbp.Data) []ipsc.Message {
	slot := 0
	if frame.Timeslot == hbp.Timeslot2 {
		slot = 1
	}
	st := &e.slots[slot]

	if frame.StreamID != st.stream || !st.seen {
		st.stream = frame.StreamID
		st.seen = true
		st.sequence = 0
		st.timestamp = 0
		st.opened = false
		st.counter++
		if st.counter == 0 {
			st.counter = 1
		}
	}

	// A terminator closes the transmission and carries no audio of its own.
	if frame.IsTerminator() {
		if !st.opened {
			return nil
		}
		st.opened = false
		st.seen = false
		return []ipsc.Message{e.frame(st, frame, slot, flagsLast, nil, 0)}
	}

	core, _, ok := dmrfec.IPSCFromBurst(frame.Payload[:])
	if !ok {
		return nil
	}

	var out []ipsc.Message
	if !st.opened {
		// **Three headers, because Motorola sends three.** A receiver joining
		// late catches one, and matching observed behaviour costs nothing
		// beyond three datagrams at the start of a transmission.
		st.opened = true
		for i := 0; i < 3; i++ {
			f := flagsMiddle
			if i == 0 {
				f = flagsFirst
			}
			out = append(out, e.frame(st, frame, slot, f, nil, 0))
		}
	}
	// The payload class marks the position in the superframe, and the
	// converter already counts it into DataType for voice frames.
	class := ipsc.PayloadFragment
	if frame.FrameType == hbp.FrameTypeVoiceSync {
		class = ipsc.PayloadSync
	}
	out = append(out, e.frame(st, frame, slot, flagsMiddle, core, class))
	return out
}

// Frame flag values, observed on every captured transmission.
const (
	flagsFirst  uint16 = 0x80dd
	flagsMiddle uint16 = 0x805d
	flagsLast   uint16 = 0x805e
)

// frame builds one IPSC voice message.
func (e *Encoder) frame(st *encodeState, src hbp.Data, slot int, flags uint16, core []byte, class byte) ipsc.Message {
	// The body is the header fields, then a frame marker, a length, a payload
	// class, the vocoder core, and a trailer. The offsets are those
	// ipsc.Message.Payload reads, so an encoded frame parses back.
	const (
		markerAt = 25
		lengthAt = 26
		classAt  = 27
		coreAt   = 28
	)
	trailer := 0
	if len(core) > 0 {
		// Frames carrying a Link Control fragment have a 14-byte trailer; the
		// captures show it holding the same destination and source the header
		// does, which is how a radio joining mid-transmission learns them.
		trailer = 14
	}
	size := coreAt + len(core) + trailer
	if len(core) == 0 {
		size = coreAt
	}
	body := make([]byte, size)

	body[0] = st.counter
	body[1] = byte(src.SourceID >> 16)
	body[2] = byte(src.SourceID >> 8)
	body[3] = byte(src.SourceID)
	body[4] = byte(src.TargetID >> 16)
	body[5] = byte(src.TargetID >> 8)
	body[6] = byte(src.TargetID)

	// Bytes 12 to 14 of the frame read 0x02 0x00 0x00 in every captured voice
	// frame from either repeater model. Their meaning is unknown and they are
	// copied rather than reasoned about.
	body[7] = 0x02

	binary.BigEndian.PutUint16(body[10:12], uint16(src.StreamID))

	// The timeslot bit and the last-frame bit share byte 17 of the frame.
	var b17 byte
	if slot == 1 {
		b17 |= ipsc.FlagSlot
	}
	if flags == flagsLast {
		b17 |= ipsc.FlagTerminator
	}
	body[12] = b17

	binary.BigEndian.PutUint16(body[13:15], flags)
	binary.BigEndian.PutUint16(body[15:17], st.sequence)
	binary.BigEndian.PutUint32(body[17:21], st.timestamp)

	if len(core) > 0 {
		body[markerAt] = ipsc.FrameVoice
		body[lengthAt] = byte(len(body) - 27)
		body[classAt] = class
		copy(body[coreAt:], core)
		// The trailer repeats the destination and source. Two independent
		// encodings of the same fact is what the captures show, and a radio
		// joining mid-transmission reads this one.
		tr := body[coreAt+len(core):]
		tr[7] = byte(src.TargetID >> 16)
		tr[8] = byte(src.TargetID >> 8)
		tr[9] = byte(src.TargetID)
		tr[10] = byte(src.SourceID >> 16)
		tr[11] = byte(src.SourceID >> 8)
		tr[12] = byte(src.SourceID)
	}

	st.sequence++
	// Sixty milliseconds is one DMR voice frame, and 480 samples at eight
	// kilohertz is sixty milliseconds. The captures advance by exactly 480.
	st.timestamp += 480

	return ipsc.Message{
		Kind: ipsc.KindVoice,
		// **The master's own ID, not the originating repeater's.** Bytes 1 to 4
		// are the sender's own radio ID: in ipsc-phase2-registration.pcap the
		// peer's messages carry the peer's and the master's carry the
		// master's.
		SenderID: e.masterID,
		Body:     body,
	}
}
