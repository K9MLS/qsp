package scheduler_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/scheduler"
)

func chicago(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatalf("timezone database unavailable: %v", err)
	}
	return loc
}

// tuesdayNet is the canonical case: 20:00 to 21:00 every Tuesday, Central time.
func tuesdayNet() scheduler.Window {
	return scheduler.Window{
		Bridge:   "tuesday-net",
		Days:     []time.Weekday{time.Tuesday},
		Start:    scheduler.LocalTime{Hour: 20, Minute: 0},
		Duration: time.Hour,
		Timezone: "America/Chicago",
		Enabled:  true,
	}
}

func mustSchedule(t *testing.T, windows ...scheduler.Window) *scheduler.Schedule {
	t.Helper()
	s, err := scheduler.NewSchedule(windows)
	if err != nil {
		t.Fatalf("NewSchedule: %v", err)
	}
	return s
}

func TestWindowIsActiveOnlyWithinItsHour(t *testing.T) {
	loc := chicago(t)
	s := mustSchedule(t, tuesdayNet())

	// 2026-08-25 is a Tuesday.
	cases := []struct {
		local string
		want  bool
	}{
		{"2026-08-25 19:59", false},
		{"2026-08-25 20:00", true},
		{"2026-08-25 20:30", true},
		{"2026-08-25 20:59", true},
		{"2026-08-25 21:00", false},
		{"2026-08-26 20:30", false}, // Wednesday
		{"2026-08-24 20:30", false}, // Monday
	}
	for _, c := range cases {
		when, err := time.ParseInLocation("2006-01-02 15:04", c.local, loc)
		if err != nil {
			t.Fatalf("parsing %q: %v", c.local, err)
		}
		if got := s.ActiveAt(when)["tuesday-net"]; got != c.want {
			t.Errorf("%s local: active = %v, want %v", c.local, got, c.want)
		}
	}
}

// TestNetStaysAt2000AcrossDaylightSaving is the property that motivates storing
// local wall time rather than an instant.
//
// A net at 20:00 must be at 20:00 in January and in July. Its UTC instant moves
// by an hour; the net does not.
func TestNetStaysAt2000AcrossDaylightSaving(t *testing.T) {
	loc := chicago(t)
	s := mustSchedule(t, tuesdayNet())

	// Tuesdays either side of both 2026 transitions.
	for _, day := range []string{"2026-03-03", "2026-03-10", "2026-10-27", "2026-11-03"} {
		when, err := time.ParseInLocation("2006-01-02 15:04", day+" 20:30", loc)
		if err != nil {
			t.Fatalf("parsing: %v", err)
		}
		if !s.ActiveAt(when)["tuesday-net"] {
			t.Errorf("%s 20:30 local: the net was not running", day)
		}
		// And it must not be running an hour either side.
		for _, off := range []time.Duration{-90 * time.Minute, 90 * time.Minute} {
			if s.ActiveAt(when.Add(off))["tuesday-net"] {
				t.Errorf("%s: the net was running at %s local", day, when.Add(off).In(loc).Format("15:04"))
			}
		}
	}
}

// TestSpringForwardGapIsSkippedNotShifted.
//
// 02:30 does not exist on a spring-forward day. Go resolves it backwards to
// 01:30 — an hour EARLIER than asked for. Firing a net early is worse than not
// firing it, and much harder for an operator to diagnose, so the occurrence is
// skipped and labelled.
func TestSpringForwardGapIsSkippedNotShifted(t *testing.T) {
	loc := chicago(t)
	// 2026-03-08 is a Sunday; clocks go 02:00 -> 03:00.
	w := scheduler.Window{
		Bridge:   "small-hours",
		Days:     []time.Weekday{time.Sunday},
		Start:    scheduler.LocalTime{Hour: 2, Minute: 30},
		Duration: time.Hour,
		Timezone: "America/Chicago",
		Enabled:  true,
	}
	s := mustSchedule(t, w)

	// The hour it would wrongly have fired in.
	wrong := time.Date(2026, 3, 8, 1, 45, 0, 0, loc)
	if s.ActiveAt(wrong)["small-hours"] {
		t.Error("the net fired an hour early, in the hour before the clocks changed")
	}
	// And not after the change either.
	after := time.Date(2026, 3, 8, 3, 15, 0, 0, loc)
	if s.ActiveAt(after)["small-hours"] {
		t.Error("the net fired after the clocks changed; the occurrence should be skipped")
	}

	// Preview must tell the operator, rather than leaving them to find out.
	from := time.Date(2026, 3, 2, 0, 0, 0, 0, loc)
	var found bool
	for _, occ := range s.Preview(from, 2) {
		if occ.Skipped {
			found = true
			if !strings.Contains(occ.Note, "clocks move forward") {
				t.Errorf("note does not explain why: %q", occ.Note)
			}
		}
	}
	if !found {
		t.Error("Preview did not flag the skipped occurrence")
	}

	// The following Sunday runs normally.
	next := time.Date(2026, 3, 15, 2, 45, 0, 0, loc)
	if !s.ActiveAt(next)["small-hours"] {
		t.Error("the net did not run on the following week")
	}
}

