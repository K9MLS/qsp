package config_test

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// **Unique without a coordinator** is the one property no issued number has,
// and what ADR-0052 rule 2 requires of identity in a network with no
// headquarters.
func TestTwoServersGenerateDifferentIdentifiers(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		id, err := config.NewIdentifier()
		if err != nil {
			t.Fatalf("NewIdentifier: %v", err)
		}
		if err := config.ValidIdentifier(id); err != nil {
			t.Fatalf("a generated identifier is not valid: %v", err)
		}
		if seen[id] {
			t.Fatalf("two identifiers collided: %q", id)
		}
		seen[id] = true
	}
}

// The shape is checked in exactly one place, and it checks the shape and
// nothing else — no prefix, no version digit, no meaning. That is what lets a
// later generation put a key fingerprint here without touching anything else.
func TestAnIdentifierIsCheckedForShapeAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
	}{
		{"empty", ""},
		{"blank", "   "},
		{"too short", "0f1e2d3c"},
		{"too long", strings.Repeat("ab", 24)},
		{"not hexadecimal", strings.Repeat("z", 32)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := config.ValidIdentifier(tc.id); err == nil {
				t.Errorf("%q was accepted as an identifier", tc.id)
			}
		})
	}

	if err := config.ValidIdentifier(strings.Repeat("0f", 16)); err != nil {
		t.Errorf("a well-formed identifier was refused: %v", err)
	}
	// Case is not meaning: an identifier is compared, and a check that rejected
	// uppercase would be reading it.
	if err := config.ValidIdentifier(strings.Repeat("0F", 16)); err != nil {
		t.Errorf("an uppercase identifier was refused: %v", err)
	}
}

// Shown only where identity is the actual question, and never as a heading. An
// operator reading a page should be reading names.
func TestAShortIdentifierIsShortAndStillAPrefix(t *testing.T) {
	id := strings.Repeat("ab", 16)

	short := config.ShortIdentifier(id)
	if len(short) != 8 {
		t.Errorf("the short form is %d characters, want 8", len(short))
	}
	if !strings.HasPrefix(id, short) {
		t.Errorf("the short form %q is not a prefix of %q", short, id)
	}
	// Something shorter than the trim is returned whole rather than padded or
	// panicking, because a server that predates this field has no identifier
	// and a page still has to render.
	if got := config.ShortIdentifier("abc"); got != "abc" {
		t.Errorf("a short input became %q", got)
	}
	if got := config.ShortIdentifier(""); got != "" {
		t.Errorf("an absent identifier became %q", got)
	}
}
