// Documentation-accuracy checks.
//
// This project has shipped the same defect four times: prose describing a build
// that no longer exists. The console claimed no protocol, routing or scheduling
// code was present long after all three were written; 0.1.1 fixed the console
// and left the same claim in two package comments, ARCHITECTURE.md and
// docs/architecture/testing.md.
//
// A check cannot know whether a sentence is true. It can know whether a
// sentence contradicts something the program can enumerate, and that covers
// every instance found so far. Three things are enumerable here:
//
//   - the request patterns the server registers,
//   - the paths that exist in the repository,
//   - the subsystems the health registry reports, and their status.
//
// Per PROJECT_MEMORY §7, this is a Go test rather than a script: verification
// scripts in this project have been less trustworthy than the code they check.
package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/server"
)

// repoRoot is this package's directory walked back to the module root.
const repoRoot = "../.."

// frozenMarker exempts a document from the accuracy checks.
//
// Some documents are dated artefacts: BLUEPRINT.md v0.4 records what was
// believed before any code was written, and rewriting it would destroy the
// record rather than correct it. A frozen document must say so in its own first
// twenty lines, in this exact form, so that a reader meets the disclaimer at the
// same moment the checker does.
//
// The marker requires a reason. Silencing a check should cost a sentence that
// shows up in the diff and that a reviewer can disagree with.
var frozenMarker = regexp.MustCompile(`(?m)^<!-- doc-accuracy: frozen — .+ -->$`)

// docFiles returns every Markdown file in the repository, and every Go file
// carrying a package comment, that is subject to these checks.
func docFiles(t *testing.T) map[string]string {
	t.Helper()

	files := make(map[string]string)
	err := filepath.WalkDir(repoRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".md" && ext != ".go" {
			return nil
		}
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		body := string(b)
		if frozenMarker.MatchString(head(body, 20)) {
			return nil
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = body
		return nil
	})
	if err != nil {
		t.Fatalf("walking the repository: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no documentation files found; is repoRoot still correct?")
	}
	return files
}

func head(s string, lines int) string {
	parts := strings.SplitN(s, "\n", lines+1)
	if len(parts) > lines {
		parts = parts[:lines]
	}
	return strings.Join(parts, "\n")
}

// codeSpan matches text between backticks, which is how both Markdown and Go
// doc comments mark a literal.
var codeSpan = regexp.MustCompile("`([^`\n]+)`")

// -----------------------------------------------------------------------------
// Check 1 — the documented endpoint inventory matches the registered routes.
// -----------------------------------------------------------------------------

// endpointInventories are documents that enumerate QSP's HTTP endpoints. A
// document listed here must name every registered endpoint and no others.
//
// SECURITY.md is the one that matters: a reader deciding whether to expose an
// instance is entitled to a complete list of what that exposes. It omitted
// GET /api/peers, which returns callsigns, radio IDs and source addresses.
var endpointInventories = []string{"SECURITY.md"}

func TestDocumentedEndpointsMatchRegisteredRoutes(t *testing.T) {
	registered := make(map[string]bool)
	for _, p := range server.APIPaths() {
		registered[p] = true
	}

	files := docFiles(t)
	for _, name := range endpointInventories {
		body, ok := files[name]
		if !ok {
			t.Errorf("%s is listed as an endpoint inventory but was not found", name)
			continue
		}

		documented := make(map[string]bool)
		for _, m := range codeSpan.FindAllStringSubmatch(body, -1) {
			span := m[1]
			// An endpoint is an absolute path with no spaces. Deployment prose
			// mentions settings and hosts too; those are not paths.
			if !strings.HasPrefix(span, "/") || strings.ContainsAny(span, " \t") {
				continue
			}
			documented[span] = true
		}

		for path := range registered {
			if !documented[path] {
				t.Errorf("%s does not mention %s, which the server registers", name, path)
			}
		}
		for path := range documented {
			if !registered[path] {
				t.Errorf("%s documents %s, which the server does not register", name, path)
			}
		}
	}
}

// -----------------------------------------------------------------------------
// Check 2 — paths named in documentation exist, and are as full as claimed.
// -----------------------------------------------------------------------------

// topLevel is the set of repository entries a documented path may start with.
// Restricting to these keeps the check away from things that merely look like
// paths — linux/arm64, GOOS/GOARCH, and the INI field names in BLUEPRINT.md.
func topLevel(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(repoRoot)
	if err != nil {
		t.Fatalf("reading repository root: %v", err)
	}
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		set[e.Name()] = true
	}
	return set
}

// allowPath exempts a single path from TestDocumentedPathsExist.
//
// Decision records legitimately name things that do not exist yet: ADR-0009
// decides where the Zello sidecar will live before phase 6 writes it. The
// allowance is per-path and requires a reason, so it cannot be used to wave
// through a link that has simply rotted.
var allowPath = regexp.MustCompile(`(?m)^<!-- doc-accuracy: allow-path (\S+) — .+ -->$`)

func allowedPaths(body string) map[string]bool {
	allowed := make(map[string]bool)
	for _, m := range allowPath.FindAllStringSubmatch(body, -1) {
		allowed[strings.TrimSuffix(m[1], "/")] = true
	}
	return allowed
}

func TestDocumentedPathsExist(t *testing.T) {
	roots := topLevel(t)

	for name, body := range docFiles(t) {
		allowed := allowedPaths(body)
		for _, m := range codeSpan.FindAllStringSubmatch(body, -1) {
			span := strings.TrimSuffix(m[1], "/")
			if !strings.Contains(span, "/") || strings.ContainsAny(span, " \t") {
				continue
			}
			if strings.Contains(span, "://") || strings.HasPrefix(span, "/") {
				continue
			}
			first, _, _ := strings.Cut(span, "/")
			if !roots[first] || allowed[span] {
				continue
			}
			if _, err := os.Stat(filepath.Join(repoRoot, span)); err != nil {
				t.Errorf("%s names %s, which does not exist", name, span)
			}
		}
	}
}

