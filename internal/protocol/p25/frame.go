package p25

import (
	"errors"
	"fmt"
)

// P25 network frames, as a reflector and a gateway exchange them.
//
// # What this is built from, and what it therefore does not claim
//
// One capture: `testdata/p25/p25-voice.pcap`, seven transmissions between
// MMDVMHost and P25Gateway on a Pi-Star, taken 2026-09-11. Every frame type
// appears 30 or 32 times with no gaps, so the shapes below are observed rather
// than inferred.
//
// **This framing is a convention rather than a published standard.** The P25 air
// interface is specified; the way a gateway packages it into UDP is not, and
// ADR-0029 forbids reading another implementation to find out. So everything
// here comes from the capture, exactly as the Homebrew and IPSC work did — and
// where the capture cannot answer a question, this package says so instead of
// guessing.
//
// # Audio is king, so the bytes are carried and never rebuilt
//
// ADR-0034: a P25 call between P25 endpoints crosses QSP without a vocoder,
// because P25 carries IMBE and DMR carries AMBE+2, and routing one through the
// other is tandem vocoding. That decision has a consequence for this package:
// **QSP has no reason to understand the inside of a voice frame, so it does
// not.** A frame is identified, checked for length, and passed on with its
// payload untouched.
//
// It is not a limitation dressed up as a principle. Every byte QSP rebuilds is
// a byte it can get wrong, and the IPSC work found real defects in exactly that
// territory — two unexplained bytes in a voice burst are still an open question
// there. Carrying a frame verbatim cannot damage audio.
//
// # What the capture could not answer
//
// **Which bytes carry the talkgroup and the source radio.** Frames 0x66 through
// 0x69 each hold three bytes that are byte-identical across all seven
// transmissions, which is where the Link Control lives — but every transmission
// was one radio on one talkgroup, so a field that never changed is
// indistinguishable from framing that never changes. Locating them needs a
// capture with two talkgroups and two radios; see `testdata/p25`.
//
// **The registration handshake.** The capture begins with the gateway already
// running, so it holds steady state and nothing about how that state was
// reached.
//
// Until both are answered, QSP can recognise and relay P25 but cannot route it,
// because routing means knowing the talkgroup.

// Kind is a P25 network frame type, named by the byte that introduces it.
type Kind byte

// The frame types observed in the capture.
const (
	// KindVoice1 through KindVoice9 are the nine frames of a Logical Data
	// Unit — the unit P25 voice arrives in. LDU1 is 0x62 to 0x6A and LDU2 is
	// 0x6B to 0x73, and a transmission alternates between them until it ends.
	KindVoice1 Kind = 0x62
	KindVoice2 Kind = 0x63
	KindVoice3 Kind = 0x64
	KindVoice4 Kind = 0x65
	KindVoice5 Kind = 0x66
	KindVoice6 Kind = 0x67
	KindVoice7 Kind = 0x68
	KindVoice8 Kind = 0x69
	KindVoice9 Kind = 0x6A

	// KindVoice10 through KindVoice18 are the second unit.
	KindVoice10 Kind = 0x6B
	KindVoice11 Kind = 0x6C
	KindVoice12 Kind = 0x6D
	KindVoice13 Kind = 0x6E
	KindVoice14 Kind = 0x6F
	KindVoice15 Kind = 0x70
	KindVoice16 Kind = 0x71
	KindVoice17 Kind = 0x72
	KindVoice18 Kind = 0x73

	// KindTerminator ends a transmission. Seventeen bytes, and every one after
	// the type is zero in all seven transmissions captured.
	KindTerminator Kind = 0x80

	// KindPoll is the keepalive: the type byte and a callsign padded with
	// spaces to ten characters.
	KindPoll Kind = 0xF0
)

// frameLength is the exact length of each frame type, including the type byte.
//
// **Exact, not a minimum.** Every type appeared 30 or 32 times in the capture at
// precisely one length, so a frame of the wrong length is a frame this package
// does not understand — and a decoder that accepted a short one would be
// inventing the difference.
var frameLength = map[Kind]int{
	KindVoice1: 22, KindVoice2: 14, KindVoice3: 17, KindVoice4: 17,
	KindVoice5: 17, KindVoice6: 17, KindVoice7: 17, KindVoice8: 17,
	KindVoice9:  16,
	KindVoice10: 22, KindVoice11: 14, KindVoice12: 17, KindVoice13: 17,
	KindVoice14: 17, KindVoice15: 17, KindVoice16: 17, KindVoice17: 17,
	KindVoice18:    16,
	KindTerminator: 17,
	KindPoll:       11,
}

