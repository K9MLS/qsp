package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestTheUpgradeCommandNamesWhatExists keeps the one-command upgrade in
// deploy/docker/README.md runnable as the files change under it.
//
// An operator pastes it and walks away, so every file it names must exist,
// the container it asks for its version must be the one the compose file
// starts, and it must still end by printing that version: the line that says
// the upgrade happened.
//
// To see it fail: rename docker-compose.zello.yml in the README's Zello
// line; change `docker exec qsp` to another name; or drop `-version`.
func TestTheUpgradeCommandNamesWhatExists(t *testing.T) {
	readme, err := os.ReadFile(filepath.Join(repoRoot, "deploy/docker/README.md"))
	if err != nil {
		t.Fatalf("reading the Docker README: %v", err)
	}
	section := string(readme)
	start := strings.Index(section, "## Upgrading")
	if start < 0 {
		t.Fatal("deploy/docker/README.md has no Upgrading section")
	}
	section = section[start+len("## Upgrading"):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}

	var commands []string
	for _, line := range strings.Split(section, "\n") {
		if l := strings.TrimSpace(line); strings.HasPrefix(l, "cd ~/qsp && git pull") {
			commands = append(commands, l)
		}
	}
	if len(commands) < 2 {
		t.Fatalf("found %d one-line upgrade commands, want the plain one and Zello's", len(commands))
	}

	compose, err := os.ReadFile(filepath.Join(repoRoot, "deploy/docker/docker-compose.yml"))
	if err != nil {
		t.Fatalf("reading the compose file: %v", err)
	}
	container := regexp.MustCompile(`container_name:\s*(\S+)`).FindSubmatch(compose)
	if container == nil {
		t.Fatal("docker-compose.yml names no container")
	}

	composeFile := regexp.MustCompile(`docker-compose[\w.]*\.yml`)
	for i, cmd := range commands {
		for _, file := range composeFile.FindAllString(cmd, -1) {
			if _, err := os.Stat(filepath.Join(repoRoot, "deploy/docker", file)); err != nil {
				t.Errorf("command %d names %s, which is not in deploy/docker", i+1, file)
			}
		}
		for _, want := range []string{
			"cd deploy/docker",
			"docker exec " + string(container[1]) + " /qsp -version",
		} {
			if !strings.Contains(cmd, want) {
				t.Errorf("command %d does not contain %q: %s", i+1, want, cmd)
			}
		}
	}
}
