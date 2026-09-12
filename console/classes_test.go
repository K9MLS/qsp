package console_test

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestEveryClassTheScriptsWriteIsStyled is the fifth check on the console's
// JavaScript, and it reconstructs a defect that shipped on 2026-09-12.
//
// A P25 block was added to the traffic panel with a heading of
// `class="panel-subhead"`. No such class is in `console.css`, so it rendered as
// unstyled body text in the middle of a panel of uppercase mono labels. **The
// whole gate chain passed**: `gofmt`, `vet`, `staticcheck` and every Go test,
// plus the four checks that already read this script — because a class name is
// a string to all of them.
//
// It was found by the operator looking at the page, like every other console
// defect this project has had, and none by a test.
//
// # What this does not check
//
// It compares names, not appearance. A class that exists and looks wrong still
// looks wrong, and a class used only from an HTML template is out of scope here
// — the templates are checked elsewhere. This catches the one failure that is
// mechanical: writing a name nothing defines.
func TestEveryClassTheScriptsWriteIsStyled(t *testing.T) {
	defined := definedClasses(t)

	// The scripts that draw into panels. join.js and setup.js are deliberately
	// absent: they render against join.css, which is a separate sheet.
	for _, script := range []string{"console.js", "network.js", "links.js",
		"access.js", "admin.js", "history.js", "record.js", "bridges.js"} {
		src, err := os.ReadFile("static/" + script)
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		for _, class := range writtenClasses(string(src)) {
			if !defined[class] {
				t.Errorf("%s writes class %q and console.css does not define it; "+
					"it will render unstyled, and nothing else in the gate chain "+
					"reads a class name", script, class)
			}
		}
	}
}

// classAttr finds a *wholly literal* class attribute in a JavaScript string.
//
// **The quote exclusion is what makes this a gate rather than noise.** The
// first draft allowed any character, so `class="pill pill--' + kind + '"`
// captured the concatenation and the check reported eleven nonexistent classes
// named `'`, `+`, `?` and `cls`. A check that cries wolf on its first run is
// one an author switches off.
//
// A class assembled from a variable is therefore not checked, and cannot be by
// name. That is the honest limit of a name check, and it is stated here rather
// than hidden behind a filter that makes the junk look like a decision.
var classAttr = regexp.MustCompile(`class="([^"']+)"`)

// writtenClasses lists the class names a script writes into markup.
func writtenClasses(src string) []string {
	seen := map[string]bool{}
	for _, m := range classAttr.FindAllStringSubmatch(src, -1) {
		for _, name := range strings.Fields(m[1]) {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// definedSelector finds a class selector at the start of a rule.
var definedSelector = regexp.MustCompile(`\.([A-Za-z][A-Za-z0-9_-]*)`)

// definedClasses lists every class named anywhere in the stylesheets.
//
// Both sheets, because tokens.css carries some and console.css the rest, and a
// class defined in either is styled.
func definedClasses(t *testing.T) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, sheet := range []string{"static/console.css", "static/tokens.css"} {
		b, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("reading %s: %v", sheet, err)
		}
		for _, m := range definedSelector.FindAllStringSubmatch(string(b), -1) {
			out[m[1]] = true
		}
	}
	return out
}
