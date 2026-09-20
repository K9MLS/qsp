package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Defaults for Policy.
const (
	// DefaultSessionLifetime is how long a session lasts.
	//
	// Long enough that an administrator setting up a club network is not logged
	// out mid-task, short enough that a browser left open on a shared machine
	// stops being a way in by the next day.
	DefaultSessionLifetime = 12 * time.Hour

	// DefaultMaxFailures is how many wrong passwords an account tolerates
	// before it stops accepting attempts.
	DefaultMaxFailures = 5

	// DefaultLockout is how long it then refuses them.
	//
	// **This bounds guessing, it does not stop it.** Five attempts every
	// fifteen minutes is twenty an hour, which is useless against a password
	// worth having and ruinous against a weak one — which is why
	// MinPasswordLength exists and why this is not the only defence.
	DefaultLockout = 15 * time.Minute

	// tokenBytes is the entropy in a session token. 32 bytes is more than the
	// 128 bits that matters and costs nothing.
	tokenBytes = 32
)

// Errors returned by Service.
var (
	// ErrInvalidCredentials means the username or the password was wrong.
	//
	// **One error for both**, deliberately. Distinguishing them turns the login
	// form into a way of asking whether a callsign holds an account here.
	ErrInvalidCredentials = errors.New("auth: the username or password is incorrect")
	// ErrLockedOut means the account is refusing attempts for now.
	ErrLockedOut = errors.New("auth: too many failed attempts; try again later")
	// ErrNoSession means the token is unknown, expired, or belongs to an
	// account that no longer exists.
	ErrNoSession = errors.New("auth: no such session")
	// ErrUsernameTaken means an account with that name already exists.
	ErrUsernameTaken = errors.New("auth: that username is taken")
	// ErrInvalidUsername means the name is empty or holds something a callsign
	// does not.
	ErrInvalidUsername = errors.New("auth: invalid username")
)

// Account is an administrator.
type Account struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
	LastLoginAt  time.Time
	FailedCount  int
	LockedUntil  time.Time
}

// Session is a logged-in browser.
type Session struct {
	Token     string
	UserID    int64
	Username  string
	CreatedAt time.Time
	ExpiresAt time.Time
	SourceIP  string
	UserAgent string
}

// Repository is the storage the login flow needs.
//
// It exists so the flow — lockout, timing, session lifetime, revocation — can
// be tested without a database. The SQL implementation is a thin adapter over
// these six methods, and the interesting behaviour is not in it.
type Repository interface {
	// AccountByUsername returns an account by its folded name. It returns
	// false, and no error, when there is no such account.
	AccountByUsername(ctx context.Context, fold string) (Account, bool, error)
	// CreateAccount stores a new account and returns it with its ID.
	CreateAccount(ctx context.Context, a Account, fold string) (Account, error)
	// UpdateAttempts records a login outcome.
	UpdateAttempts(ctx context.Context, id int64, failed int, lockedUntil, lastLogin time.Time) error
	// CreateSession stores a session.
	CreateSession(ctx context.Context, s Session) error
	// SessionByToken returns a session and its account's username.
	SessionByToken(ctx context.Context, token string) (Session, bool, error)
	// DeleteSession removes one session.
	DeleteSession(ctx context.Context, token string) error
	// DeleteExpiredSessions removes every session that has expired.
	DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error)
	// CountSessions returns how many sessions have not expired.
	//
	// **Counted in storage rather than tracked in memory.** A counter kept
	// here would be wrong after a restart, wrong about the sessions another
	// process issued, and would need its own sweep to stay right; the rows
	// already know.
	CountSessions(ctx context.Context, now time.Time) (int, error)
	// Accounts returns every account, oldest first.
	Accounts(ctx context.Context) ([]Account, error)
	// SetPassword replaces one account's hash.
	SetPassword(ctx context.Context, id int64, hash string) error
	// DeleteAccount removes an account and every session it holds.
	//
	// **One call, not two.** An account removed while its sessions survive is
	// removed for as long as a session lasts, which is not what an
	// administrator pressing the button believes they have done.
	DeleteAccount(ctx context.Context, id int64) error
}

// Policy is the tunable part of the flow.
type Policy struct {
	// SessionLifetime is how long a session lasts. Zero selects the default.
	SessionLifetime time.Duration
	// MaxFailures before an account locks. Zero selects the default.
	MaxFailures int
	// Lockout is how long it stays locked. Zero selects the default.
	Lockout time.Duration
	// Hash is the password hashing cost. The zero value selects
	// DefaultParams.
	Hash Params
}

func (p Policy) withDefaults() Policy {
	if p.SessionLifetime <= 0 {
		p.SessionLifetime = DefaultSessionLifetime
	}
	if p.MaxFailures <= 0 {
		p.MaxFailures = DefaultMaxFailures
	}
	if p.Lockout <= 0 {
		p.Lockout = DefaultLockout
	}
	if p.Hash.Iterations <= 0 {
		p.Hash = DefaultParams()
	}
	return p
}

