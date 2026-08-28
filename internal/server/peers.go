package server

import (
	"net/http"
	"time"
)

// PeerView is one peer as the console sees it.
//
// It is a deliberate projection rather than the domain type. The registry holds
// a peer's outstanding challenge salt, and exposing the domain struct directly
// would put a live credential one careless JSON tag away from the network.
// Adding a field here is an explicit act.
type PeerView struct {
	// ID is the peer's DMR repeater or radio ID.
	ID uint32 `json:"id"`
	// Callsign is what the peer announced. Empty until it is configured.
	Callsign string `json:"callsign"`
	// Address is where its datagrams arrive from.
	Address string `json:"address"`
	// State is its position in the login sequence.
	State string `json:"state"`
	// Ready reports whether it may pass traffic.
	Ready bool `json:"ready"`
	// ConnectedFor is how long since it completed registration. Zero if it
	// never has.
	ConnectedFor string `json:"connected_for,omitempty"`
	// IdleFor is how long since anything was last heard from it.
	IdleFor string `json:"idle_for"`
	// ColorCode is the peer's announced colour code. Empty until configured.
	ColorCode string `json:"color_code,omitempty"`
}

// CallView is one transmission as the console sees it.
type CallView struct {
	// Source is the radio ID that keyed up. Unlike the peer ID this survives
	// relaying, so it is the identity an operator recognises.
	Source uint32 `json:"source"`
	// Target is the talkgroup or radio being called.
	Target uint32 `json:"target"`
	// Group reports a group call rather than a private one.
	Group bool `json:"group"`
	// Timeslot is 1 or 2.
	Timeslot int `json:"timeslot"`
	// Duration is how long the call ran, or has been running.
	Duration string `json:"duration"`
	// Ago is how long since it ended. Empty while in progress.
	Ago string `json:"ago,omitempty"`
	// Frames counts the frames received.
	Frames int `json:"frames"`
	// Lost reports that the call ended without a terminator, which usually
	// means a lossy link or a peer that vanished mid-transmission.
	Lost bool `json:"lost,omitempty"`
}

// Traffic is what the listener has seen since it started.
//
// These counters diagnosed a real problem that the console could not: a hotspot
// was connected and sending keepalives, but its voice frames were being dropped
// upstream and never arrived. The peer table looked healthy and Last heard
// looked empty, which is indistinguishable from nobody talking. The counters
// said 28 datagrams in six minutes with nothing refused, which is the keepalive
// rate exactly — and that settled it in one request.
//
// An operator should not have to read /healthz to learn that.
type Traffic struct {
	// DatagramsIn is every datagram received, including keepalives.
	DatagramsIn uint64 `json:"datagrams_in"`
	// DatagramsOut is every datagram sent.
	DatagramsOut uint64 `json:"datagrams_out"`
	// Dropped is datagrams refused, each for a logged reason.
	Dropped uint64 `json:"dropped"`
	// FramesAccepted is voice frames accepted from registered peers.
	FramesAccepted uint64 `json:"frames_accepted"`
	// FramesForwarded is frames relayed to another peer.
	FramesForwarded uint64 `json:"frames_forwarded"`
	// Collisions is frames refused because a destination was already carrying
	// another transmission.
	Collisions uint64 `json:"collisions"`
}

// PeerSource supplies the current peer list.
//
// The server depends on this narrow interface rather than on the peers package
// so that it can be tested without a UDP socket, and so that the console cannot
// reach any further into peer state than this.
type PeerSource interface {
	// PeerViews returns a point-in-time snapshot, safe to call from any
	// goroutine.
	PeerViews(now time.Time) []PeerView
	// CallViews returns in-progress and recently finished transmissions.
	CallViews(now time.Time) (active, recent []CallView)
	// Traffic returns the listener's counters.
	Traffic() Traffic
}

// peersResponse is the shape returned by /api/peers.
type peersResponse struct {
	// Enabled reports whether the DMR listener is running at all.
	//
	// It exists so the console can tell "nobody is connected" from "QSP is not
	// accepting connections", which look identical in an empty list and mean
	// very different things to an operator.
	Enabled bool `json:"enabled"`
	// Reason explains a disabled listener. Empty when enabled.
	Reason string `json:"reason,omitempty"`
	// Forwarding reports whether this instance relays traffic.
	//
	// It exists because the console cannot otherwise tell a master that is
	// observing from one that is repeating, and it told the operator the wrong
	// one: the banner was static markup that always said forwarding was off,
	// including on an instance that had been repeating for eleven hours.
	Forwarding bool `json:"forwarding"`
	// Peers is the current list, ordered by ID. Never null.
	Peers []PeerView `json:"peers"`
	// Active are transmissions in progress right now. Never null.
	Active []CallView `json:"active_calls"`
	// Recent are finished transmissions, most recent first. Never null.
	Recent []CallView `json:"recent_calls"`
	// Traffic is what the listener has seen since it started.
	Traffic Traffic `json:"traffic"`
	// GeneratedAt is when the snapshot was taken, in UTC.
	GeneratedAt time.Time `json:"generated_at"`
}

// handlePeers serves the current peer list.
//
// It is read-only. QSP exposes no endpoint that changes state until
// authorisation is designed; see SECURITY.md.
func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	body := peersResponse{
		Peers:       []PeerView{},
		Active:      []CallView{},
		Recent:      []CallView{},
		GeneratedAt: now,
	}

	if s.opts.Peers == nil {
		body.Reason = s.opts.PeersDisabledReason
		if body.Reason == "" {
			body.Reason = "the DMR listener is not enabled"
		}
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}

	body.Enabled = true
	body.Forwarding = s.opts.Forwarding
	if views := s.opts.Peers.PeerViews(now); len(views) > 0 {
		body.Peers = views
	}
	body.Traffic = s.opts.Peers.Traffic()
	active, recent := s.opts.Peers.CallViews(now)
	if len(active) > 0 {
		body.Active = active
	}
	if len(recent) > 0 {
		body.Recent = recent
	}
	writeJSON(w, s.log, http.StatusOK, body)
}
