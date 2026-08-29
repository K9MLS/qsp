package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/console"
	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/peers"
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

// TestSavedJoinSettingsTakeEffectWithoutARestart is the bug this file's
// sibling exposed on a live server: the join page kept the old network name
// after a save that reported no restart was needed.
//
// Two individually reasonable statements that together were a lie — the
// operator was told the change was live, and it was not.
func TestSavedJoinSettingsTakeEffectWithoutARestart(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Join:          JoinSettings{NetworkName: "Old Name"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Before: the constructed value.
	if got := srv.Join().NetworkName; got != "Old Name" {
		t.Fatalf("network name is %q before any save", got)
	}

	srv.ApplyConfig(JoinSettings{NetworkName: "BCARA"}, MapSettings{TileURL: "x"}, true)

	if got := srv.Join().NetworkName; got != "BCARA" {
		t.Errorf("network name is %q after a save, want BCARA", got)
	}
	if got := srv.MapSettings().TileURL; got != "x" {
		t.Errorf("map settings did not update: %q", got)
	}
	if !srv.Forwarding() {
		t.Error("the forwarding flag did not update")
	}
}

// TestTheJoinEndpointServesTheAppliedSettings covers the path a member
// actually reads, rather than the accessor alone.
func TestTheJoinEndpointServesTheAppliedSettings(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Join:          JoinSettings{NetworkName: "Old Name"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.ApplyConfig(JoinSettings{NetworkName: "BCARA"}, MapSettings{}, false)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/join", nil))

	var body struct {
		Settings struct {
			NetworkName string `json:"network_name"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Settings.NetworkName != "BCARA" {
		t.Errorf("the join page serves %q", body.Settings.NetworkName)
	}
}

// TestNothingAppliedMeansTheConstructedValues. An instance that has never
// saved must behave exactly as it did before any of this existed.
func TestNothingAppliedMeansTheConstructedValues(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Join:          JoinSettings{NetworkName: "Configured"},
		Map:           MapSettings{TileURL: "tiles"},
		Forwarding:    true,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if srv.Join().NetworkName != "Configured" {
		t.Error("the constructed join settings were lost")
	}
	if srv.MapSettings().TileURL != "tiles" {
		t.Error("the constructed map settings were lost")
	}
	if !srv.Forwarding() {
		t.Error("the constructed forwarding flag was lost")
	}
}

// TestTheTileOriginIsAllowedByThePolicy. `img-src 'self'` blocked every map
// tile: the browser refused them silently and the map drew an empty frame,
// which is the policy working and the feature not.
func TestTheTileOriginIsAllowedByThePolicy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tileURL string
		want    string
	}{
		{"openstreetmap", "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
			"https://tile.openstreetmap.org"},
		{"a self-hosted server", "http://tiles.lan:8080/{z}/{x}/{y}.png",
			"http://tiles.lan:8080"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.NewBus(nil, events.Options{})
			t.Cleanup(bus.Close)
			srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
				ListenAddress: "127.0.0.1:0",
				Map:           MapSettings{TileURL: tc.tileURL},
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
			csp := rec.Header().Get("Content-Security-Policy")

			if !strings.Contains(csp, "img-src 'self' data: "+tc.want+";") {
				t.Errorf("the policy does not allow %s:\n  %s", tc.want, csp)
			}
			// And nothing else was widened.
			if !strings.Contains(csp, "script-src 'self';") {
				t.Errorf("script-src was changed: %s", csp)
			}
			if strings.Contains(csp, "*") {
				t.Errorf("the policy contains a wildcard: %s", csp)
			}
		})
	}
}

// TestNoTilesMeansTheOriginalPolicy. An operator who clears the tile URL gets
// the strict policy back unchanged.
func TestNoTilesMeansTheOriginalPolicy(t *testing.T) {
	for _, tileURL := range []string{"", "   ", "not a url at all", "ftp://tiles/{z}/{x}/{y}.png"} {
		bus := events.NewBus(nil, events.Options{})
		srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
			ListenAddress: "127.0.0.1:0",
			Map:           MapSettings{TileURL: tileURL},
		})
		bus.Close()
		if err != nil {
			t.Fatalf("New: %v", err)
		}

		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		csp := rec.Header().Get("Content-Security-Policy")

		if !strings.Contains(csp, "img-src 'self' data:;") {
			t.Errorf("tile URL %q widened the policy: %s", tileURL, csp)
		}
	}
}

// TestTheAccessPageIsReachable covers the redirect and the asset together.
func TestTheAccessPageIsReachable(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	assets, err := console.Assets()
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		ConsoleAssets: assets,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/access", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("/access returned %d, want a redirect", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/access.html", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/access.html returned %d", rec.Code)
	}
}

// TestConsoleAssetsAreRevalidated. Embedded files carry no modification time,
// so nothing tells a browser whether its copy is current — and an operator who
// upgrades gets the new server with the old console, indefinitely.
func TestConsoleAssetsAreRevalidated(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	assets, err := console.Assets()
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		ConsoleAssets: assets,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, path := range []string{"/console.js", "/map.js", "/console.css", "/"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s returned %d", path, rec.Code)
		}
		if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
			t.Errorf("%s has Cache-Control %q, want no-cache", path, got)
		}
	}
}

// stubLogins reports refusals without a Master.
type stubLogins struct{ failures []peers.LoginFailure }

func (s stubLogins) LoginFailures(time.Time) []peers.LoginFailure { return s.failures }
func (s stubLogins) BlockedSources(time.Time) int                 { return len(s.failures) }

// TestRefusedLoginsReachTheConsole. An operator learning about a run of failed
// logins from a member's phone call is the case this exists to end.
func TestRefusedLoginsReachTheConsole(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Logins: stubLogins{failures: []peers.LoginFailure{{
			Address: "203.0.113.5", RepeaterID: 3155413,
			Reason: "6 wrong password", Failures: 6,
			LockedUntil: time.Now().Add(5 * time.Minute),
		}}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))

	var body struct {
		Refused []map[string]any `json:"refused"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Refused) != 1 {
		t.Fatalf("%d refusals reported, want 1", len(body.Refused))
	}
	if body.Refused[0]["address"] != "203.0.113.5" {
		t.Errorf("the refusal does not name the source: %v", body.Refused[0])
	}
	if body.Refused[0]["reason"] == "" {
		t.Error("the refusal does not say what failed")
	}
}

// TestNoRefusalsMeansNoField, so a console with nothing to report shows
// nothing rather than an empty panel.
func TestNoRefusalsMeansNoField(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Logins:        stubLogins{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))
	if strings.Contains(rec.Body.String(), `"refused"`) {
		t.Error("an instance refusing nothing reported a refused field")
	}
}

// TestTheNetworkPageIsReachable covers the redirect and the asset together.
func TestTheNetworkPageIsReachable(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	assets, err := console.Assets()
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		ConsoleAssets: assets,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/network", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("/network returned %d, want a redirect", rec.Code)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/network.html", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/network.html returned %d", rec.Code)
	}
}

// TestACallsignIsOnlyClaimedWhenKnown.
//
// A hotspot announces its own callsign at registration, and on most hotspots
// the operator's radio carries the same DMR ID — so QSP can name that radio
// without anybody's database. A radio behind a hotspot with a different ID is
// left as a number: QSP knows which hotspot carried it and nothing about whose
// radio it is, and labelling it with the hotspot owner's callsign would be
// worse than the number.
func TestACallsignIsOnlyClaimedWhenKnown(t *testing.T) {
	views := []CallView{
		{Source: 3155413, SourceName: "KB9TYC", Target: 2, Group: true},
		{Source: 3155408, Target: 2, Group: true},
	}
	body, err := json.Marshal(views)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var out []map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out[0]["source_name"] != "KB9TYC" {
		t.Errorf("a known callsign was not carried: %v", out[0])
	}
	if _, ok := out[1]["source_name"]; ok {
		t.Error("an unknown radio was given a callsign field")
	}
}

// TestAGroupTargetIsNeverACallsign. A talkgroup number is not a radio ID, and
// looking one up finds whichever radio happens to share the number.
func TestAGroupTargetIsNeverACallsign(t *testing.T) {
	view := CallView{Source: 3132910, Target: 3155413, Group: true}
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(body), "target_name") {
		t.Error("a group call carried a target callsign")
	}
}