// emptinessClaim matches a sentence asserting that something contains nothing.
//
// docs/architecture/testing.md carried "testdata/hbp/ and testdata/p25/ are
// empty by design" for as long as three captures sat committed inside them.
var emptinessClaim = regexp.MustCompile(`(?i)\b(?:are|is)\s+empty\b`)

func TestEmptinessClaimsAreTrue(t *testing.T) {
	roots := topLevel(t)

	for name, body := range docFiles(t) {
		for _, line := range strings.Split(body, "\n") {
			if !emptinessClaim.MatchString(line) {
				continue
			}
			for _, m := range codeSpan.FindAllStringSubmatch(line, -1) {
				span := strings.TrimSuffix(m[1], "/")
				first, _, _ := strings.Cut(span, "/")
				if !roots[first] {
					continue
				}
				entries, err := os.ReadDir(filepath.Join(repoRoot, span))
				if err != nil {
					continue // covered by TestDocumentedPathsExist
				}
				if len(entries) > 0 {
					t.Errorf("%s calls %s empty, but it holds %d entries", name, span, len(entries))
				}
			}
		}
	}
}

// -----------------------------------------------------------------------------
// Check 3 — nothing claims a built subsystem is absent.
// -----------------------------------------------------------------------------

// absenceClaim matches the ways this project has phrased "we have not built
// this". Every historical instance of the defect used one of these.
var absenceClaim = regexp.MustCompile(`(?i)\b(?:` +
	`not (?:yet )?(?:built|implemented|present|written)` +
	`|no code` +
	`|is not implemented` +
	`|are not present` +
	`|later phases?` +
	`|a later phase` +
	`|contains the platform only` +
	`)\b`)

// subsystemAliases maps a health-check name to the words documentation uses for
// it. A check name is terse; prose is not.
var subsystemAliases = map[string][]string{
	"network":   {"udp listener"},
	"peers":     {"peer lifecycle"},
	"routing":   {"routing core", "routing"},
	"scheduler": {"scheduler", "scheduling"},
	"database":  {"storage"},
}

// builtSubsystems returns every registered subsystem that is not in
// unbuiltSubsystems — that is, everything with an implementation, whether or
// not this configuration has it switched on.
//
// Runtime status is the wrong source here: a disabled DMR listener also reports
// unavailable, which would quietly excuse documentation claiming the peer
// lifecycle was never written.
func builtSubsystems(t *testing.T) []string {
	t.Helper()

	a, err := build(context.Background(), testConfig(), logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	if a.health == nil {
		t.Fatal("the app holds no health registry")
	}

	unbuiltNames := make(map[string]bool, len(unbuiltSubsystems))
	for _, s := range unbuiltSubsystems {
		unbuiltNames[s.name] = true
	}

	var built []string
	for _, name := range a.health.Names() {
		if unbuiltNames[name] {
			continue
		}
		built = append(built, name)
	}
	if len(built) == 0 {
		t.Fatal("no subsystem reports as built; the registry is not being populated")
	}
	return built
}

func TestNothingClaimsABuiltSubsystemIsAbsent(t *testing.T) {
	built := builtSubsystems(t)

	// "process" is the running binary itself. No document claims its absence,
	// and the word appears constantly in ordinary prose.
	terms := make(map[string]string) // lowercase term -> subsystem
	for _, name := range built {
		if name == "process" {
			continue
		}
		terms[name] = name
		for _, alias := range subsystemAliases[name] {
			terms[alias] = name
		}
	}

	for file, body := range docFiles(t) {
		for i, line := range strings.Split(body, "\n") {
			if !absenceClaim.MatchString(line) {
				continue
			}
			lower := strings.ToLower(line)
			reported := make(map[string]bool)
			for term, subsystem := range terms {
				if reported[subsystem] || !strings.Contains(lower, term) {
					continue
				}
				reported[subsystem] = true
				t.Errorf("%s:%d claims %q is absent, but the health registry reports it as built:\n\t%s",
					file, i+1, subsystem, strings.TrimSpace(line))
			}
		}
	}
}

// TestUnbuiltSubsystemsSayWhichPhaseBringsThem guards the other direction: a
// subsystem with no implementation must tell the operator when it arrives,
// because "unavailable" alone reads like a fault rather than a roadmap entry.
//
// Subsystems that exist but are switched off are excluded: they report the
// setting that would enable them, which is a different and equally honest
// statement. app_test.go covers that case.
func TestUnbuiltSubsystemsSayWhichPhaseBringsThem(t *testing.T) {
	a, err := build(context.Background(), testConfig(), logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	unbuiltNames := make(map[string]bool, len(unbuiltSubsystems))
	for _, s := range unbuiltSubsystems {
		unbuiltNames[s.name] = true
	}

	for _, res := range a.health.Run(context.Background()).Results {
		if !unbuiltNames[res.Name] {
			continue
		}
		if res.Status != health.StatusUnavailable {
			t.Errorf("subsystem %q has no implementation but reports %q", res.Name, res.Status)
		}
		if !strings.Contains(strings.ToLower(res.Summary), "phase") {
			t.Errorf("subsystem %q is unbuilt but does not say which phase brings it: %q",
				res.Name, res.Summary)
		}
	}
}
