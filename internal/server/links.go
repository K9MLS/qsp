package server

import (
	"fmt"
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
	// Announces is what this instance calls itself to the far end, whatever
	// the protocol calls that field.
	//
	// **Derived here rather than chosen in the page**, because the page had to
	// guess and guessed wrong: it printed the network ID or, absent one, the
	// words "no network ID". A qsp link has no network ID and never will — it
	// announces a DMR ID, like the repeater it presents itself as — so a
	// working link reported a missing field as though it were a fault.
	Announces string `json:"announces,omitempty"`
	// Inbound says the far end dialled this server rather than the other way
	// round, so this link is in nobody's configuration here.
	Inbound bool `json:"inbound,omitempty"`
	// Network is the far end's network name, when it announced one.
	Network string `json:"network,omitempty"`
	// Software is what the far end says it runs, verbatim and unverified.
	Software string `json:"software,omitempty"`
	// Enabled reports whether the configuration asks for this link at all.
	//
	// **A disabled link is not a link awaiting a restart**, and this page said
	// it was: reconcileLinks never read Enabled, so a link turned off in the
	// configuration was described as "configured and not open; QSP has not
	// been restarted since this link was added" and advised "restart QSP to
	// open this link". Both sentences are false, and following the advice
	// means restarting a live network to discover it changed nothing.
	Enabled bool `json:"enabled"`
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
	// Measured says the counters above mean something.
	//
	// **Not measured is not zero.** An inbound link had no per-peer counters
	// until 0294 and the page printed a dash; a protocol that still does not
	// count must be able to say so rather than reporting a confident zero
	// beside a link that is carrying.
	Measured bool `json:"measured,omitempty"`
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
	// Address is a guess at where the far end should send for an OpenBridge
	// peering, and is said to be a guess on the page: an instance behind NAT
	// announces one nobody can reach.
	Address string `json:"address,omitempty"`
	// LinkAddress is the same guess for a QSP link, which is dialled on the
	// peer listener rather than sent to on an agreed port.
	//
	// **Two fields because there are two answers, and one was being used for
	// both.** The offer form prefilled its address box from Address, so a QSP
	// link was proposed the OpenBridge port whatever the operator chose — and
	// the fix that added a QSP-specific default put it on the *fallback* used
	// when the box arrives empty, which the page never sends. The corrected
	// function was unreachable and the wrong port shipped anyway. A value that
	// exists in two places is a value that disagrees with itself.
	LinkAddress string `json:"link_address,omitempty"`
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

	// **The running set is optional and the configuration is not.**
	//
	// This used to return here when s.opts.Links was nil, before anything read
	// the configuration — and cmd/qsp decides that nil at startup, from the
	// startup configuration: an instance that booted with no upstreams has no
	// link source for the life of the process. So a peering accepted after
	// boot could never appear on this page, whatever the document said, and
	// the page reported "this build has no links configured" — true when it
	// was written, false the moment a link was added.
	//
	// That is the case the reconcile exists for, and it was the one case the
	// reconcile could not reach. It is also every fresh install accepting its
	// first peering, which is the most important use this page has.
	var running []LinkStatus
	if s.opts.Links != nil {
		running = s.opts.Links.LinkStatuses()
	}

	if s.opts.Config == nil {
		body.Links = running
		if body.Links == nil {
			body.Links = []LinkStatus{}
		}
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}

	cfg := s.opts.Config.Current()
	body.Links = reconcileLinks(running, cfg)
	// **A link that dialled in is still a link** (ADR-0052). It arrives as a
	// peer registration rather than as configuration, so nothing in the
	// document describes it and the reconcile above cannot see it — which is
	// why the listening end of a working link read "No links are configured"
	// while it carried audio. An administrator asks one question and should
	// not have to know which end dialled to know where to look.
	if s.opts.Peers != nil {
		body.Links = append(body.Links, inboundLinks(s.opts.Peers.PeerViews(time.Now()), body.Links)...)
	}
	body.Identity = linkIdentity{
		Callsign:    linkCallsign(cfg),
		NetworkID:   firstNetworkID(cfg),
		Address:     defaultLinkAddress(cfg),
		LinkAddress: defaultQSPLinkAddress(cfg),
	}
	// Said only when it is true, and phrased as a fact about the configuration
	// rather than about the build.
	if len(body.Links) == 0 {
		body.Reason = "no links are configured"
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
// inboundLinks describes the QSP servers that have registered with this one.
//
// Recognised by what they announced: a QSP server says so in SoftwareID and
// gives its link's name in PackageID, and nothing else does. A hotspot stays a
// peer, which is what it is.
//
// **Counts are deliberately absent.** The peer table knows a link is connected
// and when it was last heard; it does not count frames each way the way an
// outbound link's transport does. Showing a zero would say the link has carried
// nothing, which is the lie this page exists to stop telling — so the fields
// are left empty and the page shows nothing rather than something false.
func inboundLinks(peers []PeerView, already []LinkStatus) []LinkStatus {
	seen := make(map[string]bool, len(already))
	for _, l := range already {
		seen[strings.ToLower(strings.TrimSpace(l.Name))] = true
	}

	var out []LinkStatus
	for _, p := range peers {
		if p.LinkName == "" {
			continue
		}
		// A name this server also uses for a link of its own. Two links to one
		// far end is a configuration to report rather than a row to duplicate,
		// and the outbound entry carries more, so it wins.
		if seen[strings.ToLower(strings.TrimSpace(p.LinkName))] {
			continue
		}
		l := LinkStatus{
			Name:       p.LinkName,
			Protocol:   config.UpstreamQSP,
			FarEnd:     p.Address,
			Announces:  fmt.Sprintf("%d", p.ID),
			Inbound:    true,
			Enabled:    true,
			Configured: true,
			Open:       p.Ready,
			Network:    p.Network,
			Software:   p.Software,
		}
		// **Counted since 0294, so this is a number rather than a dash.** The
		// peer table used to know only that a link was connected and when it
		// was last heard, so the two ends of one link could not be compared:
		// the side that dialled printed frame counts and the side that listened
		// printed nothing. Still absent-able — a protocol that does not count
		// leaves these nil and the page prints a dash, because not measured is
		// not zero.
		if p.Received != nil {
			l.Received, l.Measured = *p.Received, true
		}
		if p.Sent != nil {
			l.Sent, l.Measured = *p.Sent, true
		}
		if p.Refused != nil {
			l.Rejected, l.Measured = *p.Refused, true
		}
		// **Last heard means traffic on both ends now.** It read keepalives
		// here and traffic on an outbound link, under one column heading, so
		// the same link at the same moment reported 0s on one console and 44s
		// on the other — which reads as one end having gone deaf.
		if p.TrafficIdle != nil {
			l.EverReceived = true
			l.IdleSeconds = int(*p.TrafficIdle)
		}
		if p.Ready {
			l.Summary = "connected; this link dialled in, so it is not in this server's configuration"
			l.EverReceived = true
		} else {
			l.Summary = "registering; this link dialled in and has not finished logging in"
			l.Advice = "if it does not settle, the far end is retrying — check its journal rather " +
				"than this server's configuration, which has nothing to say about a link it did not dial"
		}
		out = append(out, l)
	}
	return out
}

// announces reports what this instance calls itself to the far end of a link.
//
// OpenBridge identifies the sending *server* by a network ID in every frame. A
// homebrew or qsp link registers as a station and is known by its DMR ID
// instead. They are different fields answering the same operator question, and
// leaving the page to choose between them produced "no network ID" beside a
// link that was carrying traffic.
func announces(u config.Upstream) string {
	if u.HomebrewProtocol() {
		if u.RepeaterID == 0 {
			return ""
		}
		return fmt.Sprintf("%d", u.RepeaterID)
	}
	if u.NetworkID == 0 {
		return ""
	}
	return fmt.Sprintf("%d", u.NetworkID)
}

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
		if u, ok := configured[key]; ok {
			l.Configured = true
			l.Enabled = u.Enabled
			// Filled from the configuration, because the running link reports
			// only the field its own protocol has. See announces.
			l.Announces = announces(u)
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
		l := LinkStatus{
			Name:       u.Name,
			Protocol:   u.Protocol,
			FarEnd:     u.Address,
			Listening:  u.ListenAddress,
			NetworkID:  u.NetworkID,
			Announces:  announces(u),
			Enabled:    u.Enabled,
			Open:       false,
			Configured: true,
		}
		if !u.Enabled {
			// Turned off deliberately. A restart does nothing here, and saying
			// otherwise sends an operator to restart a live network for a
			// change that will not happen.
			l.Summary = "disabled in the configuration, so no socket is opened for it"
			l.Advice = "set enabled to true in dmr.upstreams and restart, or remove the " +
				"link; a restart on its own will not open a link that is turned off"
			out = append(out, l)
			continue
		}
		l.PendingRestart = "restarting QSP will open it"
		l.Summary = "configured and not open; QSP has not been restarted " +
			"since this link was added"
		l.Advice = "restart QSP to open this link — until then it carries " +
			"nothing in either direction"
		out = append(out, l)
	}
	return out
}
