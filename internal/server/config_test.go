package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// stubConfig is the configuration manager, without a file or a database.
type stubConfig struct {
	current  config.Config
	readOnly error
	saveErr  error
	versions []config.Version
	// saved records what Save was asked to write.
	saved   []config.Config
	authors []string
}

func newStubConfig() *stubConfig {
	return &stubConfig{current: config.Default()}
}

func (c *stubConfig) Current() config.Config { return c.current }
func (c *stubConfig) Writable() error        { return c.readOnly }

func (c *stubConfig) Save(_ context.Context, cfg config.Config, author, summary string) (config.Version, error) {
	if c.saveErr != nil {
		return config.Version{}, c.saveErr
	}
	c.saved = append(c.saved, cfg)
	c.authors = append(c.authors, author)
	c.current = cfg
	return config.Version{Number: int64(len(c.saved)), Author: author, Summary: summary}, nil
}

func (c *stubConfig) Versions(_ context.Context, limit int) ([]config.Version, error) {
	if limit < len(c.versions) {
		return c.versions[:limit], nil
	}
	return c.versions, nil
}

func (c *stubConfig) Version(_ context.Context, number int64) (config.Version, bool, error) {
	for _, v := range c.versions {
		if v.Number == number {
			return v, true, nil
		}
	}
	return config.Version{}, false, nil
}

// recordingAudit captures events so the tests can assert on the actor.
type recordingAudit struct{ events []audit.Event }

func (a *recordingAudit) Record(_ context.Context, e audit.Event) error {
	a.events = append(a.events, e)
	return nil
}

func newConfigServer(t *testing.T, cm ConfigManager, rec audit.Recorder) (*Server, *stubAuth) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	a := newStubAuth()
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Auth:          a,
		Config:        cm,
		Audit:         rec,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, a
}

// authed sends a request carrying a session and a same-origin header.
func authed(t *testing.T, srv *Server, a *stubAuth, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	c := sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password))

	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	req.AddCookie(c)
	req.Host = "qsp.example"
	req.Header.Set("Origin", "http://qsp.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestConfigRequiresASession. The document names the password file and the
// database — not secrets, but a map of the host.
func TestConfigRequiresASession(t *testing.T) {
	srv, _ := newConfigServer(t, newStubConfig(), nil)

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/config"},
		{http.MethodPost, "/api/config"},
		{http.MethodGet, "/api/config/versions"},
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s returned %d, want 401", tc.method, tc.path, rec.Code)
		}
	}
}

func TestGetConfigReturnsTheRunningConfiguration(t *testing.T) {
	cm := newStubConfig()
	cm.current.Events.HistorySize = 321
	srv, a := newConfigServer(t, cm, nil)

	rec := authed(t, srv, a, http.MethodGet, "/api/config", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", rec.Code, rec.Body)
	}
	var body configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Config.Events.HistorySize != 321 {
		t.Errorf("returned a different configuration: %+v", body.Config.Events)
	}
	if !body.Writable {
		t.Errorf("a writable instance reported read-only: %q", body.ReadOnlyReason)
	}
}

// TestReadOnlyIsReportedBeforeAFormIsFilledIn. Finding out at the moment
// somebody presses save is the difference between a posture and a fault.
func TestReadOnlyIsReportedBeforeAFormIsFilledIn(t *testing.T) {
	cm := newStubConfig()
	cm.readOnly = config.ErrNotWritable
	srv, a := newConfigServer(t, cm, nil)

	rec := authed(t, srv, a, http.MethodGet, "/api/config", "")
	var body configResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Writable {
		t.Error("a read-only instance reported as writable")
	}
	if body.ReadOnlyReason == "" {
		t.Error("no reason was given for the instance being read-only")
	}
}

func TestSaveRecordsAndReportsTheChanges(t *testing.T) {
	cm := newStubConfig()
	rec := &recordingAudit{}
	srv, a := newConfigServer(t, cm, rec)

	next := config.Default()
	next.Events.HistorySize = 512
	body, _ := json.Marshal(saveRequest{Config: next, Summary: "bigger history"})

	res := authed(t, srv, a, http.MethodPost, "/api/config", string(body))
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	var out saveResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Version == 0 {
		t.Error("no version number was returned")
	}
	if len(out.Changes) == 0 {
		t.Error("the response listed no changes")
	}
	if len(cm.saved) != 1 {
		t.Fatalf("Save was called %d times", len(cm.saved))
	}
	// The actor audit_events has been waiting for.
	if len(cm.authors) != 1 || cm.authors[0] != "K9MLS" {
		t.Errorf("saved with author %v", cm.authors)
	}
	if len(rec.events) != 1 || rec.events[0].Actor != "K9MLS" {
		t.Errorf("the audit event names %+v", rec.events)
	}
	if rec.events[0].Action != audit.ActionConfigChanged {
		t.Errorf("audit action is %q", rec.events[0].Action)
	}
}

// TestSavingAnUnchangedConfigurationRecordsNothing. A form saved without an
// edit should not fill the history with identical versions.
func TestSavingAnUnchangedConfigurationRecordsNothing(t *testing.T) {
	cm := newStubConfig()
	rec := &recordingAudit{}
	srv, a := newConfigServer(t, cm, rec)

	body, _ := json.Marshal(saveRequest{Config: cm.current})
	res := authed(t, srv, a, http.MethodPost, "/api/config", string(body))

	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}
	if len(cm.saved) != 0 {
		t.Error("an unchanged configuration was written")
	}
	if len(rec.events) != 0 {
		t.Error("an unchanged configuration produced an audit event")
	}
}

