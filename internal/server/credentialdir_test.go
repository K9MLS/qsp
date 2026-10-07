package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/config"
)

// home is a server's own directory, and a neighbour beside it standing for
// everywhere else on the machine.
func home(t *testing.T) (root, elsewhere string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolving the temporary directory: %v", err)
	}
	root, elsewhere = filepath.Join(base, "qsp"), filepath.Join(base, "elsewhere")
	for _, d := range []string{root, elsewhere} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
	}
	return root, elsewhere
}

// TestWhichPathsAreInsideTheServersDirectory. Every read, write and delete of
// a password file asks this first.
//
// To see it fail: judge the path as written, without resolveExisting (the
// two rows about links); or accept a path whose relative form is ".." (the
// rows that climb out).
func TestWhichPathsAreInsideTheServersDirectory(t *testing.T) {
	root, elsewhere := home(t)
	if err := os.WriteFile(filepath.Join(elsewhere, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// A link inside that leads out, as a directory and as a file.
	if err := os.Symlink(elsewhere, filepath.Join(root, "door")); err != nil {
		t.Skipf("this filesystem has no symbolic links: %v", err)
	}
	if err := os.Symlink(filepath.Join(elsewhere, "secret"), filepath.Join(root, "linked.pass")); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "peer.pass"), []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	tests := []struct {
		name   string
		path   string
		inside bool
	}{
		{"a file in it", filepath.Join(root, "peer.pass"), true},
		{"a file in it that does not exist yet", filepath.Join(root, "new-link.pass"), true},
		{"a member's file, two directories that do not exist yet down", filepath.Join(root, "members", "3132911"), true},
		{"the directory itself", root, false},
		{"a file beside it", filepath.Join(elsewhere, "secret"), false},
		{"its parent", filepath.Dir(root), false},
		{"a path that climbs out and names it again", filepath.Join(root, "..", "elsewhere", "secret"), false},
		{"a path that only begins with its name", root + "-old/peer.pass", false},
		{"a system file", "/etc/passwd", false},
		{"through a directory that is a link out", filepath.Join(root, "door", "secret"), false},
		{"a new file through that link", filepath.Join(root, "door", "planted"), false},
		{"a file that is itself a link out", filepath.Join(root, "linked.pass"), false},
		{"nothing", "  ", false},
	}
	srv := &Server{opts: Options{CredentialDir: CredentialDirFor(filepath.Join(root, "qsp.json"))}}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := srv.credentialPath(tc.path)
			if (err == nil) != tc.inside {
				t.Errorf("inside: %v (%v), want %v", err == nil, err, tc.inside)
			}
		})
	}

	// A server with no configuration file has no directory, and so nothing
	// is inside it.
	bare := &Server{}
	if _, err := bare.credentialPath(filepath.Join(root, "peer.pass")); err == nil {
		t.Error("a server started with no configuration file accepted a path")
	}
}

// aBackupNaming builds a full backup whose configuration keeps its shared
// password at path, carrying contents for it.
func aBackupNaming(t *testing.T, path, contents string) []byte {
	t.Helper()
	cfg := config.Default()
	cfg.DMR.PasswordFile = path
	full := config.NewFullBackup(cfg, map[string]string{}, "0.0.0", time.Now())
	full.PasswordFiles = map[string]string{path: contents}
	var out bytes.Buffer
	if err := config.WriteFullBackup(&out, full, "right"); err != nil {
		t.Fatalf("WriteFullBackup: %v", err)
	}
	return out.Bytes()
}

