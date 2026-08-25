// Package scheduler decides which bridges should be carrying traffic right now.
//
// This is the feature QSP exists for: linking a talkgroup for a net every
// Tuesday at 20:00, automatically, without anybody remembering to do it.
//
// # Level-triggered, not edge-triggered
//
// The obvious design fires events at boundaries: "at 20:00, enable the bridge;
// at 21:00, disable it." That design has to answer a long list of awkward
// questions. What if QSP was restarted at 20:30? What if the process was paused
// and the 20:00 tick never happened? What if the clock jumped?
//
// So the scheduler answers a different question. Given an instant, which
// bridges *should* be enabled? Nothing is remembered between evaluations, and
// the answer for 20:30 is the same whether QSP has been running for a week or
// started four seconds ago.
//
// Restart recovery, missed ticks, clock steps and NTP corrections all stop being
// special cases. See docs/adr/ADR-0015.
//
// # Time is stored as local wall time, not as an instant
//
// A net at "Tuesday 20:00 in America/Chicago" must happen at 20:00 whether or
// not daylight saving is in effect. Its UTC instant is 02:00 in winter and
// 01:00 in summer, so storing the instant would drag the net an hour off twice
// a year.
//
// Windows therefore store a weekday, a local time and an IANA zone, and resolve
// to an instant at evaluation. Constitution §17: a local timestamp does not
// uniquely identify an instant, and a day is not always 24 hours long.
package scheduler

import (
	"fmt"
	"sort"
	"strings"
	"time"

	// Embeds the IANA timezone database. A scratch container has no
	// /usr/share/zoneinfo, so without this every LoadLocation would fail at
	// runtime and every schedule would refuse to load — on the deployment
	// target, and nowhere else. It costs about 450 KB.
	_ "time/tzdata"
)

// MaxWindowDuration is the failsafe ceiling on a single window.
//
// Constitution §17 requires a maximum duration. The failure it guards against
// is a schedule with a typo — a 100-hour window, or an operator who meant
// minutes and typed hours — quietly welding a talkgroup open for days. Twelve
// hours comfortably covers any net and is well short of "forever".
const MaxWindowDuration = 12 * time.Hour

// MinWindowDuration rejects windows too short to be meaningful.
const MinWindowDuration = time.Minute

// LocalTime is a time of day with no date and no zone.
//
// It is deliberately not a time.Time: a time.Time always carries a date and a
// location, and a window's start has neither until it is resolved against a
// particular day.
type LocalTime struct {
	Hour   int
	Minute int
}

// String renders as HH:MM.
func (l LocalTime) String() string { return fmt.Sprintf("%02d:%02d", l.Hour, l.Minute) }

// Validate reports whether the time of day is real.
func (l LocalTime) Validate() error {
	if l.Hour < 0 || l.Hour > 23 {
		return fmt.Errorf("hour is %d; use 0 to 23", l.Hour)
	}
	if l.Minute < 0 || l.Minute > 59 {
		return fmt.Errorf("minute is %d; use 0 to 59", l.Minute)
	}
	return nil
}

// ParseLocalTime reads "HH:MM".
func ParseLocalTime(s string) (LocalTime, error) {
	var l LocalTime
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &l.Hour, &l.Minute); err != nil {
		return LocalTime{}, fmt.Errorf("cannot read %q as a time of day: use 24-hour HH:MM, for example \"20:00\"", s)
	}
	if err := l.Validate(); err != nil {
		return LocalTime{}, err
	}
	return l, nil
}

// Window is a recurring period during which a bridge carries traffic.
type Window struct {
	// Bridge names the bridge this window enables. It must match a configured
	// bridge name.
	Bridge string
	// Days are the weekdays the window starts on, in the window's own zone.
	// A window that runs past midnight still belongs to the day it started.
	Days []time.Weekday
	// Start is the local time of day the window opens.
	Start LocalTime
	// Duration is how long it stays open.
	Duration time.Duration
	// Timezone is an IANA name such as "America/Chicago".
	//
	// Not a fixed offset: an offset cannot express "20:00 local all year", which
	// is the only thing an operator ever means.
	Timezone string
	// Enabled allows a window to be switched off without deleting it, which is
	// what an operator wants for a net that is suspended for a month.
	Enabled bool

	// loc caches the resolved location.
	loc *time.Location
}

