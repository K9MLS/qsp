// Package config defines QSP's configuration model, its defaults, its
// validation rules, and the versioning concepts the console builds on.
//
// Three rules govern this package:
//
//  1. There is exactly one configuration system. Subsystems receive typed
//     values from this package; none of them read files or environment
//     variables on their own.
//
//  2. An invalid configuration can never become active. Validate returns every
//     problem it finds rather than the first, because an operator fixing a form
//     should see all of it at once.
//
//  3. Every field carries the operator guidance the console renders. That
//     guidance lives beside the field definition so the two cannot drift.
//
// Configuration is ultimately edited through the web UI, not by hand. The JSON
// representation is a persistence format, not a user interface.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaVersion identifies the shape of the configuration document.
//
// It is incremented when a change requires migration of stored configuration.
// A document bearing a higher version than the running binary understands is
// rejected rather than partially interpreted.
const SchemaVersion = 1

// Config is the complete configuration of a QSP instance.
type Config struct {
	// Version is the schema version of this document.
	Version int `json:"version"`

	Server   Server   `json:"server"`
	Database Database `json:"database"`
	Logging  Logging  `json:"logging"`
	Events   Events   `json:"events"`
	DMR      DMR      `json:"dmr"`
	IPSC     IPSC     `json:"ipsc"`
}

// IPSC configures the Motorola IP Site Connect listener.
//
// # Why this is separate from DMR
//
// They are different protocols on different ports carrying the same audio in
// different wrappers, and a club may run either, both or neither. Folding IPSC
// into the DMR block would mean one Enabled flag for two listeners and an
// operator unable to run a Motorola repeater without also opening HBP.
//
// # What it cannot do yet
//
// It does not route: transmissions are recorded and the audio goes nowhere.
// IPSC carries nineteen bytes where DMR carries a thirty-three byte burst, so
// bridging means reconstructing rather than copying, and QSP does not ship a
// bridge that might degrade audio. See ADR-0036.
//
// It does not authenticate. No capture contains an authenticated registration
// or any refusal, and ICMP unreachable is provably ignored by a repeater, so
// the only "no" QSP can say is silence. AllowedPeers is that silence.
// maxPeerName bounds an operator-supplied repeater callsign. Callsigns run to
// seven characters and a suffix; the room above that is for "K9MLS/R" and the
// like, not for a description of the site.
const maxPeerName = 20

type IPSC struct {
	// Enabled turns the IPSC listener on.
	Enabled bool `json:"enabled"`
	// ListenAddress is the UDP host:port to bind. 50000 is the port observed
	// in practice, but a Motorola repeater has two port settings and this one
	// must match its *Master UDP Port*, not its *UDP Port*.
	ListenAddress string `json:"listen_address"`
	// MasterID is the radio ID this master announces as its own.
	//
	// **It must not equal any peer's.** A repeater refuses to register with a
	// master carrying its own ID and gives no indication why: an XPR8300
	// retried thirty-nine times over six minutes against correct replies.
	MasterID uint32 `json:"master_id"`
	// AllowedPeers lists the radio IDs that are answered. Empty answers every
	// peer, which is right on a bench and wrong on a public address — an IPSC
	// port reachable from the internet attracts whatever is pointed at it.
	AllowedPeers []uint32 `json:"allowed_peers"`
	// PeerNames is the callsign an administrator gave each repeater, keyed by
	// radio ID.
	//
	// **A Motorola repeater announces no callsign and most never will have
	// one to look up.** A hotspot states its callsign at login; an IPSC
	// repeater states nothing, so QSP matches its radio ID against the public
	// registry — and a repeater on a private ID like 999999 is not in that
	// registry and never will be. The console showed a bare number for
	// precisely the peers carrying the network, on every row where a hotspot
	// showed a callsign.
	//
	// **It is separate from AllowedPeers because it answers a different
	// question.** AllowedPeers decides who is answered and is the only "no"
	// this protocol can say; a name decides what an operator reads. Folding a
	// label into the admission list would put display text on the path that
	// decides whether a datagram is processed.
	PeerNames map[uint32]string `json:"peer_names,omitempty"`
	// PeerTimeoutSeconds is how long a peer may be silent before it is
	// dropped. Zero uses three missed keepalives plus a margin.
	//
	// No capture contains a disconnect message, so silence is the only
	// evidence a repeater has gone.
	PeerTimeoutSeconds int `json:"peer_timeout_seconds"`

	// ColourCode is written into the embedded signalling of every burst built
	// from this repeater's audio.
	//
	// **It is a pointer so that absent and zero are different things.** The
	// first version of this field was a uint8 validated for range, and 0 is a
	// legal DMR colour code — so a configuration that never mentioned it
	// passed validation, started cleanly, and built every burst with colour
	// code 0. A receiver rejects a burst whose colour code is not its own
	// without saying anything, so the whole failure presented as silence. See
	// the changelog for 0.1.32.
	//
	// It is required when the listener is enabled and has no default. One is
	// the commonest value in amateur DMR, but defaulting to it would be a
	// claim about somebody else's network — and a wrong colour code is
	// indistinguishable on air from a bridge that does not work.
	ColourCode *uint8 `json:"colour_code"`

	// SlotBitIsTimeslot2 says which value of the IPSC slot bit means timeslot
	// two.
	//
	// **The polarity was never recorded at the radio.** Fifteen transmissions
	// from two channels split cleanly by one bit, so the bit is the timeslot
	// beyond doubt; which value is which was not written down. Getting it
	// wrong puts every transmission on the other slot, which is a setting to
	// change rather than a rebuild — and the journal logs both the raw bit and
	// the slot it was read as, so one key-up on a known slot settles it.
	SlotBitIsTimeslot2 bool `json:"slot_bit_is_timeslot2"`
}

// DMR configures the Homebrew Protocol listener that peers connect to.
//
// # Why the password is a file path
//
// Configuration is versioned, exportable and diffable. A shared secret stored
// in this document would be written into the version history table, appear in
// every export, and show up in a diff on the console. PasswordFile keeps it out
// of all three: the document records where the secret lives, never what it is.
//
// See docs/adr/ADR-0012.
type DMR struct {
	// Enabled turns the peer listener on. When false, QSP runs as a console
	// only and the network subsystem reports itself unavailable.
	Enabled bool `json:"enabled"`
	// ListenAddress is the UDP host:port to bind. 62031 is the port observed in
	// practice for the Homebrew Protocol.
	ListenAddress string `json:"listen_address"`
	// PasswordFile is the path to a file whose contents are the shared peer
	// password. Leading and trailing whitespace is stripped. The file should be
	// mode 0600.
	PasswordFile string `json:"password_file"`
	// PeerPasswords is a directory of per-peer password files, each named for
	// a radio ID. Empty means every peer uses the shared password.
	//
	// **A member can be removed without changing everybody's password.** One
	// shared secret means removing one person requires a new password and every
	// remaining member reconfiguring their hotspot on the same evening, and it
	// gets worse with every member who joins. A peer with a file of its own
	// authenticates against that and not against the shared one, so removal is
	// deleting the file. See ADR-0035.
	//
	// A directory of paths rather than values, for the reason the shared
	// password is one: this document is versioned, exportable and diffable, and
	// a secret in it is a secret in the version history, in every backup, and on
	// screen in a diff.
	PeerPasswords string `json:"peer_passwords,omitempty"`
	// PeerTimeout is how long a registered peer may be silent before it is
	// removed. Observed keepalive interval is 10 s.
	PeerTimeout Duration `json:"peer_timeout"`
	// LoginTimeout bounds an incomplete handshake, which holds a slot without
	// providing service.
	LoginTimeout Duration `json:"login_timeout"`
	// MaxPeers bounds the registry.
	MaxPeers int `json:"max_peers"`
	// SubscriberTimeout is how long a radio's location is trusted after it was
	// last heard.
	//
	// It is deliberately far longer than peer_timeout, because the two answer
	// different questions. A peer that stops sending keepalives is gone; a
	// radio that stops transmitting is merely quiet, and quiet is a radio's
	// normal state.
	SubscriberTimeout Duration `json:"subscriber_timeout"`
	// Forwarding turns audio relaying on.
	//
	// It is separate from Enabled, and off by default, so that an operator can
	// run QSP as a master and watch peers connect before it starts putting
	// audio on anybody's repeater. Turning it on is the moment QSP stops
	// observing and starts transmitting.
	//
	// With it on, the master **repeats**: a group call on a talkgroup reaches
	// every other peer on that talkgroup and timeslot, with no bridge involved.
	// That is the ordinary behaviour of a DMR network and needs no
	// configuration. Bridges are additional, and move traffic *between*
	// talkgroups. See docs/adr/ADR-0019-master-repeats.md.
	Forwarding bool `json:"forwarding"`
	// Join is what a club member needs in order to point a hotspot at this
	// network. Optional: an empty Join means /api/join reports what it can and
	// tells the member to ask their admin for the rest, rather than guessing.
	Join Join `json:"join"`
	// Bridges are the routing rules. Traffic is only relayed between endpoints
	// a bridge joins.
	Bridges []Bridge `json:"bridges"`
	// Triggers open a bridge when somebody transmits on it, and close it after
	// a hang time. A bridge may be scheduled, triggered, both, or neither.
	Triggers []Trigger `json:"triggers"`
	// Schedule holds recurring windows that enable bridges automatically.
	//
	// A bridge named by any window is controlled entirely by the schedule; its
	// own "enabled" field is ignored. A bridge with no window uses that field.
	// One mechanism decides each bridge, so there is never a question of which
	// setting wins.
	Schedule []Window `json:"schedule"`
	// Upstreams are links to other DMR networks over OpenBridge.
	Upstreams []Upstream `json:"upstreams"`
	// Subscription decides which peers receive which talkgroups.
	//
	// Absent, or present and disabled, every peer receives every talkgroup —
	// which is what QSP did before per-peer attachment existed, and what a club
	// running on one talkgroup wants. See ADR-0023.
	Subscription Subscription `json:"subscription"`
	// Access decides which repeaters may register, which subscribers may
	// transmit, and which talkgroups are carried on each timeslot.
	//
	// **Its absence is meaningful, which is why it is a pointer.** An empty
	// block and no block at all behave identically — both permit everything —
	// but only the operator who wrote a block has said that is what they
	// meant. A listener reachable from beyond this host with no block at all
	// refuses to start. See docs/adr/ADR-0020-access-control.md.
	Access *Access `json:"access,omitempty"`
	// Parrot records and replays a transmission on one talkgroup, so a member
	// can prove their setup works with nobody else awake. See ADR-0028.
	Parrot Parrot `json:"parrot"`
	// Callsigns resolves radio IDs to names through the amateur DMR registry.
	// See ADR-0030.
	Callsigns Callsigns `json:"callsigns"`
	// Calls is how long a record of who transmitted is kept. See ADR-0033.
	Calls Calls `json:"calls"`
	// Identity is who this instance says it is.
	Identity Identity `json:"identity"`
}

