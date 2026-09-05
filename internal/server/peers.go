package server

import (
	"net/http"
	"sort"
	"time"

	"github.com/k9mls/qsp/internal/peers"
)

// AttachmentView is one talkgroup a peer is receiving.
type AttachmentView struct {
	Talkgroup uint32 `json:"talkgroup"`
	Timeslot  int    `json:"timeslot"`
	// Static distinguishes what an administrator pinned from what the member
	// attached by transmitting — the difference between "you cannot drop this"
	// and "this lapses if you stop using it".
	Static bool `json:"static"`
	// IdleFor is how long since it last carried traffic, empty for a static
	// attachment nobody has used.
	IdleFor string `json:"idle_for,omitempty"`
}

// PeerView is one peer as the console sees it.
//
// It is a deliberate projection rather than the domain type. The registry holds
// a peer's outstanding challenge salt, and exposing the domain struct directly
// would put a live credential one careless JSON tag away from the network.
// Adding a field here is an explicit act.
// Where a callsign came from. See PeerView.CallsignSource.
const (
	CallsignFromPeer     = "peer"
	CallsignFromRegistry = "registry"
	CallsignFromOperator = "operator"
)

type PeerView struct {
	// ID is the peer's DMR repeater or radio ID.
	ID uint32 `json:"id"`
	// CallsignSource says where the callsign came from: "peer" if the peer
	// announced it, "registry" if QSP looked it up, "operator" if an
	// administrator wrote it down. Empty when there is no callsign.
	//
	// **They are three different claims and the console shows three.** A
	// Homebrew peer states its callsign at login and QSP repeats it; an IPSC
	// repeater states nothing, so a registry match is QSP guessing from a
	// public database that can be stale, wrong, or describing the operator
	// rather than the repeater; and an operator's own label is neither
	// announced nor verified, but it is the person who owns the repeater
	// saying what it is. Presenting a guess in the same style as a statement
	// is the shape of fake data §7 forbids.
	//
	// It replaced a CallsignLookedUp bool, which could express two of the
	// three and quietly filed the third under "announced".
	CallsignSource string `json:"callsign_source,omitempty"`
	// Protocol is which listener this peer belongs to.
	//
	// **A blank column means two different things without it.** A Homebrew
	// peer announces a callsign, a location and its talkgroups; an IPSC
	// repeater announces none of them, because IP Site Connect does not carry
	// them. Merged into one list unlabelled, a repeater reads as a hotspot
	// that failed to configure itself, and an operator would go looking for a
	// fault that is not there. §7's rule against fake anything covers an empty
	// field that means "not applicable" and looks like "not set".
	Protocol string `json:"protocol"`
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
	// Location is the place name a peer announced, free text and unverified.
	// Shown even when the coordinates did not parse, because "Denton, TX" is
	// useful to an operator whether or not a pin can be drawn.
	Location string `json:"location,omitempty"`
	// Latitude and Longitude are decimal degrees, present only when the peer
	// announced coordinates that parsed and are plausible.
	//
	// **Pointers, so absent and zero are distinguishable.** A station on the
	// equator would otherwise be indistinguishable from one that sent nothing,
	// and a map would either lose it or draw it in the Gulf of Guinea.
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	// Height is metres above ground, omitted when zero or unannounced.
	Height int `json:"height,omitempty"`
	// Attachments are the talkgroups this peer is receiving, static first.
	//
	// **"Why can I not hear that talkgroup" is the most common question on any
	// DMR network**, and until now the answer was a list QSP held and did not
	// display. Omitted entirely when subscription is off, because then every
	// peer receives everything and a list would imply a limit that does not
	// exist.
	Attachments []AttachmentView `json:"attachments,omitempty"`
	// PositionRefused explains coordinates that arrived and were not used.
	//
	// A peer that announced nothing and one that announced 0,0 both produce no
	// pin, and an operator can only act on the second if somebody says which
	// happened.
	PositionRefused string `json:"position_refused,omitempty"`
}

// The protocols a peer can arrive on, as PeerView.Protocol reports them.
const (
	// ProtocolHomebrew is a hotspot or repeater on the Homebrew/MMDVM
	// protocol, which is what most members run.
	ProtocolHomebrew = "homebrew"
	// ProtocolIPSC is a Motorola repeater on IP Site Connect.
	ProtocolIPSC = "ipsc"
)

// IPSCTraffic is what the Motorola listener can report.
//
// It is deliberately shorter than Traffic. The IPSC listener counts what it has
// had a reason to count, and inventing zeroes for figures it does not keep
// would be a panel reporting numbers nobody measured.
type IPSCTraffic struct {
	// VoiceFrames is voice frames received from registered repeaters, summed
	// across peers.
	//
	// **This is why the type exists.** Before it, a network whose only traffic
	// was Motorola repeaters showed "0 voice frames" and a note advising the
	// operator to check a hotspot that was not involved.
	VoiceFrames uint64 `json:"voice_frames"`
	// Ignored is datagrams from radio IDs not on the allow list.
	Ignored uint64 `json:"ignored"`
	// Unparsed is datagrams this build does not recognise. It is expected to
	// be non-zero: eight message types are known and IPSC has more.
	Unparsed uint64 `json:"unparsed"`
}

