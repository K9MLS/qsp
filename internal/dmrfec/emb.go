package dmrfec

import "fmt"

// The 48 bits in the middle of a voice burst are either a synchronisation
// pattern, on the first burst of a superframe, or embedded signalling on the
// rest: an 8-bit EMB, a 32-bit Link Control fragment, another 8-bit EMB.
//
// IP Site Connect sends the fragment and omits the EMB, because a Motorola
// repeater knows its own colour code and regenerates it. Anything bridging
// toward Homebrew has to supply one.

// LCSS values, the two bits that say where a fragment sits in its superframe.
//
// Read off the captures rather than assumed: the four values appear in the
// order single, first, continuation, last across each superframe, and the burst
// carrying LCSSSingle is the one whose fragment is all zeros in both protocols.
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

// embObserved holds the EMB values seen on the wire, keyed by colour code and
// LCSS.
//
// **This is a table of observations, not a code.** The parity is nine bits over
// seven of colour code, a pre-emption flag and LCSS, and it is provably linear:
// parity(a^b) equals parity(a)^parity(b) across every captured pair. But every
// burst in testdata/hbp/ carries colour code 11, so only the two LCSS bits ever
// moved, and only their contribution can be derived. The colour code bits never
// varied and nothing about them can be honestly inferred.
//
// EMBFor therefore serves the colour code it has seen and refuses the rest,
// naming the capture that would extend it. Guessing here would produce bursts a
// radio silently drops, which is the worst possible failure: audio that goes
// nowhere with nothing in a log.
var embObserved = map[uint8]map[uint8]uint16{
	11: {
		LCSSFirst:        0xB269,
		LCSSContinuation: 0xB68C,
		LCSSLast:         0xB4FF,
		LCSSSingle:       0xB01A,
	},
}

// EMBFor returns the 16-bit EMB for a colour code and LCSS.
func EMBFor(colourCode, lcss uint8) (uint16, error) {
	byLCSS, ok := embObserved[colourCode]
	if !ok {
		return 0, fmt.Errorf("dmrfec: no EMB observed for colour code %d. "+
			"Every burst in testdata/hbp/ carries colour code 11, so the contribution of the "+
			"colour code bits has never been measured. Capture a transmission at this colour "+
			"code and add it, rather than computing one", colourCode)
	}
	v, ok := byLCSS[lcss]
	if !ok {
		return 0, fmt.Errorf("dmrfec: no EMB observed for LCSS %d at colour code %d", lcss, colourCode)
	}
	return v, nil
}

// ObservedColourCodes lists the colour codes EMBFor can serve.
func ObservedColourCodes() []uint8 {
	out := make([]uint8, 0, len(embObserved))
	for cc := range embObserved {
		out = append(out, cc)
	}
	return out
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
