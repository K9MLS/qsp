package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/peering"
)

// Administrators after the first (ADR-0056).
//
// **No token here, and no shell.** An administrator adding another is already
// authenticated, and that is the check — the first account is the only one that
// needs a way in from nothing.

// AccountAdmin is what the users page needs of the account store.
type AccountAdmin interface {
	Accounts(ctx context.Context) ([]auth.Account, error)
	CreateAccount(ctx context.Context, username, password string) (auth.Account, error)
	ResetPassword(ctx context.Context, username, password string) error
	RemoveAccount(ctx context.Context, username string) error
}

// userView is one administrator, as the page sees them.
//
// **No password hash, not even truncated.** There is no question a console
// answers with one, and a field that exists is a field that ends up in a
// screenshot.
type userView struct {
	Username string `json:"username"`
	Created  string `json:"created"`
	// LastSeen is empty for an account that has never signed in — which is a
	// fact worth showing, because an administrator created months ago and never
	// used is one nobody will miss when it is removed.
	LastSeen string `json:"last_seen,omitempty"`
	// Locked reports a lockout in force right now.
	Locked bool `json:"locked,omitempty"`
	// Self marks the account making the request, so the page can say "you"
	// rather than offering to remove somebody their own session belongs to
	// without warning.
	Self bool `json:"self,omitempty"`
}

// handleUsers lists the administrators.
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if s.opts.Accounts == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance has no account store"})
		return
	}

	accounts, err := s.opts.Accounts.Accounts(r.Context())
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": err.Error()})
		return
	}

	var me string
	if sess, ok := SessionFrom(r.Context()); ok {
		me = auth.NormaliseUsername(sess.Username)
	}

	now := time.Now().UTC()
	out := make([]userView, 0, len(accounts))
	for _, a := range accounts {
		v := userView{
			Username: a.Username,
			Created:  a.CreatedAt.Format("2006-01-02"),
			Locked:   !a.LockedUntil.IsZero() && a.LockedUntil.After(now),
			Self:     auth.NormaliseUsername(a.Username) == me,
		}
		if !a.LastLoginAt.IsZero() {
			v.LastSeen = a.LastLoginAt.Format("2006-01-02")
		}
		out = append(out, v)
	}
	writeJSON(w, s.log, http.StatusOK, map[string]any{"users": out})
}

// addUserRequest names a new administrator.
//
// **No password field.** QSP generates it and shows it once, the way the peer
// credentials page already does, so one administrator never knows another's
// password and nobody types a weak one for somebody else.
type addUserRequest struct {
	Username string `json:"username"`
}

// handleAddUser creates an administrator and returns their password once.
func (s *Server) handleAddUser(w http.ResponseWriter, r *http.Request) {
	if s.opts.Accounts == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance has no account store"})
		return
	}

	var req addUserRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}

	password, err := peering.NewPassphrase()
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot generate a password"})
		return
	}

	account, err := s.opts.Accounts.CreateAccount(r.Context(),
		strings.TrimSpace(req.Username), password)
	if err != nil {
		s.recordAuth(r, audit.ActionUserCreated, req.Username, audit.OutcomeFailure, err.Error())
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.recordAuth(r, audit.ActionUserCreated, account.Username, audit.OutcomeSuccess, "")

	writeJSON(w, s.log, http.StatusCreated, map[string]any{
		"username": account.Username,
		"password": password,
		"note": "Give this to " + account.Username + " now. It is not stored and " +
			"cannot be shown again; if it is lost, reset it from this page.",
	})
}

// handleResetPassword mints a new password for an account.
func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	if s.opts.Accounts == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance has no account store"})
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	password, err := peering.NewPassphrase()
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot generate a password"})
		return
	}

	if err := s.opts.Accounts.ResetPassword(r.Context(), name, password); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, auth.ErrNoSuchAccount) {
			status = http.StatusNotFound
		}
		s.recordAuth(r, audit.ActionUserPasswordReset, name, audit.OutcomeFailure, err.Error())
		writeJSON(w, s.log, status, map[string]string{"error": err.Error()})
		return
	}
	s.recordAuth(r, audit.ActionUserPasswordReset, name, audit.OutcomeSuccess, "")

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"username": name,
		"password": password,
		"note": "Give this to " + name + " now. Any lockout on the account is cleared, " +
			"and their existing sessions still work until they expire.",
	})
}

// handleRemoveUser deletes an administrator and ends their sessions.
func (s *Server) handleRemoveUser(w http.ResponseWriter, r *http.Request) {
	if s.opts.Accounts == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance has no account store"})
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if err := s.opts.Accounts.RemoveAccount(r.Context(), name); err != nil {
		status := http.StatusBadRequest
		switch {
		case errors.Is(err, auth.ErrNoSuchAccount):
			status = http.StatusNotFound
		case errors.Is(err, auth.ErrLastAccount):
			// **409, not 400.** The request was well formed and is refused
			// because of the state of the server, which is what a conflict
			// means and what tells a page to explain rather than to blame the
			// input.
			status = http.StatusConflict
		}
		s.recordAuth(r, audit.ActionUserDeleted, name, audit.OutcomeFailure, err.Error())
		writeJSON(w, s.log, status, map[string]string{"error": err.Error()})
		return
	}
	s.recordAuth(r, audit.ActionUserDeleted, name, audit.OutcomeSuccess, "")

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"username": name,
		"note":     "Removed. Any session they held has ended.",
	})
}
