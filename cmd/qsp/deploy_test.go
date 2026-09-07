package main

import (
	"os"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/buildinfo"
)

// The container files, checked against the code they deploy.
//
// # Why this file exists
//
// `deploy/docker` held a Dockerfile and a compose file that **had never been
// run**. The compose file published no UDP ports and did not use host
// networking, so a container started from it could not receive a single DMR
// packet; the build stage pinned Go 1.22 where the module needs 1.27; the
// volume was `/data` where everything else says `/var/lib/qsp`; and the
// healthcheck's comment described a readiness endpoint while the command ran
// `-version`.
//
// Nobody noticed because nothing checked. Docker is not available where these
// tests run, so the image cannot be built here — but **the parts that drift
// silently are exactly the parts a string comparison catches**: a path, a
// variable name, a version tag. Those are checked here, and the rest is
// checked by an operator running it, which is the only way it ever could be.

func deployFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile("../../deploy/docker/" + name)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return string(b)
}

// TestTheContainerReadsTheVariablesTheCodeReads is the drift this most invites.
//
// The compose file passes environment variables by name and `bootstrap.go`
// reads them by name, and nothing but this connects the two. Renaming the
// constant would leave a container that starts, finds no password, and prints
// a message naming a variable the compose file never sets.
func TestTheContainerReadsTheVariablesTheCodeReads(t *testing.T) {
	compose := deployFile(t, "docker-compose.yml")
	example := deployFile(t, ".env.example")

	for _, name := range []string{peerPasswordEnv, allowedPeersEnv} {
		if !strings.Contains(compose, name) {
			t.Errorf("the compose file never passes %s, which a first run requires", name)
		}
		if !strings.Contains(example, name) {
			t.Errorf(".env.example never mentions %s, so nobody would set it", name)
		}
	}
}

// TestTheVolumeIsWhereTheBinaryLooks catches the mismatch the old files had.
//
// The entrypoint names a configuration path and the compose file mounts a
// volume. If those disagree the container starts, writes a configuration into
// a layer that is discarded on the next `docker compose up`, and bootstraps
// again every time — losing the operator's edits silently, which is worse than
// failing.
func TestTheVolumeIsWhereTheBinaryLooks(t *testing.T) {
	const dataDir = "/var/lib/qsp"

	dockerfile := deployFile(t, "Dockerfile")
	if !strings.Contains(dockerfile, dataDir+"/qsp.json") {
		t.Errorf("the entrypoint does not point at %s/qsp.json", dataDir)
	}

	compose := deployFile(t, "docker-compose.yml")
	if !strings.Contains(compose, ":"+dataDir) {
		t.Errorf("no volume is mounted at %s, so the configuration would not survive a restart", dataDir)
	}
}

// TestTheContainerUsesHostNetworking is the one that decides whether any of
// this works at all.
//
// Docker's bridge NATs UDP and rewrites source addresses, and QSP tracks peers
// by address — ADR-0011 as amended records hotspots rebinding their NAT mapping
// every eight to nine minutes. A second NAT in that path produces intermittent
// peer-tracking faults, which is the class of defect this project has spent the
// most hours on.
//
// The old compose file used bridge networking **and published no UDP ports at
// all**, so it was a web console in a box.
func TestTheContainerUsesHostNetworking(t *testing.T) {
	compose := deployFile(t, "docker-compose.yml")
	if !strings.Contains(compose, "network_mode: host") {
		t.Fatal("the compose file does not use host networking; " +
			"Docker's bridge NAT breaks peer tracking, and QSP tracks peers by address")
	}
	// Under host networking a `ports:` block is meaningless, and leaving one
	// there would suggest the ports are handled when they are not.
	for _, line := range strings.Split(compose, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "ports:") {
			t.Error("the compose file publishes ports and uses host networking; " +
				"one of the two is wrong")
		}
	}
}

// TestThePublishedImageIsPinnedToThisVersion keeps a routine `docker compose
// pull` from becoming an unannounced upgrade of a live repeater network.
//
// `:latest` looks friendly and means the operator does not choose when their
// network changes.
func TestThePublishedImageIsPinnedToThisVersion(t *testing.T) {
	compose := deployFile(t, "docker-compose.yml")
	if strings.Contains(compose, ":latest") {
		t.Error("the compose file pulls :latest; an upgrade should be a decision")
	}
	want := "ghcr.io/k9mls/qsp:" + buildinfo.Version
	if !strings.Contains(compose, want) {
		t.Errorf("the compose file does not pin %s; VERSION and the image tag have drifted", want)
	}
}

// TestTheBuildStageMatchesTheModule catches what pinned Go 1.22 against a
// module needing 1.27: a build that fails outright, in a file nobody ran.
func TestTheBuildStageMatchesTheModule(t *testing.T) {
	gomod, err := os.ReadFile("../../go.mod")
	if err != nil {
		t.Fatalf("%v", err)
	}
	var want string
	for _, line := range strings.Split(string(gomod), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			want = strings.TrimSpace(rest)
			break
		}
	}
	if want == "" {
		t.Fatal("go.mod names no Go version")
	}
	// Compare on major.minor: go.mod may say 1.27.0 where an image tag says
	// 1.27, and the patch level is not what drifts.
	if parts := strings.Split(want, "."); len(parts) > 2 {
		want = parts[0] + "." + parts[1]
	}

	dockerfile := deployFile(t, "Dockerfile")
	if !strings.Contains(dockerfile, "golang:"+want) {
		t.Errorf("the Dockerfile does not build on golang:%s, which go.mod requires", want)
	}
}