// Identity is what this instance says about itself to other networks.
//
// **It was per link, and only per link.** `UpstreamIdentity` carries a callsign
// and a position on every upstream, so an instance with three links stated its
// callsign three times with nothing keeping them consistent — and the first
// peering an operator ever attempted had none of them, because an instance with
// no links has no identity either.
//
// A station has one callsign and one position. A link may still override any
// field, for the case where a far end needs something different, but nothing has
// to be repeated to be true.
type Identity struct {
	// Callsign is the licensed identity of whoever runs this instance. A blank
	// one makes QSP appear on somebody else's dashboard as an unidentified
	// station, which is rude and unhelpful in equal measure.
	Callsign string `json:"callsign,omitempty"`
	// Latitude and Longitude are decimal degrees.
	//
	// **Decimal degrees rather than a grid square**, because that is what
	// Pi-Star, the DMR configuration message and every dashboard in this
	// ecosystem already carry — and a conversion at every boundary is where
	// sign errors live. Optional: an instance that would rather not publish a
	// position leaves them out and is not plotted.
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	// Height is metres above ground, for a link that asks.
	Height int `json:"height,omitempty"`
	// Location and Description are free text shown on a far end's dashboard.
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
	// URL is shown beside the station on a far end's dashboard.
	URL string `json:"url,omitempty"`
}

// Merge fills empty fields of a link's identity from the instance's.
//
// **Per-link wins where it is set.** An administrator who states something on
// one link means it, and defaulting over the top of it would silently discard a
// deliberate choice — the same reasoning that makes static and dynamic
// attachments a union rather than a precedence.
func (i Identity) Merge(u UpstreamIdentity) UpstreamIdentity {
	if strings.TrimSpace(u.Callsign) == "" {
		u.Callsign = i.Callsign
	}
	if u.Latitude == 0 && u.Longitude == 0 {
		u.Latitude, u.Longitude = i.Latitude, i.Longitude
	}
	if u.Height == 0 {
		u.Height = i.Height
	}
	if strings.TrimSpace(u.Location) == "" {
		u.Location = i.Location
	}
	if strings.TrimSpace(u.Description) == "" {
		u.Description = i.Description
	}
	if strings.TrimSpace(u.URL) == "" {
		u.URL = i.URL
	}
	return u
}

// Calls configures the record of completed transmissions.
type Calls struct {
	// Retain is how long a completed call is kept.
	//
	// **This is a log of members' activity**, readable by any administrator:
	// who transmitted, what they called, and when. It exists because a net
	// control station uses the last-heard list to recover a check-in they
	// missed, and fifty entries in memory cannot survive the evening or the
	// deploy that follows it.
	//
	// Thirty days by default: enough for a monthly net, a member arguing about
	// last week, and an administrator who was away. A few hundred rows a day
	// costs nothing.
	//
	// **Zero keeps nothing**, which is a real answer for a club that would
	// rather not hold a record of who transmitted when, and the configuration
	// should be able to say so rather than making everybody keep a month.
	Retain Duration `json:"retain"`
}

// Subscription is layer 3: which peers receive which talkgroups.
type Subscription struct {
	// Enabled turns per-peer attachment on. Off by default, because turning it
	// on with nothing configured leaves a network where nobody hears anything
	// until they transmit, and that is a surprising thing to happen on upgrade.
	Enabled bool `json:"enabled"`
	// Timeout is how long a talkgroup a peer attached by transmitting keeps
	// being delivered after they stop.
	Timeout Duration `json:"timeout"`
	// Static are attachments that never lapse, for the cases transmitting
	// cannot serve: a calling channel that must be there before anybody speaks,
	// and a repeater that should always carry its regional talkgroup.
	Static []StaticAttachment `json:"static,omitempty"`
	// Unlink is a talkgroup that, transmitted on, drops every dynamic
	// attachment the peer holds. Zero means the network offers no such thing.
	//
	// **Configuration rather than a constant, for parrot's reason.** 4000 is
	// what most members have programmed because BrandMeister uses it, and a
	// club is free to pick something else — PNWDigital does not use 4000 at
	// all. Hardcoding it would be QSP deciding a talkgroup number, which is
	// the one thing §0 says it must never do.
	//
	// **Static attachments survive it.** A member pressing disconnect says
	// what they want to stop hearing; a static attachment is an administrator's
	// statement about what a peer must always carry, and a PTT does not
	// overrule it. Otherwise somebody drops themselves off the club calling
	// channel and cannot work out why they have gone deaf.
	Unlink uint32 `json:"unlink,omitempty"`
	// UnlinkTimeslot is which slot the unlink talkgroup is dialled on. Zero
	// means either.
	UnlinkTimeslot int `json:"unlink_timeslot,omitempty"`
}

// StaticAttachment is one talkgroup a peer always receives.
type StaticAttachment struct {
	// Peer is the repeater or hotspot ID.
	Peer uint32 `json:"peer"`
	// Talkgroup and Timeslot identify what it receives.
	Talkgroup uint32 `json:"talkgroup"`
	Timeslot  int    `json:"timeslot"`
}

// Parrot configures the record-and-replay talkgroup.
//
// **There is no default talkgroup**, and that is §0 rather than an oversight:
// 9990 is conventional on some networks and 9998 on others, and shipping one
// network's number is what was refused for talkgroup lists and for tile
// servers. A talkgroup QSP swallows is one an operator did not choose to lose.
type Parrot struct {
	// Enabled turns parrot on. Off by default.
	Enabled bool `json:"enabled"`
	// Talkgroup is the number that records and replays. Required when enabled.
	Talkgroup uint32 `json:"talkgroup"`
	// Timeslot the talkgroup lives on: 1 or 2.
	Timeslot int `json:"timeslot"`
	// MaxDuration bounds a recording. Zero selects thirty seconds — without a
	// bound, a stuck PTT is unbounded memory.
	MaxDuration Duration `json:"max_duration"`
	// Gap is the pause between the transmission ending and the replay
	// starting. Zero selects one second, which is long enough for a radio to
	// have returned to receive.
	Gap Duration `json:"gap"`
}

// Callsigns configures radio ID lookups against the amateur DMR registry.
//
// **Off unless configured**, because it makes an outbound request to a third
// party that an operator did not obviously ask for. A club that wants names
// turns it on; a club on an isolated network is not quietly trying to reach the
// internet.
type Callsigns struct {
	// Enabled turns lookups on.
	Enabled bool `json:"enabled"`
	// Contact is the address sent to the registry so it knows who is asking.
	//
	// **Required, with no default.** The registry asks automated clients to
	// identify themselves, and QSP has no business inventing an address for
	// somebody else — it is the operator making the requests and the operator
	// who would be contacted if something were wrong.
	Contact string `json:"contact"`
}

// Access holds the four lists that decide who QSP carries.
//
// Each list permits everything when absent, so an empty Access is exactly as
// permissive as no Access. What differs is that writing one is a statement of
// intent, and the startup check is about silence rather than about behaviour.
type Access struct {
	// Registration names the repeater IDs permitted to register.
	Registration ACL `json:"registration"`
	// Subscribers names the subscriber IDs permitted to transmit.
	//
	// A refused subscriber does not disconnect the peer carrying it. On DMR a
	// hotspot is shared infrastructure and the offending party is a radio.
	Subscribers ACL `json:"subscribers"`
	// Talkgroups names the talkgroups carried, per timeslot.
	Talkgroups Talkgroups `json:"talkgroups"`
}

