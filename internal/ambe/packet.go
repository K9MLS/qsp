package ambe

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// A FieldValue is one field of a packet: an identifier and its data bytes.
type FieldValue struct {
	ID   byte
	Data []byte
}

// Val is shorthand for a field and its data.
func Val(id byte, data ...byte) FieldValue {
	return FieldValue{ID: id, Data: data}
}

// ErrUnknownField and ErrWrongLength are the two ways a packet is refused.
var (
	ErrUnknownType  = errors.New("ambe: not a packet type the manual defines")
	ErrUnknownField = errors.New("ambe: not a field this packet type carries")
	ErrWrongLength  = errors.New("ambe: field data length disagrees with the manual")
)

// Build assembles a packet and refuses to assemble a malformed one.
//
// **This is the only way to make a packet in this package, and that is
// deliberate.** A field whose declared length does not match the bytes that
// follow it does not produce an error from the chip; it produces a chip that
// is mid-field forever and needs its power removed. So the check lives on the
// path every packet takes rather than beside it.
//
// The length written into the header is the sum of all field bytes, per
// §6.5.2, with the four header bytes excluded.
func Build(kind byte, fields ...FieldValue) ([]byte, error) {
	if _, ok := FieldsFor(kind); !ok {
		return nil, fmt.Errorf("%w: type %#02x", ErrUnknownType, kind)
	}

	body := make([]byte, 0, 64)
	for _, fv := range fields {
		spec, ok := lookup(kind, fv.ID)
		if !ok {
			return nil, fmt.Errorf("%w: field %#02x in a %s packet",
				ErrUnknownField, fv.ID, typeName(kind))
		}
		if spec.Data != Variable && len(fv.Data) != spec.Data {
			return nil, fmt.Errorf("%w: %s (%#02x) takes %d data byte(s), given %d",
				ErrWrongLength, spec.Name, spec.ID, spec.Data, len(fv.Data))
		}
		body = append(body, fv.ID)
		body = append(body, fv.Data...)
	}

	out := make([]byte, 0, 4+len(body))
	out = append(out, StartByte)
	out = binary.BigEndian.AppendUint16(out, uint16(len(body)))
	out = append(out, kind)
	return append(out, body...), nil
}

// MustBuild is Build for packets built from constants, where a refusal is a
// programming error rather than a runtime condition.
func MustBuild(kind byte, fields ...FieldValue) []byte {
	p, err := Build(kind, fields...)
	if err != nil {
		panic(err)
	}
	return p
}

// typeName is for error messages, so that a refusal says which table was
// consulted.
func typeName(kind byte) string {
	switch kind {
	case TypeControl:
		return "control"
	case TypeSpeech:
		return "speech"
	case TypeChannel:
		return "channel"
	}
	return fmt.Sprintf("type %#02x", kind)
}

// SpeechD builds a SPEECHD field: the identifier, a sample count, then two
// bytes per sample, most significant first (§6.7.1, Table 99).
//
// The manual bounds the count at 156 to 164 samples, which is skew control's
// range around the nominal 160.
func SpeechD(samples []int16) (FieldValue, error) {
	if len(samples) < 156 || len(samples) > 164 {
		return FieldValue{}, fmt.Errorf(
			"%w: SPEECHD carries 156 to 164 samples, given %d",
			ErrWrongLength, len(samples))
	}
	data := make([]byte, 0, 1+len(samples)*2)
	data = append(data, byte(len(samples)))
	for _, s := range samples {
		data = binary.BigEndian.AppendUint16(data, uint16(s))
	}
	return FieldValue{ID: 0x00, Data: data}, nil
}

// Chand builds a CHAND field: the identifier, a bit count, then the channel
// bits packed eight to a byte (§6.9.1, Table 107).
//
// The manual bounds the count at 40 to 192 bits. For rates that are not a
// multiple of 400 bps the last byte holds its data in the high bits and is
// padded with zeros in the low ones.
func Chand(bits int, data []byte) (FieldValue, error) {
	if bits < 40 || bits > 192 {
		return FieldValue{}, fmt.Errorf("%w: CHAND carries 40 to 192 bits, given %d",
			ErrWrongLength, bits)
	}
	if want := (bits + 7) / 8; len(data) != want {
		return FieldValue{}, fmt.Errorf("%w: %d bits of channel data needs %d byte(s), given %d",
			ErrWrongLength, bits, want, len(data))
	}
	return FieldValue{ID: 0x01, Data: append([]byte{byte(bits)}, data...)}, nil
}

// ParityFieldID is the identifier that precedes the parity byte, §6.5.5.
const ParityFieldID = 0x2F

// ParityByte is the exclusive-or of every byte of a packet except the start
// byte and the parity byte itself (§6.5.5, printed page 61).
//
// It takes the packet as it would be sent complete with the parity field
// identifier, and returns the byte that belongs after it.
//
// **Derived from the manual's prose and not yet seen on the wire.** The
// operator's board has parity disabled — it answered three packets that
// carried none — so nothing here has been checked against hardware. Unlike
// the four example packets in testdata, the manufacturer prints no worked
// example with parity, so this is a reading.
func ParityByte(pkt []byte) byte {
	var p byte
	for _, b := range pkt[1:] {
		p ^= b
	}
	return p
}

// WithParity appends the parity field to a packet and corrects its length.
//
// The parity bytes count toward the length (§6.5.2), which is why this cannot
// be a caller appending two bytes of its own.
func WithParity(pkt []byte) []byte {
	out := make([]byte, len(pkt), len(pkt)+2)
	copy(out, pkt)
	out = append(out, ParityFieldID)
	binary.BigEndian.PutUint16(out[1:3], binary.BigEndian.Uint16(out[1:3])+2)
	return append(out, ParityByte(out))
}
