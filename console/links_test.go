package console_test

import (
	"path/filepath"
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

// TestAnInboundLinkCanBeRefused is the rule this project already has, applied
// to the case it was taken off.
//
// **Anything a page creates, it must be able to remove.** 0284 removed the
// button from inbound links because there was nothing on this side to delete —
// true until 0288, when the offering side began allocating a DMR ID in the
// registration list and a password against it. Both are written by this
// console.
//
// The sharper version is ADR-0052 rule 1: a server decides what it accepts. A
// server that cannot refuse a neighbour from its own console is not sovereign,
// and the only recourse was asking the other operator or editing JSON.
func TestAnInboundLinkCanBeRefused(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "/api/links/inbound/") {
		t.Fatal("the page cannot refuse a link that dialled in, so a rogue network " +
			"can only be stopped by editing configuration on the server")
	}
	// **Not called Remove.** There is no link here to delete; the far end's
	// configuration is untouched and it will keep dialling. A button promising
	// removal would be describing something that does not happen.
	if !strings.Contains(js, "Stop accepting") {
		t.Error("the control on an inbound link does not say what it actually does")
	}
	// Two clicks, as removing an outbound link is, because both take traffic
	// off a network.
	if !strings.Contains(js, "Stop accepting \" + name + \"?") {
		t.Error("refusing a link is one click; it takes a network off the air")
	}
	// An operator who believes a rogue network is locked out when the shared
	// password still admits it has been told something worse than nothing.
	if !strings.Contains(js, "shared peer password still admits it") {
		t.Error("the page does not say when a refused link still has a way in")
	}
}

// The form has now suggested a public hostname to an operator whose far end was
// on the same LAN twice: once for the first link between two servers, and once
// for the first link written through this page. A note is cheaper than
// remembering.
func TestTheLinkAddressWarnsAboutTheHairpin(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "leaves the LAN and does not come back") {
		t.Error("the address box does not warn that a public name will not hairpin, " +
			"which has produced a silently dead link twice")
	}
}

// **Not measured is not zero, and inbound was a proxy for not measured.** The
// page decided between a number and a dash by asking which direction the link
// was dialled, which was true until the peer table began counting per peer.
// Both ends of one link now report the same kind of thing, which is what makes
// them comparable at all.
func TestTheCountersFollowWhetherAnythingCountsThem(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	for _, field := range []string{"l.sent", "l.received", "l.rejected"} {
		at := strings.Index(js, field)
		if at < 0 {
			t.Fatalf("the page does not show %s at all", field)
		}
		line := lineAround(js, at)
		if strings.Contains(line, "l.inbound") {
			t.Errorf("%s is chosen by the link's direction rather than by whether it is measured: %s",
				field, strings.TrimSpace(line))
		}
		if !strings.Contains(line, "l.measured") {
			t.Errorf("%s does not ask whether anything counted it: %s", field, strings.TrimSpace(line))
		}
	}
}

// **ADR-0052 rule 2 as amended.** The heading was the local label — whatever
// the dialling administrator called their own configuration block — so the
// listening server displayed a link to somebody else under its own name.
func TestALinkIsHeadedWithTheFarEndsName(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	at := strings.Index(js, "link__name")
	if at < 0 {
		t.Fatal("a link has no heading")
	}
	line := lineAround(js, at)
	if !strings.Contains(line, "l.network") {
		t.Errorf("a link is headed with the local label rather than the far end's "+
			"announced name: %s", strings.TrimSpace(line))
	}
}

// **One wrong character meant removing the link and agreeing it again.** Every
// operation has to be completable from the console.
func TestALinksAddressCanBeChangedFromThePage(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "/address") {
		t.Fatal("a link's far-end address cannot be changed from the page, so a wrong " +
			"port means removing the link and agreeing a fresh peering")
	}
	// A page that redraws every five seconds cannot hold a text box: a poll
	// landing mid-keystroke replaces what was typed with what the server has.
	if !strings.Contains(js, "editing") {
		t.Error("the poll is not suspended while the address is being edited")
	}
}

