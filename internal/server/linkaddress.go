package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/peering"
)

// Editing a link's far-end address.
//
// # Why this exists
//
// **One wrong character meant removing the link and agreeing it again.** The
// console could create a link and remove one and could not change the single
// field most likely to be wrong: the address the far end is reached at. On
// 2026-09-08 an offer proposed the OpenBridge port for a QSP link, the accept
// form wrote it, and the only route back was Remove, a fresh invitation, a
// fresh password and a fresh access-list entry on the other server — for a
// four-character mistake.
//
// It is the same shape as the IPSC gap 0265 closed and the same rule: every
// operation must be completable from the console, without editing a
// configuration file on the server.
//
// # Why only the address
//
// The other fields are not corrections. A link's name is a file path, its DMR
// ID is what the far end's access list allows, and its password was agreed with
// somebody else — changing any of those is a different peering, and doing it
// under the word "edit" would look like a correction and behave like a new
// agreement. The address is the one field this side owns alone.

// addressRequest is a new far-end address for one link.
type addressRequest struct {
	// Address is where the far end is reached, as host:port.
	Address string `json:"address"`
}

// addressResponse reports what was written.
type addressResponse struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	// NeedsRestart names settings the running service has not picked up. An
	// upstream is built once at startup, so this always has something in it —
	// and saying so is the difference between a link an operator waits on and
	// one they restart.
	NeedsRestart []string `json:"needs_restart,omitempty"`
	Version      int64    `json:"version"`
}

// handleLinkAddress changes where one link reaches the far end.
func (s *Server) handleLinkAddress(w http.ResponseWriter, r *http.Request) {
	if s.opts.Config == nil {
		writeJSON(w, s.log, http.StatusServiceUnavailable,
			map[string]string{"error": "this instance cannot be configured from here"})
		return
	}

	name := strings.TrimSpace(r.PathValue("name"))
	if name == "" {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": "which link?"})
		return
	}

	var req addressRequest
	if !decodeJSON(w, s.log, r, &req) {
		return
	}

	address := strings.TrimSpace(req.Address)
	if err := usableFarEnd(address); err != nil {
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	before := s.opts.Config.Current()
	cfg := before

	found := false
	for i := range cfg.DMR.Upstreams {
		if !strings.EqualFold(cfg.DMR.Upstreams[i].Name, name) {
			continue
		}
		found = true
		cfg.DMR.Upstreams[i].Address = address
	}
	if !found {
		// A link that dialled in is in nobody's configuration here, so there is
		// no address on this side to change — and saying "no such link" would
		// be wrong about a link the operator can see on the page.
		writeJSON(w, s.log, http.StatusNotFound, map[string]string{
			"error": fmt.Sprintf("no link called %q is configured here; a link that dialled in "+
				"has no address on this side, and its far end is the one that holds it", name),
		})
		return
	}

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	version, err := s.opts.Config.Save(r.Context(), cfg, author,
		fmt.Sprintf("far-end address for the link %q", name))
	if err != nil {
		s.recordPeering(r, audit.ActionPeeringAccepted, name, address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.recordPeering(r, audit.ActionPeeringAccepted, name, address, audit.OutcomeSuccess)

	writeJSON(w, s.log, http.StatusOK, addressResponse{
		Name:         name,
		Address:      address,
		NeedsRestart: config.NeedsRestart(before, cfg),
		Version:      version.Number,
	})
}

// usableFarEnd refuses the three addresses that produce a link reporting itself
// healthy while carrying nothing.
//
// Each has been written to a live configuration in this project: a scheme on
// the front, because every other address an operator types all day has one; a
// bind address, which tells the far end to send to every interface on their own
// machine; and anything that is not host:port.
func usableFarEnd(address string) error {
	switch {
	case address == "":
		return fmt.Errorf("an address is where the far end is reached, as host:port")
	case strings.Contains(address, "://"):
		return peering.ErrSchemeInAddress
	case strings.HasPrefix(address, "0.0.0.0:"), strings.HasPrefix(address, "[::]:"):
		return peering.ErrBindAddress
	}
	if _, _, err := net.SplitHostPort(address); err != nil {
		return fmt.Errorf("%q is not host:port", address)
	}
	return nil
}
