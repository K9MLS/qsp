package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
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
	// ResetPassword ends the account's sessions except the one whose token
	// is keep.
	ResetPassword(ctx context.Context, username, password, keep string) error
	// ChangePassword replaces the password of the account the session
	// belongs to, given the current one, and ends its other sessions.
	ChangePassword(ctx context.Context, token, current, next, ip string) error
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

	password, err := peering.NewAccountPassword()
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot generate a password"})
		return
	}

	account, err := s.opts.Accounts.CreateAccount(r.Context(),
		strings.TrimSpace(req.Username), password)
	if err != nil {
		s.recordAccount(r, audit.ActionUserCreated, req.Username, audit.OutcomeFailure, err.Error())
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.recordAccount(r, audit.ActionUserCreated, account.Username, audit.OutcomeSuccess, "")

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
	password, err := peering.NewAccountPassword()
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot generate a password"})
		return
	}

	// An administrator resetting their own keeps the session they are doing
	// it from, or the answer carrying the new password is the last thing
	// that session does and the page showing it is replaced by the sign-in.
	var keep string
	if sess, ok := SessionFrom(r.Context()); ok &&
		auth.NormaliseUsername(sess.Username) == auth.NormaliseUsername(name) {
		keep = sess.Token
	}

	if err := s.opts.Accounts.ResetPassword(r.Context(), name, password, keep); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, auth.ErrNoSuchAccount) {
			status = http.StatusNotFound
		}
		s.recordAccount(r, audit.ActionUserPasswordReset, name, audit.OutcomeFailure, err.Error())
		writeJSON(w, s.log, status, map[string]string{"error": err.Error()})
		return
	}
	s.recordAccount(r, audit.ActionUserPasswordReset, name, audit.OutcomeSuccess, "")

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"username": name,
		"password": password,
		"note": "Give this to " + name + " now. Any lockout on the account is cleared, " +
			"and anywhere they were signed in they have been signed out.",
	})
}

// changePasswordRequest is an administrator changing their own password.
type changePasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// handleChangePassword replaces the signed-in administrator's password.
//
// **Their own, and no name is read from the request.** The account is the
// one the session belongs to, so there is nothing here to point at somebody
// else's.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if s.opts.Accounts == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance has no account store"})
		return
	}
	var req changePasswordRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}
	sess, ok := SessionFrom(r.Context())
	if !ok {
		writeJSON(w, s.log, http.StatusUnauthorized, map[string]string{"error": "sign in first"})
		return
	}

	err := s.opts.Accounts.ChangePassword(r.Context(), sess.Token, req.Current, req.New,
		clientIP(r, s.opts.BehindProxy))
	if err == nil {
		s.recordAccount(r, audit.ActionUserPasswordChanged, sess.Username, audit.OutcomeSuccess, "")
		writeJSON(w, s.log, http.StatusOK, map[string]string{
			"note": "Your password is changed. You are still signed in here, and " +
				"anywhere else you were signed in you have been signed out.",
		})
		return
	}

	// **Never 401.** That is the answer that means "you are not signed in",
	// and the page would act on it; a wrong current password is a refusal of
	// this request by somebody who is.
	status, reason := http.StatusBadRequest, err.Error()
	var message string
	var lockout *auth.Lockout
	switch {
	case errors.As(err, &lockout), errors.Is(err, auth.ErrLockedOut):
		status = http.StatusTooManyRequests
		message = "too many wrong passwords from your address; wait a few minutes and try again"
		reason = "locked out"
		if lockout != nil {
			if left := time.Until(lockout.Until); left > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(left/time.Second)+1))
			}
		}
	case errors.Is(err, auth.ErrInvalidCredentials):
		status = http.StatusForbidden
		message = "the current password is not right; nothing was changed"
		reason = "wrong current password"
	case errors.Is(err, auth.ErrPasswordTooShort):
		message = "the new password is too short: use at least " +
			strconv.Itoa(auth.MinPasswordLength) + " characters. A few words together work well"
		reason = "new password too short"
	case errors.Is(err, auth.ErrPasswordTooLong):
		message = "the new password is too long"
		reason = "new password too long"
	case errors.Is(err, auth.ErrSamePassword):
		message = "the new password is the one you have now; choose a different one"
		reason = "new password unchanged"
	case errors.Is(err, auth.ErrNoSession):
		status = http.StatusUnauthorized
		message = "sign in first"
		reason = "no session"
	default:
		status = http.StatusInternalServerError
		message = "the password could not be changed; the server's log says why"
		s.log.Error("cannot change a password", "username", sess.Username, "error", err)
	}
	s.recordAccount(r, audit.ActionUserPasswordChanged, sess.Username, audit.OutcomeFailure, reason)
	writeJSON(w, s.log, status, map[string]string{"error": message})
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
		s.recordAccount(r, audit.ActionUserDeleted, name, audit.OutcomeFailure, err.Error())
		writeJSON(w, s.log, status, map[string]string{"error": err.Error()})
		return
	}
	s.recordAccount(r, audit.ActionUserDeleted, name, audit.OutcomeSuccess, "")

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"username": name,
		"note":     "Removed. Any session they held has ended.",
	})
}
