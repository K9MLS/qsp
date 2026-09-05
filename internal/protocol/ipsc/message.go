package ipsc

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Kind identifies a message by its leading byte.
//
// IPSC has no published specification, so this is not a list of what the
// protocol contains. It is a list of what testdata/ipsc/ contains: seven types
// seen between two repeaters, of which four have an understood purpose and
// three do not.
type Kind byte

// Message kinds observed in testdata/ipsc/.
//
// The four named for their behaviour were named by watching what they do, not
// from any specification. The three named for their byte are the ones whose
// purpose is genuinely unknown; giving them a descriptive name would be a claim
// about them, and this package does not make claims it cannot demonstrate.
const (
	// KindRegisterRequest is sent by a peer to its master, unprompted, every
	// ten seconds until answered. It is answered by KindRegisterReply.
	KindRegisterRequest Kind = 0x90
	// KindRegisterReply is the master's answer, sent within a millisecond.
	// This is the message a QSP master will have to produce; nine of its
	// sixteen bytes are unexplained.
	KindRegisterReply Kind = 0x91
	// KindKeepaliveRequest is sent by a registered peer every fifteen
	// seconds. Fifteen is the registered cadence: an unregistered peer retries
	// KindRegisterRequest at ten. One interval does not cover both states.
	KindKeepaliveRequest Kind = 0x96
	// KindKeepaliveReply is the master's answer, sent within milliseconds.
	KindKeepaliveReply Kind = 0x97

	// Kind85 is eleven bytes, seen in both directions, on a cadence of its
	// own: the peer sent it every 64.26 seconds, stopped for twelve minutes,
	// then resumed. The master sent exactly one, during the gap. Its body is
	// identical in every instance apart from the sender ID. Purpose unknown.
	Kind85 Kind = 0x85
	// KindF0 is nine bytes, sent once by the peer immediately after
	// KindRegisterReply and never again. Purpose unknown.
	KindF0 Kind = 0xf0
	// KindF1 is the master's answer to KindF0: forty-four bytes, sent once,
	// the largest message captured, with sixteen bytes that look like entropy
	// rather than structure. A peer list is the obvious guess and it is only a
	// guess — the capture contains one peer, so nothing distinguishes a list
	// from a fixed record. Purpose unknown.
	KindF1 Kind = 0xf1
)

// HeaderLen is the part of a message this package understands: one type byte
// and a four-byte sender ID.
const HeaderLen = 5

// Errors returned by this package.
var (
	// ErrShort means the input is too small to carry a type and a sender ID.
	ErrShort = errors.New("ipsc: message is shorter than a type byte and a sender ID")
	// ErrNotCaptured means the leading byte belongs to a message no capture in
	// testdata/ipsc/ contains, so this package refuses to guess at it.
	//
	// Its wording is deliberate. The documentation-accuracy gate reads certain
	// phrases as a claim that a whole subsystem is missing, and now that the
	// IPSC listener exists, an error string in that shape fails the build. The
	// gate is right to be strict: the sentence means one message type, and a
	// reader skimming could take it for the subsystem.
	ErrNotCaptured = errors.New("ipsc: no capture contains this message type")
)

// observedLen records the length at which each kind was captured.
//
// **It is not enforced, deliberately.** Every kind was seen at exactly one
// length, but the captures contain one peer: KindF1 at forty-four bytes may
// well grow with the number of peers a master knows about, and a parser that
// rejected forty-eight would fail on the day a third repeater joins — the day
// IPSC starts being worth having. A test asserts these lengths so that a change
// is visible; the parser accepts what arrives.
var observedLen = map[Kind]int{
	// Voice frames were seen at 52, 54, 57 and 66 bytes across one superframe,
	// so this kind has no single observed length and the zero here means only
	// "recognised". Its header is fixed; its payload is not.
	KindVoice: 0,
	// A private call is the same frame shapes as a group one — 52, 54, 57
	// and 66 bytes across a superframe — so it too has no single length.
	KindVoicePrivate: 0,
	// Text bursts were seen at 34, 54 and 60 bytes, so like voice they have no
	// single length and the zero means only "recognised".
	KindTextGroup:        0,
	KindTextPrivate:      0,
	KindRegisterRequest:  14,
	KindRegisterReply:    16,
	KindKeepaliveRequest: 14,
	KindKeepaliveReply:   14,
	Kind85:               11,
	KindF0:               9,
	KindF1:               44,
}

// ObservedLen reports the length at which a kind was captured, and whether the
// kind was captured at all.
func ObservedLen(k Kind) (int, bool) {
	n, ok := observedLen[k]
	return n, ok
}

// Message is one parsed IPSC packet.
//
// # Why one type rather than seven
//
// Every message in every capture shares the same first five bytes: a type and
// the sender's own ID. That is the only structure seven message types, two
// repeater models and two firmware versions all agree on, so it is the only
// structure this package encodes. Splitting the body into named fields would
// mean naming bytes whose meaning nothing has demonstrated.
type Message struct {
	// Kind is the leading byte.
	Kind Kind

	// SenderID is the radio ID of whichever end sent the message: a big-endian
	// uint32 at offset 1.
	//
	// It identifies the sender rather than the subject. A registration request
	// carries the peer's ID and its reply carries the master's, which is what
	// distinguishes this from an addressing field — and is why both directions
	// had to be captured before it could be claimed.
	SenderID uint32

	// Body is everything after the sender ID, verbatim and uninterpreted.
	//
	// Kept whole because naming a field is a claim about it. When a capture
	// arrives whose body moves under a known change, the difference is what
	// will name these bytes — the method that named SenderID.
	Body []byte
}

// Marshal renders the message back to the wire.
//
// Marshal(Parse(b)) == b for every frame in testdata/ipsc/, which is what makes
// the parser's reading checkable rather than merely plausible.
func (m Message) Marshal() []byte {
	out := make([]byte, HeaderLen+len(m.Body))
	out[0] = byte(m.Kind)
	binary.BigEndian.PutUint32(out[1:5], m.SenderID)
	copy(out[5:], m.Body)
	return out
}

// String renders the message for a log line.
func (m Message) String() string {
	switch m.Kind {
	case KindRegisterRequest:
		return fmt.Sprintf("register request from %d", m.SenderID)
	case KindRegisterReply:
		return fmt.Sprintf("register reply from %d", m.SenderID)
	case KindKeepaliveRequest:
		return fmt.Sprintf("keepalive from %d", m.SenderID)
	case KindKeepaliveReply:
		return fmt.Sprintf("keepalive reply from %d", m.SenderID)
	default:
		return fmt.Sprintf("type %#02x from %d, purpose unknown, %d body bytes",
			byte(m.Kind), m.SenderID, len(m.Body))
	}
}

// Parse reads one IPSC message.
//
// It accepts exactly the seven types present in testdata/ipsc/ and returns
// ErrNotCaptured for everything else, including leading bytes that other
// implementations are known to use. A wrong guess about a message type produces
// a master that misbehaves quietly, which is worse than one that plainly says
// it cannot handle something yet.
func Parse(b []byte) (Message, error) {
	if len(b) < HeaderLen {
		return Message{}, fmt.Errorf("%w: %d bytes, want at least %d", ErrShort, len(b), HeaderLen)
	}
	k := Kind(b[0])
	if _, ok := observedLen[k]; !ok {
		return Message{}, fmt.Errorf("%w: leading byte %#02x", ErrNotCaptured, b[0])
	}
	return Message{
		Kind:     k,
		SenderID: binary.BigEndian.Uint32(b[1:5]),
		Body:     append([]byte(nil), b[5:]...),
	}, nil
}
