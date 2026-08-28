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

	planes := map[string]rgb{
		"page background": background,
		"panel surface":   surface,
		"panel header":    header,
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
