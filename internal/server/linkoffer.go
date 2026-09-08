package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/peering"
)

// Offering a link to another QSP server.
//
// # Why the listening side offers
//
// An OpenBridge peering is symmetric, so either side can open it. A QSP link is
// a peer registration (ADR-0051): one side dials and the other listens, and
// only the dialling side is configured at all. The listening side is therefore
// the only one with anything to allocate — a DMR ID in its registration list,
// and a password against that ID — and the dialling side has nothing the
// listener needs to be told.
//
// # Why this is one act rather than three
//
// On 2026-09-08 a third instance was linked to two servers by hand. It
// connected to the first and was refused by the second, which had a permit list
// naming one ID. The dialling end could say only *refused by the far end
// (MSTNAK); check the password and the repeater ID*, because MSTNAK carries no
// reason, and the answer was in the far end's journal.
//
// A password issued without a list entry, or a list entry without a password,
// produces exactly that. So the offer writes both or neither, and the token it
// returns is the evidence that both were written.

// offerLinkRequest is what the console sends.
type offerLinkRequest struct {
	// Address is where the far end dials, as host:port. Empty means this
	// server's own guess, which is said to be a guess on the page.
	Address string `json:"address"`
	// RepeaterID is the DMR ID the far end will present.
	RepeaterID uint32 `json:"repeater_id"`
	// Callsign identifies this server to the other operator.
	Callsign string `json:"callsign"`
}

// offerLinkResponse carries the invitation and the password that goes with it.
type offerLinkResponse struct {
	// Token is the invitation, sent by email or any other channel.
	Token string `json:"token"`
	// Password is shown exactly once, for the reason ADR-0012 gives, and is
	// sent to the other operator separately from the token. The token travels
	// in a mail thread that may be forwarded; a token without its password is
	// not a credential.
	Password string `json:"password"`
	// Fingerprint lets the far end's console say *that password does not match
	// this invitation* at the moment of pasting, rather than as silence on a
	// link that reports itself configured.
	Fingerprint string `json:"fingerprint"`
	// Allowed reports that the registration list now names this ID.
	Allowed bool `json:"allowed"`
	// PasswordDirectory reports where per-peer passwords are now kept, when
	// this offer was what settled it.
	PasswordDirectory string `json:"password_directory,omitempty"`
	// NeedsRestart says the running service has not picked this up yet.
	NeedsRestart []string `json:"needs_restart,omitempty"`
	// Version is the configuration version this wrote.
	Version int64 `json:"version"`
}

// handleOfferLink allocates an ID and a password for a link to another QSP
// server, and returns the invitation.
func (s *Server) handleOfferLink(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	var req offerLinkRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}

	before := s.opts.Config.Current()
	cfg := before

	if req.RepeaterID == 0 {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "a link needs the DMR ID the far end will present; it is what this " +
				"server's registration list will allow and what identifies the link in every frame",
		})
		return
	}
	if why := idAlreadyMeansSomethingElse(cfg, req.RepeaterID); why != "" {
		// **One ID cannot mean two stations.** config.Validate refuses this
		// too, but only when the far end's configuration is written — which is
		// on the other operator's machine, days later, with nothing here to
		// point at.
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": why})
		return
	}

	dir, changedDir, err := peerPasswordDirectory(&cfg)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := allowRegistration(&cfg, req.RepeaterID); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	password, err := peering.NewPassphrase()
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError,
			map[string]string{"error": "cannot generate a password"})
		return
	}

	callsign := strings.ToUpper(strings.TrimSpace(req.Callsign))
	if callsign == "" {
		callsign = linkCallsign(cfg)
	}
	address := strings.TrimSpace(req.Address)
	if address == "" {
		address = defaultLinkAddress(cfg)
	}

	inv := peering.LinkInvitation{
		Network:     strings.TrimSpace(cfg.DMR.Join.NetworkName),
		Callsign:    callsign,
		Address:     address,
		RepeaterID:  req.RepeaterID,
		Fingerprint: peering.FingerprintOf(password),
		Issued:      time.Now().UTC(),
	}

	// **Encoded before anything is written.** An invitation refused for a
	// missing field should not leave a password on disk and an ID in the
	// access list for a link that was never offered.
	token, err := peering.EncodeLink(inv)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": err.Error() +
				" — fill in the address the other server should dial, as host:port",
		})
		return
	}

	// **The password file before the configuration**, and removed again if the
	// configuration will not save. An ID permitted with no password behind it
	// is refused at login with "no password is configured for repeater ID",
	// which reads like the far end's mistake and is this one.
	path, err := writePeerPassword(dir, req.RepeaterID, password)
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	summary := fmt.Sprintf("link offered to %s, DMR ID %d", callsign, req.RepeaterID)

	version, err := s.opts.Config.Save(r.Context(), cfg, author, summary)
	if err != nil {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			s.log.Warn("could not remove the password for a link that was not offered",
				"path", path, "error", rmErr)
		}
		s.recordPeering(r, audit.ActionPeeringOffered, callsign, address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.recordPeering(r, audit.ActionPeeringOffered, callsign, address, audit.OutcomeSuccess)

	resp := offerLinkResponse{
		Token:        token,
		Password:     password,
		Fingerprint:  inv.Fingerprint,
		Allowed:      true,
		NeedsRestart: config.NeedsRestart(before, cfg),
		Version:      version.Number,
	}
	if changedDir {
		resp.PasswordDirectory = dir
	}
	writeJSON(w, s.log, http.StatusOK, resp)
}

