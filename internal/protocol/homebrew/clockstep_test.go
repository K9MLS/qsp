package homebrew_test

import (
	"testing"
	"time"
	"unsafe"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/homebrew"
)

// stepWall returns t with its wall clock moved by d and its monotonic reading
// untouched: what time.Now returns after the system clock has been stepped.
//
// **The time package offers no way to build this**, deliberately: every
// method either moves both readings together or drops the monotonic one. So
// it is made by reaching into time.Time, whose wall field holds, when a
// monotonic reading is present, 33 bits of seconds above 30 bits of
// nanoseconds. That layout is not a promise, so the caller checks the result
// behaves as a stepped clock and skips if it does not.
func stepWall(t time.Time, d time.Duration) time.Time {
	type layout struct {
		wall uint64
		ext  int64
		loc  *time.Location
	}
	if unsafe.Sizeof(layout{}) != unsafe.Sizeof(t) {
		return t
	}
	p := (*layout)(unsafe.Pointer(&t))
	if secs := int64(d / time.Second); secs >= 0 {
		p.wall += uint64(secs) << 30
	} else {
		p.wall -= uint64(-secs) << 30
	}
	return t
}

// steppedClock is a clock whose wall time can jump while real time does not.
type steppedClock struct {
	base    time.Time     // from time.Now, so it carries a monotonic reading
	elapsed time.Duration // real time passed: moves both readings
	step    time.Duration // how far the wall clock has been stepped
}

func (c *steppedClock) now() time.Time { return stepWall(c.base.Add(c.elapsed), c.step) }

// TestAWallClockStepDoesNotMoveTheLinksTimers: a Raspberry Pi with no clock
// battery boots at some old date and steps to the right one when NTP answers.
// The link's keepalive, timeout and backoff are intervals, and an interval
// must not change because the date did.
//
// Each row connects, then lets a little real time pass while the wall clock
// jumps, and says what Tick must do.
//
// To see it fail: make Link.now return l.cfg.Now().UTC(); every row fails.
func TestAWallClockStepDoesNotMoveTheLinksTimers(t *testing.T) {
	probe := time.Now()
	if s := stepWall(probe, time.Hour); s.Sub(probe) != 0 || s.Round(0).Sub(probe.Round(0)) != time.Hour {
		t.Skip("time.Time is not laid out as this test assumes; a stepped clock cannot be built here")
	}

	tests := []struct {
		name string
		// backoff puts the link into backoff before the step, by a refusal.
		backoff   bool
		elapsed   time.Duration
		step      time.Duration
		wantState homebrew.State
		wantSent  string // the kind of the one datagram Tick sends, or ""
	}{
		{"a step forward is not a silent far end",
			false, time.Second, time.Hour, homebrew.StateConnected, ""},
		{"a step back does not postpone the keepalive",
			false, 11 * time.Second, -time.Hour, homebrew.StateConnected, "RPTPING"},
		{"a step back does not postpone the timeout",
			false, 31 * time.Second, -time.Hour, homebrew.StateBackoff, ""},
		{"a step forward does not cut the backoff short",
			true, time.Second, time.Hour, homebrew.StateBackoff, ""},
		{"a step back does not stretch the backoff",
			true, 6 * time.Second, -time.Hour, homebrew.StateLoggingIn, "RPTL"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := &steppedClock{base: time.Now()}
			l, err := homebrew.New(homebrew.Config{
				Name: "xlx950", RepeaterID: linkID, Password: []byte(linkPass),
				Identity:  homebrew.Identity{Callsign: "K9MLS"},
				Keepalive: 10 * time.Second, Timeout: 30 * time.Second,
				MinBackoff: 5 * time.Second, MaxBackoff: 5 * time.Second,
				Now: c.now,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			h := &harness{t: t, l: l}
			h.connect()
			if tc.backoff {
				l.Handle(hbp.Nak{RepeaterID: linkID}.Marshal())
				if l.State() != homebrew.StateBackoff {
					t.Fatalf("state is %q after a refusal, want backoff", l.State())
				}
			}

			c.elapsed, c.step = tc.elapsed, tc.step
			out := l.Tick()

			if l.State() != tc.wantState {
				t.Errorf("state is %q, want %q (%s)", l.State(), tc.wantState, out.Note)
			}
			var sent string
			if len(out.Send) > 0 {
				sent = string(h.parse(out.Send[0]).Kind())
			}
			if sent != tc.wantSent {
				t.Errorf("Tick sent %q, want %q", sent, tc.wantSent)
			}
		})
	}
}
