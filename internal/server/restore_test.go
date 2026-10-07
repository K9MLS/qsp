package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// restoreVia sends a backup back through the endpoint that reads it, confirmed
// or not, and returns the answer.
func restoreVia(t *testing.T, srv *Server, a *stubAuth, full bool, file []byte, confirm bool) (int, string) {
	t.Helper()
	path, req := "/api/admin/restore", map[string]any{"document": string(file), "confirm": confirm}
	if full {
		path = "/api/admin/full-restore"
		req = map[string]any{
			"document":   base64.StdEncoding.EncodeToString(file),
			"passphrase": "right", "confirm": confirm,
		}
	}
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	rec := authed(t, srv, a, http.MethodPost, path, string(body))
	return rec.Code, rec.Body.String()
}

// backupVia asks a server for one of its two backups.
func backupVia(t *testing.T, srv *Server, a *stubAuth, full bool) []byte {
	t.Helper()
	rec := authed(t, srv, a, http.MethodGet, "/api/admin/backup", "")
	if full {
		rec = authed(t, srv, a, http.MethodPost, "/api/admin/full-backup", `{"passphrase":"right"}`)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("the backup gave %d: %s", rec.Code, rec.Body)
	}
	return rec.Body.Bytes()
}

// A configuration that can be saved can be restored from its own backup.
//
// **Both restores read 64 KiB and a save reads a megabyte**, so a server with
// a large configuration could make a backup and could not take it back. The
// configuration here is close to the save limit and made of short numbers,
// which is what an export inflates most.
//
// To see it fail: in handleRestore or handleFullRestore, put
// decodeJSONWithin(..., limit) back to decodeJSON(...).
func TestWhatCanBeSavedCanBeRestored(t *testing.T) {
	large := config.Default()
	for id := uint32(100000); len(large.IPSC.AllowedPeers) < 120000; id++ {
		large.IPSC.AllowedPeers = append(large.IPSC.AllowedPeers, id)
	}
	save, err := json.Marshal(saveRequest{Config: large, Summary: "a large club"})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if len(save) < saveBodyLimit*3/4 || len(save) > saveBodyLimit {
		t.Fatalf("the configuration is %d bytes, which does not test a save near its %d limit",
			len(save), saveBodyLimit)
	}

	for _, tc := range []struct {
		name string
		full bool
	}{
		{"the shareable export", false},
		{"the full backup", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cm := newStubConfig()
			srv, a := newFullBackupServer(t, cm, &stubSecrets{values: map[string]string{}})

			if rec := authed(t, srv, a, http.MethodPost, "/api/config", string(save)); rec.Code != http.StatusOK {
				t.Fatalf("the save gave %d: %s", rec.Code, rec.Body)
			}
			file := backupVia(t, srv, a, tc.full)

			code, body := restoreVia(t, srv, a, tc.full, file, true)
			if code != http.StatusOK {
				t.Fatalf("restoring a %d-byte backup of a %d-byte save gave %d: %.300s",
					len(file), len(save), code, body)
			}
			if got := len(cm.current.IPSC.AllowedPeers); got != len(large.IPSC.AllowedPeers) {
				t.Errorf("%d repeaters came back, want %d", got, len(large.IPSC.AllowedPeers))
			}
		})
	}
}

