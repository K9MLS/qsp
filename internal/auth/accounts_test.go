package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/auth"
)

// memoryRepo is the storage the flow needs, in a map.
//
// It exists so that lockout, timing, session lifetime and revocation can be
// tested without a database — the SQL implementation is a thin adapter over the
// same six methods, and none of the behaviour worth testing lives in it.
type memoryRepo struct {
	mu       sync.Mutex
	nextID   int64
	accounts map[string]auth.Account // folded username -> account
	sessions map[string]auth.Session
	// failCreateSession makes storage fail, for the path where it does.
	failCreateSession bool
}

func newRepo() *memoryRepo {
	return &memoryRepo{
		accounts: make(map[string]auth.Account),
		sessions: make(map[string]auth.Session),
	}
}

func (r *memoryRepo) AccountByUsername(_ context.Context, fold string) (auth.Account, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accounts[fold]
	return a, ok, nil
}

func (r *memoryRepo) CreateAccount(_ context.Context, a auth.Account, fold string) (auth.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	a.ID = r.nextID
	r.accounts[fold] = a
	return a, nil
}

func (r *memoryRepo) UpdateAttempts(_ context.Context, id int64, failed int, locked, lastLogin time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for fold, a := range r.accounts {
		if a.ID == id {
			a.FailedCount = failed
			a.LockedUntil = locked
			a.LastLoginAt = lastLogin
			r.accounts[fold] = a
		}
	}
	return nil
}

func (r *memoryRepo) CreateSession(_ context.Context, s auth.Session) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failCreateSession {
		return errors.New("storage is unavailable")
	}
	r.sessions[s.Token] = s
	return nil
}

func (r *memoryRepo) SessionByToken(_ context.Context, token string) (auth.Session, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[token]
	return s, ok, nil
}

func (r *memoryRepo) DeleteSession(_ context.Context, token string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, token)
	return nil
}

func (r *memoryRepo) DeleteExpiredSessions(_ context.Context, now time.Time) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int
	for token, s := range r.sessions {
		if !now.Before(s.ExpiresAt) {
			delete(r.sessions, token)
			n++
		}
	}
	return n, nil
}

func (r *memoryRepo) sessionCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

const goodPassword = "a-passphrase-of-several-words"

// newService builds the flow with a cheap hash. The cost of PBKDF2 is the
// point in production and only slows the tests down here; DefaultParams is
// exercised by the password tests.
func newService(t *testing.T, repo auth.Repository, policy auth.Policy) (*auth.Service, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)}
	if policy.Hash.Iterations == 0 {
		policy.Hash = auth.Params{Iterations: 1000, SaltLength: 16, KeyLength: 32}
	}
	s, err := auth.NewService(repo, policy, c.now)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return s, c
}

func TestAnAccountCanLogIn(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	account, err := svc.CreateAccount(ctx, "K9MLS", goodPassword)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if account.ID == 0 {
		t.Error("the created account has no ID")
	}
	if account.PasswordHash == goodPassword {
		t.Fatal("the password was stored as itself")
	}

	session, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "192.0.2.1", "curl")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if session.Token == "" {
		t.Error("no session token")
	}
	if session.Username != "K9MLS" {
		t.Errorf("session names %q", session.Username)
	}
}

// TestUsernamesAreCaseInsensitive. Nobody thinks K9MLS and k9mls are two
// people, and an account they cannot log into because of a shift key is a
// support request.
func TestUsernamesAreCaseInsensitive(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := svc.Authenticate(ctx, "k9mls", goodPassword, "", ""); err != nil {
		t.Errorf("a lowercase username was refused: %v", err)
	}
	if _, err := svc.CreateAccount(ctx, "k9MLS", goodPassword); !errors.Is(err, auth.ErrUsernameTaken) {
		t.Errorf("a differently-cased duplicate was allowed: %v", err)
	}
	// The account keeps the name as entered.
	acct, _, _ := repo.AccountByUsername(ctx, "k9mls")
	if acct.Username != "K9MLS" {
		t.Errorf("the account stored %q rather than what was typed", acct.Username)
	}
}

