package console_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestMotorolaRepeatersCanBeConfiguredFromTheConsole.
//
// **There was no IPSC surface anywhere in the console.** Not hidden when
// disabled: absent. Every other subsystem is configurable from a page, and this
// one required hand-editing qsp.json — which is the activity that took
// production down, and which an operator running a club repeater should never
// have to do.
func TestMotorolaRepeatersCanBeConfiguredFromTheConsole(t *testing.T) {
	html := readFile(t, "static/network.html")
	js := stripComments(readFile(t, "static/network.js"))

	for _, field := range []struct{ id, why string }{
		{"ipsc-enabled", "IPSC cannot be turned on or off"},
		{"ipsc-listen", "there is nowhere to set the address it binds"},
		{"ipsc-master", "there is nowhere to set the master ID"},
		{"ipsc-cc", "there is nowhere to set the colour code"},
		{"ipsc-timeout", "there is nowhere to set the peer timeout"},
		{"ipsc-peers", "there is nowhere to say which repeaters are allowed"},
		{"ipsc-slot2", "there is nowhere to correct the slot bit reading"},
	} {
		if !strings.Contains(html, `id="`+field.id+`"`) {
			t.Errorf("%s: no %q control on the page", field.why, field.id)
		}
		if !strings.Contains(js, field.id) {
			t.Errorf("%q is on the page and read by nothing", field.id)
		}
	}

	// **Every one has to be written, not merely mentioned.** Searching the file
	// for the key name finds the render function reading it and passes whether
	// or not the save writes it — which it did, against a save with a field
	// deleted. "next.ipsc.x =" is an assignment into the outgoing document.
	for _, key := range []string{
		"listen_address", "master_id", "colour_code",
		"peer_timeout_seconds", "allowed_peers", "slot_bit_is_timeslot2",
	} {
		if !strings.Contains(js, "next.ipsc."+key+" =") {
			t.Errorf("the save never writes ipsc.%s, so the form edits nothing there", key)
		}
	}
}

// TestTheMotorolaPanelIsVisibleWhetherOrNotItIsOn.
//
// A section that appears only once the subsystem is enabled cannot be the place
// you enable it. That is why there was no way in.
func TestTheMotorolaPanelIsVisibleWhetherOrNotItIsOn(t *testing.T) {
	html := readFile(t, "static/network.html")

	i := strings.Index(html, `id="ipsc-enabled"`)
	if i < 0 {
		t.Fatal("no IPSC panel on the network page")
	}
	// Find the section that contains it and check nothing hides it.
	start := strings.LastIndex(html[:i], "<section")
	if start < 0 {
		t.Fatal("the IPSC controls are not in a section")
	}
	section := html[start : start+strings.Index(html[start:], "</section>")]
	// **Scoped to the section's own opening tag.** Searching the whole section
	// for "hidden" matches aria-hidden on a decorative icon and fails whether
	// or not the panel is hidden — a test that is wrong in both directions.
	openTag := section[:strings.Index(section, ">")+1]
	if strings.Contains(openTag, "hidden") {
		t.Error("the IPSC panel is hidden by default, so there is no way to enable it")
	}
	if !strings.Contains(section, "panel__count") {
		t.Error("the panel has no state summary, so off and misconfigured look alike")
	}
}

// TestTheToggleIsARealCheckbox.
//
// A div with a click handler needs role, tabindex, aria-checked, space and
// enter all reimplemented, and still will not restore with the form or be
// found by a screen reader looking for a control. The input carries the state,
// the keyboard and the accessible name; the track is paint.
func TestTheToggleIsARealCheckbox(t *testing.T) {
	html := readFile(t, "static/network.html")
	css := stripComments(readFile(t, "static/console.css"))

	toggles := regexp.MustCompile(`class="toggle"`).FindAllString(html, -1)
	if len(toggles) < 2 {
		t.Fatalf("found %d toggles; enabling IPSC and the slot bit are two", len(toggles))
	}
	if n := strings.Count(html, `class="toggle__input" id="ipsc`); n < 2 {
		t.Errorf("%d of the toggles are backed by an input; a div would need "+
			"role, tabindex and key handling reimplemented", n)
	}
	// Visually hidden, not display:none, which would take it out of tab order
	// and remove the focus ring with it.
	rule := regexp.MustCompile(`(?s)\.toggle__input\s*\{(.*?)\}`).FindStringSubmatch(css)
	if rule == nil {
		t.Fatal(".toggle__input has no rule")
	}
	if strings.Contains(rule[1], "display: none") {
		t.Error("the toggle's input is display:none, so it cannot be tabbed to or focused")
	}
	// State in words as well as colour.
	if !strings.Contains(html, "toggle__state") {
		t.Error("the toggle's state is carried by colour alone")
	}
}

// TestTheSaveNoticeIsBroughtIntoView.
//
// **A confirmation you cannot see is not one.** The saved notice is the first
// thing in the page and the Save button is the last, so pressing Save changed
// nothing in view: the change count, the version number and the restart
// instruction all rendered several screens above where the operator was
// looking. That is indistinguishable from a button that does nothing, and it
// was reported as exactly that.
func TestTheSaveNoticeIsBroughtIntoView(t *testing.T) {
	html := readFile(t, "static/network.html")
	js := stripComments(readFile(t, "static/network.js"))

	// The shape of the problem: the notice precedes the actions in the source,
	// so on a long page it is off-screen when the button is pressed.
	notice := strings.Index(html, `id="saved"`)
	actions := strings.Index(html, `id="save"`)
	if notice < 0 || actions < 0 {
		t.Fatal("the saved notice or the save button is missing")
	}
	if notice > actions {
		t.Skip("the notice now follows the button; scrolling may no longer be needed")
	}
	// **The call, not the name.** Searching for "scrollIntoView" finds the
	// helper's own declaration and passes with the call deleted — which it did.
	if !strings.Contains(js, "scrollIntoView(savedBox)") {
		t.Error("saving scrolls nothing into view, so the confirmation renders " +
			"off-screen above the button that produced it")
	}
	// Motion an operator may have asked their system not to produce.
	if !strings.Contains(js, "prefers-reduced-motion") {
		t.Error("the scroll ignores prefers-reduced-motion")
	}
}

// TestTheRestartNoticeSaysHow.
//
// "Restart required" tells an operator to interrupt their network without
// saying what to run, and QSP's two installs need different commands. The page
// promised the save would say so and the save said only which settings.
func TestTheRestartNoticeSaysHow(t *testing.T) {
	js := stripComments(readFile(t, "static/network.js"))

	i := strings.Index(js, "Restart QSP for these to take effect")
	if i < 0 {
		t.Fatal("nothing tells an operator a restart is needed")
	}
	sentence := js[i:min(i+400, len(js))]
	for _, want := range []string{"systemctl restart qsp", "container"} {
		if !strings.Contains(sentence, want) {
			t.Errorf("the restart notice never mentions %q, so it says to restart "+
				"without saying how", want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
