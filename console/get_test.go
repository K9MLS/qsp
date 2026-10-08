package console_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAPageThatLoadsThroughGetHasItAndSomewhereToSaySo. get.js is how a
// page tells a refusal from an answer (2026-10-07, F4), and it is three
// things that have to agree: the script, loaded before the page's own; the
// notice it writes into; and the page using it. A page that uses QSPGet and
// does not load it throws as it starts and shows nothing at all.
//
// What the loader does is checked in a browser, by scripts/console-check.
//
// Break it: remove the get.js script tag, or the load-error notice, from any
// page that uses QSPGet.
func TestAPageThatLoadsThroughGetHasItAndSomewhereToSaySo(t *testing.T) {
	scripts, err := filepath.Glob("static/*.js")
	if err != nil {
		t.Fatal(err)
	}
	uses := 0
	for _, path := range scripts {
		page := strings.TrimSuffix(filepath.Base(path), ".js")
		if page == "get" || !strings.Contains(stripComments(readFile(t, "static/"+page+".js")), "QSPGet(") {
			continue
		}
		uses++
		if _, err := os.Stat("static/" + page + ".html"); err != nil {
			t.Errorf("%s.js uses QSPGet and has no page of its own: %v", page, err)
			continue
		}
		html := readFile(t, "static/"+page+".html")
		get := strings.Index(html, `src="/get.js"`)
		own := strings.Index(html, `src="/`+page+`.js"`)
		switch {
		case get < 0:
			t.Errorf("%s.js uses QSPGet and %s.html does not load get.js", page, page)
		case own < get:
			t.Errorf("%s.html loads get.js after %s.js, which runs first and finds no QSPGet", page, page)
		}
		for _, want := range []string{`id="load-error"`, `id="load-error-text"`} {
			if !strings.Contains(html, want) {
				t.Errorf("%s.html has no %s for a page that could not load to say so", page, want)
			}
		}
	}
	if uses < 8 {
		t.Errorf("%d pages use QSPGet, want the eight that load something", uses)
	}
}
