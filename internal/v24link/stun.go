// Package quantar answers a Motorola Quantar's V.24 link, carried to QSP by a
// Cisco router's serial tunnel (ADR-0060).
//
// **Everything here is read off testdata/quantar.** The tunnel's header has no
// specification this project holds, so its shape is what the router sent on
// 2026-10-04 and nothing more: a two-byte marker, a two-byte type, a two-byte
// length, one further byte, and then that many bytes of whatever the serial
// line carried. What is not in a capture is not in this package.
package v24link

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Marker opens every tunnel frame the router sent.
const Marker = 0x0831

// HeaderLen is the tunnel header: marker, type, length and one more byte.
const HeaderLen = 7

// MaxPayload is the largest serial frame the router can hand over. IOS set an
// MTU of 2104 on the serial interface itself, so a longer length is not a
// frame; it is a stream that has lost its place.
const MaxPayload = 2104

// Op is the tunnel's frame type.
type Op uint16

const (
	// OpData carries one frame from the serial line. Every frame after the
	// first in the capture is this.
	OpData Op = 0x0000
	// OpOpen is what the router sends once, first, when it connects. Its
	// thirty bytes are recorded and not interpreted. **It needs no answer**:
	// the router reported the tunnel open and carried frames for six minutes
	// to a listener that sent nothing back.
	OpOpen Op = 0x0002
)

// ErrNotTunnel reports bytes that do not begin with the tunnel's marker.
var ErrNotTunnel = errors.New("v24link: not a serial tunnel frame")

// ErrTooLong reports a length no serial frame can have.
var ErrTooLong = errors.New("v24link: frame length is beyond the serial line's limit")

// Frame is one tunnel frame.
type Frame struct {
	Op Op
	// Group is the header's seventh byte. It was 1 in every frame captured and
	// the router's interface is in `stun group 1`, which is why it has this
	// name. One capture from one group cannot prove it, so it is copied into
	// every reply rather than set.
	Group byte
	// Payload is what the serial line carried, without the line's own
	// checksum: the router removes that.
	Payload []byte
}

// ReadFrame reads exactly one frame.
//
// It reads the header and then the length the header gives, so a frame the
// network delivered in pieces is one frame and two frames delivered together
// are two.
func ReadFrame(r io.Reader) (Frame, error) {
	var head [HeaderLen]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return Frame{}, err
	}
	if binary.BigEndian.Uint16(head[0:2]) != Marker {
		return Frame{}, fmt.Errorf("%w: began % x", ErrNotTunnel, head[0:2])
	}
	length := int(binary.BigEndian.Uint16(head[4:6]))
	if length > MaxPayload {
		return Frame{}, fmt.Errorf("%w: %d bytes", ErrTooLong, length)
	}
	f := Frame{
		Op:      Op(binary.BigEndian.Uint16(head[2:4])),
		Group:   head[6],
		Payload: make([]byte, length),
	}
	if _, err := io.ReadFull(r, f.Payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return Frame{}, fmt.Errorf("v24link: frame cut short: %w", err)
	}
	return f, nil
}

// Append writes the frame to dst as the router writes one.
func (f Frame) Append(dst []byte) []byte {
	dst = binary.BigEndian.AppendUint16(dst, Marker)
	dst = binary.BigEndian.AppendUint16(dst, uint16(f.Op))
	dst = binary.BigEndian.AppendUint16(dst, uint16(len(f.Payload)))
	dst = append(dst, f.Group)
	return append(dst, f.Payload...)
}
