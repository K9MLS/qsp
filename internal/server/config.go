package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	// PendingRestart names the settings the running process is not yet
	// applying, because they need a restart to take effect. Empty means the
	// running server and its configuration agree.
	PendingRestart() []string
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
	// Base is the configuration the page was given, when it sends one.
	//
	// **With it, only what the page changed is saved** (config.Merge):
	// everything else keeps whatever it is now, so a page opened before a
	// change made somewhere else does not undo it. Without it the document
	// replaces the configuration whole, which is what restoring a version
	// means and what a script posting a document expects.
	Base *config.Config `json:"base,omitempty"`
}

// conflictResponse is a save refused because a setting it changes was also
// changed somewhere else after the page was opened.
type conflictResponse struct {
	Error string `json:"error"`
	// Conflicts are the settings, by the paths the history shows.
	Conflicts []string `json:"conflicts"`
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
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, saveBodyLimit))
	// **Unknown fields are refused, as Load refuses them**, and for Load's
	// reason: a field this build does not have is dropped by the decoder, the
	// save reports success, and the operator believes a setting was applied
	// that was never written. This decoder used to accept what the file's own
	// reader would not.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		if field, ok := unknownField(err); ok {
			writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("the request names %s, which is not a setting this "+
					"QSP has; nothing was saved", field),
			})
			return
		}
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "the request body is not a configuration document",
		})
		return
	}

	defer s.editing()()
	before := s.opts.Config.Current()
	if req.Base != nil {
		merged, kept, conflicts, err := config.Merge(*req.Base, req.Config, before)
		if err != nil {
			writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
				"error": "the configuration could not be compared with the running one",
			})
			return
		}
		if len(conflicts) > 0 {
			// Nothing is saved, and it is not a failure worth an audit line:
			// nothing was attempted. The page says which settings and why.
			s.log.Info("a configuration save was refused: settings it changes were changed elsewhere",
				"author", session.Username, "settings", strings.Join(conflicts, ", "))
			writeJSON(w, s.log, http.StatusConflict, conflictResponse{
				Error: "Nothing was saved. Since this page was opened, somebody or something " +
					"else changed settings this save changes too: " + strings.Join(conflicts, ", ") +
					". Reload the page to see what they are now, then make your change again.",
				Conflicts: conflicts,
			})
			return
		}
		if len(kept) > 0 {
			s.log.Info("a configuration save kept changes made elsewhere since its page was opened",
				"author", session.Username, "settings", strings.Join(kept, ", "))
		}
		req.Config = merged
	}
	// **A document with no identifier does not take this server's away.** The
	// identifier is written once and never rewritten (ADR-0053), and a version
	// recorded before the server had one carries none — so reverting to it
	// saved an empty identifier, the next start minted a new one, and the
	// server became a stranger to every neighbour that knew it.
	if strings.TrimSpace(req.Config.Server.Identifier) == "" {
		req.Config.Server.Identifier = before.Server.Identifier
	}
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

	// **Saved is saved.** A save that was written and could not be applied
	// used to be answered "could not be saved", with no version number and a
	// failure in the audit trail, which told the operator the opposite of
	// what had happened; the next restart then surprised them with it (found
	// 2026-10-03). It is reported as the save it was, with the reason it is
	// not live yet where the page shows what needs a restart. See save.
	version, late, err := s.save(r.Context(), req.Config, session.Username, req.Summary)
	if err != nil {
		s.writeSaveError(w, r, session.Username, err)
		return
	}
	needsRestart := notApplied(config.NeedsRestart(before, req.Config), late)

	s.recordConfigChange(r, session.Username, req.Summary, version.Number, audit.OutcomeSuccess)
	s.log.Info("configuration saved",
		"author", session.Username, "version", version.Number, "changes", len(changes))

	writeJSON(w, s.log, http.StatusOK, saveResponse{
		Version:      version.Number,
		Changes:      changes,
		NeedsRestart: needsRestart,
	})
}

// saveBodyLimit is the largest configuration POST /api/config reads. The
// restore limits are derived from it, because what can be saved has to be
// restorable.
const saveBodyLimit = 1 << 20

// unknownField recognises the decoder's refusal of a field it does not know,
// and returns the field, quoted.
//
// encoding/json has no typed error for this, so the message is matched — which
// is why it is matched in one place.
func unknownField(err error) (string, bool) {
	return strings.CutPrefix(err.Error(), "json: unknown field ")
}

// asLoaded is a recorded version as this server would run it today.
//
// **A version in the history is a document an older QSP wrote**, exactly as a
// configuration file can be, and it was not treated as one: a version holding
// an allow-only subscriber list was refused with a 400 when an operator tried
// to go back to it, though the same document in the file starts the server.
// So it gets what Load gives a file, and the identifier this server has now
// when the version predates identifiers.
//
// Done where the version is read rather than where a configuration is saved,
// because a save is an operator's choice and must still be refused for an
// allow-only list (0448) and must keep a weather list they picked; and done
// before the preview, so the changes shown are the ones a restore makes.
func asLoaded(version, running config.Config) config.Config {
	cfg := version.Clone()
	cfg.Upgrade()
	if strings.TrimSpace(cfg.Server.Identifier) == "" {
		cfg.Server.Identifier = running.Server.Identifier
	}
	return cfg
}

// unwrapApply gives the reason a saved configuration could not be applied,
// without the sentinel's own words in front of it.
func unwrapApply(err error) string {
	msg := err.Error()
	if _, reason, ok := strings.Cut(msg, "; it will take effect on restart: "); ok {
		return reason
	}
	return msg
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

// handleConfigVersion returns one version's whole document.
//
// Separate from the list because the list deliberately omits documents — fifty
// versions carrying fifty configurations is a large response nobody reads — and
// this is how the console fetches the one it wants to restore.
func (s *Server) handleConfigVersion(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "this instance keeps no configuration history",
		})
		return
	}

	number, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	if err != nil || number < 1 {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "a version is a positive number",
		})
		return
	}

	version, found, err := s.opts.Config.Version(r.Context(), number)
	if err != nil {
		s.log.Error("cannot read a configuration version", "version", number, "error", err)
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{
			"error": "that version could not be read",
		})
		return
	}
	if !found {
		writeJSON(w, s.log, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("there is no version %d", number),
		})
		return
	}

	// The difference from what is running, so an operator restoring a version
	// sees what it would change before they do it rather than after.
	running := s.opts.Config.Current()
	cfg := asLoaded(version.Config, running)
	changes, err := config.Diff(running, cfg)
	if err != nil {
		changes = nil
	}

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"number":        version.Number,
		"created_at":    version.CreatedAt.UTC().Format(time.RFC3339),
		"author":        version.Author,
		"summary":       version.Summary,
		"checksum":      version.Checksum,
		"config":        cfg,
		"changes":       changes,
		"needs_restart": config.NeedsRestart(running, cfg),
	})
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
