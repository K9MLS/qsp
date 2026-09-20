package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// countingAccounts is an AccountAdmin that can also count sessions.
type countingAccounts struct {
	AccountAdmin
	active int
	err    error
}

func (a *countingAccounts) ActiveSessions(context.Context) (int, error) {
	return a.active, a.err
}

// sessionServer builds a server with a configuration and an account service.
func sessionServer(t *testing.T, cfg *stubConfig, accounts AccountAdmin) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{HistorySize: 8, SubscriberBuffer: 4})
	t.Cleanup(bus.Close)
	opts := Options{Accounts: accounts, ListenAddress: "127.0.0.1:0", ShutdownTimeout: time.Second}
	// A nil *stubConfig in an interface is not a nil interface, and the
	// read-only case turns on Config being genuinely absent.
	if cfg != nil {
		opts.Config = cfg
	}
	s, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, opts)
	if err != nil {
		t.Fatalf("building a server: %v", err)
	}
	return s
}

// asSession makes a request that arrived with a session, as requireSession
// would have left it.
func asSession(method, path string, body any, sess auth.Session) *http.Request {
	var r *http.Request
	if body != nil {
		buf, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	return r.WithContext(withSession(r.Context(), sess))
}

// TestTheSessionsBlockReportsBeforeItEdits covers what the Administration page
// states about logins.
//
// **The page may edit a setting only when it is the page that reports it**
// (ADR-0055), so the report is the part that earns the editor and the part
// worth testing hardest. Three facts the configuration file cannot give an
// operator: which lifetime is in force, whether that is their choice or the
// built-in default, and when the session reading the page ends.
//
// To see rows fail: return DefaultSessionLifetime unconditionally in
// sessionState; drop the Default flag; or read the expiry from the
// configuration rather than from the session.
func TestTheSessionsBlockReportsBeforeItEdits(t *testing.T) {
	now := time.Now().UTC()

	tests := []struct {
		name         string
		lifetime     time.Duration
		active       int
		countErr     error
		expiresAt    time.Time
		wantSeconds  int64
		wantDefault  bool
		wantActive   int
		wantExpiring bool
	}{
		{
			name:        "nothing configured is the built-in default, and says so",
			lifetime:    0,
			wantSeconds: int64(auth.DefaultSessionLifetime.Seconds()),
			wantDefault: true,
		},
		{
			name:        "a configured lifetime is reported as configured",
			lifetime:    24 * time.Hour,
			wantSeconds: 86400,
			wantDefault: false,
		},
		{
			name:        "twelve hours configured is not the default, though it matches it",
			lifetime:    12 * time.Hour,
			wantSeconds: 43200,
			wantDefault: false,
		},
		{
			name:        "the count is what the service reports",
			lifetime:    time.Hour,
			active:      3,
			wantSeconds: 3600,
			wantActive:  3,
		},
		{
			name:        "a count that cannot be read leaves the rest standing",
			lifetime:    time.Hour,
			active:      9,
			countErr:    context.DeadlineExceeded,
			wantSeconds: 3600,
			wantActive:  0,
		},
		{
			name:         "the requesting session's own end is reported",
			lifetime:     time.Hour,
			expiresAt:    now.Add(30 * time.Minute),
			wantSeconds:  3600,
			wantExpiring: true,
		},
		{
			name:        "an expired session reports no remainder rather than a negative one",
			lifetime:    time.Hour,
			expiresAt:   now.Add(-time.Minute),
			wantSeconds: 3600,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newStubConfig()
			c := cfg.current
			c.Server.SessionLifetime = config.Duration(tc.lifetime)
			cfg.current = c

			s := sessionServer(t, cfg, &countingAccounts{active: tc.active, err: tc.countErr})
			r := asSession(http.MethodGet, "/api/admin", nil,
				auth.Session{Username: "k9mls", ExpiresAt: tc.expiresAt})

			got := s.sessionState(r)

			if got.LifetimeSeconds != tc.wantSeconds {
				t.Errorf("lifetime %ds, want %ds", got.LifetimeSeconds, tc.wantSeconds)
			}
			if got.Default != tc.wantDefault {
				t.Errorf("default %v, want %v; an operator cannot otherwise tell their own "+
					"choice from QSP's", got.Default, tc.wantDefault)
			}
			if got.Active != tc.wantActive {
				t.Errorf("active %d, want %d", got.Active, tc.wantActive)
			}
			if tc.wantExpiring && got.ExpiresInSeconds <= 0 {
				t.Error("the session's remaining time is not reported; the question the block " +
					"exists for is whether a login outlasts the job in hand")
			}
			if !tc.wantExpiring && got.ExpiresInSeconds < 0 {
				t.Errorf("a remainder of %ds was reported", got.ExpiresInSeconds)
			}
			if got.MinSeconds != int64(config.MinSessionLifetime.Seconds()) ||
				got.MaxSeconds != int64(config.MaxSessionLifetime.Seconds()) {
				t.Errorf("bounds %d..%d do not come from the validator's own limits",
					got.MinSeconds, got.MaxSeconds)
			}
		})
	}
}