// A restore keeps this machine's console address and its database, on both
// paths, and takes everything else from the backup.
//
// **The database location came from the other machine.** The next restart
// opened a path that belonged to the server the backup was taken from, leaving
// this machine's accounts and history in a file nothing read; and the full
// restore kept nothing at all, the console address included.
//
// To see it fail: remove the keepMachineLocal call from handleRestore or
// handleFullRestore, or a line from keepMachineLocal.
func TestARestoreKeepsWhatBelongsToThisMachine(t *testing.T) {
	other := config.Default()
	other.Server.ListenAddress = "192.0.2.10:9000"
	other.Database.DSN = "/srv/the-other-machine/qsp.db"
	other.DMR.Join.NetworkName = "the other machine"

	for _, tc := range []struct {
		name string
		full bool
	}{
		{"the shareable export", false},
		{"the full backup", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newStubConfig()
			source.current = other
			from, fa := newFullBackupServer(t, source, &stubSecrets{values: map[string]string{}})
			file := backupVia(t, from, fa, tc.full)

			cm := newStubConfig()
			cm.current.Server.ListenAddress = "127.0.0.1:8080"
			cm.current.Database.DSN = "/var/lib/qsp/this-machine.db"
			here := cm.current
			srv, a := newFullBackupServer(t, cm, &stubSecrets{values: map[string]string{}})

			if code, body := restoreVia(t, srv, a, tc.full, file, true); code != http.StatusOK {
				t.Fatalf("the restore gave %d: %s", code, body)
			}
			got := cm.saved[0]
			for _, f := range []struct{ field, got, want string }{
				{"server.listen_address", got.Server.ListenAddress, here.Server.ListenAddress},
				{"database.driver", got.Database.Driver, here.Database.Driver},
				{"database.dsn", got.Database.DSN, here.Database.DSN},
				{"dmr.join.network_name", got.DMR.Join.NetworkName, "the other machine"},
			} {
				if f.got != f.want {
					t.Errorf("%s was restored as %q, want %q", f.field, f.got, f.want)
				}
			}
		})
	}
}

// passwordedConfig names a password file of each kind under dir, with links
// turned off so that the files need not exist for it to be valid.
func passwordedConfig(dir string) config.Config {
	cfg := config.Default()
	cfg.DMR.PasswordFile = filepath.Join(dir, "peer.password")
	cfg.DMR.PeerPasswords = filepath.Join(dir, "members")
	cfg.DMR.Upstreams = []config.Upstream{
		{
			Name: "cameron", Protocol: "openbridge",
			Address: "kb9tyc.example.com:62045", ListenAddress: "0.0.0.0:62045",
			NetworkID: 3127045, PassphraseFile: filepath.Join(dir, "cameron.pass"),
		},
		{
			Name: "never-issued", Protocol: "openbridge",
			Address: "w9xyz.example.com:62046", ListenAddress: "0.0.0.0:62046",
			NetworkID: 3127046, PassphraseFile: filepath.Join(dir, "never-issued.pass"),
		},
	}
	return cfg
}

