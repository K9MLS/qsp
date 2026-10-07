package server

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/peering"
)

// TestAnAdministratorIsHandedAPasswordAPersonCanType asserts what the two
// handlers reach for, as TestTheIssuedPasswordIsTheOneAPersonCanType does for
// a hotspot's.
//
// **This is that mistake a second time.** Adding an administrator and
// resetting a password both called NewPassphrase, and the console showed 43
// characters of base64 for somebody to type at a login form. A test of the
// generator alone would pass while the handlers called the other one.
//
// Break it: call peering.NewPassphrase in either handler.
func TestAnAdministratorIsHandedAPasswordAPersonCanType(t *testing.T) {
	src := readSource(t, "users.go")

	if n := strings.Count(src, "peering.NewAccountPassword()"); n != 2 {
		t.Errorf("users.go calls peering.NewAccountPassword %d times, want twice: "+
			"once to add an administrator and once to reset a password", n)
	}
	if strings.Contains(src, "peering.NewPassphrase()") {
		t.Error("users.go calls peering.NewPassphrase, which is 43 characters of " +
			"base64 and is meant for a link between servers")
	}

	// And what they hand over is short, and is a password the console accepts:
	// a generator shortened below the policy would make every reset fail.
	for range 100 {
		p, err := peering.NewAccountPassword()
		if err != nil {
			t.Fatalf("%v", err)
		}
		if len(p) > 20 {
			t.Fatalf("%q is %d characters; it is typed at a login form", p, len(p))
		}
		if err := auth.ValidatePassword(p); err != nil {
			t.Fatalf("%q is refused by the console's own password rule: %v", p, err)
		}
	}
}
