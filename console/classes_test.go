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

// TestParagraphClassesOwnTheirMargin is the defect a screenshot found on
// 2026-09-12.
//
// The reset in `console.css` sets `box-sizing` and nothing else, and `body`
// gets `margin: 0`. So a `<p class="inline-note">` carried the browser default
// `margin: 1em 0` — about 13px at `--text-sm` — on top of its own padding, and
// `.panel` has `overflow: hidden`, so the bottom margin could not collapse
// out. The note read 12px above its text and ~25px below it, with a further
// 13px pushing its divider away from the metrics above. Three different
// numbers, none of them chosen by anybody.
//
// **This checks declaration, not value.** `margin: 0` and
// `margin: var(--space-2) 0` both pass, because either is a decision; what
// fails is saying nothing and inheriting a number from the user agent.
//
// # Two ways the first draft of this was wrong
//
// It checked `strings.Contains(rule, "margin")` against the raw rule, and the
// comment *inside* `.inline-note` contains the words "margin: 1em 0" — so the
// gate passed on the fixed code and would have passed on the broken code too.
// A test that cannot fail, seventh instance, caught by deliberately breaking
// the CSS and watching it stay green for the wrong reason.
//
// And it took its class list from a guess — which named `.hint`, a `<button>`
// in `access.js` where a paragraph margin means nothing. The list is now
// derived from the markup: whatever class the scripts actually put on a `<p>`.
func TestParagraphClassesOwnTheirMargin(t *testing.T) {
	// stripComments is traffic_test.go's, reused rather than rewritten: §8a
	// records that a rule written into a test does not reach the next command
	// typed from memory, and the same goes for a helper written twice.
	css := stripComments(stylesheets(t))

	for _, class := range paragraphClasses(t) {
		// **A modifier is never used alone.** `.inline-note--neutral` sets a
		// colour and nothing else, and is always written beside
		// `.inline-note`, which owns the box. Checking it would demand a
		// margin that must not be there. This is a property of the naming
		// convention rather than an exemption list — §8a: an exemption list
		// is how a gate stops being a gate.
		if strings.Contains(class, "--") {
			continue
		}
		rule, ok := ruleFor(css, class)
		if !ok {
			// A class may be styled by a compound selector or carry no rule of
			// its own; that is not this gate's business.
			continue
		}
		if !marginDeclared.MatchString(rule) {
			t.Errorf(".%s is used on a <p> and declares no margin, so it "+
				"inherits the user agent's `margin: 1em 0`; inside a panel "+
				"with overflow: hidden that cannot collapse out and reads as "+
				"lopsided padding", class)
		}
	}
}

// marginDeclared matches a margin declaration, long-hand or specific side.
var marginDeclared = regexp.MustCompile(`(^|[;{\s])margin(-top|-bottom|-block[a-z-]*)?\s*:`)

// paragraphClass finds a class attribute on a paragraph the scripts write.
var paragraphClass = regexp.MustCompile(`<p class="([^"']+)"`)

// paragraphClasses lists the classes the scripts put on a <p>.
func paragraphClasses(t *testing.T) []string {
	t.Helper()
	seen := map[string]bool{}
	entries, err := os.ReadDir("static")
	if err != nil {
		t.Fatalf("reading static: %v", err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		src, err := os.ReadFile("static/" + e.Name())
		if err != nil {
			t.Fatalf("reading %s: %v", e.Name(), err)
		}
		for _, m := range paragraphClass.FindAllStringSubmatch(string(src), -1) {
			for _, name := range strings.Fields(m[1]) {
				seen[name] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// stylesheets returns both sheets concatenated.
func stylesheets(t *testing.T) string {
	t.Helper()
	var all strings.Builder
	for _, sheet := range []string{"static/console.css", "static/tokens.css"} {
		b, err := os.ReadFile(sheet)
		if err != nil {
			t.Fatalf("reading %s: %v", sheet, err)
		}
		all.Write(b)
	}
	return all.String()
}

// ruleFor returns the body of the first rule whose selector is exactly this
// class, which is enough for the single-class selectors this sheet uses.
func ruleFor(css, class string) (string, bool) {
	needle := "\n." + class + " {"
	i := strings.Index(css, needle)
	if i < 0 {
		return "", false
	}
	rest := css[i+len(needle):]
	end := strings.Index(rest, "}")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