// sectionBlock returns the markup of one <section> by its id, so an assertion
// is about that block rather than about the page.
func sectionBlock(t *testing.T, html, id string) string {
	t.Helper()

	at := strings.Index(html, `id="`+id+`"`)
	if at < 0 {
		t.Fatalf("the page has no block with id %q", id)
	}
	start := strings.LastIndex(html[:at], "<section")
	if start < 0 {
		t.Fatalf("%s is not inside a section", id)
	}
	end := strings.Index(html[start:], "</section>")
	if end < 0 {
		t.Fatalf("the section around %s is never closed", id)
	}
	return html[start : start+end]
}

// inputsIn counts the things an operator can type or choose in.
func inputsIn(block string) int {
	return strings.Count(block, "<input") +
		strings.Count(block, "<select") +
		strings.Count(block, "<textarea")
}

// lineAround returns the source line containing an offset, so an assertion is
// about the expression that decides something rather than about the file.
func lineAround(s string, at int) string {
	start := strings.LastIndex(s[:at], "\n") + 1
	end := strings.Index(s[at:], "\n")
	if end < 0 {
		return s[start:]
	}
	return s[start : at+end]
}

// **The page never said which end ends up dialling**, and an operator read
// "offer a peering" as "set up the link from here" — which is the natural
// reading and the opposite of what it does. Offering a QSP link means this
// server listens; the link is written on the other operator's server when they
// accept. It cost a restart and a round trip on 2026-09-08.
func TestTheOfferFormSaysWhichEndDials(t *testing.T) {
	html := readFile(t, "static/links.html")

	field := fieldBlock(t, html, "offer-kind")
	if !strings.Contains(field, "listens and the other one dials") {
		t.Error("the offer form does not say that offering a QSP link means this server listens")
	}
	if !strings.Contains(field, "Accept a peering") {
		t.Error("the offer form does not say how to make this server dial instead")
	}
}

// **One value was used for two answers.** The offer form prefilled its address
// box from the OpenBridge default, so a QSP link was proposed port 62045
// whatever the operator chose — and the fix that added a QSP default put it on
// the fallback used when the box arrives empty, which this page never sends.
// The corrected function was unreachable and the wrong port shipped anyway.
func TestTheAddressSuggestionFollowsTheKindOfLink(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "link_address") {
		t.Fatal("the page has only one address suggestion, so a QSP link is offered " +
			"the OpenBridge port")
	}
	body := functionBody(t, js, "fillOfferAddress")
	if !strings.Contains(body, "offerKind()") {
		t.Error("the address suggestion does not depend on which kind of link is being offered")
	}
	if !strings.Contains(body, "link_address") {
		t.Error("the address suggestion never uses the QSP link default")
	}
}

// functionBody returns the source of one named function, so an assertion is
// about that function rather than about the file — and is not defeated by an
// expression spanning two lines, which is what a line-scoped version of this
// test was.
func functionBody(t *testing.T, js, name string) string {
	t.Helper()

	at := strings.Index(js, "function "+name+"(")
	if at < 0 {
		t.Fatalf("the page has no function called %s", name)
	}
	rest := js[at:]
	depth, start := 0, strings.Index(rest, "{")
	if start < 0 {
		t.Fatalf("%s has no body", name)
	}
	for i := start; i < len(rest); i++ {
		switch rest[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return rest[start : i+1]
			}
		}
	}
	t.Fatalf("the body of %s is never closed", name)
	return ""
}

