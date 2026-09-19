package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestADongleThatMovedRecoversWithoutAHuman checks the deployment files that
// put AMBEserver back on a dongle that moved (0416).
//
// **AMBEserver does not notice its dongle leaving.** It keeps running, keeps
// 2460 bound, answers nothing, and systemctl calls it active; QSP reports
// failing and retries forever. Production ran three days that way after the
// dongle came back on a different ttyUSB, with Zello silent both ways.
//
// None of this can run here: there is no udev and no systemd in the test
// environment. So as with the other deployment tests, the rows check the joins
// that break silently — a rule naming a unit that was renamed, a guard that
// cannot act on the state the unit is actually in, a documented symptom that no
// longer matches what the program prints.
func TestADongleThatMovedRecoversWithoutAHuman(t *testing.T) {
	const (
		rulePath   = "deploy/udev/99-ambe-dongle-restart.rules"
		latency    = "deploy/udev/99-ambe-dongle-latency.rules"
		replugUnit = "deploy/systemd/ambeserver-replug.service"
		ambeUnit   = "deploy/systemd/ambeserver.service"
		zelloDoc   = "docs/ZELLO.md"
		dockerDoc  = "deploy/docker/README.md"
	)

	read := func(t *testing.T, name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		return string(b)
	}

	rule := read(t, rulePath)
	// **Directives only, not the comments explaining them.** These files carry
	// their reasoning inline, and the reasoning names the things deliberately
	// not used — try-restart, [Install] — so a whole-file grep fails on prose
	// that is correct. Two rows failed that way before this.
	replug := directives(read(t, replugUnit))

	tests := []struct {
		name  string
		check func() error
	}{
		{"the rule asks for a unit this repository ships", func() error {
			want, err := wantedUnit(rule)
			if err != nil {
				return err
			}
			path := filepath.Join(repoRoot, "deploy/systemd", want)
			if _, err := os.Stat(path); err != nil {
				return fmt.Errorf("the rule wants %s and deploy/systemd has no such unit: %w", want, err)
			}
			if want != filepath.Base(replugUnit) {
				return fmt.Errorf("the rule wants %s, not %s", want, filepath.Base(replugUnit))
			}
			return nil
		}},
		{"the rule signals systemd rather than running systemctl itself", func() error {
			// systemctl in a udev RUN+= is discouraged upstream and can hang a
			// udev worker until it is killed. The first draft of this patch did
			// exactly that.
			for _, f := range []string{rulePath, latency} {
				body := read(t, f)
				for _, line := range strings.Split(body, "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "#") {
						continue
					}
					if strings.Contains(line, "RUN+=") && strings.Contains(line, "systemctl") {
						return fmt.Errorf("%s runs systemctl from a udev rule", f)
					}
				}
			}
			if !strings.Contains(rule, `TAG+="systemd"`) {
				return errors.New("the rule does not tag the device for systemd, so no .device unit exists to want the unit")
			}
			return nil
		}},
		{"the rule matches the same devices as the latency rule", func() error {
			body := read(t, latency)
			for _, key := range []string{`SUBSYSTEM=="usb-serial"`, `DRIVER=="ftdi_sio"`} {
				if !strings.Contains(body, key) {
					return fmt.Errorf("the latency rule no longer matches on %s; this rule was written to follow it", key)
				}
				if !strings.Contains(rule, key) {
					return fmt.Errorf("the restart rule does not match on %s, so the two rules fire on different devices", key)
				}
			}
			if !strings.Contains(rule, `ACTION=="add"`) {
				return errors.New("the restart rule does not fire on add, which is when the dongle comes back")
			}
			return nil
		}},
		{"the unit acts on the AMBEserver unit this repository ships", func() error {
			if _, err := os.Stat(filepath.Join(repoRoot, ambeUnit)); err != nil {
				return fmt.Errorf("stat %s: %w", ambeUnit, err)
			}
			if !strings.Contains(replug, filepath.Base(ambeUnit)) {
				return fmt.Errorf("%s names no %s", replugUnit, filepath.Base(ambeUnit))
			}
			return nil
		}},
		{"the unit can act on a failed unit, not only a running one", func() error {
			// try-restart does nothing to a failed unit, and AMBEserver reaches
			// that state whenever the dongle is absent at boot: five restarts
			// trip its start limit. A guard that cannot recover from failed is
			// the bug this file is meant to fix.
			if strings.Contains(replug, "try-restart") {
				return errors.New("try-restart does nothing to a failed unit; a replug after a tripped start limit would be ignored")
			}
			for _, want := range []string{"is-enabled", "reset-failed", "restart"} {
				if !strings.Contains(replug, want) {
					return fmt.Errorf("%s does not use %s", replugUnit, want)
				}
			}
			return nil
		}},
		{"the unit leaves an install that disabled AMBEserver alone", func() error {
			if !strings.Contains(replug, "is-enabled --quiet ambeserver.service || exit 0") {
				return errors.New("the guard does not exit when AMBEserver is disabled; plugging a dongle into a machine that disabled it would start a vocoder service")
			}
			return nil
		}},
		{"only udev starts the unit", func() error {
			if strings.Contains(replug, "[Install]") {
				return errors.New(replugUnit + " has an [Install] section; it is started by udev through the device unit and nothing else should enable it")
			}
			if !strings.Contains(replug, "Type=oneshot") {
				return errors.New(replugUnit + " is not Type=oneshot")
			}
			return nil
		}},
		{"the documented symptom is what the program actually prints", func() error {
			// **Read from the docs into the code, not the other way round.** The
			// first version of this row searched the source for notes containing
			// "not reachable" and required the docs to quote them; rewording the
			// source left that set empty and the row passed having checked
			// nothing. So the quotes in the troubleshooting section are the
			// input, and each has to exist as a note the program can print.
			doc := read(t, zelloDoc)
			quoted, err := quotedWarnings(doc, "When Zello goes quiet both ways")
			if err != nil {
				return err
			}
			if len(quoted) < 2 {
				return fmt.Errorf("the troubleshooting section quotes %d warning(s); it is about a failure that prints two, one per direction", len(quoted))
			}
			var notes []string
			for _, f := range []string{"internal/vocoderlink/vocoderlink.go", "internal/vocoderlink/outbound.go"} {
				notes = append(notes, allNotes(read(t, f))...)
			}
			for _, q := range quoted {
				if !slices.Contains(notes, q) {
					return fmt.Errorf("docs/ZELLO.md quotes %q, which internal/vocoderlink no longer prints", q)
				}
			}
			return nil
		}},
		{"every install list that names AMBEserver names the recovery too", func() error {
			for _, f := range []string{zelloDoc, dockerDoc} {
				body := read(t, f)
				if !strings.Contains(body, "ambeserver.service") && !strings.Contains(body, "AMBEserver") {
					continue
				}
				if !strings.Contains(body, "ambeserver-replug.service") {
					return fmt.Errorf("%s sets up AMBEserver without the replug recovery", f)
				}
			}
			return nil
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.check(); err != nil {
				t.Error(err)
			}
		})
	}
}

