package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// lockingAuth refuses every attempt as an address that is locked out until a
// fixed moment, the way auth.Service does.
type lockingAuth struct {
	*stubAuth
	until time.Time
}

func (l lockingAuth) Authenticate(_ context.Context, username, password, ip, agent string) (auth.Session, error) {
	return auth.Session{}, &auth.Lockout{Until: l.until}
}

func loginFrom(srv *Server, addr, username string) *httptest.ResponseRecorder {
	body := strings.NewReader(`{"username":` + strconv.Quote(username) + `,"password":"wrong"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	req.RemoteAddr = addr
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func loginEvents(rec *recordingAudit) []audit.Event {
	var out []audit.Event
	for _, e := range rec.events {
		if e.Action == audit.ActionUserLogin {
			out = append(out, e)
		}
	}
	return out
}

// TestRefusedSignInsCannotFillTheAuditTrail. The login form is open to
// anybody, and every attempt used to be a row holding whatever was typed.
//
// To see each case fail, in turn: return name unchanged from auditActor;
// make firstOfRefusal return true; make authAuditBudget.allow return
// (true, 0).
func TestRefusedSignInsCannotFillTheAuditTrail(t *testing.T) {
	t.Run("a long name is recorded short", func(t *testing.T) {
		rec := &recordingAudit{}
		srv, _ := newConfigServer(t, nil, rec)
		loginFrom(srv, "203.0.113.5:4000", strings.Repeat("Ж", 4000))

		got := loginEvents(rec)
		if len(got) != 1 {
			t.Fatalf("%d rows for one attempt, want 1", len(got))
		}
		if n := utf8.RuneCountInString(got[0].Actor); n > maxAuditActor+1 {
			t.Errorf("the name was recorded at %d characters, want at most %d", n, maxAuditActor+1)
		}
		if !utf8.ValidString(got[0].Actor) {
			t.Error("the name was cut in the middle of a character")
		}
		if err := got[0].Validate(); err != nil {
			t.Errorf("the row would be refused: %v", err)
		}
	})

	t.Run("an attempt with no name is still a row", func(t *testing.T) {
		rec := &recordingAudit{}
		srv, _ := newConfigServer(t, nil, rec)
		loginFrom(srv, "203.0.113.5:4000", "   ")
		got := loginEvents(rec)
		if len(got) != 1 {
			t.Fatalf("%d rows, want 1", len(got))
		}
		if err := got[0].Validate(); err != nil {
			t.Errorf("the row would be refused: %v", err)
		}
	})

	t.Run("an address being refused is recorded once", func(t *testing.T) {
		rec := &recordingAudit{}
		bus := events.NewBus(nil, events.Options{})
		t.Cleanup(bus.Close)
		srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
			ListenAddress: "127.0.0.1:0",
			Auth:          lockingAuth{newStubAuth(), time.Now().Add(10 * time.Minute)},
			Audit:         rec,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		for range 10 {
			if rr := loginFrom(srv, "203.0.113.5:4000", "K9MLS"); rr.Code != http.StatusTooManyRequests {
				t.Fatalf("answered %d, want 429", rr.Code)
			}
		}
		if got := loginEvents(rec); len(got) != 1 {
			t.Errorf("%d rows for ten attempts during one refusal, want 1", len(got))
		}
		// Somebody else being refused is their own row.
		loginFrom(srv, "198.51.100.9:4000", "K9MLS")
		if got := loginEvents(rec); len(got) != 2 {
			t.Errorf("%d rows after a second address was refused, want 2", len(got))
		}
	})

	t.Run("a flood from many addresses is counted, not stored", func(t *testing.T) {
		rec := &recordingAudit{}
		srv, _ := newConfigServer(t, nil, rec)
		const sent = authAuditPerMinute + 130
		for i := range sent {
			loginFrom(srv, "203.0.113."+strconv.Itoa(i%250)+":4000", "GUESS"+strconv.Itoa(i))
		}
		if got := loginEvents(rec); len(got) != authAuditPerMinute {
			t.Fatalf("%d rows for %d attempts in a minute, want %d", len(got), sent, authAuditPerMinute)
		}

		// The next minute's first refusal says how many were left out.
		srv.authAudit.mu.Lock()
		srv.authAudit.minute = srv.authAudit.minute.Add(-2 * time.Minute)
		srv.authAudit.mu.Unlock()
		loginFrom(srv, "203.0.113.5:4000", "LATER")

		got := loginEvents(rec)
		if len(got) != authAuditPerMinute+2 {
			t.Fatalf("%d rows, want the %d, a count and the new one", len(got), authAuditPerMinute)
		}
		count := got[authAuditPerMinute]
		if count.Actor != audit.SystemActor || !strings.HasPrefix(count.Detail["reason"], "130 more") {
			t.Errorf("the count row is %+v", count)
		}
		if err := count.Validate(); err != nil {
			t.Errorf("the count row would be refused: %v", err)
		}
	})

	t.Run("a sign-in that works is always recorded", func(t *testing.T) {
		rec := &recordingAudit{}
		srv, a := newConfigServer(t, nil, rec)
		for i := range authAuditPerMinute + 5 {
			loginFrom(srv, "203.0.113.5:4000", "GUESS"+strconv.Itoa(i))
		}
		before := len(loginEvents(rec))
		if rr := postLogin(t, srv, "K9MLS", a.password); rr.Code != http.StatusOK {
			t.Fatalf("the sign-in answered %d", rr.Code)
		}
		got := loginEvents(rec)
		if len(got) != before+1 || got[len(got)-1].Outcome != audit.OutcomeSuccess {
			t.Errorf("the sign-in was not recorded during a flood")
		}
	})
}

// TestSetupAlwaysAsksForTheToken. A request from this machine used to be let
// off it, and behind nginx or Caddy on the same host every request is from
// this machine.
//
// To see it fail: in handleSetup, wrap the token check in
// `if !strings.HasPrefix(r.RemoteAddr, "127.")`.
func TestSetupAlwaysAsksForTheToken(t *testing.T) {
	for _, tc := range []struct {
		name, remote string
		behindProxy  bool
		token        string
		want         int
	}{
		{"from this machine, no token", "127.0.0.1:50000", false, "", http.StatusForbidden},
		{"from this machine over IPv6, no token", "[::1]:50000", false, "", http.StatusForbidden},
		{"from this machine, a wrong token", "127.0.0.1:50000", false, "0000", http.StatusForbidden},
		{"through a proxy on this host, no token", "127.0.0.1:50000", true, "", http.StatusForbidden},
		{"from the network, no token", "203.0.113.7:50000", false, "", http.StatusForbidden},
		{"from this machine, the token", "127.0.0.1:50000", false, "the-token", http.StatusCreated},
		{"from the network, the token", "203.0.113.7:50000", false, "the-token", http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.NewBus(nil, events.Options{})
			t.Cleanup(bus.Close)
			srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
				ListenAddress: "127.0.0.1:0",
				Setup:         noAccounts{},
				BehindProxy:   tc.behindProxy,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			srv.setup.token = "the-token"

			state := httptest.NewRequest(http.MethodGet, "/api/setup", nil)
			state.RemoteAddr = tc.remote
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, state)
			if !strings.Contains(rec.Body.String(), `"token_required": true`) &&
				!strings.Contains(rec.Body.String(), `"token_required":true`) {
				t.Errorf("the page is not told to ask for the token: %s", rec.Body.String())
			}

			req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(
				`{"token":"`+tc.token+`","username":"K9MLS","password":"a-passphrase-of-several-words"}`))
			req.RemoteAddr = tc.remote
			req.Header.Set("Content-Type", "application/json")
			rec = httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("answered %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}