// Talkgroups holds one list per DMR timeslot.
//
// The two are separate because repeaters are configured per slot, and an
// operator who carries a statewide talkgroup on one slot and local traffic on
// the other cannot express that with a single list.
type Talkgroups struct {
	Timeslot1 ACL `json:"timeslot_1"`
	Timeslot2 ACL `json:"timeslot_2"`
}

// ACL is one access control list.
//
// The zero value permits everything: "deny" naming nobody refuses nobody. That
// is what lets an absent block behave exactly as QSP did before access control
// existed, so that upgrading cannot silently disconnect a running club.
type ACL struct {
	// Mode is "permit" or "deny". Empty means "deny".
	//
	// A permit list refuses anything it does not name; a deny list allows
	// anything it does not name.
	Mode string `json:"mode"`
	// IDs are single IDs ("3100") or inclusive ranges ("3100-3199").
	//
	// Ranges are strings rather than objects because every list is hand-edited
	// until the admin interface exists, a real talkgroup list is mostly ranges,
	// and this is what an operator arriving from HBlink already types.
	IDs []string `json:"ids"`
}

// Upstream is a link to another DMR server over OpenBridge.
//
// See docs/adr/ADR-0018-openbridge.md. Everything here is administrator
// configuration: a club in Toulouse points at a different master from a club in
// Texas, and QSP has no opinion about which.
type Upstream struct {
	// Name identifies this link on the console and in the health report.
	Name string `json:"name"`
	// Protocol is how the link is carried: "openbridge" or "homebrew".
	//
	// Empty means openbridge, so every document written before outbound peer
	// mode existed keeps working and keeps meaning what it already meant.
	//
	// **openbridge** is a bridge between two networks, agreed out of band.
	// **homebrew** logs into somebody else's master as a peer, which is how
	// XLX, DMR+, IPSC2 and another QSP are reached. See ADR-0024 — and note
	// that ADR-0018 forbids pointing a homebrew link at BrandMeister, whose
	// operators define peer bridging as prohibited.
	Protocol string `json:"protocol,omitempty"`
	// Enabled turns the link on.
	//
	// It defaults to false deliberately. Enabling an upstream puts a club's
	// audio onto somebody else's network, and that should be a deliberate edit
	// rather than something inherited from a copied configuration.
	Enabled bool `json:"enabled"`
	// Address is the far end, host:port. OpenBridge conventionally uses 62035.
	Address string `json:"address"`
	// ListenAddress is the local UDP host:port to receive on. OpenBridge has no
	// connection establishment, so the far end sends to an address agreed in
	// advance rather than one discovered from our packets.
	ListenAddress string `json:"listen_address"`
	// NetworkID identifies this server to the far end, in the format of a DMR
	// radio ID. It is stamped into the repeater ID field of every frame sent,
	// which on OpenBridge names the sending server rather than a repeater.
	NetworkID uint32 `json:"network_id"`
	// PassphraseFile holds the shared secret, mode 0600.
	//
	// It is a path for the same reason DMR.PasswordFile is: configuration gets
	// versioned, exported and pasted into support requests, and a secret should
	// not travel with it.
	PassphraseFile string `json:"passphrase_file"`
	// Export names local talkgroups whose traffic is sent upstream.
	//
	// These are the *local* talkgroup and timeslot. QSP moves traffic to TS1
	// on the way out, because proper OpenBridge passes all traffic on TS1 and
	// an administrator should not have to remember that.
	Export []UpstreamTalkgroup `json:"export"`
	// Import names talkgroups accepted from upstream, with the local talkgroup
	// and timeslot they are delivered on.
	//
	// Export and Import are separate lists because they are genuinely different
	// sets: a club may send its own net upstream while accepting a nationwide
	// talkgroup down. Collapsing them makes the asymmetric case unexpressible
	// and the symmetric case look safer than it is.
	//
	// **Neither list routes anything today.** No code reads them to move a
	// frame; a bridge with an endpoint naming this link is what carries
	// traffic to it and back. They are kept because they describe the intended
	// direction filtering and because removing a documented field would refuse
	// configurations already written against it — but an operator who fills
	// them in and expects audio to cross will not get any.
	//
	// A rule once required one of them to be non-empty. It was removed: it
	// disagreed with the check that a link must be named by a bridge, and a
	// configuration satisfying one and failing the other stopped a live
	// network from starting.
	Import []UpstreamTalkgroup `json:"import"`
	// RepeaterID is the ID QSP presents when logging into a master, used by
	// the homebrew protocol instead of NetworkID.
	//
	// It must not collide with a peer registered locally: QSP would then hold
	// one ID meaning two stations, and a private call to it would be routable
	// to two places.
	RepeaterID uint32 `json:"repeater_id,omitempty"`
	// PasswordFile holds the login password for a homebrew link, mode 0600.
	// A path rather than a value, for the reason in ADR-0012.
	PasswordFile string `json:"password_file,omitempty"`
	// Identity is what QSP tells the far end about itself. Required for a
	// homebrew link, ignored for OpenBridge.
	Identity *UpstreamIdentity `json:"identity,omitempty"`
	// StaleAfter is how long without traffic before the link is reported as
	// possibly broken.
	//
	// OpenBridge has no keep-alive, so QSP cannot distinguish a quiet talkgroup
	// from a dead link. Any value here is a guess; the health summary says so
	// rather than claiming to know. Zero disables the warning.
	StaleAfter Duration `json:"stale_after"`
}

// UpstreamIdentity is what QSP announces when it logs into another master.
//
// **It is not decoration.** A master a peer logs into shows these fields to its
// own users, and a blank callsign makes QSP appear on somebody else's dashboard
// as an unidentified station — discourteous at best, and on a network that
// requires identification, grounds for removal.
type UpstreamIdentity struct {
	// Callsign is required. Everything else has a working default, because a
	// reflector does not care about transmit power and an operator should not
	// have to invent one.
	Callsign string `json:"callsign"`
	// RXFrequency and TXFrequency are in hertz. Zero for a link with no radio,
	// which is what QSP is.
	RXFrequency uint32 `json:"rx_frequency,omitempty"`
	TXFrequency uint32 `json:"tx_frequency,omitempty"`
	// ColourCode is 0 to 15.
	ColourCode int `json:"colour_code,omitempty"`
	// Latitude and Longitude are decimal degrees, and Height is metres.
	Latitude  float64 `json:"latitude,omitempty"`
	Longitude float64 `json:"longitude,omitempty"`
	Height    int     `json:"height,omitempty"`
	// Location and Description are free text shown on the far end's dashboard.
	Location    string `json:"location,omitempty"`
	Description string `json:"description,omitempty"`
	// URL is shown beside the station on the far end's dashboard.
	URL string `json:"url,omitempty"`
	// Timeslots is 1 for a simplex link or 2 for duplex.
	Timeslots int `json:"timeslots,omitempty"`
}

// UpstreamProtocol values.
const (
	// UpstreamOpenBridge bridges two networks over an agreed passphrase.
	UpstreamOpenBridge = "openbridge"
	// UpstreamHomebrew logs into another master as a peer.
	UpstreamHomebrew = "homebrew"
	// UpstreamQSP links to another QSP server (ADR-0051).
	//
	// It is the homebrew peer conversation on the wire — this server logs
	// into the other one, so only the side that offered the peering needs a
	// reachable address and the side that dials needs no port forward at
	// all. What differs is everything above the wire, which is why it is a
	// protocol value rather than a flag on homebrew:
	//
	//   - The talkgroup and the timeslot cross unchanged. OpenBridge forces
	//     TS1 because BrandMeister needs it to; two instances of the same
	//     software have no reason to throw the slot away, and doing so is
	//     what silenced a repeater for a day.
	//   - Every talkgroup crosses. The far end is not a foreign network to
	//     be metered but a peer, and each side's own access lists decide
	//     what it keeps.
	//   - It needs no bridge, so it is exempt from the rule that a link
	//     nothing routes to is a fault.
	UpstreamQSP = "qsp"
)

// QSPLink reports whether this link reaches another QSP server.
//
// **The zero value is not this.** An empty protocol means OpenBridge, for
// documents written before outbound peer mode existed, so a link only reaches
// another QSP when it says so.
func (u Upstream) QSPLink() bool {
	return strings.EqualFold(strings.TrimSpace(u.Protocol), UpstreamQSP)
}

// UpstreamTalkgroup is one talkgroup carried over a link, named as it exists
// locally.
type UpstreamTalkgroup struct {
	// Talkgroup is the local talkgroup ID.
	Talkgroup uint32 `json:"talkgroup"`
	// Timeslot is the local timeslot, 1 or 2. Traffic is translated to TS1 for
	// the link itself.
	Timeslot int `json:"timeslot"`
}

// Trigger opens a bridge on demand, when somebody transmits on it.
//
// A scheduled bridge is open because the calendar says so; a triggered bridge is
// open because somebody is using it. Clubs want both.
type Trigger struct {
	// Bridge is the name of the bridge this trigger opens.
	Bridge string `json:"bridge"`
	// On are the endpoints that open it. Usually a subset of the bridge's own
	// endpoints, so a local repeater can open a link outward without the wider
	// network opening it inward.
	On []Endpoint `json:"on"`
	// HangTime is how long the bridge stays open after the last transmission,
	// for example "3m". It must outlast the pauses in a conversation.
	HangTime Duration `json:"hang_time"`
	// Enabled allows a trigger to be suspended without deleting it.
	Enabled bool `json:"enabled"`
}