// TestAFullRestoreWritesOnlyInsideTheServersDirectory. A backup is a file
// somebody was handed. Its configuration says where its password files go,
// and that was the only check on where they went: a backup naming
// ~/.ssh/authorized_keys as its password file wrote one.
//
// To see it fail: remove the credentialPath test from writePasswordFiles.
func TestAFullRestoreWritesOnlyInsideTheServersDirectory(t *testing.T) {
	tests := []struct {
		name string
		// where is the path the backup names, given the two directories.
		where   func(root, elsewhere string) string
		written bool
	}{
		{"in this server's directory", func(root, _ string) string { return filepath.Join(root, "peer.pass") }, true},
		{"somewhere else on the machine", func(_, elsewhere string) string { return filepath.Join(elsewhere, "authorized_keys") }, false},
		{"climbing out of the directory", func(root, _ string) string { return filepath.Join(root, "..", "elsewhere", "authorized_keys") }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, elsewhere := home(t)
			path := tc.where(root, elsewhere)
			file := aBackupNaming(t, path, "planted by a backup\n")

			cm := newStubConfig()
			srv, a := newFullBackupServer(t, cm, &stubSecrets{values: map[string]string{}})
			srv.opts.CredentialDir = CredentialDirFor(filepath.Join(root, "qsp.json"))

			// Asked first, and told which files will not be written.
			code, body := restoreVia(t, srv, a, true, file, false)
			if code != http.StatusPreconditionRequired {
				t.Fatalf("an unconfirmed restore gave %d: %s", code, body)
			}
			var asked struct {
				Refused []string `json:"refused_password_files"`
				Summary string   `json:"summary"`
			}
			if err := json.Unmarshal([]byte(body), &asked); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			if (len(asked.Refused) == 0) != tc.written {
				t.Errorf("before confirming, refused is %v", asked.Refused)
			}
			if !tc.written && !strings.Contains(asked.Summary, "will not be written") {
				t.Errorf("the confirmation does not say a file will be left out: %s", asked.Summary)
			}

			code, body = restoreVia(t, srv, a, true, file, true)
			if code != http.StatusOK {
				t.Fatalf("the restore gave %d: %s", code, body)
			}
			var done struct {
				Written []string              `json:"password_files"`
				Failed  []passwordFileFailure `json:"password_files_failed"`
			}
			if err := json.Unmarshal([]byte(body), &done); err != nil {
				t.Fatalf("decoding: %v", err)
			}
			_, err := os.Stat(path)
			if exists := err == nil; exists != tc.written {
				t.Errorf("the file exists: %v, want %v", exists, tc.written)
			}
			if tc.written != (len(done.Written) == 1) || tc.written == (len(done.Failed) == 1) {
				t.Errorf("reported written %v and failed %v", done.Written, done.Failed)
			}
			if !tc.written && len(done.Failed) == 1 && !strings.Contains(done.Failed[0].Error, root) {
				t.Errorf("the refusal does not name the directory: %q", done.Failed[0].Error)
			}
		})
	}
}

