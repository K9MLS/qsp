package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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
	files, err := s.readPasswordFiles(cfg)
	if err != nil {
		s.recordBackup(r, audit.ActionConfigFullExported, audit.OutcomeFailure)
		status, message := http.StatusInternalServerError, "cannot read a password file: "+err.Error()
		if errors.Is(err, errOutsideCredentialDir) {
			// The configuration is at fault and the operator can mend it,
			// which is what a 400 says and a 500 does not.
			status = http.StatusBadRequest
			message = "no backup was made: " + err.Error() + ". Move that file into the " +
				"directory and change the setting that names it, then take the backup again"
		}
		writeJSON(w, s.log, status, map[string]string{"error": message})
		return
	}
	full := config.NewFullBackup(cfg, values, buildinfo.Version, time.Now())
	full.PasswordFiles = files

	var out bytes.Buffer
	if err := config.WriteFullBackup(&out, full, body.Passphrase); err != nil {
		s.recordBackup(r, audit.ActionConfigFullExported, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": err.Error()})
		return
	}
	// **A backup that cannot be restored is refused when it is made**, which
	// is the only moment the operator can still do something about it. See
	// fullRestoreBodyLimit.
	if size := int64(base64.StdEncoding.EncodedLen(out.Len())); size > fullRestoreBodyLimit-fullRestoreEnvelope {
		s.recordBackup(r, audit.ActionConfigFullExported, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("this server's full backup would be %d KiB as a restore "+
				"sends it, and a restore reads %d KiB; it was not written, because a "+
				"backup that cannot be restored is worse than none",
				size>>10, fullRestoreBodyLimit>>10),
		})
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
// both. The body is bounded by fullRestoreBodyLimit.
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
	if !decodeJSONWithin(w, s.log, r, &req, fullRestoreBodyLimit) {
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

	before := s.opts.Config.Current()
	cfg := full.Backup.Config
	keepMachineLocal(&cfg, before)

	// Worked out before the confirmation, so that what the operator agrees to
	// is what will happen.
	absent := absentPasswordFiles(cfg, full.PasswordFiles)
	outside := s.outsidePasswordFiles(full.PasswordFiles)

	if !req.Confirm {
		// Said before it happens, in the operator's terms, and naming what
		// will come back rather than summarising it. The identity warning is
		// the shareable restore's, unchanged, because the hazard is the same
		// one.
		//
		// **It used to end "Links and members will work immediately", whatever
		// the file held.** The password files were never in it, so that was
		// false for every link the console had made. It now says what comes
		// back and names what does not.
		summary := fmt.Sprintf("This replaces every setting on this server "+
			"with the full backup taken on %s by QSP %s, and restores %d "+
			"credentials and %d password files.",
			full.Backup.ExportedAt.Format("2 January 2006"),
			full.Backup.QSPVersion, len(full.Secrets), len(full.PasswordFiles))
		if len(absent) == 0 {
			summary += " Every password file the configuration names comes back with it."
		} else {
			summary += fmt.Sprintf(" %d password files the configuration names are neither "+
				"in this backup nor on this machine; the links and members that need them "+
				"will be refused until they are reissued.", len(absent))
		}
		if len(outside) > 0 {
			summary += fmt.Sprintf(" %d of the password files will not be written: the backup "+
				"places them outside %s, where this server keeps its own. The links and "+
				"members that need them will be refused until they are reissued.",
				len(outside), s.opts.CredentialDir)
		}
		writeJSON(w, s.log, http.StatusPreconditionRequired, map[string]any{
			"error":   "not confirmed",
			"summary": summary,
			"identity": "The backup carries a server identifier. Taking it makes " +
				"this machine a replacement for the one that made the backup — if " +
				"that server is still running, two servers will claim one identity " +
				"and neither will say so. QSP cannot see the other machine and " +
				"will not pretend to check.",
			"credentials": "These credentials belong to links whose far ends have " +
				"not been asked. Restoring them revives the passwords the other " +
				"operators issued, which is why this is a replacement rather than " +
				"a second server.",
			"kept": "This machine keeps its own console address and its own " +
				"database; neither is taken from the backup.",
			"credential_names":       full.SecretNames(),
			"password_files":         full.PasswordFilePaths(),
			"missing_password_files": absent,
			"refused_password_files": outside,
			"exported_at":            full.Backup.ExportedAt,
			"qsp_version":            full.Backup.QSPVersion,
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

	// The password files in the same position, for the same reason. A file
	// that cannot be written does not stop the restore: it is one link
	// awaiting a credential, which the answer names, where stopping here
	// would leave the credentials above restored and nothing else.
	written, failed := s.writePasswordFiles(cfg, full.PasswordFiles)
	for _, f := range failed {
		s.log.Warn("cannot restore a password file", "path", f.Path, "error", f.Error)
	}

	version, late, err := s.save(r.Context(), cfg, actor,
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
	// exactly this positive statement. What did not come back is named beside
	// it, because a positive statement about part of a restore reads as one
	// about all of it.
	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"version":                version.Number,
		"exported":               full.Backup.ExportedAt,
		"credentials":            restored,
		"password_files":         written,
		"password_files_failed":  failed,
		"missing_password_files": absentPasswordFiles(cfg, nil),
		"needs_restart":          notApplied(config.NeedsRestart(before, cfg), late),
	})
}

// fullRestoreBodyLimit is the largest full restore request read, and so the
// largest full backup handleFullBackup will write.
//
// A full backup is the configuration, which a save bounds at a megabyte; every
// stored credential, each bounded by the 64 KiB its own endpoint reads; and
// every password file, each bounded by passwordFileLimit. It carries no call
// history and no audit trail. Encrypted and then base64, it travels at four
// thirds of that. Sixteen megabytes is the configuration at its limit and well
// over a hundred credentials at theirs — far past any server this project has
// seen, and still small enough to hold in memory three times over, which is
// what decoding, decrypting and parsing it costs.
//
// **The backup side checks against the same number**, so the limit cannot
// produce a file that is made in good faith and refused on the day it is
// needed.
const fullRestoreBodyLimit = 16 << 20

// fullRestoreEnvelope is the room left for the passphrase and the JSON around
// the document in a restore request.
const fullRestoreEnvelope = 8 << 10

// passwordFileLimit is the largest password file a backup will carry. A
// password is tens of bytes; a file this large named as one is a mistake in
// the configuration, and copying it into a backup would not make it a
// credential.
const passwordFileLimit = 64 << 10

// passwordFilePaths lists the password files a configuration names, and the
// directory holding each member's own.
//
// The same four references config.MissingCredentials reports for the
// shareable export, which names what it cannot carry; this is the list of
// what the full backup does carry.
func passwordFilePaths(cfg config.Config) (files []string, memberDir string) {
	add := func(path string) {
		if path = strings.TrimSpace(path); path != "" && !slices.Contains(files, path) {
			files = append(files, path)
		}
	}
	add(cfg.DMR.PasswordFile)
	for _, u := range cfg.DMR.Upstreams {
		add(u.PasswordFile)
		add(u.PassphraseFile)
	}
	slices.Sort(files)
	return files, strings.TrimSpace(cfg.DMR.PeerPasswords)
}

// memberPasswordFile reports whether path is a member's password in dir: a
// file directly inside it, named by a radio ID, which is the only thing QSP
// reads from there (ADR-0035).
func memberPasswordFile(dir, path string) bool {
	if dir == "" || filepath.Clean(filepath.Dir(path)) != filepath.Clean(dir) {
		return false
	}
	id, err := strconv.ParseUint(filepath.Base(path), 10, 32)
	return err == nil && id != 0
}

// readPasswordFiles reads every password file the configuration names.
//
// **A file that is not there is left out; a file that cannot be read fails the
// backup.** Absent is a fact about the server — a link whose password was
// never issued — and the restore reports it. Unreadable is allSecrets' case: a
// backup silently lacking a credential the server does have, which the
// operator cannot know about when they make it.
//
// **And a file outside this server's directory fails it too**, naming the
// file. A password-file setting is a path anybody signed in can save, and a
// backup that read whatever it named would be a way to carry any file the
// service can read off the machine. See credentialdir.go.
func (s *Server) readPasswordFiles(cfg config.Config) (map[string]string, error) {
	files, dir := passwordFilePaths(cfg)
	if dir != "" {
		// Before it is listed, not only before its files are read.
		if _, err := s.credentialPath(dir); err != nil {
			return nil, fmt.Errorf("the member password directory: %w", err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("the member password directory: %w", err)
		}
		for _, e := range entries {
			path := filepath.Join(dir, e.Name())
			if e.Type().IsRegular() && memberPasswordFile(dir, path) {
				files = append(files, path)
			}
		}
	}

	out := make(map[string]string, len(files))
	for _, path := range files {
		// Every file, the members' included: those are found by listing a
		// directory, and a link in there is a way out of it.
		if _, err := s.credentialPath(path); err != nil {
			return nil, err
		}
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("%q: %w", path, err)
		}
		if len(raw) > passwordFileLimit {
			return nil, fmt.Errorf("%q is %d KiB, which is not a password", path, len(raw)>>10)
		}
		out[path] = string(raw)
	}
	return out, nil
}

// passwordFileFailure is one password file a restore could not write.
type passwordFileFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// writePasswordFiles puts a backup's password files where the restored
// configuration names them.
//
// **Only a path the configuration names is written, and only inside this
// server's own directory.** The file says where each one goes, and the
// configuration that names the paths allowed is in the same file, so the
// first rule by itself let a backup carry anything to anywhere the service
// can write: it only had to name the place twice. The second rule is not the
// backup's to state. A path that fails either is reported and left alone.
//
// The directory at 0700 before the file at 0600, as everywhere else a
// credential is written. The mode is set again afterwards because WriteFile
// keeps the mode of a file that already exists, and QSP refuses to use a
// password file anybody else can read.
func (s *Server) writePasswordFiles(cfg config.Config, files map[string]string) (written []string, failed []passwordFileFailure) {
	named, dir := passwordFilePaths(cfg)
	written = []string{}

	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	slices.Sort(paths)

	for _, path := range paths {
		if !slices.Contains(named, path) && !memberPasswordFile(dir, path) {
			failed = append(failed, passwordFileFailure{Path: path,
				Error: "the restored configuration does not name this file, so it was not written"})
			continue
		}
		if _, err := s.credentialPath(path); err != nil {
			failed = append(failed, passwordFileFailure{Path: path,
				Error: err.Error() + ", so it was not written"})
			continue
		}
		if err := writePasswordFile(path, files[path]); err != nil {
			failed = append(failed, passwordFileFailure{Path: path, Error: err.Error()})
			continue
		}
		written = append(written, path)
	}
	return written, failed
}

// outsidePasswordFiles lists the files of a backup that a restore will not
// write for being outside this server's directory, so the operator is told
// before agreeing and not after.
func (s *Server) outsidePasswordFiles(files map[string]string) []string {
	out := []string{}
	for path := range files {
		if _, err := s.credentialPath(path); err != nil {
			out = append(out, path)
		}
	}
	slices.Sort(out)
	return out
}

// writePasswordFile writes one credential, readable by nobody else.
func writePasswordFile(path, contents string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cannot create the password directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		return fmt.Errorf("cannot write the password file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("cannot restrict the password file: %w", err)
	}
	return nil
}

// absentPasswordFiles lists the password files the configuration names that
// are neither on this machine nor in carried.
//
// Each of these is a link or a member that will be refused, and the page says
// so by path rather than claiming the restore is complete. A member's own file
// cannot be listed: the configuration names the directory, not who has one.
func absentPasswordFiles(cfg config.Config, carried map[string]string) []string {
	named, _ := passwordFilePaths(cfg)
	out := []string{}
	for _, path := range named {
		if _, ok := carried[path]; ok {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			continue
		}
		out = append(out, path)
	}
	return out
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
