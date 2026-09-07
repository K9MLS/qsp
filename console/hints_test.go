package console_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestEveryHintButtonIsWiredAndSaysSomething is the test that would have caught
// a button that does nothing.
//
// `hints.js` is what makes a hint expand, and it is loaded per page. The
// console page grew a hint beside "Connected peers" and did not load it, so the
// button rendered correctly — the stylesheet is shared — and did nothing at
// all. Its own header calls that outcome "worse than no buttons at all".
//
// **Fourteenth instance of something built, styled and never wired.** The
// difference this time is that a test now says so for every page rather than
// for the one somebody happened to look at.
func TestEveryHintButtonIsWiredAndSaysSomething(t *testing.T) {
	pages, err := filepath.Glob("static/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no pages found: %v", err)
	}

	button := regexp.MustCompile(`aria-controls="(hint-[^"]+)"`)
	for _, page := range pages {
		html := readFile(t, page)
		hints := button.FindAllStringSubmatch(html, -1)
		if len(hints) == 0 {
			continue
		}

		// The script that makes them expand.
		if !strings.Contains(html, `src="/hints.js"`) {
			t.Errorf("%s has %d hint buttons and does not load hints.js; "+
				"they would render and do nothing", filepath.Base(page), len(hints))
		}

		for _, h := range hints {
			id := h[1]
			// The panel the button reveals has to exist, or the button
			// controls nothing and a screen reader is told about an element
			// that is not there.
			if !strings.Contains(html, `id="`+id+`"`) {
				t.Errorf("%s: a hint button controls %q and no such element exists",
					filepath.Base(page), id)
			}
		}
	}
}

// TestHintsAreReachableWithoutAMouse checks the two properties hints.js chose a
// button for in the first place.
//
// Its header rejects a floating tooltip because hover does not exist on a touch
// screen and is not reachable from a keyboard. That reasoning is only worth
// anything if the markup keeps its side of it: a real <button>, and a label for
// somebody who cannot see the icon.
func TestHintsAreReachableWithoutAMouse(t *testing.T) {
	pages, err := filepath.Glob("static/*.html")
	if err != nil {
		t.Fatalf("%v", err)
	}
	opener := regexp.MustCompile(`<[a-zA-Z]+[^>]*class="hint"[^>]*>`)
	for _, page := range pages {
		for _, tag := range opener.FindAllString(readFile(t, page), -1) {
			if !strings.HasPrefix(tag, "<button") {
				t.Errorf("%s: a hint is %q, which is not focusable or activated by Enter",
					filepath.Base(page), tag)
			}
			if !strings.Contains(tag, "aria-label") {
				t.Errorf("%s: an icon-only hint button carries no label", filepath.Base(page))
			}
			if !strings.Contains(tag, `type="button"`) {
				t.Errorf("%s: a hint button has no type and would submit a form",
					filepath.Base(page))
			}
		}
	}
}

// TestThePeersCaptionSaysOnlyWhatItMustReplaces three sentences that sat above
// the table permanently.
//
// They cost a line of the panel on every load to say something an operator
// needs once, which is what the hint is for. A caption stays, because a table
// without one is announced by a screen reader as nothing in particular.
func TestThePeersCaptionSaysOnlyWhatItMust(t *testing.T) {
	js := stripComments(readFile(t, "static/console.js"))
	for _, gone := range []string{
		"announces no callsign or location",
		"written down by an administrator and a dash",
	} {
		if strings.Contains(js, gone) {
			t.Errorf("the peers caption still carries %q, which belongs in the hint", gone)
		}
	}
	if !strings.Contains(js, "<caption>Peers currently registered") {
		t.Error("the peers table lost its caption entirely")
	}
	// Telling a signed-in administrator that addresses are for signed-in
	// administrators is noise; telling a signed-out one why the column is
	// missing answers their question.
	if !strings.Contains(js, "Sign in to see peer addresses") {
		t.Error("a signed-out caller is not told why the address column is absent")
	}

	html := readFile(t, "static/index.html")
	if !strings.Contains(html, "hint-peers") {
		t.Error("the peers panel has no hint to hold the explanation")
	}
	if !strings.Contains(html, "written down by an\n          administrator") {
		t.Error("the explanation did not survive the move into the hint")
	}
}
