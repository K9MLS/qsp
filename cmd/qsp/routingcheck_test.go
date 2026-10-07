package main

import (
	"context"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/health"
)

// routingLine is a routing check for a server started as described, whose saved
// configuration now says what the last two arguments say.
func routingLine(listening, forwarding bool, bridges int, wanted bool) routingCheck {
	return routingCheck{
		listening: listening, forwarding: forwarding,
		bridges: func() int { return bridges },
		wanted:  func() bool { return wanted },
	}
}

// TestTheRoutingLineSaysWhatIsRunning. The line was built once, from two
// numbers read at startup: a bridge added from the console left it saying
// there were none, and a server with Forwarding set and no DMR listener was
// told its stations could hear each other (2026-10-07, G9). Forwarding saved
// and not yet in force had no words at all (G2).
//
// To see each fail: in routingCheck.Check, remove the branch its name says.
func TestTheRoutingLineSaysWhatIsRunning(t *testing.T) {
	for _, tc := range []struct {
		name   string
		check  routingCheck
		status health.Status
		says   []string
		not    string
	}{
		// A master that repeats with no bridges is the ordinary club network,
		// not a degraded one (ADR-0019).
		{"no bridges", routingLine(true, true, 0, true), health.StatusHealthy,
			[]string{"hear each other", "no bridges"}, ""},
		{"two bridges", routingLine(true, true, 2, true), health.StatusHealthy,
			[]string{"hear each other", "2 bridge"}, ""},
		{"forwarding off", routingLine(true, false, 0, false), health.StatusUnavailable,
			[]string{"hear each other", "Forwarding is off"}, "restart"},
		{"no listener, forwarding set", routingLine(false, true, 3, true), health.StatusUnavailable,
			[]string{"not accepted"}, "hear each other"},
		{"forwarding turned on, not restarted", routingLine(true, false, 0, true), health.StatusUnavailable,
			[]string{"restarted", "cannot hear each other"}, "Forwarding is off"},
		{"forwarding turned off, not restarted", routingLine(true, true, 1, false), health.StatusDegraded,
			[]string{"still running", "restarted"}, "across"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.check.Check(context.Background())
			if res.Status != tc.status {
				t.Errorf("status is %q, want %q: %s", res.Status, tc.status, res.Summary)
			}
			for _, want := range tc.says {
				if !strings.Contains(res.Summary, want) {
					t.Errorf("the line does not say %q: %q", want, res.Summary)
				}
			}
			if tc.not != "" && strings.Contains(res.Summary, tc.not) {
				t.Errorf("the line says %q, which is not so: %q", tc.not, res.Summary)
			}
		})
	}
}

// TestTheHealthLinesCountWhatIsSavedNow: the count is asked for each time.
//
// To see it fail: have either check keep the number it was built with.
func TestTheHealthLinesCountWhatIsSavedNow(t *testing.T) {
	bridges, windows := 0, 0
	r := routingCheck{listening: true, forwarding: true,
		bridges: func() int { return bridges }, wanted: func() bool { return true }}
	s := schedulerCheck{windows: func() int { return windows }, forwarding: true}

	if got := s.Check(context.Background()); got.Status != health.StatusUnavailable {
		t.Errorf("no schedule, and the line is %q: %s", got.Status, got.Summary)
	}
	bridges, windows = 4, 2
	if got := r.Check(context.Background()).Summary; !strings.Contains(got, "4 bridge") {
		t.Errorf("four bridges were saved and the line says %q", got)
	}
	if got := s.Check(context.Background()).Summary; !strings.Contains(got, "2 scheduled") {
		t.Errorf("two windows were saved and the line says %q", got)
	}
}
