package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
)

// ConfigManager is what the configuration endpoints need.
//
// An interface, so the handlers can be tested without a database or a file —
// which is where every decision in ADR-0027 lives, and none of it is in the
// storage.
type ConfigManager interface {
	// Current returns the configuration the instance is running.
	Current() config.Config
	// Writable reports whether a save could succeed, without attempting one.
	// A nil error means it could.
	Writable() error
	// Save validates, records a version, writes the file, and queues the
	// change for the goroutine that owns the routing core.
	Save(ctx context.Context, cfg config.Config, author, summary string) (config.Version, error)
	// Versions returns the history, newest first.
	Versions(ctx context.Context, limit int) ([]config.Version, error)
	// Version returns one version by number.
	Version(ctx context.Context, number int64) (config.Version, bool, error)
}

// configResponse is what GET /api/config returns.
type configResponse struct {
	// Config is the running configuration.
	Config config.Config `json:"config"`
	// Writable reports whether saving is possible at all.
	//
	// **The console asks before it offers a form.** A club running QSP from a
	// read-only file or a container image is a reasonable posture, and finding
	// out at the moment somebody presses save is the difference between a
	// posture and a fault.
	Writable bool `json:"writable"`
	// ReadOnlyReason explains a false Writable.
	ReadOnlyReason string `json:"read_only_reason,omitempty"`
}

// saveRequest is what the console posts.
type saveRequest struct {
	Config  config.Config `json:"config"`
	Summary string        `json:"summary"`
}

// saveResponse reports what happened.
type saveResponse struct {
	// Version is the number recorded.
	Version int64 `json:"version"`
	// Changes are the fields that differ from what was running.
	Changes []config.Change `json:"changes"`
	// NeedsRestart names the settings that were saved and cannot take effect
	// until QSP is restarted.
	//
	// **Fields rather than a flag.** "Restart required" tells an operator to
	// interrupt their network without saying what for, and they will
	// reasonably want to know whether it can wait until the net is over.
	NeedsRestart []string `json:"needs_restart,omitempty"`
}

// validationResponse reports every problem at once.
//
// Every problem, not the first: an operator fixing a form should see all of it
// rather than discovering the next one each time they press save. That is what
// Config.Validate has always done, and this carries it through.
type validationResponse struct {
	Error  string              `json:"error"`
	Fields []validationProblem `json:"fields"`
}

type validationProblem struct {
	Field   string `json:"field"`
	Problem string `json:"problem"`
	Fix     string `json:"fix"`
}

// handleGetConfig returns the running configuration.
//
// It requires a session. The document names the peer password file and the
// database, which is not a set of secrets but is a map of the host, and an
// unauthenticated reader has no business with it.
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "this instance cannot show its configuration; it was started without one",
		})
		return
	}

	body := configResponse{Config: s.opts.Config.Current(), Writable: true}
	if err := s.opts.Config.Writable(); err != nil {
		body.Writable = false
		body.ReadOnlyReason = err.Error()
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// handleSaveConfig validates, records, writes and applies a configuration.
func (s *Server) handleSaveConfig(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "this instance cannot save a configuration; it was started without one",
		})
		return
	}

	session, ok := SessionFrom(r.Context())
	if !ok {
		// requireSession wraps this handler, so reaching here without one
		// means the wrapper was removed. Failing is better than recording a
		// change with no author.
		writeJSON(w, s.log, http.StatusUnauthorized, map[string]string{
			"error": "this endpoint requires a logged-in administrator",
		})
		return
	}

	var req saveRequest
	// Bounded: a configuration is a few kilobytes and an unbounded read is a
	// way to spend memory.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "the request body is not a configuration document",
		})
		return
	}

	before := s.opts.Config.Current()
	changes, err := config.Diff(before, req.Config)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "the configuration could not be compared with the running one",
		})
		return
	}
	if len(changes) == 0 {
		// Saving a form nobody changed should not fill the history with
		// identical versions, and telling the operator nothing changed is more
		// use than a version number.
		writeJSON(w, s.log, http.StatusOK, saveResponse{Changes: []config.Change{}})
		return
	}

	version, err := s.opts.Config.Save(r.Context(), req.Config, session.Username, req.Summary)
	if err != nil {
		s.writeSaveError(w, r, session.Username, err)
		return
	}

	s.recordConfigChange(r, session.Username, req.Summary, version.Number, audit.OutcomeSuccess)
	s.log.Info("configuration saved",
		"author", session.Username, "version", version.Number, "changes", len(changes))

	writeJSON(w, s.log, http.StatusOK, saveResponse{
		Version:      version.Number,
		Changes:      changes,
		NeedsRestart: config.NeedsRestart(before, req.Config),
	})
}

