package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/auth"
)

// The first administrator (ADR-0056).
//
// # Why this exists at all
//
// ADR-0026 made the first account from a shell and gave a real reason: a QSP
// reachable from the internet with an empty database belongs to whoever loads
// it first, and the operator would never know, because the result looks exactly
// like a working setup.
//
// ADR-0056 answers that rather than ignoring it. The console became the whole
// product, and a project whose premise is that an operator should never need a
// terminal cannot require one for the first thing an operator does. So the page
// exists **and something guards it**: without the token an attacker who wins
// the race gets a form they cannot submit.
//
// # The token
//
// Generated when the server starts with no account, logged once, and held in
// memory only — a restart mints a new one and nothing is written to disk, so
// there is no file to leak or to forget about. It stops working the instant an
// account exists.
//
// # Loopback needs no token
//
// A request arriving on the loopback interface is from somebody already on the
// machine, who could read the token from the journal in any case. Skipping it
// there **recognises a check that has already been passed** rather than
// removing one — and it is what makes an ordinary single-machine install need
// no terminal at all.

// SetupAccounts is what the setup page needs of the account store.
type SetupAccounts interface {
	// AnyAccount reports whether an administrator exists.
	AnyAccount(ctx context.Context) (bool, error)
	// CreateAccount makes one.
	CreateAccount(ctx context.Context, username, password string) (auth.Account, error)
}

// setupState holds the one-time token.
//
// **In memory, never on disk.** A token that survived a restart would be a
// token that survives an operator walking away from a half-configured server.
type setupState struct {
	mu    sync.Mutex
	token string
	// done latches once an account exists, so the page stops answering without
	// asking the database on every request.
	done bool
}

// setupTokenBytes is the length of the token before hex encoding.
//
// 16 bytes. It guards a window measured in minutes on a working server, and it
// is typed or pasted by a person, so it is sized to be unguessable rather than
// to survive an offline attack — there is nothing here to attack offline.
const setupTokenBytes = 16

// newSetupToken generates one.
func newSetupToken() (string, error) {
	b := make([]byte, setupTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("server: generating a setup token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// setupNeeded reports whether this server still has no administrator.
func (s *Server) setupNeeded(ctx context.Context) bool {
	if s.opts.Setup == nil {
		return false
	}
	s.setup.mu.Lock()
	done := s.setup.done
	s.setup.mu.Unlock()
	if done {
		return false
	}

	any, err := s.opts.Setup.AnyAccount(ctx)
	if err != nil {
		// **Unreachable is not "no administrator".** Serving the setup page
		// because a query failed would offer the server to anybody during a
		// database problem, which is exactly when nobody is watching.
		s.log.Warn("cannot tell whether this server has an administrator",
			"error", err.Error())
		return false
	}
	if any {
		s.setup.mu.Lock()
		s.setup.done = true
		s.setup.token = ""
		s.setup.mu.Unlock()
	}
	return !any
}

// fromLoopback reports whether a request came from this machine.
//
// **Read from the connection, never from a header.** `X-Forwarded-For` is
// whatever the client wrote, so trusting it here would let anybody claim to be
// local — which is the whole exemption, handed over.
func fromLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// setupBody is what the page reads before showing the form.
type setupBody struct {
	// Needed is false once an administrator exists.
	Needed bool `json:"needed"`
	// TokenRequired is false from loopback.
	TokenRequired bool `json:"token_required"`
	// Note explains where to find the token, when one is wanted.
	Note string `json:"note,omitempty"`
}

// handleSetupState tells the page whether to show the form, and whether to ask
// for a token.
func (s *Server) handleSetupState(w http.ResponseWriter, r *http.Request) {
	if !s.setupNeeded(r.Context()) {
		// **404, not an empty form.** A setup endpoint still answering on a
		// running network is a way in, and saying "not needed" tells somebody
		// probing that this is a QSP with an administrator.
		http.NotFound(w, r)
		return
	}

	body := setupBody{Needed: true, TokenRequired: !fromLoopback(r)}
	if body.TokenRequired {
		body.Note = "This server printed a setup token when it started. Find it with " +
			"`docker logs qsp` or `journalctl -u qsp`, and paste it below."
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// setupRequest creates the first administrator.
type setupRequest struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleSetup creates the first administrator, once.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !s.setupNeeded(r.Context()) {
		http.NotFound(w, r)
		return
	}

	var req setupRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}

	if !fromLoopback(r) {
		s.setup.mu.Lock()
		want := s.setup.token
		s.setup.mu.Unlock()

		// **Constant time, and a refusal that says nothing.** Comparing with ==
		// leaks the token a character at a time to somebody willing to measure,
		// and an error distinguishing "wrong token" from "no token" tells them
		// they are close.
		if want == "" || subtle.ConstantTimeCompare([]byte(req.Token), []byte(want)) != 1 {
			s.recordAuth(r, audit.ActionUserLogin, req.Username, audit.OutcomeFailure,
				"setup token refused")
			writeJSON(w, s.log, http.StatusForbidden, map[string]string{
				"error": "that setup token is not this server's. It was printed once when " +
					"QSP started; if you cannot find it, restart QSP and it will print a new one",
			})
			return
		}
	}

	account, err := s.opts.Setup.CreateAccount(r.Context(),
		strings.TrimSpace(req.Username), req.Password)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// Latched here as well as on the next read, so the window closes on this
	// request rather than on whichever one happens to come next.
	s.setup.mu.Lock()
	s.setup.done = true
	s.setup.token = ""
	s.setup.mu.Unlock()

	s.log.Info("the first administrator was created", "username", account.Username)
	s.recordAuth(r, audit.ActionUserCreated, account.Username, audit.OutcomeSuccess, "setup")

	writeJSON(w, s.log, http.StatusCreated, map[string]any{
		"username": account.Username,
		"note":     "Sign in with the account you just made. This page will not work again.",
	})
}

// PrepareSetup mints a setup token when this server has no administrator, and
// logs it once.
//
// Called at startup. **Logged rather than displayed**, so it is not visible to
// whoever reaches the page, and once rather than repeatedly, so it does not
// scroll past in a busy journal every minute.
func (s *Server) PrepareSetup(ctx context.Context) error {
	if s.opts.Setup == nil {
		return nil
	}
	any, err := s.opts.Setup.AnyAccount(ctx)
	if err != nil {
		return err
	}
	if any {
		s.setup.mu.Lock()
		s.setup.done = true
		s.setup.mu.Unlock()
		return nil
	}

	token, err := newSetupToken()
	if err != nil {
		return err
	}
	s.setup.mu.Lock()
	s.setup.token = token
	s.setup.mu.Unlock()

	s.log.Warn("this server has no administrator and is waiting to be set up",
		"open", "the console",
		"setup_token", token,
		"note", "no token is needed from this machine; the token is for setting up over a network",
		"expires", "when the first administrator is created, or at the next restart",
	)
	_ = time.Now
	return nil
}
