package server

import (
	"net/http"
	"time"

	"github.com/k9mls/qsp/internal/hotspot"
)

// Member onboarding.
//
// A club network is one admin and fifty to a hundred members, each of whom has
// to point a hotspot at it. Getting the first hotspot connected to QSP took two
// sessions across two days, and QSP was never at fault: the obstacles were
// Enabled=0 in /etc/dmrgateway while the WPSD dashboard reported the network as
// enabled, and a talkgroup rewrite that meant the number dialled was not the
// number that arrived.
//
// A member who hits either concludes the software is broken. Told fifty times,
// that is the product's reputation. This endpoint exists so the admin can send
// one URL instead of having fifty conversations.
//
// The shared secret is deliberately absent. Everything here is safe to show
// anyone who can already reach the console; the password is the one thing that
// is not, and it travels by whatever channel the club already trusts.

// JoinSettings are the connection details a member needs, supplied by the
// binary because the server does not read configuration itself.
type JoinSettings struct {
	// NetworkName is what the club calls this network.
	NetworkName string `json:"network_name,omitempty"`
	// Address is the host or IP a hotspot should point at. Empty when the
	// binary could not determine a routable one, which the page must say
	// rather than guess.
	Address string `json:"address,omitempty"`
	// Port is the DMR listener's UDP port.
	Port int `json:"port"`
	// Talkgroups are the ones members can use, in the form they must be
	// dialled.
	Talkgroups []JoinTalkgroup `json:"talkgroups"`
	// AddressReason explains an empty Address.
	AddressReason string `json:"address_reason,omitempty"`
	// Parrot is the echo talkgroup, or zero when the club runs none.
	//
	// It is here for the generated configuration rather than for display: a
	// member arriving from another network has parrot programmed as a private
	// call, and the rule that converts it lives on their hotspot.
	Parrot uint32 `json:"parrot,omitempty"`
}

// JoinTalkgroup is one talkgroup as a member must dial it.
//
// Dialled and Arrives differ whenever the hotspot rewrites, which under WPSD's
// automatic rules it always does. Showing only one of them is how an evening
// gets lost.
type JoinTalkgroup struct {
	// Name is what the club calls it.
	Name string `json:"name"`
	// Dialled is the talkgroup number to enter into the radio.
	Dialled uint32 `json:"dialled"`
	// Arrives is the talkgroup it becomes at QSP, when the hotspot rewrites.
	// Zero when no rewrite is expected.
	Arrives uint32 `json:"arrives,omitempty"`
	// Timeslot is the DMR timeslot, 1 or 2.
	Timeslot int `json:"timeslot"`
}

// joinResponse is the shape returned by /api/join.
type joinResponse struct {
	// Enabled reports whether the DMR listener is accepting peers at all.
	// A member following instructions against a disabled listener would see
	// nothing happen and have no way to know why.
	Enabled bool `json:"enabled"`
	// Reason explains a disabled listener.
	Reason string `json:"reason,omitempty"`

	Settings JoinSettings `json:"settings"`

	// Connected is the number of peers currently registered, so a member can
	// see the network is alive before they start.
	Connected int `json:"connected"`
	// You describes the peer that appears to be the caller's, matched by
	// source address. Nil until their hotspot registers.
	//
	// This is the whole point of serving this as a page rather than a PDF: the
	// machine tells them it worked.
	You *PeerView `json:"you,omitempty"`

	// Heard is the caller's most recent transmission, if QSP saw one.
	//
	// Without it the page ends in ambiguity: a member keys up and is told that
	// silence is normal, which is true and useless. QSP already knows whether
	// the transmission arrived and how many frames it carried, so it can say
	// so — and a member who can see that their audio reached the network stops
	// wondering whether they configured something wrong.
	Heard *CallView `json:"heard,omitempty"`

	GeneratedAt time.Time `json:"generated_at"`
}

// handleJoin reports what a member needs in order to connect a hotspot.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	body := joinResponse{
		Settings:    s.Join(),
		GeneratedAt: now,
	}
	if body.Settings.Talkgroups == nil {
		body.Settings.Talkgroups = []JoinTalkgroup{}
	}

	if s.opts.Peers == nil {
		body.Reason = s.opts.PeersDisabledReason
		if body.Reason == "" {
			body.Reason = "the DMR listener is not enabled, so no hotspot can connect yet"
		}
		writeJSON(w, s.log, http.StatusOK, body)
		return
	}

	body.Enabled = true
	views := s.opts.Peers.PeerViews(now)
	body.Connected = len(views)

	// Match the caller to a registered peer by source address. A hotspot and
	// the browser looking at this page are usually on the same LAN behind one
	// router, so this is a hint rather than proof — but it is right often
	// enough to turn "did it work?" into "yes, you are W5ABC".
	if host := clientHost(r); host != "" {
		for i := range views {
			if peerHost(views[i].Address) == host {
				body.You = &views[i]
				break
			}
		}
	}

	// Their most recent transmission, once their hotspot is identified.
	//
	// Matching on the peer's radio ID rather than the call's source, because a
	// relayed call keeps the originating radio's ID and this member did not
	// send it. Active calls are checked first: somebody watching this page
	// while keying up should see it happen, not afterwards.
	if body.You != nil {
		active, recent := s.opts.Peers.CallViews(now)
		body.Heard = mostRecentFrom(body.You.ID, active, recent)
	}

	writeJSON(w, s.log, http.StatusOK, body)
}

