package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/auth"
)

// TestFailedLoginsAreCountedAgainstTheSourceNotTheAccount. Counted on the
// account, five wrong passwords from anybody kept the real administrator out
// for fifteen minutes, and five more renewed it: the lockout was a switch a
// stranger could hold down. Counted on the address, the stranger is the one
// refused. Every case also checks the other half — that a name nobody holds is
// indistinguishable from a wrong password, including once the address is being
// refused.
//
// To see it fail: in Service.Authenticate, replace `source := throttleKey(ip)`
// with `source := fold`, which is the old behaviour of counting by account.
func TestFailedLoginsAreCountedAgainstTheSourceNotTheAccount(t *testing.T) {
	const (
		stranger = "203.0.113.5"
		admin    = "198.51.100.7"
	)
	type step struct {
		username, password, ip string
		// unlock runs `qsp unlock K9MLS` before the attempt.
		unlock bool
		// wait moves the clock on before the attempt.
		wait time.Duration
		want error
	}
	wrong := func(ip string) step {
		return step{username: "K9MLS", password: "wrong", ip: ip, want: auth.ErrInvalidCredentials}
	}
	nobody := func(ip string) step {
		return step{username: "NOBODY", password: "wrong", ip: ip, want: auth.ErrInvalidCredentials}
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "a stranger guessing does not keep the administrator out",
			steps: []step{
				wrong(stranger), wrong(stranger), wrong(stranger),
				{username: "K9MLS", password: "wrong", ip: stranger, want: auth.ErrLockedOut},
				{username: "K9MLS", password: goodPassword, ip: admin},
			},
		},
		{
			name: "the address that guessed is refused even with the right password",
			steps: []step{
				wrong(stranger), wrong(stranger), wrong(stranger),
				{username: "K9MLS", password: goodPassword, ip: stranger, want: auth.ErrLockedOut},
			},
		},
		{
			name: "a name nobody holds is counted and refused exactly as a real one",
			steps: []step{
				nobody(stranger), nobody(stranger), nobody(stranger),
				{username: "NOBODY", password: "wrong", ip: stranger, want: auth.ErrLockedOut},
				// And the refusal does not depend on the name sent next.
				{username: "K9MLS", password: "wrong", ip: stranger, want: auth.ErrLockedOut},
			},
		},
		{
			name: "a refusal earned on a real name answers an unknown one the same",
			steps: []step{
				wrong(stranger), wrong(stranger), wrong(stranger),
				{username: "NOBODY", password: "wrong", ip: stranger, want: auth.ErrLockedOut},
			},
		},
		{
			name: "the administrator signing in does not hand the guesser fresh attempts",
			steps: []step{
				wrong(stranger), wrong(stranger), wrong(stranger),
				{username: "K9MLS", password: goodPassword, ip: admin},
				{username: "K9MLS", password: "wrong", ip: stranger, want: auth.ErrLockedOut},
			},
		},
		{
			name: "the refusal ends by itself",
			steps: []step{
				wrong(stranger), wrong(stranger), wrong(stranger),
				{username: "K9MLS", password: goodPassword, ip: stranger, wait: 11 * time.Minute},
			},
		},
		{
			name: "failures spread wider than the period are not added up",
			steps: []step{
				wrong(admin), wrong(admin),
				{username: "K9MLS", password: "wrong", ip: admin, wait: 11 * time.Minute, want: auth.ErrInvalidCredentials},
				{username: "K9MLS", password: goodPassword, ip: admin},
			},
		},
		{
			name: "qsp unlock lets an administrator who mistyped back in at once",
			steps: []step{
				wrong(admin), wrong(admin), wrong(admin),
				{username: "K9MLS", password: goodPassword, ip: admin, want: auth.ErrLockedOut},
				{username: "K9MLS", password: goodPassword, ip: admin, unlock: true},
			},
		},
		{
			name: "qsp unlock does nothing for an address that only guessed at names",
			steps: []step{
				nobody(stranger), nobody(stranger), nobody(stranger),
				{username: "K9MLS", password: goodPassword, ip: stranger, unlock: true, want: auth.ErrLockedOut},
			},
		},
		{
			// The bypass found 2026-10-07: two guesses at a real name, the
			// third at a name nobody holds, and the address went on guessing
			// at the real name for ever. To see it fail: add
			// `st.marked = 1` to sourceThrottle.fail, which is the old
			// behaviour of remembering an account nothing was written on.
			name: "tripping on a name nobody holds does not forgive the guesses before it",
			steps: []step{
				wrong(stranger), wrong(stranger), nobody(stranger),
				{username: "K9MLS", password: "wrong", ip: stranger, want: auth.ErrLockedOut},
				{username: "K9MLS", password: goodPassword, ip: stranger, want: auth.ErrLockedOut},
				{username: "K9MLS", password: goodPassword, ip: stranger, wait: 9 * time.Minute, want: auth.ErrLockedOut},
			},
		},
		{
			name: "a name nobody holds between real guesses does not hide them",
			steps: []step{
				wrong(stranger), nobody(stranger), wrong(stranger),
				{username: "K9MLS", password: goodPassword, ip: stranger, want: auth.ErrLockedOut},
				{username: "K9MLS", password: goodPassword, ip: stranger, unlock: true},
			},
		},
		{
			name: "one IPv6 /64 is one source however many addresses it uses",
			steps: []step{
				wrong("2001:db8:1:2::1"), wrong("2001:db8:1:2::2"), wrong("2001:db8:1:2:ffff::3"),
				{username: "K9MLS", password: goodPassword, ip: "2001:db8:1:2::4", want: auth.ErrLockedOut},
				// The /64 next door is somebody else.
				{username: "K9MLS", password: goodPassword, ip: "2001:db8:1:3::1"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := newRepo()
			svc, clk := newService(t, repo, auth.Policy{MaxFailures: 3, Lockout: 10 * time.Minute})
			ctx := context.Background()
			if _, err := svc.CreateAccount(ctx, "K9MLS", goodPassword); err != nil {
				t.Fatalf("CreateAccount: %v", err)
			}

			for i, st := range tc.steps {
				clk.advance(st.wait)
				if st.unlock {
					if _, err := svc.Unlock(ctx, "K9MLS"); err != nil {
						t.Fatalf("Unlock: %v", err)
					}
				}
				_, err := svc.Authenticate(ctx, st.username, st.password, st.ip, "")
				if !errors.Is(err, st.want) {
					t.Fatalf("step %d (%s from %s) returned %v, want %v",
						i+1, st.username, st.ip, err, st.want)
				}
			}
		})
	}
}

// TestARefusalSaysWhenItEnds. "Try again later" leaves somebody who mistyped
// their own passphrase guessing at how much later; the console turns this into
// minutes.
//
// To see it fail: in Authenticate, return `&Lockout{Until: until.Add(time.Minute)}`.
func TestARefusalSaysWhenItEnds(t *testing.T) {
	repo := newRepo()
	svc, clk := newService(t, repo, auth.Policy{MaxFailures: 1, Lockout: 10 * time.Minute})
	ctx := context.Background()

	tripped := clk.now()
	_, _ = svc.Authenticate(ctx, "NOBODY", "wrong", "203.0.113.5", "")
	clk.advance(time.Minute)
	_, err := svc.Authenticate(ctx, "NOBODY", "wrong", "203.0.113.5", "")

	var lockout *auth.Lockout
	if !errors.As(err, &lockout) {
		t.Fatalf("the refusal is %v, which carries no time", err)
	}
	if want := tripped.Add(10 * time.Minute); !lockout.Until.Equal(want) {
		t.Errorf("the refusal ends at %s, want %s", lockout.Until, want)
	}
}
