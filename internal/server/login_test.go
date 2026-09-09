package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/console"
	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/buildinfo"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// stubAuth is the login flow, without a database.
type stubAuth struct {
	// password is what Authenticate accepts.
	password string
	// locked makes every attempt report a lockout.
	locked bool
	// sessions are the tokens Session will recognise.
	sessions map[string]auth.Session
	// ended records tokens EndSession was called with.
	ended []string
}

func newStubAuth() *stubAuth {
	return &stubAuth{password: "a-passphrase-of-several-words", sessions: map[string]auth.Session{}}
}

func (s *stubAuth) Authenticate(_ context.Context, username, password, ip, agent string) (auth.Session, error) {
	if s.locked {
		return auth.Session{}, auth.ErrLockedOut
	}
	if username == "" || password != s.password {
		return auth.Session{}, auth.ErrInvalidCredentials
	}
	session := auth.Session{
		Token:     "token-for-" + username,
		Username:  username,
		ExpiresAt: time.Now().Add(time.Hour).UTC(),
		SourceIP:  ip,
		UserAgent: agent,
	}
	s.sessions[session.Token] = session
	return session, nil
}

func (s *stubAuth) Session(_ context.Context, token string) (auth.Session, error) {
	session, ok := s.sessions[token]
	if !ok {
		return auth.Session{}, auth.ErrNoSession
	}
	return session, nil
}

func (s *stubAuth) EndSession(_ context.Context, token string) error {
	s.ended = append(s.ended, token)
	delete(s.sessions, token)
	return nil
}

func newAuthServer(t *testing.T, a Authenticator, behindProxy bool) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Auth:          a,
		BehindProxy:   behindProxy,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func postLogin(t *testing.T, srv *Server, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"username":"` + username + `","password":"` + password + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func sessionCookieFrom(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie was set")
	return nil
}

func TestLoginSetsASessionCookie(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	rec := postLogin(t, srv, "K9MLS", a.password)
	if rec.Code != http.StatusOK {
		t.Fatalf("login returned %d: %s", rec.Code, rec.Body)
	}

	c := sessionCookieFrom(t, rec)
	if c.Value == "" {
		t.Error("the cookie carries no token")
	}
	if !c.HttpOnly {
		t.Error("the session cookie is readable from JavaScript")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite is %v, want Lax", c.SameSite)
	}
}

// TestSecureFollowsBehindProxy is ADR-0026's rule. Setting Secure
// unconditionally silently breaks a club on plain HTTP over a LAN — the browser
// drops the cookie and the login appears to succeed and do nothing.
func TestSecureFollowsBehindProxy(t *testing.T) {
	for _, behind := range []bool{false, true} {
		a := newStubAuth()
		srv := newAuthServer(t, a, behind)
		rec := postLogin(t, srv, "K9MLS", a.password)
		c := sessionCookieFrom(t, rec)
		if c.Secure != behind {
			t.Errorf("behind_proxy=%v gave Secure=%v", behind, c.Secure)
		}
	}
}

// TestAWrongPasswordAndAnUnknownUserAnswerIdentically. Anything else turns the
// login form into a way of asking which callsigns hold accounts here.
func TestAWrongPasswordAndAnUnknownUserAnswerIdentically(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	wrong := postLogin(t, srv, "K9MLS", "not-the-password")
	unknown := postLogin(t, srv, "", "whatever")

	if wrong.Code != http.StatusUnauthorized || unknown.Code != http.StatusUnauthorized {
		t.Fatalf("codes differ: wrong=%d unknown=%d", wrong.Code, unknown.Code)
	}
	if wrong.Body.String() != unknown.Body.String() {
		t.Errorf("bodies differ:\n  wrong:   %s\n  unknown: %s", wrong.Body, unknown.Body)
	}
	// And neither sets a cookie.
	for _, rec := range []*httptest.ResponseRecorder{wrong, unknown} {
		for _, c := range rec.Result().Cookies() {
			if c.Name == SessionCookie && c.Value != "" {
				t.Error("a refused login set a session cookie")
			}
		}
	}
}

// TestALockedAccountSaysSo. An operator told only "incorrect" keeps trying,
// which extends the lockout they cannot see.
func TestALockedAccountSaysSo(t *testing.T) {
	a := newStubAuth()
	a.locked = true
	srv := newAuthServer(t, a, false)

	rec := postLogin(t, srv, "K9MLS", a.password)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a locked account returned %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "failed attempts") {
		t.Errorf("the message does not explain the wait: %s", rec.Body)
	}
}

func TestSessionEndpointReportsWhoIsLoggedIn(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	// Nobody, and that is a 200 rather than an error: a console loading
	// normally should not produce one in the browser.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("an anonymous session check returned %d", rec.Code)
	}
	var body sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Authenticated {
		t.Error("an anonymous request reported as authenticated")
	}

	// Now logged in.
	c := sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password))
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(c)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Authenticated || body.Username != "K9MLS" {
		t.Errorf("session reported %+v", body)
	}
}

