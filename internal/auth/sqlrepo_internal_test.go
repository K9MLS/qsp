package auth

import (
	"testing"
	"time"
)

// The stored time format, tested from inside the package rather than through an
// exported hook: a function existing only so a test can reach it is a worse
// trade than a second test file.

// TestStoredTimesSortChronologically is what the expiry sweep depends on. The
// query compares text, so a format where "2026-9-30" sorts after "2026-10-01"
// would leave expired sessions alive and unexpired ones deleted — quietly, and
// only around a month boundary.
func TestStoredTimesSortChronologically(t *testing.T) {
	pairs := [][2]string{
		{"2026-09-30T23:59:59Z", "2026-10-01T00:00:01Z"},
		{"2026-01-09T00:00:00Z", "2026-01-10T00:00:00Z"},
		{"2026-12-31T23:59:59Z", "2027-01-01T00:00:00Z"},
		{"2026-08-28T09:00:00Z", "2026-08-28T10:00:00Z"},
	}
	for _, p := range pairs {
		earlier := storeTime(mustTime(t, p[0]))
		later := storeTime(mustTime(t, p[1]))
		if earlier >= later {
			t.Errorf("%q does not sort before %q", earlier, later)
		}
	}
}

// TestTheZeroTimeIsStoredAsEmpty. Never-locked and never-logged-in are the
// common cases, and a timestamp in year one would read as a date.
func TestTheZeroTimeIsStoredAsEmpty(t *testing.T) {
	if got := storeTime(time.Time{}); got != "" {
		t.Errorf("the zero time stored as %q", got)
	}
	if got := readTime(""); !got.IsZero() {
		t.Errorf("an empty value read as %v", got)
	}
}

func TestTimesRoundTrip(t *testing.T) {
	want := mustTime(t, "2026-08-28T17:31:30.673444326Z")
	got := readTime(storeTime(want))
	if !got.Equal(want) {
		t.Errorf("round trip gave %v, want %v", got, want)
	}
}

// TestLocalTimesAreStoredAsUTC. Two instances in different zones must produce
// values that compare correctly, and the sweep compares text.
func TestLocalTimesAreStoredAsUTC(t *testing.T) {
	zone := time.FixedZone("CDT", -5*60*60)
	local := time.Date(2026, 8, 28, 12, 0, 0, 0, zone)

	stored := storeTime(local)
	if stored[len(stored)-1] != 'Z' {
		t.Errorf("a local time stored as %q rather than UTC", stored)
	}
	if !readTime(stored).Equal(local) {
		t.Error("converting to UTC changed the instant")
	}
}

// TestUnparseableTimesReadAsNever. For a lockout that means not locked and for
// a last login it means never used — both of which fail towards letting the
// operator in rather than locking them out of their own instance over a
// corrupt row.
func TestUnparseableTimesReadAsNever(t *testing.T) {
	for _, bad := range []string{"yesterday", "0", "2026-13-45T99:99:99Z", "   "} {
		if got := readTime(bad); !got.IsZero() {
			t.Errorf("%q read as %v", bad, got)
		}
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parsing %q: %v", s, err)
	}
	return v.UTC()
}
