// Package routing decides where a transmission should go.
//
// This package is pure. It performs no I/O, holds no sockets, and forwards
// nothing: given a table of bridges and a call arriving somewhere, it returns
// the list of places that call should be delivered to. Whether anything acts on
// that list is the caller's business.
//
// That separation is deliberate. Routing is the centre of QSP and the part
// where a mistake is least visible and most damaging — a wrong decision does
// not crash, it quietly sends a net onto the wrong talkgroup or loops audio
// back on itself. Keeping the decision pure means the whole of it can be tested
// as a table of inputs and expected outputs, with no network in the way.
//
// # The model
//
// An [Endpoint] is a place traffic can arrive at or be sent to: one talkgroup,
// on one timeslot, at one peer. A [Bridge] joins two or more endpoints, and a
// call arriving at one is delivered to the others.
//
// This is the model that makes scheduled and PTT-triggered bridging possible
// later: a bridge is a value that can be enabled and disabled, so a schedule is
// something that flips Enabled rather than a separate mechanism bolted on.
package routing

import (
	"fmt"
	"sort"
	"strings"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// AnyPeer matches every peer.
//
// A bridge endpoint with AnyPeer means "this talkgroup and timeslot, wherever
// it appears", which is how a club links a talkgroup across every repeater it
// owns without listing them.
const AnyPeer = hbp.RepeaterID(0)

// Endpoint is one place traffic arrives at or is sent to.
type Endpoint struct {
	// Peer is the peer this endpoint lives at, or AnyPeer for all of them.
	//
	// Ignored when Upstream is set: a link is not a peer, and the two are
	// mutually exclusive.
	Peer hbp.RepeaterID
	// Upstream names a link to another network, empty for an ordinary peer
	// endpoint.
	//
	// An upstream is a destination like any other as far as the routing
	// decision is concerned — it takes part in contention, it is refused when
	// busy, and it is counted in the same drops. What differs is only how the
	// frame leaves, which is the caller's business rather than this package's.
	// See docs/adr/ADR-0018-openbridge.md.
	Upstream string
	// Transcoder names a vocoder channel, empty for anything else.
	//
	// **A transcoder is a third kind of destination and not a variety of the
	// first two**, because it contends like a peer and configures like a
	// link. ADR-0063 is the reasoning; the short version is that an
	// AMBE-3000 is one channel and can physically carry one call, which is
	// ADR-0022's timeslot case rather than its link case. Reusing Upstream
	// would have given it an OpenBridge link's contention key and delivered
	// two talkgroups to one chip.
	//
	// Mutually exclusive with Peer and Upstream: an endpoint is one of the
	// three.
	Transcoder string
	// Talkgroup is the talkgroup ID.
	Talkgroup uint32
	// Timeslot is the DMR timeslot.
	Timeslot hbp.Timeslot
}

// String implements fmt.Stringer.
func (e Endpoint) String() string {
	if e.Transcoder != "" {
		return fmt.Sprintf("transcoder %s TG%d TS%d", e.Transcoder, e.Talkgroup, e.Timeslot)
	}
	if e.Upstream != "" {
		return fmt.Sprintf("upstream %s TG%d TS%d", e.Upstream, e.Talkgroup, e.Timeslot)
	}
	peer := "any"
	if e.Peer != AnyPeer {
		peer = fmt.Sprintf("%d", e.Peer)
	}
	return fmt.Sprintf("%s/TG%d/%s", peer, e.Talkgroup, e.Timeslot)
}

// Matches reports whether a call arriving at actual belongs to this endpoint.
//
// An endpoint with AnyPeer matches any peer; otherwise the peer must be equal.
// Talkgroup and timeslot must always match exactly: delivering a talkgroup to
// the wrong timeslot would collide with whatever else is using it.
func (e Endpoint) Matches(actual Endpoint) bool {
	if e.Talkgroup != actual.Talkgroup || e.Timeslot != actual.Timeslot {
		return false
	}

	// The three kinds are never each other, whatever their talkgroups.
	//
	// Without this, a non-peer endpoint carries AnyPeer by default, AnyPeer
	// matches everything, and the routing table concludes that the link *is*
	// the peer that just transmitted — so it declines to send the frame there,
	// on the grounds that a call is never sent back where it came from. The
	// bridge then appears configured and carries nothing. That shipped once
	// for links, and a transcoder endpoint has exactly the same default.
	if e.kind() != actual.kind() {
		return false
	}
	switch {
	case e.Transcoder != "":
		return e.Transcoder == actual.Transcoder
	case e.Upstream != "":
		return e.Upstream == actual.Upstream
	}

	return e.Peer == AnyPeer || e.Peer == actual.Peer
}

// kind names which of the three sorts of destination this is, so that the
// comparison is one switch rather than a chain of exclusions that has to be
// extended correctly every time a kind is added.
type endpointKind uint8

const (
	kindPeer endpointKind = iota
	kindUpstream
	kindTranscoder
)

func (e Endpoint) kind() endpointKind {
	switch {
	case e.Transcoder != "":
		return kindTranscoder
	case e.Upstream != "":
		return kindUpstream
	}
	return kindPeer
}

// Validate reports whether the endpoint is usable.
func (e Endpoint) Validate() error {
	if e.Upstream != "" && e.Transcoder != "" {
		return fmt.Errorf("endpoint names both upstream %q and transcoder %q; an endpoint is one kind",
			e.Upstream, e.Transcoder)
	}
	if e.Transcoder != "" && e.Peer != AnyPeer {
		return fmt.Errorf("endpoint names both transcoder %q and peer %d; an endpoint is one kind",
			e.Transcoder, e.Peer)
	}
	if e.Upstream != "" && e.Peer != AnyPeer {
		return fmt.Errorf("endpoint names both upstream %q and peer %d; an endpoint is one or the other",
			e.Upstream, e.Peer)
	}
	if e.Talkgroup == 0 {
		return fmt.Errorf("endpoint has talkgroup 0, which is not a valid destination")
	}
	if e.Timeslot != hbp.Timeslot1 && e.Timeslot != hbp.Timeslot2 {
		return fmt.Errorf("endpoint has timeslot %d; DMR has two timeslots, 1 and 2", e.Timeslot)
	}
	return nil
}

// Permission records which peers may receive audio from one transcoder.
//
// **The default is nobody, and that is the whole point of the type.**
// ADR-0062 requires that a repeater owner opt in before transcoded audio
// appears on their machine, because a Zello user is not necessarily licensed
// and their audio reaches RF. An unlicensed transmission on a licensed
// operator's repeater is that operator's problem, not the network's, so it
// cannot be something a default arranges on their behalf.
//
// The zero value therefore permits nothing. That is the opposite of
// [access.List], which is permissive when empty — and the difference is
// deliberate: an access list governs who may use a network the operator
// already runs, while this governs whether somebody else's licence is put at
// risk.
type Permission struct {
	// Peers are the repeater IDs that have opted in.
	//
	// **Zero is not a wildcard here.** Elsewhere in this package AnyPeer is
	// 0 and matches everything, and carrying that convention into a
	// permission would mean a stray zero in a list silently permitted every
	// repeater on the network. A zero entry is ignored.
	Peers []hbp.RepeaterID
	// All permits every registered peer.
	//
	// A separate boolean rather than a wildcard entry, so that permitting
	// everything is a sentence an operator wrote on purpose and not a
	// consequence of a malformed list.
	All bool
}

// Permits reports whether a peer has opted in to transcoded audio.
//
// **AnyPeer is permitted only by All.** A bridge endpoint may name AnyPeer,
// meaning "this talkgroup wherever it appears", and resolving that to a list
// of repeaters needs the set of registered peers — which this package does not
// have and should not, because it is pure. So a transcoded call bridged to
// AnyPeer is withheld unless the operator has said every peer may receive
// transcoded audio, which is the safe reading of an unresolvable permission
// rather than a limitation.
func (p Permission) Permits(peer hbp.RepeaterID) bool {
	if p.All {
		return true
	}
	for _, id := range p.Peers {
		if id != 0 && id == peer {
			return true
		}
	}
	return false
}

// Bridge joins endpoints so that traffic arriving at one reaches the others.
type Bridge struct {
	// Name identifies the bridge to an operator. It must be unique.
	Name string
	// Enabled reports whether the bridge is currently carrying traffic.
	//
	// Scheduled and PTT-triggered bridging are expressed by flipping this, not
	// by a separate mechanism.
	Enabled bool
	// Endpoints are the places this bridge joins. At least two are required:
	// a bridge with one endpoint connects nothing.
	Endpoints []Endpoint
}

// Validate reports every problem with the bridge.
func (b Bridge) Validate() error {
	if strings.TrimSpace(b.Name) == "" {
		return fmt.Errorf("a bridge must have a name so that an operator can identify it")
	}
	if len(b.Endpoints) < 2 {
		return fmt.Errorf("bridge %q has %d endpoint(s); a bridge needs at least 2 to connect anything",
			b.Name, len(b.Endpoints))
	}
	seen := make(map[Endpoint]bool, len(b.Endpoints))
	for i, e := range b.Endpoints {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("bridge %q endpoint %d: %w", b.Name, i, err)
		}
		if seen[e] {
			return fmt.Errorf("bridge %q lists endpoint %s twice; remove the duplicate", b.Name, e)
		}
		seen[e] = true
	}
	return nil
}