// writeSaveError turns a refused save into something actionable.
func (s *Server) writeSaveError(w http.ResponseWriter, r *http.Request, author string, err error) {
	s.recordConfigChange(r, author, "", 0, audit.OutcomeFailure)

	var invalid *config.ValidationError
	if errors.As(err, &invalid) {
		problems := make([]validationProblem, 0, len(invalid.Errors))
		for _, fe := range invalid.Errors {
			problems = append(problems, validationProblem{
				Field: fe.Field, Problem: fe.Problem, Fix: fe.Fix,
			})
		}
		writeJSON(w, s.log, http.StatusBadRequest, validationResponse{
			Error:  "the configuration has problems that must be fixed before it can be saved",
			Fields: problems,
		})
		return
	}

	if errors.Is(err, config.ErrNotWritable) {
		// 409 rather than 500: nothing failed, the instance is simply not one
		// that can be configured this way, and the message says so.
		writeJSON(w, s.log, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}

	s.log.Error("cannot save the configuration", "author", author, "error", err)
	writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{
		"error": "the configuration could not be saved; see the server log",
	})
}

// recordConfigChange writes the audit event.
//
// **This is what audit_events.actor has been waiting for.** A failed save is
// recorded as well as a successful one: an operator who cannot save is a fact
// worth having later, and the absence of a record would make it look as though
// nobody tried.
func (s *Server) recordConfigChange(r *http.Request, author, summary string, version int64, outcome audit.Outcome) {
	if s.opts.Audit == nil {
		return
	}
	detail := map[string]string{}
	if version > 0 {
		detail["version"] = strconv.FormatInt(version, 10)
	}
	if summary != "" {
		detail["summary"] = summary
	}
	if err := s.opts.Audit.Record(r.Context(), audit.Event{
		OccurredAt: time.Now().UTC(),
		Actor:      author,
		Action:     audit.ActionConfigChanged,
		Outcome:    outcome,
		SourceIP:   clientIP(r, s.opts.BehindProxy),
		Detail:     detail,
	}); err != nil {
		s.log.Warn("cannot record a configuration change in the audit trail", "error", err)
	}
}

// versionSummary is one entry in the history.
//
// **The document is deliberately absent.** A list of fifty versions carrying
// fifty configurations is a large response nobody reads, and the console fetches
// the one it wants by number.
type versionSummary struct {
	Number    int64  `json:"number"`
	CreatedAt string `json:"created_at"`
	Author    string `json:"author"`
	Summary   string `json:"summary,omitempty"`
	Checksum  string `json:"checksum"`
}

// handleConfigVersions returns the history, newest first.
func (s *Server) handleConfigVersions(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "this instance keeps no configuration history",
		})
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	versions, err := s.opts.Config.Versions(r.Context(), limit)
	if err != nil {
		s.log.Error("cannot read configuration history", "error", err)
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{
			"error": "the configuration history could not be read",
		})
		return
	}

	out := make([]versionSummary, 0, len(versions))
	for _, v := range versions {
		out = append(out, versionSummary{
			Number:    v.Number,
			CreatedAt: v.CreatedAt.UTC().Format(time.RFC3339),
			Author:    v.Author,
			Summary:   v.Summary,
			Checksum:  v.Checksum,
		})
	}
	writeJSON(w, s.log, http.StatusOK, map[string]any{"versions": out})
}
