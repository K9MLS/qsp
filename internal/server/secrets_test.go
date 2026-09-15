package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/database"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/secrets"
)

// The credential endpoints: ADR-0065's "all configuration in the console,
// including secrets", with the value still living outside the configuration
// document.

// newSecretServer builds a server with a credential store, reusing the
// package's existing helpers rather than inventing another.
func newSecretServer(t *testing.T, store *secrets.Store) (*Server, *stubAuth) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	a := newStubAuth()
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}},
		bus, Options{
			ListenAddress: "127.0.0.1:0",
			Auth:          a,
			Secrets:       store,
		})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, a
}

// secretStoreFor builds a store backed by a real database, or skips.
//
// The development container has no SQLite driver — the same reason seven
// cmd/qsp tests cannot run there — so these skip rather than fail and run on a
// machine with one.
func secretStoreFor(t *testing.T) *secrets.Store {
	t.Helper()
	if !database.DriverRegistered("sqlite") {
		t.Skip("no sqlite driver in this build; these tests run where one is")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, nil, database.Options{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "qsp.db"),
	})
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	store, err := secrets.Open(secrets.Options{
		DB: db.SQL(), KeyPath: filepath.Join(t.TempDir(), "secrets.key"),
	})
	if err != nil {
		t.Fatalf("opening a store: %v", err)
	}
	return store
}

// TestNoEndpointReturnsASecret is the property the whole design turns on.
//
// **A page that displays a password leaks it to whoever is looking at the
// screen.** There is deliberately no endpoint that returns a value, and this
// asserts it from the route table rather than from a reading of the handlers —
// so a later addition has to break this test to happen.
func TestNoEndpointReturnsASecret(t *testing.T) {
	var reads []string
	for _, pattern := range APIPatterns() {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			t.Fatalf("a route pattern has no method: %q", pattern)
		}
		if method == "GET" && strings.HasPrefix(path, "/api/secrets") {
			reads = append(reads, pattern)
		}
	}

	// Exactly one GET, and it is the listing.
	if len(reads) != 1 {
		t.Fatalf("the credential routes offer %d GET endpoints: %v", len(reads), reads)
	}
	if reads[0] != "GET /api/secrets" {
		t.Errorf("the only credential GET is %q; anything with a name in the "+
			"path would be a way to read a value", reads[0])
	}

	// And the type the listing returns has nowhere to put one, which is the
	// real guarantee: a handler cannot be changed to include a value without
	// changing the type.
	//
	// **Checked by field name and not by substring.** An earlier version
	// searched the whole document for "password" and failed on the perfectly
	// correct record for a secret *named* `dmr.password` — the value was never
	// there, and the test could not tell a field from a name.
	raw, err := json.Marshal(secretRecord{Name: "dmr.password"})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshalling: %v", err)
	}
	for field := range fields {
		switch field {
		case "name", "updated_at", "updated_by":
		default:
			t.Errorf("a credential record carries an unexpected field %q; the "+
				"three it may have are a name and when and by whom it changed",
				field)
		}
	}
	for _, forbidden := range []string{"value", "password", "secret", "token"} {
		if _, present := fields[forbidden]; present {
			t.Errorf("a credential record carries a %q field", forbidden)
		}
	}
}

// TestACredentialGoesInAndIsListedWithoutItsValue.
func TestACredentialGoesInAndIsListedWithoutItsValue(t *testing.T) {
	store := secretStoreFor(t)
	srv, a := newSecretServer(t, store)

	const name, value = "transcoder.dvstick.zello", "unmistakable-secret-value"

	rec := authed(t, srv, a, http.MethodPut, "/api/secrets/"+name,
		`{"value":"`+value+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("storing a credential gave %d: %s", rec.Code, rec.Body)
	}

	// The value is retrievable through the store, which is what the connector
	// will do.
	got, err := store.Get(context.Background(), name)
	if err != nil {
		t.Fatalf("reading it back: %v", err)
	}
	if got != value {
		t.Errorf("the stored credential is %q", got)
	}

	// And the listing names it without carrying it.
	rec = authed(t, srv, a, http.MethodGet, "/api/secrets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("listing gave %d: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, name) {
		t.Errorf("the listing does not name the credential: %s", body)
	}
	if strings.Contains(body, value) {
		t.Errorf("the listing carries the value: %s", body)
	}

	var listed secretsResponse
	if err := json.Unmarshal([]byte(body), &listed); err != nil {
		t.Fatalf("decoding the listing: %v", err)
	}
	if len(listed.Secrets) != 1 {
		t.Fatalf("%d credentials listed, want 1", len(listed.Secrets))
	}
	if listed.Secrets[0].UpdatedAt.IsZero() {
		t.Error("the listed credential has no date; one nobody can date is one " +
			"nobody trusts")
	}
	if listed.Secrets[0].UpdatedBy == "" {
		t.Error("the listed credential records no author")
	}
}