// TestAnUnknownUsernameAnswersLikeAWrongPassword is the property that stops the
// login form being a way of asking which callsigns hold accounts.
func TestAnUnknownUsernameAnswersLikeAWrongPassword(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	_, unknown := svc.Authenticate(ctx, "NOBODY", goodPassword, "", "")
	_, wrong := svc.Authenticate(ctx, "K9MLS", "not-the-password", "", "")

	if !errors.Is(unknown, auth.ErrInvalidCredentials) {
		t.Errorf("an unknown username returned %v", unknown)
	}
	if !errors.Is(wrong, auth.ErrInvalidCredentials) {
		t.Errorf("a wrong password returned %v", wrong)
	}
	if unknown.Error() != wrong.Error() {
		t.Errorf("the two answers differ:\n  unknown: %v\n  wrong:   %v", unknown, wrong)
	}
}

// TestAnUnknownUsernameStillDoesTheWork. The equal error is only half of it: a
// name that answers in a microsecond while a real one takes a hash is the same
// disclosure by a different measure.
func TestAnUnknownUsernameStillDoesTheWork(t *testing.T) {
	repo := newRepo()
	// A cost high enough that the difference between doing the work and
	// skipping it is not lost in noise.
	svc, _ := newService(t, repo, auth.Policy{
		Hash: auth.Params{Iterations: 60000, SaltLength: 16, KeyLength: 32},
	})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	start := time.Now()
	_, _ = svc.Authenticate(ctx, "NOBODY", goodPassword, "", "")
	unknown := time.Since(start)

	start = time.Now()
	_, _ = svc.Authenticate(ctx, "K9MLS", "not-the-password", "", "")
	wrong := time.Since(start)

	// Not a timing assertion — those are flaky on shared hardware. The claim
	// is only that the unknown path is not trivially fast, which is what
	// skipping the derivation would make it.
	if unknown < wrong/4 {
		t.Errorf("an unknown username returned in %s against %s for a wrong password; "+
			"the decoy hash is not being verified", unknown, wrong)
	}
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	repo := newRepo()
	svc, clk := newService(t, repo, auth.Policy{MaxFailures: 3, Lockout: 10 * time.Minute})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := svc.Authenticate(ctx, "K9MLS", "wrong", "", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
			t.Fatalf("attempt %d returned %v", i+1, err)
		}
	}

	// Now locked — and the right password does not open it, which is what
	// makes the lock worth anything.
	if _, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "", ""); !errors.Is(err, auth.ErrLockedOut) {
		t.Fatalf("the account was not locked: %v", err)
	}

	clk.advance(11 * time.Minute)
	if _, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "", ""); err != nil {
		t.Errorf("the lock did not lift: %v", err)
	}
}

// TestTheCounterResetsWithTheLock. Without this the account would be one
// mistake from relocking for the rest of its life.
func TestTheCounterResetsWithTheLock(t *testing.T) {
	repo := newRepo()
	svc, clk := newService(t, repo, auth.Policy{MaxFailures: 2, Lockout: time.Minute})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	svc.Authenticate(ctx, "K9MLS", "wrong", "", "")
	svc.Authenticate(ctx, "K9MLS", "wrong", "", "")
	clk.advance(2 * time.Minute)

	// One wrong attempt after the lock lifts must not lock it again.
	if _, err := svc.Authenticate(ctx, "K9MLS", "wrong", "", ""); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("got %v", err)
	}
	if _, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "", ""); err != nil {
		t.Errorf("the account relocked after one mistake: %v", err)
	}
}

// TestASuccessClearsTheCount. Somebody who mistypes twice and then gets it
// right should not be one mistake from a lockout tomorrow.
func TestASuccessClearsTheCount(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{MaxFailures: 3})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	svc.Authenticate(ctx, "K9MLS", "wrong", "", "")
	svc.Authenticate(ctx, "K9MLS", "wrong", "", "")
	if _, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "", ""); err != nil {
		t.Fatalf("a good password was refused: %v", err)
	}

	// Two more failures must not lock it: the count started again.
	svc.Authenticate(ctx, "K9MLS", "wrong", "", "")
	svc.Authenticate(ctx, "K9MLS", "wrong", "", "")
	if _, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "", ""); err != nil {
		t.Errorf("the failure count survived a success: %v", err)
	}
}

func TestSessionsExpire(t *testing.T) {
	repo := newRepo()
	svc, clk := newService(t, repo, auth.Policy{SessionLifetime: time.Hour})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	session, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "", "")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	clk.advance(59 * time.Minute)
	if _, err := svc.Session(ctx, session.Token); err != nil {
		t.Errorf("a session expired early: %v", err)
	}

	clk.advance(2 * time.Minute)
	if _, err := svc.Session(ctx, session.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("an expired session was accepted: %v", err)
	}
	// Seen to be dead means gone, so it cannot be presented again.
	if repo.sessionCount() != 0 {
		t.Error("an expired session was left in storage after being refused")
	}
}

