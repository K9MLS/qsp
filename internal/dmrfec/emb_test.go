package dmrfec_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
)

// TestTheEMBTableMatchesEveryCapturedBurst checks the table against the traffic
// it came from, so a typo in it cannot survive.
func TestTheEMBTableMatchesEveryCapturedBurst(t *testing.T) {
	seen := map[uint16]int{}
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		middle, _ := dmrfec.Middle(b.Burst)
		if middle == dmrfec.VoiceSyncBS {
			continue
		}
		emb, _ := dmrfec.SplitMiddle(middle)
		seen[emb]++

		cc, lcss := dmrfec.ColourCodeOf(emb), dmrfec.LCSSOf(emb)
		want, err := dmrfec.EMBFor(cc, lcss)
		if err != nil {
			t.Fatalf("EMB %#04x is on the wire but EMBFor rejects it: %v", emb, err)
		}
		if want != emb {
			t.Errorf("colour code %d LCSS %d: table says %#04x, the wire says %#04x", cc, lcss, want, emb)
		}
	}
	if len(seen) != 4 {
		t.Errorf("%d distinct EMB values in the captures, want the four LCSS states", len(seen))
	}
}

// TestTheMiddleSplitsAndRebuilds covers the packing either side of the
// fragment, which is the part easiest to get backwards.
func TestTheMiddleSplitsAndRebuilds(t *testing.T) {
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		middle, _ := dmrfec.Middle(b.Burst)
		if middle == dmrfec.VoiceSyncBS {
			continue
		}
		emb, frag := dmrfec.SplitMiddle(middle)
		if got := dmrfec.EmbeddedMiddle(emb, frag); got != middle {
			t.Fatalf("middle %012x split and rebuilt as %012x", middle, got)
		}
	}
}

// TestEveryColourCodeProducesAValidEMB covers the axis the captures could not.
//
// Every burst in testdata/hbp/ carries colour code 11, so the code was fitted on
// the LCSS axis and predicts the rest. What can be checked here is that the
// prediction is self-consistent for all sixteen colour codes and all four LCSS
// values, and that nothing outside those ranges is quietly accepted.
func TestEveryColourCodeProducesAValidEMB(t *testing.T) {
	seen := map[uint16]bool{}
	for cc := uint8(0); cc <= 15; cc++ {
		for lcss := uint8(0); lcss <= 3; lcss++ {
			emb, err := dmrfec.EMBFor(cc, lcss)
			if err != nil {
				t.Fatalf("colour code %d LCSS %d: %v", cc, lcss, err)
			}
			if dmrfec.ColourCodeOf(emb) != cc || dmrfec.LCSSOf(emb) != lcss {
				t.Errorf("EMB %#04x does not read back as colour code %d LCSS %d", emb, cc, lcss)
			}
			if !dmrfec.ValidEMB(emb) {
				t.Errorf("EMB %#04x fails its own parity check", emb)
			}
			if seen[emb] {
				t.Errorf("EMB %#04x is produced by two different inputs", emb)
			}
			seen[emb] = true
		}
	}
	if len(seen) != 64 {
		t.Errorf("%d distinct EMB values for 64 inputs", len(seen))
	}
}

// TestTheEMBCodeCorrectsNothingItShouldNot checks that a single flipped bit is
// detected rather than read as a different valid value.
func TestTheEMBCodeCorrectsNothingItShouldNot(t *testing.T) {
	emb, _ := dmrfec.EMBFor(11, dmrfec.LCSSFirst)
	var accepted int
	for bit := 0; bit < 16; bit++ {
		if dmrfec.ValidEMB(emb ^ 1<<uint(bit)) {
			accepted++
		}
	}
	if accepted != 0 {
		t.Errorf("%d single-bit corruptions of %#04x passed the parity check", accepted, emb)
	}
}

// TestOutOfRangeInputsAreRefused keeps the boundaries honest.
func TestOutOfRangeInputsAreRefused(t *testing.T) {
	if _, err := dmrfec.EMBFor(16, dmrfec.LCSSFirst); err == nil {
		t.Error("colour code 16 was accepted; DMR allows 0 to 15")
	}
	if _, err := dmrfec.EMBFor(11, 4); err == nil {
		t.Error("LCSS 4 was accepted; it is two bits")
	}
}
