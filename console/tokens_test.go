package console

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// tokens.css promises a 4.5:1 minimum and nothing checked it.
//
// That promise has now been broken twice. Once by an opacity of 0.45 applied to
// muted text, caught by an operator looking at the screen. Once while widening
// the gap between panels and the page: lightening a panel's header band by 3%
// put subtle text at 4.39:1, caught only because the ratio happened to be
// calculated by hand at the time.
//
// A rule a stylesheet states about itself should not depend on somebody
// remembering to do the arithmetic.

var (
	hexVar   = regexp.MustCompile(`(?m)^\s*(--color-[a-z-]+):\s*(#[0-9a-fA-F]{6})\s*;`)
	alphaVar = regexp.MustCompile(`(?m)^\s*(--color-[a-z-]+):\s*rgba\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)\s*,\s*([0-9.]+)\s*\)\s*;`)
)

type rgb struct{ r, g, b float64 }

func parseHex(s string) (rgb, error) {
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil {
		return rgb{}, err
	}
	return rgb{float64(v >> 16 & 0xff), float64(v >> 8 & 0xff), float64(v & 0xff)}, nil
}

// over composites a translucent colour onto an opaque one.
func over(fg rgb, alpha float64, bg rgb) rgb {
	return rgb{
		fg.r*alpha + bg.r*(1-alpha),
		fg.g*alpha + bg.g*(1-alpha),
		fg.b*alpha + bg.b*(1-alpha),
	}
}

// relativeLuminance implements WCAG 2.1 §relative luminance.
func relativeLuminance(c rgb) float64 {
	f := func(v float64) float64 {
		v /= 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.r) + 0.7152*f(c.g) + 0.0722*f(c.b)
}

func contrast(a, b rgb) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// tokens reads the colour variables out of the stylesheet itself, so the test
// measures what ships rather than a copy that can drift from it.
func tokens(t *testing.T) (opaque map[string]rgb, translucent map[string]struct {
	c     rgb
	alpha float64
}) {
	t.Helper()
	body, err := assets.ReadFile("static/tokens.css")
	if err != nil {
		t.Fatalf("reading tokens.css: %v", err)
	}
	css := string(body)

	opaque = make(map[string]rgb)
	for _, m := range hexVar.FindAllStringSubmatch(css, -1) {
		c, err := parseHex(m[2])
		if err != nil {
			t.Fatalf("%s is not a colour: %v", m[1], err)
		}
		opaque[m[1]] = c
	}

	translucent = make(map[string]struct {
		c     rgb
		alpha float64
	})
	for _, m := range alphaVar.FindAllStringSubmatch(css, -1) {
		var vals [3]float64
		for i := 0; i < 3; i++ {
			v, err := strconv.ParseFloat(m[i+2], 64)
			if err != nil {
				t.Fatalf("%s has a bad channel: %v", m[1], err)
			}
			vals[i] = v
		}
		alpha, err := strconv.ParseFloat(m[5], 64)
		if err != nil {
			t.Fatalf("%s has a bad alpha: %v", m[1], err)
		}
		translucent[m[1]] = struct {
			c     rgb
			alpha float64
		}{rgb{vals[0], vals[1], vals[2]}, alpha}
	}

	if len(opaque) == 0 {
		t.Fatal("no colour tokens were parsed; the stylesheet's shape has changed and this test is now blind")
	}
	return opaque, translucent
}

// TestTextMeetsContrastFloor checks every text colour against every surface it
// is drawn on, including the composited header band.
func TestTextMeetsContrastFloor(t *testing.T) {
	const floor = 4.5

	opaque, translucent := tokens(t)

	need := func(name string) rgb {
		c, ok := opaque[name]
		if !ok {
			t.Fatalf("tokens.css no longer defines %s", name)
		}
		return c
	}

	background := need("--color-background")
	surface := need("--color-surface")

	// The header band is translucent black over the surface, so its effective
	// colour has to be composited before it can be measured. Measuring the
	// surface instead would silently pass a band that fails.
	sunken, ok := translucent["--color-surface-sunken"]
	if !ok {
		t.Fatal("tokens.css no longer defines --color-surface-sunken")
	}
	header := over(sunken.c, sunken.alpha, surface)

	// Table rows are composited onto the surface too, and text sits on them.
	// A stripe that broke the floor would be invisible to a check that only
	// measured the surface underneath it.
	planes := map[string]rgb{
		"page background": background,
		"panel surface":   surface,
		"panel header":    header,
	}
	for name, plane := range map[string]string{
		"striped table row": "--color-row-stripe",
		"hovered table row": "--color-row-hover",
	} {
		tint, ok := translucent[plane]
		if !ok {
			t.Fatalf("tokens.css no longer defines %s", plane)
		}
		planes[name] = over(tint.c, tint.alpha, surface)
	}
	texts := []string{
		"--color-foreground",
		"--color-foreground-muted",
		"--color-foreground-subtle",
	}

	for plane, bg := range planes {
		for _, name := range texts {
			got := contrast(need(name), bg)
			if got < floor {
				t.Errorf("%s on the %s is %.2f:1, below the %.1f:1 floor tokens.css promises",
					name, plane, got, floor)
			}
		}
	}
}

