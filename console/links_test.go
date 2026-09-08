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

// TestThePageReadsWhatTheAPINowReports is the "declared and read by nothing"
// check, pointed at the console.
//
// `configured`, `pending_restart` and `needs_restart` are new fields on three
// responses. A field the API sends and the page ignores compiles, vets, passes
// staticcheck and passes every Go test, and this project has paid for that
// nine times.
func TestThePageReadsWhatTheAPINowReports(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	for _, field := range []struct{ name, why string }{
		{"configured", "a link removed from the configuration would go on showing as healthy"},
		{"pending_restart", "nothing would say the link and the running server disagree"},
		{"needs_restart", "an operator would be left waiting on a link that has no socket yet"},
	} {
		if !strings.Contains(js, field.name) {
			t.Errorf("the page never reads %q: %s", field.name, field.why)
		}
	}
}

// TestBothWritePathsMentionTheRestart.
//
// Accepting a peering opens no socket and removing one closes none, and the
// operator is looking at the result of the write, not at the list. The
// sentence comes from one function so the two paths cannot drift apart.
func TestBothWritePathsMentionTheRestart(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "function restartNote") {
		t.Fatal("no shared restart notice; two paths would each carry their own wording")
	}
	// Called from the list, from both halves of accept, and from remove.
	if n := strings.Count(js, "restartNote("); n < 4 {
		t.Errorf("restartNote is called %d times; the accept and remove results "+
			"and the declaration are four", n)
	}
}

// TestEveryCopyableBlockIsOneObject.
//
// A label, a small outlined button floating to its right, and a separate box
// underneath that happened to be what the button acted on: three things that
// read as three things. The first operator to use this page drag-selected a
// scrolling one-line box of base64 and hoped they caught both ends.
//
// The control belongs inside the block's outline, which is what says *this
// copies this* without a sentence explaining it.
func TestEveryCopyableBlockIsOneObject(t *testing.T) {
	html := readFile(t, "static/links.html")

	// Nothing generated is left in the old shape.
	if strings.Contains(html, `class="copyable"`) {
		t.Error("a copy control still sits outside the block it copies")
	}
	// Every copy button names a block, and every named block exists.
	ids := regexp.MustCompile(`data-copy="([^"]+)"`).FindAllStringSubmatch(html, -1)
	if len(ids) < 3 {
		t.Fatalf("found %d copy controls; the invitation, the passphrase and the "+
			"reciprocal are three", len(ids))
	}
	for _, m := range ids {
		if !strings.Contains(html, `id="`+m[1]+`"`) {
			t.Errorf("a copy button names %q and no such block exists", m[1])
		}
	}
}

// TestTheCopyControlSaysWhatHappened.
//
// Colour alone does not carry a state: a tick that turns green is nothing to
// an operator who cannot tell the two greens apart. The word changes too, and
// the space for the longer word is reserved so the button does not move out
// from under the pointer that pressed it.
func TestTheCopyControlSaysWhatHappened(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))
	css := stripComments(readFile(t, "static/console.css"))

	if !strings.Contains(js, "copyblock__word") {
		t.Error("the copy control changes no text, so success is carried by colour alone")
	}
	if !strings.Contains(js, `"data-copied"`) {
		t.Error("nothing marks the copied state for the stylesheet")
	}
	// **Scoped to the rule, not the file.** Searching the whole stylesheet for
	// "min-width" finds a dozen unrelated rules and passes whether or not this
	// one has it — which it did, against a stylesheet where the reservation had
	// been deleted.
	rule := regexp.MustCompile(`(?s)\.copyblock__word\s*\{(.*?)\}`).FindStringSubmatch(css)
	if rule == nil {
		t.Fatal(".copyblock__word has no rule at all")
	}
	if !strings.Contains(rule[1], "min-width") {
		t.Error("the word has no reserved width; the button resizes when it changes")
	}
}

