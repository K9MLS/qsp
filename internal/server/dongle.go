package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/dongle"
)

// DongleControl reports on and controls the vocoder dongle's service.
// *dongle.Dongle satisfies it.
type DongleControl interface {
	Status(ctx context.Context) dongle.Status
	Control(ctx context.Context, verb string) error
}

// handleDongle reports the service and the adapter.
func (s *Server) handleDongle(w http.ResponseWriter, r *http.Request) {
	if s.opts.Dongle == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "no transcoder is configured on this instance, so there is no dongle to report on"})
		return
	}
	writeJSON(w, s.log, http.StatusOK, s.opts.Dongle.Status(r.Context()))
}

// handleDongleControl starts, stops or restarts the service.
//
// **Audited whatever the outcome.** Stopping the vocoder takes Zello off the
// air, so who did it and when is exactly what an operator asks afterwards —
// including an attempt systemd refused.
func (s *Server) handleDongleControl(w http.ResponseWriter, r *http.Request) {
	if s.opts.Dongle == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "no transcoder is configured on this instance"})
		return
	}
	verb := r.PathValue("verb")
	err := s.opts.Dongle.Control(r.Context(), verb)

	outcome, status := audit.OutcomeSuccess, http.StatusOK
	switch {
	case err == nil:
	case errors.Is(err, dongle.ErrNotAuthorized):
		outcome, status = audit.OutcomeDenied, http.StatusForbidden
	case errors.Is(err, dongle.ErrNotManaged):
		outcome, status = audit.OutcomeFailure, http.StatusConflict
	default:
		outcome, status = audit.OutcomeFailure, http.StatusBadGateway
	}
	s.recordDongle(r, verb, outcome)
	if err != nil {
		writeJSON(w, s.log, status, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, s.log, http.StatusOK, s.opts.Dongle.Status(r.Context()))
}

func (s *Server) recordDongle(r *http.Request, verb string, outcome audit.Outcome) {
	if s.opts.Audit == nil {
		return
	}
	actor := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		actor = sess.Username
	}
	if err := s.opts.Audit.Record(r.Context(), audit.Event{
		OccurredAt: time.Now().UTC(),
		Actor:      actor,
		Action:     audit.ActionDongleControlled,
		Subject:    verb,
		Outcome:    outcome,
		SourceIP:   clientIP(r, s.opts.BehindProxy),
	}); err != nil {
		s.log.Warn("cannot record a dongle action in the audit trail", "error", err)
	}
}