// Window is a recurring period during which a bridge carries traffic.
//
// Times are local wall-clock times in the named IANA zone, not instants. A net
// at 20:00 happens at 20:00 all year; storing an instant would drag it an hour
// off at each daylight-saving change.
type Window struct {
	// Bridge is the name of the bridge this window enables.
	Bridge string `json:"bridge"`
	// Days are weekdays, 0 for Sunday through 6 for Saturday, in the window's
	// own zone.
	Days []int `json:"days"`
	// Start is the local time of day in 24-hour HH:MM, for example "20:00".
	Start string `json:"start"`
	// Duration is how long the window stays open, for example "1h".
	Duration Duration `json:"duration"`
	// Timezone is an IANA name such as "America/Chicago". Abbreviations like
	// "CST" are not accepted: they cannot express "20:00 local all year".
	Timezone string `json:"timezone"`
	// Enabled allows a window to be suspended without deleting it.
	Enabled bool `json:"enabled"`
}

// Bridge joins endpoints so that traffic arriving at one reaches the others.
type Bridge struct {
	// Name identifies the bridge to an operator. It must be unique.
	Name string `json:"name"`
	// Enabled reports whether the bridge is currently carrying traffic.
	// Scheduled bridging will flip this rather than adding a mechanism.
	Enabled bool `json:"enabled"`
	// Endpoints are the places this bridge joins. At least two are required.
	Endpoints []Endpoint `json:"endpoints"`
}

// Join describes what a club member needs in order to point a hotspot at this
// network, and is served by GET /api/join.
//
// This is configuration rather than something QSP derives, and the reason is
// the whole difficulty of onboarding: the number a member dials is rewritten
// by their own hotspot before QSP ever sees it. QSP knows only the arriving
// talkgroup. Only the admin, who has seen the TGRewrite lines in
// /etc/dmrgateway, knows both halves — and a member told only one of them
// reaches nothing and concludes the software is broken.
//
// The shared peer password is deliberately absent. Everything here is safe to
// show a club's members; the password is not, and it travels separately.
type Join struct {
	// NetworkName is what the club calls this network.
	NetworkName string `json:"network_name"`
	// Address is the host or IP a member's hotspot should point at. Left empty
	// when the admin has not said; the page then asks them to ask, rather than
	// guessing at an address that may be a container's.
	Address string `json:"address"`
	// Talkgroups are the ones members may use.
	Talkgroups []JoinTalkgroup `json:"talkgroups"`
}

// JoinTalkgroup is one talkgroup as a member must dial it.
type JoinTalkgroup struct {
	// Name is what the club calls it, in plain words.
	Name string `json:"name"`
	// Dialled is the number entered into the radio.
	Dialled uint32 `json:"dialled"`
	// Arrives is the talkgroup it becomes by the time QSP sees it. Zero means
	// the hotspot does not rewrite it.
	//
	// **Deprecated, and no longer offered by the console.** A talkgroup number
	// is the same on both sides of a hotspot: 2 is 2 and 11 is 11. A hotspot
	// carrying only this network needs no rewrite rules at all, and generated
	// configuration never renumbers a talkgroup.
	//
	// Still read, because a club that inherited a rewrite it cannot change has
	// to be able to describe it. Nothing creates one any more.
	Arrives uint32 `json:"arrives,omitempty"`
	// Timeslot is 1 or 2.
	Timeslot int `json:"timeslot"`
}

// Target returns the talkgroup QSP will actually receive.
func (t JoinTalkgroup) Target() uint32 {
	if t.Arrives != 0 {
		return t.Arrives
	}
	return t.Dialled
}

// Endpoint is one talkgroup on one timeslot, at one peer or at one link.
type Endpoint struct {
	// Peer is the peer's repeater ID, or 0 for every connected peer.
	Peer uint32 `json:"peer"`
	// Upstream names a link to another network, empty for an ordinary peer.
	//
	// **Without this there was no way to send anything to a link.** The
	// routing core has carried an Upstream on its own endpoint since links
	// existed, and the only place it was ever set was on the inbound side, so
	// traffic could arrive from another network and never leave for one. A
	// link opened, authenticated, reported healthy, and was unreachable from
	// any configuration a person could write.
	//
	// Ignored when Peer is set, and the two together are an error: a link is
	// not a peer.
	Upstream string `json:"upstream,omitempty"`
	// Talkgroup is the talkgroup ID.
	Talkgroup uint32 `json:"talkgroup"`
	// Timeslot is 1 or 2.
	Timeslot int `json:"timeslot"`
}

// OpenBridgeTimeslot is the only timeslot an endpoint naming an OpenBridge link
// may carry.
//
// OpenBridge passes all traffic on TS1 with the slot bit clear, and
// openbridge.Encode forces it on every frame sent. An endpoint on TS2 therefore
// matches nothing arriving and produces nothing that can leave — the link opens,
// authenticates, reports healthy, and carries no audio in either direction.
//
// It is a constant rather than a literal in two places because the accept
// handler writes it and Validate refuses anything else, and those two must not
// be able to disagree.
const OpenBridgeTimeslot = 1

// Server configures the HTTP console listener.
type Server struct {
	// ListenAddress is the host:port the console binds to.
	ListenAddress string `json:"listen_address"`
	// ReadHeaderTimeout bounds how long a client may take to send request
	// headers. It is the primary defence against slow-header denial of service.
	ReadHeaderTimeout Duration `json:"read_header_timeout"`
	// ReadTimeout bounds the total time to read a request.
	ReadTimeout Duration `json:"read_timeout"`
	// WriteTimeout bounds the total time to write a response. Server-Sent
	// Events connections are exempt; see the server package.
	WriteTimeout Duration `json:"write_timeout"`
	// IdleTimeout bounds how long a keep-alive connection may sit unused.
	IdleTimeout Duration `json:"idle_timeout"`
	// ShutdownTimeout bounds graceful shutdown before connections are forced closed.
	ShutdownTimeout Duration `json:"shutdown_timeout"`
	// BehindProxy indicates the console is served behind a reverse proxy that
	// terminates TLS. It affects which forwarding headers are trusted.
	BehindProxy bool `json:"behind_proxy"`
	// Map configures the console's peer map. See ADR-0025.
	Map Map `json:"map"`
}

// Database configures persistent storage.
// Map configures the console's peer map.
//
// QSP draws the map itself and vendors no library, so what is configurable here
// is where the tiles come from — and whose name goes under them.
type Map struct {
	// TileURL is a slippy-map template, with {z}, {x} and {y}.
	//
	// It defaults to OpenStreetMap. **An instance large enough to matter should
	// point this somewhere else**: those servers are funded by donations and
	// their policy asks heavy users to run their own.
	//
	// Empty disables tiles without disabling the map. The pins still draw, on
	// nothing, which is the right answer on a network with no route out.
	TileURL string `json:"tile_url"`
	// Attribution is rendered over the map whenever tiles are.
	//
	// **It is a licence condition of the data, not a courtesy.** An operator
	// who changes TileURL changes this to match; leaving somebody else's
	// attribution over another provider's tiles is worse than none.
	Attribution string `json:"attribution"`
	// MaxZoom bounds how far in the map will go. Tile servers stop at some
	// level and requesting past it fetches nothing but 404s.
	MaxZoom int `json:"max_zoom"`
}

type Database struct {
	// Driver is the database/sql driver name to open. It must already be
	// registered by the binary; see docs/adr/ADR-0005.
	Driver string `json:"driver"`
	// DSN is the data source name passed to the driver.
	DSN string `json:"dsn"`
	// MaxOpenConns bounds concurrent connections. SQLite tolerates many
	// readers but exactly one writer, so this is deliberately small.
	MaxOpenConns int `json:"max_open_conns"`
	// ConnMaxLifetime bounds how long a pooled connection is reused.
	ConnMaxLifetime Duration `json:"conn_max_lifetime"`
	// BusyTimeout is how long SQLite waits for a lock before returning busy.
	BusyTimeout Duration `json:"busy_timeout"`
}

// Logging configures the structured logger.
type Logging struct {
	// Level is one of debug, info, warn, error.
	Level string `json:"level"`
	// Format is one of text, json.
	Format string `json:"format"`
	// IncludeSource adds file and line to each record. Useful in development,
	// costly in hot paths.
	IncludeSource bool `json:"include_source"`
}

// Events configures the internal event bus.
type Events struct {
	// HistorySize is how many past events are retained for reconnecting
	// console clients. Larger values tolerate longer disconnections at the
	// cost of memory.
	HistorySize int `json:"history_size"`
	// SubscriberBuffer is the per-client queue depth. A client that exceeds it
	// is marked lagged and must resynchronise.
	SubscriberBuffer int `json:"subscriber_buffer"`
}

