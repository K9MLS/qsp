package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
)

// Refusing a link that dialled in.
//
// # Why this exists, and why the reasoning that removed it was wrong
//
// 0284 took the Remove button off inbound links, because an inbound link is in
// nobody's configuration here — this server received a registration, not a
// document — so there was nothing on this side to delete and the button would
// have done nothing or matched something else by name.
//
// **That was true for about forty minutes.** 0288 made the offering side
// allocate: a DMR ID in the registration list, and a password against that ID.
// So this server does hold state for an inbound link, it is state this console
// created, and the rule is that anything a page creates it must be able to
// remove.
//
// The sharper version is ADR-0052 rule 1: a server decides what it accepts and
// nobody decides for it. Without this, the only way to stop accepting a link is
// to ask the other operator to remove their end, or to edit JSON on the server.
// A federation in which a server cannot refuse a neighbour is not one.
//
// # Why it is not called Remove
//
// There is no link here to delete. What this does is stop this server accepting
// the far end's registrations: it revokes the password that ID logs in with and
// makes the registration list refuse it. The far end's configuration is
// untouched and will keep dialling.
//
// # What it does not do
//
// **It does not disconnect a session already established.** That survives until
// it times out or QSP restarts, exactly as revoking a member's credential does,
// and the response says so rather than implying the link is gone the moment the
// button is clicked.

// refuseResponse reports what was actually withdrawn, and what was not.
type refuseResponse struct {
	// Peer is the DMR ID that will no longer be accepted.
	Peer uint32 `json:"peer"`
	// PasswordRevoked reports that a password existed for this ID alone and
	// has been deleted.
	//
	// **False is not a failure.** A link written before per-peer passwords
	// existed authenticates with the shared password, and there is nothing of
	// its own to revoke — which is a fact the operator needs, because the
	// shared password still lets it in.
	PasswordRevoked bool `json:"password_revoked"`
	// Refused reports that the registration list now refuses this ID.
	Refused bool `json:"refused"`
	// Reason explains anything this could not do.
	Reason string `json:"reason,omitempty"`
	// NeedsRestart names settings written that the running service has not
	// picked up.
	NeedsRestart []string `json:"needs_restart,omitempty"`
	// Version is the configuration version this wrote.
	Version int64 `json:"version"`
}

// handleRefuseInbound stops this server accepting registrations from one ID.
func (s *Server) handleRefuseInbound(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	raw := r.PathValue("id")
	id64, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || id64 == 0 {
		writeJSON(w, s.log, http.StatusBadRequest,
			map[string]string{"error": fmt.Sprintf("%q is not a DMR ID", raw)})
		return
	}
	id := uint32(id64)

	before := s.opts.Config.Current()
	cfg := before

	refused, why := refuseRegistration(&cfg, id)

	// **The configuration first, and the password only if it saves.** The
	// opposite order leaves an ID that can still register with a password that
	// has been deleted, which is refused at login as *no password is configured
	// for repeater ID* — a message that reads like the far end's mistake.
	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	version, err := s.opts.Config.Save(r.Context(), cfg,
		author, fmt.Sprintf("stopped accepting DMR ID %d", id))
	if err != nil {
		s.recordCredential(r, "peer.credential.revoked", id, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	revoked, revokeErr := revokePeerPassword(cfg, id)
	if revokeErr != nil {
		// Reported rather than fatal. The list entry is already saved, so the
		// ID is refused either way, and an operator told nothing about a
		// password left on disk cannot go and remove it.
		why = strings.TrimSpace(why + " The password file could not be removed: " +
			revokeErr.Error() + ".")
	}

	s.recordCredential(r, "peer.credential.revoked", id, audit.OutcomeSuccess)

	writeJSON(w, s.log, http.StatusOK, refuseResponse{
		Peer:            id,
		PasswordRevoked: revoked,
		Refused:         refused,
		Reason:          strings.TrimSpace(why),
		NeedsRestart:    config.NeedsRestart(before, cfg),
		Version:         version.Number,
	})
}

// refuseRegistration makes the registration list refuse one ID, and says what
// it could not do.
//
// The three shapes a list can be in, and each needs a different act:
//
//   - **No access block at all**, which is the default and permits everything.
//     One is created naming this ID as denied. Everybody else is unaffected,
//     which is what the absent block meant.
//   - **A permit list**, where the ID is there because something added it.
//     The entry is taken out. If it is covered by a range rather than named,
//     the range is left alone and the ID is denied instead, because splitting a
//     range in a live access list is the edit that took a club's network down
//     for an hour.
//   - **A deny list**, where the ID is added.
func refuseRegistration(cfg *config.Config, id uint32) (bool, string) {
	entry := strconv.FormatUint(uint64(id), 10)

	if cfg.DMR.Access == nil {
		cfg.DMR.Access = &config.Access{}
		cfg.DMR.Access.Registration = config.ACL{Mode: string(access.ModeDeny), IDs: []string{entry}}
		return true, "This server had no registration list and accepted everything; it now " +
			"refuses this ID and still accepts every other station."
	}

	acl := cfg.DMR.Access.Registration
	list, err := access.Parse("dmr.access.registration", access.Registration,
		access.Mode(acl.Mode), acl.IDs)
	if err != nil {
		return false, "This server's registration list cannot be read, so the ID was not " +
			"refused: " + err.Error()
	}

	if list.Mode() == access.ModePermit {
		kept := make([]string, 0, len(acl.IDs))
		for _, e := range acl.IDs {
			if strings.TrimSpace(e) != entry {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			// A permit list with no entries refuses every station, which
			// access.Parse rightly will not accept. Nobody meant to take the
			// whole network off the air by refusing one link.
			return false, "This ID is the only station this server permits, so removing it " +
				"would refuse everybody. Add the stations that should stay before refusing this one."
		}
		if len(kept) != len(acl.IDs) {
			cfg.DMR.Access.Registration.IDs = kept
			return true, ""
		}
		// Still allowed, so it is inside a range rather than named.
		return false, "This ID is permitted by a range in the registration list rather than " +
			"by an entry of its own. Splitting a range here is not something this page will " +
			"do to a live network — narrow the range on the access control page."
	}

	if list.Allows(id) {
		cfg.DMR.Access.Registration.IDs = append(acl.IDs, entry)
		return true, ""
	}
	return true, "This ID was already refused by the registration list."
}

// revokePeerPassword deletes the password belonging to one ID, if it has one.
//
// A missing file is not an error: a link written before the offer form existed
// authenticates with the shared password and has nothing of its own. The caller
// reports that, because the shared password still admits it.
func revokePeerPassword(cfg config.Config, id uint32) (bool, error) {
	dir := strings.TrimSpace(cfg.DMR.PeerPasswords)
	if dir == "" {
		return false, nil
	}
	err := os.Remove(filepath.Join(dir, strconv.FormatUint(uint64(id), 10)))
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, err
	}
}