// CallView is one transmission as the console sees it.
type CallView struct {
	// Source is the radio ID that keyed up. Unlike the peer ID this survives
	// relaying, so it is the identity an operator recognises.
	Source uint32 `json:"source"`
	// SourceName is the callsign, when QSP knows it.
	//
	// **Only from a hotspot's own registration.** A radio whose ID matches a
	// connected peer is that peer, and QSP can say so without anybody's
	// database. A radio behind a hotspot with a different ID is left as a
	// number: QSP knows which hotspot carried it and nothing about whose radio
	// it is, and labelling it with the hotspot owner's callsign would be worse
	// than the number.
	SourceName string `json:"source_name,omitempty"`
	// TargetName is the called party's callsign, for private calls only.
	TargetName string `json:"target_name,omitempty"`
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
	// EndedAt is when it ended, in UTC. Zero while in progress.
	//
	// It exists because two listeners' recent calls have to be merged into one
	// list that is genuinely most-recent-first, and Ago is a rendered string
	// that cannot be sorted. The console may also use it to keep relative
	// times fresh between polls.
	EndedAt time.Time `json:"ended_at,omitempty"`
	// Frames counts the frames received.
	Frames int `json:"frames"`
	// Voice reports whether any voice frame arrived. A text message is a few
	// one-frame data bursts, and without this the console cannot tell one from
	// a transmission that failed halfway.
	Voice bool `json:"voice"`
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
	// Answered is the subset QSP replied to, and Ignored the subset it did not.
	//
	// **Named "answered" rather than "refused" deliberately.** This payload
	// already carries a `refused` list of registration refusals, with addresses
	// and reasons, and two different meanings under one key in one object is
	// how a console reads the wrong thing.
	//
	// **A refusal QSP answered is the protocol working.** A keepalive from a
	// peer that has not registered is answered with MSTNAK so the peer logs in
	// again, which is what ADR-0011 intends — and counting it beside a stray
	// port scan produced a permanently non-zero number that looked like a fault
	// and was not.
	Answered uint64 `json:"answered"`
	Ignored  uint64 `json:"ignored"`
	// RecentDrops explains those counters without a restart.
	//
	// The reason for a drop is logged at debug, production runs at info, and
	// raising the level needs a restart — which resets the counter. An operator
	// could not see why a number was what it was without destroying the number.
	RecentDrops []peers.DropNote `json:"recent_drops,omitempty"`
	// IPSC carries the Motorola listener's figures, when that listener is
	// running. Nil when it is not.
	//
	// **A separate object rather than added into the counters above**, because
	// those are documented figures for one socket and summing two into them
	// would change what an existing number means without saying so. It is also
	// the honest shape: the two listeners do not count the same things, and a
	// single total would imply they do.
	IPSC *IPSCTraffic `json:"ipsc,omitempty"`
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
	// Map is how the console should draw peer positions. It travels with the
	// peer list because that is the only response that needs it, and a second
	// endpoint for three fields would be three fields and an endpoint.
	Map MapSettings `json:"map"`
	// Refused reports logins QSP is currently turning away.
	//
	// **Nothing surfaced this before.** Forty failed logins from one address in
	// six minutes looked, from the console, like the dropped counter going up —
	// and the operator found out because the member messaged them.
	Refused []peers.LoginFailure `json:"refused,omitempty"`
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

// MapSettings is what the console needs to draw a map.
type MapSettings struct {
	// TileURL is a slippy-map template. Empty draws pins on no background,
	// which is the right answer on a network with no route out.
	TileURL string `json:"tile_url,omitempty"`
	// Attribution is rendered over the map whenever tiles are. It is a licence
	// condition of the data rather than a courtesy.
	Attribution string `json:"attribution,omitempty"`
	// MaxZoom bounds how far in the map will go.
	MaxZoom int `json:"max_zoom,omitempty"`
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

	// Before the early return. A refusal is exactly what an operator needs to
	// see when the peer list is empty — the case where somebody is trying to
	// connect and failing is the one where nothing else on the page says so.
	if r := s.loginReporter(); r != nil {
		body.Refused = r.LoginFailures(now)
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
	body.Forwarding = s.Forwarding()
	body.Map = s.MapSettings()
	body.Peers = append(body.Peers, s.opts.Peers.PeerViews(now)...)
	body.Traffic = s.opts.Peers.Traffic()
	active, recent := s.opts.Peers.CallViews(now)
	body.Active = append(body.Active, active...)
	body.Recent = append(body.Recent, recent...)

	// Motorola repeaters, when that listener is running. They are appended
	// rather than replacing anything, and Traffic is deliberately left as the
	// DMR listener's: it is a documented set of counters for one socket, and
	// summing two sockets into it would change what an existing number means
	// without saying so. The IPSC listener's counters are in /healthz.
	if s.opts.IPSCPeers != nil {
		// The Motorola listener's own figures, kept beside the DMR listener's
		// rather than folded into them.
		body.Traffic.IPSC = s.opts.IPSCPeers.Traffic().IPSC
		body.Peers = append(body.Peers, s.opts.IPSCPeers.PeerViews(now)...)
		a, r := s.opts.IPSCPeers.CallViews(now)
		body.Active = append(body.Active, a...)
		body.Recent = append(body.Recent, r...)
	}

	// One list, ordered as each was ordered alone: peers by ID, recent calls
	// most recent first. Appending one source after another would group by
	// protocol instead, and a "recent calls" list that is really two lists
	// end to end misleads about what happened when.
	sort.SliceStable(body.Peers, func(i, j int) bool {
		return body.Peers[i].ID < body.Peers[j].ID
	})
	sort.SliceStable(body.Recent, func(i, j int) bool {
		return body.Recent[i].EndedAt.After(body.Recent[j].EndedAt)
	})

	writeJSON(w, s.log, http.StatusOK, body)
}
