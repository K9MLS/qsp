package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// stubAccounts is the account store, holding W9XYZ and nobody else.
type stubAccounts struct{}

func (stubAccounts) Accounts(context.Context) ([]auth.Account, error) { return nil, nil }

func (stubAccounts) CreateAccount(_ context.Context, username, _ string) (auth.Account, error) {
	if username == "W9XYZ" {
		return auth.Account{}, auth.ErrUsernameTaken
	}
	return auth.Account{Username: username}, nil
}

func (stubAccounts) ChangePassword(context.Context, string, string, string, string) error {
	return nil
}

func (stubAccounts) ResetPassword(_ context.Context, username, _, _ string) error {
	if username != "W9XYZ" {
		return auth.ErrNoSuchAccount
	}
	return nil
}

func (stubAccounts) RemoveAccount(_ context.Context, username string) error {
	if username != "W9XYZ" {
		return auth.ErrNoSuchAccount
	}
	return nil
}

// auditServer builds a server whose audit events the test can read.
func auditServer(t *testing.T, opts Options) (*Server, *recordingAudit) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{HistorySize: 8, SubscriberBuffer: 4})
	t.Cleanup(bus.Close)
	rec := &recordingAudit{}
	opts.ListenAddress = "127.0.0.1:0"
	opts.Audit = rec
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, rec
}

// TestAccountChangesNameWhoMadeThem. The trail exists to say who did what, and
// for accounts it said the account did it to itself: the reset or removed
// username was written as the actor and the administrator who pressed the
// button was written nowhere. With one kind of account that can do everything,
// "who removed this administrator" is the question most worth being able to
// answer.
//
// To see it fail: in recordAccount, swap the values given to Actor and Subject,
// which puts the account in the actor's place as recordAuth did.
func TestAccountChangesNameWhoMadeThem(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		target      string
		body        any
		handler     func(*Server) http.HandlerFunc
		wantAction  audit.Action
		wantOutcome audit.Outcome
	}{
		{
			name: "adding an administrator", method: http.MethodPost, target: "N0CALL",
			body:       addUserRequest{Username: "N0CALL"},
			handler:    func(s *Server) http.HandlerFunc { return s.handleAddUser },
			wantAction: audit.ActionUserCreated, wantOutcome: audit.OutcomeSuccess,
		},
		{
			name: "failing to add one", method: http.MethodPost, target: "W9XYZ",
			body:       addUserRequest{Username: "W9XYZ"},
			handler:    func(s *Server) http.HandlerFunc { return s.handleAddUser },
			wantAction: audit.ActionUserCreated, wantOutcome: audit.OutcomeFailure,
		},
		{
			name: "resetting a password", method: http.MethodPost, target: "W9XYZ",
			handler:    func(s *Server) http.HandlerFunc { return s.handleResetPassword },
			wantAction: audit.ActionUserPasswordReset, wantOutcome: audit.OutcomeSuccess,
		},
		{
			name: "resetting one that does not exist", method: http.MethodPost, target: "NOBODY",
			handler:    func(s *Server) http.HandlerFunc { return s.handleResetPassword },
			wantAction: audit.ActionUserPasswordReset, wantOutcome: audit.OutcomeFailure,
		},
		{
			name: "removing an administrator", method: http.MethodDelete, target: "W9XYZ",
			handler:    func(s *Server) http.HandlerFunc { return s.handleRemoveUser },
			wantAction: audit.ActionUserDeleted, wantOutcome: audit.OutcomeSuccess,
		},
		{
			name: "failing to remove one", method: http.MethodDelete, target: "NOBODY",
			handler:    func(s *Server) http.HandlerFunc { return s.handleRemoveUser },
			wantAction: audit.ActionUserDeleted, wantOutcome: audit.OutcomeFailure,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, rec := auditServer(t, Options{Accounts: stubAccounts{}})

			r := asSession(tc.method, "/api/users", tc.body, auth.Session{Username: "K9MLS"})
			r.SetPathValue("name", tc.target)
			tc.handler(srv)(httptest.NewRecorder(), r)

			if len(rec.events) != 1 {
				t.Fatalf("%d audit events were written, want 1: %+v", len(rec.events), rec.events)
			}
			e := rec.events[0]
			if e.Actor != "K9MLS" {
				t.Errorf("the actor is %q, want the signed-in administrator K9MLS", e.Actor)
			}
			if e.Subject != tc.target {
				t.Errorf("the subject is %q, want the account acted on, %q", e.Subject, tc.target)
			}
			if e.Action != tc.wantAction || e.Outcome != tc.wantOutcome {
				t.Errorf("recorded %s/%s, want %s/%s", e.Action, e.Outcome, tc.wantAction, tc.wantOutcome)
			}
			if err := e.Validate(); err != nil {
				t.Errorf("the event would be refused by a recorder: %v", err)
			}
		})
	}
}