func TestLogoutEndsTheSessionAndClearsTheCookie(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)
	c := sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password))

	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("logout returned %d", rec.Code)
	}
	if len(a.ended) != 1 || a.ended[0] != c.Value {
		t.Errorf("the session was not ended server-side: %v", a.ended)
	}
	cleared := sessionCookieFrom(t, rec)
	if cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("the cookie was not cleared: %+v", cleared)
	}
}

// TestLogoutAnswersTheSameWithoutASession. Reporting "you were not logged in"
// tells whoever sent it something about a cookie they may not own.
func TestLogoutAnswersTheSameWithoutASession(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	with := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password)))
	srv.Handler().ServeHTTP(with, req)

	without := httptest.NewRecorder()
	srv.Handler().ServeHTTP(without, httptest.NewRequest(http.MethodPost, "/api/logout", nil))

	if with.Code != without.Code || with.Body.String() != without.Body.String() {
		t.Errorf("logout answers differ:\n  with:    %d %s\n  without: %d %s",
			with.Code, with.Body, without.Code, without.Body)
	}
}

// TestAnInstanceWithNoAccountsSaysSo. It is not broken, and a 404 would read
// like a missing feature rather than a step the operator has not taken.
func TestAnInstanceWithNoAccountsSaysSo(t *testing.T) {
	srv := newAuthServer(t, nil, false)

	rec := postLogin(t, srv, "K9MLS", "anything")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("returned %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "adduser") {
		t.Errorf("the message does not say how to create one: %s", rec.Body)
	}
}

func TestRequireSessionRefusesAnonymousRequests(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	var reached bool
	h := srv.requireSession(func(http.ResponseWriter, *http.Request) { reached = true })

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodPost, "/api/anything", nil))

	if reached {
		t.Error("an anonymous request reached a protected handler")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("returned %d, want 401", rec.Code)
	}
}

func TestRequireSessionPassesTheSessionThrough(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)
	c := sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password))

	var got auth.Session
	h := srv.requireSession(func(_ http.ResponseWriter, r *http.Request) {
		got, _ = SessionFrom(r.Context())
	})

	req := httptest.NewRequest(http.MethodPost, "/api/anything", nil)
	req.AddCookie(c)
	req.Host = "qsp.example"
	req.Header.Set("Origin", "https://qsp.example")
	h(httptest.NewRecorder(), req)

	// The actor an audited write would record. A guess here is how
	// audit_events.actor stops being true.
	if got.Username != "K9MLS" {
		t.Errorf("the handler saw %+v", got)
	}
}

// TestCrossOriginWritesAreRefused is the second lock on the door SameSite=Lax
// already closes, because Lax is honoured by browsers rather than guaranteed by
// them.
func TestCrossOriginWritesAreRefused(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)
	c := sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password))

	var reached bool
	h := srv.requireSession(func(http.ResponseWriter, *http.Request) { reached = true })

	req := httptest.NewRequest(http.MethodPost, "/api/anything", nil)
	req.AddCookie(c)
	req.Host = "qsp.example"
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h(rec, req)

	if reached {
		t.Error("a cross-origin write reached a protected handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("returned %d, want 403", rec.Code)
	}
}

// TestARequestWithNoOriginIsAllowed. curl, a script, an operator with a token —
// none of them is what CSRF protects against, and refusing them would break
// every legitimate use of the API from a terminal.
func TestARequestWithNoOriginIsAllowed(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)
	c := sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password))

	var reached bool
	h := srv.requireSession(func(http.ResponseWriter, *http.Request) { reached = true })

	req := httptest.NewRequest(http.MethodPost, "/api/anything", nil)
	req.AddCookie(c)
	req.Host = "qsp.example"
	rec := httptest.NewRecorder()
	h(rec, req)

	if !reached {
		t.Errorf("a request with no Origin was refused: %d %s", rec.Code, rec.Body)
	}
}

// TestASameOriginWriteIsAllowedOverAnyScheme. An instance behind a proxy sees a
// different scheme from the one the browser used, so comparing anything but the
// host would refuse every request it received.
func TestASameOriginWriteIsAllowedOverAnyScheme(t *testing.T) {
	for _, origin := range []string{"http://qsp.example", "https://qsp.example"} {
		req := httptest.NewRequest(http.MethodPost, "/api/anything", nil)
		req.Host = "qsp.example"
		req.Header.Set("Origin", origin)
		if err := checkOrigin(req); err != nil {
			t.Errorf("Origin %q was refused: %v", origin, err)
		}
	}
}

func TestReadsAreNotOriginChecked(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		req := httptest.NewRequest(method, "/api/peers", nil)
		req.Host = "qsp.example"
		req.Header.Set("Origin", "https://evil.example")
		if err := checkOrigin(req); err != nil {
			t.Errorf("%s was origin-checked: %v", method, err)
		}
	}
}

// TestAnUnknownCookieIsNotASession guards the obvious forgery.
func TestAnUnknownCookieIsNotASession(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "made-up"})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	var body sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Authenticated {
		t.Error("an invented cookie was accepted as a session")
	}
}

