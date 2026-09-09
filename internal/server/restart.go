package server

import (
	"net/http"
	"time"

	"github.com/k9mls/qsp/internal/audit"
)

// Restarting QSP from the console.
//
// # Why a button exists at all
//
// **The Links page tells an operator to restart QSP in four places and cannot
// do it.** An upstream is built once at startup, so a link written through the
// accept form carries nothing until the process comes back — and the page says
// so, then leaves the operator to find a terminal and remember which of two
// unrelated commands applies. Production is systemd and the test server is
// Docker Compose; the page knows neither, and an operator on a phone has
// neither.
//
// It is the rule that produced the Stop accepting button: an instruction a page
// gives is an instruction the page should be able to carry out.
//
// # What it actually does, which is not restarting
//
// **QSP cannot restart itself.** It exits, and whatever supervises it starts it
// again — systemd with `Restart=always`, Docker with a restart policy, or
// nothing at all, in which case the server stays down. QSP cannot see its own
// supervisor from inside and will not imply a check it has not made, so the
// confirmation says what happens rather than promising an outcome.
//
// The exit is the ordinary one: the same signal path as `systemctl restart`,
// so the audit record, the shutdown timeout and every subsystem's close run
// exactly as they always have. Nothing bespoke happens on the way out.

// restartResponse is what the console is told before the process goes.
type restartResponse struct {
	// Restarting is always true; the field exists so a client can tell a
	// successful request from an error body.
	Restarting bool `json:"restarting"`
	// Note is what an operator needs to know: what drops, and the one case
	// where the server does not come back.
	Note string `json:"note"`
}

// restartDelay is how long the process waits before exiting.
//
// Long enough for the response to reach the browser and be rendered, short
// enough that nobody wonders whether the button worked. Exiting inside the
// handler would close the connection before the answer arrived, so the page
// would report a network error for an action that succeeded.
const restartDelay = 500 * time.Millisecond

// restartNote is what an operator is told, and it promises nothing.
//
// **QSP cannot see its own supervisor.** Whether anything starts it again is a
// property of the machine, not of this process, so the note says what QSP does
// and names the case where the server stays down rather than implying a check
// that was never made.
const restartNote = "QSP is stopping. Every hotspot and link drops and reconnects. " +
	"If nothing on this machine is set to start QSP again, it stays down."

// handleRestart stops QSP so that its supervisor starts it again.
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if s.opts.Restart == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "this instance cannot restart itself from here",
		})
		return
	}

	actor := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		actor = sess.Username
	}
	if s.opts.Audit != nil {
		// **Recorded before it happens**, because the process is about to stop
		// and an audit written afterwards is an audit that never gets written.
		if err := s.opts.Audit.Record(r.Context(), audit.Event{
			OccurredAt: time.Now().UTC(),
			Actor:      actor,
			Action:     "service.restart.requested",
			Outcome:    audit.OutcomeSuccess,
		}); err != nil {
			s.log.Warn("cannot record a restart in the audit trail",
				"error", err.Error())
		}
	}

	s.log.Info("restart requested from the console", "actor", actor)

	writeJSON(w, s.log, http.StatusAccepted, restartResponse{
		Restarting: true,
		Note:       restartNote,
	})

	go func() {
		time.Sleep(restartDelay)
		s.opts.Restart()
	}()
}