// Validate reports whether the window is usable, resolving its timezone.
func (w *Window) Validate() error {
	if strings.TrimSpace(w.Bridge) == "" {
		return fmt.Errorf("a window must name the bridge it enables")
	}
	if len(w.Days) == 0 {
		return fmt.Errorf("window for bridge %q has no days; it would never run", w.Bridge)
	}
	seen := make(map[time.Weekday]bool, len(w.Days))
	for _, d := range w.Days {
		if d < time.Sunday || d > time.Saturday {
			return fmt.Errorf("window for bridge %q has weekday %d; use 0 (Sunday) to 6 (Saturday)", w.Bridge, d)
		}
		if seen[d] {
			return fmt.Errorf("window for bridge %q lists %s twice", w.Bridge, d)
		}
		seen[d] = true
	}
	if err := w.Start.Validate(); err != nil {
		return fmt.Errorf("window for bridge %q: %w", w.Bridge, err)
	}
	if w.Duration < MinWindowDuration {
		return fmt.Errorf("window for bridge %q lasts %s; the minimum is %s",
			w.Bridge, w.Duration, MinWindowDuration)
	}
	if w.Duration > MaxWindowDuration {
		return fmt.Errorf("window for bridge %q lasts %s; the maximum is %s, "+
			"which exists so that a mistyped schedule cannot hold a talkgroup open for days",
			w.Bridge, w.Duration, MaxWindowDuration)
	}
	if strings.TrimSpace(w.Timezone) == "" {
		return fmt.Errorf("window for bridge %q has no timezone; use an IANA name such as \"America/Chicago\"", w.Bridge)
	}
	loc, err := time.LoadLocation(w.Timezone)
	if err != nil {
		return fmt.Errorf("window for bridge %q has unknown timezone %q: use an IANA name such as "+
			"\"America/Chicago\" or \"Europe/London\", not an abbreviation like \"CST\"", w.Bridge, w.Timezone)
	}
	w.loc = loc
	return nil
}

// Location returns the window's resolved timezone.
func (w *Window) Location() *time.Location {
	if w.loc == nil {
		return time.UTC
	}
	return w.loc
}

// Occurrence is one concrete run of a window.
type Occurrence struct {
	// Bridge is the bridge enabled.
	Bridge string
	// Start and End are real instants, in UTC.
	Start time.Time
	End   time.Time
	// LocalStart renders the start in the window's own zone, which is what the
	// operator wrote and what they will check against.
	LocalStart string
	// Skipped reports that this occurrence will not happen.
	Skipped bool
	// Note explains anything unusual: a skipped occurrence, or a duration that
	// differs from the configured one because a DST change fell inside it.
	Note string
}

// Contains reports whether an instant falls inside the occurrence.
func (o Occurrence) Contains(t time.Time) bool {
	return !o.Skipped && !t.Before(o.Start) && t.Before(o.End)
}

// Schedule is the set of configured windows.
//
// It is immutable once built, like routing.Table, so a configuration change
// swaps in a new one rather than mutating a live schedule.
type Schedule struct {
	windows []Window
}

// NewSchedule validates and builds a schedule, reporting every problem it finds.
func NewSchedule(windows []Window) (*Schedule, error) {
	var problems []string
	out := make([]Window, 0, len(windows))

	for i, w := range windows {
		win := w
		win.Days = append([]time.Weekday(nil), w.Days...)
		if err := win.Validate(); err != nil {
			problems = append(problems, fmt.Sprintf("window %d: %v", i, err))
			continue
		}
		out = append(out, win)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid schedule: %s", strings.Join(problems, "; "))
	}
	return &Schedule{windows: out}, nil
}

// ActiveAt reports which bridges the schedule says should be carrying traffic
// at an instant.
//
// This is the whole interface the routing core needs. It is a pure function of
// the schedule and the instant: no stored state, no memory of past evaluations,
// so the answer after a restart is identical to the answer without one.
func (s *Schedule) ActiveAt(t time.Time) map[string]bool {
	active := make(map[string]bool)
	if s == nil {
		return active
	}
	for i := range s.windows {
		w := &s.windows[i]
		if !w.Enabled {
			continue
		}
		for _, occ := range w.occurrencesAround(t) {
			if occ.Contains(t) {
				active[w.Bridge] = true
				break
			}
		}
	}
	return active
}

// occurrencesAround returns the occurrences of a window that could contain t.
//
// A window may start on the previous local day and run past midnight, so
// candidate start days are walked backwards using calendar arithmetic. Walking
// back by subtracting 24 hours would be wrong: a local day is 23 or 25 hours
// long across a DST change.
func (w *Window) occurrencesAround(t time.Time) []Occurrence {
	loc := w.Location()
	local := t.In(loc)

	// One day back covers any window within MaxWindowDuration, plus one more
	// for margin around a 25-hour day.
	const lookback = 2

	var out []Occurrence
	for back := lookback; back >= 0; back-- {
		day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, -back)
		if !w.runsOn(day.Weekday()) {
			continue
		}
		out = append(out, w.occurrenceOn(day))
	}
	return out
}

func (w *Window) runsOn(d time.Weekday) bool {
	for _, day := range w.Days {
		if day == d {
			return true
		}
	}
	return false
}

