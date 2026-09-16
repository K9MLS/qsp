package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/buildinfo"
	"github.com/k9mls/qsp/internal/config"
)

// The encrypted full backup: [ADR-0065].
//
// # Two backups, and the words on them matter more than the code
//
// The shareable export carries configuration and no secrets, and exists to be
// emailed to somebody helping with a broken server. This one carries the
// credentials too, encrypted, and exists for an operator recovering their own
// machine.
//
// **Mailing the wrong one publishes every password on the server.** That is
// the first thing to get right about having two, which is why the file
// extensions differ, the endpoints differ, and an import of one says which it
// found rather than only complaining.
//
// # The passphrase
//
// ADR-0065 requires QSP to say, **when it makes the file**, that the
// passphrase is the operator's to keep and that losing it makes the backup
// useless. A full backup nobody can decrypt is worse than a partial one
// because of what the operator believes about it.
//
// The response carries [config.PassphraseWarning] for the console to show, and
// the passphrase itself is never stored, logged, or written into the audit
// trail.

// fullBackupRequest carries the passphrase.
//
// **In the body and never the query string**, which is logged by every proxy
// in the way, written into an access log and kept in a browser's history — and
// a passphrase in a log is every backup made with it.
type fullBackupRequest struct {
	Passphrase string `json:"passphrase"`
}

