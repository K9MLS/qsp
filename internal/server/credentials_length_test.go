package server

import (
	"os"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/peering"
)

// TestTheIssuedPasswordIsTheOneAPersonCanType asserts what the handler reaches
// for, not what the generator is capable of.
//
// **A test of NewMemberPassword alone passes while the handler calls
// NewPassphrase**, which is exactly how the 43-character password reached a
// screen aimed at a club member: the same function served a link between two
// servers and a person with a phone, and nothing said which was which.
//
// Reading the source is a blunt instrument and it is the right one here. The
// alternative is a full session, a peer directory and an HTTP round trip to
// observe one call, and that test would pass if the handler were deleted.
func TestTheIssuedPasswordIsTheOneAPersonCanType(t *testing.T) {
	src := readSource(t, "credentials.go")

	if !strings.Contains(src, "peering.NewMemberPassword()") {
		t.Error("the credential handler does not call peering.NewMemberPassword; " +
			"a member should not be handed a link passphrase to type")
	}
	if strings.Contains(src, "peering.NewPassphrase()") {
		t.Error("the credential handler calls peering.NewPassphrase, which is " +
			"43 characters of base64 and is meant for a link between servers")
	}

	// And the thing the handler produces really is short, so the assertion
	// above is about something that matters rather than about a name.
	p, err := peering.NewMemberPassword()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(p) > 16 {
		t.Errorf("an issued password is %d characters; it is typed into a "+
			"hotspot by hand, often on a phone", len(p))
	}
}

// readSource returns a file from this package, for the rare assertion that is
// about which function is called rather than about what it returns.
func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
