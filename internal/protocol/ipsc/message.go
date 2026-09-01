package ipsc

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Kind identifies a message by its leading byte.
//
// IPSC has no published specification, so this is not a list of what the
// protocol contains. It is a list of what testdata/ipsc/ contains.
type Kind byte

const (
	// KindRegisterRequest is the fourteen-byte message a peer sends to its
	// master, unprompted, every ten seconds until answered.
	//
	// The name describes what the message was observed doing, not what
	// Motorola calls it. No reply has ever been captured, so whether this is
	// the whole of registration or the first step of several is unknown.
	KindRegisterRequest Kind = 0x90
)

// RegisterRequestLen is the length of every KindRegisterRequest observed.
// All thirty-five in testdata/ipsc/ are exactly this long.
const RegisterRequestLen = 14

// Errors returned by this package.
var (
	// ErrShort means the input is too small to be the message its leading
	// byte claims.
	ErrShort = errors.New("ipsc: message is shorter than its type requires")
	// ErrTrailingBytes means the input is longer than the message allows.
	ErrTrailingBytes = errors.New("ipsc: message has unexpected trailing bytes")
	// ErrNotCaptured means the leading byte belongs to a message no capture
	// in testdata/ipsc/ contains, so this package refuses to guess at it.
	// See doc.go and ADR-0029.
	ErrNotCaptured = errors.New("ipsc: message type not present in any capture")
)

// ObservedTrailer is the nine bytes following the peer ID in every captured
// request.
//
// **Its meaning is unknown and it is not a validity rule.** Both captures came
// from one XPR8300 on firmware R02.30.20, and the only setting that differed
// between them was the Radio ID — so these bytes being identical says nothing
// about whether another repeater, another firmware or another configuration
// would send the same. Parse deliberately accepts any trailer. This exists so
// that a test can assert what was seen, and so that the day a capture disagrees
// is a visible event rather than a silent one.
var ObservedTrailer = [9]byte{0x6a, 0x00, 0x00, 0x80, 0x4c, 0x04, 0x06, 0x04, 0x00}

// RegisterRequest is a parsed KindRegisterRequest.
type RegisterRequest struct {
	// PeerID is the repeater's Radio ID: a big-endian uint32 at offset 1.
	//
	// This is the one field in the message with an established meaning.
	// Capture A carries 100 and capture B carries 3132910, matching what CPS
	// was set to in each case, and nothing else in the payload moved between
	// them.
	PeerID uint32

	// Trailer is bytes 5 to 13 inclusive, verbatim and uninterpreted.
	//
	// Kept whole rather than split into named fields, because naming a field
	// is a claim about it. When a capture arrives whose trailer differs, the
	// difference is what will name these bytes.
	Trailer [9]byte
}

// Kind reports the message type.
func (RegisterRequest) Kind() Kind { return KindRegisterRequest }

// Marshal renders the message back to the wire.
//
// Marshal(Parse(b)) == b for every frame in testdata/ipsc/, which is the
// property that makes the parser's reading of the bytes checkable rather than
// merely plausible.
func (r RegisterRequest) Marshal() []byte {
	out := make([]byte, RegisterRequestLen)
	out[0] = byte(KindRegisterRequest)
	binary.BigEndian.PutUint32(out[1:5], r.PeerID)
	copy(out[5:], r.Trailer[:])
	return out
}

// String renders the message for a log line.
func (r RegisterRequest) String() string {
	return fmt.Sprintf("register request from peer %d", r.PeerID)
}

// Message is one parsed IPSC packet.
type Message interface {
	Kind() Kind
	Marshal() []byte
}

// Parse reads one IPSC message.
//
// It handles exactly the message types present in testdata/ipsc/ and returns
// ErrNotCaptured for everything else, including leading bytes that other
// implementations are known to use. A wrong guess about a message type produces
// a master that misbehaves quietly, which is worse than one that plainly says
// it cannot handle something yet.
func Parse(b []byte) (Message, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrShort)
	}
	switch Kind(b[0]) {
	case KindRegisterRequest:
		return parseRegisterRequest(b)
	default:
		return nil, fmt.Errorf("%w: leading byte %#02x", ErrNotCaptured, b[0])
	}
}

func parseRegisterRequest(b []byte) (RegisterRequest, error) {
	if len(b) < RegisterRequestLen {
		return RegisterRequest{}, fmt.Errorf("%w: register request is %d bytes, want %d",
			ErrShort, len(b), RegisterRequestLen)
	}
	if len(b) > RegisterRequestLen {
		return RegisterRequest{}, fmt.Errorf("%w: register request is %d bytes, want %d",
			ErrTrailingBytes, len(b), RegisterRequestLen)
	}
	var r RegisterRequest
	r.PeerID = binary.BigEndian.Uint32(b[1:5])
	copy(r.Trailer[:], b[5:])
	return r, nil
}
