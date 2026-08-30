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
	ours := map[string]bool{
		"console.js": true, "map.js": true, "join.js": true,
		"signin.js": true, "access.js": true, "network.js": true,
		"bridges.js": true, "history.js": true, "hints.js": true,
		"nav.js": true, "links.js": true,
	}
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

	// Markup as well as scripts. The first version checked only scripts, on
	// the reasoning that a page's own classes are visible when you look at it —
	// which is exactly the reasoning that let `.muted` ship.
	// Only the literal part of the attribute, up to the first quote or the
	// point where an expression begins. `class="badge " + kind` contributes
	// "badge" and stops; anything else here would be checking JavaScript
	// against a stylesheet.
	classAttr := regexp.MustCompile(`class=\\?"([a-zA-Z][\w\- ]*)`)
	var checked int
	for _, script := range []string{
		"static/console.js", "static/map.js", "static/join.js", "static/access.js",
		"static/index.html", "static/join.html", "static/signin.html",
		"static/access.html", "static/network.html", "static/network.js",
		"static/bridges.html", "static/bridges.js",
		"static/history.html", "static/history.js", "static/hints.js",
	} {
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

// TestTheAccessPageIsServed. It is reached by a redirect and a nav link, so a
// missing asset would show as a blank page rather than an error.
func TestTheAccessPageIsServed(t *testing.T) {
	for _, name := range []string{"static/access.html", "static/access.js"} {
		if _, err := assets.ReadFile(name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
	page, err := assets.ReadFile("static/access.html")
	if err != nil {
		t.Fatalf("reading access.html: %v", err)
	}
	body := string(page)
	if !strings.Contains(body, "/access.js") {
		t.Error("access.html does not load access.js")
	}
	// The page must offer a way in rather than a dead form when nobody is
	// signed in: the endpoints refuse anonymously and the page should say so.
	if !strings.Contains(body, "/signin") {
		t.Error("access.html does not link to the sign-in page")
	}
}

// TestTheJoinPageIsReachableFromTheConsole. It is the page an operator sends to
// members, and it existed with no link to it from anywhere — findable only by
// somebody who already knew the URL, which is not the person who needs it.
func TestTheJoinPageIsReachableFromTheConsole(t *testing.T) {
	for _, page := range []string{"static/index.html", "static/access.html"} {
		body, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		if !strings.Contains(string(body), `href="/join"`) {
			t.Errorf("%s does not link to the join page", page)
		}
	}
}

// TestTheNetworkPageIsServed. It edits the join details and parrot, which were
// otherwise a matter of hand-editing JSON or posting it with curl.
func TestTheNetworkPageIsServed(t *testing.T) {
	for _, name := range []string{"static/network.html", "static/network.js"} {
		if _, err := assets.ReadFile(name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
	page, err := assets.ReadFile("static/network.html")
	if err != nil {
		t.Fatalf("reading network.html: %v", err)
	}
	body := string(page)
	if !strings.Contains(body, "/network.js") {
		t.Error("network.html does not load network.js")
	}
	// **The console must not offer to rewrite a talkgroup number.** It used to
	// carry an "Arrives" field for the case where a hotspot rewrites on the way
	// out, and teaching that as normal is how a network ends up with rules
	// nobody remembers writing and a member transmitting into silence. A
	// talkgroup number is the same on both sides of a hotspot: 2 is 2, 11 is 11.
	if strings.Contains(body, "Arrives") {
		t.Error("the talkgroup form still offers to rewrite a talkgroup number")
	}
	script, err := assets.ReadFile("static/network.js")
	if err != nil {
		t.Fatalf("reading network.js: %v", err)
	}
	src := string(script)
	if !strings.Contains(src, ">Dialled<") {
		t.Error("the talkgroup form does not show the number members dial")
	}
	if strings.Contains(src, `data-key="arrives"`) {
		t.Error("the talkgroup form still writes a rewrite into the configuration")
	}
}

// TestEveryAdminPageSharesTheNavigation. A page reachable only by typing its
// URL is one an operator does not know exists.
func TestEveryAdminPageSharesTheNavigation(t *testing.T) {
	pages := []string{"static/index.html", "static/access.html", "static/network.html",
		"static/bridges.html", "static/history.html"}
	for _, page := range pages {
		body, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		for _, link := range []string{`href="/access"`, `href="/network"`,
			`href="/bridges"`, `href="/history"`, `href="/join"`} {
			if !strings.Contains(string(body), link) {
				t.Errorf("%s does not carry %s", page, link)
			}
		}
	}
}

// TestWrappedPanelsAreSpaced.
//
// .main is a grid and its gap separates its own children, so an admin page
// that wraps its panels — as they all do, to hide the whole form until the
// configuration loads — got the gap once around the wrapper and the panels
// inside it touched.
func TestWrappedPanelsAreSpaced(t *testing.T) {
	css, err := assets.ReadFile("static/console.css")
	if err != nil {
		t.Fatalf("reading console.css: %v", err)
	}
	if !strings.Contains(string(css), ".stack {") {
		t.Fatal("no rule spaces panels inside a wrapper")
	}

	for _, page := range []string{"static/access.html", "static/network.html",
		"static/bridges.html", "static/history.html"} {
		body, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		src := string(body)
		// **Only a wrapper needs the rule.** Panels that are direct children of
		// .main already get its grid gap; the first version of this check
		// required the class of every page with two panels and failed the
		// history page, which does not wrap them.
		wrapper := strings.Index(src, `<div id="form"`)
		if wrapper < 0 {
			continue
		}
		if strings.Count(src[wrapper:], `<section class="panel"`) > 1 &&
			!strings.Contains(src[:wrapper+40], `class="stack"`) {
			t.Errorf("%s wraps several panels with nothing to space them", page)
		}
	}
}

// TestTheBridgesPageIsServed. Bridges and the schedule were the last
// configuration area with no interface at all.
func TestTheBridgesPageIsServed(t *testing.T) {
	for _, name := range []string{"static/bridges.html", "static/bridges.js"} {
		if _, err := assets.ReadFile(name); err != nil {
			t.Errorf("%s is not embedded: %v", name, err)
		}
	}
	body, err := assets.ReadFile("static/bridges.html")
	if err != nil {
		t.Fatalf("reading bridges.html: %v", err)
	}
	src := string(body)
	if !strings.Contains(src, "/bridges.js") {
		t.Error("bridges.html does not load bridges.js")
	}
	// The rule an operator will otherwise discover when their net does not
	// open: a scheduled bridge is off outside its windows whatever its own
	// setting says.
	if !strings.Contains(src, "controlled entirely by the schedule") {
		t.Error("the page does not say that the schedule overrides a bridge's own setting")
	}
	// An IANA zone is required and an abbreviation cannot express a
	// daylight-saving change, which is worth saying before somebody types CST.
	if !strings.Contains(src, "America/Chicago") {
		t.Error("the page does not show what a timezone should look like")
	}
}

// TestEveryFunctionTheConsoleCallsExists.
//
// **Removing the map deleted a neighbouring function and left its call site.**
// The whole poll then threw on every tick, and the catch reported "Cannot reach
// QSP" — so a working instance looked unreachable, which is the worst way for a
// mistake like this to present.
//
// JavaScript has no compiler to notice, and nothing else here reads the file
// for consistency. This does.
func TestEveryFunctionTheConsoleCallsExists(t *testing.T) {
	for _, script := range []string{
		"static/console.js", "static/access.js", "static/network.js",
		"static/bridges.js", "static/history.js", "static/join.js", "static/signin.js",
	} {
		body, err := assets.ReadFile(script)
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		src := string(body)

		defined := map[string]bool{}
		for _, m := range regexp.MustCompile(`function (\w+)\s*\(`).FindAllStringSubmatch(src, -1) {
			defined[m[1]] = true
		}
		// Names bound to a variable rather than declared.
		for _, m := range regexp.MustCompile(`(?m)^\s*(?:var|let|const)\s+(\w+)\s*=`).FindAllStringSubmatch(src, -1) {
			defined[m[1]] = true
		}
		// Parameters, which are callable inside their own function and are not
		// declarations anywhere — `handler` in a helper that takes one is the
		// case that found this.
		for _, m := range regexp.MustCompile(`function\s*\w*\s*\(([^)]*)\)`).FindAllStringSubmatch(src, -1) {
			for _, param := range strings.Split(m[1], ",") {
				if p := strings.TrimSpace(param); p != "" {
					defined[p] = true
				}
			}
		}
		for _, builtin := range []string{
			"fetch", "setTimeout", "setInterval", "parseInt", "parseFloat",
			"isNaN", "String", "Number", "Date", "JSON", "Math", "Object",
			"Array", "encodeURIComponent", "decodeURIComponent", "alert",
			"requestAnimationFrame", "EventSource", "ResizeObserver",
		} {
			defined[builtin] = true
		}

		// Calls of the shape name(...) at statement level, which is where a
		// deleted function shows up.
		calls := regexp.MustCompile(`(?m)^\s+(\w+)\((?:payload|\)|[a-z])`)
		var checked int
		for _, m := range calls.FindAllStringSubmatch(src, -1) {
			name := m[1]
			switch name {
			case "if", "for", "while", "switch", "return", "function", "catch", "typeof":
				continue
			}
			checked++
			if !defined[name] {
				t.Errorf("%s calls %s(), which nothing in that file defines", script, name)
			}
		}
		if checked == 0 {
			t.Errorf("%s: no calls were examined; this check is not checking anything", script)
		}
	}
}

// TestTheMapPlacesRelativeToItsCentre.
//
// **Six attempts at this map computed positions from a measured width**, and a
// measurement that was wrong put every tile and pin in a corner. Positions are
// offsets from the map's centre now, with the plane centred by CSS — a
// measurement cannot misplace what it is not used to place.
//
// The measurement still chooses how many tiles to draw, where being wrong costs
// a few tiles nobody sees.
func TestTheMapPlacesRelativeToItsCentre(t *testing.T) {
	body, err := assets.ReadFile("static/map.js")
	if err != nil {
		t.Fatalf("reading map.js: %v", err)
	}
	src := string(body)

	// The arithmetic must not reintroduce an origin derived from the width.
	for _, gone := range []string{"originX", "originY", "width / 2", "height / 2"} {
		if strings.Contains(src, gone) {
			t.Errorf("map.js still positions from %q; placement must be offsets "+
				"from the centre", gone)
		}
	}
	if !strings.Contains(src, "map__plane") {
		t.Error("map.js does not draw onto a centred plane")
	}

	// Style attributes are checked across every script by the test below, which
	// reads line by line and can tell a rule from a comment about it. What
	// matters here is that the map positions through the style object, which is
	// CSSOM and which the policy permits.
	if !strings.Contains(src, ".style.left =") {
		t.Error("map.js does not position through the style object")
	}
}

// TestTheConsoleNeverWritesStyleAttributes.
//
// The policy is `style-src 'self'`, which forbids them everywhere and not only
// in the map. One written anywhere is refused silently, which is the hardest
// kind of fault to see: the markup is right, the CSS is right, and the element
// is in the wrong place.
func TestTheConsoleNeverWritesStyleAttributes(t *testing.T) {
	for _, script := range []string{
		"static/console.js", "static/map.js", "static/access.js",
		"static/network.js", "static/bridges.js", "static/history.js", "static/hints.js",
		"static/join.js", "static/signin.js",
	} {
		body, err := assets.ReadFile(script)
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			// Comments explaining the rule are not breaches of it.
			if strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "//") ||
				strings.HasPrefix(trimmed, "/*") {
				continue
			}
			// Both routes to the same breach: a style attribute written into
			// markup, and one set through setAttribute. The second was missed
			// by the first version of this check, which is the sort of gap a
			// check only reveals when somebody tries to defeat it.
			if strings.Contains(line, `style="`) || strings.Contains(line, `style='`) ||
				strings.Contains(line, `setAttribute("style"`) ||
				strings.Contains(line, `setAttribute('style'`) {
				t.Errorf("%s writes a style attribute: %s", script, trimmed)
			}
		}
	}

	css, err := assets.ReadFile("static/console.css")
	if err != nil {
		t.Fatalf("reading console.css: %v", err)
	}
	rule := string(css)
	i := strings.Index(rule, ".map__plane {")
	if i < 0 {
		t.Fatal("no rule centres the plane")
	}
	block := rule[i : i+220]
	for _, want := range []string{"left: 50%", "top: 50%"} {
		if !strings.Contains(block, want) {
			t.Errorf("the plane is not centred: %q missing", want)
		}
	}
}

// TestHintsAreDisclosuresRatherThanTooltips.
//
// **A floating tooltip has to be positioned**, and positioning against a
// measured box is the class of bug that cost this project a day. A disclosure
// that expands in the flow cannot be put in the wrong place. Hover is also
// unavailable on a touch screen and unreachable from a keyboard, so a button is
// both the accessible answer and the robust one.
func TestHintsAreDisclosuresRatherThanTooltips(t *testing.T) {
	script, err := assets.ReadFile("static/hints.js")
	if err != nil {
		t.Fatalf("reading hints.js: %v", err)
	}
	src := string(script)

	// Nothing about a hint may be positioned.
	for _, banned := range []string{"getBoundingClientRect", ".style.left", ".style.top"} {
		if strings.Contains(src, banned) {
			t.Errorf("hints.js uses %s; a hint is a disclosure and must not be positioned", banned)
		}
	}
	if !strings.Contains(src, "aria-expanded") {
		t.Error("hints.js does not report its state to assistive technology")
	}

	// **Wiring must be idempotent.** A page that draws its own form calls wire
	// again for the markup it just made; wiring a button twice would toggle it
	// twice per click, which is a hint that never opens.
	if !strings.Contains(src, "data-wired") {
		t.Error("hints.js can wire the same button twice, which is a hint that never opens")
	}
	if !strings.Contains(src, "QSPHints") {
		t.Error("hints.js exposes no way to wire markup drawn after it runs")
	}

	// Every hint button must name a paragraph that exists.
	for _, page := range []string{
		"static/access.html", "static/network.html", "static/bridges.html",
		"static/history.html",
	} {
		body, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		html := string(body)

		if !strings.Contains(html, "/hints.js") {
			t.Errorf("%s has hints and does not load hints.js", page)
		}

		ids := regexp.MustCompile(`aria-controls="([^"]+)"`).FindAllStringSubmatch(html, -1)
		if len(ids) == 0 {
			t.Errorf("%s carries no hints", page)
		}
		for _, m := range ids {
			if !strings.Contains(html, `id="`+m[1]+`"`) {
				t.Errorf("%s has a hint pointing at %q, which does not exist", page, m[1])
			}
		}
		// A button with no label is a question mark screen readers cannot
		// explain.
		buttons := strings.Count(html, `class="hint"`)
		labels := strings.Count(html, `aria-label="Explain this"`)
		if buttons != labels {
			t.Errorf("%s has %d hint buttons and %d labels", page, buttons, labels)
		}
	}
}

// TestControlBoundariesMeetTheNonTextFloor.
//
// WCAG 1.4.11 asks 3:1 of the visible boundary of anything a user has to find
// and operate. Nothing here measured that, and the hint button's outline was
// --color-border-strong: 1.64:1 against a panel's header band, which is to say
// invisible. It had been that way since the button was written, through every
// review, because the contrast tests measured text and the failure was not
// text.
//
// The two planes both matter: the same button appears in a panel's header band
// and inside a panel's body on the access page.
func TestControlBoundariesMeetTheNonTextFloor(t *testing.T) {
	const floor = 3.0

	opaque, translucent := tokens(t)

	surface, ok := opaque["--color-surface"]
	if !ok {
		t.Fatal("tokens.css no longer defines --color-surface")
	}
	sunken, ok := translucent["--color-surface-sunken"]
	if !ok {
		t.Fatal("tokens.css no longer defines --color-surface-sunken")
	}
	boundary, ok := translucent["--color-border-control"]
	if !ok {
		t.Fatal("tokens.css no longer defines --color-border-control")
	}

	planes := map[string]rgb{
		"panel surface": surface,
		"panel header":  over(sunken.c, sunken.alpha, surface),
	}
	for name, bg := range planes {
		got := contrast(over(boundary.c, boundary.alpha, bg), bg)
		if got < floor {
			t.Errorf("--color-border-control on the %s is %.2f:1, below the %.1f:1 WCAG 1.4.11 floor",
				name, got, floor)
		}
	}

	// The token exists to be used. A boundary colour nothing references is a
	// measurement that passes while the control it describes stays invisible.
	css, err := assets.ReadFile("static/console.css")
	if err != nil {
		t.Fatalf("reading console.css: %v", err)
	}
	if !strings.Contains(string(css), "var(--color-border-control)") {
		t.Error("--color-border-control is defined and used by nothing")
	}
}

// TestAHintOpensBeneathItsHeading.
//
// **This is the check that was missing, and the reason a broken hint shipped.**
// TestHintsAreDisclosuresRatherThanTooltips asserted the mechanism — hints.js
// positions nothing, sets aria-expanded, wires idempotently — every word of
// which was true while the thing rendered wrongly.
//
// .panel__head is `display: flex; justify-content: space-between`, and the
// disclosure paragraph was a sibling of the title inside it. Closed, the button
// was distributed into the dead centre of the header band. Opened, the
// paragraph became a fourth item on the same row: beside the button rather than
// beneath it, jammed against the count, dragged up by the row's baseline
// alignment. Two correct rules; the pair was wrong.
//
// So this asserts the outcome. Nothing that expands may live inside the header
// band, whatever the stylesheet does.
func TestAHintOpensBeneathItsHeading(t *testing.T) {
	pages := []string{"static/access.html", "static/network.html",
		"static/bridges.html", "static/history.html"}

	var checked int
	for _, page := range pages {
		body, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		html := string(body)

		for _, head := range enclosedDivs(html, `<div class="panel__head">`) {
			checked++
			for _, banned := range []string{"hint__text", "panel__disclosure"} {
				if strings.Contains(head, banned) {
					t.Errorf("%s: a panel header contains %s, so opening it puts the "+
						"paragraph beside the title rather than beneath it", page, banned)
				}
			}
			// The button must be grouped with its title, or space-between
			// strands it in the middle of the band with nothing beside it.
			if strings.Contains(head, `class="hint"`) &&
				!strings.Contains(head, `class="panel__heading"`) {
				t.Errorf("%s: a hint button sits directly in a space-between header "+
					"band, which puts it in the centre of the panel", page)
			}
		}
	}
	if checked < len(pages) {
		t.Fatalf("only %d panel headers were examined; this check has gone blind", checked)
	}
	t.Logf("checked %d panel headers", checked)
}

// enclosedDivs returns the inner markup of every <div> that starts with open,
// matching nesting rather than the next closing tag. A regex would stop at the
// first </div>, which in a panel header is the one closing the heading group —
// and would then report success for exactly the markup this is checking for.
func enclosedDivs(html, open string) []string {
	var found []string
	for at := 0; ; {
		i := strings.Index(html[at:], open)
		if i < 0 {
			return found
		}
		i += at
		depth, j := 1, i+len(open)
		for depth > 0 && j < len(html) {
			switch {
			case strings.HasPrefix(html[j:], "<div"):
				depth++
				j += 4
			case strings.HasPrefix(html[j:], "</div>"):
				depth--
				j += 6
			default:
				j++
			}
		}
		found = append(found, html[i+len(open):j])
		at = j
	}
}

// TestOneNameMeansOneThing.
//
// .hint was two rules 600 lines apart: a block paragraph with padding and a
// top border, and a 20px circular button. Same class, same specificity, so the
// later one won and the console's "no voice frames yet" note was being drawn as
// a circle with its text spilling out.
//
// Neither rule was wrong. There is no way to catch that by reading either one.
func TestOneNameMeansOneThing(t *testing.T) {
	body, err := assets.ReadFile("static/console.css")
	if err != nil {
		t.Fatalf("reading console.css: %v", err)
	}
	css := string(body)

	// Rule openings of exactly `.hint {`, which is the button and must be
	// declared once.
	if n := len(regexp.MustCompile(`(?m)^\.hint \{`).FindAllString(css, -1)); n != 1 {
		t.Errorf("console.css opens `.hint {` %d times; one class cannot mean two things", n)
	}
	if strings.Contains(css, ".hint--neutral") {
		t.Error("`.hint--neutral` is back; a hint is a button and a note is a note")
	}

	// And the note that used to carry the button's name must still be styled.
	for _, name := range []string{".inline-note {", ".inline-note--neutral {"} {
		if !strings.Contains(css, name) {
			t.Errorf("console.css no longer defines %s", name)
		}
	}
}

// TestTheHintTargetIsBigEnough. tokens.css declares --target-min: 44px and the
// hint button was 20px square, which is the sort of thing a token exists to
// prevent and does not prevent on its own.
func TestTheHintTargetIsBigEnough(t *testing.T) {
	body, err := assets.ReadFile("static/console.css")
	if err != nil {
		t.Fatalf("reading console.css: %v", err)
	}
	css := string(body)

	at := strings.Index(css, ".hint::after {")
	if at < 0 {
		t.Fatal("the hint button has no target overlay, so its tap area is its 28px circle")
	}
	rule := css[at:]
	if end := strings.Index(rule, "}"); end >= 0 {
		rule = rule[:end]
	}
	for _, want := range []string{"var(--target-min)", "position: absolute"} {
		if !strings.Contains(rule, want) {
			t.Errorf("the hint's target overlay does not use %s", want)
		}
	}
}

// TestTheNavSaysWhenItsLinksLeadNowhere.
//
// The administration group offers four pages regardless of session. Nothing
// behind them discloses anything — each page keeps its form hidden until
// /api/config answers, and /api/config is behind requireSession — but four
// links that can only say "sign in" are a dead end presented as a destination.
//
// Also: five pages carried five copies of the same /api/session fetch, and only
// one of them could sign out. Copies drift.
func TestTheNavSaysWhenItsLinksLeadNowhere(t *testing.T) {
	nav, err := assets.ReadFile("static/nav.js")
	if err != nil {
		t.Fatalf("reading nav.js: %v", err)
	}
	src := string(nav)
	for _, want := range []string{"nav-admin-group", "nav__note", "/api/session", "/api/logout"} {
		if !strings.Contains(src, want) {
			t.Errorf("nav.js does not reference %s", want)
		}
	}

	for _, page := range []string{"static/index.html", "static/access.html",
		"static/network.html", "static/bridges.html", "static/history.html"} {
		body, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		html := string(body)
		if !strings.Contains(html, `src="/nav.js"`) {
			t.Errorf("%s has a nav and does not load nav.js", page)
		}
		if !strings.Contains(html, `id="nav-admin-group"`) {
			t.Errorf("%s does not name its administration group, so nav.js cannot mark it", page)
		}
	}

	// The copies must be gone, not merely joined by a sixth.
	for _, script := range []string{"static/console.js", "static/access.js",
		"static/network.js", "static/bridges.js", "static/history.js"} {
		body, err := assets.ReadFile(script)
		if err != nil {
			t.Fatalf("reading %s: %v", script, err)
		}
		if strings.Contains(string(body), "/api/session") {
			t.Errorf("%s still fetches the session itself; nav.js owns the topbar", script)
		}
	}
}

// TestTheJoinPageDoesNotAssumeOneNetwork.
//
// The page told every member to turn BrandMeister off. On this club that is
// wrong for a quarter of them, and a member who follows it loses a network they
// wanted — or ignores the step and gets a talkgroup collision instead, which
// presents as transmitting into silence with every log healthy.
func TestTheJoinPageDoesNotAssumeOneNetwork(t *testing.T) {
	body, err := assets.ReadFile("static/join.html")
	if err != nil {
		t.Fatalf("reading join.html: %v", err)
	}
	html := string(body)

	// The instruction may still be given — it is right for most members — but
	// not before asking.
	if strings.Contains(html, "turn off BrandMeister") {
		t.Error("the join page still instructs every member to disable BrandMeister")
	}
	for _, want := range []string{
		`name="other-networks"`,
		`id="step-generated"`,
		`id="prefix"`,
		`id="block"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("join.html has no %s, so a multi-network member has no path", want)
		}
	}

	script, err := assets.ReadFile("static/join.js")
	if err != nil {
		t.Fatalf("reading join.js: %v", err)
	}
	if !strings.Contains(string(script), "/api/join/config") {
		t.Error("join.js never asks for the generated block")
	}
}

// TestTheGeneratedBlockIsNotWrapped.
//
// A DMRGateway rule broken across two lines is a rule a member pastes as two
// lines, and the second one is a syntax error in a file that takes their
// hotspot off every network at once when it fails to parse.
func TestTheGeneratedBlockIsNotWrapped(t *testing.T) {
	css, err := assets.ReadFile("static/join.css")
	if err != nil {
		t.Fatalf("reading join.css: %v", err)
	}
	at := strings.Index(string(css), ".config {")
	if at < 0 {
		t.Fatal("join.css does not style the generated block")
	}
	rule := string(css)[at:]
	if end := strings.Index(rule, "}"); end >= 0 {
		rule = rule[:end]
	}
	// **`pre-wrap` contains `pre`.** The first version of this check tested for
	// the substring and passed against the wrapping value it exists to reject,
	// which is a test that cannot fail — the fault this file has caught twice
	// now in other places.
	if !strings.Contains(rule, "white-space: pre;") {
		t.Error("the generated block does not use white-space: pre, so a rule may wrap " +
			"into two lines and be pasted as two")
	}
	for _, wrapping := range []string{"pre-wrap", "pre-line", "normal"} {
		if strings.Contains(rule, "white-space: "+wrapping) {
			t.Errorf("the generated block uses white-space: %s, which wraps", wrapping)
		}
	}
	if !strings.Contains(rule, "overflow-x: auto") {
		t.Error("a long rule has nowhere to go but off the page")
	}
}

// TestHiddenMeansHidden.
//
// **The console hides by attribute and the attribute was being ignored.**
// `[hidden] { display: none }` is a user-agent rule, and any author rule with a
// class selector outranks it — so `.empty { display: flex }` meant the access
// page's "Loading" panel stayed on screen above the form it had already
// finished rendering. Every `hide()` in every page script was correct and none
// of them worked on an element whose class set a display mode.
//
// Found by an operator saying a page had never worked, not by anything here.
func TestHiddenMeansHidden(t *testing.T) {
	sheets := []string{"static/tokens.css", "static/console.css", "static/join.css"}

	var declared bool
	for _, sheet := range sheets {
		body, err := assets.ReadFile(sheet)
		if err != nil {
			t.Fatalf("reading %s: %v", sheet, err)
		}
		css := string(body)
		// **Anchored to the start of a line.** A plain substring search finds
		// the phrase inside the comment above the rule — which quotes the
		// user-agent declaration in order to explain the bug — and then
		// measures the comment instead of the CSS. That is the same fault as
		// the wrapping check: asserting something adjacent to the thing that
		// matters.
		loc := regexp.MustCompile(`(?m)^\[hidden\] \{`).FindStringIndex(css)
		if loc == nil {
			continue
		}
		rule := css[loc[0]:]
		if end := strings.Index(rule, "}"); end >= 0 {
			rule = rule[:end]
		}
		if !strings.Contains(rule, "display: none") {
			t.Errorf("%s declares [hidden] without display: none", sheet)
		}
		// Without !important the declaration loses to every class that sets a
		// display mode, which is the whole fault.
		if !strings.Contains(rule, "!important") {
			t.Errorf("%s declares [hidden] without !important, so a class with a "+
				"display rule still wins", sheet)
		}
		declared = true
	}
	if !declared {
		t.Error("no stylesheet makes the hidden attribute override a component's display")
	}

	// And the pages must still be using it, or the rule guards nothing.
	var users int
	for _, page := range []string{"static/access.html", "static/network.html",
		"static/bridges.html", "static/history.html", "static/join.html"} {
		body, err := assets.ReadFile(page)
		if err != nil {
			t.Fatalf("reading %s: %v", page, err)
		}
		if strings.Contains(string(body), " hidden>") {
			users++
		}
	}
	if users < 4 {
		t.Errorf("only %d pages hide by attribute; this check has gone blind", users)
	}
}

// TestTheConsoleShowsLinks.
//
// **An operator asked twice where to see a link and the answer was /healthz.**
// One opened, authenticated, carried audio between two servers, and no page
// showed that another network existed — which made a working link and a dead
// one look identical. ADR-0032 names this as required rather than optional.
func TestTheConsoleShowsLinks(t *testing.T) {
	page, err := assets.ReadFile("static/links.html")
	if err != nil {
		t.Fatalf("reading links.html: %v", err)
	}
	html := string(page)
	for _, want := range []string{`id="links"`, `src="/links.js"`, `id="signed-out"`} {
		if !strings.Contains(html, want) {
			t.Errorf("links.html has no %s", want)
		}
	}

	script, err := assets.ReadFile("static/links.js")
	if err != nil {
		t.Fatalf("reading links.js: %v", err)
	}
	src := string(script)
	if !strings.Contains(src, "/api/links") {
		t.Error("links.js never asks for the links")
	}
	// Both directions, always. One is not evidence of the other: a link that
	// has sent thousands and received none is working perfectly on a quiet
	// network, or is unauthenticated at the far end.
	for _, want := range []string{"l.sent", "l.received", "l.rejected"} {
		if !strings.Contains(src, want) {
			t.Errorf("links.js does not show %s, so a one-way link looks like a working one", want)
		}
	}

	// And every admin page must offer the page, or it is unreachable.
	for _, name := range []string{"static/access.html", "static/network.html",
		"static/bridges.html", "static/history.html", "static/links.html"} {
		body, err := assets.ReadFile(name)
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if !strings.Contains(string(body), `href="/links"`) {
			t.Errorf("%s does not link to the links page", name)
		}
	}
}

// TestAPeeringIsAgreedInTwoSteps.
//
// ADR-0032: a peering is agreed by two people. **One click is not agreement.**
// The first step reads the invitation and shows who is asking; the second
// writes configuration. A page that wrote a link the moment a token was pasted
// would make consent a formality, which is the thing this whole flow exists to
// stop being.
func TestAPeeringIsAgreedInTwoSteps(t *testing.T) {
	page, err := assets.ReadFile("static/links.html")
	if err != nil {
		t.Fatalf("reading links.html: %v", err)
	}
	html := string(page)
	for _, want := range []string{`id="accept"`, `id="accept-confirm"`, `id="accept-go"`} {
		if !strings.Contains(html, want) {
			t.Errorf("links.html has no %s, so accepting is a single click", want)
		}
	}
	if !strings.Contains(html, `id="accept-confirm" hidden`) {
		t.Error("the confirmation step is visible before an invitation is read")
	}

	script, err := assets.ReadFile("static/links.js")
	if err != nil {
		t.Fatalf("reading links.js: %v", err)
	}
	src := string(script)
	if !strings.Contains(src, "confirm: confirm") {
		t.Error("links.js does not send a confirmation flag, so the server cannot refuse an " +
			"unconfirmed peering")
	}
	if !strings.Contains(src, "/api/links/offer") || !strings.Contains(src, "/api/links/accept") {
		t.Error("links.js does not use both halves of the peering flow")
	}
}

// TestThePassphraseIsNotInTheInvitationBox.
//
// The invitation goes by email. The passphrase does not, and the page has to
// say so where somebody about to send both in one message will read it.
func TestThePassphraseIsNotInTheInvitationBox(t *testing.T) {
	page, err := assets.ReadFile("static/links.html")
	if err != nil {
		t.Fatalf("reading links.html: %v", err)
	}
	html := string(page)
	if !strings.Contains(html, "send this by email") {
		t.Error("the page does not say the invitation is the emailable half")
	}
	if !strings.Contains(html, "send this another way") {
		t.Error("the page does not say the passphrase travels separately")
	}
	// A password field, because a passphrase pasted into a page on a shared
	// screen is one somebody else read.
	if !strings.Contains(html, `id="accept-pass" type="password"`) {
		t.Error("the passphrase is typed into a plain text field")
	}
}