// TestAnInvalidConfigurationReportsEveryProblem. An operator fixing a form
// should see all of it rather than discovering the next problem each time they
// press save.
func TestAnInvalidConfigurationReportsEveryProblem(t *testing.T) {
	cm := newStubConfig()
	cm.saveErr = &config.ValidationError{Errors: []config.FieldError{
		{Field: "server.listen_address", Problem: "must not be empty", Fix: "use 127.0.0.1:8080"},
		{Field: "database.dsn", Problem: "must not be empty", Fix: "use qsp.db"},
	}}
	rec := &recordingAudit{}
	srv, a := newConfigServer(t, cm, rec)

	next := config.Default()
	next.Events.HistorySize = 999
	body, _ := json.Marshal(saveRequest{Config: next})

	res := authed(t, srv, a, http.MethodPost, "/api/config", string(body))
	if res.Code != http.StatusBadRequest {
		t.Fatalf("returned %d, want 400: %s", res.Code, res.Body)
	}

	var out validationResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Fields) != 2 {
		t.Errorf("reported %d problems, want both", len(out.Fields))
	}
	for _, p := range out.Fields {
		if p.Fix == "" {
			t.Errorf("problem on %s has no suggested fix", p.Field)
		}
	}
	// A refused save is still an event: an operator who could not save is a
	// fact worth having, and no record would look like nobody tried.
	if len(rec.events) != 1 || rec.events[0].Outcome != audit.OutcomeFailure {
		t.Errorf("a refused save recorded %+v", rec.events)
	}
}

// TestAReadOnlyInstanceRefusesWithAConflict. Nothing failed; the instance is
// simply not one that can be configured this way.
func TestAReadOnlyInstanceRefusesWithAConflict(t *testing.T) {
	cm := newStubConfig()
	cm.saveErr = config.ErrNotWritable
	srv, a := newConfigServer(t, cm, nil)

	next := config.Default()
	next.Events.HistorySize = 999
	body, _ := json.Marshal(saveRequest{Config: next})

	res := authed(t, srv, a, http.MethodPost, "/api/config", string(body))
	if res.Code != http.StatusConflict {
		t.Errorf("returned %d, want 409: %s", res.Code, res.Body)
	}
}

// TestSaveNamesTheSettingsThatNeedARestart. A bare "restart required" tells an
// operator to interrupt their network without saying what for.
func TestSaveNamesTheSettingsThatNeedARestart(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, nil)

	next := config.Default()
	next.Server.ListenAddress = "0.0.0.0:9000"
	body, _ := json.Marshal(saveRequest{Config: next})

	res := authed(t, srv, a, http.MethodPost, "/api/config", string(body))
	var out saveResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.NeedsRestart) == 0 {
		t.Fatal("changing the listen address did not report a restart")
	}
	var named bool
	for _, f := range out.NeedsRestart {
		if f == "server.listen_address" {
			named = true
		}
	}
	if !named {
		t.Errorf("the restart list does not name the field: %v", out.NeedsRestart)
	}
}

// TestALiveChangeAsksForNoRestart is the case that matters daily: adding a
// bridge must not tell a club to restart mid-net.
func TestALiveChangeAsksForNoRestart(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, nil)

	next := config.Default()
	next.DMR.Join.NetworkName = "BCARA"
	body, _ := json.Marshal(saveRequest{Config: next})

	res := authed(t, srv, a, http.MethodPost, "/api/config", string(body))
	var out saveResponse
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.NeedsRestart) != 0 {
		t.Errorf("a live change asked for a restart: %v", out.NeedsRestart)
	}
}

func TestVersionsAreListedNewestFirst(t *testing.T) {
	cm := newStubConfig()
	cm.versions = []config.Version{
		{Number: 3, Author: "K9MLS", Summary: "third", CreatedAt: time.Now()},
		{Number: 2, Author: "W5ABC", CreatedAt: time.Now()},
		{Number: 1, Author: "K9MLS", CreatedAt: time.Now()},
	}
	srv, a := newConfigServer(t, cm, nil)

	res := authed(t, srv, a, http.MethodGet, "/api/config/versions", "")
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	var out struct {
		Versions []versionSummary `json:"versions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Versions) != 3 || out.Versions[0].Number != 3 {
		t.Errorf("history is %+v", out.Versions)
	}
	// The documents are absent on purpose: fifty versions carrying fifty
	// configurations is a large response nobody reads.
	if strings.Contains(res.Body.String(), "listen_address") {
		t.Error("the history carries whole configuration documents")
	}
}

// TestACrossOriginSaveIsRefused. requireSession enforces this, and a save is
// exactly the request it matters for.
func TestACrossOriginSaveIsRefused(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, nil)
	c := sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password))

	next := config.Default()
	next.Events.HistorySize = 999
	body, _ := json.Marshal(saveRequest{Config: next})

	req := httptest.NewRequest(http.MethodPost, "/api/config", strings.NewReader(string(body)))
	req.AddCookie(c)
	req.Host = "qsp.example"
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("returned %d, want 403", rec.Code)
	}
	if len(cm.saved) != 0 {
		t.Error("a cross-origin request saved a configuration")
	}
}

func TestAnInstanceWithNoConfigurationSaysSo(t *testing.T) {
	srv, a := newConfigServer(t, nil, nil)

	res := authed(t, srv, a, http.MethodGet, "/api/config", "")
	if res.Code != http.StatusServiceUnavailable {
		t.Errorf("returned %d, want 503: %s", res.Code, res.Body)
	}
}

func TestSaveRejectsMalformedJSON(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, nil)

	res := authed(t, srv, a, http.MethodPost, "/api/config", "not a configuration")
	if res.Code != http.StatusBadRequest {
		t.Errorf("returned %d, want 400", res.Code)
	}
	if len(cm.saved) != 0 {
		t.Error("malformed JSON reached the writer")
	}
}
