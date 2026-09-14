package config

import (
	"strings"
	"testing"
	"time"
)

// TestSessionLifetimeIsConfigurable is the setting an operator asked for on
// 2026-09-14, after being logged out every morning.
//
// Twelve hours is the right default and it happens to span a night: log in one
// evening, come back the next day, expired. Diagnosed on this project's own
// production server — one session row, expiring exactly twelve hours after the
// last login — and the number had never been reachable from a configuration
// file. `auth.Policy{}` was constructed empty in all three places that built
// it.
func TestSessionLifetimeIsConfigurable(t *testing.T) {
	c := Default()
	if got := time.Duration(c.Server.SessionLifetime); got != 12*time.Hour {
		t.Errorf("the default session lifetime is %v, want 12h; changing it "+
			"changes every server that does not set one", got)
	}

	c.Server.SessionLifetime = Duration(24 * time.Hour)
	if err := c.Validate(); err != nil {
		t.Errorf("24h was refused: %v; that is the value the operator asked for", err)
	}
}

// TestZeroSessionLifetimeIsTheDefaultRatherThanAnError keeps an existing
// configuration working.
//
// Every qsp.json in the field predates this field, so it is absent and
// unmarshals to zero. Zero has to mean "the default" — auth.Policy already
// treats it that way — and not "sessions expire immediately", which is what a
// naive validation would produce.
func TestZeroSessionLifetimeIsTheDefaultRatherThanAnError(t *testing.T) {
	c := Default()
	c.Server.SessionLifetime = 0
	if err := c.Validate(); err != nil {
		t.Errorf("a configuration written before this field existed was "+
			"refused: %v", err)
	}
}

// TestAnAbsurdSessionLifetimeIsRefused bounds it at both ends.
//
// Not for symmetry: this console can add administrators, change access lists
// and restart the server. An operator who wants longer than a week wants no
// login at all and should have to say so.
func TestAnAbsurdSessionLifetimeIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		d    time.Duration
		want string
	}{
		{"a second", time.Second, "shorter than a minute"},
		{"a year", 365 * 24 * time.Hour, "longer than a week"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Server.SessionLifetime = Duration(tc.d)
			err := c.Validate()
			if err == nil {
				t.Fatalf("%v was accepted", tc.d)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the error does not say %q, so an operator cannot "+
					"tell what is wrong: %v", tc.want, err)
			}
		})
	}
}
