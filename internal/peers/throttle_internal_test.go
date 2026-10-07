package peers

import (
	"net/netip"
	"testing"
	"time"
)

func sourceN(n int) netip.AddrPort {
	return netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, byte(n >> 16), byte(n >> 8), byte(n)}), 4000)
}

// TestAFullThrottleTableStillCountsARealGuesser. The table of failing
// addresses holds 4,096. Full, it used to count nobody new, and it can be
// filled with failures that prove nothing about where they came from: a
// digest nobody asked for, from any address the sender likes. After that a
// real guesser at a new address was never counted and never locked out.
//
// To see it fail: have makeRoom return false whenever the table is full.
func TestAFullThrottleTableStillCountsARealGuesser(t *testing.T) {
	now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.UTC)
	guesser := netip.MustParseAddrPort("198.51.100.7:4000")

	tests := []struct {
		name string
		// fill is what every other entry in the table is.
		fill func(th *throttle, n int)
	}{
		{"full of forged failures", func(th *throttle, n int) {
			th.fail(sourceN(n), ReasonUnsolicited, now)
		}},
		{"full of other guessers, none locked out", func(th *throttle, n int) {
			th.fail(sourceN(n), ReasonWrongPassword, now)
		}},
		{"full of addresses locked out", func(th *throttle, n int) {
			for range 3 {
				th.fail(sourceN(n), ReasonWrongPassword, now)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			th := newThrottle(3, 5*time.Minute)
			for n := range maxTrackedSources {
				tc.fill(th, n)
			}
			if len(th.attempts) != maxTrackedSources {
				t.Fatalf("the table holds %d, want it full at %d", len(th.attempts), maxTrackedSources)
			}

			later := now.Add(time.Second)
			var locked bool
			for range 3 {
				locked, _ = th.fail(guesser, ReasonWrongPassword, later)
			}
			if !locked || !th.locked(guesser, later) {
				t.Error("three wrong passwords from a new address were not counted: the limit is off")
			}
			if len(th.attempts) > maxTrackedSources {
				t.Errorf("the table grew to %d", len(th.attempts))
			}
		})
	}
}

// TestForgedFailuresCannotPushARealOneOut. The other direction: a table that
// makes room must not make it out of the entries that matter, whether the
// rest of it is forged failures or real ones.
//
// To see it fail: in makeRoom, drop the `!reason.provesAddress()` test (the
// second row), or choose the entry to drop without looking at what it proved
// (the first).
func TestForgedFailuresCannotPushARealOneOut(t *testing.T) {
	now := time.Date(2026, 10, 7, 14, 0, 0, 0, time.UTC)
	guesser := netip.MustParseAddrPort("198.51.100.7:4000")

	tests := []struct {
		name string
		// others is how many more real, counted failures share the table.
		others int
	}{
		{"with room in the table", 0},
		{"with the table full of real failures", maxTrackedSources - 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			th := newThrottle(3, 5*time.Minute)
			// The guesser first, so it is the quietest entry there is.
			th.fail(guesser, ReasonWrongPassword, now)
			th.fail(guesser, ReasonWrongPassword, now)
			for n := range tc.others {
				th.fail(sourceN(1<<20+n), ReasonWrongPassword, now.Add(time.Second))
			}

			for n := range 3 * maxTrackedSources {
				th.fail(sourceN(n), ReasonUnsolicited, now.Add(2*time.Second+time.Duration(n)*time.Millisecond))
			}
			if len(th.attempts) > maxTrackedSources {
				t.Fatalf("the table grew to %d", len(th.attempts))
			}
			if !th.tracked(guesser.Addr()) {
				t.Fatal("two real failures were forgotten to make room for forged ones")
			}
			if locked, _ := th.fail(guesser, ReasonWrongPassword, now.Add(time.Minute)); !locked {
				t.Error("the third wrong password did not lock the address out")
			}
		})
	}
}