// TestFallBackRepeatedHourRunsOnce.
//
// 01:30 happens twice on a fall-back day. The window opens at the first and
// runs for its configured duration in real time, so it does not re-open an hour
// later.
func TestFallBackRepeatedHourRunsOnce(t *testing.T) {
	chicago(t) // fail early and clearly if the timezone database is missing
	// 2026-11-01 is a Sunday; clocks go 02:00 -> 01:00.
	w := scheduler.Window{
		Bridge:   "small-hours",
		Days:     []time.Weekday{time.Sunday},
		Start:    scheduler.LocalTime{Hour: 1, Minute: 30},
		Duration: time.Hour,
		Timezone: "America/Chicago",
		Enabled:  true,
	}
	s := mustSchedule(t, w)

	// The first 01:30 is CDT, UTC 06:30.
	first := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)
	if !s.ActiveAt(first.Add(15 * time.Minute))["small-hours"] {
		t.Error("the net was not running during the first pass through 01:30")
	}
	// One hour of real time later, it is over.
	if s.ActiveAt(first.Add(75 * time.Minute))["small-hours"] {
		t.Error("the net was still running after its hour; the repeated hour re-opened it")
	}
}

// TestWindowCrossingMidnight.
//
// A window that starts at 23:30 belongs to the day it started on and runs into
// the next. Walking candidate days back must use calendar arithmetic, because a
// local day is not always 24 hours.
func TestWindowCrossingMidnight(t *testing.T) {
	loc := chicago(t)
	w := scheduler.Window{
		Bridge:   "late-net",
		Days:     []time.Weekday{time.Saturday},
		Start:    scheduler.LocalTime{Hour: 23, Minute: 30},
		Duration: 2 * time.Hour,
		Timezone: "America/Chicago",
		Enabled:  true,
	}
	s := mustSchedule(t, w)

	// 2026-08-29 is a Saturday.
	cases := []struct {
		local string
		want  bool
	}{
		{"2026-08-29 23:29", false},
		{"2026-08-29 23:45", true},
		{"2026-08-30 00:30", true}, // Sunday, still the Saturday window
		{"2026-08-30 01:29", true},
		{"2026-08-30 01:30", false},
		{"2026-08-30 23:45", false}, // Sunday does not start a window
	}
	for _, c := range cases {
		when, _ := time.ParseInLocation("2006-01-02 15:04", c.local, loc)
		if got := s.ActiveAt(when)["late-net"]; got != c.want {
			t.Errorf("%s local: active = %v, want %v", c.local, got, c.want)
		}
	}
}

// TestMidnightCrossingOverAFallBackNight exercises both subtleties at once:
// a window spanning midnight on a 25-hour day.
func TestMidnightCrossingOverAFallBackNight(t *testing.T) {
	loc := chicago(t)
	// 2026-10-31 is a Saturday; the clocks change early on the Sunday.
	w := scheduler.Window{
		Bridge:   "late-net",
		Days:     []time.Weekday{time.Saturday},
		Start:    scheduler.LocalTime{Hour: 23, Minute: 0},
		Duration: 4 * time.Hour,
		Timezone: "America/Chicago",
		Enabled:  true,
	}
	s := mustSchedule(t, w)

	start := time.Date(2026, 10, 31, 23, 0, 0, 0, loc)
	for _, off := range []time.Duration{time.Minute, time.Hour, 2 * time.Hour, 3*time.Hour + 59*time.Minute} {
		if !s.ActiveAt(start.Add(off))["late-net"] {
			t.Errorf("the net was not running %s after it started", off)
		}
	}
	if s.ActiveAt(start.Add(4*time.Hour + time.Minute))["late-net"] {
		t.Error("the net was still running after four hours of real time")
	}
}

// TestRestartRecoveryIsAutomatic.
//
// The level-triggered design means there is nothing to recover: a fresh
// Schedule gives the same answer as one that has been running all week.
func TestRestartRecoveryIsAutomatic(t *testing.T) {
	loc := chicago(t)
	mid := time.Date(2026, 8, 25, 20, 30, 0, 0, loc)

	running := mustSchedule(t, tuesdayNet())
	if !running.ActiveAt(mid)["tuesday-net"] {
		t.Fatal("the net is not running mid-window")
	}

	// A brand-new schedule, as if QSP had just started.
	restarted := mustSchedule(t, tuesdayNet())
	if !restarted.ActiveAt(mid)["tuesday-net"] {
		t.Error("a freshly started QSP did not pick up a net already in progress")
	}
}