// Errors this package reports.
var (
	// ErrShort is a datagram with no type byte at all.
	ErrShort = errors.New("p25: a frame needs at least a type byte")
	// ErrUnknownKind is a type byte the capture never showed.
	//
	// **Refused rather than relayed.** A gateway sending something QSP has
	// never seen is a gateway doing something QSP does not understand, and
	// forwarding it blind would put unknown bytes on somebody's repeater.
	ErrUnknownKind = errors.New("p25: unrecognised frame type")
	// ErrLength is a known type at the wrong length.
	ErrLength = errors.New("p25: wrong length for this frame type")
)

// Frame is one P25 network datagram.
//
// **Payload is the bytes after the type, verbatim and uninspected.** Marshal
// returns what Parse was given, byte for byte, because that is the whole of
// what QSP does with P25 audio.
type Frame struct {
	Kind    Kind
	Payload []byte
}

// Voice reports whether this frame carries audio.
func (f Frame) Voice() bool {
	return f.Kind >= KindVoice1 && f.Kind <= KindVoice18
}

// StartsTransmission reports whether this frame begins one.
//
// Every one of the seven transmissions captured opened with 0x62, the first
// frame of LDU1, and no 0x62 appeared mid-transmission other than at the start
// of a later logical data unit — so this is where a call begins and where a
// stream identifier would be assigned.
func (f Frame) StartsTransmission() bool { return f.Kind == KindVoice1 }

// EndsTransmission reports whether this frame ends one.
func (f Frame) EndsTransmission() bool { return f.Kind == KindTerminator }

// Parse reads one datagram.
func Parse(b []byte) (Frame, error) {
	if len(b) < 1 {
		return Frame{}, ErrShort
	}
	kind := Kind(b[0])
	want, known := frameLength[kind]
	if !known {
		return Frame{}, fmt.Errorf("%w: 0x%02x", ErrUnknownKind, byte(kind))
	}
	if len(b) != want {
		return Frame{}, fmt.Errorf("%w: 0x%02x is %d bytes, want %d",
			ErrLength, byte(kind), len(b), want)
	}

	// **Copied, not aliased.** A frame handed on while the caller reuses its
	// read buffer is a frame that changes underneath whoever relays it, and the
	// resulting corruption would look like a radio problem.
	payload := make([]byte, len(b)-1)
	copy(payload, b[1:])
	return Frame{Kind: kind, Payload: payload}, nil
}

// Marshal returns the frame as it arrived.
func (f Frame) Marshal() []byte {
	out := make([]byte, 0, len(f.Payload)+1)
	out = append(out, byte(f.Kind))
	return append(out, f.Payload...)
}

// Poll is the keepalive a gateway sends, carrying the callsign it announces.
type Poll struct {
	// Callsign is the announced callsign, with padding removed.
	Callsign string
	// raw is the payload exactly as it arrived, so a poll QSP relays is the
	// poll it received.
	//
	// **Found by the fuzzer within seconds of the target existing.** A
	// keepalive padded with NUL rather than space came back space-padded,
	// because the trim accepted both and the render wrote one. Harmless in
	// itself, and it breaks the rule this whole package is built on: QSP does
	// not rewrite bytes it carries. A parser that quietly normalises is a
	// parser that will one day normalise something that matters.
	raw []byte
}

// pollCallsignLength is the fixed width of the callsign field.
//
// Ten characters, space padded, observed on every poll in both captures.
const pollCallsignLength = 10

// ParsePoll reads a keepalive.
func ParsePoll(b []byte) (Poll, error) {
	f, err := Parse(b)
	if err != nil {
		return Poll{}, err
	}
	if f.Kind != KindPoll {
		return Poll{}, fmt.Errorf("%w: 0x%02x is not a poll", ErrUnknownKind, byte(f.Kind))
	}
	return Poll{Callsign: trimPadding(f.Payload), raw: f.Payload}, nil
}

// NewPoll builds a keepalive for QSP to send.
//
// Space padded, which is what every poll in both captures uses. A callsign
// longer than the field is truncated rather than refused: the field is a fixed
// width on the wire and there is nowhere for the rest to go.
func NewPoll(callsign string) Poll {
	padded := make([]byte, pollCallsignLength)
	for i := range padded {
		padded[i] = ' '
	}
	copy(padded, callsign)
	return Poll{Callsign: trimPadding(padded), raw: padded}
}

