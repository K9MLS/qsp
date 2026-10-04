package server

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/p25calls"
)

// P25CallHistory is the record of completed P25 transmissions.
type P25CallHistory interface {
	Since(ctx context.Context, from time.Time, limit int) ([]p25calls.Call, error)
	Enabled() bool
}

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
	// Timeslot is 1 or 2, and absent for a P25 call, which has none.
	Timeslot int `json:"timeslot,omitempty"`
	// Mode is "P25" for a P25 call and absent for a DMR one. Via is where a
	// P25 call came into QSP, and NotCarried that it was heard and not
	// relayed because another station was talking.
	Mode       string `json:"mode,omitempty"`
	Via        string `json:"via,omitempty"`
	NotCarried bool   `json:"not_carried,omitempty"`
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

	dmr := s.opts.Calls != nil && s.opts.Calls.Enabled()
	p25 := s.opts.P25Calls != nil && s.opts.P25Calls.Enabled()
	if !dmr && !p25 {
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

	if dmr {
		found, err := s.opts.Calls.Since(r.Context(), since, limit)
		if err != nil {
			s.log.Warn("cannot read the call record", "error", err)
			writeJSON(w, s.log, http.StatusInternalServerError,
				map[string]string{"error": "cannot read the call record"})
			return
		}
		for _, c := range found {
			body.Calls = append(body.Calls, callView{
				Source:    c.Source,
				Target:    c.Target,
				Group:     c.Group,
				Timeslot:  int(c.Key.Timeslot),
				Started:   c.Started.UTC(),
				Seconds:   c.Ended.Sub(c.Started).Seconds(),
				Voice:     c.Voice,
				Frames:    c.Frames,
				EndReason: string(c.EndReason),
			})
		}
	}
	if p25 {
		found, err := s.opts.P25Calls.Since(r.Context(), since, limit)
		if err != nil {
			s.log.Warn("cannot read the P25 call record", "error", err)
			writeJSON(w, s.log, http.StatusInternalServerError,
				map[string]string{"error": "cannot read the call record"})
			return
		}
		for _, c := range found {
			body.Calls = append(body.Calls, p25Record(c))
		}
	}

	// **One record, newest first, and cut to the limit after merging.** Each
	// store was asked for up to limit rows, so the newest limit of both
	// together are among what came back.
	sort.SliceStable(body.Calls, func(i, j int) bool {
		return body.Calls[i].Started.After(body.Calls[j].Started)
	})
	if len(body.Calls) > limit {
		body.Calls = body.Calls[:limit]
	}
	if s.opts.Callsign != nil {
		for i := range body.Calls {
			if body.Calls[i].Source != 0 {
				body.Calls[i].Callsign = s.opts.Callsign(body.Calls[i].Source)
			}
		}
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// p25Record is one P25 call as the record page shows it. **No timeslot**,
// because the mode has none; a group call always, because that is all a
// repeater or gateway has been heard to carry.
func p25Record(c p25calls.Call) callView {
	return callView{
		Source:     c.Source,
		Target:     uint32(c.Talkgroup),
		Group:      true,
		Mode:       "P25",
		Via:        c.Via,
		NotCarried: !c.Carried,
		Started:    c.Started.UTC(),
		Seconds:    c.Ended.Sub(c.Started).Seconds(),
		Voice:      true,
		Frames:     c.Frames,
		EndReason:  p25EndReason(c.EndReason),
	}
}

// p25EndReason is how a P25 call's end is written on the record page: empty
// when its own station closed it, which is the ordinary case and needs no
// remark, as on a DMR row.
func p25EndReason(r p25calls.EndReason) string {
	if r == p25calls.EndMarked {
		return ""
	}
	return string(r)
}