// **The page said "restart QSP" in four places and could not do it.** An
// upstream is built once at startup, so a link written through the accept form
// carries nothing until the process comes back — and the answer was to find a
// terminal and know whether this machine is systemd or Docker Compose. The page
// knows neither, and an operator on a phone has neither.
func TestTheLinksPageCanRestartQSP(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "/api/restart") {
		t.Fatal("the page tells an operator to restart QSP and cannot do it")
	}

	body := functionBody(t, js, "showRestart")
	// Two clicks, like Remove and Stop accepting: it drops every hotspot and
	// link on the server.
	if !strings.Contains(body, "armed(") {
		t.Error("restarting is one click, and it takes every hotspot and link down")
	}
	// **Only where the page has just said a restart is needed.** A restart
	// button on a healthy page is an invitation to press it.
	if !strings.Contains(js, "needsRestart") {
		t.Error("the restart button is not tied to the page having asked for one")
	}
	// The flag is recomputed on every render, or a button outlives its reason.
	if !strings.Contains(functionBody(t, js, "render"), "needsRestart = false") {
		t.Error("the restart button survives the restart it asked for")
	}
}

// **A name is easier to suggest than to repair.** An operator typed "QSP Test
// Server" into the accept form — a reasonable thing to type, and also the name
// of their own server, so a link to somebody else's was named after theirs. The
// console cannot rename a link, so the only way back was hand-editing JSON.
//
// It also broke audio: the routing core addressed the lowercased name and the
// upstream registry held the configured one, so frames going into the link were
// refused while frames coming out of it were fine.
func TestTheAcceptFormSuggestsASafeLinkName(t *testing.T) {
	js := stripComments(readFile(t, "static/links.js"))

	if !strings.Contains(js, "suggestAcceptName") {
		t.Fatal("the accept form does not suggest a name, so an operator invents one")
	}
	body := functionBody(t, js, "slug")
	if !strings.Contains(body, "toLowerCase") {
		t.Error("the suggested name is not lower case")
	}
	if !strings.Contains(body, "a-z0-9") {
		t.Error("the suggested name may contain characters that become a file name")
	}
	// Never over what the operator typed: a suggestion that overwrites is not a
	// suggestion.
	if !strings.Contains(functionBody(t, js, "suggestAcceptName"), "box.value.trim() !== \"\"") {
		t.Error("the suggestion overwrites a name the operator typed")
	}
}

// TestTheAdministrationPageAnswersQuestions holds the page to ADR-0055.
//
// **The rule that keeps it from becoming a settings dump**: a page may edit a
// setting when it is the page that reports the problem, and — binding harder —
// if it is not reporting a problem with a setting, it does not get to edit it.
// Today that is the callsign lookup and nothing else, so a second input
// appearing here is the thing this test exists to notice.
func TestTheAdministrationPageAnswersQuestions(t *testing.T) {
	html := readFile(t, "static/admin.html")

	// The blocks the record names. Backup and restore is not built yet.
	for _, id := range []string{"block-server", "block-agreement", "block-services", "block-callsigns"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf("the page has no %s block", id)
		}
	}

	// **Settings live in one block, and only one.** ADR-0055 allows this page
	// to edit a setting when it is the page that reports the problem — today
	// the callsign lookup and nothing else.
	//
	// Counted per block rather than per page, because the first version of this
	// counted every input and fired on the restore box: a field an operator
	// types into to perform an act is not a setting the server stores, and a
	// test that cannot tell them apart catches the wrong drift and gets
	// loosened rather than obeyed.
	settings := inputsIn(sectionBlock(t, html, "block-callsigns"))
	if settings != 2 {
		t.Errorf("the callsign block has %d inputs, want the toggle and the contact "+
			"address; a third needs ADR-0055 amended first", settings)
	}
	for _, id := range []string{"block-server", "block-agreement", "block-services"} {
		if n := inputsIn(sectionBlock(t, html, id)); n != 0 {
			t.Errorf("%s has %d inputs; it reports and does not edit", id, n)
		}
	}

	// The identifier is displayed and must never be editable.
	if strings.Contains(html, `id="server-identifier"`) && strings.Contains(html, "<input") {
		t.Error("the identifier appears to be editable; there is deliberately no way to change one")
	}

	js := stripComments(readFile(t, "static/admin.js"))

	// **The restart control is always here**, unlike on the Links page, where
	// it appears only beside a message asking for one. This page is where the
	// acts belonging to the server live and where an operator comes looking for
	// a restart whether or not anything is waiting; hiding it sends them to
	// find a terminal, which is the failure the page exists to end.
	//
	// Asserted per branch rather than once, because a version that offered it
	// on two of the three paths would pass a single check and still send an
	// operator to a terminal from the third.
	if !strings.Contains(functionBody(t, js, "offerRestart"), "data-restart") {
		t.Error("the page cannot restart QSP")
	}
	branches := strings.Count(functionBody(t, js, "renderAgreement"), "offerRestart()")
	if branches < 3 {
		t.Errorf("renderAgreement offers a restart on %d of its 3 paths; an operator "+
			"reaching the missing one is sent to a terminal", branches)
	}

	// A duration alone reads as a fault on a server restarted a minute ago.
	if !strings.Contains(functionBody(t, js, "uptime"), "since") {
		t.Error("uptime is shown without the start time, so a small number reads as a fault")
	}
}