// occurrenceOn resolves the window on a given local calendar day.
//
// # Spring forward
//
// A local time inside the skipped hour does not exist. Go maps it backwards —
// asking for 02:30 on a spring-forward day yields 01:30, an hour *earlier* than
// requested. Firing a net an hour early is worse than not firing it, and far
// harder for an operator to understand, so the occurrence is skipped and
// labelled instead.
//
// # Fall back
//
// A local time inside the repeated hour happens twice. Go resolves it to the
// first occurrence, which is deterministic and is what an operator expects: the
// net starts the first time the clock reads 20:00. The window then runs for its
// configured duration in real time.
//
// A DST change *inside* a window is not adjusted: a one-hour window is one hour
// of real time, not one hour of wall clock. That is the reading that keeps a
// net the length it was scheduled for, and the note records when it applies.
func (w *Window) occurrenceOn(day time.Time) Occurrence {
	loc := w.Location()
	start := time.Date(day.Year(), day.Month(), day.Day(), w.Start.Hour, w.Start.Minute, 0, 0, loc)

	occ := Occurrence{
		Bridge:     w.Bridge,
		Start:      start.UTC(),
		End:        start.Add(w.Duration).UTC(),
		LocalStart: start.Format("2006-01-02 15:04 MST"),
	}

	// If the resolved wall time is not the one asked for, the local time does
	// not exist on this day.
	if start.Hour() != w.Start.Hour || start.Minute() != w.Start.Minute {
		occ.Skipped = true
		// Show the time the operator wrote, not the time Go resolved it to.
		// Rendering "01:30" for a window configured at "02:30" would send them
		// looking for a bug in the wrong place.
		occ.LocalStart = fmt.Sprintf("%s %s (does not exist)",
			start.Format("2006-01-02"), w.Start)
		occ.Note = fmt.Sprintf(
			"%s does not exist on this date in %s because clocks move forward; this occurrence is skipped",
			w.Start, w.Timezone)
		return occ
	}

	// Report a DST transition inside the window, which makes the wall-clock end
	// time differ from start plus duration.
	if _, startOff := start.Zone(); true {
		end := start.Add(w.Duration)
		if _, endOff := end.Zone(); endOff != startOff {
			occ.Note = fmt.Sprintf(
				"clocks change during this window, so it ends at %s local rather than %s",
				end.Format("15:04"),
				fmt.Sprintf("%02d:%02d", (w.Start.Hour+int(w.Duration.Hours()))%24, w.Start.Minute))
		}
	}
	return occ
}

// Preview returns the next occurrences of every window, in order.
//
// Constitution §17: before saving a schedule, show the operator what will
// actually happen. Preview is that function — it resolves real instants,
// renders them in the window's own zone, and flags anything unusual, so an
// operator sees a skipped DST occurrence before they rely on a net that will
// not run.
func (s *Schedule) Preview(from time.Time, perWindow int) []Occurrence {
	if s == nil || perWindow <= 0 {
		return nil
	}

	var out []Occurrence
	for i := range s.windows {
		w := &s.windows[i]
		if !w.Enabled {
			continue
		}
		loc := w.Location()
		local := from.In(loc)
		found := 0

		// Scan forward far enough to find the requested number of occurrences
		// for a once-weekly window, plus a week of margin. The bound must
		// scale with perWindow: a fixed fortnight silently returns two
		// occurrences when three weekly ones were asked for.
		maxDays := 7*perWindow + 7
		for offset := 0; offset <= maxDays && found < perWindow; offset++ {
			day := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc).AddDate(0, 0, offset)
			if !w.runsOn(day.Weekday()) {
				continue
			}
			occ := w.occurrenceOn(day)
			if occ.End.Before(from) {
				continue
			}
			out = append(out, occ)
			found++
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if !out[i].Start.Equal(out[j].Start) {
			return out[i].Start.Before(out[j].Start)
		}
		return out[i].Bridge < out[j].Bridge
	})
	return out
}

// Windows returns a copy of the configured windows.
func (s *Schedule) Windows() []Window {
	if s == nil {
		return nil
	}
	out := make([]Window, len(s.windows))
	for i, w := range s.windows {
		out[i] = w
		out[i].Days = append([]time.Weekday(nil), w.Days...)
	}
	return out
}

// Bridges returns every bridge name the schedule refers to, sorted.
//
// The caller uses it to check that a schedule does not name a bridge that does
// not exist — a typo that would otherwise fail silently, with an operator
// waiting for a net that never links.
func (s *Schedule) Bridges() []string {
	if s == nil {
		return nil
	}
	seen := make(map[string]bool, len(s.windows))
	var out []string
	for _, w := range s.windows {
		if !seen[w.Bridge] {
			seen[w.Bridge] = true
			out = append(out, w.Bridge)
		}
	}
	sort.Strings(out)
	return out
}
