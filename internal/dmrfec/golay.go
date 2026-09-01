// Package dmrfec converts between the two shapes DMR voice takes on a network.
//
// # The problem it solves
//
// The Homebrew protocol forwards the burst a radio put on the air: 264 bits
// carrying three 72-bit vocoder frames *including* forward error correction.
// Motorola's IP Site Connect forwards the vocoder parameters alone: three
// 49-bit frames, unprotected. Both carry the same speech from the same radio,
// so a gateway between them adds or removes exactly one layer.
//
// **Nothing here decodes audio.** The 49 parameter bits pass through untouched
// and only the wrapper around them changes, so this is not transcoding and must
// never become it. See docs/adr/ADR-0034 and ADR-0036.
//
// # Provenance
//
// The transformation is published, not reverse-engineered. ETSI TS 102 361-1
// defines the burst: 264 bits, three 72-bit vocoder frames including FEC plus a
// 48-bit synchronisation field placed in the middle. The 49-to-72 encoding
// comes from the P25 half-rate vocoder specification: one [24,12] extended
// Golay code and one [23,12] Golay code protect the 24 most sensitive bits, the
// remaining 25 are left unprotected, the [23,12] codeword is scrambled by a
// pseudo-random sequence keyed on the first 12 bits, and the 72 bits are
// interleaved.
//
// Published or not, every claim here is checked against real traffic:
// dmrfec_test.go runs the whole chain over the 916 captured bursts in
// testdata/hbp/. 99% of voice bursts decode with a zero syndrome, and the
// exceptions are genuine over-the-air bit errors the codes then correct. A
// wrong reading of the interleave or the scrambler scores zero.
package dmrfec

// Golay generator polynomial for the [23,12] code: x^11 + x^10 + x^6 + x^5 +
// x^4 + x^2 + 1.
const golay23Poly = 0xC75

// golay23Syndromes maps a syndrome to the error pattern that produced it.
//
// [23,12] Golay is a perfect code: the 2^11 syndromes correspond exactly to the
// 1 + 23 + 253 + 1771 = 2048 error patterns of weight three or less, so the
// table is complete and every syndrome has exactly one answer. No search and no
// ambiguity at decode time.
var golay23Syndromes [2048]uint32

func init() {
	for i := range golay23Syndromes {
		golay23Syndromes[i] = ^uint32(0)
	}
	golay23Syndromes[0] = 0
	// Weight one, two and three, in that order, so a lower weight always wins
	// if two patterns somehow collided. They cannot in a perfect code; writing
	// it this way means the table is right even if that ever stopped holding.
	for a := 0; a < 23; a++ {
		record(1 << a)
		for b := a + 1; b < 23; b++ {
			record(1<<a | 1<<b)
			for c := b + 1; c < 23; c++ {
				record(1<<a | 1<<b | 1<<c)
			}
		}
	}
}

func record(pattern uint32) {
	s := syndrome23(pattern)
	if golay23Syndromes[s] == ^uint32(0) {
		golay23Syndromes[s] = pattern
	}
}

// syndrome23 is the remainder of a 23-bit word divided by the generator.
func syndrome23(word uint32) uint32 {
	r := word
	for i := 22; i >= 11; i-- {
		if r&(1<<uint(i)) != 0 {
			r ^= golay23Poly << uint(i-11)
		}
	}
	return r & 0x7FF
}

// EncodeGolay23 produces a systematic [23,12] codeword from 12 data bits.
func EncodeGolay23(data uint32) uint32 {
	data &= 0xFFF
	return data<<11 | syndrome23(data<<11)
}

// EncodeGolay24 produces a [24,12] extended codeword: the [23,12] codeword with
// an overall even parity bit appended.
func EncodeGolay24(data uint32) uint32 {
	c := EncodeGolay23(data)
	return c<<1 | uint32(popcount(c)&1)
}

// DecodeGolay23 corrects up to three bit errors and returns the data bits and
// how many bits were corrected.
func DecodeGolay23(word uint32) (data uint32, corrected int) {
	word &= 0x7FFFFF
	e := golay23Syndromes[syndrome23(word)]
	return (word ^ e) >> 11 & 0xFFF, popcount(e)
}

// DecodeGolay24 corrects the [24,12] extended codeword.
//
// The extra parity bit is used to detect rather than to correct: it is dropped
// before decoding, which is what the vocoder specification's own decoder does.
// Treating it as a fourth correctable bit would sometimes turn a detected
// four-bit error into a confidently wrong frame.
func DecodeGolay24(word uint32) (data uint32, corrected int) {
	return DecodeGolay23(word >> 1)
}

func popcount(v uint32) int {
	n := 0
	for v != 0 {
		v &= v - 1
		n++
	}
	return n
}