// TestTheSessionLifetimeIsSavedOrRefusedWithAReason covers the editor.
//
// **The bounds are the validator's, not the handler's.** A handler with its own
// copy is a console that accepts a value the configuration will not load; this
// saves through the same path `-check` uses and returns what it says. The stub
// used here validates for that reason.
func TestTheSessionLifetimeIsSavedOrRefusedWithAReason(t *testing.T) {
	tests := []struct {
		name        string
		seconds     int64
		wantCode    int
		wantSays    string
		wantDefault bool
	}{
		{name: "twelve hours", seconds: 43200, wantCode: http.StatusOK},
		{name: "the shortest allowed", seconds: 60, wantCode: http.StatusOK},
		{name: "the longest allowed", seconds: 7 * 24 * 3600, wantCode: http.StatusOK},
		{name: "ninety minutes", seconds: 5400, wantCode: http.StatusOK},
		{
			name:     "under a minute is refused, with the limit in the message",
			seconds:  2,
			wantCode: http.StatusBadRequest,
			wantSays: "minute",
		},
		{
			name:     "over a week is refused",
			seconds:  8 * 24 * 3600,
			wantCode: http.StatusBadRequest,
			wantSays: "week",
		},
		{
			// **Zero is how an operator goes back to the default**, which the
			// configuration already means by an absent value. The page cannot
			// send it -- its field wants a duration -- but the endpoint accepts
			// it rather than inventing a reason to refuse, and the block then
			// reports the default as the default again.
			name:        "zero returns to the built-in default",
			seconds:     0,
			wantCode:    http.StatusOK,
			wantDefault: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newStubConfig()
			s := sessionServer(t, cfg, &countingAccounts{active: 1})
			r := asSession(http.MethodPut, "/api/admin/session-lifetime",
				map[string]int64{"seconds": tc.seconds},
				auth.Session{Username: "k9mls", ExpiresAt: time.Now().Add(time.Hour)})
			w := httptest.NewRecorder()

			s.handleSessionLifetime(w, r)

			if w.Code != tc.wantCode {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.wantCode, w.Body.String())
			}
			if tc.wantCode != http.StatusOK {
				if !strings.Contains(w.Body.String(), tc.wantSays) {
					t.Errorf("the refusal does not say %q: %s", tc.wantSays, w.Body.String())
				}
				if len(cfg.saved) != 0 {
					t.Error("a refused lifetime was saved anyway")
				}
				return
			}
			if len(cfg.saved) != 1 {
				t.Fatalf("saved %d configurations, want 1", len(cfg.saved))
			}
			if got := int64(time.Duration(cfg.saved[0].Server.SessionLifetime).Seconds()); got != tc.seconds {
				t.Errorf("saved %ds, want %ds", got, tc.seconds)
			}
			if got := s.sessionState(r).Default; got != tc.wantDefault {
				t.Errorf("the block reports default=%v after saving %ds", got, tc.seconds)
			}
			// The actor is the session's own account, so the version history
			// says who changed it rather than "unknown".
			if len(cfg.authors) != 1 || cfg.authors[0] != "k9mls" {
				t.Errorf("the change was recorded as %v, want the signed-in account", cfg.authors)
			}
		})
	}
}

// TestChangingTheLifetimeDoesNotEndTheSessionThatChangedIt is the row that
// matters most to whoever clicks Save.
//
// **A setting change that logs everybody out is an outage**, and the operator
// making it would be the first one out -- mid-restore, in the case the page
// worries about. The new value belongs to the next login.
func TestChangingTheLifetimeDoesNotEndTheSessionThatChangedIt(t *testing.T) {
	cfg := newStubConfig()
	s := sessionServer(t, cfg, &countingAccounts{active: 2})

	mine := auth.Session{Username: "k9mls", ExpiresAt: time.Now().UTC().Add(9 * time.Hour)}
	r := asSession(http.MethodPut, "/api/admin/session-lifetime",
		map[string]int64{"seconds": 60}, mine)
	w := httptest.NewRecorder()
	s.handleSessionLifetime(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Sessions adminSessions `json:"sessions"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("reading the response: %v", err)
	}
	if body.Sessions.LifetimeSeconds != 60 {
		t.Errorf("the block reports %ds after saving 60s", body.Sessions.LifetimeSeconds)
	}
	// Whatever the new lifetime is, this session keeps the expiry it was
	// issued with: hours away, not a minute.
	if body.Sessions.ExpiresInSeconds < 3600 {
		t.Errorf("the caller's session now ends in %ds; a shorter lifetime must not "+
			"cut short a login already issued", body.Sessions.ExpiresInSeconds)
	}
}

// TestAServerWithNoConfigurationFileRefusesTheEdit keeps the read-only case
// honest: it still reports, and says why it cannot save.
func TestAServerWithNoConfigurationFileRefusesTheEdit(t *testing.T) {
	s := sessionServer(t, nil, &countingAccounts{active: 1})
	r := asSession(http.MethodPut, "/api/admin/session-lifetime",
		map[string]int64{"seconds": 3600}, auth.Session{Username: "k9mls"})
	w := httptest.NewRecorder()

	s.handleSessionLifetime(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503: %s", w.Code, w.Body.String())
	}
	state := s.sessionState(httptest.NewRequest(http.MethodGet, "/api/admin", nil))
	if state.LifetimeSeconds != int64(auth.DefaultSessionLifetime.Seconds()) || !state.Default {
		t.Errorf("an unconfigurable server reports %ds default=%v, want the built-in default",
			state.LifetimeSeconds, state.Default)
	}
}
