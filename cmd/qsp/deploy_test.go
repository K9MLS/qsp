package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/buildinfo"
	"github.com/k9mls/qsp/internal/config"
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

// TestZelloIsAnAddOnThatSharesQSPsSocket checks the pieces of the Zello
// container install that only work together (0415).
//
// **Nothing here fails loudly when it drifts.** QSP serves its logon socket
// only to its own UID, so a connector image with a different USER connects,
// is refused, and reports qsp_unreachable. A socket directory mounted into one
// container and not the other leaves QSP serving a socket the connector cannot
// see. A pin that falls behind VERSION pulls a connector from another release.
// Each of those starts, looks healthy in `docker compose ps`, and carries no
// audio, which is the failure this project finds last.
//
// Docker cannot run where these tests do, so as with the rest of this file the
// parts that drift silently are checked as text, and **every expected value is
// read from the code or from QSP's own image** — the socket path from config,
// the user from QSP's Dockerfile, the version from buildinfo — so a row cannot
// agree with a file that moved away from the program.
//
// To see rows fail: set USER 65533:65533 in Dockerfile.zello; delete the
// qsp-run mount from the qsp service in docker-compose.zello.yml; change
// DefaultZelloLogonSocket to /run/qspx/zello.sock; pin qsp-zello to :latest;
// add a setup-qemu-action step to the workflow; name qsp-zello in
// docker-compose.yml.
func TestZelloIsAnAddOnThatSharesQSPsSocket(t *testing.T) {
	socketDir := filepath.Dir(config.DefaultZelloLogonSocket)

	files := make(map[string]string)
	for _, name := range []string{
		"deploy/docker/Dockerfile", "deploy/docker/Dockerfile.zello",
		"deploy/docker/docker-compose.yml", "deploy/docker/docker-compose.zello.yml",
		".github/workflows/ci.yml", ".gitignore", ".dockerignore",
	} {
		b, err := os.ReadFile(filepath.Join(repoRoot, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		files[name] = string(b)
	}
	qspImage := files["deploy/docker/Dockerfile"]
	zelloImage := files["deploy/docker/Dockerfile.zello"]
	override := files["deploy/docker/docker-compose.zello.yml"]
	workflow := files[".github/workflows/ci.yml"]

	tests := []struct {
		name  string
		check func() error
	}{
		{"the connector image is pinned to this version", func() error {
			if want := "ghcr.io/k9mls/qsp-zello:" + buildinfo.Version; !strings.Contains(override, want) {
				return fmt.Errorf("docker-compose.zello.yml does not pin %s", want)
			}
			if strings.Contains(override, ":latest") {
				return errors.New("docker-compose.zello.yml pulls :latest; an upgrade should be a decision")
			}
			return nil
		}},
		{"both services mount one volume at the socket's directory", func() error {
			for _, svc := range []string{"qsp", "qsp-zello"} {
				block, err := serviceBlock(override, svc)
				if err != nil {
					return err
				}
				if !strings.Contains(block, "qsp-run:"+socketDir) {
					return fmt.Errorf("service %s does not mount qsp-run at %s, where QSP makes its logon socket", svc, socketDir)
				}
			}
			return nil
		}},
		{"the connector runs as QSP's user", func() error {
			want, err := userOf(qspImage)
			if err != nil {
				return fmt.Errorf("QSP's Dockerfile: %w", err)
			}
			got, err := userOf(zelloImage)
			if err != nil {
				return fmt.Errorf("Dockerfile.zello: %w", err)
			}
			if got != want {
				return fmt.Errorf("Dockerfile.zello runs as %s and QSP as %s; the logon socket refuses another UID", got, want)
			}
			return nil
		}},
		{"the connector image owns the socket's directory before switching user", func() error {
			user, err := userOf(zelloImage)
			if err != nil {
				return err
			}
			own := strings.Index(zelloImage, "--chown="+user+" /out"+socketDir+" "+socketDir)
			switched := strings.Index(zelloImage, "\nUSER ")
			entry := strings.Index(zelloImage, "\nENTRYPOINT")
			if !(own >= 0 && switched > own && entry > switched) {
				return fmt.Errorf("want %s owned by %s, then USER, then ENTRYPOINT; found at %d, %d, %d",
					socketDir, user, own, switched, entry)
			}
			return nil
		}},
		{"the connector shares the host's network", func() error {
			block, err := serviceBlock(override, "qsp-zello")
			if err != nil {
				return err
			}
			if !strings.Contains(block, "network_mode: host") {
				return errors.New("qsp-zello is not on the host network; USRP and AMBEserver are on the host's loopback")
			}
			return nil
		}},
		{"the connector reads its file where the compose file mounts it", func() error {
			path, err := configArgOf(zelloImage)
			if err != nil {
				return err
			}
			block, err := serviceBlock(override, "qsp-zello")
			if err != nil {
				return err
			}
			if !strings.Contains(block, "target: "+path) {
				return fmt.Errorf("Dockerfile.zello reads %s and qsp-zello mounts nothing there", path)
			}
			if !strings.Contains(block, "create_host_path: false") {
				return errors.New("a missing qsp-zello.json would be created as a directory; set create_host_path: false")
			}
			if strings.Contains(block, "qsp-data") {
				return errors.New("qsp-zello mounts QSP's data volume, which holds its database and key")
			}
			return nil
		}},
		{"the connector image cross-compiles rather than emulates", func() error {
			for _, want := range []string{
				"FROM --platform=$BUILDPLATFORM golang:" + goModMinor(t),
				"GOOS=$TARGETOS GOARCH=$TARGETARCH go build",
			} {
				if !strings.Contains(zelloImage, want) {
					return fmt.Errorf("Dockerfile.zello lacks %q", want)
				}
			}
			if strings.Contains(workflow, "setup-qemu") {
				return errors.New("the workflow sets up QEMU; both images cross-compile and none should emulate")
			}
			return nil
		}},
		{"the connector image is published only by the image job", func() error {
			i := strings.Index(workflow, "\n  image:\n")
			if i < 0 {
				return errors.New("the workflow has no image job")
			}
			const want = "file: deploy/docker/Dockerfile.zello"
			if !strings.Contains(workflow[i:], want) {
				return errors.New("the image job does not build Dockerfile.zello")
			}
			if strings.Contains(workflow[:i], want) {
				return errors.New("Dockerfile.zello is built outside the image job, which is the one gated on ci")
			}
			return nil
		}},
		{"the main compose file stays QSP alone", func() error {
			if strings.Contains(files["deploy/docker/docker-compose.yml"], "qsp-zello") {
				return errors.New("docker-compose.yml names qsp-zello; Zello is added by its own file")
			}
			return nil
		}},
		{"the connector's file is ignored by git and the build", func() error {
			for _, f := range []string{".gitignore", ".dockerignore"} {
				if !strings.Contains(files[f], "deploy/docker/qsp-zello.json") {
					return fmt.Errorf("%s does not ignore deploy/docker/qsp-zello.json", f)
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

// serviceBlock returns one service's lines from a compose file, from its
// two-space-indented name to the next line indented two spaces or less.
func serviceBlock(compose, name string) (string, error) {
	lines := strings.Split(compose, "\n")
	for i, line := range lines {
		if line != "  "+name+":" {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			l := lines[j]
			if l == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
				continue
			}
			if !strings.HasPrefix(l, "   ") {
				end = j
				break
			}
		}
		return strings.Join(lines[i+1:end], "\n"), nil
	}
	return "", fmt.Errorf("the compose file has no service %q", name)
}

// userOf returns the value of a Dockerfile's USER instruction.
func userOf(dockerfile string) (string, error) {
	for _, line := range strings.Split(dockerfile, "\n") {
		if rest, ok := strings.CutPrefix(line, "USER "); ok {
			return strings.TrimSpace(rest), nil
		}
	}
	return "", errors.New("no USER instruction, so the image runs as root")
}

// configArgOf returns the path after -config in a Dockerfile's CMD.
func configArgOf(dockerfile string) (string, error) {
	for _, line := range strings.Split(dockerfile, "\n") {
		rest, ok := strings.CutPrefix(line, "CMD ")
		if !ok {
			continue
		}
		var args []string
		if err := json.Unmarshal([]byte(rest), &args); err != nil {
			return "", fmt.Errorf("CMD is not an exec-form list: %w", err)
		}
		for i, a := range args {
			if a == "-config" && i+1 < len(args) {
				return args[i+1], nil
			}
		}
		return "", errors.New("CMD passes no -config")
	}
	return "", errors.New("no CMD instruction")
}

// goModMinor returns go.mod's Go version as major.minor, which is what an image
// tag names.
func goModMinor(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "go "); ok {
			parts := strings.Split(strings.TrimSpace(rest), ".")
			if len(parts) > 2 {
				parts = parts[:2]
			}
			return strings.Join(parts, ".")
		}
	}
	t.Fatal("go.mod names no Go version")
	return ""
}

// TestServiceBlockReadsOnlyItsOwnService keeps the compose reader from letting
// one service's mount satisfy a row about another.
func TestServiceBlockReadsOnlyItsOwnService(t *testing.T) {
	compose := "services:\n  qsp:\n    volumes:\n      - a:/a\n\n    # a comment\n  qsp-zello:\n    image: z\n\nvolumes:\n  a:\n"
	tests := []struct {
		name, service, want, notWant string
		wantErr                      bool
	}{
		{name: "a service's own lines, past blanks and comments", service: "qsp", want: "a:/a", notWant: "image: z"},
		{name: "stops at the top-level key", service: "qsp-zello", want: "image: z", notWant: "  a:"},
		{name: "a name is matched whole, not as a prefix", service: "qs", wantErr: true},
		{name: "a missing service is an error", service: "ambeserver", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := serviceBlock(compose, tc.service)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("serviceBlock(%q) = %q, want an error", tc.service, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("serviceBlock(%q): %v", tc.service, err)
			}
			if !strings.Contains(got, tc.want) || strings.Contains(got, tc.notWant) {
				t.Errorf("serviceBlock(%q) = %q, want %q and not %q", tc.service, got, tc.want, tc.notWant)
			}
		})
	}
}