// TestExpiryIsCheckedServerSide. A cookie's own lifetime is a hint the browser
// may ignore, so the row is what decides.
func TestExpiryIsCheckedServerSide(t *testing.T) {
	repo := newRepo()
	svc, clk := newService(t, repo, auth.Policy{SessionLifetime: time.Minute})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	s, _ := svc.Authenticate(ctx, "K9MLS", goodPassword, "", "")

	clk.advance(2 * time.Minute)
	// The token is unchanged and the caller presents it exactly as before.
	if _, err := svc.Session(ctx, s.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("expiry was not enforced on the server: %v", err)
	}
}

func TestLoggingOutEndsTheSessionImmediately(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	s, _ := svc.Authenticate(ctx, "K9MLS", goodPassword, "", "")

	if err := svc.EndSession(ctx, s.Token); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if _, err := svc.Session(ctx, s.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Errorf("a logged-out session still worked: %v", err)
	}
}

func TestUnknownAndEmptyTokensAreRefused(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	for _, token := range []string{"", "not-a-token", strings.Repeat("a", 200)} {
		if _, err := svc.Session(ctx, token); !errors.Is(err, auth.ErrNoSession) {
			t.Errorf("token %q returned %v", token, err)
		}
	}
}

func TestSweepRemovesExpiredSessionsOnly(t *testing.T) {
	repo := newRepo()
	svc, clk := newService(t, repo, auth.Policy{SessionLifetime: time.Hour})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	old, _ := svc.Authenticate(ctx, "K9MLS", goodPassword, "", "")
	clk.advance(90 * time.Minute)
	fresh, _ := svc.Authenticate(ctx, "K9MLS", goodPassword, "", "")

	n, err := svc.SweepSessions(ctx)
	if err != nil {
		t.Fatalf("SweepSessions: %v", err)
	}
	if n != 1 {
		t.Errorf("swept %d sessions, want 1", n)
	}
	if _, err := svc.Session(ctx, fresh.Token); err != nil {
		t.Errorf("the sweep took a live session: %v", err)
	}
	if _, err := svc.Session(ctx, old.Token); !errors.Is(err, auth.ErrNoSession) {
		t.Error("the expired session survived the sweep")
	}
}

func TestTokensAreUniqueAndOpaque(t *testing.T) {
	seen := make(map[string]bool, 500)
	for i := 0; i < 500; i++ {
		token, err := auth.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if seen[token] {
			t.Fatal("NewToken returned a duplicate")
		}
		seen[token] = true
		if len(token) < 32 {
			t.Fatalf("token is only %d characters: %q", len(token), token)
		}
		// URL-safe, so it can be a cookie value without escaping.
		if strings.ContainsAny(token, "+/=;, ") {
			t.Fatalf("token needs escaping to be a cookie: %q", token)
		}
	}
}

func TestUsernameValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ok   bool
	}{
		{"K9MLS", true},
		{"k9mls", true},
		{"VK2-ABC", true},
		{"club_admin", true},
		{"F/K9MLS", true},
		{"", false},
		{"   ", false},
		{"has space", false},
		{"drop;table", false},
		{"<script>", false},
		{strings.Repeat("a", 65), false},
	} {
		err := auth.ValidateUsername(tc.name)
		if tc.ok && err != nil {
			t.Errorf("%q was refused: %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%q was accepted", tc.name)
		}
	}
}

func TestAccountCreationEnforcesThePasswordRules(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	if _, err := svc.CreateAccount(ctx, "K9MLS", "short"); err == nil {
		t.Error("a short password was accepted")
	}
	// And nothing was written.
	if _, found, _ := repo.AccountByUsername(ctx, "k9mls"); found {
		t.Error("a refused account was stored anyway")
	}
}

// TestAFailedSessionWriteIsNotASilentLogin. Returning a session the store never
// kept would give the caller a cookie that stops working on the next request.
func TestAFailedSessionWriteIsNotASilentLogin(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	repo.failCreateSession = true

	if _, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "", ""); err == nil {
		t.Error("a login succeeded although the session could not be stored")
	}
}

func TestNewServiceRequiresARepository(t *testing.T) {
	if _, err := auth.NewService(nil, auth.Policy{}, nil); err == nil {
		t.Error("a service was built with no repository")
	}
}
