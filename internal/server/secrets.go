package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/secrets"
	"github.com/k9mls/qsp/internal/zello"
	"github.com/k9mls/qsp/internal/zellologon"
)

// Credentials an operator types into the console.
//
// # Why these endpoints exist separately from the configuration
//
// [ADR-0065] settles it: **all configuration is entered in the console,
// including secrets**, and a secret still lives outside the configuration
// document. Those are not in tension — the first is about where an operator
// types, the second about where the value is stored.
//
// The configuration already round-trips through `GET` and `POST /api/config`,
// which validates, records a version, writes and applies. A credential cannot
// travel that way: `configuration_versions` stores the full JSON document for
// every save so an operator can inspect, diff and roll back, so a password
// written into configuration would appear in every snapshot, every diff and
// every version the console shows, in plain text, with an author's name
// attached.
//
// So configuration refers to a secret by name and these endpoints carry the
// value.
//
// # A value goes in and never comes out
//
// There is no endpoint that returns a secret. **A page that displays a
// password leaks it to whoever is looking at the screen**, and an operator who
// needs the value has it elsewhere or should replace it. The listing answers
// the question a console actually asks — is this configured, and when did it
// change — and answers it without decrypting anything, so it cannot leak a
// value even if the page rendering it is wrong.

// CredentialStore is the credential store the server needs.
//
// **An interface for the same reason ConfigManager and Auth are**: the handler
// that confirms before replacing every setting on a server has to be testable
// without a database, and it was not — a break that skipped the confirmation
// entirely passed, because the only test of that path stopped at the
// no-store check.
type CredentialStore interface {
	List(context.Context) ([]secrets.Record, error)
	Get(context.Context, string) (string, error)
	Set(ctx context.Context, name, value, author string) error
	Delete(ctx context.Context, name string) error
	Has(ctx context.Context, name string) (bool, error)
}

// secretRecord is what the console is told about a stored credential.
//
// **No value field, and that is the guarantee rather than a convention**: a
// type with nowhere to put a secret cannot be made to carry one by a later
// change to a handler.
type secretRecord struct {
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
	UpdatedBy string    `json:"updated_by"`
}

// secretsResponse lists what is stored.
type secretsResponse struct {
	Secrets []secretRecord `json:"secrets"`
}

// setSecretRequest carries a value in.
type setSecretRequest struct {
	Value string `json:"value"`
}

// handleSecrets lists stored credentials by name, never by value.
func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) {
	store, ok := s.secretStore(w)
	if !ok {
		return
	}

	list, err := store.List(r.Context())
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot list the stored credentials: " + err.Error()})
		return
	}

	out := make([]secretRecord, 0, len(list))
	for _, rec := range list {
		out = append(out, secretRecord{
			Name:      rec.Name,
			UpdatedAt: rec.UpdatedAt,
			UpdatedBy: rec.UpdatedBy,
		})
	}
	writeJSON(w, s.log, http.StatusOK, secretsResponse{Secrets: out})
}

// handleSetSecret stores a credential under a name.
//
// **The name comes from the path and the value from the body**, so a value
// never appears in a URL: a query string is logged by every proxy in the way,
// written into an access log, and kept in a browser's history.
func (s *Server) handleSetSecret(w http.ResponseWriter, r *http.Request) {
	store, ok := s.secretStore(w)
	if !ok {
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": "a credential needs a name"})
		return
	}

	var body setSecretRequest
	if !decodeJSON(w, s.log, r, &body) {
		return
	}

	// **A Zello key that cannot sign is refused as it is entered**, with the
	// parser's own reason — a PEM header one dash short looks entirely
	// normal and is unreadable everywhere. Stored, it would fail only when
	// qsp-zello next connected, as a logon refusal naming nothing.
	if name == zellologon.PrivateKeyName {
		if err := zello.CheckPrivateKey(body.Value); err != nil {
			writeJSON(w, s.log, http.StatusBadRequest,
				map[string]string{"error": "that is not a usable private key: " + err.Error()})
			return
		}
	}

	actor := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		actor = sess.Username
	}

	if err := store.Set(r.Context(), name, body.Value, actor); err != nil {
		s.recordSecret(r, "secret.set", name, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot store the credential: " + err.Error()})
		return
	}

	s.recordSecret(r, "secret.set", name, audit.OutcomeSuccess)
	writeJSON(w, s.log, http.StatusOK, map[string]string{"name": name})
}

// handleRemoveSecret deletes a credential.
//
// **Anything a page creates it must be able to remove.** An operator whose
// first attempt at a Zello credential went wrong should not be left with a
// stored secret they can only delete by opening the database.
func (s *Server) handleRemoveSecret(w http.ResponseWriter, r *http.Request) {
	store, ok := s.secretStore(w)
	if !ok {
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": "a credential needs a name"})
		return
	}

	if err := store.Delete(r.Context(), name); err != nil {
		s.recordSecret(r, "secret.removed", name, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot remove the credential: " + err.Error()})
		return
	}

	s.recordSecret(r, "secret.removed", name, audit.OutcomeSuccess)
	w.WriteHeader(http.StatusNoContent)
}

// secretStore returns the store, or writes the reason there isn't one.
//
// **Not a nil check that silently does nothing.** A server started without a
// database has no secret store, and an operator typing a credential into a
// page that accepts it and stores nothing would believe the link was
// configured — which is the failure the whole project keeps writing records
// about.
func (s *Server) secretStore(w http.ResponseWriter) (CredentialStore, bool) {
	if s.opts.Secrets == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable, map[string]string{
			"error": "this instance cannot store credentials; it was started " +
				"without a database, so there is nowhere to keep them",
		})
		return nil, false
	}
	return s.opts.Secrets, true
}

// recordSecret writes the audit event a club asks about afterwards: who
// changed which credential, and when.
//
// **The name and never the value.** An audit trail is read, exported and kept
// far longer than a session, and a password in it is a password in every copy
// of it.
func (s *Server) recordSecret(r *http.Request, action, name string, outcome audit.Outcome) {
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
		Subject:    name,
		Outcome:    outcome,
		SourceIP:   clientIP(r, s.opts.BehindProxy),
	}); err != nil {
		s.log.Warn("cannot record a credential change in the audit trail", "error", err)
	}
}

// SecretMissing reports whether a configured credential has no stored value,
// for a health check.
//
// It exists because the two states an operator confuses are "configured and
// wrong" and "configured and absent", and only the second is fixable by
// typing. A store that cannot decrypt a secret reports it as present, not
// missing, so that a key mismatch is not mistaken for a credential nobody
// entered.
func SecretMissing(store CredentialStore, r *http.Request, name string) (bool, error) {
	if store == nil {
		return false, errors.New("server: no credential store")
	}
	has, err := store.Has(r.Context(), name)
	return !has, err
}
