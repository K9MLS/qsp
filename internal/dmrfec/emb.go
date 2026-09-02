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
// **What the captures directly confirm is the LCSS axis**, because every burst
// in testdata/hbp/ carries colour code 11 and nothing ever moved those four
// bits. The model predicts the colour code axis rather than demonstrating it.
// The prediction is worth far more than a refusal — a bridge that served one
// colour code would be useless — but it is a prediction, and
// testdata/hbp/EMB-CAPTURE-REQUEST.md sets out the two-minute capture that
// turns it into an observation.
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