// mostRecentFrom returns the newest call originated by a radio, preferring one
// still in progress.
func mostRecentFrom(id uint32, active, recent []CallView) *CallView {
	for i := range active {
		if active[i].Source == id {
			return &active[i]
		}
	}
	// recent is newest-first, as the console renders it.
	for i := range recent {
		if recent[i].Source == id {
			return &recent[i]
		}
	}
	return nil
}

// clientHost extracts the caller's address, ignoring the port.
func clientHost(r *http.Request) string {
	return peerHost(r.RemoteAddr)
}

// peerHost strips the port from a host:port pair, tolerating IPv6 brackets and
// values that carry no port at all.
func peerHost(addr string) string {
	if addr == "" {
		return ""
	}
	if i := lastIndexByte(addr, ':'); i >= 0 {
		// An IPv6 address without a port has several colons and no brackets;
		// only treat the tail as a port when it looks like one.
		if isDigits(addr[i+1:]) {
			addr = addr[:i]
		}
	}
	if len(addr) >= 2 && addr[0] == '[' && addr[len(addr)-1] == ']' {
		addr = addr[1 : len(addr)-1]
	}
	return addr
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// handleHotspotConfig renders the network block a member pastes into their own
// hotspot.
//
// **The prefix and the block number are query parameters rather than
// configuration**, because they are facts about a file only the member can see.
// Their hotspot may already carry BrandMeister, DMR+ and two others, and which
// leading digits and which [DMR Network N] slots are free is answerable only
// from that machine. The rewrite happens there too, before anything reaches
// QSP, so one member choosing 7 and another choosing 3 affects neither the
// network nor each other.
//
// It is unauthenticated for the same reason the rest of this page is: everything
// it returns is safe to show anybody who can already reach the console, and the
// one thing that is not — the password — is a placeholder in the output.
func (s *Server) handleHotspotConfig(w http.ResponseWriter, r *http.Request) {
	settings := s.Join()

	net := hotspot.Network{
		Name:    settings.NetworkName,
		Address: settings.Address,
		Port:    settings.Port,
		Parrot:  settings.Parrot,
		Block:   queryInt(r, "block", 4),
		Prefix:  queryInt(r, "prefix", 0),
	}
	for _, tg := range settings.Talkgroups {
		net.Talkgroups = append(net.Talkgroups, hotspot.Talkgroup{
			Name:     tg.Name,
			Dialled:  tg.Dialled,
			Arrives:  tg.Arrives,
			Timeslot: tg.Timeslot,
		})
	}

	// The radio ID is observed, never derived.
	//
	// QSP has seen the ID of every radio that has transmitted through a peer,
	// and CallView.Source is that radio rather than the hotspot. Stripping the
	// two-digit suffix off a peer ID would be arithmetic on a convention the
	// access work already established is not a rule of the protocol — and a
	// private call rule naming the wrong radio sends a member's texts somewhere
	// they will never look. Unknown produces no rules and says so.
	if id := queryInt(r, "radio_id", 0); id > 0 {
		net.RadioID = uint32(id)
	} else if s.opts.Peers != nil {
		now := time.Now().UTC()
		if host := clientHost(r); host != "" {
			views := s.opts.Peers.PeerViews(now)
			for i := range views {
				if peerHost(views[i].Address) != host {
					continue
				}
				active, recent := s.opts.Peers.CallViews(now)
				if call := mostRecentFrom(views[i].ID, active, recent); call != nil {
					net.RadioID = call.Source
				}
				break
			}
		}
	}

	cfg, err := hotspot.Render(net)
	if err != nil {
		writeJSON(w, s.log, http.StatusOK, hotspotResponse{Reason: err.Error()})
		return
	}
	writeJSON(w, s.log, http.StatusOK, hotspotResponse{
		Block:    cfg.Block,
		Warnings: cfg.Warnings,
		RadioID:  net.RadioID,
		Prefix:   net.Prefix,
	})
}

// hotspotResponse is the shape returned by /api/join/config.
type hotspotResponse struct {
	// Block is the text to paste, empty when Reason explains why there is none.
	Block string `json:"block,omitempty"`
	// Warnings are things the member must check that QSP cannot.
	Warnings []string `json:"warnings,omitempty"`
	// RadioID is the ID used in the block, zero when QSP has not heard them.
	// Echoed so the page can say whether it knew or guessed nothing.
	RadioID uint32 `json:"radio_id,omitempty"`
	// Prefix is the leading digit used, zero for a single-network hotspot.
	Prefix int `json:"prefix"`
	// Reason explains an empty Block.
	Reason string `json:"reason,omitempty"`
}

// queryInt reads a small non-negative integer from the query string, falling
// back rather than failing: a member who edits the URL should get a page, not
// an error they cannot act on.
func queryInt(r *http.Request, key string, fallback int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" || !isDigits(raw) || len(raw) > 9 {
		return fallback
	}
	n := 0
	for i := 0; i < len(raw); i++ {
		n = n*10 + int(raw[i]-'0')
	}
	return n
}
