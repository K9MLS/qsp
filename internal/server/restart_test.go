package server

import (
	"strings"
	"testing"
	"time"
)

// **The response has to leave before the process does.** Exiting inside the
// handler closes the connection before the answer arrives, so the page reports
// a network error for an action that succeeded — and an operator who believes a
// restart failed presses the button again.
func TestARestartWaitsForItsAnswerToLeave(t *testing.T) {
	if restartDelay <= 0 {
		t.Fatal("QSP exits before its answer is written")
	}
	if restartDelay > 5*time.Second {
		t.Errorf("the delay is %s; an operator will press the button again", restartDelay)
	}
}

// **It says what happens rather than promising an outcome.** QSP cannot restart
// itself: it exits, and whatever supervises it starts it again — or nothing
// does, and the server stays down. It cannot see its supervisor from inside, so
// it must not imply a check it has not made.
func TestARestartDoesNotPromiseToComeBack(t *testing.T) {
	// Asserted against the constant the handler sends, not against a copy the
	// test wrote — a test that builds the thing it then checks cannot fail,
	// which this project has managed six times.
	if !strings.Contains(restartNote, "stays down") {
		t.Error("the note does not say the server may not come back")
	}
	if !strings.Contains(restartNote, "drops") {
		t.Error("the note does not say that everything connected drops")
	}
	for _, promise := range []string{"will be back", "will restart", "restarts automatically", "comes back"} {
		if strings.Contains(restartNote, promise) {
			t.Errorf("the note says %q, which QSP cannot see from inside", promise)
		}
	}
}
