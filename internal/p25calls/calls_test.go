package p25calls

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 4, 21, 39, 0, 0, time.UTC)

func call(source uint32, startedAfter time.Duration) Call {
	return Call{Started: t0.Add(startedAfter), Source: source, Talkgroup: 1,
		ViaKind: ViaRepeater, Via: "Quantar, site 1", Carried: true}
}

// What Last heard holds after each thing a listener can report.
//
// Break it: announce a call on every frame, lose a call that was never
// reported in progress, or keep more history than was asked for, and a row
// fails.
func TestWhatTheTrackerHolds(t *testing.T) {
	type step struct {
		do   string // "heard" or "finished"
		key  string
		call Call
	}
	tests := []struct {
		name       string
		history    int
		steps      []step
		active     []uint32 // sources, oldest first
		recent     []uint32 // sources, newest first
		starts     int
		ends       int
		lastReason EndReason
	}{
		{"a call in progress", 0,
			[]step{{"heard", "a", call(1, 0)}}, []uint32{1}, nil, 1, 0, ""},
		{"every frame of it is one call", 0,
			[]step{{"heard", "a", call(1, 0)}, {"heard", "a", call(1, 0)}, {"heard", "a", call(1, 0)}},
			[]uint32{1}, nil, 1, 0, ""},
		{"finished, it moves to the history", 0,
			[]step{{"heard", "a", call(1, 0)}, {"finished", "a", call(1, 0)}},
			nil, []uint32{1}, 1, 1, EndMarked},
		{"two stations at once are two calls, oldest first", 0,
			[]step{{"heard", "b", call(2, time.Second)}, {"heard", "a", call(1, 0)}},
			[]uint32{1, 2}, nil, 2, 0, ""},
		{"one finishing leaves the other", 0,
			[]step{{"heard", "a", call(1, 0)}, {"heard", "b", call(2, time.Second)}, {"finished", "a", call(1, 0)}},
			[]uint32{2}, []uint32{1}, 2, 1, EndMarked},
		{"a call reported only at its end is still recorded", 0,
			[]step{{"finished", "a", call(1, 0)}}, nil, []uint32{1}, 0, 1, EndMarked},
		{"the newest finished is first", 0,
			[]step{{"finished", "a", call(1, 0)}, {"finished", "a", call(2, 0)}, {"finished", "b", call(3, 0)}},
			nil, []uint32{3, 2, 1}, 0, 3, EndMarked},
		{"the history is cut to its size, oldest out", 2,
			[]step{{"finished", "a", call(1, 0)}, {"finished", "a", call(2, 0)}, {"finished", "a", call(3, 0)}},
			nil, []uint32{3, 2}, 0, 3, EndMarked},
		{"a reason given is kept", 0,
			[]step{{"finished", "a", Call{Started: t0, Source: 1, EndReason: EndQuiet}}},
			nil, []uint32{1}, 0, 1, EndQuiet},
		{"the same station again is a new call", 0,
			[]step{{"heard", "a", call(1, 0)}, {"finished", "a", call(1, 0)}, {"heard", "a", call(2, time.Minute)}},
			[]uint32{2}, []uint32{1}, 2, 1, EndMarked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var starts, ends int
			var last Call
			tr := NewTracker(Options{
				History: tc.history,
				OnStart: func(Call) { starts++ },
				OnEnd:   func(c Call) { ends++; last = c },
			})
			for _, s := range tc.steps {
				if s.do == "heard" {
					tr.Heard(s.key, s.call)
				} else {
					tr.Finished(s.key, s.call, t0.Add(5*time.Second))
				}
			}
			active, recent := tr.Snapshot()
			if got := sources(active); !equal(got, tc.active) {
				t.Errorf("in progress: %v, want %v", got, tc.active)
			}
			if got := sources(recent); !equal(got, tc.recent) {
				t.Errorf("finished: %v, want %v", got, tc.recent)
			}
			if starts != tc.starts || ends != tc.ends {
				t.Errorf("%d starts and %d ends announced, want %d and %d", starts, ends, tc.starts, tc.ends)
			}
			for _, c := range active {
				if !c.InProgress() {
					t.Errorf("a call in progress has an end time: %+v", c)
				}
			}
			for _, c := range recent {
				if c.InProgress() {
					t.Errorf("a finished call has no end time: %+v", c)
				}
			}
			if tc.ends > 0 && last.EndReason != tc.lastReason {
				t.Errorf("the last call ended %q, want %q", last.EndReason, tc.lastReason)
			}
		})
	}
}

// Break it: let a seed push out a call that finished since the tracker was
// made, and a restart shows yesterday ahead of a moment ago.
func TestSeedingGoesBehindWhatIsAlreadyThere(t *testing.T) {
	tr := NewTracker(Options{History: 3})
	tr.Finished("a", call(9, 0), t0)
	tr.Seed([]Call{{Source: 3, Ended: t0}, {Source: 2, Ended: t0}, {Source: 1, Ended: t0}})
	_, recent := tr.Snapshot()
	if got, want := sources(recent), []uint32{9, 3, 2}; !equal(got, want) {
		t.Errorf("after seeding: %v, want %v", got, want)
	}
}

// A listener reports without asking whether anything is listening.
//
// Break it: dereference a nil tracker, and a server with no P25 record
// panics on the first call.
func TestNoTrackerAcceptsEverythingAndKeepsNothing(t *testing.T) {
	var tr *Tracker
	tr.Heard("a", call(1, 0))
	if got := tr.Finished("a", call(1, 0), t0); got.Source != 1 {
		t.Errorf("the call came back as %+v", got)
	}
	tr.Seed([]Call{call(2, 0)})
	if a, r := tr.Snapshot(); a != nil || r != nil {
		t.Errorf("a nil tracker held %v and %v", a, r)
	}
}

// Break it: measure a call in progress from its end time, which is zero, and
// its duration is fifty-six years.
func TestHowLongACallRan(t *testing.T) {
	tests := []struct {
		name string
		call Call
		want time.Duration
	}{
		{"in progress, measured to now", Call{Started: t0}, 3 * time.Second},
		{"finished, measured to its end", Call{Started: t0, Ended: t0.Add(5 * time.Second)}, 5 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.call.Duration(t0.Add(3 * time.Second)); got != tc.want {
				t.Errorf("got %v", got)
			}
		})
	}
}

func sources(calls []Call) []uint32 {
	var out []uint32
	for _, c := range calls {
		out = append(out, c.Source)
	}
	return out
}

func equal(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
