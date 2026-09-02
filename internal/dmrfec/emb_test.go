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

// TestTheSuperframeLCSSOrderMatchesTheCaptures reads the Homebrew traffic as
// sequences rather than as a set, which is the only way the order shows up.
//
// Every LCSS appears twice in a row on the wire because the capture was taken
// at a master and holds each burst twice: once arriving and once relayed.
// Collapsing the repeats gives the order LCSSForPosition encodes.
func TestTheSuperframeLCSSOrderMatchesTheCaptures(t *testing.T) {
	var run []uint8
	var complete, agreed int

	flush := func() {
		if len(run) < 5 {
			run = nil
			return
		}
		complete++
		ok := true
		for i, got := range run[:5] {
			want, valid := dmrfec.LCSSForPosition(i + 1)
			if !valid {
				t.Fatalf("position %d has no LCSS", i+1)
			}
			if got != want {
				ok = false
			}
		}
		if ok {
			agreed++
		} else {
			// Not a failure on its own. A superframe with a burst missing
			// shifts every position after it, and a capture of real traffic
			// over a real radio link has losses in it.
			t.Logf("superframe %d does not match: %v", complete, run[:5])
		}
		run = nil
	}

	// Each burst appears twice, so take every second one. Skipping *equal*
	// neighbours instead would collapse the two genuine continuation positions
	// into one, which is a real trap: the wrong deduplication produces a
	// plausible five-element sequence that is silently missing a burst.
	var seen int
	var inFrame bool
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			flush()
			inFrame = false
			continue
		}
		middle, _ := dmrfec.Middle(b.Burst)
		if middle == dmrfec.VoiceSyncBS {
			flush()
			inFrame = true
			seen = 0
			continue
		}
		if !inFrame {
			continue
		}
		if seen%2 == 1 {
			seen++
			continue
		}
		seen++
		emb, _ := dmrfec.SplitMiddle(middle)
		run = append(run, dmrfec.LCSSOf(emb))
	}
	flush()

	if complete == 0 {
		t.Fatal("no complete superframes found")
	}
	pct := agreed * 100 / complete
	t.Logf("%d superframes, %d matching the encoded order (%d%%)", complete, agreed, pct)
	if pct < 95 {
		t.Errorf("only %d%% of superframes follow the order LCSSForPosition encodes; "+
			"a wrong order would score near zero and a right one near a hundred, so this "+
			"is a wrong order rather than a lossy capture", pct)
	}
}

// TestMiddleForPositionRebuildsTheCapturedMiddles is the end-to-end check: a
// position and a colour code must produce the 48 bits actually seen on air.
func TestMiddleForPositionRebuildsTheCapturedMiddles(t *testing.T) {
	sync, err := dmrfec.MiddleForPosition(0, 11, 0)
	if err != nil {
		t.Fatalf("position 0: %v", err)
	}
	if sync != dmrfec.VoiceSyncBS {
		t.Errorf("position 0 gave %012x, want the voice sync pattern", sync)
	}

	var matched int
	for _, b := range readBursts(t) {
		if !b.isVoice() {
			continue
		}
		middle, _ := dmrfec.Middle(b.Burst)
		if middle == dmrfec.VoiceSyncBS {
			continue
		}
		emb, fragment := dmrfec.SplitMiddle(middle)
		cc, lcss := dmrfec.ColourCodeOf(emb), dmrfec.LCSSOf(emb)
		for pos := 1; pos < dmrfec.SuperframeBursts; pos++ {
			if want, ok := dmrfec.LCSSForPosition(pos); !ok || want != lcss {
				continue
			}
			got, err := dmrfec.MiddleForPosition(pos, cc, fragment)
			if err != nil {
				t.Fatalf("position %d: %v", pos, err)
			}
			if got != middle {
				t.Fatalf("position %d rebuilt %012x, the wire had %012x", pos, got, middle)
			}
			matched++
			break
		}
	}
	if matched == 0 {
		t.Fatal("no captured middle was rebuilt")
	}
	t.Logf("%d captured middles rebuilt from a position and a colour code", matched)
}

// TestAPositionOutsideASuperframeIsRefused keeps the bounds honest.
func TestAPositionOutsideASuperframeIsRefused(t *testing.T) {
	for _, pos := range []int{-1, 6, 7, 100} {
		if _, err := dmrfec.MiddleForPosition(pos, 11, 0); err == nil {
			t.Errorf("burst position %d was accepted; a superframe holds %d",
				pos, dmrfec.SuperframeBursts)
		}
	}
}
