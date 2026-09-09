package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/k9mls/qsp/internal/auth"
)

// **A console able to lock an operator out of their own server is worse than
// one that refuses** (ADR-0056). Recovery means a shell, which is the thing the
// console exists to avoid.
func TestTheLastAdministratorCannotBeRemoved(t *testing.T) {
	repo := newRepo()
	s, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	if _, err := s.CreateAccount(ctx, "K9MLS", "a-long-enough-password"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	if err := s.RemoveAccount(ctx, "K9MLS"); !errors.Is(err, auth.ErrLastAccount) {
		t.Fatalf("removing the only account gave %v, want ErrLastAccount", err)
	}

	// With a second, the first may go.
	if _, err := s.CreateAccount(ctx, "KD9EJA", "another-long-password"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := s.RemoveAccount(ctx, "K9MLS"); err != nil {
		t.Fatalf("removing one of two accounts: %v", err)
	}

	// And now the survivor is the last one.
	if err := s.RemoveAccount(ctx, "KD9EJA"); !errors.Is(err, auth.ErrLastAccount) {
		t.Errorf("the survivor was removable: %v", err)
	}
}

// **Otherwise "removed" means "removed in about a fortnight"**, which is the
// session lifetime, and an administrator pressing the button believes
// otherwise.
func TestRemovingAnAccountEndsItsSessions(t *testing.T) {
	repo := newRepo()
	s, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	if _, err := s.CreateAccount(ctx, "K9MLS", "a-long-enough-password"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if _, err := s.CreateAccount(ctx, "KD9EJA", "another-long-password"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	session, err := s.Authenticate(ctx, "KD9EJA", "another-long-password", "10.0.0.1", "test")
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := s.Session(ctx, session.Token); err != nil {
		t.Fatalf("the session does not work before removal: %v", err)
	}

	if err := s.RemoveAccount(ctx, "KD9EJA"); err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if _, err := s.Session(ctx, session.Token); err == nil {
		t.Error("the removed account's session still works")
	}
}

// A reset clears the lockout: an operator resetting a password for somebody
// locked out has answered the question the lockout was asking, and leaving it
// would make the new password appear not to work.
func TestAResetReplacesThePasswordAndClearsTheLockout(t *testing.T) {
	repo := newRepo()
	s, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	if _, err := s.CreateAccount(ctx, "K9MLS", "the-old-password"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if err := s.ResetPassword(ctx, "K9MLS", "the-new-password"); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	if _, err := s.Authenticate(ctx, "K9MLS", "the-new-password", "10.0.0.1", "t"); err != nil {
		t.Errorf("the new password does not work: %v", err)
	}
	if _, err := s.Authenticate(ctx, "K9MLS", "the-old-password", "10.0.0.1", "t"); err == nil {
		t.Error("the old password still works")
	}

	// **A password creation would refuse is refused here too**, or a reset
	// becomes the way round the policy. Enforced by `Hash` rather than by this
	// method — an explicit check here was removed after it turned out removing
	// it changed nothing, which is a safeguard that cannot fail.
	if err := s.ResetPassword(ctx, "K9MLS", "short"); err == nil {
		t.Error("a reset accepted a password creation would refuse")
	}
	if err := auth.ValidatePassword("short"); err == nil {
		t.Fatal("the policy accepts a short password, so the assertion above " +
			"proves nothing about resets")
	}
	if err := s.ResetPassword(ctx, "NOBODY", "a-long-enough-password"); !errors.Is(err, auth.ErrNoSuchAccount) {
		t.Errorf("resetting an unknown account gave %v, want ErrNoSuchAccount", err)
	}
}

// **What the setup page turns on**: a server with no account serves setup and
// nothing else; one with an account refuses it (ADR-0056).
func TestAnyAccountAnswersWhetherSetupIsStillOpen(t *testing.T) {
	repo := newRepo()
	s, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()

	any, err := s.AnyAccount(ctx)
	if err != nil {
		t.Fatalf("AnyAccount: %v", err)
	}
	if any {
		t.Fatal("a fresh server reports an administrator")
	}

	if _, err := s.CreateAccount(ctx, "K9MLS", "a-long-enough-password"); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if any, err = s.AnyAccount(ctx); err != nil || !any {
		t.Errorf("a server with an account reports none (%v)", err)
	}
}
