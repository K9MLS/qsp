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

// TestTheImageRunsUnprivileged.
//
// Until 0412 the image ran as root, on the reasoning that a fresh volume is
// root-owned and an unprivileged user could not write to it. Docker copies a
// directory's ownership into a fresh named volume, so the image ships
// /var/lib/qsp owned by the user and needs no entrypoint script.
//
// To see it bite: delete USER, or drop --chown from the data directory's COPY —
// a fresh install then fails writing its database, the defect other projects
// shipped when they made this change without it.
func TestTheImageRunsUnprivileged(t *testing.T) {
	df := deployFile(t, "Dockerfile")
	for _, want := range []string{
		"USER 65532:65532",
		"COPY --from=build --chown=65532:65532 /out/var/lib/qsp /var/lib/qsp",
		"COPY --from=build --chown=65532:65532 /out/run/qsp /run/qsp",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("the Dockerfile lacks %q", want)
		}
	}
	// The owned directory must be in place before the user switch, and the
	// switch before the entrypoint, or the entrypoint runs as root.
	user, data, entry := strings.Index(df, "\nUSER "), strings.Index(df, "/out/var/lib/qsp /var/lib/qsp"), strings.Index(df, "\nENTRYPOINT")
	if !(data >= 0 && user > data && entry > user) {
		t.Errorf("order is data %d, USER %d, ENTRYPOINT %d; want the data directory, then USER, then ENTRYPOINT", data, user, entry)
	}
}

// TestTheImageCrossCompilesRatherThanEmulates: an arm64 image built under QEMU
// takes minutes; a native build stage with GOOS/GOARCH takes seconds.
//
// To see it bite: remove --platform=$BUILDPLATFORM, or the GOOS/GOARCH on the
// build — the second silently builds amd64 binaries into the arm64 image.
func TestTheImageCrossCompilesRatherThanEmulates(t *testing.T) {
	df := deployFile(t, "Dockerfile")
	for _, want := range []string{
		"FROM --platform=$BUILDPLATFORM golang:",
		"ARG TARGETOS", "ARG TARGETARCH",
		"CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build",
	} {
		if !strings.Contains(df, want) {
			t.Errorf("the Dockerfile lacks %q", want)
		}
	}
}

// TestTheImageIsPublishedOnlyAfterTheTests reads the workflow as text; there
// is no YAML parser among QSP's dependencies, and adding one for a test is not
// worth it.
//
// To see rows fail: drop `needs: ci`; give the whole workflow packages: write;
// or unpin a docker/ action to a tag.
func TestTheImageIsPublishedOnlyAfterTheTests(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	wf := string(b)
	i := strings.Index(wf, "\n  image:\n")
	if i < 0 {
		t.Fatal("the workflow has no image job")
	}
	top, image := wf[:i], wf[i:]

	for _, want := range []string{
		"needs: ci",
		"if: startsWith(github.ref, 'refs/tags/v')",
		"packages: write",
		"platforms: linux/amd64,linux/arm64",
		`tag="${GITHUB_REF_NAME#v}"`,
		"provenance: mode=max",
		"sbom: true",
	} {
		if !strings.Contains(image, want) {
			t.Errorf("the image job lacks %q", want)
		}
	}
	if strings.Contains(top, "packages: write") {
		t.Error("packages: write is granted outside the image job; only publishing needs it")
	}
	for _, line := range strings.Split(image, "\n") {
		use, ok := strings.CutPrefix(strings.TrimSpace(line), "- uses: ")
		if !ok || !strings.HasPrefix(use, "docker/") {
			continue
		}
		at := strings.Index(use, "@")
		if at < 0 {
			t.Errorf("%q names no version at all", use)
			continue
		}
		ref := strings.Fields(use[at+1:])[0]
		if len(ref) != 40 || strings.Trim(ref, "0123456789abcdef") != "" {
			t.Errorf("%q is not pinned to a full commit SHA", use)
		}
	}
}
