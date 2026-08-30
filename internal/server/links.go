package server

import (
	"net/http"
	"time"
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
	writeJSON(w, s.log, http.StatusOK, body)
}
