package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/peering"
)

// Member credentials.
//
// **A member is removed by deleting one file**, rather than by changing the
// password every other member is using. ADR-0035 decided that; this is where an
// administrator does it, because a feature that requires SSH to use is one that
// gets used once and then avoided.

// credentialResponse reports what happened.
type credentialResponse struct {
	// Peer is the radio ID the credential belongs to.
	Peer uint32 `json:"peer"`
	// Password is shown exactly once and is never readable again.
	//
	// It is written to a file at mode 0600 and the configuration records only
	// the directory, for the reason ADR-0012 gives: this document is versioned,
	// exportable and diffable, so a secret in it is a secret in the version
	// history, in every backup, and on screen in a diff.
	Password string `json:"password,omitempty"`
	// Issued reports whether a password now exists for this peer.
	Issued bool `json:"issued"`
	// Reason explains a refusal.
	Reason string `json:"reason,omitempty"`
}

// handleIssueCredential generates a password for one peer.
func (s *Server) handleIssueCredential(w http.ResponseWriter, r *http.Request) {
	dir, peer, ok := s.credentialTarget(w, r)
	if !ok {
		return
	}

	password, err := peering.NewMemberPassword()
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot generate a password"})
		return
	}

	// **The directory before the file**, at 0700. A world-readable directory of
	// member credentials is worse than one shared secret, and this must not be
	// the change that introduces it.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot create the password directory: " + err.Error()})
		return
	}
	path := filepath.Join(dir, strconv.FormatUint(uint64(peer), 10))
	if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot write the password: " + err.Error()})
		return
	}

	s.recordCredential(r, "peer.credential.issued", peer, audit.OutcomeSuccess)

	writeJSON(w, s.log, http.StatusOK, credentialResponse{
		Peer:     peer,
		Password: password,
		Issued:   true,
	})
}

// handleRevokeCredential deletes a peer's password.
//
// **Deleting the file does not disconnect the peer.** A session already
// established survives until it times out or the service restarts; this stops
// the next login. An administrator removing somebody urgently needs the
// registration access list as well, and the response says so rather than
// letting them believe otherwise.
func (s *Server) handleRevokeCredential(w http.ResponseWriter, r *http.Request) {
	dir, peer, ok := s.credentialTarget(w, r)
	if !ok {
		return
	}

	path := filepath.Join(dir, strconv.FormatUint(uint64(peer), 10))
	err := os.Remove(path)
	switch {
	case err == nil:
		s.recordCredential(r, "peer.credential.revoked", peer, audit.OutcomeSuccess)
		writeJSON(w, s.log, http.StatusOK, credentialResponse{
			Peer: peer,
			Reason: "This peer now uses the shared password again. It stays connected " +
				"until its session times out; to remove it from the network now, add its " +
				"ID to the registration list on the access control page.",
		})
	case os.IsNotExist(err):
		// Not an error. The peer was already using the shared password, and an
		// administrator asking twice should be told the state rather than
		// shown a failure.
		writeJSON(w, s.log, http.StatusOK, credentialResponse{
			Peer:   peer,
			Reason: "This peer had no password of its own; it was already using the shared one.",
		})
	default:
		s.recordCredential(r, "peer.credential.revoked", peer, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot remove the password: " + err.Error()})
	}
}

// credentialTarget resolves the directory and the peer, answering the caller on
// failure.
func (s *Server) credentialTarget(w http.ResponseWriter, r *http.Request) (string, uint32, bool) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot be configured from here"})
		return "", 0, false
	}

	dir := strings.TrimSpace(s.opts.Config.Current().DMR.PeerPasswords)
	if dir == "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "this network uses one shared password; set dmr.peer_passwords to a " +
				"directory before issuing a member their own",
		})
		return "", 0, false
	}

	raw := r.PathValue("id")
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || id == 0 {
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": fmt.Sprintf("%q is not a radio ID", raw)})
		return "", 0, false
	}
	return dir, uint32(id), true
}

// recordCredential writes the audit event a club asks about afterwards: who
// removed whom, and when.
func (s *Server) recordCredential(r *http.Request, action string, peer uint32, outcome audit.Outcome) {
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
		Subject:    strconv.FormatUint(uint64(peer), 10),
		Outcome:    outcome,
		SourceIP:   clientIP(r, s.opts.BehindProxy),
	}); err != nil {
		s.log.Warn("cannot record a credential change in the audit trail", "error", err)
	}
}