var wantsRe = regexp.MustCompile(`ENV\{SYSTEMD_WANTS\}\+?="([^"]+)"`)

// wantedUnit returns the unit a udev rule asks systemd to start.
func wantedUnit(rule string) (string, error) {
	m := wantsRe.FindStringSubmatch(rule)
	if m == nil {
		return "", errors.New("the rule sets no SYSTEMD_WANTS, so it asks for nothing")
	}
	return m[1], nil
}

// directives returns a unit or rules file without its comment lines.
func directives(body string) string {
	var keep []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		keep = append(keep, line)
	}
	return strings.Join(keep, "\n")
}

var noteRe = regexp.MustCompile(`c\.note\("([^"]+)"\)`)

// allNotes returns every literal note string a source file can print.
func allNotes(src string) []string {
	var out []string
	for _, m := range noteRe.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

var docQuoteRe = regexp.MustCompile(`\.\.\. "([^"]+)"`)

// quotedWarnings returns the warning texts a documentation section quotes.
func quotedWarnings(doc, heading string) ([]string, error) {
	i := strings.Index(doc, "## "+heading)
	if i < 0 {
		return nil, fmt.Errorf("docs/ZELLO.md has no section %q", heading)
	}
	body := doc[i+len(heading):]
	if j := strings.Index(body, "\n## "); j >= 0 {
		body = body[:j]
	}
	var out []string
	for _, m := range docQuoteRe.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out, nil
}

// TestWantedUnitReadsTheRule keeps the rule reader honest: a rule that asks for
// nothing must be an error rather than an empty pass.
func TestWantedUnitReadsTheRule(t *testing.T) {
	tests := []struct {
		name, rule, want string
		wantErr          bool
	}{
		{name: "appending form", rule: `ACTION=="add", TAG+="systemd", ENV{SYSTEMD_WANTS}+="a.service"`, want: "a.service"},
		{name: "assigning form", rule: `ENV{SYSTEMD_WANTS}="b.service"`, want: "b.service"},
		{name: "no wants is an error", rule: `ACTION=="add", TAG+="systemd"`, wantErr: true},
		{name: "a comment mentioning it is not a rule", rule: `# ENV{SYSTEMD_WANTS}+="c.service" is how this works`, want: "c.service"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := wantedUnit(tc.rule)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("wantedUnit(%q) = %q, want an error", tc.rule, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("wantedUnit(%q): %v", tc.rule, err)
			}
			if got != tc.want {
				t.Errorf("wantedUnit(%q) = %q, want %q", tc.rule, got, tc.want)
			}
		})
	}
}