// Service is the login flow.
//
// It is safe for concurrent use to the extent its Repository is: it holds no
// mutable state of its own.
type Service struct {
	repo   Repository
	policy Policy
	now    func() time.Time
	// decoy is a valid hash of a password nobody holds.
	//
	// **It is what makes an unknown username cost the same as a wrong one.**
	// Without it, a login for a name with no account returns before any key
	// derivation happens, and the difference is measurable from outside — which
	// turns the form into a way of asking which callsigns hold accounts here.
	decoy string
}

// NewService constructs the login flow.
func NewService(repo Repository, policy Policy, now func() time.Time) (*Service, error) {
	if repo == nil {
		return nil, errors.New("auth: a repository is required")
	}
	if now == nil {
		now = time.Now
	}
	policy = policy.withDefaults()

	// Derived once at construction so that the per-attempt cost is a
	// comparison rather than a derivation of its own.
	decoy, err := Hash("decoy-password-nobody-holds", policy.Hash)
	if err != nil {
		return nil, fmt.Errorf("auth: cannot prepare the login flow: %w", err)
	}
	return &Service{repo: repo, policy: policy, now: now, decoy: decoy}, nil
}

// NormaliseUsername folds a username for comparison.
//
// Callsigns are case-insensitive in practice — nobody thinks K9MLS and k9mls
// are two people — so the folded form is what uniqueness is enforced on, while
// the account keeps the name as it was entered.
func NormaliseUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// ValidateUsername checks a username is something a person could hold.
func ValidateUsername(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("%w: must not be empty", ErrInvalidUsername)
	}
	if len(trimmed) > 64 {
		return fmt.Errorf("%w: must be at most 64 characters", ErrInvalidUsername)
	}
	for _, r := range trimmed {
		// Letters, digits, and the few punctuation marks callsigns and
		// usernames actually use. Refusing spaces and control characters here
		// keeps a username out of places it would need escaping.
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '/' {
			continue
		}
		return fmt.Errorf("%w: %q is not allowed; use letters, digits, and - _ /",
			ErrInvalidUsername, r)
	}
	return nil
}

// CreateAccount adds an administrator.
//
// It is called from the command line and from nowhere else; see ADR-0026. The
// web surface has no unauthenticated path that reaches it.
func (s *Service) CreateAccount(ctx context.Context, username, password string) (Account, error) {
	if err := ValidateUsername(username); err != nil {
		return Account{}, err
	}
	if err := ValidatePassword(password); err != nil {
		return Account{}, err
	}

	username = strings.TrimSpace(username)
	fold := NormaliseUsername(username)

	if _, found, err := s.repo.AccountByUsername(ctx, fold); err != nil {
		return Account{}, err
	} else if found {
		return Account{}, fmt.Errorf("%w: %s", ErrUsernameTaken, username)
	}

	hash, err := Hash(password, s.policy.Hash)
	if err != nil {
		return Account{}, err
	}
	return s.repo.CreateAccount(ctx, Account{
		Username:     username,
		PasswordHash: hash,
		CreatedAt:    s.now().UTC(),
	}, fold)
}

// Authenticate checks a username and password and returns a new session.
func (s *Service) Authenticate(ctx context.Context, username, password, ip, agent string) (Session, error) {
	now := s.now().UTC()
	fold := NormaliseUsername(username)

	account, found, err := s.repo.AccountByUsername(ctx, fold)
	if err != nil {
		return Session{}, err
	}

	if !found {
		// Do the work anyway. An unknown name that answers in a microsecond
		// while a known one takes a hundred milliseconds is a way of listing
		// which callsigns hold accounts.
		_ = Verify(password, s.decoy)
		return Session{}, ErrInvalidCredentials
	}

	// The lock is checked before the password so that a locked account costs
	// nothing to refuse, which is the point of locking it.
	if !account.LockedUntil.IsZero() && now.Before(account.LockedUntil) {
		return Session{}, ErrLockedOut
	}

	if err := Verify(password, account.PasswordHash); err != nil {
		failed := account.FailedCount + 1
		var lockedUntil time.Time
		if failed >= s.policy.MaxFailures {
			lockedUntil = now.Add(s.policy.Lockout)
			// The counter resets with the lock, so the next window is a fresh
			// set of attempts rather than one attempt and an immediate relock.
			failed = 0
		}
		if err := s.repo.UpdateAttempts(ctx, account.ID, failed, lockedUntil, account.LastLoginAt); err != nil {
			return Session{}, err
		}
		return Session{}, ErrInvalidCredentials
	}

	// A success clears the count. Somebody who mistypes twice and then gets it
	// right should not be one mistake from a lockout tomorrow.
	if err := s.repo.UpdateAttempts(ctx, account.ID, 0, time.Time{}, now); err != nil {
		return Session{}, err
	}

	token, err := NewToken()
	if err != nil {
		return Session{}, err
	}
	session := Session{
		Token:     token,
		UserID:    account.ID,
		Username:  account.Username,
		CreatedAt: now,
		ExpiresAt: now.Add(s.policy.SessionLifetime),
		SourceIP:  ip,
		UserAgent: agent,
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return Session{}, err
	}
	return session, nil
}

