package config

import (
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/k9mls/qsp/internal/access"
)

// AccessLists parses the configured lists into the form the master and the
// routing core evaluate.
//
// A nil Access yields the zero Lists, which permits everything. Callers do not
// have to special-case the absence, which is deliberate: an access check that
// is skipped when unconfigured is one that can be skipped by accident.
func (c Config) AccessLists() (access.Lists, error) {
	if c.DMR.Access == nil {
		return access.Lists{}, nil
	}
	a := c.DMR.Access

	var (
		lists access.Lists
		err   error
	)
	for _, spec := range []struct {
		field string
		kind  access.Kind
		acl   ACL
		into  *access.List
	}{
		{"dmr.access.registration", access.Registration, a.Registration, &lists.Registration},
		{"dmr.access.subscribers", access.Subscriber, a.Subscribers, &lists.Subscriber},
		{"dmr.access.talkgroups.timeslot_1", access.Talkgroup, a.Talkgroups.Timeslot1, &lists.Talkgroup1},
		{"dmr.access.talkgroups.timeslot_2", access.Talkgroup, a.Talkgroups.Timeslot2, &lists.Talkgroup2},
	} {
		*spec.into, err = access.Parse(spec.field, spec.kind, access.Mode(spec.acl.Mode), spec.acl.IDs)
		if err != nil {
			return access.Lists{}, err
		}
	}
	return lists, nil
}

// AccessAdvisories reports entries that parse but look like mistakes.
//
// These are logged at startup and never block it. The numbering they check is a
// registry convention rather than a rule of the protocol, so refusing on one
// would make QSP wrong the day the convention changed.
func (c Config) AccessAdvisories() []string {
	lists, err := c.AccessLists()
	if err != nil {
		// An invalid configuration is reported by Validate, in full. Repeating
		// a subset of it here as advice would be noise.
		return nil
	}
	return lists.Registration.Advisories("dmr.access.registration", access.Registration)
}

// validateAccess adds every problem with the access block, and the one problem
// that is the absence of it.
func (c Config) validateAccess(v *validator) {
	if !c.DMR.Enabled {
		return
	}

	if c.DMR.Access == nil {
		if !reachableBeyondHost(c.DMR.ListenAddress) {
			return
		}
		// ADR-0020. A startup warning is read once by whoever happens to be
		// watching the journal; the risk it guards is that the moment UDP
		// 62031 is forwarded at the router, an unconfigured instance repeats
		// everything to everyone from the open internet.
		v.add("dmr.access",
			fmt.Sprintf("the DMR listener accepts peers on %s, which is reachable from beyond this host, "+
				"and no access block is configured: every repeater may register, every subscriber may "+
				"transmit, and every talkgroup is carried", c.DMR.ListenAddress),
			`add an "access" block to "dmr"; to permit everything deliberately, `+
				`use {"registration": {"mode": "deny", "ids": []}}`)
		return
	}

	a := c.DMR.Access
	for _, spec := range []struct {
		field string
		kind  access.Kind
		acl   ACL
	}{
		{"dmr.access.registration", access.Registration, a.Registration},
		{"dmr.access.subscribers", access.Subscriber, a.Subscribers},
		{"dmr.access.talkgroups.timeslot_1", access.Talkgroup, a.Talkgroups.Timeslot1},
		{"dmr.access.talkgroups.timeslot_2", access.Talkgroup, a.Talkgroups.Timeslot2},
	} {
		if _, err := access.Parse(spec.field, spec.kind, access.Mode(spec.acl.Mode), spec.acl.IDs); err != nil {
			// The parser's message already leads with the field and explains
			// the entry, so it is the problem rather than a summary of one.
			problem := strings.TrimPrefix(err.Error(), spec.field+": ")
			v.add(spec.field, problem,
				`each entry is an ID such as "3100" or an inclusive range such as "3100-3199"; `+
					`mode is "permit" or "deny"`)
		}
	}
}

// reachableBeyondHost reports whether a listen address accepts traffic from
// anywhere other than this machine.
//
// A hostname that is not an IP literal counts as reachable. Resolving it would
// mean DNS during validation, which makes whether a configuration is valid
// depend on whether the network is up; assuming the cautious answer costs an
// operator on an unusual setup one explicit line, and assuming the other way
// costs somebody an open master.
func reachableBeyondHost(listen string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(listen))
	if err != nil {
		// Not a parseable address. Validate reports that separately; treating
		// it as reachable here avoids a malformed address being the way to
		// skip the check.
		return true
	}
	host = strings.TrimSpace(host)
	if host == "" {
		// An empty host means every interface.
		return true
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return true
	}
	if addr.IsUnspecified() {
		return true
	}
	return !addr.IsLoopback()
}

// HomebrewProtocol reports whether this link logs into another master as a peer
// rather than bridging over OpenBridge.
//
// An empty protocol is OpenBridge, so a document written before outbound peer
// mode existed keeps meaning what it meant.
func (u Upstream) HomebrewProtocol() bool {
	return strings.EqualFold(strings.TrimSpace(u.Protocol), UpstreamHomebrew)
}

// validateHomebrewUpstream checks a link that logs into another master.
//
// It needs different things from an OpenBridge link: an address and a password,
// no listen address, and an identity, because the far end shows that identity to
// its own users. See ADR-0024.
func (c Config) validateHomebrewUpstream(v *validator, field string, u Upstream, localPeers map[uint32]bool) {
	if u.RepeaterID == 0 {
		v.add(field+".repeater_id", "must not be 0 for a homebrew link",
			"use the DMR ID this link should present; it is what the far end registers")
	} else if localPeers[u.RepeaterID] {
		// One ID meaning two stations would make a private call to it routable
		// to two places, which surfaces as intermittent misrouting rather than
		// as an error.
		v.add(field+".repeater_id",
			fmt.Sprintf("%d is also used by a locally configured peer", u.RepeaterID),
			"give the link its own DMR ID; one ID cannot mean two stations")
	}

	if strings.TrimSpace(u.PasswordFile) == "" {
		v.add(field+".password_file", "must not be empty when a homebrew link is enabled",
			"create a file containing the login password for the far end, mode 0600, and give "+
				"its path here; the password is never stored in this configuration")
	}

	if u.Identity == nil || strings.TrimSpace(u.Identity.Callsign) == "" {
		// A blank callsign appears on the far end's dashboard as an
		// unidentified station.
		v.add(field+".identity.callsign", "must not be empty for a homebrew link",
			"the far end shows this to its own users; use the callsign of the station "+
				"responsible for this link")
	}

	if u.Identity != nil {
		if cc := u.Identity.ColourCode; cc < 0 || cc > 15 {
			v.add(field+".identity.colour_code", fmt.Sprintf("is %d; DMR colour codes are 0 to 15", cc),
				"use the colour code the far end expects, or leave it out")
		}
		if ts := u.Identity.Timeslots; ts != 0 && ts != 1 && ts != 2 {
			v.add(field+".identity.timeslots", fmt.Sprintf("is %d; DMR has one or two", ts),
				"use 2 for a duplex link, 1 for simplex, or leave it out")
		}
		if lat := u.Identity.Latitude; lat < -90 || lat > 90 {
			v.add(field+".identity.latitude", fmt.Sprintf("is %g, outside -90 to 90", lat),
				"use decimal degrees, or leave it out")
		}
		if lon := u.Identity.Longitude; lon < -180 || lon > 180 {
			v.add(field+".identity.longitude", fmt.Sprintf("is %g, outside -180 to 180", lon),
				"use decimal degrees, or leave it out")
		}
	}
}
