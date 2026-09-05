package console

import (
	"regexp"
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
