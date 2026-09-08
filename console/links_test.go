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

// TestALinkCanBeRemovedFromThePage is the rule the whole page broke.
//
// **Anything a page creates, it must be able to remove.** Accepting a peering
// wrote an upstream, a bridge and a passphrase file, and there was no button,
// no endpoint and no way back — an operator whose first attempt went wrong was
// left with a broken link on the page unless they edited JSON on the server,
// which is precisely what this page exists to avoid.
func TestALinkCanBeRemovedFromThePage(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "data-remove") {
		t.Fatal("a link cannot be removed from the page")
	}
	if !strings.Contains(js, `method: "DELETE"`) {
		t.Error("the remove button does not call the API")
	}
	// **Two clicks.** Removing a link takes a network down, and a single
	// button beside a status row is one slip away from doing it.
	if !strings.Contains(js, "armed") {
		t.Error("the remove button acts on one click")
	}
	// And it disarms itself: a button left asking a question is one an
	// operator meets later having forgotten what it asked.
	if !strings.Contains(js, "setTimeout") {
		t.Error("the armed state never expires")
	}
}

// TestTheDestructiveStyleReusesItsToken checks the page did not invent a second
// name for an idea the stylesheet already had.
func TestTheDestructiveStyleReusesItsToken(t *testing.T) {
	// **Comments stripped**, because the first version of this failed on the
	// comment explaining why the token was not invented. Third test today to
	// read prose instead of code.
	css := stripComments(readFile(t, "static/console.css"))
	if !strings.Contains(css, "var(--color-destructive)") {
		t.Error("the remove button does not use the existing destructive colour")
	}
	if strings.Contains(css, "--color-danger") {
		t.Error("a second token was invented for the colour --color-destructive already names")
	}
}

// TestTheAcceptFormSeparatesTheTwoOppositeAddresses is the defect that took
// production down, at the level of the page that produced it.
//
// The accept form had one box, "We listen on", and the handler read it twice:
// once as the local address to bind and once as the address the far end is told
// to send to. **Those are exact opposites**, and every value an operator could
// type was wrong in one of three ways — 0.0.0.0 binds and is refused as
// somewhere to send to, a public name is accepted as somewhere to send to and
// cannot be bound, a LAN address does both and reaches nothing from outside.
// The offer form beside it has had these as two fields all along.
func TestTheAcceptFormSeparatesTheTwoOppositeAddresses(t *testing.T) {
	html := readFile(t, "static/links.html")
	js := stripComments(readFile(t, "static/links.js"))

	for _, field := range []struct{ role, id string }{
		{"the address this server binds", "accept-listen"},
		{"the address the far end sends to", "accept-address"},
	} {
		if !strings.Contains(html, `id="`+field.id+`"`) {
			t.Errorf("the accept form has no box for %s", field.role)
		}
		if !strings.Contains(js, field.id) {
			t.Errorf("the box for %s is on the page and never read", field.role)
		}
	}

	// Both have to reach the API, under the names the handler decodes. A box
	// read into a variable that is never sent is the same defect one step
	// further along.
	for _, key := range []string{"listen:", "address:"} {
		if !strings.Contains(js, key) {
			t.Errorf("the accept request never sends %q", key)
		}
	}
}
