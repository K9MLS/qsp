package hbp

import (
	"encoding/binary"
	"fmt"
)

// DMRD sizing.
//
// The protocol header and payload occupy 53 bytes. Every DMRD frame in
// testdata/hbp/hbp-voice-session.pcap is 55 bytes: MMDVMHost appends two
// trailing bytes understood to carry link quality. Their meaning was not
// established from the capture, so this package preserves them verbatim rather
// than interpreting them.
const (
	dataHeaderSize  = 20 // tag(4) + seq(1) + src(3) + dst(3) + repeater(4) + bits(1) + stream(4)
	dataPayloadSize = 33 // DMR burst
	dataMinSize     = dataHeaderSize + dataPayloadSize
	dataMaxTrailing = 8

	// DataPayloadSize is the length of the DMR burst carried by every frame.
	DataPayloadSize = dataPayloadSize
)

// Timeslot is a DMR timeslot.
type Timeslot uint8

// The two DMR timeslots.
const (
	Timeslot1 Timeslot = 1
	Timeslot2 Timeslot = 2
)

// String implements fmt.Stringer.
func (t Timeslot) String() string { return fmt.Sprintf("TS%d", uint8(t)) }

// CallType distinguishes group from individual calls.
type CallType uint8

// Call types.
const (
	CallGroup   CallType = 0
	CallPrivate CallType = 1
)

// String implements fmt.Stringer.
func (c CallType) String() string {
	if c == CallPrivate {
		return "private"
	}
	return "group"
}

// FrameType classifies a burst within a transmission.
//
// Values are as observed. FrameTypeSync appears exactly twice per stream, at
// its start and end — the voice header and the voice terminator. That invariant
// holds for all fourteen stream traversals in the voice fixture and is the
// primary structural check on a decoder.
type FrameType uint8

// Frame types.
const (
	FrameTypeVoice     FrameType = 0
	FrameTypeVoiceSync FrameType = 1
	FrameTypeSync      FrameType = 2
	FrameTypeReserved  FrameType = 3
)

// String implements fmt.Stringer.
func (f FrameType) String() string {
	switch f {
	case FrameTypeVoice:
		return "voice"
	case FrameTypeVoiceSync:
		return "voice-sync"
	case FrameTypeSync:
		return "sync"
	default:
		return "reserved"
	}
}

// StreamID identifies one transmission.
//
// A stream is the unit of a single keyup: every frame of one transmission
// carries the same StreamID, and it changes when the operator unkeys and keys
// again.
//
// It is not unique across links. The voice fixture shows the same StreamID on
// both the local and master links as a gateway relays a transmission, which is
// why anything counting streams must key on StreamID together with the
// connection it arrived on.
type StreamID uint32

// String implements fmt.Stringer.
func (s StreamID) String() string { return fmt.Sprintf("0x%08x", uint32(s)) }

// Data is a voice or data frame. Tag "DMRD".
//
// # Repeater ID is not stable across a relay
//
// RepeaterID identifies the sender of this particular packet, not the origin of
// the transmission. In the voice fixture, frames arriving from the master carry
// the station's own ID, while the same frames relayed onward by the local
// gateway carry a different one. Code that treats RepeaterID as a stable
// identity for a transmission will be wrong; SourceID is the radio that keyed
// up.
type Data struct {
	Sequence   uint8
	SourceID   uint32
	TargetID   uint32
	RepeaterID RepeaterID
	Timeslot   Timeslot
	CallType   CallType
	FrameType  FrameType
	// DataType is the low nibble of the flags byte. Its interpretation depends
	// on FrameType and was not established from the capture, so it is exposed
	// raw rather than decoded into something this package cannot justify.
	DataType uint8
	StreamID StreamID
	// Payload is the 33-byte DMR burst.
	Payload [dataPayloadSize]byte
	// Trailing holds any bytes after the burst, preserved verbatim so that a
	// frame round-trips exactly. MMDVMHost emits two.
	Trailing []byte
}

// Kind implements Message.
func (Data) Kind() Kind { return KindData }

// Marshal implements Message.
func (m Data) Marshal() []byte {
	return m.AppendTo(make([]byte, 0, dataMinSize+len(m.Trailing)))
}

// AppendTo implements Message.
func (m Data) AppendTo(dst []byte) []byte {
	dst = append(dst, "DMRD"...)
	dst = append(dst, m.Sequence)
	dst = appendU24(dst, m.SourceID)
	dst = appendU24(dst, m.TargetID)

	var id [4]byte
	putID(id[:], m.RepeaterID)
	dst = append(dst, id[:]...)

	dst = append(dst, m.flags())

	var stream [4]byte
	binary.BigEndian.PutUint32(stream[:], uint32(m.StreamID))
	dst = append(dst, stream[:]...)

	dst = append(dst, m.Payload[:]...)
	return append(dst, m.Trailing...)
}

// flags reassembles the bit-packed byte at offset 15.
func (m Data) flags() byte {
	var b byte
	if m.Timeslot == Timeslot2 {
		b |= 0x80
	}
	if m.CallType == CallPrivate {
		b |= 0x40
	}
	b |= (byte(m.FrameType) & 0x03) << 4
	b |= byte(m.DataType) & 0x0F
	return b
}

func parseData(b []byte) (Message, error) {
	if len(b) < dataMinSize {
		return nil, fmt.Errorf("%w: DMRD needs at least %d bytes, got %d", ErrShort, dataMinSize, len(b))
	}
	if trailing := len(b) - dataMinSize; trailing > dataMaxTrailing {
		return nil, fmt.Errorf("%w: DMRD has %d bytes after the burst, at most %d are accepted",
			ErrTrailingBytes, trailing, dataMaxTrailing)
	}

	flags := b[15]
	slot := Timeslot1
	if flags&0x80 != 0 {
		slot = Timeslot2
	}
	call := CallGroup
	if flags&0x40 != 0 {
		call = CallPrivate
	}

	m := Data{
		Sequence:   b[4],
		SourceID:   u24(b[5:8]),
		TargetID:   u24(b[8:11]),
		RepeaterID: getID(b[11:15]),
		Timeslot:   slot,
		CallType:   call,
		FrameType:  FrameType((flags >> 4) & 0x03),
		DataType:   flags & 0x0F,
		StreamID:   StreamID(binary.BigEndian.Uint32(b[16:20])),
	}
	copy(m.Payload[:], b[dataHeaderSize:dataMinSize])

	// Copy rather than alias: the caller's buffer is typically a reused read
	// buffer and would be overwritten by the next packet.
	if trailing := b[dataMinSize:]; len(trailing) > 0 {
		m.Trailing = make([]byte, len(trailing))
		copy(m.Trailing, trailing)
	}
	return m, nil
}

// IsTerminator reports whether this frame is a sync frame.
//
// A stream begins and ends with one. Note that the header and the terminator
// share this frame type, so position within the stream distinguishes them; this
// method alone does not.
func (m Data) IsTerminator() bool { return m.FrameType == FrameTypeSync }

func u24(b []byte) uint32 { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }

func appendU24(dst []byte, v uint32) []byte {
	return append(dst, byte(v>>16), byte(v>>8), byte(v))
}
