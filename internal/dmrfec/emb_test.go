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

// TestAnUnobservedColourCodeIsRefused is the honest half of this package.
//
// The parity over the seven information bits is provably linear, but every
// captured burst carries colour code 11, so only the LCSS bits ever moved and
// only their contribution is known. Computing an EMB for another colour code
// would produce bursts a radio silently drops — audio going nowhere with
// nothing in a log, which is worse than a refusal that names the fix.
func TestAnUnobservedColourCodeIsRefused(t *testing.T) {
	for _, cc := range []uint8{0, 1, 2, 12, 15} {
		if _, err := dmrfec.EMBFor(cc, dmrfec.LCSSFirst); err == nil {
			t.Errorf("colour code %d was served from a table that has never seen it", cc)
		}
	}
	if _, err := dmrfec.EMBFor(11, dmrfec.LCSSFirst); err != nil {
		t.Errorf("colour code 11 is in every capture and was refused: %v", err)
	}
}