// Default returns the recommended configuration.
//
// Every value here is the one the console displays as "recommended" and
// restores when an operator clicks restore-to-default.
func Default() Config {
	return Config{
		Version: SchemaVersion,
		Server: Server{
			ListenAddress:     "127.0.0.1:8080",
			ReadHeaderTimeout: Duration(5 * time.Second),
			ReadTimeout:       Duration(30 * time.Second),
			WriteTimeout:      Duration(30 * time.Second),
			IdleTimeout:       Duration(120 * time.Second),
			ShutdownTimeout:   Duration(15 * time.Second),
			BehindProxy:       false,
			Map: Map{
				TileURL:     "https://tile.openstreetmap.org/{z}/{x}/{y}.png",
				Attribution: "© OpenStreetMap contributors",
				MaxZoom:     18,
			},
		},
		Database: Database{
			Driver:          "sqlite",
			DSN:             "qsp.db",
			MaxOpenConns:    4,
			ConnMaxLifetime: Duration(time.Hour),
			BusyTimeout:     Duration(5 * time.Second),
		},
		Logging: Logging{
			Level:         "info",
			Format:        "text",
			IncludeSource: false,
		},
		Events: Events{
			HistorySize:      256,
			SubscriberBuffer: 64,
		},
		DMR: DMR{
			// Off by default. A freshly installed QSP must not start accepting
			// connections before an operator has decided it should.
			Enabled:       false,
			ListenAddress: "0.0.0.0:62031",
			PasswordFile:  "",
			PeerTimeout:   Duration(60 * time.Second),
			LoginTimeout:  Duration(30 * time.Second),
			MaxPeers:      200,
			// Thirty days of who transmitted and when. See ADR-0033.
			Calls: Calls{Retain: Duration(30 * 24 * time.Hour)},
			// Two hours: a working shift. Long enough that a private call to
			// somebody who spoke this morning still reaches them, short enough
			// that it does not follow them to yesterday's hotspot.
			SubscriberTimeout: Duration(2 * time.Hour),
			Forwarding:        false,
			Bridges:           nil,
			Triggers:          nil,
			Schedule:          nil,
			// Nil, not an empty block. The listener is off by default, so
			// nothing is exposed; when an operator enables it on a reachable
			// address, the absence is what makes QSP ask them to decide.
			Access: nil,
			// Off, so every peer receives every talkgroup, as before.
			Subscription: Subscription{Enabled: false, Timeout: Duration(15 * time.Minute)},
		},
	}
}

// Duration is a time.Duration that serialises to and from a human-readable
// string such as "30s". Storing raw nanoseconds in a document an operator may
// read or export would be needlessly hostile.
type Duration time.Duration

// AsDuration converts back to time.Duration.
func (d Duration) AsDuration() time.Duration { return time.Duration(d) }

// String implements fmt.Stringer.
func (d Duration) String() string { return time.Duration(d).String() }

// MarshalJSON implements json.Marshaler.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON implements json.Unmarshaler, accepting only a duration string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("duration must be a string such as \"30s\": %w", err)
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("cannot parse duration %q: use a value such as \"500ms\", \"30s\" or \"1h\"", s)
	}
	*d = Duration(parsed)
	return nil
}

// FieldError describes one invalid field.
//
// Field is the JSON path an operator would recognise. Problem states what is
// wrong. Fix states what to do about it. Constitution §12 forbids errors that
// say only that a value is invalid.
type FieldError struct {
	Field   string
	Problem string
	Fix     string
}

// Error implements error.
func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s (%s)", e.Field, e.Problem, e.Fix)
}

// ValidationError aggregates every problem found in one configuration.
type ValidationError struct {
	Errors []FieldError
}

// Error implements error.
func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return "invalid configuration: " + e.Errors[0].Error()
	}
	parts := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		parts = append(parts, fe.Error())
	}
	return fmt.Sprintf("invalid configuration (%d problems): %s", len(e.Errors), strings.Join(parts, "; "))
}

// Fields returns the JSON paths of every invalid field, sorted.
func (e *ValidationError) Fields() []string {
	out := make([]string, 0, len(e.Errors))
	for _, fe := range e.Errors {
		out = append(out, fe.Field)
	}
	sort.Strings(out)
	return out
}

type validator struct{ errs []FieldError }

func (v *validator) add(field, problem, fix string) {
	v.errs = append(v.errs, FieldError{Field: field, Problem: problem, Fix: fix})
}

func (v *validator) positive(field string, value int, fix string) {
	if value <= 0 {
		v.add(field, fmt.Sprintf("must be greater than zero, got %d", value), fix)
	}
}

func (v *validator) positiveDuration(field string, value Duration, fix string) {
	if value <= 0 {
		v.add(field, fmt.Sprintf("must be greater than zero, got %s", value), fix)
	}
}

