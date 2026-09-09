package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/buildinfo"
)

// SessionCookie is the cookie the console carries.
const SessionCookie = "qsp_session"

// Authenticator is the login flow the server needs.
//
// An interface rather than *auth.Service so the handlers can be tested without
// a database, which is the same reason auth.Service takes a Repository.
type Authenticator interface {
	Authenticate(ctx context.Context, username, password, ip, agent string) (auth.Session, error)
	Session(ctx context.Context, token string) (auth.Session, error)
	EndSession(ctx context.Context, token string) error
}

// loginRequest is what the console posts.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// sessionResponse says who is logged in.
type sessionResponse struct {
	// Authenticated is false rather than the response being a 401, when the
	// console asks who it is. A page loading normally should not produce an
	// error in the browser's console every time nobody is logged in.
	Authenticated bool   `json:"authenticated"`
	Username      string `json:"username,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	// Version is what this server is running, sent only to a signed-in
	// operator. See handleSession.
	Version string `json:"version,omitempty"`
}

// handleLogin exchanges a username and password for a session cookie.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.opts.Auth == nil {
		// An instance with no accounts configured is not broken, and saying so
		// is better than a 404 that reads like a missing feature.
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "this instance has no administrator accounts; create one with qsp adduser",
		})
		return
	}

	// A body limit, because this is reachable without credentials and an
	// unbounded read is a way to spend memory without holding any.
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "the request body is not the expected JSON",
		})
		return
	}

	session, err := s.opts.Auth.Authenticate(r.Context(), req.Username, req.Password,
		clientIP(r, s.opts.BehindProxy), r.UserAgent())
	switch {
	case errors.Is(err, auth.ErrLockedOut):
		// Said plainly. An operator who has locked themselves out and is told
		// only "incorrect" will keep trying, which extends the lockout.
		s.log.Warn("login refused: account locked", "username", req.Username)
		s.recordAuth(r, audit.ActionUserLogin, req.Username, audit.OutcomeDenied, "locked out")
		writeJSON(w, s.log, http.StatusTooManyRequests, map[string]string{
			"error": "too many failed attempts; wait a few minutes and try again",
		})
		return
	case err != nil:
		// Everything else is one message. Distinguishing a wrong password from
		// an unknown username turns this into a way of asking which callsigns
		// hold accounts here.
		s.log.Warn("login refused", "username", req.Username, "from", clientIP(r, s.opts.BehindProxy))
		// The username as typed, which may be nobody's account. A failed
		// attempt against a name that does not exist is the shape of somebody
		// guessing, and an audit trail that only records successes cannot show
		// it.
		s.recordAuth(r, audit.ActionUserLogin, req.Username, audit.OutcomeFailure, "")
		writeJSON(w, s.log, http.StatusUnauthorized, map[string]string{
			"error": "the username or password is incorrect",
		})
		return
	}

	http.SetCookie(w, s.sessionCookie(session.Token, session.ExpiresAt))
	s.log.Info("login", "username", session.Username, "from", clientIP(r, s.opts.BehindProxy))
	s.recordAuth(r, audit.ActionUserLogin, session.Username, audit.OutcomeSuccess, "")
	writeJSON(w, s.log, http.StatusOK, sessionResponse{
		Authenticated: true,
		Username:      session.Username,
		ExpiresAt:     session.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// handleLogout ends the session the request carries.
//
// It answers the same way whether there was a session or not: a logout that
// reports "you were not logged in" tells whoever sent it something about a
// cookie they may not own.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Read before the session is destroyed, or the record names nobody.
	who := "unknown"
	if sess, ok := s.session(r); ok {
		who = sess.Username
	}

	if s.opts.Auth != nil {
		if c, err := r.Cookie(SessionCookie); err == nil {
			if err := s.opts.Auth.EndSession(r.Context(), c.Value); err != nil {
				s.log.Warn("cannot end a session", "error", err)
			} else {
				s.recordAuth(r, audit.ActionUserLogout, who, audit.OutcomeSuccess, "")
			}
		}
	}
	// Cleared regardless, so a browser holding a token the server has already
	// forgotten stops sending it.
	http.SetCookie(w, s.sessionCookie("", time.Unix(0, 0)))
	writeJSON(w, s.log, http.StatusOK, sessionResponse{Authenticated: false})
}

// handleSession reports who the request belongs to.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	session, ok := s.session(r)
	if !ok {
		// **The version is told to an anonymous visitor too.** The console
		// shows it at the foot of every page whether anybody is signed in or
		// not, which is the operator's decision: an exact build number is worth
		// something to somebody probing, and the judgement is that a QSP
		// console reachable by strangers is not the situation this is built
		// for. Recorded here rather than argued each time it is read.
		writeJSON(w, s.log, http.StatusOK, sessionResponse{
			Authenticated: false,
			Version:       buildinfo.Version,
		})
		return
	}
	writeJSON(w, s.log, http.StatusOK, sessionResponse{
		Authenticated: true,
		Username:      session.Username,
		ExpiresAt:     session.ExpiresAt.UTC().Format(time.RFC3339),
		// **Here, and not on the login response.** The console chrome asks this
		// endpoint on every page load and asks the login endpoint once, so a
		// version returned there is a version nothing reads — which is exactly
		// what 0310 shipped, and what §8a calls fixing the half that is named
		// rather than the half that is called.
		//
		// Only to somebody signed in, for the same reason the administration
		// group is hidden from an anonymous visitor: an exact build number is
		// worth more to somebody probing than to a visitor.
		Version: buildinfo.Version,
	})
}

// session returns the session a request carries, if it has a good one.
func (s *Server) session(r *http.Request) (auth.Session, bool) {
	if s.opts.Auth == nil {
		return auth.Session{}, false
	}
	c, err := r.Cookie(SessionCookie)
	if err != nil || c.Value == "" {
		return auth.Session{}, false
	}
	session, err := s.opts.Auth.Session(r.Context(), c.Value)
	if err != nil {
		return auth.Session{}, false
	}
	return session, true
}

// sessionCookie builds the cookie, or the one that clears it.
func (s *Server) sessionCookie(token string, expires time.Time) *http.Cookie {
	c := &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		// Lax rather than Strict: following a link to the console must not
		// appear logged out, which teaches people to log in twice and defeats
		// the point of the cookie lasting.
		SameSite: http.SameSiteLaxMode,
		// **Secure follows behind_proxy**, per ADR-0026. Setting it always
		// would silently break a club running plain HTTP on a LAN — the
		// browser would drop the cookie and the login would appear to succeed
		// and do nothing. Never setting it would leak the session on a public
		// instance.
		Secure:  s.opts.BehindProxy,
		Expires: expires,
	}
	if token == "" {
		c.MaxAge = -1
	}
	return c
}

// requireSession wraps a handler so it is reachable only by a logged-in
// administrator.
//
// **It also enforces the CSRF rule**, because the two questions are asked of
// exactly the same requests and separating them is how one of them gets
// forgotten on a new endpoint.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, ok := s.session(r)
		if !ok {
			writeJSON(w, s.log, http.StatusUnauthorized, map[string]string{
				"error": "this endpoint requires a logged-in administrator",
			})
			return
		}
		if err := checkOrigin(r); err != nil {
			// SameSite=Lax stops a cross-site form post carrying the cookie in
			// every browser that honours it, which is not a guarantee and is
			// not all of them. This is the second lock on the same door.
			s.log.Warn("request refused: cross-origin", "path", r.URL.Path, "error", err)
			writeJSON(w, s.log, http.StatusForbidden, map[string]string{
				"error": "this request did not come from this instance's console",
			})
			return
		}
		r = r.WithContext(withSession(r.Context(), session))
		next(w, r)
	}
}

// checkOrigin refuses a state-changing request that came from elsewhere.
//
// The comparison is on host rather than scheme and port, because an instance
// behind a proxy sees a different scheme from the one the browser used and
// would otherwise refuse every request it received.
func checkOrigin(r *http.Request) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		// No Origin at all is a non-browser client — curl, a script, an
		// operator with a session token. Those are not the thing CSRF
		// protects against, and refusing them would break every legitimate
		// use of the API from a terminal.
		return nil
	}

	u, err := url.Parse(origin)
	if err != nil {
		return fmt.Errorf("unparseable origin header %q", origin)
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return fmt.Errorf("origin %q is not %q", u.Host, r.Host)
	}
	return nil
}

// sessionKey is the context key for the authenticated session.
type sessionKey struct{}

func withSession(ctx context.Context, s auth.Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

// SessionFrom returns the session a request was authenticated with.
//
// It is what an audited write handler names as the actor, so that
// audit_events.actor is something true rather than a guess.
func SessionFrom(ctx context.Context) (auth.Session, bool) {
	s, ok := ctx.Value(sessionKey{}).(auth.Session)
	return s, ok
}

// recordAuth writes a sign-in or sign-out to the audit trail.
//
// **Nothing did.** `login.go` carried no audit call at all: every attempt was
// written to the log and none of it reached `audit_events`, while
// `ActionUserLogin`, `ActionUserLogout` and `OutcomeDenied` sat declared and
// unused. SECURITY.md says roles are deliberately absent because there is one
// kind of account that can do everything, and that the audit trail records who
// did what — which it could not answer for the question of who was in the
// system at all.
//
// Failures are recorded as well as successes, and a lockout distinctly from a
// wrong password: an attempt against a username that holds no account is the
// shape of somebody guessing, and a trail of successes alone cannot show it.
func (s *Server) recordAuth(r *http.Request, action audit.Action, username string, outcome audit.Outcome, note string) {
	if s.opts.Audit == nil {
		return
	}
	detail := map[string]string{}
	if note != "" {
		detail["reason"] = note
	}
	if len(detail) == 0 {
		detail = nil
	}
	if err := s.opts.Audit.Record(r.Context(), audit.Event{
		OccurredAt: time.Now().UTC(),
		Actor:      username,
		Action:     action,
		Outcome:    outcome,
		SourceIP:   clientIP(r, s.opts.BehindProxy),
		Detail:     detail,
	}); err != nil {
		// Warned rather than failed. A sign-in that succeeded is not undone by
		// a trail that could not be written, and refusing the request would
		// lock an operator out of a console over a database problem.
		s.log.Warn("cannot record an authentication in the audit trail", "error", err)
	}
}