// TestTheBridgesPageIsReachable covers the redirect and the asset together.
func TestTheBridgesPageIsReachable(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	assets, err := console.Assets()
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		ConsoleAssets: assets,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bridges", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("/bridges returned %d, want a redirect", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/bridges.html", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/bridges.html returned %d", rec.Code)
	}
}

// TestAVersionCanBeReadBack. Restoring one needs its document, and the list
// deliberately omits documents — fifty versions carrying fifty configurations
// is a response nobody reads.
func TestAVersionCanBeReadBack(t *testing.T) {
	cm := newStubConfig()
	older := config.Default()
	older.Events.HistorySize = 111
	cm.versions = []config.Version{{
		Number: 4, Author: "K9MLS", Summary: "before the net",
		CreatedAt: time.Now(), Config: older,
	}}
	srv, a := newConfigServer(t, cm, nil)

	res := authed(t, srv, a, http.MethodGet, "/api/config/versions/4", "")
	if res.Code != http.StatusOK {
		t.Fatalf("returned %d: %s", res.Code, res.Body)
	}

	var body struct {
		Number  int64           `json:"number"`
		Author  string          `json:"author"`
		Config  config.Config   `json:"config"`
		Changes []config.Change `json:"changes"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Number != 4 || body.Author != "K9MLS" {
		t.Errorf("wrong version returned: %+v", body)
	}
	if body.Config.Events.HistorySize != 111 {
		t.Errorf("the document was not carried: %+v", body.Config.Events)
	}
	// The difference from what is running, so an operator sees what a restore
	// would do before doing it rather than after.
	if len(body.Changes) == 0 {
		t.Error("no changes were reported against the running configuration")
	}
}

func TestAnUnknownVersionIsNotFound(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, nil)

	res := authed(t, srv, a, http.MethodGet, "/api/config/versions/99", "")
	if res.Code != http.StatusNotFound {
		t.Errorf("returned %d, want 404: %s", res.Code, res.Body)
	}
}

func TestAVersionMustBeANumber(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, nil)

	for _, bad := range []string{"nonsense", "0", "-1"} {
		res := authed(t, srv, a, http.MethodGet, "/api/config/versions/"+bad, "")
		if res.Code != http.StatusBadRequest && res.Code != http.StatusNotFound {
			t.Errorf("version %q returned %d", bad, res.Code)
		}
	}
}

// TestReadingAVersionNeedsASession. The document names the password file and
// the database, exactly as the running configuration does.
func TestReadingAVersionNeedsASession(t *testing.T) {
	srv, _ := newConfigServer(t, newStubConfig(), nil)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/config/versions/1", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("returned %d, want 401", rec.Code)
	}
}

// TestTheHistoryPageIsReachable covers the redirect and the asset.
func TestTheHistoryPageIsReachable(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	assets, err := console.Assets()
	if err != nil {
		t.Fatalf("assets: %v", err)
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		ConsoleAssets: assets,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/history", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("/history returned %d, want a redirect", rec.Code)
	}
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/history.html", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/history.html returned %d", rec.Code)
	}
}
