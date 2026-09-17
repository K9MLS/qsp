package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// TestTheReadmeTellsAnOperatorWhatTheProgramDoes ties what the README tells an
// operator to type, and where to browse, to the program that will answer.
//
// **The README is the first thing a stranger reads, and it said the console was
// on 127.0.0.1:8080** directly under the Docker install, whose first run writes
// 0.0.0.0:8080. Both halves were true of something; together they told a Docker
// operator the wrong thing about who could reach their console. The existing
// gates check that paths exist and endpoints match routes, and none of them
// reads a port, an address or a variable name.
//
// **Every expected value is read from the code, never written here.** A row that
// said "62031" would agree with a README that said 62031 after the listener
// moved, which is a test encoding the same assumption as the thing it checks.
// And each claim is looked for in its own section, because the defect was a true
// statement in the wrong place: "appears somewhere in the README" would have
// passed it.
func TestTheReadmeTellsAnOperatorWhatTheProgramDoes(t *testing.T) {
	const (
		onAir   = "On the air in minutes"
		without = "Without Docker"
	)

	starter := starterConfig("/var/lib/qsp/peer-password", []string{"3132910"})
	builtIn := config.Default()

	dmrPort := portOf(t, "the first run's DMR listener", starter.DMR.ListenAddress)
	consolePort := portOf(t, "the first run's console", starter.Server.ListenAddress)

	listener := "DMR listener off"
	if builtIn.DMR.Enabled {
		listener = "DMR listener on"
	}

	tests := []struct {
		name    string
		section string
		want    []string
		notWant []string
	}{
		{
			name:    "the Docker steps name the port the first run listens on",
			section: onAir,
			want:    []string{dmrPort},
		},
		{
			name:    "the Docker steps browse to the port the first run's console uses",
			section: onAir,
			want:    []string{":" + consolePort},
		},
		{
			// Only meaningful while the two differ, which is the point of the
			// first run's setting; if they ever agree, nothing is being claimed.
			name:    "the Docker steps do not give the source build's console address",
			section: onAir,
			notWant: differing(builtIn.Server.ListenAddress, starter.Server.ListenAddress),
		},
		{
			name:    "the Docker steps name both variables a first run requires",
			section: onAir,
			want:    []string{peerPasswordEnv, allowedPeersEnv},
		},
		{
			name:    "the source build gives the built-in console address",
			section: without,
			want:    []string{builtIn.Server.ListenAddress},
		},
		{
			name:    "the source build says whether the listener starts",
			section: without,
			want:    []string{listener},
		},
	}

	readme, err := readReadme()
	if err != nil {
		t.Fatalf("%v", err)
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, err := readmeSection(readme, tc.section)
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(body, w) {
					t.Errorf("README section %q does not say %q, which the program uses", tc.section, w)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(body, nw) {
					t.Errorf("README section %q says %q, which is not what this install does", tc.section, nw)
				}
			}
		})
	}
}

// TestReadmeSectionFindsOnlyItsOwnSection keeps the helper honest. A section
// reader that ran to the end of the file would let a claim in one section
// satisfy a row about another, which is the defect the test above exists for.
func TestReadmeSectionFindsOnlyItsOwnSection(t *testing.T) {
	doc := "# T\n\n## One\n\nalpha\n\n### Sub\n\nbeta\n\n## Two\n\ngamma\n"

	tests := []struct {
		name    string
		heading string
		want    string
		notWant string
		wantErr bool
	}{
		{name: "includes its subsections", heading: "One", want: "beta", notWant: "gamma"},
		{name: "stops at the next section", heading: "Two", want: "gamma", notWant: "alpha"},
		{name: "a subsection is not a section", heading: "Sub", wantErr: true},
		{name: "a missing section is an error, not an empty pass", heading: "Three", wantErr: true},
		{name: "a heading is matched whole, not as a prefix", heading: "On", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, err := readmeSection(doc, tc.heading)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("readmeSection(%q) = %q, want an error", tc.heading, body)
				}
				return
			}
			if err != nil {
				t.Fatalf("readmeSection(%q): %v", tc.heading, err)
			}
			if !strings.Contains(body, tc.want) {
				t.Errorf("section %q = %q, want it to contain %q", tc.heading, body, tc.want)
			}
			if strings.Contains(body, tc.notWant) {
				t.Errorf("section %q = %q, want it not to contain %q", tc.heading, body, tc.notWant)
			}
		})
	}
}

func readReadme() (string, error) {
	path := filepath.Join(repoRoot, "README.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(b), nil
}

// readmeSection returns the body of the level-two section with this exact
// heading, subsections included, up to the next level-two heading.
func readmeSection(doc, heading string) (string, error) {
	lines := strings.Split(doc, "\n")
	start := -1
	for i, line := range lines {
		if line == "## "+heading {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("README has no section %q; a renamed heading disarms every row that names it", heading)
	}
	end := len(lines)
	for i := start; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n"), nil
}

func portOf(t *testing.T, what, addr string) string {
	t.Helper()
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("%s address %q: %v", what, addr, err)
	}
	return port
}

// differing returns a as a forbidden string only when it differs from b.
func differing(a, b string) []string {
	if a == b {
		return nil
	}
	return []string{a}
}
