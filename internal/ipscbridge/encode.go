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
	masterID   uint32
	colourCode uint8
	cfg        Config
	slots      [2]encodeState
}

type encodeState struct {
	stream    hbp.StreamID
	seen      bool
	sequence  uint16
	timestamp uint32
	counter   uint8
	opened    bool
}

// NewEncoder returns an encoder that sends as the given master radio ID, using
// cfg for the colour code and the slot bit's polarity.
//
// **The polarity comes from the same Config the Converter reads**, and that is
// the point. Until this was wired the encoder hardcoded one polarity while the
// converter took the operator's, so an instance configured the other way
// received audio on one timeslot and sent it back out marked as the other. Both
// halves were individually correct.
func NewEncoder(masterID uint32, cfg Config) *Encoder {
	return &Encoder{masterID: masterID, colourCode: cfg.ColourCode, cfg: cfg}
}

// SetColourCode sets the colour code this encoder signs frames with.
//
// # Why this is per repeater and not per network
//
// A colour code is the DMR air interface's co-channel discriminator: a radio
// programmed for one ignores bursts carrying another. It belongs to the RF side
// and QSP neither filters on it nor cares what it is — but QSP does have to
// *write* one into every frame it builds, because the Slot Type of a header and
// the EMB of a voice burst both carry it, and there is no value meaning "none".
//
// One colour code for the whole network is therefore the one choice that cannot
// be right: ipsc-two-peers.pcap has two repeaters transmitting at the same
// moment on colour codes 1 and 4. Signing both with either number is wrong for
// one of them. Mirroring back the colour code a repeater itself uses is right
// for every repeater at once, which is what "all colour codes, all devices"
// requires.
//
// The listener learns each peer's from the frames that peer sends and calls
// this before every encode. A peer that has never transmitted has none to
// mirror, and keeps the configured default.
func (e *Encoder) SetColourCode(cc uint8) {
	if cc <= 15 {
		e.colourCode = cc
	}
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
		return []ipsc.Message{e.signalling(st, frame, slot, flagsLast,
			ipsc.FrameTerminator, dmrfec.DataTypeTerminatorWithLC)}
	}

	// A text message is DMR data, not audio, and needs re-wrapping rather than
	// rebuilding. It carries no vocoder core, so it has to be recognised
	// before the core is looked for. See ADR-0045.
	if frame.FrameType == hbp.FrameTypeSync {
		if m, ok := e.text(st, frame, slot); ok {
			return []ipsc.Message{m}
		}
		return nil
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
			out = append(out, e.signalling(st, frame, slot, f,
				ipsc.FrameHeader, dmrfec.DataTypeVoiceLCHeader))
		}
	}
	out = append(out, e.voice(st, frame, slot, core))
	return out
}

// Frame flag values, observed on every captured transmission.
const (
	flagsFirst  uint16 = 0x80dd
	flagsMiddle uint16 = 0x805d
	flagsLast   uint16 = 0x805e
)

// Body offsets, in Message.Body coordinates. A body begins at byte 5 of the
// datagram, after the type byte and the 32-bit sender ID, so a body offset is
// five less than the datagram offset the captures are read in.
const (
	bodyMarker    = 25 // datagram 30
	bodyLength    = 26 // datagram 31
	bodyClass     = 27 // datagram 32
	bodyCore      = 28 // datagram 33
	bodyConstants = 27 // datagram 32, on a header or terminator
	bodyLC        = 33 // datagram 38
	bodySlotType  = 46 // datagram 51
	bodyTail      = 47 // datagram 52
)

