package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/config"
)

// LinkSource supplies what is known about the links to other networks.
//
// An interface rather than the upstream set itself, for the reason every other
// source here is one: the console reads state and must not be able to change
// it, and a package that could send a frame is one that eventually does.
type LinkSource interface {
	// LinkStatuses returns one entry per configured link, in configured order.
	LinkStatuses() []LinkStatus
}

// LinkStatus is one link as an operator needs to see it.
//
// **Nothing showed this anywhere.** A link opened, authenticated, carried
// audio between two servers, and the only report of any of it was a line in
// /healthz. An operator watching a console saw a peer list, a talkgroup, and no
// indication that another network existed at all — which made a working link
// and a dead one look identical, and cost an afternoon of deciding which it
// was. ADR-0032 names this as required rather than optional.
type LinkStatus struct {
	// Name is the link's configured name.
	Name string `json:"name"`
	// Protocol is openbridge or homebrew.
	Protocol string `json:"protocol"`
	// FarEnd is the address frames are sent to.
	FarEnd string `json:"far_end"`
	// Listening is the address frames arrive on, empty for a protocol that
	// does not listen on one.
	Listening string `json:"listening,omitempty"`
	// NetworkID is what this instance announces on this link.
	NetworkID uint32 `json:"network_id,omitempty"`
	// Open reports whether the socket is bound.
	Open bool `json:"open"`
	// Sent and Received are frames each way.
	//
	// **Both directions, because one is not evidence of the other.** A link
	// that has sent thousands and received none is working perfectly on a
	// quiet network, or is unauthenticated at the far end, and the health
	// report cannot tell those apart from one number.
	Sent     uint64 `json:"sent"`
	Received uint64 `json:"received"`
	// Rejected counts datagrams that arrived and did not verify. A steady
	// trickle is almost always a passphrase the two ends disagree about, which
	// is otherwise indistinguishable from silence.
	Rejected uint64 `json:"rejected"`
	// EverReceived and IdleSeconds describe the quiet. Idle is meaningless
	// when nothing has ever arrived, which is why the two are separate.
	EverReceived bool `json:"ever_received"`
	IdleSeconds  int  `json:"idle_seconds,omitempty"`
	// Summary and Advice come from the link itself, because what to check
	// differs between the protocols and wrong advice is worse than none.
	Summary string `json:"summary"`
	Advice  string `json:"advice,omitempty"`
	// Configured reports whether the current configuration still holds this
	// link. Open reports whether this process holds a socket for it.
	//
	// **They can disagree, and for three hours nothing said so.** Upstreams
	// are built once at startup and `applyPending` does not touch them, so a
	// link removed from the configuration keeps its socket and keeps
	// appearing here, and a link just accepted has no socket at all. The page
	// showed two removed links as healthy, with counters, indistinguishable
	// from live ones, while `DELETE` correctly answered 404 for links that no
	// longer existed. Two statements individually true.
	Configured bool `json:"configured"`
	// PendingRestart says what a restart would do to this link, and is empty
	// when the configuration and the process already agree.
	//
	// `config.NeedsRestart` has always named `dmr.upstreams` as restart-only.
	// Nothing on this page asked it.
	PendingRestart string `json:"pending_restart,omitempty"`
}

// linksResponse is the shape returned by /api/links.
type linksResponse struct {
	// Links is every configured link, or empty.
	Links []LinkStatus `json:"links"`
	// Reason explains an empty list when links are not available at all,
	// which is different from an instance that has none.
	Reason string `json:"reason,omitempty"`
	// GeneratedAt is when this was read.
	GeneratedAt time.Time `json:"generated_at"`
	// Identity is what this instance announces on a link, so the offer form
	// can fill itself in.
	//
	// **An operator should type a callsign once.** Before this the offer form
	// had no callsign box at all, the invitation was refused for wanting one,
	// and the error named two other fields that were already correct.
	Identity linkIdentity `json:"identity"`
}

// linkIdentity is who this instance is, for the offer form.
type linkIdentity struct {
	// Callsign identifies this network to the other administrator.
	Callsign string `json:"callsign,omitempty"`
	// NetworkID is what this instance announces, taken from an existing link
	// when there is one. Empty on an instance that has never peered, which is
	// every instance the first time.
	NetworkID uint32 `json:"network_id,omitempty"`
	// Address is a guess at where the far end should send, and is said to be a
	// guess on the page: an instance behind NAT announces one nobody can reach.
	Address string `json:"address,omitempty"`
}

