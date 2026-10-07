package auth_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/database"
	"github.com/k9mls/qsp/internal/database/dbtest"
)

const newPassword = "a-different-passphrase"

// TestChangingYourOwnPassword is every way the form can be filled in.
//
// To see it fail, one at a time, in Service.ChangePassword: skip the verify
// (the wrong-current cases); drop `s.throttle.fail` (the guessing case); drop
// the ValidatePassword call and the ErrSamePassword check (the two refusals
// of the new one).
func TestChangingYourOwnPassword(t *testing.T) {
	const here = "198.51.100.7"
	tests := []struct {
		name          string
		current, next string
		// guesses is how many wrong current passwords come first.
		guesses int
		want    error
		// changed reports whether the new password is the one that works after.
		changed bool
	}{
		{name: "the right current password", current: goodPassword, next: newPassword, changed: true},
		{name: "a wrong current password", current: "not-the-password", next: newPassword,
			want: auth.ErrInvalidCredentials},
		{name: "no current password", current: "", next: newPassword, want: auth.ErrInvalidCredentials},
		{name: "a new one that is too short", current: goodPassword, next: "short",
			want: auth.ErrPasswordTooShort},
		{name: "a new one that is the old one", current: goodPassword, next: goodPassword,
			want: auth.ErrSamePassword},
		{name: "the right password after too many wrong ones", current: goodPassword, next: newPassword,
			guesses: 3, want: auth.ErrLockedOut},
		{name: "the right password after fewer", current: goodPassword, next: newPassword,
			guesses: 2, changed: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo()
			svc, _ := newService(t, repo, auth.Policy{MaxFailures: 3, Lockout: 10 * time.Minute})
			ctx := context.Background()
			if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}
			session, err := svc.Authenticate(ctx, "K9MLS", goodPassword, here, "")
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}

			for range tc.guesses {
				if err := svc.ChangePassword(ctx, session.Token, "a-guess-at-it", newPassword, here); !errors.Is(err, auth.ErrInvalidCredentials) {
					t.Fatalf("a wrong guess returned %v", err)
				}
			}
			if err := svc.ChangePassword(ctx, session.Token, tc.current, tc.next, here); !errors.Is(err, tc.want) {
				t.Fatalf("returned %v, want %v", err, tc.want)
			}

			// From another address, so a refusal earned above is not what answers.
			works := newPassword
			if !tc.changed {
				works = goodPassword
			}
			if _, err := svc.Authenticate(ctx, "K9MLS", works, "203.0.113.9", ""); err != nil {
				t.Errorf("%q does not sign in afterwards: %v", works, err)
			}
			if tc.changed {
				if _, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "203.0.113.10", ""); err == nil {
					t.Error("the old password still signs in")
				}
			}
		})
	}
}

// TestChangingAPasswordNeedsASession. The form is behind a sign-in, and this
// is the layer that would be reached if that were ever got wrong.
func TestChangingAPasswordNeedsASession(t *testing.T) {
	repo := newRepo()
	svc, _ := newService(t, repo, auth.Policy{})
	ctx := context.Background()
	if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	for _, token := range []string{"", "not-a-session"} {
		if err := svc.ChangePassword(ctx, token, goodPassword, newPassword, ""); !errors.Is(err, auth.ErrNoSession) {
			t.Errorf("token %q returned %v, want ErrNoSession", token, err)
		}
	}
}

// TestANewPasswordEndsTheOtherSessions. A password is replaced because it is
// forgotten or because somebody else has it, and in the second case they are
// signed in. Their session used to go on working until it expired.
//
// Each case runs against the in-memory repository and against SQLite.
//
// To see it fail: in SQLRepository.SetPassword and the test repository's,
// delete the statement that removes sessions. For "keeps", pass "" as keep
// from ChangePassword.
func TestANewPasswordEndsTheOtherSessions(t *testing.T) {
	tests := []struct {
		name string
		// act replaces K9MLS's password; mine is the session doing it.
		act func(ctx context.Context, svc *auth.Service, mine string) error
		// keeps reports whether the session that did it survives.
		keeps bool
	}{
		{"changing your own keeps the session you did it from",
			func(ctx context.Context, svc *auth.Service, mine string) error {
				return svc.ChangePassword(ctx, mine, goodPassword, newPassword, "")
			}, true},
		{"resetting your own from the users list keeps it too",
			func(ctx context.Context, svc *auth.Service, mine string) error {
				return svc.ResetPassword(ctx, "K9MLS", newPassword, mine)
			}, true},
		{"a reset by somebody else ends every one",
			func(ctx context.Context, svc *auth.Service, mine string) error {
				return svc.ResetPassword(ctx, "K9MLS", newPassword, "")
			}, false},
		{"somebody else's session is not a reason to keep anything",
			func(ctx context.Context, svc *auth.Service, mine string) error {
				other, err := svc.Authenticate(ctx, "KD9EJA", goodPassword, "", "")
				if err != nil {
					return err
				}
				return svc.ResetPassword(ctx, "K9MLS", newPassword, other.Token)
			}, false},
	}
	repos := []struct {
		name string
		make func(t *testing.T) auth.Repository
	}{
		{"in memory", func(t *testing.T) auth.Repository { return newRepo() }},
		{"sqlite", func(t *testing.T) auth.Repository {
			dbtest.NeedSQLite(t)
			ctx := context.Background()
			db, err := database.Open(ctx, nil, database.Options{
				Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "qsp.db"),
			})
			if err != nil {
				t.Fatalf("opening a database: %v", err)
			}
			t.Cleanup(func() { db.Close() })
			if _, err := db.Migrate(ctx); err != nil {
				t.Fatalf("migrating: %v", err)
			}
			repo, err := auth.NewSQLRepository(db.SQL())
			if err != nil {
				t.Fatalf("NewSQLRepository: %v", err)
			}
			return repo
		}},
	}
	for _, rp := range repos {
		for _, tc := range tests {
			t.Run(rp.name+"/"+tc.name, func(t *testing.T) {
				svc, _ := newService(t, rp.make(t), auth.Policy{})
				ctx := context.Background()
				for _, name := range []string{"K9MLS", "KD9EJA"} {
					if _, err := svc.CreateAccount(ctx, name, goodPassword); err != nil {
						t.Fatalf("CreateAccount: %v", err)
					}
				}
				mine, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "198.51.100.7", "")
				if err != nil {
					t.Fatalf("Authenticate: %v", err)
				}
				elsewhere, err := svc.Authenticate(ctx, "K9MLS", goodPassword, "203.0.113.5", "")
				if err != nil {
					t.Fatalf("Authenticate: %v", err)
				}
				colleague, err := svc.Authenticate(ctx, "KD9EJA", goodPassword, "203.0.113.6", "")
				if err != nil {
					t.Fatalf("Authenticate: %v", err)
				}

				if err := tc.act(ctx, svc, mine.Token); err != nil {
					t.Fatalf("replacing the password: %v", err)
				}

				if _, err := svc.Session(ctx, elsewhere.Token); !errors.Is(err, auth.ErrNoSession) {
					t.Errorf("the account's other session still works: %v", err)
				}
				_, err = svc.Session(ctx, mine.Token)
				if tc.keeps && err != nil {
					t.Errorf("the session that did it was ended: %v", err)
				}
				if !tc.keeps && !errors.Is(err, auth.ErrNoSession) {
					t.Errorf("the account's session survived: %v", err)
				}
				// Nobody else's is touched.
				if _, err := svc.Session(ctx, colleague.Token); err != nil {
					t.Errorf("another administrator was signed out: %v", err)
				}
			})
		}
	}
}
