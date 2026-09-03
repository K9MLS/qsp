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