// TestAFullBackupReadsOnlyInsideTheServersDirectory. The other direction:
// point a password-file setting at any file the service can read, take a
// full backup, and the file left with it.
//
// To see it fail: remove the credentialPath loop from readPasswordFiles.
func TestAFullBackupReadsOnlyInsideTheServersDirectory(t *testing.T) {
	const private = "something that is not a password\n"
	tests := []struct {
		name    string
		setting func(c *config.Config, path string)
	}{
		{"the shared password file", func(c *config.Config, p string) { c.DMR.PasswordFile = p }},
		{"a link's passphrase file", func(c *config.Config, p string) {
			c.DMR.Upstreams = []config.Upstream{{Name: "x", Protocol: "openbridge", PassphraseFile: p}}
		}},
		{"the directory of members' passwords", func(c *config.Config, p string) {
			c.DMR.PeerPasswords = filepath.Dir(p)
		}},
		// Refused before it is listed, whatever is or is not in it.
		{"a directory of members' passwords with none in it", func(c *config.Config, p string) {
			c.DMR.PeerPasswords = filepath.Join(filepath.Dir(p), "empty")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, elsewhere := home(t)
			// Named as a member's file would be, so the third row has
			// something it would have read.
			target := filepath.Join(elsewhere, "3132911")
			if err := os.WriteFile(target, []byte(private), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			if err := os.MkdirAll(filepath.Join(elsewhere, "empty"), 0o700); err != nil {
				t.Fatalf("MkdirAll: %v", err)
			}
			cm := newStubConfig()
			tc.setting(&cm.current, target)
			srv, a := newFullBackupServer(t, cm, &stubSecrets{values: map[string]string{}})
			srv.opts.CredentialDir = CredentialDirFor(filepath.Join(root, "qsp.json"))

			rec := authed(t, srv, a, http.MethodPost, "/api/admin/full-backup", `{"passphrase":"right"}`)
			if rec.Code == http.StatusOK {
				t.Fatalf("a backup was made, %d bytes of it, with a password setting naming %s",
					rec.Body.Len(), target)
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("the backup gave %d, want 400: %s", rec.Code, rec.Body)
			}
			said := rec.Body.String()
			if !strings.Contains(said, elsewhere) || !strings.Contains(said, root) {
				t.Errorf("the refusal does not name the file and the directory: %s", said)
			}
			if strings.Contains(said, strings.TrimSpace(private)) {
				t.Error("the refusal carries the file's contents")
			}
		})
	}
}

// TestALinksPassphraseFileIsItsOwnAndInTheServersDirectory. A link's
// passphrase is written to <name>.pass beside the shared password. A link
// named for that password's own file wrote over it, and every hotspot was
// refused from then on.
//
// To see it fail: remove the `taken` test from writePassphrase, or the test
// on the name.
func TestALinksPassphraseFileIsItsOwnAndInTheServersDirectory(t *testing.T) {
	root, _ := home(t)
	shared := filepath.Join(root, "peers.pass")
	if err := os.WriteFile(shared, []byte("the shared password"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := config.Default()
	cfg.DMR.PasswordFile = shared
	cfg.DMR.Upstreams = []config.Upstream{
		{Name: "cameron", Protocol: "qsp", PassphraseFile: filepath.Join(root, "cameron.pass")},
	}
	srv := &Server{opts: Options{CredentialDir: CredentialDirFor(filepath.Join(root, "qsp.json"))}}

	tests := []struct {
		name string
		link string
		ok   bool
	}{
		{"a new link", "pete", true},
		{"a link agreeing again under its own name", "cameron", true},
		{"the same, in another case", "Cameron", true},
		{"a link named for the shared password file", "peers", false},
		{"a name with a directory in it", "../elsewhere/pete", false},
		{"a name that is a path", "/tmp/pete", false},
		{"a hidden file", ".pete", false},
		{"no name", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path, err := srv.writePassphrase(cfg, tc.link, "agreed")
			if (err == nil) != tc.ok {
				t.Fatalf("written: %v (%v), want %v", err == nil, err, tc.ok)
			}
			if tc.ok && filepath.Dir(path) != root {
				t.Errorf("written to %s, outside %s", path, root)
			}
			if raw, _ := os.ReadFile(shared); string(raw) != "the shared password" {
				t.Fatalf("the shared password file now holds %q", raw)
			}
		})
	}
}

// TestRemovingALinkDeletesOnlyItsOwnFile. The file removed with a link is
// whatever its passphrase setting names, and a setting can name anything.
//
// To see it fail: remove the credentialPath test from handleRemoveLink.
func TestRemovingALinkDeletesOnlyItsOwnFile(t *testing.T) {
	tests := []struct {
		name    string
		where   func(root, elsewhere string) string
		removed bool
	}{
		{"its passphrase, in this server's directory", func(root, _ string) string { return filepath.Join(root, "pete.pass") }, true},
		{"a file somewhere else", func(_, elsewhere string) string { return filepath.Join(elsewhere, "important") }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root, elsewhere := home(t)
			path := tc.where(root, elsewhere)
			if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
			cm := newStubConfig()
			cm.current.DMR.PasswordFile = filepath.Join(root, "peers.pass")
			cm.current.DMR.Upstreams = []config.Upstream{{Name: "pete", Protocol: "qsp",
				Address: "pete.example.com:62031", PassphraseFile: path}}
			srv, a := newConfigServer(t, cm, &recordingAudit{})

			rec := authed(t, srv, a, http.MethodDelete, "/api/links/pete", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("removing the link gave %d: %s", rec.Code, rec.Body)
			}
			_, err := os.Stat(path)
			if gone := os.IsNotExist(err); gone != tc.removed {
				t.Errorf("the file is gone: %v, want %v", gone, tc.removed)
			}
		})
	}
}
