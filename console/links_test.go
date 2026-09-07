package console_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestTheOfferFormHasABoxForEverythingTheInvitationNeeds is the test that would
// have caught it.
//
// `peering.Invitation.Validate` refuses an invitation with no callsign, and the
// offer form had **no callsign box at all**. The refusal named the missing
// field — "an invitation needs a callsign" — and the console then appended its
// own advice to check the network ID and the address, which were both already
// correct.
//
// So an operator on a fresh instance got an error naming something they could
// not enter, followed by instructions about two things that were not wrong. The
// callsign came from `dmr.identity`, which `config.Default()` leaves empty and
// which nothing in QSP has ever asked anybody to fill in.
//
// **A form that cannot supply what the API requires is broken however good the
// API is**, and nothing here connected the two.
func TestTheOfferFormHasABoxForEverythingTheInvitationNeeds(t *testing.T) {
	html := readFile(t, "static/links.html")
	js := stripComments(readFile(t, "static/links.js"))

	// Every field peering.Invitation.Validate refuses an invitation for
	// lacking, and the id the page uses for it.
	for _, field := range []struct{ name, id string }{
		{"callsign", "offer-callsign"},
		{"network ID", "offer-netid"},
		{"address", "offer-address"},
	} {
		if !strings.Contains(html, `id="`+field.id+`"`) {
			t.Errorf("the offer form has no box for the %s, which an invitation is refused without",
				field.name)
		}
		if !strings.Contains(js, field.id) {
			t.Errorf("the %s box is on the page and never read", field.name)
		}
	}
}

// TestTheOfferFormFillsItselfIn checks the second half.
//
// Every box on that form asked an operator for something the server already
// knew, and getting any of them wrong produced an error naming a different
// field. `/api/links` returns the instance's identity now and the page uses it,
// so a value already present is not typed again.
func TestTheOfferFormFillsItselfIn(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))
	if !strings.Contains(js, "fillOffer") {
		t.Fatal("nothing fills the offer form in")
	}
	if !strings.Contains(js, "body.identity") {
		t.Error("the page never reads the identity the API returns")
	}
	// It must not overwrite something the operator has typed.
	if !strings.Contains(js, "!node.value") {
		t.Error("fillOffer does not check whether the box is already filled in")
	}
}

// TestEveryOfferFieldIsLabelled keeps the page usable for somebody who cannot
// see the layout. A new input added to an existing row is the easiest place to
// forget one.
func TestEveryOfferFieldIsLabelled(t *testing.T) {
	html := readFile(t, "static/links.html")
	inputs := regexp.MustCompile(`<input[^>]*id="offer-[^"]+"[^>]*>`).FindAllString(html, -1)
	if len(inputs) < 3 {
		t.Fatalf("found %d offer inputs, want at least 3", len(inputs))
	}
	for _, tag := range inputs {
		id := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(tag)[1]
		// The console wraps each input in its own <label>, so the label text
		// precedes the input inside the same element.
		at := strings.Index(html, `id="`+id+`"`)
		before := html[max(0, at-400):at]
		if !strings.Contains(before, "picker__label") {
			t.Errorf("%s has no label above it", id)
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TestEveryTokenBlockHasACopyButton is the complaint an operator made on first
// use, and it was a fair one.
//
// These blocks hold three hundred characters of base64 in a scrolling one-line
// box. Copying one meant dragging across it and hoping both ends came with it,
// and there are three of them in one workflow.
func TestEveryTokenBlockHasACopyButton(t *testing.T) {
	html := readFile(t, "static/links.html")

	for _, id := range []string{"offer-token", "offer-pass", "accept-reciprocal"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Fatalf("the page has no %s block at all", id)
		}
		if !strings.Contains(html, `data-copy="`+id+`"`) {
			t.Errorf("%s holds a long token and has no copy button", id)
		}
	}

	js := stripComments(readFile(t, "static/links.js"))
	if !strings.Contains(js, "data-copy") {
		t.Error("the copy buttons are in the markup and nothing wires them up")
	}
	// **The fallback is not decoration.** navigator.clipboard needs a secure
	// context, and a console reached over plain HTTP on a LAN is not one, so
	// selection is the path most operators will actually take.
	if !strings.Contains(js, "isSecureContext") || !strings.Contains(js, "selectNodeContents") {
		t.Error("copying has no fallback for a console served over plain HTTP")
	}
}