// handleLinks reports the links to other networks.
//
// **Authenticated, unlike /api/peers.** A peer list describes stations that
// have chosen to announce themselves on a network their operators joined. A
// link describes an agreement between two administrators, including the address
// of somebody else's server and whether their passphrase is working — which is
// theirs to disclose rather than this instance's.
func (s *Server) handleLinks(w http.ResponseWriter, r *http.Request) {
	body := linksResponse{Links: []LinkStatus{}, GeneratedAt: time.Now().UTC()}

	if s.opts.Links == nil {
		body.Reason = "this build has no links configured"
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}
	body.Links = s.opts.Links.LinkStatuses()
	if body.Links == nil {
		body.Links = []LinkStatus{}
	}
	if s.opts.Config != nil {
		cfg := s.opts.Config.Current()
		body.Links = reconcileLinks(body.Links, cfg)
		body.Identity = linkIdentity{
			Callsign:  linkCallsign(cfg),
			NetworkID: firstNetworkID(cfg),
			Address:   defaultLinkAddress(cfg),
		}
	}
	writeJSON(w, s.log, http.StatusOK, body)
}

// reconcileLinks reports what is running against what is configured.
//
// # The defect this exists after
//
// Two links were removed from the configuration, correctly, and went on being
// listed as healthy for three hours: sockets bound, counters shown, a "Nothing
// yet" pill indistinguishable from a live link on a quiet network. Meanwhile
// `DELETE /api/links/{name}` answered 404 for both, which was also correct,
// because they were not in the document any more. **The page and the remover
// read different sources and nothing compared them**, so the honest handler
// looked broken and the stale display looked authoritative.
//
// The cause is that upstreams are built once at startup: `applyPending` in
// internal/peers reconfigures routing, access, triggers, schedule and
// subscription, and does not touch links. That is a recorded decision rather
// than an oversight — `config.NeedsRestart` names `dmr.upstreams` and says
// links hold sockets and a handshake — but nothing told the operator, in the
// response or on the page.
//
// So this does not close or open anything. It says which of the three states
// each link is in, and what a restart would do about it.
func reconcileLinks(running []LinkStatus, cfg config.Config) []LinkStatus {
	configured := make(map[string]config.Upstream, len(cfg.DMR.Upstreams))
	order := make([]string, 0, len(cfg.DMR.Upstreams))
	for _, u := range cfg.DMR.Upstreams {
		key := strings.ToLower(strings.TrimSpace(u.Name))
		configured[key] = u
		order = append(order, key)
	}

	out := make([]LinkStatus, 0, len(running)+len(configured))
	seen := make(map[string]bool, len(running))

	for _, l := range running {
		key := strings.ToLower(strings.TrimSpace(l.Name))
		seen[key] = true
		if _, ok := configured[key]; ok {
			l.Configured = true
			out = append(out, l)
			continue
		}
		// Open, and no longer in the document. **The summary and advice are
		// replaced rather than kept**, because the link's own advice is about
		// checking the far end's address and firewall, which is wrong and
		// expensive advice for a link that was deliberately deleted.
		l.Configured = false
		l.PendingRestart = "restarting QSP will close it"
		l.Summary = "removed from the configuration and still open; " +
			"QSP has not been restarted since it was removed"
		l.Advice = "nothing further is needed here — the link is gone from the " +
			"configuration and its socket closes at the next restart"
		out = append(out, l)
	}

	// Configured and not open: accepted since the last restart, so no socket
	// exists and nothing can arrive on it. Listed rather than omitted, because
	// a link an operator has just agreed to and cannot see is the same silence
	// this page was built to end.
	for _, key := range order {
		if seen[key] {
			continue
		}
		u := configured[key]
		out = append(out, LinkStatus{
			Name:           u.Name,
			Protocol:       u.Protocol,
			FarEnd:         u.Address,
			Listening:      u.ListenAddress,
			NetworkID:      u.NetworkID,
			Open:           false,
			Configured:     true,
			PendingRestart: "restarting QSP will open it",
			Summary: "configured and not open; QSP has not been restarted " +
				"since this link was added",
			Advice: "restart QSP to open this link — until then it carries " +
				"nothing in either direction",
		})
	}
	return out
}
