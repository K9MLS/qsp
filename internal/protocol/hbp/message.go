package hbp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Kind identifies a message type by its wire tag.
type Kind string

// Message kinds observed in testdata/hbp/ and implemented here.
const (
	// KindLogin is a peer's initial login request. Tag "RPTL".
	KindLogin Kind = "RPTL"
	// KindAck is a master's acknowledgement. Tag "RPTACK". Its four-byte
	// payload is an authentication salt or a repeater ID depending on
	// connection state; see Ack.
	KindAck Kind = "RPTACK"
	// KindKey is a peer's authentication response. Tag "RPTK".
	KindKey Kind = "RPTK"
	// KindConfig is a peer's full configuration announcement. Tag "RPTC".
	KindConfig Kind = "RPTC"
	// KindPing is a peer's keepalive. Tag "RPTPING".
	KindPing Kind = "RPTPING"
	// KindPong is a master's keepalive reply. Tag "MSTPONG".
	KindPong Kind = "MSTPONG"
	// KindData is a voice or data frame. Tag "DMRD".
	KindData Kind = "DMRD"
	// KindGatewayConfig is the abbreviated configuration used on a local
	// gateway link. Tag "DMRC".
	KindGatewayConfig Kind = "DMRC"
	// KindGatewayPong is the four-byte keepalive used on a local gateway link.
	// Tag "DMRP".
	KindGatewayPong Kind = "DMRP"
)

// Errors returned by this package.
var (
	// ErrShort means the input is too small to be the message its tag claims.
	ErrShort = errors.New("hbp: message is shorter than its type requires")
	// ErrUnknownKind means the leading bytes match no recognised tag.
	ErrUnknownKind = errors.New("hbp: unrecognised message tag")
	// ErrNotCaptured means the tag belongs to a message this build recognises
	// by name but has never observed on the wire, and therefore refuses to
	// parse. See ADR-0008 and testdata/hbp/README.
	ErrNotCaptured = errors.New("hbp: message type recognised but not implemented")
	// ErrTrailingBytes means the input is longer than the message allows.
	ErrTrailingBytes = errors.New("hbp: message has unexpected trailing bytes")
	// ErrFieldFormat means a fixed-width text field contains bytes that cannot
	// appear on the wire.
	ErrFieldFormat = errors.New("hbp: field contains invalid characters")
)

// Message is one parsed HBP packet.
//
// Every implementation round-trips: for any m obtained from Parse,
// Parse(m.Marshal()) yields an equal message with identical bytes. That
// property is enforced by test against every frame in the captured fixtures.
type Message interface {
	// Kind reports the message type.
	Kind() Kind
	// Marshal returns the wire encoding.
	Marshal() []byte
	// AppendTo appends the wire encoding to dst, allowing a caller to reuse a
	// buffer on a hot path.
	AppendTo(dst []byte) []byte
}

// uncapturedTags are message tags known to exist in HBP that do not appear in
// any fixture.
//
// They are listed so that Parse can refuse them precisely instead of either
// misparsing them or reporting them as unknown. RPTCL is the reason this list
// is necessary: it shares a four-byte prefix with RPTC, so without an explicit
// guard a close message would be parsed as a truncated configuration.
var uncapturedTags = []struct {
	tag  string
	note string
}{
	{"RPTO", "peer options; the specification does not define it, so capture a hotspot sending an options string to implement it"},
	{"RPTSBKN", "beacon; neither specified nor observed"},
}

// Parse decodes a single HBP message.
//
// Input is treated as hostile. Parse never panics, never reads beyond the slice,
// and does not retain it: every returned message owns its own copy of any
// variable data.
func Parse(b []byte) (Message, error) {
	if len(b) < 4 {
		return nil, fmt.Errorf("%w: got %d bytes, need at least 4 for a message tag", ErrShort, len(b))
	}

	// Longer tags are tested first: RPTPING and RPTCL both begin with RPT, and
	// RPTCL shares its first four bytes with RPTC.
	for _, u := range uncapturedTags {
		if hasTag(b, u.tag) {
			return nil, fmt.Errorf("%w: %q (%s)", ErrNotCaptured, u.tag, u.note)
		}
	}

	switch {
	// Longer tags first: RPTPING, RPTPONG and RPTP share a prefix, as do
	// MSTPING and MSTPONG, and RPTCL shares four bytes with RPTC.
	case hasTag(b, "RPTPING"):
		return parsePing(b)
	case hasTag(b, "MSTPONG"):
		return parsePong(b)
	case hasTag(b, "RPTPONG"):
		return parseRepeaterPong(b)
	case hasTag(b, "MSTPING"):
		return parseMasterPing(b)
	case hasTag(b, "MSTNAK"):
		return parseNak(b)
	case hasTag(b, "MSTACK"):
		return parseMasterAck(b)
	case hasTag(b, "MSTCL"):
		return parseMasterClose(b)
	case hasTag(b, "RPTCL"):
		return parseRepeaterClose(b)
	case hasTag(b, "RPTACK"):
		return parseAck(b)
	case hasTag(b, "RPTL"):
		return parseLogin(b)
	case hasTag(b, "RPTK"):
		return parseKey(b)
	case hasTag(b, "RPTC"):
		return parseConfig(b)
	case hasTag(b, "DMRD"):
		return parseData(b)
	case hasTag(b, "DMRC"):
		return parseGatewayConfig(b)
	case hasTag(b, "DMRP"):
		return parseGatewayPong(b)
	}

	return nil, fmt.Errorf("%w: leading bytes %q", ErrUnknownKind, printable(b[:4]))
}

// hasTag reports whether b begins with tag.
func hasTag(b []byte, tag string) bool {
	if len(b) < len(tag) {
		return false
	}
	for i := 0; i < len(tag); i++ {
		if b[i] != tag[i] {
			return false
		}
	}
	return true
}

// printable renders bytes for an error message without emitting control
// characters into a log.
func printable(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c < 0x7f {
			out[i] = c
			continue
		}
		out[i] = '.'
	}
	return string(out)
}

// exactLen validates a fixed-size message.
func exactLen(b []byte, want int, kind Kind) error {
	if len(b) < want {
		return fmt.Errorf("%w: %s is %d bytes, got %d", ErrShort, kind, want, len(b))
	}
	if len(b) > want {
		return fmt.Errorf("%w: %s is %d bytes, got %d", ErrTrailingBytes, kind, want, len(b))
	}
	return nil
}

// RepeaterID is a DMR radio or repeater identifier.
//
// It is carried as a four-byte big-endian integer in control messages and as
// three bytes in DMRD, which is why it is a distinct type rather than a bare
// uint32: the two encodings are easy to confuse.
type RepeaterID uint32

// String implements fmt.Stringer.
func (r RepeaterID) String() string { return fmt.Sprintf("%d", uint32(r)) }

func putID(dst []byte, id RepeaterID) { binary.BigEndian.PutUint32(dst, uint32(id)) }
func getID(b []byte) RepeaterID       { return RepeaterID(binary.BigEndian.Uint32(b)) }