// Table is the set of configured bridges.
//
// It is immutable once built. A configuration change produces a new Table which
// the routing core swaps in atomically, so a call is never routed against a
// half-applied configuration (invariant I3).
type Table struct {
	bridges []Bridge
	// permissions is keyed by transcoder name. A name absent from the map
	// permits nothing, which is the same as a zero Permission — so a
	// transcoder nobody has opted in to cannot deliver to any repeater.
	permissions map[string]Permission
}

// TableOption configures a table.
type TableOption func(*Table)

// WithPermissions sets which peers may receive audio from each transcoder,
// keyed by transcoder name.
//
// Omitting it, or omitting a name from the map, permits nothing — see
// [Permission].
func WithPermissions(p map[string]Permission) TableOption {
	return func(t *Table) {
		t.permissions = make(map[string]Permission, len(p))
		for name, perm := range p {
			t.permissions[name] = Permission{
				Peers: append([]hbp.RepeaterID(nil), perm.Peers...),
				All:   perm.All,
			}
		}
	}
}

// NewTable validates and builds a routing table.
//
// It returns every problem it finds rather than the first, because an operator
// fixing a form should see all of them at once.
func NewTable(bridges []Bridge, opts ...TableOption) (*Table, error) {
	var problems []string
	names := make(map[string]bool, len(bridges))

	for _, b := range bridges {
		if err := b.Validate(); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		key := strings.ToLower(strings.TrimSpace(b.Name))
		if names[key] {
			problems = append(problems, fmt.Sprintf("two bridges are named %q; names must be unique", b.Name))
			continue
		}
		names[key] = true
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid routing table: %s", strings.Join(problems, "; "))
	}

	// Copy so that a caller mutating its slice afterwards cannot change an
	// active table.
	out := make([]Bridge, len(bridges))
	for i, b := range bridges {
		out[i] = b
		out[i].Endpoints = append([]Endpoint(nil), b.Endpoints...)
	}
	t := &Table{bridges: out}
	for _, opt := range opts {
		opt(t)
	}
	return t, nil
}