// Validate reports every problem with c.
//
// It returns nil when the configuration is usable. A non-nil result is always
// of type *ValidationError.
func (c Config) Validate() error {
	v := &validator{}

	switch {
	case c.Version <= 0:
		v.add("version", fmt.Sprintf("must be a positive schema version, got %d", c.Version),
			fmt.Sprintf("set it to %d", SchemaVersion))
	case c.Version > SchemaVersion:
		v.add("version", fmt.Sprintf("document is version %d but this build understands up to %d", c.Version, SchemaVersion),
			"upgrade QSP, or restore a configuration version created by this build")
	}

	if strings.TrimSpace(c.Server.ListenAddress) == "" {
		v.add("server.listen_address", "must not be empty",
			"use \"127.0.0.1:8080\" for local access, or \"0.0.0.0:8080\" to listen on every interface")
	} else if _, _, err := net.SplitHostPort(c.Server.ListenAddress); err != nil {
		v.add("server.listen_address", fmt.Sprintf("%q is not a host:port address", c.Server.ListenAddress),
			"include a port, for example \"127.0.0.1:8080\"")
	}

	v.positiveDuration("server.read_header_timeout", c.Server.ReadHeaderTimeout,
		"use \"5s\"; this bounds slow-header denial of service and must not be disabled")
	v.positiveDuration("server.read_timeout", c.Server.ReadTimeout, "use \"30s\"")
	v.positiveDuration("server.write_timeout", c.Server.WriteTimeout, "use \"30s\"")
	v.positiveDuration("server.idle_timeout", c.Server.IdleTimeout, "use \"120s\"")
	if c.Server.Map.TileURL != "" {
		if !strings.Contains(c.Server.Map.TileURL, "{z}") ||
			!strings.Contains(c.Server.Map.TileURL, "{x}") ||
			!strings.Contains(c.Server.Map.TileURL, "{y}") {
			v.add("server.map.tile_url",
				fmt.Sprintf("%q is not a slippy-map template", c.Server.Map.TileURL),
				"it must contain {z}, {x} and {y}, for example "+
					"\"https://tile.openstreetmap.org/{z}/{x}/{y}.png\"; leave it empty to "+
					"draw the map without tiles")
		}
		if strings.TrimSpace(c.Server.Map.Attribution) == "" {
			// Using somebody's tiles without their attribution is a licence
			// breach, and the operator who changed the URL is the only one who
			// knows what belongs here.
			v.add("server.map.attribution", "must not be empty when tiles are configured",
				"name whoever provides the tiles and the data; for OpenStreetMap that is "+
					"\"© OpenStreetMap contributors\"")
		}
		if z := c.Server.Map.MaxZoom; z < 1 || z > 22 {
			v.add("server.map.max_zoom", fmt.Sprintf("is %d; slippy maps run from 1 to 22", z),
				"use 18, which is as far as most tile servers go")
		}
	}

	if c.DMR.Callsigns.Enabled && strings.TrimSpace(c.DMR.Callsigns.Contact) == "" {
		v.add("dmr.callsigns.contact", "must not be empty when lookups are enabled",
			"the registry asks automated clients to say who they are; use an email "+
				"address a person reads, so they can reach you rather than block you")
	}

	if c.DMR.Parrot.Enabled {
		p := c.DMR.Parrot
		if p.Talkgroup == 0 {
			v.add("dmr.parrot.talkgroup", "must not be 0 when parrot is enabled",
				"choose a talkgroup nothing else on this network uses; 9990 is common, "+
					"but QSP ships no default because it would swallow a number you may want")
		}
		if p.Timeslot != 1 && p.Timeslot != 2 {
			v.add("dmr.parrot.timeslot",
				fmt.Sprintf("is %d; DMR has timeslots 1 and 2", p.Timeslot),
				"use 2 unless the talkgroup lives on timeslot 1")
		}
		if d := p.MaxDuration.AsDuration(); d < 0 || d > 5*time.Minute {
			v.add("dmr.parrot.max_duration", fmt.Sprintf("is %s", d),
				"use something between a few seconds and a minute; a member who transmits "+
					"for longer waits as long again to hear it")
		}
		if d := p.Gap.AsDuration(); d < 0 || d > time.Minute {
			v.add("dmr.parrot.gap", fmt.Sprintf("is %s", d),
				"use a second or two, long enough for a radio to return to receive")
		}
	}

	v.positiveDuration("server.shutdown_timeout", c.Server.ShutdownTimeout,
		"use \"15s\"; this bounds how long in-flight requests may finish during shutdown")

	if strings.TrimSpace(c.Database.Driver) == "" {
		v.add("database.driver", "must not be empty",
			"use \"sqlite\"; the driver must be registered in the binary")
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		v.add("database.dsn", "must not be empty",
			"use \"qsp.db\" for a file in the working directory, or an absolute path")
	}
	v.positive("database.max_open_conns", c.Database.MaxOpenConns,
		"use 4; SQLite permits many readers but only one writer, so a large pool does not help")
	v.positiveDuration("database.conn_max_lifetime", c.Database.ConnMaxLifetime, "use \"1h\"")
	v.positiveDuration("database.busy_timeout", c.Database.BusyTimeout,
		"use \"5s\"; this is how long a query waits for a write lock before failing")

	if _, err := parseLevel(c.Logging.Level); err != nil {
		v.add("logging.level", fmt.Sprintf("%q is not a recognised level", c.Logging.Level),
			"use \"debug\", \"info\", \"warn\" or \"error\"")
	}
	if _, err := parseFormat(c.Logging.Format); err != nil {
		v.add("logging.format", fmt.Sprintf("%q is not a recognised format", c.Logging.Format),
			"use \"text\" for interactive use or \"json\" for production")
	}

	if c.IPSC.Enabled {
		if strings.TrimSpace(c.IPSC.ListenAddress) == "" {
			v.add("ipsc.listen_address", "must not be empty when the IPSC listener is enabled",
				"use \"0.0.0.0:50000\" to accept repeaters on every interface")
		} else if _, _, err := net.SplitHostPort(c.IPSC.ListenAddress); err != nil {
			v.add("ipsc.listen_address", fmt.Sprintf("%q is not a host:port address", c.IPSC.ListenAddress),
				"include a port, for example \"0.0.0.0:50000\"")
		}
		if c.IPSC.MasterID == 0 {
			v.add("ipsc.master_id", "must be set when the IPSC listener is enabled",
				"use this network's own DMR ID; a master announces one and 0 is not one")
		}
		for _, p := range c.IPSC.AllowedPeers {
			if p == c.IPSC.MasterID {
				v.add("ipsc.allowed_peers",
					fmt.Sprintf("%d is also ipsc.master_id", p),
					"give the master an ID of its own; a repeater refuses to register with a master "+
						"carrying its own ID, and retries silently rather than reporting it")
			}
		}
		allowed := make(map[uint32]bool, len(c.IPSC.AllowedPeers))
		for _, p := range c.IPSC.AllowedPeers {
			allowed[p] = true
		}
		for id, name := range c.IPSC.PeerNames {
			trimmed := strings.TrimSpace(name)
			switch {
			case trimmed == "":
				v.add(fmt.Sprintf("ipsc.peer_names.%d", id), "is empty",
					"remove the entry rather than naming a repeater with a blank; "+
						"a blank name reads as a lookup that failed")
			case trimmed != name:
				v.add(fmt.Sprintf("ipsc.peer_names.%d", id),
					fmt.Sprintf("%q has leading or trailing whitespace", name),
					"a name is shown beside a radio ID and the space is invisible there")
			case len(trimmed) > maxPeerName:
				v.add(fmt.Sprintf("ipsc.peer_names.%d", id),
					fmt.Sprintf("is %d characters, over the %d allowed", len(trimmed), maxPeerName),
					"a callsign column is narrow; use the repeater's callsign rather than a description")
			}
			// **A name for a repeater that is not admitted is read by
			// nothing.** That pattern has been the defect nine times in this
			// codebase, so it is an error here rather than a curiosity to
			// find later. An empty allow list admits everybody, so a name is
			// never orphaned by one.
			if len(allowed) > 0 && !allowed[id] {
				v.add(fmt.Sprintf("ipsc.peer_names.%d", id),
					"names a repeater that is not in ipsc.allowed_peers",
					"add the radio ID to the allowed list, or remove the name; as it stands "+
						"the name will never be shown because the repeater is never answered")
			}
		}
		if c.IPSC.PeerTimeoutSeconds < 0 {
			v.add("ipsc.peer_timeout_seconds", "must not be negative",
				"leave it at 0 for the default, or give a value above the fifteen-second keepalive cadence")
		}
		switch {
		case c.IPSC.ColourCode == nil:
			v.add("ipsc.colour_code", "must be set when the IPSC listener is enabled",
				"use the colour code the repeater is programmed with; there is no default because "+
					"a burst carrying the wrong one is rejected silently and presents as no audio")
		case *c.IPSC.ColourCode > 15:
			v.add("ipsc.colour_code", fmt.Sprintf("%d is out of range", *c.IPSC.ColourCode),
				"DMR colour codes run from 0 to 15; use the one the repeater is programmed with")
		}
	}

	if c.DMR.Enabled {
		if strings.TrimSpace(c.DMR.ListenAddress) == "" {
			v.add("dmr.listen_address", "must not be empty when the DMR listener is enabled",
				"use \"0.0.0.0:62031\" to accept peers on every interface")
		} else if _, _, err := net.SplitHostPort(c.DMR.ListenAddress); err != nil {
			v.add("dmr.listen_address", fmt.Sprintf("%q is not a host:port address", c.DMR.ListenAddress),
				"include a port, for example \"0.0.0.0:62031\"")
		}
		if strings.TrimSpace(c.DMR.PasswordFile) == "" {
			v.add("dmr.password_file", "must not be empty when the DMR listener is enabled",
				"create a file containing the shared peer password, mode 0600, and give its path here; "+
					"the password is never stored in this configuration")
		}
		if dir := strings.TrimSpace(c.DMR.PeerPasswords); dir != "" {
			// **Absolute, because the service's working directory is not
			// obvious.** A relative path resolves against wherever systemd
			// happened to start the process, so a member's password would be
			// written somewhere nobody thinks to look and read from somewhere
			// else after a change to the unit file.
			if !filepath.IsAbs(dir) {
				v.add("dmr.peer_passwords", fmt.Sprintf("%q is not an absolute path", dir),
					"give a full path, for example \"/var/lib/qsp/peers\"; a relative one "+
						"resolves against the service's working directory")
			}
			// A directory and a file are different things, and pointing one at
			// the other would put a member's password where the shared one
			// lives, or make the shared password unreadable.
			if dir == strings.TrimSpace(c.DMR.PasswordFile) {
				v.add("dmr.peer_passwords", "is the same path as dmr.password_file",
					"the shared password is a file and this is a directory of per-member "+
						"files; give this one a path of its own")
			}
		}
		v.positiveDuration("dmr.peer_timeout", c.DMR.PeerTimeout,
			"use \"60s\"; peers send a keepalive every 10 s, so this tolerates five losses")
		v.positiveDuration("dmr.login_timeout", c.DMR.LoginTimeout,
			"use \"30s\"; this bounds how long an unfinished login may hold a slot")
		v.positive("dmr.max_peers", c.DMR.MaxPeers,
			"use 200; this bounds memory and the size of a routing decision")
		v.positiveDuration("dmr.subscriber_timeout", c.DMR.SubscriberTimeout,
			"use \"2h\"; this is how long a radio's location is trusted after it last "+
				"transmitted, and is much longer than peer_timeout because a quiet radio "+
				"is not a departed one")

		names := make(map[string]bool, len(c.DMR.Bridges))
		// Which links exist, and which a bridge actually reaches.
		links := map[string]bool{}
		// Which of them speak OpenBridge, because only those force TS1. A
		// Homebrew link to XLX or DMR+ carries both slots and an endpoint on
		// TS2 is perfectly ordinary there.
		openBridgeLinks := map[string]bool{}
		for _, u := range c.DMR.Upstreams {
			if u.Enabled {
				key := strings.ToLower(strings.TrimSpace(u.Name))
				links[key] = true
				if !u.HomebrewProtocol() {
					openBridgeLinks[key] = true
				}
			}
		}
		reached := map[string]bool{}

		for i, b := range c.DMR.Bridges {
			field := fmt.Sprintf("dmr.bridges[%d]", i)
			if strings.TrimSpace(b.Name) == "" {
				v.add(field+".name", "must not be empty",
					"give the bridge a name you will recognise on the console, such as \"tuesday-net\"")
			} else if key := strings.ToLower(strings.TrimSpace(b.Name)); names[key] {
				v.add(field+".name", fmt.Sprintf("%q is used by more than one bridge", b.Name),
					"bridge names must be unique; rename one of them")
			} else {
				names[key] = true
			}
			if len(b.Endpoints) < 2 {
				v.add(field+".endpoints", fmt.Sprintf("has %d endpoint(s)", len(b.Endpoints)),
					"a bridge needs at least 2 endpoints to connect anything")
			}
			for j, e := range b.Endpoints {
				ef := fmt.Sprintf("%s.endpoints[%d]", field, j)
				if e.Talkgroup == 0 {
					v.add(ef+".talkgroup", "must not be 0",
						"use the talkgroup number, for example 3148")
				}
				if e.Timeslot != 1 && e.Timeslot != 2 {
					v.add(ef+".timeslot", fmt.Sprintf("is %d", e.Timeslot),
						"DMR has two timeslots; use 1 or 2")
				}
				link := strings.ToLower(strings.TrimSpace(e.Upstream))
				switch {
				case link == "":
				case e.Peer != 0:
					v.add(ef, "names both a peer and a link",
						"a link is not a peer; set one or the other")
				case !links[link]:
					v.add(ef+".upstream", fmt.Sprintf("%q does not match any enabled link", e.Upstream),
						"check the spelling against dmr.upstreams, and that the link is enabled")
				default:
					reached[link] = true
					// **A bridge that cannot carry is worse than no bridge.**
					// Refused rather than corrected, because silently moving
					// an operator's slot would leave the document saying one
					// thing and the network doing another — and because the
					// configuration this refuses is one in which the link
					// already carries nothing. A startup error naming the fix
					// replaces a day of healthy counters and silence.
					if openBridgeLinks[link] && e.Timeslot != OpenBridgeTimeslot {
						v.add(ef+".timeslot",
							fmt.Sprintf("is %d on an endpoint naming OpenBridge link %q", e.Timeslot, e.Upstream),
							fmt.Sprintf("use %d. OpenBridge passes all traffic on TS1, so an endpoint "+
								"on any other slot matches nothing in either direction; the other "+
								"endpoint of this bridge keeps your own network's slot",
								OpenBridgeTimeslot))
					}
				}
			}
		}

		// **A link nothing routes to is the fault that cost an afternoon.**
		// The socket opens, the far end authenticates, the health report says
		// no traffic has arrived, and the advice sends an operator to check
		// somebody else's address — while no configuration on this side could
		// ever have put a frame on it.
		for i, u := range c.DMR.Upstreams {
			if !u.Enabled {
				continue
			}
			// **A QSP link needs no bridge and must not be asked for one.**
			// It is a peer (ADR-0051): repeat reaches it the way repeat
			// reaches a hotspot, and every talkgroup crosses. Demanding a
			// bridge here would reintroduce the endpoint that has to carry a
			// timeslot, which is the whole fault the record was written
			// after.
			if u.QSPLink() {
				continue
			}
			if !reached[strings.ToLower(strings.TrimSpace(u.Name))] {
				v.add(fmt.Sprintf("dmr.upstreams[%d]", i),
					fmt.Sprintf("no bridge sends anything to %q", u.Name),
					"add a bridge with an endpoint naming this link, or the link will "+
						"open, authenticate and carry nothing")
			}
		}

		for i, tr := range c.DMR.Triggers {
			field := fmt.Sprintf("dmr.triggers[%d]", i)
			if strings.TrimSpace(tr.Bridge) == "" {
				v.add(field+".bridge", "must name the bridge this trigger opens",
					"use the name of one of the bridges in dmr.bridges")
			} else if !names[strings.ToLower(strings.TrimSpace(tr.Bridge))] {
				v.add(field+".bridge", fmt.Sprintf("%q does not match any configured bridge", tr.Bridge),
					"check the spelling against dmr.bridges")
			}
			if len(tr.On) == 0 {
				v.add(field+".on", "is empty, so nothing could ever open the bridge",
					"list the endpoints that should open it, usually the local repeater's talkgroup")
			}
			for j, e := range tr.On {
				ef := fmt.Sprintf("%s.on[%d]", field, j)
				if e.Talkgroup == 0 {
					v.add(ef+".talkgroup", "must not be 0", "use the talkgroup number, for example 3148")
				}
				if e.Timeslot != 1 && e.Timeslot != 2 {
					v.add(ef+".timeslot", fmt.Sprintf("is %d", e.Timeslot), "DMR has two timeslots; use 1 or 2")
				}
			}
			if tr.HangTime < 0 {
				v.add(field+".hang_time", "must not be negative", "use \"3m\", or omit it for the default")
			}
		}

		// Upstreams. Each link puts a club's audio on somebody else's network,
		// so the errors here name the consequence rather than the field.
		upstreamNames := make(map[string]bool, len(c.DMR.Upstreams))
		// Static attachments name local peers; a homebrew upstream must not
		// claim an ID one of them already uses.
		localPeerIDs := make(map[uint32]bool, len(c.DMR.Subscription.Static))
		for _, a := range c.DMR.Subscription.Static {
			localPeerIDs[a.Peer] = true
		}

		for i, u := range c.DMR.Upstreams {
			field := fmt.Sprintf("dmr.upstreams[%d]", i)

			switch strings.ToLower(strings.TrimSpace(u.Protocol)) {
			case "", UpstreamOpenBridge, UpstreamHomebrew, UpstreamQSP:
			default:
				v.add(field+".protocol", fmt.Sprintf("%q is not a protocol QSP speaks", u.Protocol),
					"use \"qsp\" to link to another QSP server, \"openbridge\" to bridge to "+
						"a network that speaks it, or \"homebrew\" to log into another master "+
						"as a peer")
			}

			if err := ValidUpstreamName(u.Name); err != nil {
				// **A link name becomes a file path**: the passphrase is
				// written to name + ".pass" beside the peer password file and
				// deleted from there when the link is removed. Checked here as
				// well as in the handler so a hand-edited document cannot do
				// what the form now refuses.
				v.add(field+".name", err.Error(),
					"name it after the network it reaches, such as \"brandmeister\"")
			} else if key := strings.ToLower(strings.TrimSpace(u.Name)); upstreamNames[key] {
				v.add(field+".name", fmt.Sprintf("%q is used by more than one upstream", u.Name),
					"names appear in the health report; give each link a distinct one")
			} else {
				upstreamNames[key] = true
			}

			// A disabled link is a note about intent, not a live connection, so
			// only its name is checked. Requiring a passphrase file for a link
			// switched off would stop an operator from writing down a
			// configuration before they have been granted the bridge.
			if !u.Enabled {
				continue
			}

			if strings.TrimSpace(u.Address) == "" {
				v.add(field+".address", "must not be empty when the link is enabled",
					"use the far end's host:port, such as \"3102.master.brandmeister.network:62035\"")
			} else if _, _, err := net.SplitHostPort(u.Address); err != nil {
				v.add(field+".address", fmt.Sprintf("%q is not host:port", u.Address),
					"OpenBridge conventionally uses port 62035")
			}

			// The two protocols need different things, and requiring
			// OpenBridge's fields of a homebrew link would be asking for a
			// listen address and a passphrase that link will never use.
			if u.HomebrewProtocol() {
				c.validateHomebrewUpstream(v, field, u, localPeerIDs)
				continue
			}

			if strings.TrimSpace(u.ListenAddress) == "" {
				v.add(field+".listen_address", "must not be empty when the link is enabled",
					"OpenBridge has no connection setup, so the far end sends to an address "+
						"agreed in advance; use \"0.0.0.0:62035\"")
			} else if _, _, err := net.SplitHostPort(u.ListenAddress); err != nil {
				v.add(field+".listen_address", fmt.Sprintf("%q is not host:port", u.ListenAddress),
					"use \"0.0.0.0:62035\"")
			}

			if u.NetworkID == 0 {
				v.add(field+".network_id", "must not be 0",
					"use the DMR ID the far end expects; it identifies this server in every frame sent")
			}

			if strings.TrimSpace(u.PassphraseFile) == "" {
				v.add(field+".passphrase_file", "must not be empty when the link is enabled",
					"create a file containing the passphrase agreed with the far end, mode 0600, "+
						"and give its path here")
			}

			// **There was a rule here requiring export or import.** It was
			// written when those lists were expected to be the routing
			// mechanism, and they never became one: no code has ever read them
			// to move a frame. A bridge naming the link does that, and is
			// checked below.
			//
			// Two rules that did not know about each other is worse than
			// either alone. A configuration with a bridge and no export
			// satisfied one and failed the other, and the failure was a
			// service that would not start — on a live network, because the
			// rules disagreed rather than because anything was wrong.

			if u.StaleAfter < 0 {
				v.add(field+".stale_after", "must not be negative",
					"use \"4h\", or 0 to disable the warning")
			}

			for _, dir := range []struct {
				name string
				tgs  []UpstreamTalkgroup
			}{{"export", u.Export}, {"import", u.Import}} {
				for j, tg := range dir.tgs {
					sub := fmt.Sprintf("%s.%s[%d]", field, dir.name, j)
					if tg.Talkgroup == 0 {
						v.add(sub+".talkgroup", "must not be 0",
							"use the talkgroup as it exists on this network, not as the far end names it")
					}
					if tg.Timeslot != 1 && tg.Timeslot != 2 {
						v.add(sub+".timeslot", fmt.Sprintf("is %d", tg.Timeslot),
							"DMR has two timeslots; use 1 or 2. Traffic is moved to TS1 for the "+
								"link itself, which QSP does for you")
					}
				}
			}
		}

		// Join. What a member is told to dial has to be a talkgroup this
		// instance will actually relay, or they reach silence and blame the
		// software. QSP can check that, and it is the one part of onboarding
		// an admin cannot verify without a second radio.
		reachable := make(map[[2]uint32]bool)
		for _, b := range c.DMR.Bridges {
			for _, e := range b.Endpoints {
				reachable[[2]uint32{e.Talkgroup, uint32(e.Timeslot)}] = true
			}
		}

		seen := make(map[[2]uint32]string, len(c.DMR.Join.Talkgroups))
		for i, tg := range c.DMR.Join.Talkgroups {
			field := fmt.Sprintf("dmr.join.talkgroups[%d]", i)

			if strings.TrimSpace(tg.Name) == "" {
				v.add(field+".name", "must not be empty",
					"name it the way the club refers to it, such as \"Club chat\"")
			}
			if tg.Dialled == 0 {
				v.add(field+".dialled", "must not be 0",
					"use the number a member enters into their radio, which their hotspot may rewrite")
			}
			if tg.Timeslot != 1 && tg.Timeslot != 2 {
				v.add(field+".timeslot", fmt.Sprintf("is %d", tg.Timeslot),
					"DMR has two timeslots; use 1 or 2")
			}

			key := [2]uint32{tg.Target(), uint32(tg.Timeslot)}
			if prior, dup := seen[key]; dup {
				v.add(field, fmt.Sprintf("arrives as TG %d on TS %d, the same as %q",
					tg.Target(), tg.Timeslot, prior),
					"two entries that arrive identically are indistinguishable to a member; remove one")
			}
			seen[key] = tg.Name

			// **Only meaningful when bridges are the only way traffic moves.**
			// With dmr.forwarding on, a repeating master carries every
			// talkgroup between peers and no bridge is involved — which is how
			// most clubs run, and how this one does. Complaining that no
			// bridge carries a talkgroup is then simply untrue, and it refused
			// to save a configuration describing a network that works.
			//
			// The third place this project has found the same mistake: a rule
			// written when bridging was the whole routing model, left behind by
			// ADR-0019, and correct-looking until somebody ran it.
			if !c.DMR.Forwarding && len(c.DMR.Bridges) > 0 &&
				tg.Timeslot >= 1 && tg.Timeslot <= 2 &&
				tg.Dialled != 0 && !reachable[key] {
				v.add(field, fmt.Sprintf("tells members to dial %d, arriving as TG %d on TS %d, "+
					"which no bridge carries", tg.Dialled, tg.Target(), tg.Timeslot),
					"add that talkgroup to a bridge, or correct \"arrives\" to match the rewrite "+
						"in the hotspot's DMRGateway configuration")
			}
		}

		for i, w := range c.DMR.Schedule {
			field := fmt.Sprintf("dmr.schedule[%d]", i)
			if strings.TrimSpace(w.Bridge) == "" {
				v.add(field+".bridge", "must name the bridge this window enables",
					"use the name of one of the bridges in dmr.bridges")
			} else if !names[strings.ToLower(strings.TrimSpace(w.Bridge))] {
				v.add(field+".bridge", fmt.Sprintf("%q does not match any configured bridge", w.Bridge),
					"check the spelling against dmr.bridges; a window naming a bridge that does not "+
						"exist would leave you waiting for a net that never links")
			}
			if len(w.Days) == 0 {
				v.add(field+".days", "is empty, so the window would never run",
					"list weekdays as numbers, 0 for Sunday through 6 for Saturday")
			}
			for _, d := range w.Days {
				if d < 0 || d > 6 {
					v.add(field+".days", fmt.Sprintf("contains %d", d),
						"use 0 for Sunday through 6 for Saturday")
					break
				}
			}
			if _, _, err := parseClock(w.Start); err != nil {
				v.add(field+".start", fmt.Sprintf("%q is not a time of day", w.Start),
					"use 24-hour HH:MM, for example \"20:00\"")
			}
			if w.Duration <= 0 {
				v.add(field+".duration", "must be greater than zero",
					"use \"1h\" for a one-hour net")
			}
			if strings.TrimSpace(w.Timezone) == "" {
				v.add(field+".timezone", "must not be empty",
					"use an IANA name such as \"America/Chicago\", not an abbreviation like \"CST\"")
			}
		}

		// **There was a rule here refusing forwarding with no bridges**, on the
		// grounds that QSP would relay nothing. That was true while bridging
		// was the whole routing model and became false the day the master
		// learned to repeat: a club whose members share one talkgroup
		// configures no bridges and works exactly as intended. The health
		// check was corrected for ADR-0019 and this was not, so the field's
		// own documentation twenty lines above described a configuration the
		// validator refused.
		//
		// Inverting it was the second mistake and lasted one test run. Off
		// with no bridges is the observation mode Forwarding exists to
		// provide — running as a master and watching peers connect before
		// putting audio on anybody's repeater.
		//
		// **Both states are legitimate, so neither is a validation error.**
		// This was a judgement about what an operator probably meant, and
		// that belongs in the health report, which says plainly that
		// forwarding is off and peers cannot hear each other. A validator
		// refuses what cannot work; it does not guess at intent.
	}

	v.positive("events.history_size", c.Events.HistorySize,
		"use 256; this is how many events a reconnecting console client can replay")
	v.positive("events.subscriber_buffer", c.Events.SubscriberBuffer,
		"use 64; a client exceeding this is marked lagged and resynchronises")

	if c.DMR.Enabled && c.DMR.Subscription.Enabled {
		v.positiveDuration("dmr.subscription.timeout", c.DMR.Subscription.Timeout,
			"use \"15m\"; this is how long a talkgroup a member attached by transmitting "+
				"keeps arriving after they stop")
		for i, a := range c.DMR.Subscription.Static {
			field := fmt.Sprintf("dmr.subscription.static[%d]", i)
			if a.Peer == 0 {
				v.add(field+".peer", "must name a repeater or hotspot ID",
					"use the ID the peer registers with; 0 is not a station")
			}
			if a.Talkgroup == 0 {
				v.add(field+".talkgroup", "must name a talkgroup",
					"use the talkgroup this peer should always receive")
			}
			if a.Timeslot != 1 && a.Timeslot != 2 {
				v.add(field+".timeslot", fmt.Sprintf("is %d; DMR has two timeslots", a.Timeslot),
					"use 1 or 2")
			}
		}
	}

	c.validateAccess(v)
	// Last, because it reads addresses the rules above have already reported
	// as malformed, and one mistake should produce one error.
	c.validateListeners(v)

	if len(v.errs) == 0 {
		return nil
	}
	return &ValidationError{Errors: v.errs}
}