// A full backup carries the password files the configuration names, and a
// restore puts them back where it names them, readable by nobody else.
//
// **The console writes a link's password to a file and never to the store**,
// so a full backup of the store alone restored a server whose links and
// members were all refused — under a confirmation that said "Links and members
// will work immediately." The file that was never on the server is named as
// missing rather than passed over.
//
// To see it fail: in handleFullBackup make it `_ = files`, or in
// handleFullRestore hand writePasswordFiles nil instead of full.PasswordFiles.
func TestAFullBackupCarriesThePasswordFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	cfg := passwordedConfig(dir)
	member := filepath.Join(dir, "members", "3132911")
	absent := filepath.Join(dir, "never-issued.pass")
	want := map[string]string{
		cfg.DMR.PasswordFile:                "the shared password\n",
		cfg.DMR.Upstreams[0].PassphraseFile: "agreed with cameron",
		member:                              "a member's own",
	}
	if err := os.MkdirAll(filepath.Join(dir, "members"), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for path, contents := range want {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	// Not a member's password, so not this backup's to carry.
	notes := filepath.Join(dir, "members", "notes.txt")
	if err := os.WriteFile(notes, []byte("not a credential"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	source := newStubConfig()
	source.current = cfg
	from, fa := newFullBackupServer(t, source, &stubSecrets{values: map[string]string{}})
	file := backupVia(t, from, fa, true)

	// The machine is lost, and the files with it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	cm := newStubConfig()
	srv, a := newFullBackupServer(t, cm, &stubSecrets{values: map[string]string{}})
	// The replacement machine is laid out as the lost one was.
	srv.opts.CredentialDir = from.opts.CredentialDir

	code, body := restoreVia(t, srv, a, true, file, false)
	if code != http.StatusPreconditionRequired {
		t.Fatalf("an unconfirmed restore gave %d: %s", code, body)
	}
	if strings.Contains(body, "work immediately") {
		t.Errorf("the confirmation promises working links with a password file missing: %s", body)
	}
	var asked struct {
		Missing []string `json:"missing_password_files"`
	}
	if err := json.Unmarshal([]byte(body), &asked); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !slices.Equal(asked.Missing, []string{absent}) {
		t.Errorf("the confirmation names %v as missing, want only %s", asked.Missing, absent)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("an unconfirmed restore wrote password files")
	}

	code, body = restoreVia(t, srv, a, true, file, true)
	if code != http.StatusOK {
		t.Fatalf("the restore gave %d: %s", code, body)
	}
	var done struct {
		Written []string              `json:"password_files"`
		Failed  []passwordFileFailure `json:"password_files_failed"`
		Missing []string              `json:"missing_password_files"`
	}
	if err := json.Unmarshal([]byte(body), &done); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if len(done.Written) != len(want) || len(done.Failed) != 0 {
		t.Errorf("wrote %v and failed %v, want %d written", done.Written, done.Failed, len(want))
	}
	if !slices.Equal(done.Missing, []string{absent}) {
		t.Errorf("the restore names %v as missing, want only %s", done.Missing, absent)
	}

	for path, contents := range want {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s did not come back: %v", path, err)
			continue
		}
		if string(raw) != contents {
			t.Errorf("%s came back as %q, want %q", path, raw, contents)
		}
		if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s came back as %v, want mode 0600", path, info.Mode())
		}
	}
	if info, err := os.Stat(filepath.Join(dir, "members")); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("the member directory came back as %v, want mode 0700", info)
	}
	if _, err := os.Stat(notes); err == nil {
		t.Error("a file that is not a password was carried by the backup")
	}
}

// A restore writes a password file only where the restored configuration
// names one.
//
// The backup says where each file goes, and a backup is a file somebody was
// handed: without the check it could put anything anywhere the service can
// write.
//
// To see it fail: put `false &&` in front of the condition that refuses a
// path in writePasswordFiles.
func TestARestoreWritesOnlyTheFilesTheConfigurationNames(t *testing.T) {
	dir := t.TempDir()
	cfg := passwordedConfig(filepath.Join(dir, "secrets"))

	for _, tc := range []struct {
		name    string
		path    string
		written bool
	}{
		{"the shared password", cfg.DMR.PasswordFile, true},
		{"a link's passphrase", cfg.DMR.Upstreams[0].PassphraseFile, true},
		{"a member's own", filepath.Join(cfg.DMR.PeerPasswords, "3132911"), true},
		{"a file nothing names", filepath.Join(dir, "elsewhere", "qsp.json"), false},
		{"a file beside the members that is not an ID", filepath.Join(cfg.DMR.PeerPasswords, "authorized_keys"), false},
		{"a directory below the members", filepath.Join(cfg.DMR.PeerPasswords, "deeper", "3132911"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Server{opts: Options{CredentialDir: CredentialDirFor(filepath.Join(dir, "qsp.json"))}}
			written, failed := srv.writePasswordFiles(cfg, map[string]string{tc.path: "contents"})
			_, err := os.Stat(tc.path)
			if exists := err == nil; exists != tc.written {
				t.Errorf("written is %v, want %v", exists, tc.written)
			}
			if tc.written != (len(written) == 1) || tc.written == (len(failed) == 1) {
				t.Errorf("reported written %v and failed %v", written, failed)
			}
		})
	}
}
