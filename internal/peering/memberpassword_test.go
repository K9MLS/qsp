package peering_test

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/peering"
)

// TestAMemberPasswordCanBeTypedByAPerson is the reason this generator exists
// apart from NewPassphrase.
//
// The screen that shows it is aimed at a club member setting up a hotspot,
// probably on a phone. It used to hand them 43 characters of mixed-case base64.
func TestAMemberPasswordCanBeTypedByAPerson(t *testing.T) {
	const forbidden = "0O1lI" // misread from a screen or over the air

	for range 200 {
		p, err := peering.NewMemberPassword()
		if err != nil {
			t.Fatalf("%v", err)
		}
		bare := strings.ReplaceAll(p, "-", "")
		if len(bare) != peering.MemberPasswordLength {
			t.Fatalf("%q has %d characters, want %d", p, len(bare),
				peering.MemberPasswordLength)
		}
		if strings.ContainsAny(bare, forbidden) {
			t.Fatalf("%q contains a character that is misread: one of %q", p, forbidden)
		}
		if strings.ContainsAny(bare, strings.ToUpper(bare)) && bare != strings.ToLower(bare) {
			t.Fatalf("%q is mixed case; it has to be dictatable", p)
		}
	}
}

// TestMemberPasswordsDoNotRepeat is a smoke test for the obvious catastrophe.
//
// Not a statistical test — 47 bits will not collide in a thousand draws for
// reasons no test needs to demonstrate. This catches a generator that stopped
// reading randomness at all, which is the failure that actually happens.
func TestMemberPasswordsDoNotRepeat(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		p, err := peering.NewMemberPassword()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if seen[p] {
			t.Fatalf("%q was generated twice in a thousand draws", p)
		}
		seen[p] = true
	}
}

// TestALinkPassphraseIsStillLong keeps the two apart.
//
// A passphrase between two servers is pasted into a configuration file and
// should stay long. Shortening both because one was awkward for a person would
// weaken the one nobody types.
func TestALinkPassphraseIsStillLong(t *testing.T) {
	p, err := peering.NewPassphrase()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(p) < 40 {
		t.Errorf("a link passphrase is %d characters; it is pasted, not typed, "+
			"and should not have been shortened with the member password", len(p))
	}
}

// TestAnAccountPasswordCanBeReadToSomebody is NewMemberPassword's test again,
// for the second place a person was handed a link's passphrase: the console's
// own logins, when an administrator is added or a password reset.
//
// Break it: return NewPassphrase's 43 characters, or drop a group.
func TestAnAccountPasswordCanBeReadToSomebody(t *testing.T) {
	const forbidden = "0O1lI"
	seen := map[string]bool{}
	for range 1000 {
		p, err := peering.NewAccountPassword()
		if err != nil {
			t.Fatalf("%v", err)
		}
		groups := strings.Split(p, "-")
		if len(groups) != 4 {
			t.Fatalf("%q is %d groups, want four", p, len(groups))
		}
		for _, g := range groups {
			if len(g) != 4 {
				t.Fatalf("%q has a group of %d characters, want four", p, len(g))
			}
		}
		bare := strings.ReplaceAll(p, "-", "")
		if len(bare) != peering.AccountPasswordLength {
			t.Fatalf("%q has %d characters, want %d", p, len(bare), peering.AccountPasswordLength)
		}
		if strings.ContainsAny(bare, forbidden) {
			t.Fatalf("%q contains a character that is misread: one of %q", p, forbidden)
		}
		if bare != strings.ToLower(bare) {
			t.Fatalf("%q is mixed case; it has to be dictatable", p)
		}
		if seen[p] {
			t.Fatalf("%q was generated twice in a thousand draws", p)
		}
		seen[p] = true
	}
}

// Every character of the alphabet turns up, and none far more than another:
// the picker shared by both generators redraws rather than folds.
//
// Break it: take every byte modulo the alphabet's length without redrawing,
// and the first letters come up about a tenth more often than the last.
func TestTheAlphabetIsDrawnEvenly(t *testing.T) {
	counts := map[rune]int{}
	total := 0
	for range 40000 {
		p, err := peering.NewAccountPassword()
		if err != nil {
			t.Fatalf("%v", err)
		}
		for _, c := range strings.ReplaceAll(p, "-", "") {
			counts[c]++
			total++
		}
	}
	const alphabet = 27
	if len(counts) != alphabet {
		t.Fatalf("%d different characters came up, want all %d", len(counts), alphabet)
	}
	mean := float64(total) / alphabet
	for c, n := range counts {
		// 640,000 draws: each character about 23,700 times, give or take
		// 150. Folding puts the first thirteen 5% above that and the rest 5%
		// below, so 3% is nearly five deviations from chance and well inside
		// what folding does.
		if d := float64(n) - mean; d > 0.03*mean || d < -0.03*mean {
			t.Errorf("%q came up %d times against a mean of %.0f", c, n, mean)
		}
	}
}