// Load reads and validates a configuration document.
//
// Unknown fields are rejected: a typo in a field name must not silently leave
// the default in place, because the operator would believe a setting had been
// applied when it had not.
func Load(r io.Reader) (Config, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	cfg := Default()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("cannot read configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Save writes c as indented JSON.
//
// Save refuses to write an invalid configuration. Persisting something that
// cannot be loaded again would strand the operator.
func Save(w io.Writer, c Config) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("refusing to write an invalid configuration: %w", err)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(c)
}

// parseLevel and parseFormat duplicate the logging package's parsers so that
// config does not depend on logging. The duplication is two switch statements
// and keeps the dependency graph acyclic; both are covered by tests.
func parseLevel(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug", "info", "warn", "warning", "error":
		return strings.ToLower(strings.TrimSpace(s)), nil
	default:
		return "", fmt.Errorf("unknown level %q", s)
	}
}

func parseFormat(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "text", "json":
		return strings.ToLower(strings.TrimSpace(s)), nil
	default:
		return "", fmt.Errorf("unknown format %q", s)
	}
}

// CheckPeerPasswordMode reports whether a peer password file's permissions are
// tight enough to hold a shared secret.
//
// A password readable by every account on the host is not a shared secret, and
// nothing about that failure is visible: the file works, peers authenticate,
// and the exposure is silent. ssh refuses a private key with loose permissions
// for the same reason, and this follows that precedent rather than warning and
// continuing — a warning in a log nobody reads is not a control.
//
// Only the group and world bits matter. The owner's bits are their business,
// and the execute bit, while meaningless here, is not a disclosure.
//
// This takes a mode rather than a path so that it is testable without touching
// a filesystem, and so the decision about which platforms can enforce it lives
// with the caller. Windows cannot: os.Stat synthesises a mode from the
// read-only attribute and reports 0666 for an ordinary file whatever its ACL
// says, so enforcing this there would reject every correctly secured file.
func CheckPeerPasswordMode(mode fs.FileMode) error {
	if perm := mode.Perm() & 0o077; perm != 0 {
		return fmt.Errorf("the peer password file is mode %#o, readable beyond its owner; "+
			"run chmod 600 on it — a shared secret every account on this host can read "+
			"is not a secret", mode.Perm())
	}
	return nil
}