// header builds the common first 25 bytes every voice message carries.
func (e *Encoder) preamble(st *encodeState, src hbp.Data, slot int, flags uint16, body []byte) {
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

	// **The high half, not the low.** streamFor packs a 16-bit IPSC stream into
	// the top of a Homebrew stream ID and the sender's radio ID into the
	// bottom, so the low half of a relayed frame is the radio ID and nothing
	// else: every transmission that radio ever makes would leave here carrying
	// one stream ID for ever. A capture on 2026-09-06 shows two overs nineteen
	// seconds apart, on different talkgroups and different timeslots, both
	// going out as 0x0cdee — the low half of 3132910.
	//
	// A receiver tells one transmission from the next by this field. QSP's own
	// listener does exactly that, and so does the converter, so two
	// consecutive overs from one radio would arrive at a repeater with grounds
	// to be treated as one continuing transmission.
	//
	// The high half is the originating IPSC stream when the audio came from a
	// repeater, so a relayed transmission leaves with the stream it arrived
	// with and can be followed across a capture. When it came from a hotspot
	// it is the top of the 32-bit stream MMDVM generates per transmission,
	// which varies as required.
	binary.BigEndian.PutUint16(body[10:12], uint16(src.StreamID>>16))

	// The timeslot bit and the last-frame bit share byte 17 of the frame.
	var b17 byte
	if e.slotBitSet(slot) {
		b17 |= ipsc.FlagSlot
	}
	if flags == flagsLast {
		b17 |= ipsc.FlagTerminator
	}
	body[12] = b17

	binary.BigEndian.PutUint16(body[13:15], flags)
	binary.BigEndian.PutUint16(body[15:17], st.sequence)
	binary.BigEndian.PutUint32(body[17:21], st.timestamp)

	st.sequence++
	// Sixty milliseconds is one DMR voice frame, and 480 samples at eight
	// kilohertz is sixty milliseconds. The captures advance by exactly 480.
	st.timestamp += 480
}

// slotBitSet reports whether the IPSC slot bit should be set for a slot index,
// honouring the configured polarity.
//
// slot 0 is Homebrew timeslot 1 and slot 1 is timeslot 2. When
// SlotBitIsTimeslot2 is true the bit is set for timeslot 2, which is the
// polarity the Converter applies in reverse.
func (e *Encoder) slotBitSet(slot int) bool {
	isTimeslot2 := slot == 1
	return isTimeslot2 == e.cfg.SlotBitIsTimeslot2
}

// signalling builds a 54-byte voice header or terminator.
//
// # The shape, measured
//
// 93 of these were captured from two repeater models and every one is 54 bytes.
// Byte 31 is not a length — a header's length is fixed and the byte carries the
// timeslot instead. Bytes 38 to 49 are the twelve-octet Link Control block,
// byte 51 is the DMR Slot Type, and bytes 52 and 53 are a two-byte tail this
// project cannot derive. See ipsc.HeaderTailLen.
func (e *Encoder) signalling(st *encodeState, src hbp.Data, slot int, flags uint16,
	marker byte, dataType uint8) ipsc.Message {
	body := make([]byte, bodyTail+ipsc.HeaderTailLen)
	e.preamble(st, src, slot, flags, body)

	body[bodyMarker] = marker

	b31 := ipsc.HeaderConstantBit
	if e.slotBitSet(slot) {
		b31 |= ipsc.HeaderSlotBit
	}
	body[bodyLength] = b31

	copy(body[bodyConstants:], ipsc.HeaderConstants[:])

	// The Link Control a repeater needs in order to know who is talking to
	// whom before any audio arrives. Building it can only fail on a malformed
	// data type, and both data types used here are the ones dmrfec supports.
	private := src.CallType == hbp.CallPrivate
	block, err := dmrfec.LinkControlBlock(
		dmrfec.LinkControlFor(src.TargetID, src.SourceID, private), dataType)
	if err == nil {
		copy(body[bodyLC:], block)
	}

	body[bodySlotType] = dmrfec.SlotTypeInfo(e.colourCode, dataType)

	// The two bytes at bodyTail stay zero. They are not derivable from any
	// capture held and a master has no measurement to report there; the
	// length above reserves them so the datagram is the 54 bytes a repeater
	// sends.

	return ipsc.Message{Kind: voiceKindFor(src), SenderID: e.masterID, Body: body}
}