// idAlreadyMeansSomethingElse reports why an ID cannot be given to a new link.
//
// The three collisions that produce silence rather than an error, all of which
// this project has met: a station's own master ID, which that station refuses
// and then retries forever without saying why; another link's ID, where the
// second registration replaces the first at the far end and one link goes quiet
// while both report healthy; and a locally configured peer's ID, which makes a
// private call routable to two places.
func idAlreadyMeansSomethingElse(cfg config.Config, id uint32) string {
	if cfg.IPSC.Enabled && id == cfg.IPSC.MasterID {
		return fmt.Sprintf("%d is this server's IPSC master ID; a station refuses to register "+
			"with a master carrying its own ID, and retries silently rather than reporting it", id)
	}
	for _, u := range cfg.DMR.Upstreams {
		if u.RepeaterID == id {
			return fmt.Sprintf("%d is already used by the link %q; the far end registers by ID, "+
				"so the second link would replace the first and one of them would go quiet",
				id, u.Name)
		}
	}
	for _, a := range cfg.DMR.Subscription.Static {
		if a.Peer == id {
			return fmt.Sprintf("%d is already a peer on this network; one ID cannot mean two "+
				"stations, and a private call to it would be routable to two places", id)
		}
	}
	return ""
}

// peerPasswordDirectory resolves where a per-peer password is written, settling
// it in the configuration when it has never been set.
//
// **ADR-0035 exists so that a member can be removed without changing
// everybody's password**, and a link authenticating with the shared hotspot
// password gives that up — which is the debt the 2026-09-08 handover records
// against the first QSP link. Requiring an operator to set the directory by
// hand first would leave the shared password as the path of least resistance,
// so the offer settles it: beside the shared password file, at 0700, and the
// response says where.
func peerPasswordDirectory(cfg *config.Config) (string, bool, error) {
	if dir := strings.TrimSpace(cfg.DMR.PeerPasswords); dir != "" {
		return dir, false, nil
	}

	shared := strings.TrimSpace(cfg.DMR.PasswordFile)
	if shared == "" {
		return "", false, fmt.Errorf("this server has no peer password file, so there is " +
			"nowhere to keep a password for this link; set dmr.password_file or " +
			"dmr.peer_passwords first")
	}
	dir := filepath.Join(filepath.Dir(shared), "peers")
	cfg.DMR.PeerPasswords = dir
	return dir, true, nil
}

// allowRegistration makes sure the registration list names this ID.
//
// # Why a denied ID is refused rather than fixed
//
// A permit list is a list of who may register, so adding an entry is exactly
// what the operator asked for. A deny list is a list of who may not, and the
// entries may be ranges: removing one ID from "3100-3199" means splitting a
// range in a live access list, which is the kind of edit that has taken a
// network down for an hour in this project. Refusing and saying which entry is
// in the way leaves that decision with the operator.
func allowRegistration(cfg *config.Config, id uint32) error {
	// **An absent access block is not an empty one.** `Access` is a pointer and
	// is nil until somebody configures a list; each list then permits
	// everything, so that upgrading cannot silently disconnect a running club.
	// There is nothing to add in that case, and dereferencing it would panic
	// the offer on exactly the servers that have never restricted anything.
	if cfg.DMR.Access == nil {
		return nil
	}

	acl := cfg.DMR.Access.Registration
	list, err := access.Parse("dmr.access.registration", access.Registration,
		access.Mode(acl.Mode), acl.IDs)
	if err != nil {
		return fmt.Errorf("this server's registration list cannot be read, so a link "+
			"cannot be added to it: %w", err)
	}
	if list.Allows(id) {
		return nil
	}
	if list.Mode() == access.ModeDeny {
		return fmt.Errorf("%d is refused by this server's registration list; remove it from "+
			"the denied entries on the access control page, or offer the link a different ID", id)
	}
	cfg.DMR.Access.Registration.IDs = append(acl.IDs, strconv.FormatUint(uint64(id), 10))
	return nil
}

// writePeerPassword stores one peer's password, named by its ID.
//
// The directory at 0700 before the file at 0600: a world-readable directory of
// member credentials is worse than one shared secret, and this must not be the
// change that introduces it.
func writePeerPassword(dir string, id uint32, password string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create the password directory: %w", err)
	}
	path := filepath.Join(dir, strconv.FormatUint(uint64(id), 10))
	if err := os.WriteFile(path, []byte(password), 0o600); err != nil {
		return "", fmt.Errorf("cannot write the password: %w", err)
	}
	return path, nil
}