// Unlock clears an account's failed attempts and its lock.
//
// **The recovery path is the host, as it is for the password.** Fifteen minutes
// is a short wait for somebody guessing and a long one for an operator who
// fat-fingered their own passphrase five times, and the person with shell
// access on the machine is already trusted with more than this.
//
// It reports whether there was such an account, so the caller can say "no such
// user" rather than silently doing nothing.
func (s *Service) Unlock(ctx context.Context, username string) (bool, error) {
	account, found, err := s.repo.AccountByUsername(ctx, NormaliseUsername(username))
	if err != nil || !found {
		return false, err
	}
	if err := s.repo.UpdateAttempts(ctx, account.ID, 0, time.Time{}, account.LastLoginAt); err != nil {
		return false, err
	}
	return true, nil
}

// Session returns the session a token names, if it is still good.
//
// **Expiry is checked here rather than trusted to the cookie.** A cookie's own
// lifetime is a hint the browser may ignore, and a token that outlives its row
// is a session nobody can revoke.
func (s *Service) Session(ctx context.Context, token string) (Session, error) {
	if token == "" {
		return Session{}, ErrNoSession
	}
	session, found, err := s.repo.SessionByToken(ctx, token)
	if err != nil {
		return Session{}, err
	}
	if !found {
		return Session{}, ErrNoSession
	}
	if !s.now().UTC().Before(session.ExpiresAt) {
		// Removed on sight rather than left for the sweep, so a token that has
		// been seen to be dead cannot be presented again.
		_ = s.repo.DeleteSession(ctx, token)
		return Session{}, ErrNoSession
	}
	return session, nil
}

// EndSession logs a browser out.
func (s *Service) EndSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.repo.DeleteSession(ctx, token)
}

// SweepSessions removes expired sessions.
func (s *Service) SweepSessions(ctx context.Context) (int, error) {
	return s.repo.DeleteExpiredSessions(ctx, s.now().UTC())
}

// ActiveSessions is how many logins are currently valid.
//
// **The console reports it so that a number higher than the number of people
// who should be logged in is visible at all.** Expired sessions are swept on a
// timer rather than at expiry, so the count excludes them explicitly instead
// of trusting the sweep to have run.
func (s *Service) ActiveSessions(ctx context.Context) (int, error) {
	return s.repo.CountSessions(ctx, s.now().UTC())
}

// NewToken returns a session token.
//
// URL-safe base64 of 32 random bytes: opaque, carries no claims, and means
// nothing away from the row that stores it.
func NewToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("auth: cannot generate a session token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ErrLastAccount is a refusal to remove the only administrator.
//
// **A console able to lock an operator out of their own server is worse than
// one that refuses** (ADR-0056). Recovering from it means a shell, which is the
// thing the console exists to avoid.
var ErrLastAccount = errors.New("auth: this is the only administrator, and removing it would " +
	"leave nobody able to sign in")

// ErrNoSuchAccount is a name that is not an account.
var ErrNoSuchAccount = errors.New("auth: no such account")

// Accounts returns every account, oldest first.
func (s *Service) Accounts(ctx context.Context) ([]Account, error) {
	return s.repo.Accounts(ctx)
}

// AnyAccount reports whether this server has an administrator yet.
//
// **What the setup page turns on.** A server with no account serves setup and
// nothing else; a server with one refuses it (ADR-0056).
func (s *Service) AnyAccount(ctx context.Context) (bool, error) {
	accounts, err := s.repo.Accounts(ctx)
	if err != nil {
		return false, err
	}
	return len(accounts) > 0, nil
}

// ResetPassword sets a new password for an account and returns nothing.
//
// **The caller generates the password and shows it once.** One administrator
// choosing another's password means one administrator knowing another's
// password, which the peer credentials page already declines to do.
func (s *Service) ResetPassword(ctx context.Context, username, password string) error {
	account, ok, err := s.repo.AccountByUsername(ctx, NormaliseUsername(username))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoSuchAccount, username)
	}
	// **No explicit ValidatePassword here.** `Hash` applies the policy itself,
	// and a second call would be a safeguard that cannot fail — removing it
	// changed no behaviour, which is how it was found.
	hash, err := Hash(password, s.policy.Hash)
	if err != nil {
		return err
	}
	return s.repo.SetPassword(ctx, account.ID, hash)
}

// RemoveAccount deletes an account and every session it holds.
func (s *Service) RemoveAccount(ctx context.Context, username string) error {
	accounts, err := s.repo.Accounts(ctx)
	if err != nil {
		return err
	}
	if len(accounts) <= 1 {
		return ErrLastAccount
	}

	fold := NormaliseUsername(username)
	account, ok, err := s.repo.AccountByUsername(ctx, fold)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: %q", ErrNoSuchAccount, username)
	}
	return s.repo.DeleteAccount(ctx, account.ID)
}