// A header body runs from the preamble to the end of the unresolved tail, and
// that has to come to the 54 bytes measured off the wire. Stated as a
// compile-time check so the two cannot drift apart silently.
const _ = uint(bodyTail + ipsc.HeaderTailLen - (ipsc.HeaderLenTotal - ipsc.HeaderLen))

// voice builds one voice frame: 52 bytes on a synchronisation burst, 66 on the
// burst carrying the assembled Link Control, 57 on the other four.
//
// # Where the shape comes from
//
// A transmission cycles sync, fragment, fragment, fragment, fragment-with-Link
// -Control, fragment — 52 57 57 57 66 57 — in every superframe of every
// transmission in ipsc-two-peers.pcap, from both repeater models. The class at
// byte 32 and the trailer length move together, so the shape is a consequence
// of the burst's position rather than an independent field.
//
// **The position is taken from the burst itself, not from a counter.** The
// trailer carries a Link Control fragment and an LCSS describing that fragment,
// and the two must agree or the frame contradicts itself. Reading both out of
// the same burst makes them agree by construction; a counter running alongside
// could drift after a lost frame and produce a burst whose LCSS says "first
// fragment" over bytes that are a continuation.
func (e *Encoder) voice(st *encodeState, src hbp.Data, slot int, core []byte) ipsc.Message {
	class, lcss, fragment, withLC := e.position(src)

	trailer := 0
	switch {
	case class == ipsc.PayloadSync:
		trailer = 0
	case withLC:
		trailer = 4 + dmrfec.LinkControlBytes + 1
	default:
		trailer = 4 + 1
	}

	body := make([]byte, bodyCore+len(core)+trailer)
	e.preamble(st, src, slot, flagsMiddle, body)

	// The marker carries the timeslot in its high bit, the same fact byte 17
	// holds. A frame built without it is a frame on the wrong slot, and the
	// receiver reads the marker.
	marker := ipsc.FrameVoice
	if e.slotBitSet(slot) {
		marker |= ipsc.FrameSlotBit
	}
	body[bodyMarker] = marker
	body[bodyLength] = byte(len(body) - 27)
	body[bodyClass] = class
	copy(body[bodyCore:], core)

	if trailer > 0 {
		tr := body[bodyCore+len(core):]
		binary.BigEndian.PutUint32(tr[:4], fragment)
		if withLC {
			// The assembled Link Control, so a radio joining mid-transmission
			// learns who is talking without waiting to reassemble four
			// fragments. Motorola sends it once per superframe.
			copy(tr[4:], dmrfec.LinkControlFor(src.TargetID, src.SourceID,
				src.CallType == hbp.CallPrivate))
		}
		// The last byte is the EMB's payload: colour code and LCSS.
		tr[len(tr)-1] = e.colourCode<<4 | lcss<<1
	}

	return ipsc.Message{Kind: voiceKindFor(src), SenderID: e.masterID, Body: body}
}

// voiceKindFor picks the leading byte for a transmission QSP is sending.
//
// **This half is inferred, and the fixture says so.** ipsc-private-voice.pcap
// holds private calls from a repeater to a master and none the other way, so
// what a master sends for one is reasoned from what it sends for a group call
// and from the fact that the two differ in the leading byte, the destination
// and the FLCO. ADR-0041 built the whole outbound voice path that way and it
// matched a real master on 22 of 24 bytes when one was finally captured, which
// is a precedent rather than a proof.
//
// The alternative is worse than an inference: sending a private call as 0x80
// would put a conversation between two members onto a talkgroup.
func voiceKindFor(src hbp.Data) ipsc.Kind {
	if src.CallType == hbp.CallPrivate {
		return ipsc.KindVoicePrivate
	}
	return ipsc.KindVoice
}