// TestStatusColoursAreLegibleOnPanels covers the colours that carry meaning.
// A status nobody can read is worse than no status, and relying on colour alone
// already asks a lot of it.
func TestStatusColoursAreLegibleOnPanels(t *testing.T) {
	const floor = 4.5
	opaque, _ := tokens(t)

	surface, ok := opaque["--color-surface"]
	if !ok {
		t.Fatal("tokens.css no longer defines --color-surface")
	}

	var checked int
	for name, c := range opaque {
		if !strings.HasPrefix(name, "--color-healthy") &&
			!strings.HasPrefix(name, "--color-degraded") &&
			!strings.HasPrefix(name, "--color-failing") &&
			!strings.HasPrefix(name, "--color-unavailable") {
			continue
		}
		checked++
		if got := contrast(c, surface); got < floor {
			t.Errorf("%s on a panel is %.2f:1, below %.1f:1", name, got, floor)
		}
	}
	if checked == 0 {
		t.Skip("no status colour tokens found under the expected names")
	}
}

// TestPanelsAreDistinguishableFromThePage is the complaint that started this.
// Surface and background were 1.09:1 apart, which is not a difference anybody
// can see, so a column of panels read as one continuous area with hairlines
// drawn on it.
//
// The threshold is deliberately modest. There is very little room above it
// before subtle text on the surface breaks the 4.5:1 floor, which is why panels
// also carry a shadow — most of the perceptual separation comes from that, and
// a ratio alone cannot measure it.
func TestPanelsAreDistinguishableFromThePage(t *testing.T) {
	const minimum = 1.2

	opaque, _ := tokens(t)
	background := opaque["--color-background"]
	surface := opaque["--color-surface"]

	if got := contrast(surface, background); got < minimum {
		t.Errorf("panels sit %.2f:1 from the page, below %.2f:1; they will read as one mass",
			got, minimum)
	}
}

// TestPanelsCarryAShadow guards the other half of that separation. The ratio
// test above cannot see a shadow, and the shadow is doing most of the work.
func TestPanelsCarryAShadow(t *testing.T) {
	body, err := assets.ReadFile("static/console.css")
	if err != nil {
		t.Fatalf("reading console.css: %v", err)
	}
	css := string(body)

	// Anchored to the start of a line, so it matches the .panel rule itself
	// rather than a descendant selector that happens to end in the same text.
	// A substring search found ".main > .panel {" the moment one was added
	// above it, read that rule's body, and reported a shadow missing that was
	// three rules further down.
	rule := regexp.MustCompile(`(?m)^\.panel\s*\{([^}]*)\}`)
	m := rule.FindStringSubmatch(css)
	if m == nil {
		t.Fatal("console.css no longer has a top-level .panel rule; this test is now blind")
	}
	if !strings.Contains(m[1], "box-shadow") {
		t.Error(".panel has no box-shadow; surface and background are too close " +
			"for the fill alone to separate them")
	}
}

// TestNoRawHexInComponentStyles keeps colour decisions in one file. A hex in a
// component is a colour nothing can audit, and this test would not see it.
func TestNoRawHexInComponentStyles(t *testing.T) {
	hex := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b`)

	for _, name := range []string{"static/console.css", "static/join.css"} {
		body, err := assets.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, "url(") || strings.HasPrefix(strings.TrimSpace(line), "*") {
				continue
			}
			if m := hex.FindString(line); m != "" {
				t.Errorf("%s:%d uses the raw colour %s; put it in tokens.css so it can be audited\n\t%s",
					name, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}

// report prints the measured values, so a failure is diagnosable and a passing
// run still shows the numbers when asked with -v.
func TestContrastReport(t *testing.T) {
	opaque, translucent := tokens(t)
	surface := opaque["--color-surface"]
	sunken := translucent["--color-surface-sunken"]

	rows := []struct {
		plane string
		c     rgb
	}{
		{"page background", opaque["--color-background"]},
		{"panel surface", surface},
		{"panel header", over(sunken.c, sunken.alpha, surface)},
	}
	for _, r := range rows {
		for _, text := range []string{
			"--color-foreground", "--color-foreground-muted", "--color-foreground-subtle",
		} {
			t.Logf("%-16s %-26s %.2f:1", r.plane, text, contrast(opaque[text], r.c))
		}
	}
	t.Logf("%-16s %-26s %.2f:1", "separation", "surface vs background",
		contrast(surface, opaque["--color-background"]))
}

// TestMapIsServed keeps the map's script reachable. It is a separate file so it
// can be lifted out, which also means it can be forgotten.
func TestMapIsServed(t *testing.T) {
	for _, name := range []string{"static/map.js", "static/console.js", "static/index.html"} {
		if _, err := assets.ReadFile(name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
	index, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("reading index.html: %v", err)
	}
	// map.js defines what console.js uses, so it has to be loaded first.
	body := string(index)
	mapAt := strings.Index(body, "/map.js")
	consoleAt := strings.Index(body, "/console.js")
	if mapAt < 0 {
		t.Fatal("index.html does not load map.js")
	}
	if consoleAt < 0 {
		t.Fatal("index.html does not load console.js")
	}
	if mapAt > consoleAt {
		t.Error("map.js is loaded after console.js, which uses it")
	}
}

// TestTheMapVendorsNothing is the property ADR-0025 is about. A script tag
// pointing at a CDN, or a vendored library appearing in static/, would end the
// console's no-dependency guarantee quietly.
func TestTheMapVendorsNothing(t *testing.T) {
	index, err := assets.ReadFile("static/index.html")
	if err != nil {
		t.Fatalf("reading index.html: %v", err)
	}
	for _, tag := range regexp.MustCompile(`(?i)<(script|link)[^>]*>`).FindAllString(string(index), -1) {
		if strings.Contains(tag, "//") && !strings.Contains(tag, `"/`) {
			t.Errorf("the console loads something from off the instance: %s", tag)
		}
	}

	// Every script in static/ is one QSP wrote. Naming them rather than
	// counting them: a count says "three" when a library arrives and somebody
	// updates the number, while a name says which file nobody recognises.
	ours := map[string]bool{"console.js": true, "map.js": true, "join.js": true, "signin.js": true}
	entries, err := assets.ReadDir("static")
	if err != nil {
		t.Fatalf("reading static: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".js") {
			continue
		}
		if !ours[name] {
			t.Errorf("static/%s is a script the console did not write; vendoring a library "+
				"ends the no-dependency guarantee ADR-0025 turns on", name)
		}
	}
}

