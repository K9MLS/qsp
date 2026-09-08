package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/peering"
)

// Accepting a link to another QSP server.
//
// # What this writes, and what it does not
//
// One upstream block: a name, an address, a DMR ID, a password file and a
// callsign. That is the entire configuration on this side, and the far end has
// none at all.
//
// It writes **no bridge**. The OpenBridge path creates one because a link with
// nothing routing to it opens, authenticates and carries silence — true there,
// and false here: a linked QSP server is a peer, so repeat reaches it directly
// (ADR-0051). A bridge joins endpoints and an endpoint carries a timeslot,
// which is how the OpenBridge accept form came to ask an operator a question
// the protocol had already answered.
//
// It writes **no listen address, no network ID, and no talkgroup lists**. There
// is nothing to bind because this side dials; everything crosses and each
// server's own access lists decide what it keeps (ADR-0052 rule 1).
//
// # There is no reciprocal
//
// An OpenBridge peering is agreed in two halves because both ends must be
// configured. A link is a peer registration: the offering side already wrote
// the password and the access-list entry when it made the invitation, so
// accepting one ends the exchange. `Invitation.Reply` exists to recognise the
// end of a two-legged exchange, and one leg cannot loop.
func (s *Server) acceptLink(w http.ResponseWriter, r *http.Request, req acceptRequest) {
	inv, err := peering.DecodeLink(req.Token)
	if err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	// **The password always has to be typed here.** The offering side is a
	// different machine — it listens and this one dials — so there is no
	// outstanding offer on this instance to look the secret up from, and no
	// held-passphrase store is consulted. The empty-box dead end that stopped
	// the first OpenBridge peering cannot arise: there is one box and it is
	// never optional.
	password := strings.TrimSpace(req.Passphrase)
	if password == "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "this invitation needs the password that came with it, sent separately " +
				"from the token",
		})
		return
	}
	if err := inv.Accept(password, time.Now().UTC()); err != nil {
		s.recordPeering(r, audit.ActionPeeringAccepted, inv.Callsign, inv.Address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(inv.Callsign))
	}
	// Checked here as well as in Validate, because the password file is written
	// before the configuration is saved and the name becomes that file's path.
	if err := config.ValidUpstreamName(name); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	cfg := s.opts.Config.Current()
	before := cfg

	for _, u := range cfg.DMR.Upstreams {
		if strings.EqualFold(u.Name, name) {
			writeJSON(w, s.log, http.StatusConflict,
				map[string]string{"error": fmt.Sprintf("a link called %q already exists", name)})
			return
		}
	}

	// **The ID was allocated by the far end and has to be free at this end
	// too.** It is the ID this server presents when it dials, so a collision
	// here is this server holding one ID for two stations — and the failure is
	// silent on both sides.
	if why := idAlreadyMeansSomethingElse(cfg, inv.RepeaterID); why != "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": why + " — ask the other operator for an invitation with a different ID",
		})
		return
	}

	// **Everything that can refuse this link refuses it above this line.** The
	// OpenBridge path wrote its passphrase file before it had looked at the
	// address, so a refusal afterwards left a .pass file for a link that was
	// never created.
	if strings.TrimSpace(cfg.DMR.Identity.Callsign) == "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{
			"error": "this server has no callsign, and a link announces one to the far end; " +
				"set dmr.identity.callsign in Administration first",
		})
		return
	}

	path, err := s.writePassphrase(cfg, name, password)
	if err != nil {
		writeJSON(w, s.log, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	cfg = linkConfig(cfg, name, inv, path)

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	summary := fmt.Sprintf("link to %s (%s)", inv.Callsign, inv.Network)

	version, err := s.opts.Config.Save(r.Context(), cfg, author, summary)
	if err != nil {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			s.log.Warn("could not remove the password for a link that was not written",
				"path", path, "error", rmErr)
		}
		s.recordPeering(r, audit.ActionPeeringAccepted, inv.Callsign, inv.Address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	s.recordPeering(r, audit.ActionPeeringAccepted, inv.Callsign, inv.Address, audit.OutcomeSuccess)

	writeJSON(w, s.log, http.StatusOK, acceptResponse{
		Version:  version.Number,
		Callsign: inv.Callsign,
		// **The far end's announced display name**, which is what both consoles
		// head this link with (ADR-0052 rule 2, as amended). The local name is
		// what this server calls its own configuration block and is not the
		// same thing.
		Network: inv.Network,
		// No reciprocal, and nothing further to send.
		Complete:     true,
		NeedsRestart: config.NeedsRestart(before, cfg),
	})
}

// linkConfig returns cfg with a link to another QSP server added.
//
// **Separated from the handler so that a test can assert on what is written**
// rather than on a struct the test built itself. The first version of that test
// constructed its own upstream and checked it had no bridge, which is a test
// that cannot fail — the fourth time this project has written one.
func linkConfig(cfg config.Config, name string, inv peering.LinkInvitation, passwordPath string) config.Config {
	cfg.DMR.Upstreams = append(cfg.DMR.Upstreams, config.Upstream{
		Name:         name,
		Protocol:     config.UpstreamQSP,
		Enabled:      true,
		Address:      inv.Address,
		RepeaterID:   inv.RepeaterID,
		PasswordFile: passwordPath,
		Identity: &config.UpstreamIdentity{
			Callsign: strings.TrimSpace(cfg.DMR.Identity.Callsign),
		},
	})
	return cfg
}
