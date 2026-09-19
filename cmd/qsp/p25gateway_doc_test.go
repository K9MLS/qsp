package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/p25link"
)

// TestTheP25GatewayGuideMatchesTheProgram checks the operator guide for
// connecting a P25 gateway (0417).
//
// **The guide exists because an hour went into a failure that was not QSP's**:
// a Pi-Star sending to the wrong machine, then to a hostname that could not be
// reached from inside the network. What it tells an operator to look for — the
// poll interval on a capture, the log line that means success, the health
// summary that means nothing has linked — all come from the program, and all of
// it goes stale silently. A guide that names a log line QSP no longer prints
// sends a stranger looking for a string that does not exist.
//
// Each row reads its truth from the code: the interval and the miss count from
// p25link's constants, the strings from p25link's sources, the absence of any
// password from the P25 configuration itself.
func TestTheP25GatewayGuideMatchesTheProgram(t *testing.T) {
	const guide = "docs/P25-GATEWAY.md"

	read := func(t *testing.T, name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		return string(b)
	}

	// **Normalised, because Markdown wraps.** A row looking for a phrase that
	// happens to straddle a line break fails on the line width rather than on
	// the claim, which is a test that punishes editing rather than drift.
	doc := collapse(read(t, guide))

	tests := []struct {
		name  string
		check func() error
	}{
		{"the poll interval is the one a gateway actually uses", func() error {
			secs := int(p25link.PollInterval / time.Second)
			want := fmt.Sprintf("every %d seconds", secs)
			if !strings.Contains(doc, want) {
				return fmt.Errorf("the guide does not say %q; a reader timing polls on a capture would measure something else", want)
			}
			return nil
		}},
		{"the miss count before a gateway is forgotten matches the code", func() error {
			want := fmt.Sprintf("after %s missed polls", numberWord(p25link.MissedPollsBeforeGone))
			if !strings.Contains(doc, want) {
				return fmt.Errorf("the guide does not say %q", want)
			}
			return nil
		}},
		{"the success line is what the program logs", func() error {
			src := read(t, "internal/p25link/serve.go")
			msgs := logMessages(src)
			var registered string
			for _, m := range msgs {
				if strings.Contains(m, "registered") {
					registered = m
					break
				}
			}
			if registered == "" {
				return errors.New("internal/p25link/serve.go logs nothing about a gateway registering; the guide tells operators to grep for it")
			}
			if !strings.Contains(doc, registered) {
				return fmt.Errorf("the guide does not quote %q, which is what a linked gateway logs", registered)
			}
			return nil
		}},
		{"the health summary for an empty reflector is quoted as printed", func() error {
			src := read(t, "internal/p25link/health.go")
			m := regexp.MustCompile(`health\.Healthy\("([^"]+)"\)`).FindStringSubmatch(src)
			if m == nil {
				return errors.New("internal/p25link/health.go no longer has a healthy summary to quote")
			}
			if !strings.Contains(doc, m[1]) {
				return fmt.Errorf("the guide does not quote %q, the summary shown until a gateway links", m[1])
			}
			return nil
		}},
		{"the guide says a callsign is a claim while the protocol has no credential", func() error {
			// If P25 ever gains a shared secret, this row fails and the guide
			// has to be rewritten rather than left saying authentication is
			// impossible.
			cfg := read(t, "internal/config/config.go")
			p25 := structBody(cfg, "P25")
			if p25 == "" {
				return errors.New("internal/config/config.go has no P25 struct")
			}
			for _, credential := range []string{"Password", "Passphrase", "Secret", "Token"} {
				if strings.Contains(p25, credential) {
					return fmt.Errorf("the P25 configuration now has a %s; the guide still tells operators a callsign is all there is", credential)
				}
			}
			if !strings.Contains(doc, "claim, not a credential") {
				return errors.New("the guide does not say a callsign is a claim rather than a credential")
			}
			return nil
		}},
		{"the guide does not promise talkgroup routing", func() error {
			// The flat relay is the known limit. A guide that stops saying so
			// while voice still fans out to everybody is the dishonest kind of
			// documentation this project treats as a defect.
			src := read(t, "internal/p25link/serve.go")
			routed := strings.Contains(src, "targets by talkgroup") || strings.Contains(src, "routeByTalkgroup")
			saysFlat := strings.Contains(doc, "flat reflector")
			if routed && saysFlat {
				return errors.New("voice now routes by talkgroup and the guide still calls it a flat reflector")
			}
			if !routed && !saysFlat {
				return errors.New("voice still relays to every gateway and the guide no longer says so")
			}
			return nil
		}},
		{"the allow list's hazard when empty is stated", func() error {
			cfg := read(t, "internal/config/config.go")
			if !strings.Contains(cfg, "Empty answers every gateway") {
				return errors.New("internal/config no longer says an empty allow list answers everybody; check whether the guide's warning is still true")
			}
			if !strings.Contains(doc, "empty list answers every gateway") {
				return errors.New("the guide does not warn that an empty allow list answers every gateway")
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

// collapse turns every run of whitespace into one space, so a phrase can be
// found regardless of where the text was wrapped.
func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

var logMsgRe = regexp.MustCompile(`\.(?:Info|Warn|Error)\("([^"]+)"`)

// logMessages returns the literal log messages in a source file.
func logMessages(src string) []string {
	var out []string
	for _, m := range logMsgRe.FindAllStringSubmatch(src, -1) {
		out = append(out, m[1])
	}
	return out
}

// structBody returns the body of a named struct declaration.
func structBody(src, name string) string {
	start := strings.Index(src, "type "+name+" struct {")
	if start < 0 {
		return ""
	}
	rest := src[start:]
	end := strings.Index(rest, "\n}")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

func numberWord(n int) string {
	words := map[int]string{1: "one", 2: "two", 3: "three", 4: "four", 5: "five"}
	if w, ok := words[n]; ok {
		return w
	}
	return fmt.Sprintf("%d", n)
}

// TestStructBodyStopsAtTheStruct keeps the configuration reader from spilling
// into the next declaration, which would let a neighbouring field's name
// satisfy — or wrongly fail — the credential row above.
func TestStructBodyStopsAtTheStruct(t *testing.T) {
	src := "type A struct {\n\tX string\n}\n\ntype B struct {\n\tPassword string\n}\n"
	tests := []struct {
		name, want, notWant string
		structName          string
		wantEmpty           bool
	}{
		{name: "its own fields", structName: "A", want: "X string", notWant: "Password"},
		{name: "the later struct", structName: "B", want: "Password", notWant: "X string"},
		{name: "a missing struct is empty", structName: "C", wantEmpty: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := structBody(src, tc.structName)
			if tc.wantEmpty {
				if got != "" {
					t.Fatalf("structBody(%q) = %q, want empty", tc.structName, got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("structBody(%q) = %q, want it to contain %q", tc.structName, got, tc.want)
			}
			if strings.Contains(got, tc.notWant) {
				t.Errorf("structBody(%q) = %q, want it not to contain %q", tc.structName, got, tc.notWant)
			}
		})
	}
}