// TestSignInPageIsServed. It is reached by a redirect rather than by a link
// from the console's markup, so nothing else would notice it going missing.
func TestSignInPageIsServed(t *testing.T) {
	for _, name := range []string{"static/signin.html", "static/signin.js"} {
		if _, err := assets.ReadFile(name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
	page, err := assets.ReadFile("static/signin.html")
	if err != nil {
		t.Fatalf("reading signin.html: %v", err)
	}
	body := string(page)
	if !strings.Contains(body, "/signin.js") {
		t.Error("signin.html does not load signin.js")
	}
	// The page styles itself from the shared sheets rather than its own, so a
	// change to the tokens reaches it.
	for _, sheet := range []string{"/tokens.css", "/console.css"} {
		if !strings.Contains(body, sheet) {
			t.Errorf("signin.html does not load %s", sheet)
		}
	}
	// A password field must not be a text field, whatever else changes.
	if !strings.Contains(body, `id="password" type="password"`) &&
		!strings.Contains(body, `type="password"`) {
		t.Error("the password field is not type=password")
	}
}

// TestEveryClassTheScriptsUseIsStyled.
//
// The peers table asked for `.muted`, nothing defined it, and the browser's
// default link colour showed through: blue, underlined, and unreadable on a
// dark panel. It reached a live server.
//
// A class name is a string in one file and a selector in another, so nothing
// connects them — no compiler, no linter, and the contrast test measures tokens
// rather than whether a rule exists to use them.
func TestEveryClassTheScriptsUseIsStyled(t *testing.T) {
	styles := ""
	for _, sheet := range []string{"static/console.css", "static/tokens.css", "static/join.css"} {
		body, err := assets.ReadFile(sheet)
		if err != nil {
			t.Fatalf("reading %s: %v", sheet, err)
		}
		styles += string(body)
	}

	defined := make(map[string]bool)
	for _, m := range regexp.MustCompile(`\.([a-zA-Z][\w-]*)`).FindAllStringSubmatch(styles, -1) {
		defined[m[1]] = true
	}

	// Classes the markup carries are covered by the pages themselves; this is
	// about the ones only the scripts know, which nothing else would catch.
	// Only the literal part of the attribute, up to the first quote or the
	// point where an expression begins. `class="badge " + kind` contributes
	// "badge" and stops; anything else here would be checking JavaScript
	// against a stylesheet.
	classAttr := regexp.MustCompile(`class=\\?"([a-zA-Z][\w\- ]*)`)
	var checked int
	for _, script := range []string{"static/console.js", "static/map.js", "static/join.js"} {
		body, err := assets.ReadFile(script)
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		for _, m := range classAttr.FindAllStringSubmatch(string(body), -1) {
			for _, name := range strings.Fields(m[1]) {
				// A name ending in a hyphen is a modifier prefix finished by
				// concatenation — `class="status status--" + kind` — and the
				// completed name cannot be known from the source.
				if strings.HasSuffix(name, "-") {
					continue
				}
				checked++
				if !defined[name] {
					t.Errorf("%s uses class %q, which no stylesheet defines", script, name)
				}
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d classes were found in the scripts; the check is missing some", checked)
	}
	t.Logf("checked %d class references", checked)
}
