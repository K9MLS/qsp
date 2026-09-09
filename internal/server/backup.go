package server

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/buildinfo"
	"github.com/k9mls/qsp/internal/config"
)

// Backup and restore (ADR-0054).
//
// # Why an export is a download and an import is a paste
//
// An export is a file an operator keeps, mails, or commits, so it arrives as a
// download with a filename. An import is a decision with consequences, so it
// arrives as a document the operator has looked at, with a confirmation in
// front of it — the same shape as accepting a peering, and for the same reason.

// handleBackup returns this server's configuration as a file.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance has no configuration to export"})
		return
	}

	cfg := s.opts.Config.Current()
	backup := config.NewBackup(cfg, buildinfo.Version, time.Now())

	var body bytes.Buffer
	if err := config.WriteBackup(&body, backup); err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": err.Error()})
		return
	}

	// **Named after the server and the day**, because an operator with three
	// backups in a downloads folder needs to tell them apart, and
	// `qsp-backup.json` three times over is how the wrong one gets restored.
	name := strings.TrimSpace(cfg.DMR.Join.NetworkName)
	if name == "" {
		name = "qsp"
	}
	filename := fmt.Sprintf("%s-%s.qspbackup.json",
		safeFilename(name), backup.ExportedAt.Format("2006-01-02"))

	s.recordBackup(r, "config.exported", audit.OutcomeSuccess)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	if _, err := w.Write(body.Bytes()); err != nil {
		s.log.Warn("cannot send a backup", "error", err.Error())
	}
}

// restoreRequest is an export being imported.
type restoreRequest struct {
	// Document is the export, as pasted or uploaded.
	Document string `json:"document"`
	// Confirm must be true. **An import replaces every setting on this
	// server**, and a request that arrives without it is answered with what
	// would happen rather than by doing it.
	Confirm bool `json:"confirm"`
	// NewIdentity asks for a fresh identifier instead of the one in the file.
	//
	// **The difference between a replacement and a clone.** Restoring onto a
	// second machine while the first still runs gives two servers claiming one
	// identity, which is the collision class this project has met repeatedly
	// and which failed silently every time. QSP cannot see the other machine,
	// so it asks rather than checking.
	NewIdentity bool `json:"new_identity"`
}

// restoreResponse reports what an import did and what it could not.
type restoreResponse struct {
	Version int64 `json:"version"`
	// Identifier is what the restored server is now called, truncated.
	Identifier string `json:"identifier,omitempty"`
	// Replaced reports that the file's identifier was taken, rather than a new
	// one generated.
	Replaced bool `json:"replaced"`
	// Missing lists the credentials the restored server does not have.
	//
	// **A restored link is awaiting a credential, not broken.** That
	// distinction is the entire reason ADR-0054 exists: without it, a restore
	// produces a page full of links reporting themselves configured and not
	// open, which reads as a network fault.
	Missing []config.MissingCredential `json:"missing_credentials,omitempty"`
	// NeedsRestart is everything the running process is not yet applying —
	// after an import, almost everything.
	NeedsRestart []string `json:"needs_restart,omitempty"`
}

// handleRestore replaces this server's configuration with an export.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	var req restoreRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}

	backup, err := config.ReadBackup(strings.NewReader(req.Document))
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if !req.Confirm {
		// Said before it happens, in the operator's terms, and it is the whole
		// of what an import does rather than a summary of part of it.
		writeJSON(w, s.log, http.StatusPreconditionRequired, map[string]any{
			"error": "not confirmed",
			"summary": fmt.Sprintf("This replaces every setting on this server with the "+
				"backup taken on %s by QSP %s. %d credentials cannot be restored and are "+
				"listed below; links and members will be refused until they are reissued.",
				backup.ExportedAt.Format("2 January 2006"), backup.QSPVersion,
				len(backup.Missing)),
			"identity": "The backup carries a server identifier. Taking it makes this " +
				"machine a replacement for the one that made the backup — if that server " +
				"is still running, two servers will claim one identity and neither will " +
				"say so. QSP cannot see the other machine and will not pretend to check.",
			"missing_credentials": backup.Missing,
			"exported_at":         backup.ExportedAt,
			"qsp_version":         backup.QSPVersion,
		})
		return
	}

	cfg := backup.Config
	replaced := false
	switch {
	case req.NewIdentity || strings.TrimSpace(backup.Identifier) == "":
		id, gerr := config.NewIdentifier()
		if gerr != nil {
			writeJSON(w, s.log, http.StatusInternalServerError,
				map[string]string{"error": gerr.Error()})
			return
		}
		cfg.Server.Identifier = id
	default:
		if verr := config.ValidIdentifier(backup.Identifier); verr != nil {
			writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": verr.Error()})
			return
		}
		cfg.Server.Identifier = backup.Identifier
		replaced = true
	}

	// **The console's own address is not restored.** A backup taken from a
	// server bound to one address, imported onto a machine that does not have
	// it, produces an instance that cannot bind — and the operator finds out
	// when the console they are reading stops answering. The listening address
	// belongs to the machine, not to the configuration being carried.
	cfg.Server.ListenAddress = s.opts.Config.Current().Server.ListenAddress

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	summary := fmt.Sprintf("restored from a backup taken %s",
		backup.ExportedAt.Format("2006-01-02"))

	before := s.opts.Config.Current()
	version, err := s.opts.Config.Save(r.Context(), cfg, author, summary)
	if err != nil {
		s.recordBackup(r, "config.restored", audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.recordBackup(r, "config.restored", audit.OutcomeSuccess)

	writeJSON(w, s.log, http.StatusOK, restoreResponse{
		Version:      version.Number,
		Identifier:   config.ShortIdentifier(cfg.Server.Identifier),
		Replaced:     replaced,
		Missing:      config.MissingCredentials(cfg),
		NeedsRestart: config.NeedsRestart(before, cfg),
	})
}

// recordBackup writes the audit event ADR-0032 requires.
func (s *Server) recordBackup(r *http.Request, action string, outcome audit.Outcome) {
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
		Action:     audit.Action(action),
		Outcome:    outcome,
	}); err != nil {
		s.log.Warn("cannot record a backup in the audit trail", "error", err.Error())
	}
}

// safeFilename reduces a display name to something a browser will save.
//
// Not a slug for storage — nothing reads this back — but a filename an operator
// can tell apart from two others in a downloads folder.
func safeFilename(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}
