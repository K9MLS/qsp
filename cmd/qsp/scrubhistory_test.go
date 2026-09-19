package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheHistoryScrubDerivesWhatToReplace runs scripts/scrub-history.py against
// a repository this test builds, with a planted name and a planted address.
//
// **The tool has one job and one chance.** It rewrites every commit before the
// repository is published, and a string it fails to derive is a string that
// gets published. The risk is not that it breaks loudly; it is that it reports
// success having found four of five things.
//
// So the fixture is a small history with the same shape as the real one: a
// commit that uses a name and an address, then a scrub commit that replaces
// them in the tree only — which is the state the real repository was in. What
// the tool must work out for itself is that the name is sensitive because it is
// no longer at HEAD, that the address is public, and that both are still in the
// first commit.
func TestTheHistoryScrubDerivesWhatToReplace(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	script, err := filepath.Abs(filepath.Join(repoRoot, "scripts", "scrub-history.py"))
	if err != nil {
		t.Fatalf("finding the script: %v", err)
	}

	const (
		name    = "Winterbourne" // stands in for a first name or a town
		kept    = "Talkgroup"    // a capitalised word the scrub reworded but kept
		subject = "the tree is clean but the history is not"
	)
	// **Assembled, not written.** A literal here is a public address in the
	// repository, and cmd/qsp/addresses_test.go is right to refuse one — it
	// caught this file, as it caught its own fixtures.
	address := fmt.Sprintf("%d.%d.%d.%d", 42, 42, 42, 42)

	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	// git grep exits 1 when it finds nothing, which is what success looks like
	// below, so it needs a runner that does not treat that as a failure.
	grep := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"grep"}, args...)...)
		cmd.Dir = repo
		out, err := cmd.Output()
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
				return "" // nothing found
			}
			t.Fatalf("git grep %s: %v", strings.Join(args, " "), err)
		}
		return string(out)
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, path), []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}

	run("init", "-q", "-b", "main")
	// The first commit, holding what should not be published.
	write("peer.md", fmt.Sprintf("%s runs the repeater at %s on %s two.\n", name, address, kept))
	// **And a file the scrub does not touch**, which keeps the reworded word in
	// the tree. That is what separates a name from an ordinary word: the scrub's
	// diff replaced both, and only one of them left the repository. The real
	// scan found three such words.
	write("net.md", fmt.Sprintf("%s two is the Tuesday net.\n", kept))
	run("add", "-A")
	run("commit", "-q", "-m", fmt.Sprintf("A peer from %s at %s", name, address))
	// The scrub: the tree is fixed, the history is not.
	write("peer.md", "K9XYZ runs the repeater at 203.0.113.42 on channel two.\n")
	run("add", "-A")
	run("commit", "-q", "-m", subject)

	python := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("python3", append([]string{script, "--repo", repo, "--subject", subject}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("scrub-history.py %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}

	t.Run("it derives the name and the address without being told", func(t *testing.T) {
		out := python("--show")
		for _, want := range []string{name, address} {
			if !strings.Contains(out, want) {
				t.Errorf("the tool did not derive %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, kept+" ->") {
			t.Errorf("the tool would replace %q, which is still in the tree and was never sensitive:\n%s", kept, out)
		}
	})

	t.Run("reporting changes nothing", func(t *testing.T) {
		before := run("rev-parse", "HEAD")
		python()
		if after := run("rev-parse", "HEAD"); after != before {
			t.Errorf("HEAD moved without --apply: %q then %q", before, after)
		}
		if !strings.Contains(run("show", "HEAD~1:peer.md"), name) {
			t.Error("the first commit no longer holds the name, so something was applied")
		}
	})

	if _, err := exec.LookPath("git-filter-repo"); err != nil {
		t.Skip("git-filter-repo is not installed; the rewrite itself is not exercised")
	}

	t.Run("applying it removes both from every commit", func(t *testing.T) {
		out := python("--apply")
		if !strings.Contains(out, "verified") {
			t.Fatalf("the tool did not report a clean verification:\n%s", out)
		}
		for _, gone := range []string{name, address} {
			if hits := grep("-I", "-l", "-F", "-e", gone, "--", "."); hits != "" {
				t.Errorf("%q survives in the tree: %s", gone, hits)
			}
			if msgs := run("log", "--all", "--format=%h", "--grep", gone); strings.TrimSpace(msgs) != "" {
				t.Errorf("%q survives in a commit message: %s", gone, msgs)
			}
		}
		if !strings.Contains(run("show", "HEAD:net.md"), kept) {
			t.Error("the reworded word was replaced too; only what left the tree should go")
		}
		if n := strings.TrimSpace(run("rev-list", "--all", "--count")); n != "2" {
			t.Errorf("history has %s commits, want 2; the rewrite dropped or added one", n)
		}
	})
}

// TestTheScrubToolRefusesAnUncleanedHistory checks the verification itself: it
// is the only thing standing between a partial rewrite and a force-push, so a
// pass it cannot fail is worth nothing.
func TestTheScrubToolRefusesAnUncleanedHistory(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	script := filepath.Join(repoRoot, "scripts", "scrub-history.py")
	repo := t.TempDir()

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.invalid",
			"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.invalid")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q", "-b", "main")
	planted := fmt.Sprintf("%d.%d.%d.%d", 42, 42, 42, 42)
	if err := os.WriteFile(filepath.Join(repo, "a.md"), []byte("a host at "+planted+"\n"), 0o644); err != nil {
		t.Fatalf("writing: %v", err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "a commit with an address in it")

	// verify() is asked about a history nobody rewrote, with the address it
	// would have replaced. It has to object.
	out, err := exec.Command("python3", "-c", fmt.Sprintf(`
import sys; sys.path.insert(0, %q)
import importlib.util
spec = importlib.util.spec_from_file_location("scrub", %q)
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
problems = m.verify(%q, {%q: "198.51.100.42"})
print("PROBLEMS:", len(problems))
for p in problems: print(" -", p)
`, filepath.Dir(script), script, repo, planted)).CombinedOutput()
	if err != nil {
		t.Fatalf("running verify: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "PROBLEMS: 0") {
		t.Errorf("verify() passed a history that still contains the address:\n%s", out)
	}
	if !strings.Contains(string(out), "public address") {
		t.Errorf("verify() did not say an address remains:\n%s", out)
	}
}

// TestTheScrubToolGivesCollidingAddressesDifferentReplacements covers the case
// that would quietly rewrite history into a lie: two public addresses sharing a
// last octet, which the real repository had. Keeping the octet is a readability
// choice, and it must not merge two hosts into one.
func TestTheScrubToolGivesCollidingAddressesDifferentReplacements(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 is not available")
	}
	script := filepath.Join(repoRoot, "scripts", "scrub-history.py")
	out, err := exec.Command("python3", "-c", fmt.Sprintf(`
import importlib.util
spec = importlib.util.spec_from_file_location("scrub", %q)
m = importlib.util.module_from_spec(spec); spec.loader.exec_module(m)
blob = ("%%d.%%d.%%d.60 and %%d.%%d.%%d.60 and 10.0.0.60 and 5.1.2.2" %% (42,42,42, 11,22,33)).encode()
got = m.derive_addresses(".", blob=blob)
print("MAPPED:", len(got))
print("DISTINCT:", len(set(got.values())))
print("PRIVATE_IN:", "10.0.0.60" in got)
print("CLAUSE_IN:", "5.1.2.2" in got)
`, script)).CombinedOutput()
	if err != nil {
		t.Fatalf("running derive_addresses: %v\n%s", err, out)
	}
	for _, want := range []string{"MAPPED: 2", "DISTINCT: 2", "PRIVATE_IN: False", "CLAUSE_IN: False"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
}