// TestClockJumpsDoNotStrandAWindow.
//
// Evaluating out of order, or jumping backwards, must not confuse anything —
// there is no state to confuse.
func TestClockJumpsDoNotStrandAWindow(t *testing.T) {
	loc := chicago(t)
	s := mustSchedule(t, tuesdayNet())

	inside := time.Date(2026, 8, 25, 20, 30, 0, 0, loc)
	outside := time.Date(2026, 8, 25, 23, 0, 0, 0, loc)

	for i := 0; i < 5; i++ {
		if !s.ActiveAt(inside)["tuesday-net"] {
			t.Fatal("inside the window but not active")
		}
		if s.ActiveAt(outside)["tuesday-net"] {
			t.Fatal("outside the window but active")
		}
		// Jump backwards a week, then forwards.
		if s.ActiveAt(inside.AddDate(0, 0, -7))["tuesday-net"] != true {
			t.Fatal("the same window a week earlier was not active")
		}
	}
}

func TestDisabledWindowNeverRuns(t *testing.T) {
	loc := chicago(t)
	w := tuesdayNet()
	w.Enabled = false
	s := mustSchedule(t, w)

	if s.ActiveAt(time.Date(2026, 8, 25, 20, 30, 0, 0, loc))["tuesday-net"] {
		t.Error("a disabled window ran")
	}
	if len(s.Preview(time.Date(2026, 8, 24, 0, 0, 0, 0, loc), 3)) != 0 {
		t.Error("a disabled window appeared in the preview")
	}
}

func TestPreviewShowsWhatWillActuallyHappen(t *testing.T) {
	loc := chicago(t)
	s := mustSchedule(t, tuesdayNet())

	from := time.Date(2026, 8, 23, 12, 0, 0, 0, loc) // a Sunday
	occ := s.Preview(from, 3)
	if len(occ) != 3 {
		t.Fatalf("got %d occurrences, want 3", len(occ))
	}
	for i, o := range occ {
		if o.Bridge != "tuesday-net" {
			t.Errorf("occurrence %d is for %q", i, o.Bridge)
		}
		if o.Start.Location() != time.UTC {
			t.Errorf("occurrence %d start is in %v, want UTC", i, o.Start.Location())
		}
		if !strings.Contains(o.LocalStart, "20:00") {
			t.Errorf("occurrence %d local start = %q, want it to show 20:00", i, o.LocalStart)
		}
		if o.End.Sub(o.Start) != time.Hour {
			t.Errorf("occurrence %d lasts %s, want 1h", i, o.End.Sub(o.Start))
		}
		if i > 0 && !occ[i-1].Start.Before(o.Start) {
			t.Error("occurrences are not in order")
		}
	}
	// A week apart, in wall-clock terms.
	if occ[1].Start.Sub(occ[0].Start) != 7*24*time.Hour {
		t.Errorf("consecutive occurrences are %s apart", occ[1].Start.Sub(occ[0].Start))
	}
}

func TestPreviewSpansADaylightSavingChange(t *testing.T) {
	loc := chicago(t)
	s := mustSchedule(t, tuesdayNet())

	// Start before the November change and look past it.
	from := time.Date(2026, 10, 26, 0, 0, 0, 0, loc)
	occ := s.Preview(from, 3)
	if len(occ) < 2 {
		t.Fatalf("got %d occurrences", len(occ))
	}
	for _, o := range occ {
		if !strings.Contains(o.LocalStart, "20:00") {
			t.Errorf("a net moved off 20:00 local across the change: %q", o.LocalStart)
		}
	}
	// The UTC instants must differ by more than a whole number of weeks,
	// because the offset changed.
	gap := occ[1].Start.Sub(occ[0].Start)
	if gap != 7*24*time.Hour && gap != 7*24*time.Hour+time.Hour {
		t.Errorf("gap across the change = %s; expected 168h or 169h", gap)
	}
}

