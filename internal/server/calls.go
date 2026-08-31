package server

import (
	"context"
	"net/http"
	"time"

	"github.com/k9mls/qsp/internal/calls"
)

// CallHistory reads completed calls back.
//
// An interface rather than the store, for the reason every other source here is
// one: the console reads and must not be able to write, and a handler holding a
// store is one that eventually prunes from a request.
type CallHistory interface {
	// Since returns completed calls that started at or after from, newest
	// first, capped at limit.
	Since(ctx context.Context, from time.Time, limit int) ([]calls.Call, error)
	// Enabled reports whether anything is kept at all.
	Enabled() bool
}

// callView is one call as the console shows it.
type callView struct {
	// Source is the radio that keyed up, and Callsign is who that is when the
	// registry knows.
	Source   uint32 `json:"source"`
	Callsign string `json:"callsign,omitempty"`
	// Target is the talkgroup or radio called.
	Target uint32 `json:"target"`
	// Group distinguishes a talkgroup call from a private one.
	Group bool `json:"group"`
	// Timeslot is 1 or 2.
	Timeslot int `json:"timeslot"`
	// Started is when the first frame arrived, in UTC.
	Started time.Time `json:"started"`
	// Seconds is how long it ran. Zero for a one-burst data message.
	Seconds float64 `json:"seconds"`
	// Voice distinguishes a transmission from a text message, which arrives as
	// a handful of one-frame data bursts and would otherwise bury the voice
	// this list exists to show.
	Voice bool `json:"voice"`
	// Frames counts what arrived.
	Frames int `json:"frames"`
	// EndReason records how it finished, empty when it ended normally.
	EndReason string `json:"end_reason,omitempty"`
}

// callsResponse is the shape returned by /api/calls.
type callsResponse struct {
	Calls []callView `json:"calls"`
	// Since is the window that was asked for, echoed so a console showing "the
	// last two hours" is showing what it says.
	Since time.Time `json:"since"`
	// Reason explains an empty list when no record is kept at all, which is
	// different from a quiet network.
	Reason string `json:"reason,omitempty"`
}

// handleCalls reads the call record.
//
// **Authenticated, unlike the live list on the overview.** The overview shows
// what is happening now, which anybody watching a repeater can hear anyway. This
// is thirty days of who transmitted and when, which is a different thing: a
// record of members' activity, and one an administrator should have to sign in
// to read.
func (s *Server) handleCalls(w http.ResponseWriter, r *http.Request) {
	body := callsResponse{Calls: []callView{}}

	if s.opts.Calls == nil || !s.opts.Calls.Enabled() {
		body.Reason = "no call record is kept; set dmr.calls.retain to keep one"
		body.Since = time.Now().UTC()
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}

	// Defaulting to twelve hours covers an evening net looked at the next
	// morning, which is the reason this exists.
	hours := queryInt(r, "hours", 12)
	if hours < 1 {
		hours = 1
	}
	if hours > 24*400 {
		hours = 24 * 400
	}
	limit := queryInt(r, "limit", 200)
	if limit > 1000 {
		limit = 1000
	}

	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)
	body.Since = since

	found, err := s.opts.Calls.Since(r.Context(), since, limit)
	if err != nil {
		s.log.Warn("cannot read the call record", "error", err)
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot read the call record"})
		return
	}

	for _, c := range found {
		view := callView{
			Source:    c.Source,
			Target:    c.Target,
			Group:     c.Group,
			Timeslot:  int(c.Key.Timeslot),
			Started:   c.Started.UTC(),
			Seconds:   c.Ended.Sub(c.Started).Seconds(),
			Voice:     c.Voice,
			Frames:    c.Frames,
			EndReason: string(c.EndReason),
		}
		if s.opts.Callsign != nil {
			// **Resolved at read time, not stored.** A callsign can be wrong
			// when a call happens and right a week later, and the record's job
			// is to say which radio transmitted rather than to freeze a guess
			// about whose it was.
			view.Callsign = s.opts.Callsign(c.Source)
		}
		body.Calls = append(body.Calls, view)
	}
	writeJSON(w, s.log, http.StatusOK, body)
}
