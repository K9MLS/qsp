package peers

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Login throttling. A member with a wrong password retries every ten seconds
// for as long as their hotspot is switched on, which is indistinguishable from
// somebody guessing — and QSP answered every one of them.

func addr(s string) netip.AddrPort { return netip.MustParseAddrPort(s) }

func TestASourceIsLockedOutAfterRepeatedFailures(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(3, time.Minute)
	from := addr("203.0.113.5:41000")

	for i := 0; i < 2; i++ {
		locked, _ := th.fail(from, ReasonWrongPassword, now)
		if locked {
			t.Fatalf("locked out after %d failures, want 3", i+1)
		}
		now = now.Add(10 * time.Second)
	}

	locked, summary := th.fail(from, ReasonWrongPassword, now)
	if !locked {
		t.Fatal("three failures did not lock the source out")
	}
	if summary == "" {
		t.Error("the lockout was not reported")
	}
	if !th.locked(from, now) {
		t.Error("the source is not being ignored")
	}
}

// TestTheLockoutIsReportedOnce. A hotspot retrying every ten seconds produces
// forty log lines saying the same thing, which is how an operator learns to
// skim past the one that matters.
func TestTheLockoutIsReportedOnce(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(2, time.Minute)
	from := addr("203.0.113.5:41000")

	th.fail(from, ReasonWrongPassword, now)
	_, first := th.fail(from, ReasonWrongPassword, now)
	if first == "" {
		t.Fatal("the lockout was not reported at all")
	}

	for i := 0; i < 20; i++ {
		now = now.Add(10 * time.Second)
		if _, summary := th.fail(from, ReasonWrongPassword, now); summary != "" {
			t.Fatalf("attempt %d was reported again: %s", i, summary)
		}
	}
}

// TestTheSummaryNamesTheReason. "Authentication failed" covered three
// different problems, and each needs something different done about it.
func TestTheSummaryNamesTheReason(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(2, time.Minute)
	from := addr("203.0.113.5:41000")

	th.fail(from, ReasonWrongPassword, now)
	_, summary := th.fail(from, ReasonWrongPassword, now)

	if !strings.Contains(summary, string(ReasonWrongPassword)) {
		t.Errorf("the summary does not say what failed: %q", summary)
	}
	if !strings.Contains(summary, "203.0.113.5") {
		t.Errorf("the summary does not name the source: %q", summary)
	}
}

// TestThrottlingIsPerAddress. A repeater ID is whatever the caller claims, and
// a determined guesser would vary it; the address is the one thing they cannot
// choose freely.
func TestThrottlingIsPerAddress(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(2, time.Minute)

	th.fail(addr("203.0.113.5:41000"), ReasonWrongPassword, now)
	th.fail(addr("203.0.113.5:52000"), ReasonWrongPassword, now)

	// Same address, different ports: the same source.
	if !th.locked(addr("203.0.113.5:9999"), now) {
		t.Error("failures from one address across different ports were counted separately")
	}
	// A different address is unaffected.
	if th.locked(addr("198.51.100.7:41000"), now) {
		t.Error("one source's failures locked out another")
	}
}

// TestASuccessClearsTheHistory. A member who fixes their password must not be
// held to the attempts before they did.
func TestASuccessClearsTheHistory(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(3, time.Minute)
	from := addr("203.0.113.5:41000")

	th.fail(from, ReasonWrongPassword, now)
	th.fail(from, ReasonWrongPassword, now)
	th.succeed(from)

	// Two more must not lock them out; the count started again.
	th.fail(from, ReasonWrongPassword, now)
	th.fail(from, ReasonWrongPassword, now)
	if th.locked(from, now) {
		t.Error("failures survived a successful login")
	}
}

// TestTheLockoutLifts. A hotspot that is fixed while locked out should connect
// on its next retry rather than needing a restart of anything.
func TestTheLockoutLifts(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(2, time.Minute)
	from := addr("203.0.113.5:41000")

	th.fail(from, ReasonWrongPassword, now)
	th.fail(from, ReasonWrongPassword, now)
	if !th.locked(from, now) {
		t.Fatal("not locked out")
	}

	if th.locked(from, now.Add(2*time.Minute)) {
		t.Error("the lockout did not lift")
	}
}

// TestAnOldRunStartsAgain. A hotspot that fails once a day for a week is
// somebody who fixed it and broke it again, not somebody guessing.
func TestAnOldRunStartsAgain(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(3, time.Minute)
	from := addr("203.0.113.5:41000")

	th.fail(from, ReasonWrongPassword, now)
	th.fail(from, ReasonWrongPassword, now)

	// A day later.
	later := now.Add(24 * time.Hour)
	th.fail(from, ReasonWrongPassword, later)
	if th.locked(from, later) {
		t.Error("failures from a day ago counted towards a lockout today")
	}
}

// TestExpireForgetsStaleSources, or the map grows with every address that ever
// mistyped a password.
func TestExpireForgetsStaleSources(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(3, time.Minute)

	th.fail(addr("203.0.113.5:41000"), ReasonWrongPassword, now)
	if len(th.attempts) != 1 {
		t.Fatalf("%d sources tracked", len(th.attempts))
	}

	th.expire(now.Add(time.Hour))
	if len(th.attempts) != 0 {
		t.Errorf("%d stale sources survived", len(th.attempts))
	}
}

// TestALockedSourceIsStillListed. An operator needs to see what is being
// refused, which is the whole reason this is reported at all.
func TestALockedSourceIsStillListed(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(2, time.Minute)
	from := addr("203.0.113.5:41000")

	th.fail(from, ReasonWrongPassword, now)
	th.fail(from, ReasonUnknownID, now)

	claimed := map[netip.Addr]hbp.RepeaterID{from.Addr(): 3155413}
	list := th.recentFailures(now, claimed)

	if len(list) != 1 {
		t.Fatalf("%d entries, want 1", len(list))
	}
	f := list[0]
	if f.Address != "203.0.113.5" {
		t.Errorf("address is %q", f.Address)
	}
	if f.RepeaterID != 3155413 {
		t.Errorf("repeater ID is %d, want the one that was claimed", f.RepeaterID)
	}
	if f.Failures != 2 {
		t.Errorf("failures is %d", f.Failures)
	}
	if f.LockedUntil.IsZero() {
		t.Error("a locked source does not report when it will be listened to again")
	}
	// Both reasons, because an operator seeing only one would fix half of it.
	if !strings.Contains(f.Reason, "wrong password") ||
		!strings.Contains(f.Reason, "no password configured") {
		t.Errorf("the reason does not cover both failures: %q", f.Reason)
	}
}

func TestBlockedCountsOnlyLockedSources(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	th := newThrottle(2, time.Minute)

	th.fail(addr("203.0.113.5:1"), ReasonWrongPassword, now) // one failure
	th.fail(addr("198.51.100.7:1"), ReasonWrongPassword, now)
	th.fail(addr("198.51.100.7:1"), ReasonWrongPassword, now) // locked

	if got := th.blocked(now); got != 1 {
		t.Errorf("blocked = %d, want 1", got)
	}
}
