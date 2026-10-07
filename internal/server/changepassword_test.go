package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/auth"
)

// passwordAccounts records what the handlers ask of the account store.
type passwordAccounts struct {
	stubAccounts
	changeErr error

	token, current, next, ip string
	resetKeep                string
	resetCalled              bool
}

func (p *passwordAccounts) ChangePassword(_ context.Context, token, current, next, ip string) error {
	p.token, p.current, p.next, p.ip = token, current, next, ip
	return p.changeErr
}

func (p *passwordAccounts) ResetPassword(_ context.Context, _, _, keep string) error {
	p.resetCalled, p.resetKeep = true, keep
	return nil
}

// TestChangingMyPasswordFromTheConsole is what the page is told for each way
// it can go.
//
// **Never 401 for a wrong current password.** That answer means "you are not
// signed in" and the page acts on it.
//
// To see it fail: map auth.ErrInvalidCredentials to http.StatusUnauthorized
// in handleChangePassword, or delete either recordAccount call.
func TestChangingMyPasswordFromTheConsole(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantCode    int
		wantSays    string
		wantOutcome audit.Outcome
	}{
		{"it worked", nil, http.StatusOK, "still signed in here", audit.OutcomeSuccess},
		{"wrong current password", auth.ErrInvalidCredentials, http.StatusForbidden,
			"current password is not right", audit.OutcomeFailure},
		{"too many wrong ones", &auth.Lockout{Until: time.Now().Add(5 * time.Minute)},
			http.StatusTooManyRequests, "too many wrong passwords", audit.OutcomeFailure},
		{"new one too short", fmt.Errorf("%w: use more", auth.ErrPasswordTooShort),
			http.StatusBadRequest, "too short", audit.OutcomeFailure},
		{"new one the same", auth.ErrSamePassword, http.StatusBadRequest,
			"the one you have now", audit.OutcomeFailure},
		{"the database is in trouble", context.DeadlineExceeded,
			http.StatusInternalServerError, "could not be changed", audit.OutcomeFailure},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &passwordAccounts{changeErr: tc.err}
			srv, rec := auditServer(t, Options{Accounts: accounts})

			r := asSession(http.MethodPost, "/api/account/password",
				changePasswordRequest{Current: "the-old-one", New: "the-new-one-entirely"},
				auth.Session{Token: "my-session", Username: "K9MLS"})
			r.RemoteAddr = "198.51.100.7:40000"
			w := httptest.NewRecorder()
			srv.handleChangePassword(w, r)

			if w.Code != tc.wantCode {
				t.Fatalf("answered %d, want %d: %s", w.Code, tc.wantCode, w.Body)
			}
			if !strings.Contains(w.Body.String(), tc.wantSays) {
				t.Errorf("the answer does not say %q: %s", tc.wantSays, w.Body)
			}
			for _, secret := range []string{"the-old-one", "the-new-one-entirely"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Errorf("the answer repeats a password: %s", w.Body)
				}
			}
			// The account is the session's, and nothing else.
			if accounts.token != "my-session" || accounts.ip != "198.51.100.7" {
				t.Errorf("asked for session %q from %q", accounts.token, accounts.ip)
			}
			if tc.wantCode == http.StatusTooManyRequests && w.Header().Get("Retry-After") == "" {
				t.Error("a refusal for guessing does not say when it ends")
			}

			if len(rec.events) != 1 {
				t.Fatalf("%d audit events, want 1: %+v", len(rec.events), rec.events)
			}
			e := rec.events[0]
			if e.Action != audit.ActionUserPasswordChanged || e.Outcome != tc.wantOutcome ||
				e.Actor != "K9MLS" || e.Subject != "K9MLS" {
				t.Errorf("recorded %+v", e)
			}
			for _, v := range e.Detail {
				if strings.Contains(v, "the-old-one") || strings.Contains(v, "the-new-one-entirely") {
					t.Errorf("the audit trail holds a password: %+v", e.Detail)
				}
			}
			if err := e.Validate(); err != nil {
				t.Errorf("the event would be refused by a recorder: %v", err)
			}
		})
	}
}

// TestChangingAPasswordIsBehindASignIn. The route, not the handler: with no
// session it is refused, and with one it is refused from another site's page,
// which is the check a signed-in browser needs and the handler does not make.
//
// To see it fail: register the route without s.requireSession.
func TestChangingAPasswordIsBehindASignIn(t *testing.T) {
	for _, tc := range []struct {
		name     string
		signedIn bool
		origin   string
		want     int
		reached  bool
	}{
		{"nobody signed in", false, "http://qsp.example", http.StatusUnauthorized, false},
		{"signed in, from another site's page", true, "https://evil.example", http.StatusForbidden, false},
		{"signed in, from the console", true, "http://qsp.example", http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &passwordAccounts{}
			a := newStubAuth()
			a.sessions["signed-in"] = auth.Session{Token: "signed-in", Username: "K9MLS",
				ExpiresAt: time.Now().Add(time.Hour).UTC()}
			srv, _ := auditServer(t, Options{Accounts: accounts, Auth: a})

			req := httptest.NewRequest(http.MethodPost, "/api/account/password",
				strings.NewReader(`{"current":"the-old-one","new":"the-new-one-entirely"}`))
			req.Host = "qsp.example"
			req.Header.Set("Origin", tc.origin)
			if tc.signedIn {
				req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "signed-in"})
			}
			w := httptest.NewRecorder()
			srv.Handler().ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Errorf("answered %d, want %d: %s", w.Code, tc.want, w.Body)
			}
			if reached := accounts.current != ""; reached != tc.reached {
				t.Errorf("the account store was reached: %v, want %v", reached, tc.reached)
			}
		})
	}
}

// TestAResetKeepsOnlyTheSessionOfWhoeverResetTheirOwn. An administrator
// resetting somebody else signs that person out everywhere; one resetting
// their own stays signed in where they are reading the new password.
//
// To see it fail: pass sess.Token as keep unconditionally in
// handleResetPassword.
func TestAResetKeepsOnlyTheSessionOfWhoeverResetTheirOwn(t *testing.T) {
	for _, tc := range []struct{ name, target, wantKeep string }{
		{"somebody else's", "W9XYZ", ""},
		{"their own", "K9MLS", "my-session"},
		{"their own, typed in another case", "k9mls", "my-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accounts := &passwordAccounts{}
			srv, _ := auditServer(t, Options{Accounts: accounts})
			r := asSession(http.MethodPost, "/api/users/x/password", nil,
				auth.Session{Token: "my-session", Username: "K9MLS"})
			r.SetPathValue("name", tc.target)
			w := httptest.NewRecorder()
			srv.handleResetPassword(w, r)
			if w.Code != http.StatusOK || !accounts.resetCalled {
				t.Fatalf("answered %d: %s", w.Code, w.Body)
			}
			if accounts.resetKeep != tc.wantKeep {
				t.Errorf("kept session %q, want %q", accounts.resetKeep, tc.wantKeep)
			}
		})
	}
}