// position reads a burst's place in its superframe out of the burst.
//
// A synchronisation burst is named by its frame type and carries no embedded
// signalling at all. Every other burst carries an EMB whose LCSS says which
// fragment it holds, and that is the position: first, continuation, last —
// the one that also carries the assembled Link Control — or single, the burst
// with no fragment.
//
// A burst whose EMB does not verify is treated as a single, which is what a
// burst carrying no embedded Link Control is. That is the honest reading of an
// unreadable EMB and it keeps the audio flowing, which ADR-0036's ordering
// requires.
func (e *Encoder) position(src hbp.Data) (class byte, lcss uint8, fragment uint32, withLC bool) {
	if src.FrameType == hbp.FrameTypeVoiceSync {
		return ipsc.PayloadSync, 0, 0, false
	}
	middle, ok := dmrfec.Middle(src.Payload[:])
	if !ok {
		return ipsc.PayloadFragment, dmrfec.LCSSSingle, 0, false
	}
	emb, frag := dmrfec.SplitMiddle(middle)
	if !dmrfec.ValidEMB(emb) {
		return ipsc.PayloadFragment, dmrfec.LCSSSingle, 0, false
	}
	switch l := dmrfec.LCSSOf(emb); l {
	case dmrfec.LCSSLast:
		return ipsc.PayloadFragmentWithLC, l, frag, true
	default:
		return ipsc.PayloadFragment, l, frag, false
	}
}

// text builds the IP Site Connect frame that carries one Homebrew data burst.
//
// # Why this is not the voice path
//
// A voice frame carries three vocoder frames with their forward error
// correction stripped, and rebuilding one means regenerating that FEC and
// placing the burst in a superframe. A data burst carries a 96-bit information
// block and nothing else: this reads the block back out of the burst and writes
// it where a repeater writes one.
//
// **The layout is the voice header's.** ADR-0045 measured it: the twelve-octet
// block at byte 38, zero at 50, the DMR Slot Type at 51, and the two-byte tail
// ADR-0042 could not derive, written as zero here as it is there.
//
// # What decides the message type
//
// `0x83` for a group text and `0x84` for a private one, which is the only
// difference between them. Sending a private message as a group text would put
// it on a talkgroup for everyone to read, so the call type is taken from the
// frame rather than assumed.
func (e *Encoder) text(st *encodeState, frame hbp.Data, slot int) (ipsc.Message, bool) {
	payload, _, ok := dmrfec.DecodeBPTC(frame.Payload[:])
	if !ok {
		return ipsc.Message{}, false
	}
	block := dmrfec.BurstBytesFrom(payload)
	if len(block) < dmrfec.LinkControlBlockBytes {
		return ipsc.Message{}, false
	}
	block = block[:dmrfec.LinkControlBlockBytes]

	kind := ipsc.KindTextGroup
	if frame.CallType == hbp.CallPrivate {
		kind = ipsc.KindTextPrivate
	}

	body := make([]byte, bodyTail+ipsc.HeaderTailLen)
	e.preamble(st, frame, slot, flagsMiddle, body)

	// Byte 12 of the datagram reads 0x01 on every captured data burst where a
	// voice frame reads 0x02. The preamble writes the voice value because
	// every other caller is voice, so it is corrected here rather than made a
	// parameter nobody else would pass.
	body[7] = 0x01

	body[bodyMarker] = frame.DataType
	b31 := ipsc.HeaderConstantBit
	if e.slotBitSet(slot) {
		b31 |= ipsc.HeaderSlotBit
	}
	body[bodyLength] = b31
	copy(body[bodyConstants:], ipsc.HeaderConstants[:])
	copy(body[bodyLC:], block)
	body[bodySlotType] = dmrfec.SlotTypeInfo(e.colourCode, frame.DataType)

	return ipsc.Message{Kind: kind, SenderID: e.masterID, Body: body}, true
}