// TestAPageThatCreatesACredentialCanRemoveIt.
//
// An operator whose first attempt went wrong should not be left with a stored
// secret they can only delete by opening the database.
func TestAPageThatCreatesACredentialCanRemoveIt(t *testing.T) {
	store := secretStoreFor(t)
	srv, a := newSecretServer(t, store)
	const name = "transcoder.dvstick.zello"

	rec := authed(t, srv, a, http.MethodPut, "/api/secrets/"+name, `{"value":"v"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("storing gave %d: %s", rec.Code, rec.Body)
	}

	rec = authed(t, srv, a, http.MethodDelete, "/api/secrets/"+name, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("removing gave %d: %s", rec.Code, rec.Body)
	}

	has, err := store.Has(context.Background(), name)
	if err != nil {
		t.Fatalf("checking: %v", err)
	}
	if has {
		t.Error("the credential survived its removal")
	}

	// Removing it twice is not an error: the caller wanted it gone.
	rec = authed(t, srv, a, http.MethodDelete, "/api/secrets/"+name, "")
	if rec.Code != http.StatusNoContent {
		t.Errorf("removing a missing credential gave %d", rec.Code)
	}
}

// TestAValueNeverTravelsInAUrl.
//
// A query string is logged by every proxy in the way, written into an access
// log and kept in a browser's history. So the name comes from the path and the
// value from the body — and a route carrying a value would have to be added to
// break this.
func TestAValueNeverTravelsInAUrl(t *testing.T) {
	for _, pattern := range APIPatterns() {
		if !strings.Contains(pattern, "/api/secrets") {
			continue
		}
		lower := strings.ToLower(pattern)
		for _, forbidden := range []string{"{value}", "value=", "{password}", "{token}"} {
			if strings.Contains(lower, forbidden) {
				t.Errorf("the route %q carries a value in its path", pattern)
			}
		}
	}

	// The request type puts it in the body, and has exactly one field.
	raw, err := json.Marshal(setSecretRequest{Value: "v"})
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if string(raw) != `{"value":"v"}` {
		t.Errorf("a set request marshals as %s", raw)
	}
}

// TestAnInstanceWithNoStoreSaysSoRatherThanDiscarding.
//
// **The failure this prevents is the one the project keeps writing records
// about**: a page that accepts a credential and stores nothing, leaving an
// operator believing the link is configured. A server started without a
// database has nowhere to keep one, and must say that rather than return
// success.
func TestAnInstanceWithNoStoreSaysSoRatherThanDiscarding(t *testing.T) {
	srv, a := newSecretServer(t, nil) // no store

	for _, tc := range []struct {
		method, path string
		body         string
	}{
		{http.MethodGet, "/api/secrets", ""},
		{http.MethodPut, "/api/secrets/dmr.password", `{"value":"v"}`},
		{http.MethodDelete, "/api/secrets/dmr.password", ""},
	} {
		rec := authed(t, srv, a, tc.method, tc.path, tc.body)

		if rec.Code == http.StatusOK || rec.Code == http.StatusNoContent {
			t.Errorf("%s %s succeeded with no store; the credential went nowhere "+
				"and the operator was told it worked", tc.method, tc.path)
			continue
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s gave %d, want 503", tc.method, tc.path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "nowhere to keep") {
			t.Errorf("%s %s does not say why: %s", tc.method, tc.path, rec.Body)
		}
	}
}

// TestACredentialNeedsAName, so an empty path cannot become a row nothing
// finds.
func TestACredentialNeedsAName(t *testing.T) {
	store := secretStoreFor(t)
	srv, a := newSecretServer(t, store)

	rec := authed(t, srv, a, http.MethodPut, "/api/secrets/", `{"value":"v"}`)
	if rec.Code == http.StatusOK {
		t.Error("a credential with no name was stored")
	}
}