// **A backup carries no secret, so the page must not imply it does** — and it
// must say what a restore cannot bring back, because a restored server with
// every link refused looks exactly like a network fault (ADR-0054).
func TestTheBackupBlockSaysWhatItCannotCarry(t *testing.T) {
	html := readFile(t, "static/admin.html")
	block := sectionBlock(t, html, "block-backup")

	if !strings.Contains(block, "no passwords") {
		t.Error("the page does not say a backup carries no passwords, which is the " +
			"property that makes it safe to send to somebody")
	}
	if !strings.Contains(block, "/api/admin/backup") {
		t.Error("the page cannot download a backup")
	}

	js := stripComments(readFile(t, "static/admin.js"))

	// Two answers: the first says what it would do, the second does it.
	body := functionBody(t, js, "restore")
	if !strings.Contains(body, "428") {
		t.Error("an import is not shown to the operator before it happens")
	}
	if !strings.Contains(body, "missing_credentials") {
		t.Error("the confirmation does not list the credentials a restore cannot bring back")
	}
	// **Replacement or clone is the operator's answer, not QSP's assumption.**
	// Two servers claiming one identity is a fault neither reports.
	if !strings.Contains(body, "new_identity") {
		t.Error("an import does not ask whether this server replaces the one that made " +
			"the backup, so two servers could claim one identity")
	}
}

// **The version, on every page.** It lived in a startup log line and on one
// page, and an operator read it out of `journalctl` after every deploy for two
// days — the question "what is actually running here", asked constantly and
// answered nowhere convenient.
func TestTheVersionIsOnEveryPage(t *testing.T) {
	pages, err := filepath.Glob("static/*.html")
	if err != nil {
		t.Fatalf("listing pages: %v", err)
	}
	if len(pages) < 5 {
		t.Fatalf("only %d pages found; this test would pass by finding nothing", len(pages))
	}

	for _, page := range pages {
		html := readFile(t, page)
		// The sign-in page has no chrome to hang it on.
		if !strings.Contains(html, "brand__tagline") {
			continue
		}
		if !strings.Contains(html, `id="nav-version"`) {
			t.Errorf("%s does not show which version is running", page)
		}
		// **At the foot of the navigation**, where the "Not yet built" section
		// used to be — not under the brand, which is where a first attempt put
		// it and where the operator did not look.
		nav := html[strings.Index(html, "<nav"):]
		if !strings.Contains(nav[:strings.Index(nav, "</nav>")], `id="nav-version"`) {
			t.Errorf("%s shows the version outside the navigation", page)
		}
	}

	js := stripComments(readFile(t, "static/nav.js"))
	// **One fetch and one source.** Riding on the session request means there
	// is nothing to drift and no second round trip on every page load.
	if strings.Contains(js, `fetch("/healthz"`) {
		t.Error("the version is fetched separately; it rides on the session request")
	}
	// Labelled, because a bare number at the foot of a sidebar is a number.
	body := functionBody(t, js, "showVersion")
	if !strings.Contains(body, `"Version "`) {
		t.Error("the version is shown unlabelled")
	}
}