// TestNoControlSitsFlushAgainstTheFieldAboveIt.
//
// The accept panel put a label, a textarea, a label, an input and a button
// next to each other as plain siblings, outside .picker — the only thing on
// this page that gave anything a gap. They rendered touching.
func TestNoControlSitsFlushAgainstTheFieldAboveIt(t *testing.T) {
	html := readFile(t, "static/links.html")
	css := stripComments(readFile(t, "static/console.css"))

	if !strings.Contains(html, `class="fieldstack"`) {
		t.Fatal("the loose fields are still bare siblings with nothing spacing them")
	}
	if !strings.Contains(css, ".fieldstack") {
		t.Error(".fieldstack is used and not styled")
	}
	// The submit needs more air than the gap between a label and its control,
	// or it reads as part of the field above it.
	if !strings.Contains(css, ".fieldstack__action") {
		t.Error("the action in a field stack has no separation from the last field")
	}
	if !strings.Contains(html, "fieldstack__action") {
		t.Error(".fieldstack__action is styled and used by nothing")
	}
}

// TestTheOfferFormAsksForALinkBeforeItsFields is the shape of the defect this
// whole page had.
//
// The form asked for a talkgroup, a timeslot, a listen address and a network ID
// on every peering, because it only knew how to write an OpenBridge one. A QSP
// link has none of those: the slot crosses unchanged so there is no endpoint to
// match, this side dials so there is nothing to bind, and everything crosses so
// there is no list. Three faults on 2026-09-08 came from that single mechanism,
// and the accept form asked a question the protocol had already answered.
//
// So the first question is what is at the other end, and the fields follow it.
func TestTheOfferFormAsksForALinkBeforeItsFields(t *testing.T) {
	html := readFile(t, "static/links.html")
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(html, `id="offer-kind"`) {
		t.Fatal("the offer form does not ask what is at the other end")
	}
	if !strings.Contains(html, `id="offer-repeater-id"`) {
		t.Error("the offer form has no box for the DMR ID a QSP link allocates, " +
			"which is what the far end presents when it registers")
	}
	if !strings.Contains(js, "/api/links/offer-link") {
		t.Error("the page never offers a QSP link; it can only write OpenBridge peerings")
	}

	// Every OpenBridge-only field has to be marked as one, or it is shown on a
	// link that has no use for it.
	for _, id := range []string{"offer-tg", "offer-slot", "offer-netid"} {
		field := fieldBlock(t, html, id)
		if !strings.Contains(field, `data-kind="openbridge"`) {
			t.Errorf("%s is shown for every kind of link; it is meaningless on a QSP link", id)
		}
	}
	if field := fieldBlock(t, html, "offer-repeater-id"); !strings.Contains(field, `data-kind="qsp"`) {
		t.Error("the DMR ID box is shown for an OpenBridge peering, which does not have one")
	}
}

// The accept form reads the kind off the token rather than asking. An operator
// holding a token cannot answer that question without reading base64.
func TestTheAcceptFormFollowsTheTokenItWasGiven(t *testing.T) {
	html := readFile(t, "static/links.html")
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "QSP-PEER-2.") {
		t.Fatal("the accept form cannot tell a QSP link invitation from an OpenBridge one")
	}
	for _, id := range []string{"accept-tg", "accept-listen", "accept-address", "accept-netid"} {
		field := fieldBlock(t, html, id)
		if !strings.Contains(field, `data-accept-kind="openbridge"`) {
			t.Errorf("%s is shown for a QSP link, which has no such setting", id)
		}
	}
}

// fieldBlock returns the markup of the one labelled field carrying this id, so
// an assertion is about that field rather than about the whole document.
//
// **Scoped deliberately.** A test searching the entire file for a string finds
// it in some other field and passes, which is how three assertions in this
// project passed against the code they existed to reject.
func fieldBlock(t *testing.T, html, id string) string {
	t.Helper()

	at := strings.Index(html, `id="`+id+`"`)
	if at < 0 {
		t.Fatalf("the page has no field with id %q", id)
	}
	start := strings.LastIndex(html[:at], "<label")
	if start < 0 {
		t.Fatalf("%s is not inside a label", id)
	}
	end := strings.Index(html[start:], "</label>")
	if end < 0 {
		t.Fatalf("the label around %s is never closed", id)
	}
	return html[start : start+end]
}