func TestLoginRejectsAnOversizedBody(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	// Reachable without credentials, so an unbounded read is a way to spend
	// memory without holding any.
	body := strings.NewReader(`{"username":"` + strings.Repeat("a", 64<<10) + `"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("an oversized body returned %d, want 400", rec.Code)
	}
}

func TestLoginRejectsMalformedJSON(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("returned %d, want 400", rec.Code)
	}
}

// TestSessionFromIsEmptyWithoutOne stops a handler treating a missing session
// as an anonymous-but-present actor.
func TestSessionFromIsEmptyWithoutOne(t *testing.T) {
	if _, ok := SessionFrom(context.Background()); ok {
		t.Error("a context with no session reported one")
	}
}

func TestErrNoSessionIsNotLeaked(t *testing.T) {
	// The handler must not pass a storage error through to the client, where
	// it would describe the shape of the database.
	a := newStubAuth()
	srv := newAuthServer(t, a, false)
	rec := postLogin(t, srv, "K9MLS", "wrong")

	if strings.Contains(rec.Body.String(), "auth:") {
		t.Errorf("an internal error string reached the client: %s", rec.Body)
	}
	if errors.Is(errors.New(rec.Body.String()), auth.ErrInvalidCredentials) {
		t.Error("the internal error was returned verbatim")
	}
}

// TestSignInPageIsReachable. /signin exists for the same reason /join does: a
// URL an operator types or bookmarks should not end in .html.
func TestSignInPageIsReachable(t *testing.T) {
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
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signin", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("/signin returned %d, want a redirect", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/signin.html" {
		t.Errorf("/signin redirects to %q", loc)
	}

	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/signin.html", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/signin.html returned %d", rec.Code)
	}
}

// TestEveryAuthenticationIsAudited.
//
// **login.go carried no audit call at all.** Every attempt went to the log and
// none of it reached audit_events, while ActionUserLogin, ActionUserLogout and
// OutcomeDenied sat declared and unused — the same shape as the link export
// lists and database.busy_timeout.
//
// SECURITY.md says roles are deliberately absent because one kind of account
// can do everything, and that the audit trail records who did what. It could
// not answer who was in the system at all.
func TestEveryAuthenticationIsAudited(t *testing.T) {
	src, err := os.ReadFile("login.go")
	if err != nil {
		t.Fatalf("reading login.go: %v", err)
	}
	body := string(src)

	// A failed attempt is the one that matters most: an attempt against a
	// username holding no account is the shape of somebody guessing, and a
	// trail of successes alone cannot show it.
	for _, want := range []string{
		"audit.ActionUserLogin, req.Username, audit.OutcomeDenied",
		"audit.ActionUserLogin, req.Username, audit.OutcomeFailure",
		"audit.ActionUserLogin, session.Username, audit.OutcomeSuccess",
		"audit.ActionUserLogout, who, audit.OutcomeSuccess",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("no audit record for %q", want)
		}
	}

	// Logout must read the session before ending it, or the record names
	// nobody.
	//
	// **Scoped to the handler.** The first version searched the whole file and
	// found EndSession in the interface declaration above, so it compared a
	// position in the handler against one in a type — and failed against
	// correct code. Measuring the wrong occurrence of the right string.
	at := strings.Index(body, "func (s *Server) handleLogout")
	if at < 0 {
		t.Fatal("handleLogout is gone")
	}
	handler := body[at:]
	if next := strings.Index(handler[1:], "\nfunc "); next >= 0 {
		handler = handler[:next]
	}
	end := strings.Index(handler, "EndSession")
	read := strings.Index(handler, "who = sess.Username")
	if read < 0 || end < 0 || read > end {
		t.Error("logout ends the session before reading who it belonged to")
	}
}

// TestTheVersionRidesOnTheEndpointTheConsoleAsks is the test that would have
// caught it.
//
// **0310 put the version on the login response**, which the console chrome
// reads once, instead of on the session response, which it reads on every page
// load. So the value was returned to nothing and the sidebar stayed empty
// through a correct build, a correct deploy and a correct version check.
//
// That is §8a's "fix the half that is called, not the half that is named",
// recorded the same morning and repeated within hours. The lesson it adds:
// **asserting a field is set is not the same as asserting the caller receives
// it** — this drives the endpoint the console actually fetches.
func TestTheVersionRidesOnTheEndpointTheConsoleAsks(t *testing.T) {
	a := newStubAuth()
	srv := newAuthServer(t, a, false)

	// **Anonymous too.** Withholding it was the safer default and the operator
	// chose otherwise: the version is a fact about the server, it is read
	// constantly, and a console that answers only after a sign-in answers a
	// moment too late.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/session", nil))

	var body sessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version != buildinfo.Version {
		t.Errorf("an anonymous request was told %q, want %q", body.Version, buildinfo.Version)
	}

	// Signed in: the version, on the request the chrome makes every page load.
	req := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	req.AddCookie(sessionCookieFrom(t, postLogin(t, srv, "K9MLS", a.password)))
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Version == "" {
		t.Fatal("the session response carries no version, so the sidebar shows none")
	}
	if body.Version != buildinfo.Version {
		t.Errorf("the session reports version %q, want %q", body.Version, buildinfo.Version)
	}
}