// Marshal renders a keepalive.
//
// **A parsed poll renders exactly as it arrived**, padding bytes and all, so
// relaying one cannot change it. A poll built by NewPoll renders with the
// space padding both captures show.
func (p Poll) Marshal() []byte {
	payload := p.raw
	if payload == nil {
		return NewPoll(p.Callsign).Marshal()
	}
	out := make([]byte, 0, len(payload)+1)
	out = append(out, byte(KindPoll))
	return append(out, payload...)
}

// trimPadding removes the trailing spaces a fixed-width field carries.
func trimPadding(b []byte) string {
	end := len(b)
	for end > 0 && (b[end-1] == ' ' || b[end-1] == 0) {
		end--
	}
	return string(b[:end])
}

// The talkgroup and the source radio.
//
// # How these were located
//
// `testdata/p25/p25-talkgroups.pcap`, 2026-09-11: fourteen transmissions across
// **four** talkgroups, with one talkgroup returned to after another had been
// used. That last part is what makes this a finding rather than a coincidence —
// a byte that follows the talkgroup rather than drifting with time.
//
// The first capture could not answer this. Every transmission in it was one
// radio on one talkgroup, so a field that never changed was indistinguishable
// from framing that never changes.
//
//	frame 0x65, bytes 1–3   0x00039D 925, 0x00270F 9999,
//	                        0x002A88 10888, 0x007BB8 31672
//	frame 0x66, bytes 1–3   0x2FCDEE — 3132910, the transmitting radio, in
//	                        every one of the fourteen
//
// The operator confirmed 925, 9999, 10888 and 31672 are the talkgroups
// programmed into the radio. The reflector addresses in the capture agree from
// the other side: in a P25 gateway a talkgroup is a reflector, and the host
// changed when these bytes did.
//
// **Frames 0x67, 0x68 and 0x69 hold three constant bytes each** whose values
// are neither the radio ID nor any talkgroup. P25 protects its Link Control
// with Reed–Solomon, and that is the obvious explanation — but it is a guess,
// so it is written here as one and nothing depends on it. QSP carries those
// bytes untouched either way.

const (
	// talkgroupFrame carries the talkgroup, and sourceFrame the radio.
	talkgroupFrame = KindVoice4
	sourceFrame    = KindVoice5

	// lcOffset is where each frame's Link Control fragment begins.
	lcOffset = 1
	// lcWidth is how much of it each frame carries.
	lcWidth = 3
)

// ErrNoLinkControl is a request for a field from a frame that does not carry it.
var ErrNoLinkControl = errors.New("p25: this frame carries no link control")

// Talkgroup returns the talkgroup a voice frame names.
//
// **Only frame 0x65 carries it.** A transmission is nine frames per logical
// data unit and the Link Control is spread across several of them, so a caller
// wanting the talkgroup must wait for the right one rather than expecting every
// frame to answer.
//
// The identifier is sixteen bits, and the byte above it was zero in all
// fourteen captured transmissions. It is returned separately rather than folded
// in, so that a capture showing it non-zero produces a visible surprise instead
// of a talkgroup number sixty-five thousand too large.
func (f Frame) Talkgroup() (id uint16, high byte, err error) {
	if f.Kind != talkgroupFrame {
		return 0, 0, fmt.Errorf("%w: 0x%02x is not 0x%02x",
			ErrNoLinkControl, byte(f.Kind), byte(talkgroupFrame))
	}
	if len(f.Payload) < lcOffset+lcWidth-1+1 {
		return 0, 0, ErrShort
	}
	lc := f.Payload[lcOffset-1 : lcOffset-1+lcWidth]
	return uint16(lc[1])<<8 | uint16(lc[2]), lc[0], nil
}

// SourceID returns the radio a voice frame says is transmitting.
//
// Twenty-four bits, which is what a P25 radio identifier is, and it matched the
// operator's own DMR ID exactly in every captured transmission.
//
// **Asserted, not verified.** A radio announces its own identifier and nothing
// in the frame proves it; the same is true of every network QSP speaks, and
// ADR-0052 rule 4 says so in general terms. It decides what an operator reads
// and it must not decide what QSP admits.
func (f Frame) SourceID() (uint32, error) {
	if f.Kind != sourceFrame {
		return 0, fmt.Errorf("%w: 0x%02x is not 0x%02x",
			ErrNoLinkControl, byte(f.Kind), byte(sourceFrame))
	}
	if len(f.Payload) < lcOffset+lcWidth-1+1 {
		return 0, ErrShort
	}
	lc := f.Payload[lcOffset-1 : lcOffset-1+lcWidth]
	return uint32(lc[0])<<16 | uint32(lc[1])<<8 | uint32(lc[2]), nil
}