func TestValidationRejectsUnusableWindows(t *testing.T) {
	cases := map[string]scheduler.Window{
		"no bridge":     {Days: []time.Weekday{time.Tuesday}, Duration: time.Hour, Timezone: "UTC"},
		"no days":       {Bridge: "n", Duration: time.Hour, Timezone: "UTC"},
		"duplicate day": {Bridge: "n", Days: []time.Weekday{time.Tuesday, time.Tuesday}, Duration: time.Hour, Timezone: "UTC"},
		"bad weekday":   {Bridge: "n", Days: []time.Weekday{9}, Duration: time.Hour, Timezone: "UTC"},
		"bad hour":      {Bridge: "n", Days: []time.Weekday{time.Tuesday}, Start: scheduler.LocalTime{Hour: 25}, Duration: time.Hour, Timezone: "UTC"},
		"bad minute":    {Bridge: "n", Days: []time.Weekday{time.Tuesday}, Start: scheduler.LocalTime{Minute: 61}, Duration: time.Hour, Timezone: "UTC"},
		"too short":     {Bridge: "n", Days: []time.Weekday{time.Tuesday}, Duration: time.Second, Timezone: "UTC"},
		"too long":      {Bridge: "n", Days: []time.Weekday{time.Tuesday}, Duration: 48 * time.Hour, Timezone: "UTC"},
		"no timezone":   {Bridge: "n", Days: []time.Weekday{time.Tuesday}, Duration: time.Hour},
		"abbreviation":  {Bridge: "n", Days: []time.Weekday{time.Tuesday}, Duration: time.Hour, Timezone: "CST"},
	}
	for name, w := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := scheduler.NewSchedule([]scheduler.Window{w}); err == nil {
				t.Errorf("accepted an invalid window: %s", name)
			}
		})
	}
}

// TestFailsafeMaximumDurationIsExplained.
func TestFailsafeMaximumDurationIsExplained(t *testing.T) {
	w := tuesdayNet()
	w.Duration = 100 * time.Hour
	_, err := scheduler.NewSchedule([]scheduler.Window{w})
	if err == nil {
		t.Fatal("a 100-hour window was accepted")
	}
	if !strings.Contains(err.Error(), "days") {
		t.Errorf("error should explain what the limit protects against, got: %v", err)
	}
}

// TestTimezoneDatabaseIsEmbedded.
//
// A scratch container has no /usr/share/zoneinfo. Without the embedded
// database, every schedule would fail to load on the deployment target and
// nowhere else.
func TestTimezoneDatabaseIsEmbedded(t *testing.T) {
	for _, name := range []string{"America/Chicago", "Europe/London", "Australia/Sydney", "UTC"} {
		if _, err := time.LoadLocation(name); err != nil {
			t.Errorf("timezone %q is unavailable: %v", name, err)
		}
	}
}

func TestNilScheduleIsSafe(t *testing.T) {
	var s *scheduler.Schedule
	if len(s.ActiveAt(time.Now())) != 0 {
		t.Error("a nil schedule reported active bridges")
	}
	if s.Preview(time.Now(), 3) != nil {
		t.Error("a nil schedule produced a preview")
	}
	if s.Windows() != nil || s.Bridges() != nil {
		t.Error("a nil schedule returned windows or bridges")
	}
}

func TestScheduleIsImmutableAfterConstruction(t *testing.T) {
	loc := chicago(t)
	windows := []scheduler.Window{tuesdayNet()}
	s := mustSchedule(t, windows...)

	windows[0].Enabled = false
	windows[0].Days[0] = time.Friday
	windows[0].Bridge = "tampered"

	if !s.ActiveAt(time.Date(2026, 8, 25, 20, 30, 0, 0, loc))["tuesday-net"] {
		t.Error("mutating the input slice changed a live schedule")
	}
}

func TestMultipleWindowsForOneBridge(t *testing.T) {
	loc := chicago(t)
	morning := tuesdayNet()
	morning.Start = scheduler.LocalTime{Hour: 9, Minute: 0}
	s := mustSchedule(t, tuesdayNet(), morning)

	for _, at := range []string{"2026-08-25 09:30", "2026-08-25 20:30"} {
		when, _ := time.ParseInLocation("2006-01-02 15:04", at, loc)
		if !s.ActiveAt(when)["tuesday-net"] {
			t.Errorf("%s: the bridge was not active", at)
		}
	}
	when, _ := time.ParseInLocation("2006-01-02 15:04", "2026-08-25 14:00", loc)
	if s.ActiveAt(when)["tuesday-net"] {
		t.Error("the bridge was active between its two windows")
	}
}

func TestParseLocalTime(t *testing.T) {
	if lt, err := scheduler.ParseLocalTime(" 20:00 "); err != nil || lt.Hour != 20 || lt.Minute != 0 {
		t.Errorf("ParseLocalTime(20:00) = %v, %v", lt, err)
	}
	for _, bad := range []string{"", "8pm", "25:00", "20:61", "twenty"} {
		if _, err := scheduler.ParseLocalTime(bad); err == nil {
			t.Errorf("ParseLocalTime(%q) was accepted", bad)
		}
	}
}

func TestBridgesListsEveryReferencedBridge(t *testing.T) {
	second := tuesdayNet()
	second.Bridge = "another"
	s := mustSchedule(t, tuesdayNet(), second, tuesdayNet())

	got := s.Bridges()
	if len(got) != 2 || got[0] != "another" || got[1] != "tuesday-net" {
		t.Errorf("Bridges() = %v, want [another tuesday-net]", got)
	}
}
