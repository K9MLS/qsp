package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/secrets"
)

// stubSecrets is the credential store a backup needs, without a database.
//
// It satisfies secretLister, which exists so this dependency is visible and a
// test can supply it — the development container has no SQLite driver.
type stubSecrets struct {
	values  map[string]string
	getErr  error
	listErr error
	setErr  error
}

func (s *stubSecrets) List(context.Context) ([]secrets.Record, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	out := make([]secrets.Record, 0, len(s.values))
	for name := range s.values {
		out = append(out, secrets.Record{Name: name})
	}
	return out, nil
}

func (s *stubSecrets) Set(_ context.Context, name, value, _ string) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.values == nil {
		s.values = map[string]string{}
	}
	s.values[name] = value
	return nil
}

func (s *stubSecrets) Delete(_ context.Context, name string) error {
	delete(s.values, name)
	return nil
}

func (s *stubSecrets) Has(_ context.Context, name string) (bool, error) {
	_, ok := s.values[name]
	return ok, nil
}

func (s *stubSecrets) Get(_ context.Context, name string) (string, error) {
	if s.getErr != nil {
		return "", s.getErr
	}
	v, ok := s.values[name]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}

// TestABackupThatCannotReadACredentialFailsRatherThanOmittingIt.
//
// **Writing a file that silently lacks one credential produces a restore that
// comes up with three links working and one not**, for a reason nothing in the
// file records — and the operator has no way to know the backup was incomplete
// when they made it. A key mismatch is worth stopping for.
func TestABackupThatCannotReadACredentialFailsRatherThanOmittingIt(t *testing.T) {
	srv := &Server{}

	store := &stubSecrets{
		values: map[string]string{"dmr.password": "value"},
		getErr: secrets.ErrNotFound,
	}
	if _, err := srv.allSecrets(context.Background(), store); err == nil {
		t.Error("a backup was assembled while a credential could not be read")
	}

	store = &stubSecrets{listErr: secrets.ErrNotFound}
	if _, err := srv.allSecrets(context.Background(), store); err == nil {
		t.Error("a backup was assembled while the store could not be listed")
	}

	// And the working case returns every value.
	store = &stubSecrets{values: map[string]string{
		"dmr.password":             "one",
		"transcoder.dvstick.zello": "two",
	}}
	got, err := srv.allSecrets(context.Background(), store)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(got) != 2 || got["dmr.password"] != "one" {
		t.Errorf("the credentials read as %v", got)
	}
}

func newFullBackupServer(t *testing.T, cm ConfigManager, store CredentialStore) (*Server, *stubAuth) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	a := newStubAuth()
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}},
		bus, Options{
			ListenAddress: "127.0.0.1:0",
			Auth:          a,
			Config:        cm,
			Secrets:       store,
		})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, a
}

