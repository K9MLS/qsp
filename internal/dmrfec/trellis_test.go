package dmrfec_test

import (
	"encoding/hex"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// burstFrom rebuilds a DMR burst from 196 payload bits, one per byte, with the
// 48-bit sync field between the halves left zero.
func burstFrom(bits []byte) []byte {
	all := make([]byte, 264)
	copy(all[0:98], bits[0:98])
	copy(all[166:264], bits[98:196])
	burst := make([]byte, dmrfec.BurstBytes)
	for i, b := range all {
		if b == 1 {
			burst[i/8] |= 0x80 >> (i % 8)
		}
	}
	return burst
}

// TestATrellisBlockSurvivesTheRoundTrip is the weaker half of this file, and
// it says so.
//
// **It would pass on a mistranscribed table.** Encode and decode share the
// state transition, constellation and interleave tables, so an error in any of
// them cancels out. It is here because a codec that cannot even agree with
// itself is broken beyond argument, not because it proves the tables right.
func TestATrellisBlockSurvivesTheRoundTrip(t *testing.T) {
	in := make([]byte, dmrfec.TrellisPayloadBytes)
	for i := range in {
		in[i] = byte(i*37 + 11)
	}
	bits, ok := dmrfec.EncodeTrellis(in)
	if !ok {
		t.Fatal("the encoder refused 18 octets")
	}
	if len(bits) != 196 {
		t.Fatalf("a Rate 3/4 block encoded to %d bits, want 196", len(bits))
	}
	out, ok := dmrfec.DecodeTrellis(burstFrom(bits))
	if !ok {
		t.Fatal("the decoder refused a burst this package built")
	}
	if hex.EncodeToString(out) != hex.EncodeToString(in) {
		t.Errorf("round trip: in %s, out %s",
			hex.EncodeToString(in), hex.EncodeToString(out))
	}
}

// TestEveryTribitIsReachableFromEveryState checks the state transition table
// for the property the specification claims for it.
//
// **This one a mistranscription can fail.** Table B.7 is a Latin square: each
// of the eight states maps the eight input tribits onto eight distinct
// constellation points, and the sixteen points are covered twice across the
// table. A transposed row, a duplicated entry or a dropped digit breaks that,
// and decoding depends on it: DecodeTrellis finds the input by looking for the
// observed point in the current state's row, and a row with a repeat would make
// that answer ambiguous.
func TestEveryTribitIsReachableFromEveryState(t *testing.T) {
	points := map[byte]int{}
	for state := 0; state < 8; state++ {
		seen := map[byte]bool{}
		for input := 0; input < 8; input++ {
			p := dmrfec.TrellisPoint(byte(state), byte(input))
			if p > 15 {
				t.Fatalf("state %d input %d gives point %d, outside the 16", state, input, p)
			}
			if seen[p] {
				t.Errorf("state %d reaches point %d from two different tribits, "+
					"so decoding that point is ambiguous", state, p)
			}
			seen[p] = true
			points[p]++
		}
	}
	for p := byte(0); p < 16; p++ {
		if points[p] != 4 {
			t.Errorf("constellation point %d appears %d times across the table, want 4",
				p, points[p])
		}
	}
}

// TestTheConstellationMappingIsOneToOne guards Table B.8.
//
// Sixteen points, sixteen distinct dibit pairs, and every one of the sixteen
// possible pairs used exactly once — the mapping is a permutation. A duplicate
// would make two points indistinguishable on the wire and a decoder would pick
// whichever it found first.
func TestTheConstellationMappingIsOneToOne(t *testing.T) {
	seen := map[[2]byte]int{}
	for p := byte(0); p < 16; p++ {
		pair := dmrfec.TrellisDibits(p)
		if prev, ok := seen[pair]; ok {
			t.Errorf("points %d and %d both send %v", prev, p, pair)
		}
		seen[pair] = int(p)
	}
	if len(seen) != 16 {
		t.Errorf("%d distinct dibit pairs across 16 points", len(seen))
	}
}

// TestTheInterleaveScheduleIsAPermutation guards Table B.9.
//
// Ninety-eight entries covering 0 to 97 exactly once. A duplicate would drop
// one dibit and double another, which is the kind of error that leaves a
// decoder working on most bursts and failing on some — the worst kind to find
// on air.
func TestTheInterleaveScheduleIsAPermutation(t *testing.T) {
	sched := dmrfec.TrellisInterleaveSchedule()
	if len(sched) != 98 {
		t.Fatalf("the schedule has %d entries, want 98", len(sched))
	}
	seen := make([]bool, 98)
	for i, v := range sched {
		if v < 0 || v > 97 {
			t.Fatalf("entry %d is %d, outside 0..97", i, v)
		}
		if seen[v] {
			t.Errorf("encoder dibit %d appears twice in the schedule", v)
		}
		seen[v] = true
	}
}

// TestABurstThatIsNotTrellisCodedIsRefused is the honesty requirement.
//
// A burst whose dibits are not constellation points, or whose points name no
// input from the state reached, is unreadable — and saying so is what lets the
// caller warn instead of sending silence. There is no error correction here on
// purpose: a search that guesses at a member's message is worse than admitting
// the burst could not be read.
func TestABurstThatIsNotTrellisCodedIsRefused(t *testing.T) {
	burst := make([]byte, dmrfec.BurstBytes)
	for i := range burst {
		burst[i] = byte(i * 29)
	}
	if _, ok := dmrfec.DecodeTrellis(burst); ok {
		t.Error("arbitrary bytes decoded as a Rate 3/4 block")
	}
	if _, ok := dmrfec.DecodeTrellis(burst[:10]); ok {
		t.Error("a short burst decoded as a Rate 3/4 block")
	}
	if _, ok := dmrfec.EncodeTrellis(make([]byte, 12)); ok {
		t.Error("a 12-octet block encoded as Rate 3/4, which carries 18")
	}
}