// handleFullBackup writes an encrypted backup carrying the credentials.
//
// A POST rather than a GET, because it takes a passphrase in a body and
// because it is not a safe, repeatable read: each call produces a new file
// carrying every secret on the server.
func (s *Server) handleFullBackup(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance has no configuration to export"})
		return
	}
	store, ok := s.secretStore(w)
	if !ok {
		return
	}

	var body fullBackupRequest
	if !decodeJSON(w, s.log, r, &body) {
		return
	}
	if strings.TrimSpace(body.Passphrase) == "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "a full backup needs a passphrase; it carries every " +
				"credential on this server",
			"warning": config.PassphraseWarning,
		})
		return
	}

	values, err := s.allSecrets(r.Context(), store)
	if err != nil {
		s.recordBackup(r, audit.ActionConfigFullExported, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot read the stored credentials: " + err.Error()})
		return
	}

	cfg := s.opts.Config.Current()
	full := config.NewFullBackup(cfg, values, buildinfo.Version, time.Now())

	var out bytes.Buffer
	if err := config.WriteFullBackup(&out, full, body.Passphrase); err != nil {
		s.recordBackup(r, audit.ActionConfigFullExported, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": err.Error()})
		return
	}

	// **A different extension from the shareable export**, so the two are
	// distinguishable in a downloads folder months later. `.qspfull` is the
	// one that must never be emailed.
	name := strings.TrimSpace(cfg.DMR.Join.NetworkName)
	if name == "" {
		name = "qsp"
	}
	filename := fmt.Sprintf("%s-%s.qspfull",
		safeFilename(name), full.Backup.ExportedAt.Format("2006-01-02"))

	// The audit trail records that a full backup was taken and by whom —
	// which matters more than for the shareable one, because the file is a
	// credential. **Never the passphrase**, and not the names of the secrets
	// either: a trail listing which credentials exist is a map for somebody
	// who later gets the file.
	s.recordBackup(r, audit.ActionConfigFullExported, audit.OutcomeSuccess)

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	// The warning travels in a header as well as being the console's to show,
	// so an operator using curl is told too.
	w.Header().Set("X-QSP-Passphrase-Warning", config.PassphraseWarning)
	if _, err := w.Write(out.Bytes()); err != nil {
		s.log.Warn("cannot send a full backup", "error", err.Error())
	}
}

// fullRestoreRequest carries the file, its passphrase and the confirmation.
//
// The document is base64 because the file is binary and this travels as JSON —
// the same shape as the shareable restore, so one console page pattern covers
// both and the body is bounded by the JSON decoder rather than by a limit this
// handler has to remember.
type fullRestoreRequest struct {
	Document   string `json:"document"`
	Passphrase string `json:"passphrase"`
	Confirm    bool   `json:"confirm"`
}

// handleFullRestore reads an encrypted backup and applies it.
//
// **Configuration and credentials in one action**, because a restore that
// wrote the configuration and left the credentials for a second step would
// leave a server whose links are configured and silent — the state the
// shareable import has to explain, and the whole point of this format is not
// having it.
//
// **It confirms first, exactly as the shareable restore does.** ADR-0065 says
// so and ADR-0054 gives the reason: a backup carries a server identifier, so
// taking it makes this machine a replacement for the one that made it, and two
// servers claiming one identity is a collision class this project has met
// repeatedly. The credentials this format restores belong to links whose far
// ends have not been asked, which is the same confirmation covering more.
func (s *Server) handleFullRestore(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot restore a configuration"})
		return
	}
	store, ok := s.secretStore(w)
	if !ok {
		return
	}

	var req fullRestoreRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}
	if strings.TrimSpace(req.Passphrase) == "" {
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": "a full backup needs its passphrase to be opened"})
		return
	}

	raw, err := base64.StdEncoding.DecodeString(req.Document)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": "the uploaded file is not base64: " + err.Error()})
		return
	}

	full, err := config.ReadFullBackup(bytes.NewReader(raw), req.Passphrase)
	switch {
	case errors.Is(err, config.ErrNotAFullBackup):
		s.recordBackup(r, audit.ActionConfigFullRestored, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
			"fix": "the shareable export is restored through /api/admin/restore; " +
				"this endpoint reads the encrypted full backup",
		})
		return
	case errors.Is(err, config.ErrWrongPassphrase):
		s.recordBackup(r, audit.ActionConfigFullRestored, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusUnauthorized,
			map[string]string{"error": err.Error()})
		return
	case err != nil:
		s.recordBackup(r, audit.ActionConfigFullRestored, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": err.Error()})
		return
	}

	if err := full.Backup.Config.Validate(); err != nil {
		s.recordBackup(r, audit.ActionConfigFullRestored, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "the configuration in this backup is not valid: " + err.Error(),
		})
		return
	}

	if !req.Confirm {
		// Said before it happens, in the operator's terms, and naming what
		// will come back rather than summarising it. The identity warning is
		// the shareable restore's, unchanged, because the hazard is the same
		// one.
		writeJSON(w, s.log, http.StatusPreconditionRequired, map[string]any{
			"error": "not confirmed",
			"summary": fmt.Sprintf("This replaces every setting on this server "+
				"with the full backup taken on %s by QSP %s, and restores %d "+
				"credentials. Links and members will work immediately.",
				full.Backup.ExportedAt.Format("2 January 2006"),
				full.Backup.QSPVersion, len(full.Secrets)),
			"identity": "The backup carries a server identifier. Taking it makes " +
				"this machine a replacement for the one that made the backup — if " +
				"that server is still running, two servers will claim one identity " +
				"and neither will say so. QSP cannot see the other machine and " +
				"will not pretend to check.",
			"credentials": "These credentials belong to links whose far ends have " +
				"not been asked. Restoring them revives the passwords the other " +
				"operators issued, which is why this is a replacement rather than " +
				"a second server.",
			"credential_names": full.SecretNames(),
			"exported_at":      full.Backup.ExportedAt,
			"qsp_version":      full.Backup.QSPVersion,
		})
		return
	}

	actor := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		actor = sess.Username
	}

	// **The credentials before the configuration.** If the configuration
	// lands first and a credential write then fails, the server is running a
	// configuration whose links have no passwords — silent, and looking
	// correct. The other order leaves credentials for links that do not exist
	// yet, which is inert.
	restored := make([]string, 0, len(full.Secrets))
	for _, name := range full.SecretNames() {
		if err := store.Set(r.Context(), name, full.Secrets[name], actor); err != nil {
			s.recordBackup(r, audit.ActionConfigFullRestored, audit.OutcomeFailure)
			writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{
				"error": fmt.Sprintf("restored %d of %d credentials and then "+
					"failed on %q: %v; the configuration has not been changed",
					len(restored), len(full.Secrets), name, err),
			})
			return
		}
		restored = append(restored, name)
	}

	version, err := s.opts.Config.Save(r.Context(), full.Backup.Config, actor,
		fmt.Sprintf("restored from a full backup of %s",
			full.Backup.ExportedAt.Format("2006-01-02")))
	if err != nil {
		s.recordBackup(r, audit.ActionConfigFullRestored, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{
			"error": "the credentials were restored and the configuration was " +
				"not: " + err.Error(),
		})
		return
	}

	s.recordBackup(r, audit.ActionConfigFullRestored, audit.OutcomeSuccess)

	// **What came back, named.** An operator told "restored" has a different
	// confidence from one who can see that four credentials returned — and
	// ADR-0065 replaced the shareable export's missing-credentials list with
	// exactly this positive statement.
	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"version":     version.Number,
		"exported":    full.Backup.ExportedAt,
		"credentials": restored,
	})
}

// allSecrets reads every stored credential for a backup.
//
// **A secret that will not decrypt fails the whole backup.** Writing a file
// that silently lacks one credential produces a restore that comes up with
// three links working and one not, for a reason nothing in the file records —
// and the operator has no way to know the backup was incomplete when they made
// it. A key mismatch is worth stopping for.
func (s *Server) allSecrets(ctx context.Context, store CredentialStore) (map[string]string, error) {
	list, err := store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, rec := range list {
		value, err := store.Get(ctx, rec.Name)
		if err != nil {
			return nil, fmt.Errorf("%q: %w", rec.Name, err)
		}
		out[rec.Name] = value
	}
	return out, nil
}