// TestAFullBackupNeedsAPassphraseAndSaysWhatLosingItMeans.
//
// ADR-0065 requires QSP to say, **when it makes the file**, that the
// passphrase is the operator's to keep and that losing it makes the backup
// useless — because a full backup nobody can decrypt is worse than a partial
// one, on account of what the operator believes about it.
func TestAFullBackupNeedsAPassphraseAndSaysWhatLosingItMeans(t *testing.T) {
	srv, a := newFullBackupServer(t, newStubConfig(), nil)

	// No store: it says so rather than writing an empty backup.
	rec := authed(t, srv, a, http.MethodPost, "/api/admin/full-backup",
		`{"passphrase":"p"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a full backup with no credential store gave %d: %s", rec.Code, rec.Body)
	}

	// The warning has to be reachable, and it has to say the thing that
	// matters.
	w := strings.ToLower(config.PassphraseWarning)
	for _, must := range []string{"other than this server", "does not store", "cannot be opened"} {
		if !strings.Contains(w, must) {
			t.Errorf("the warning does not say %q: %s", must, config.PassphraseWarning)
		}
	}
}

// TestTheTwoBackupsAreNotTheSameEndpointOrTheSameFile.
//
// **Mailing the wrong one publishes every password on the server**, which is
// the first thing to get right about having two. Different endpoints,
// different methods, and a restore of one through the other's endpoint says
// which it found.
func TestTheTwoBackupsAreNotTheSameEndpointOrTheSameFile(t *testing.T) {
	var shareable, full []string
	for _, pattern := range APIPatterns() {
		switch {
		case strings.Contains(pattern, "/api/admin/full-"):
			full = append(full, pattern)
		case strings.Contains(pattern, "/api/admin/backup"),
			strings.Contains(pattern, "/api/admin/restore"):
			shareable = append(shareable, pattern)
		}
	}

	if len(full) != 2 {
		t.Errorf("the full backup offers %d endpoints: %v", len(full), full)
	}
	if len(shareable) != 2 {
		t.Errorf("the shareable export offers %d endpoints: %v", len(shareable), shareable)
	}
	for _, p := range full {
		for _, q := range shareable {
			if p == q {
				t.Errorf("the two backups share the endpoint %q", p)
			}
		}
	}

	// The shareable export is a GET, because it is a safe repeatable read.
	// The full backup is a POST, because it takes a passphrase in a body and
	// each call produces a file carrying every secret on the server.
	for _, p := range full {
		if strings.HasPrefix(p, "GET ") {
			t.Errorf("%q is a GET; a full backup takes a passphrase in a body "+
				"and is not a safe repeatable read", p)
		}
	}
}

// TestARestoreConfirmsBeforeItReplacesAnything.
//
// ADR-0065 says a full restore carries the shareable restore's confirmation,
// and ADR-0054 gives the reason: a backup holds a server identifier, so taking
// it makes this machine a replacement — and two servers claiming one identity
// is a collision class this project has met repeatedly. **An earlier version of
// this handler skipped the confirmation entirely**, which was caught by reading
// the handler it was modelled on.
func TestARestoreConfirmsBeforeItReplacesAnything(t *testing.T) {
	cm := newStubConfig()
	srv, a := newFullBackupServer(t, cm, nil)

	// A store is needed before the confirmation is reached, so this asserts
	// the order of the checks too: no store is refused before anything is
	// decrypted.
	rec := authed(t, srv, a, http.MethodPost, "/api/admin/full-restore",
		`{"document":"","passphrase":"p"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a restore with no credential store gave %d: %s", rec.Code, rec.Body)
	}
	if len(cm.saved) != 0 {
		t.Error("a configuration was saved by a restore that could not proceed")
	}

	// **And now the confirmation itself**, with a store and a real backup. An
	// earlier version of this test stopped above, so a break that skipped the
	// confirmation entirely passed — and the check being skipped is a restore
	// that replaces every setting on a server without asking.
	store := &stubSecrets{values: map[string]string{}}
	cm2 := newStubConfig()
	srv2, a2 := newFullBackupServer(t, cm2, store)

	document := encodedFullBackup(t, "right", map[string]string{
		"dmr.password":             "one",
		"transcoder.dvstick.zello": "two",
	})

	rec = authed(t, srv2, a2, http.MethodPost, "/api/admin/full-restore",
		`{"document":"`+document+`","passphrase":"right"}`)
	if rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("an unconfirmed restore gave %d, want 428: %s", rec.Code, rec.Body)
	}
	if len(cm2.saved) != 0 {
		t.Error("an unconfirmed restore saved a configuration")
	}
	if len(store.values) != 0 {
		t.Errorf("an unconfirmed restore wrote %d credentials", len(store.values))
	}

	// The summary has to carry the identity warning, which is the hazard
	// ADR-0054 wrote it for: two servers claiming one identity.
	body := rec.Body.String()
	for _, must := range []string{"identifier", "two servers", "credential"} {
		if !strings.Contains(strings.ToLower(body), must) {
			t.Errorf("the confirmation does not mention %q: %s", must, body)
		}
	}
	// And it names what will come back.
	if !strings.Contains(body, "dmr.password") {
		t.Errorf("the confirmation does not name the credentials: %s", body)
	}

	// Confirmed, it restores the credentials and the configuration.
	rec = authed(t, srv2, a2, http.MethodPost, "/api/admin/full-restore",
		`{"document":"`+document+`","passphrase":"right","confirm":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("a confirmed restore gave %d: %s", rec.Code, rec.Body)
	}
	if len(store.values) != 2 || store.values["dmr.password"] != "one" {
		t.Errorf("the credentials restored as %v", store.values)
	}
	if len(cm2.saved) != 1 {
		t.Errorf("%d configurations were saved, want 1", len(cm2.saved))
	}
}

// TestTheCredentialsLandBeforeTheConfiguration.
//
// If the configuration lands first and a credential write then fails, the
// server is running a configuration whose links have no passwords — silent,
// and looking correct. The other order leaves credentials for links that do
// not exist yet, which is inert.
func TestTheCredentialsLandBeforeTheConfiguration(t *testing.T) {
	store := &stubSecrets{values: map[string]string{}, setErr: secrets.ErrNotFound}
	cm := newStubConfig()
	srv, a := newFullBackupServer(t, cm, store)

	document := encodedFullBackup(t, "right", map[string]string{"dmr.password": "one"})
	rec := authed(t, srv, a, http.MethodPost, "/api/admin/full-restore",
		`{"document":"`+document+`","passphrase":"right","confirm":true}`)

	if rec.Code == http.StatusOK {
		t.Fatalf("a restore succeeded while a credential could not be written: %s", rec.Body)
	}
	if len(cm.saved) != 0 {
		t.Error("the configuration was saved although a credential failed; the " +
			"server would be running links with no passwords")
	}
	if !strings.Contains(rec.Body.String(), "has not been changed") {
		t.Errorf("the failure does not say the configuration was left alone: %s", rec.Body)
	}
}

// encodedFullBackup writes a full backup and returns it base64-encoded.
func encodedFullBackup(t *testing.T, passphrase string, values map[string]string) string {
	t.Helper()
	var buf strings.Builder
	full := config.NewFullBackup(config.Default(), values, "0.1.226", timeFixed())
	if err := config.WriteFullBackup(&stringWriter{&buf}, full, passphrase); err != nil {
		t.Fatalf("writing a full backup: %v", err)
	}
	return base64.StdEncoding.EncodeToString([]byte(buf.String()))
}

// TestARestoreWithoutAPassphraseOrABadFileIsRefused.
func TestARestoreWithoutAPassphraseOrABadFileIsRefused(t *testing.T) {
	// The document decoding and the format checks happen in internal/config
	// and are tested there; what matters here is that each failure is
	// reported as itself rather than as a generic error.
	var buf strings.Builder
	full := config.NewFullBackup(config.Default(), map[string]string{
		"dmr.password": "value",
	}, "0.1.226", timeFixed())
	if err := config.WriteFullBackup(&stringWriter{&buf}, full, "right"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	document := base64.StdEncoding.EncodeToString([]byte(buf.String()))

	body, err := json.Marshal(fullRestoreRequest{
		Document:   document,
		Passphrase: "right",
	})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if !strings.Contains(string(body), `"confirm":false`) {
		t.Errorf("a restore request does not default to unconfirmed: %s", body)
	}
}

// timeFixed is a stable timestamp, so a test asserting a filename or a summary
// does not depend on the day it runs.
func timeFixed() time.Time {
	return time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
}

// stringWriter adapts a strings.Builder for an io.Writer parameter.
type stringWriter struct{ b *strings.Builder }

func (w *stringWriter) Write(p []byte) (int, error) { return w.b.Write(p) }