// TestSettingsSavedFromTheAdministrationPageAreAudited. The two settings that
// page edits were saved through Config.Save directly, which writes the
// configuration history and no audit event — so a change to how long a login
// lasts was in neither the trail nor anybody's view of who made it.
//
// To see it fail: delete the `s.recordSettingSave(...)` line from
// handleSessionLifetime or handleCallsigns.
func TestSettingsSavedFromTheAdministrationPageAreAudited(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		body        any
		handler     func(*Server) http.HandlerFunc
		saveErr     error
		wantCode    int
		wantOutcome audit.Outcome
		wantVersion string
	}{
		{
			name: "session lifetime", path: "/api/admin/session-lifetime",
			body:     sessionLifetimeRequest{Seconds: 3600},
			handler:  func(s *Server) http.HandlerFunc { return s.handleSessionLifetime },
			wantCode: http.StatusOK, wantOutcome: audit.OutcomeSuccess, wantVersion: "1",
		},
		{
			name: "session lifetime refused by the validator", path: "/api/admin/session-lifetime",
			body:     sessionLifetimeRequest{Seconds: 2},
			handler:  func(s *Server) http.HandlerFunc { return s.handleSessionLifetime },
			wantCode: http.StatusBadRequest, wantOutcome: audit.OutcomeFailure,
		},
		{
			name: "callsign lookup", path: "/api/admin/callsigns",
			body:     callsignRequest{Enabled: true, Contact: "k9mls@example.org"},
			handler:  func(s *Server) http.HandlerFunc { return s.handleCallsigns },
			wantCode: http.StatusOK, wantOutcome: audit.OutcomeSuccess, wantVersion: "1",
		},
		{
			name: "callsign lookup that could not be written", path: "/api/admin/callsigns",
			body:     callsignRequest{Enabled: false},
			handler:  func(s *Server) http.HandlerFunc { return s.handleCallsigns },
			saveErr:  context.DeadlineExceeded,
			wantCode: http.StatusBadRequest, wantOutcome: audit.OutcomeFailure,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := newStubConfig()
			cm.saveErr = tc.saveErr
			srv, rec := auditServer(t, Options{Config: cm})

			r := asSession(http.MethodPut, tc.path, tc.body,
				auth.Session{Username: "K9MLS", ExpiresAt: time.Now().Add(time.Hour)})
			w := httptest.NewRecorder()
			tc.handler(srv)(w, r)
			if w.Code != tc.wantCode {
				t.Fatalf("returned %d, want %d: %s", w.Code, tc.wantCode, w.Body)
			}

			saves := configEvents(rec)
			if len(saves) != 1 {
				t.Fatalf("%d configuration events were written, want 1: %+v", len(saves), rec.events)
			}
			e := saves[0]
			if e.Actor != "K9MLS" || e.Outcome != tc.wantOutcome {
				t.Errorf("recorded actor %q outcome %s, want K9MLS %s", e.Actor, e.Outcome, tc.wantOutcome)
			}
			if e.Detail["version"] != tc.wantVersion {
				t.Errorf("recorded version %q, want %q", e.Detail["version"], tc.wantVersion)
			}
			if e.Detail["summary"] == "" {
				t.Error("the event does not say which setting changed")
			}
		})
	}
}
