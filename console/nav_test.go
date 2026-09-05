package console

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// TestASectionHeadingIsNotTheColourOfALink is the complaint that started this.
//
// The sidebar's section headings and its not-yet-built links were both
// --color-foreground-subtle, at the same left inset, in the same column. An
// operator seeing it for the first time read "Operations" and "Administration"
// as two more links, and was right to: **the eye groups by colour and position
// before it reads a 10px uppercase label as a different kind of thing.** Size,
// tracking and case were all carrying the distinction and none of them wins
// against colour.
func TestASectionHeadingIsNotTheColourOfALink(t *testing.T) {
	raw, err := assets.ReadFile("static/console.css")
	if err != nil {
		t.Fatalf("reading console.css: %v", err)
	}
	css := string(raw)

	colour := func(selector string) string {
		t.Helper()
		re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(selector) + `\s*\{(.*?)\}`)
		m := re.FindStringSubmatch(css)
		if m == nil {
			t.Fatalf("console.css no longer has a rule for %s", selector)
		}
		c := regexp.MustCompile(`color:\s*var\((--[a-z-]+)\)`).FindStringSubmatch(m[1])
		if c == nil {
			t.Fatalf("%s no longer sets a colour from a token", selector)
		}
		return c[1]
	}

	heading := colour(".nav__heading")
	for _, link := range []string{".nav__link", `.nav__link[aria-disabled="true"]`} {
		if got := colour(link); got == heading {
			t.Errorf("%s and .nav__heading are both %s, so a heading reads as one more link",
				link, got)
		}
	}
}

// TestAdministrationShipsHidden is the safe default for a sidebar that a
// signed-out visitor sees.
//
// nav.js reveals the administration group once /api/session confirms a
// session. **If the markup shipped visible, every page load would flash seven
// administration links at a visitor before the fetch returned**, and a fetch
// that never returned would leave them there. Nothing behind them leaks, so
// this is about what a sidebar is for rather than about secrecy — but a
// default that is wrong until JavaScript corrects it is still a default that
// is wrong.
//
// The nav is copied into seven pages. A rule that holds in one copy and not
// the other six is the reason this counts them.
func TestAdministrationShipsHidden(t *testing.T) {
	pages, err := fs.Glob(assets, "static/*.html")
	if err != nil {
		t.Fatalf("reading the console's pages: %v", err)
	}

	var withNav int
	for _, page := range pages {
		raw, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		html := string(raw)
		if !strings.Contains(html, `id="nav-admin-group"`) {
			continue
		}
		withNav++

		for _, want := range []string{
			`<ul class="nav__group" id="nav-admin-group" aria-labelledby="nav-admin" hidden>`,
			`<p class="nav__heading" id="nav-admin" hidden>Administration</p>`,
		} {
			if !strings.Contains(html, want) {
				t.Errorf("%s: the administration group or its heading does not ship hidden", page)
				break
			}
		}
	}
	if withNav < 7 {
		t.Errorf("only %d pages carry the sidebar; there were seven, so this test "+
			"is no longer reading them all", withNav)
	}
}