// Decision is the outcome of routing one call.
type Decision struct {
	// Targets are the endpoints the call should be delivered to, ordered and
	// free of duplicates. Empty when the call goes nowhere.
	Targets []Endpoint
	// Bridges names the bridges that produced the targets, for the operator.
	Bridges []string
	// Reason explains an empty result.
	//
	// "Why didn't this call route?" is the question this package exists to
	// answer, and an empty list with no explanation is the worst possible
	// answer to it.
	Reason string
	// Withheld are endpoints a bridge named and permission refused.
	//
	// **Separate from Targets rather than simply missing from it**, because a
	// repeater that has not opted in to transcoded audio looks exactly like a
	// repeater nobody bridged — and those two need different answers from an
	// operator. Naming the refused destination is the same requirement the
	// COLLISIONS counter failed: a drop an operator cannot attribute cannot
	// be acted on.
	Withheld []Endpoint
}

// Routed reports whether the call has anywhere to go.
func (d Decision) Routed() bool { return len(d.Targets) > 0 }

// Route decides where a call arriving at from should be delivered.
//
// Three properties hold for every result:
//
//  1. The source endpoint is never a target. Delivering a call back to the
//     peer that sent it is an echo, and on a repeater it is feedback.
//  2. Targets are unique. Two bridges joining the same pair of endpoints
//     deliver one copy, not two.
//  3. The order is deterministic, so the same inputs always produce the same
//     output and a test or a log can be compared.
//
// Route does not consider whether a target is currently busy. Contention is a
// separate concern belonging to the routing core, which knows what is in
// flight; this function knows only the configuration.
func (t *Table) Route(from Endpoint) Decision {
	if t == nil || len(t.bridges) == 0 {
		return Decision{Reason: "no bridges are configured"}
	}

	var (
		targets    []Endpoint
		seen       = make(map[Endpoint]bool)
		used       []string
		matchedAny bool
		disabled   []string
	)

	for _, b := range t.bridges {
		if !b.matches(from) {
			continue
		}
		matchedAny = true
		if !b.Enabled {
			disabled = append(disabled, b.Name)
			continue
		}

		// carries reports whether this bridge would deliver the call
		// somewhere, independently of whether another bridge already added
		// that target.
		//
		// Reporting only the bridge that happened to add a target first would
		// mislead an operator: with two overlapping bridges, disabling the
		// named one would not stop the call, because the unnamed one still
		// carries it.
		var carries bool
		for _, e := range b.Endpoints {
			// Never send a call back where it came from.
			if e.Matches(from) {
				continue
			}
			carries = true
			if seen[e] {
				continue
			}
			seen[e] = true
			targets = append(targets, e)
		}
		if carries {
			used = append(used, b.Name)
		}
	}

	// **Transcoded audio reaches a repeater only if its owner said so.**
	// ADR-0062: a Zello user is not necessarily licensed and their audio
	// reaches RF, so this is opt-in per repeater and the default is nobody.
	// Applied here rather than at delivery because the decision is
	// configuration, and this is the package that owns configuration
	// decisions — and because a caller that had to remember to check would
	// eventually be a caller that forgot.
	var withheld []Endpoint
	if from.Transcoder != "" {
		permitted := t.permissions[from.Transcoder]
		kept := targets[:0:0]
		for _, e := range targets {
			if e.Transcoder == "" && e.Upstream == "" && !permitted.Permits(e.Peer) {
				withheld = append(withheld, e)
				continue
			}
			kept = append(kept, e)
		}
		targets = kept
	}

	sortEndpoints(targets)
	sortEndpoints(withheld)
	sort.Strings(used)

	d := Decision{Targets: targets, Bridges: used, Withheld: withheld}
	if len(targets) > 0 {
		return d
	}

	switch {
	case len(withheld) > 0:
		names := make([]string, 0, len(withheld))
		for _, e := range withheld {
			names = append(names, e.String())
		}
		d.Reason = fmt.Sprintf("no peer bridged to transcoder %q permits transcoded "+
			"audio: %s. A repeater receives it only if its owner opted in, because "+
			"a transcoded transmission may come from an unlicensed user and reaches RF",
			from.Transcoder, strings.Join(names, ", "))
	case !matchedAny:
		d.Reason = fmt.Sprintf("no bridge includes %s", from)
	case len(disabled) > 0:
		sort.Strings(disabled)
		d.Reason = fmt.Sprintf("the only bridge(s) for %s are disabled: %s", from, strings.Join(disabled, ", "))
	default:
		d.Reason = fmt.Sprintf("every endpoint bridged to %s is the source itself", from)
	}
	return d
}

