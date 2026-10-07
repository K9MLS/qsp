package console_test

import (
	"regexp"
	"strings"
	"testing"
)

// TestAPageWithASaveBarLoadsItAndHasSomewhereToAnswer.
//
// A page's Save button was at its foot and the answer at its head, so a
// refused save looked like a successful one. The save bar keeps the two
// together, and it is three things that have to agree: the script, loaded
// before the page's own; the place the answer is written; and the page's
// script using it. Any one missing and the page saves in silence again, or
// throws on load and shows nothing at all.
//
// What the bar does is checked in a browser, by scripts/console-check.
//
// Break it: remove the savebar.js script tag from a page that uses it, or
// move it below the page's own script.
func TestAPageWithASaveBarLoadsItAndHasSomewhereToAnswer(t *testing.T) {
	uses := 0
	for _, page := range []string{"bridges", "network", "access", "zello", "weather", "admin", "links"} {
		js := stripComments(readFile(t, "static/"+page+".js"))
		html := readFile(t, "static/"+page+".html")
		if !strings.Contains(js, "QSPSaveBar(") {
			if strings.Contains(html, `class="savebar"`) {
				t.Errorf("%s.html has a save bar that %s.js never speaks through", page, page)
			}
			continue
		}
		uses++

		bar := strings.Index(html, `src="/savebar.js"`)
		own := strings.Index(html, `src="/`+page+`.js"`)
		switch {
		case bar < 0:
			t.Errorf("%s.js uses the save bar and %s.html does not load savebar.js; "+
				"the page throws as it starts and draws nothing", page, page)
		case own < bar:
			t.Errorf("%s.html loads savebar.js after %s.js, which runs first and finds no bar", page, page)
		}

		status := regexp.MustCompile(`(?s)<div class="savebar">.*?</div>`).FindString(html)
		if status == "" {
			t.Errorf("%s.html has no save bar for its script to use", page)
			continue
		}
		for _, want := range []string{`id="save"`, `id="save-status"`, `role="status"`} {
			if !strings.Contains(status, want) {
				t.Errorf("%s.html's save bar has no %s", page, want)
			}
		}
	}
	if uses == 0 {
		t.Fatal("no page uses the save bar; this check is not checking anything")
	}
}
