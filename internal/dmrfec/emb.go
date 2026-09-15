package dmrfec

import "fmt"

// The 48 bits in the middle of a voice burst are either a synchronisation
// pattern, on the first burst of a superframe, or embedded signalling on the
// rest: an 8-bit EMB, a 32-bit Link Control fragment, another 8-bit EMB.
//
// IP Site Connect sends the fragment and omits the EMB, because a Motorola
// repeater knows its own colour code and rebuilds it. Anything bridging toward
// Homebrew has to supply one.

// LCSS values: the two bits that say where a fragment sits in its superframe.
//
// Read off the captures rather than assumed. The burst carrying LCSSSingle is
// the one whose fragment is all zeros in both protocols, which is what tied the
// two together.
const (
	// LCSSSingle marks a burst with no Link Control fragment.
	LCSSSingle uint8 = 0
	// LCSSFirst marks the first fragment.
	LCSSFirst uint8 = 1
	// LCSSLast marks the last.
	LCSSLast uint8 = 2
	// LCSSContinuation marks the ones between.
	LCSSContinuation uint8 = 3
)

// embGenerator is the generator polynomial of the code protecting the EMB:
// x^8 + x^5 + x^4 + x^3 + 1.
//
// # How it was found, and how far the evidence reaches
//
// The EMB is sixteen bits: colour code (4), a pre-emption flag (1), LCSS (2),
// and nine of protection. The parity is provably linear — parity(a^b) equals
// parity(a)^parity(b) across every captured pair — but a systematic cyclic code
// with a nine-bit generator does not reproduce it in any bit order.
//
// It is a fifteen-bit codeword with an overall parity bit appended, and
// searching all 256 degree-eight generators under that model leaves **exactly
// one** that reproduces every captured EMB. One survivor out of 256 against
// four independent nine-bit observations is not a coincidence that needs
// entertaining.
//
// **The colour code axis was a prediction and is now an observation.** Until
// 2026-09-15 every burst this project held carried colour code 11, nothing had
// ever moved those four bits, and the model predicted that axis rather than
// demonstrating it.
//
// testdata/hbp/hbp-emb-colourcode-4.pcap holds a second repeater transmitting
// at **colour code 4**, and this generator reproduces all four of its EMBs —
// 411e, 436d, 45fb and 4788. Four unseen values predicted correctly on the
// first attempt, against a generator chosen because it was the only one of 256
// to fit the first colour code.
//
// **It is still not every colour code.** 11 is 1011 and 4 is 0100, two vectors
// where spanning four bits needs four, so codes 1, 2 and 8 would complete it —
// see testdata/hbp/EMB-CAPTURE-REQUEST.md. But a generator this well
// corroborated is one to rely on rather than refuse.
const embGenerator = 0x139

// EMBFor computes the sixteen-bit EMB for a colour code and LCSS.
//
// The pre-emption bit is zero: it marks an encrypted transmission and every
// capture this project holds has it clear. A network using it needs a capture
// before this function is trusted with one.
func EMBFor(colourCode, lcss uint8) (uint16, error) {
	if colourCode > 15 {
		return 0, fmt.Errorf("dmrfec: colour code %d is out of range; DMR allows 0 to 15", colourCode)
	}
	if lcss > 3 {
		return 0, fmt.Errorf("dmrfec: LCSS %d is out of range; it is two bits", lcss)
	}
	info := uint32(colourCode)<<3 | uint32(lcss)
	code15 := info<<8 | embParity(info<<8)
	return uint16(code15<<1) | uint16(popcount(code15)&1), nil
}

func embParity(v uint32) uint32 {
	for i := 15; i >= 8; i-- {
		if v>>uint(i)&1 == 1 {
			v ^= embGenerator << uint(i-8)
		}
	}
	return v & 0xFF
}

// EmbeddedMiddle builds the 48 bits that sit between a burst's payload halves
// from an EMB and a Link Control fragment.
func EmbeddedMiddle(emb uint16, fragment uint32) uint64 {
	return uint64(emb>>8)<<40 | uint64(fragment)<<8 | uint64(emb&0xFF)
}

// SplitMiddle takes those 48 bits apart again.
func SplitMiddle(middle uint64) (emb uint16, fragment uint32) {
	emb = uint16(middle>>40)<<8 | uint16(middle&0xFF)
	fragment = uint32(middle >> 8)
	return emb, fragment
}

// ColourCodeOf reads the colour code out of an EMB.
func ColourCodeOf(emb uint16) uint8 { return uint8(emb >> 12) }

// LCSSOf reads the LCSS out of an EMB.
func LCSSOf(emb uint16) uint8 { return uint8(emb>>9) & 3 }

// ValidEMB reports whether an EMB's parity is consistent, which is how a
// receiver tells signalling from noise.
func ValidEMB(emb uint16) bool {
	want, err := EMBFor(ColourCodeOf(emb), LCSSOf(emb))
	return err == nil && want == emb
}

// SuperframeBursts is how many bursts one DMR superframe holds: six of 60 ms,
// 360 ms in all.
const SuperframeBursts = 6

// LCSSForPosition returns the LCSS for a burst's position in its superframe,
// counting the synchronisation burst as zero.
//
// # Where the order comes from
//
// The Homebrew captures, read as sequences rather than as a set. Following each
// synchronisation burst, the LCSS runs first, continuation, continuation, last,
// single — 71 superframes of 72 agree exactly, and the two that do not are cut
// short by the end of a transmission.
//
// The reading needed one correction on the way. Every LCSS appeared twice in a
// row, which looked like a twelve-burst superframe until the cause became
// obvious: the capture holds each burst twice, once arriving from a hotspot and
// once as QSP relays it onward. A capture taken at a master sees both halves of
// its own traffic.
//
// ok is false for position zero, the synchronisation burst, which carries a
// pattern rather than signalling and so has no LCSS at all.
func LCSSForPosition(position int) (lcss uint8, ok bool) {
	switch position {
	case 0:
		return 0, false // synchronisation burst
	case 1:
		return LCSSFirst, true
	case 2, 3:
		return LCSSContinuation, true
	case 4:
		return LCSSLast, true
	case 5:
		return LCSSSingle, true
	default:
		return 0, false
	}
}

// MiddleForPosition builds the 48 bits between a burst's payload halves for a
// given position in the superframe.
//
// The synchronisation burst gets the pattern and ignores the fragment. Every
// other burst gets an EMB computed from the colour code and its position, with
// the fragment between the two halves of it.
func MiddleForPosition(position int, colourCode uint8, fragment uint32) (uint64, error) {
	if position == 0 {
		return VoiceSyncBS, nil
	}
	lcss, ok := LCSSForPosition(position)
	if !ok {
		return 0, fmt.Errorf("dmrfec: burst position %d is outside a superframe of %d",
			position, SuperframeBursts)
	}
	emb, err := EMBFor(colourCode, lcss)
	if err != nil {
		return 0, err
	}
	return EmbeddedMiddle(emb, fragment), nil
}