// matches reports whether any of the bridge's endpoints covers from.
func (b Bridge) matches(from Endpoint) bool {
	for _, e := range b.Endpoints {
		if e.Matches(from) {
			return true
		}
	}
	return false
}

// Bridges returns a copy of the configured bridges, ordered by name.
func (t *Table) Bridges() []Bridge {
	if t == nil {
		return nil
	}
	out := make([]Bridge, len(t.bridges))
	for i, b := range t.bridges {
		out[i] = b
		out[i].Endpoints = append([]Endpoint(nil), b.Endpoints...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// EnabledCount returns the number of bridges currently carrying traffic.
func (t *Table) EnabledCount() int {
	if t == nil {
		return 0
	}
	n := 0
	for _, b := range t.bridges {
		if b.Enabled {
			n++
		}
	}
	return n
}

func sortEndpoints(e []Endpoint) {
	sort.Slice(e, func(i, j int) bool { return endpointBefore(e[i], e[j]) })
}

// endpointBefore is the ordering sortEndpoints applies, as a comparison, so a
// list of something carrying an endpoint can be put in the same order rather
// than in one that looks like it.
func endpointBefore(a, b Endpoint) bool {
	if a.Peer != b.Peer {
		return a.Peer < b.Peer
	}
	if a.Talkgroup != b.Talkgroup {
		return a.Talkgroup < b.Talkgroup
	}
	return a.Timeslot < b.Timeslot
}
