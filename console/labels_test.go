package console

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// **A caption that looks like a label is not a label.** Two fields on the links
// page were captioned with a <p> carrying the label class: the right size, the
// right colour, the right position, and no association with the control at all.
// A screen reader reads those controls unnamed, and clicking the caption does
// not focus the field, which is the one interaction that makes a label worth
// having.
//
// It looked correct, which is why it survived. This test does not look.
func TestEveryFormControlHasALabel(t *testing.T) {
	var (
		control  = regexp.MustCompile(`(?s)<(input|textarea|select)\b.*?>`)
		idAttr   = regexp.MustCompile(`id="([^"]+)"`)
		typeAttr = regexp.MustCompile(`type="([^"]+)"`)
		forAttr  = regexp.MustCompile(`<label[^>]*for="([^"]+)"`)
	)

	pages, err := fs.Glob(assets, "static/*.html")
	if err != nil {
		t.Fatalf("reading the console's pages: %v", err)
	}
	if len(pages) == 0 {
		t.Fatal("no pages found; this test would pass on an empty console")
	}

	var checked int
	for _, page := range pages {
		raw, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		html := string(raw)

		named := map[string]bool{}
		for _, m := range forAttr.FindAllStringSubmatch(html, -1) {
			named[m[1]] = true
		}

		for _, loc := range control.FindAllStringIndex(html, -1) {
			tag := html[loc[0]:loc[1]]
			if m := typeAttr.FindStringSubmatch(tag); m != nil {
				switch m[1] {
				case "hidden", "submit", "button":
					// Not a field an operator fills in; its own text names it.
					continue
				}
			}
			checked++

			// A control inside a <label> is named by it without an id, which
			// is why this looks for an unclosed <label> before the control
			// rather than only for a for= attribute.
			if strings.LastIndex(html[:loc[0]], "<label") >
				strings.LastIndex(html[:loc[0]], "</label>") {
				continue
			}
			if strings.Contains(tag, "aria-label") {
				continue
			}
			id := idAttr.FindStringSubmatch(tag)
			if id == nil {
				t.Errorf("%s: a control has no id, no aria-label and no enclosing label: %s",
					page, oneLine(tag))
				continue
			}
			if !named[id[1]] {
				t.Errorf("%s: control %q has no <label for>, no aria-label and no enclosing label",
					page, id[1])
			}
		}
	}

	// **The count is the guard against a silent pass.** A regex that stopped
	// matching would report nothing and look exactly like a console with no
	// defects in it.
	if checked < 20 {
		t.Errorf("only %d controls were checked; the console has more than that, "+
			"so this test is no longer reading the pages", checked)
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
