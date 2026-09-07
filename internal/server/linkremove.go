package server

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
)

// Removing a link.
//
// # Why this exists
//
// **The Links page could create a link and not remove one.** Accepting a
// peering writes an upstream, a bridge and a passphrase file, and nothing in
// the API or the console could undo any of it — so an operator whose first
// attempt went wrong was left with a broken link on the page, permanently,
// unless they edited JSON by hand on the server. That is exactly the thing the
// page exists to avoid.
//
// It was found the way these things are always found here: somebody used it.
//
// # What comes out
//
// The three pieces accepting a peering put in, and only those:
//
//   - the upstream named in the request,
//   - the bridge accept created for it, which is that name with "-link",
//   - the passphrase file, because leaving a secret behind for a link nobody
//     has is a secret nobody is looking after.
//
// A bridge an operator built themselves is left alone even if it mentions the
// upstream: removing configuration somebody wrote by hand, because it referred
// to something else being removed, is a surprise nobody asked for. The response
// names what was left so it is not a silent decision.
func (s *Server) handleRemoveLink(w http.ResponseWriter, r *http.Request) {
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

	cfg := s.opts.Config.Current()

	var removed *config.Upstream
	kept := make([]config.Upstream, 0, len(cfg.DMR.Upstreams))
	for _, u := range cfg.DMR.Upstreams {
		if strings.EqualFold(u.Name, name) {
			link := u
			removed = &link
			continue
		}
		kept = append(kept, u)
	}
	if removed == nil {
		writeJSON(w, s.log, http.StatusNotFound,
			map[string]string{"error": fmt.Sprintf("there is no link called %q", name)})
		return
	}
	cfg.DMR.Upstreams = kept

	// The bridge accept made, by the name it made it under. Anything else that
	// mentions this upstream was written by an operator and stays.
	bridgeName := removed.Name + "-link"
	var orphaned []string
	bridges := make([]config.Bridge, 0, len(cfg.DMR.Bridges))
	for _, b := range cfg.DMR.Bridges {
		if strings.EqualFold(b.Name, bridgeName) {
			continue
		}
		if bridgeMentions(b, removed.Name) {
			orphaned = append(orphaned, b.Name)
		}
		bridges = append(bridges, b)
	}
	cfg.DMR.Bridges = bridges

	author := "unknown"
	if sess, ok := SessionFrom(r.Context()); ok {
		author = sess.Username
	}
	version, err := s.opts.Config.Save(r.Context(), cfg, author,
		fmt.Sprintf("removed the link %q", removed.Name))
	if err != nil {
		s.recordPeering(r, "peering.removed", removed.Name, removed.Address, audit.OutcomeFailure)
		writeJSON(w, s.log, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.recordPeering(r, "peering.removed", removed.Name, removed.Address, audit.OutcomeSuccess)

	// **After the configuration is saved, never before.** A passphrase deleted
	// under a link that is still configured leaves an instance that cannot
	// authenticate and cannot say why; a file left behind under a link that is
	// gone is untidy and harmless.
	if p := strings.TrimSpace(removed.PassphraseFile); p != "" {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			s.log.Warn("could not remove the passphrase file for a removed link",
				"link", removed.Name, "path", p, "error", err)
		}
	}

	writeJSON(w, s.log, http.StatusOK, map[string]any{
		"version": version.Number,
		"name":    removed.Name,
		// Bridges that referred to this upstream and were written by somebody
		// rather than by accept. Named so the operator can decide.
		"orphaned_bridges": orphaned,
	})
}

// bridgeMentions reports whether a bridge routes to this upstream.
func bridgeMentions(b config.Bridge, upstream string) bool {
	for _, e := range b.Endpoints {
		if strings.EqualFold(e.Upstream, upstream) {
			return true
		}
	}
	return false
}