// LoadPeerPassword reads the shared peer password from DMR.PasswordFile.
//
// The password never enters Config, so it never reaches the version history,
// an export, or a diff. Reading it is a separate, explicit act.
//
// An empty or whitespace-only file is an error: it would otherwise authenticate
// every peer that guessed an empty password.
func LoadPeerPassword(read func(string) ([]byte, error), path string) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("no peer password file is configured; set dmr.password_file")
	}
	raw, err := read(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read the peer password file %q: %w "+
			"(create it with the shared password as its only contents, mode 0600)", path, err)
	}
	password := strings.TrimSpace(string(raw))
	if password == "" {
		return nil, fmt.Errorf("the peer password file %q is empty; "+
			"put the shared password in it, or no peer can be authenticated safely", path)
	}
	return []byte(password), nil
}

// parseClock reads a 24-hour HH:MM time of day.
//
// It duplicates the scheduler's parser so that config depends on nothing, in
// keeping with the dependency rule that config imports only the standard
// library. Both are covered by tests.
func parseClock(s string) (hour, minute int, err error) {
	trimmed := strings.TrimSpace(s)
	if _, err := fmt.Sscanf(trimmed, "%d:%d", &hour, &minute); err != nil {
		return 0, 0, fmt.Errorf("cannot read %q as HH:MM", s)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("%q is not a valid time of day", s)
	}
	return hour, minute, nil
}
