package config

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// validUpstream is a link an administrator would plausibly write.
func validUpstream() Upstream {
	return Upstream{
		Name:           "brandmeister",
		Enabled:        true,
		Address:        "3102.master.brandmeister.network:62035",
		ListenAddress:  "0.0.0.0:62035",
		NetworkID:      3132910,
		PassphraseFile: "/var/lib/qsp/bm.pass",
		StaleAfter:     Duration(4 * 3600 * 1e9),
	}
}

func withUpstreams(us ...Upstream) Config {
	c := Default()
	c.DMR.Enabled = true
	// An empty block is the deliberate permit-everything of ADR-0020: the
	// operator has said so, which is what the startup check is about.
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "peer.pass"
	c.DMR.Upstreams = us
	c.DMR.Bridges = bridgesReaching(us...)
	return c
}

// bridgesReaching gives every enabled link something that routes to it.
//
// These tests are about a link's own fields, and a link nothing routes to is a
// separate fault with its own check. Without this they would all fail on that
// one instead, which is a test file that stops testing what it says it does.
func bridgesReaching(us ...Upstream) []Bridge {
	var out []Bridge
	for _, u := range us {
		if !u.Enabled {
			continue
		}
		// An endpoint naming an OpenBridge link is TS1, because that is what
		// the protocol carries. A homebrew link keeps the ordinary slot.
		linkSlot := OpenBridgeTimeslot
		if u.HomebrewProtocol() {
			linkSlot = 2
		}
		out = append(out, Bridge{
			Name:    "to-" + u.Name,
			Enabled: true,
			Endpoints: []Endpoint{
				{Talkgroup: 9, Timeslot: 2},
				{Upstream: u.Name, Talkgroup: 9, Timeslot: linkSlot},
			},
		})
	}
	return out
}

func upstreamProblems(t *testing.T, c Config) string {
	t.Helper()
	if err := c.Validate(); err != nil {
		return err.Error()
	}
	return ""
}

func TestUpstreamAcceptsAWellFormedLink(t *testing.T) {
	if msg := upstreamProblems(t, withUpstreams(validUpstream())); msg != "" {
		t.Errorf("a valid upstream was rejected:\n%s", msg)
	}
}

// TestDisabledUpstreamIsBarelyChecked.
//
// An administrator writes the configuration down before BrandMeister grants the
// bridge — they have no passphrase yet, and possibly no address. Requiring them
// would mean the only way to record the intent is to not record it.
func TestDisabledUpstreamIsBarelyChecked(t *testing.T) {
	c := withUpstreams(Upstream{Name: "brandmeister-pending"})

	if msg := upstreamProblems(t, c); msg != "" {
		t.Errorf("a disabled upstream awaiting approval was rejected:\n%s", msg)
	}
}

// TestDisabledUpstreamStillNeedsAName, because the name is what an operator
// sees in the health report, and an unnamed one cannot be talked about.
func TestDisabledUpstreamStillNeedsAName(t *testing.T) {
	c := withUpstreams(Upstream{Enabled: false})

	if msg := upstreamProblems(t, c); !strings.Contains(msg, ".name") {
		t.Errorf("an unnamed upstream was accepted:\n%s", msg)
	}
}

func TestEnabledUpstreamRequiresItsEssentials(t *testing.T) {
	cases := []struct {
		name   string
		mangle func(*Upstream)
		want   string
	}{
		{"no address", func(u *Upstream) { u.Address = "" }, ".address"},
		{"address without a port", func(u *Upstream) { u.Address = "master.example.net" }, ".address"},
		{"no listen address", func(u *Upstream) { u.ListenAddress = "" }, ".listen_address"},
		{"no network ID", func(u *Upstream) { u.NetworkID = 0 }, ".network_id"},
		{"no passphrase file", func(u *Upstream) { u.PassphraseFile = "" }, ".passphrase_file"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u := validUpstream()
			tc.mangle(&u)

			msg := upstreamProblems(t, withUpstreams(u))
			if !strings.Contains(msg, tc.want) {
				t.Errorf("expected a complaint about %s, got:\n%s", tc.want, msg)
			}
		})
	}
}

// TestALinkCarryingNothingIsRefused, by the check that describes what actually
// carries traffic.
//
// **This asserted the wrong rule until it stopped a live network.** It required
// export or import to be non-empty, which was written when those lists were
// expected to be the routing mechanism. They never became one: no code reads
// them to move a frame. A bridge naming the link does that.
//
// Two rules that did not know about each other is worse than either alone. A
// configuration with a bridge and no export satisfied one and failed the other,
// and the failure mode was a service that would not start.
func TestALinkCarryingNothingIsRefused(t *testing.T) {
	u := validUpstream()

	// With a bridge, this is a working link and must be accepted.
	if err := withUpstreams(u).Validate(); err != nil {
		t.Errorf("a link with a bridge and no export lists was refused: %v", err)
	}

	// Without one, nothing can reach it and that is the fault worth naming.
	c := withUpstreams(u)
	c.DMR.Bridges = nil
	msg := upstreamProblems(t, c)
	if !strings.Contains(msg, "no bridge sends anything") {
		t.Errorf("a link nothing routes to was accepted:\n%s", msg)
	}
}

// TestUpstreamAllowsOneDirection, TestUpstreamChecksTalkgroupsAndTimeslots and
// TestUpstreamTimeslotErrorExplainsTheTS1Rule were removed in 0308 with the
// export and import lists they exercised. Each carefully checked a value that
// no code read to move a frame — a whole family of assertions about a setting
// that decided nothing, which is worth remembering the next time a field is
// added before the behaviour that would use it.

func TestUpstreamNamesMustBeDistinct(t *testing.T) {
	a := validUpstream()
	b := validUpstream()
	b.ListenAddress = "0.0.0.0:62036"

	msg := upstreamProblems(t, withUpstreams(a, b))
	if !strings.Contains(msg, "more than one upstream") {
		t.Errorf("two links with the same name were accepted:\n%s", msg)
	}
}

func TestUpstreamRejectsNegativeStaleAfter(t *testing.T) {
	u := validUpstream()
	u.StaleAfter = Duration(-1)

	msg := upstreamProblems(t, withUpstreams(u))
	if !strings.Contains(msg, ".stale_after") {
		t.Errorf("a negative stale_after was accepted:\n%s", msg)
	}
}

// TestUpstreamsAreOptional. Most instances have none.
func TestUpstreamsAreOptional(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	// An empty block is the deliberate permit-everything of ADR-0020: the
	// operator has said so, which is what the startup check is about.
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "peer.pass"

	if msg := upstreamProblems(t, c); msg != "" {
		t.Errorf("a configuration with no upstreams was rejected:\n%s", msg)
	}
}

// Outbound peer mode. See ADR-0024.

func homebrewUpstream() Upstream {
	return Upstream{
		Name:         "xlx950",
		Protocol:     "homebrew",
		Enabled:      true,
		Address:      "xlx950.example.org:62030",
		RepeaterID:   3132910,
		PasswordFile: "/var/lib/qsp/xlx950.pass",
		Identity:     &UpstreamIdentity{Callsign: "K9MLS"},
	}
}

// TestAnEmptyProtocolIsOpenBridge is the upgrade guarantee. Every document
// written before outbound peer mode must keep meaning what it meant.
func TestAnEmptyProtocolIsOpenBridge(t *testing.T) {
	var u Upstream
	if u.HomebrewProtocol() {
		t.Error("an unset protocol was read as homebrew")
	}
	if !(Upstream{Protocol: "openbridge"}).HomebrewProtocol() == false {
		t.Error("openbridge was read as homebrew")
	}
	if !(Upstream{Protocol: "HomeBrew"}).HomebrewProtocol() {
		t.Error("the protocol comparison is case sensitive; a hand-edited file will not match")
	}
}

func TestHomebrewUpstreamIsAccepted(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{}
	c.DMR.Upstreams = []Upstream{homebrewUpstream()}

	c.DMR.Bridges = bridgesReaching(c.DMR.Upstreams...)

	if err := c.Validate(); err != nil {
		t.Fatalf("a well-formed homebrew link was rejected: %v", err)
	}
}

// TestHomebrewDoesNotNeedOpenBridgeFields. Requiring a listen address and a
// passphrase of a link that uses neither would be asking an operator for
// values that go nowhere.
func TestHomebrewDoesNotNeedOpenBridgeFields(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{}
	u := homebrewUpstream()
	u.ListenAddress = ""
	u.PassphraseFile = ""
	u.NetworkID = 0
	c.DMR.Upstreams = []Upstream{u}

	c.DMR.Bridges = bridgesReaching(c.DMR.Upstreams...)

	if err := c.Validate(); err != nil {
		t.Errorf("a homebrew link was asked for OpenBridge's fields: %v", err)
	}
}

func TestHomebrewValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Upstream)
		field  string
	}{
		{"no repeater ID", func(u *Upstream) { u.RepeaterID = 0 },
			"dmr.upstreams[0].repeater_id"},
		{"no password file", func(u *Upstream) { u.PasswordFile = "" },
			"dmr.upstreams[0].password_file"},
		{"no identity", func(u *Upstream) { u.Identity = nil },
			"dmr.upstreams[0].identity.callsign"},
		{"blank callsign", func(u *Upstream) { u.Identity.Callsign = "  " },
			"dmr.upstreams[0].identity.callsign"},
		{"colour code 16", func(u *Upstream) { u.Identity.ColourCode = 16 },
			"dmr.upstreams[0].identity.colour_code"},
		{"three timeslots", func(u *Upstream) { u.Identity.Timeslots = 3 },
			"dmr.upstreams[0].identity.timeslots"},
		{"latitude off the planet", func(u *Upstream) { u.Identity.Latitude = 200 },
			"dmr.upstreams[0].identity.latitude"},
		{"longitude off the planet", func(u *Upstream) { u.Identity.Longitude = -300 },
			"dmr.upstreams[0].identity.longitude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := enabledDMR()
			c.DMR.Access = &Access{}
			u := homebrewUpstream()
			tc.mutate(&u)
			c.DMR.Upstreams = []Upstream{u}

			c.DMR.Bridges = bridgesReaching(c.DMR.Upstreams...)

			err := c.Validate()
			if err == nil {
				t.Fatal("an invalid homebrew link was accepted")
			}
			var found bool
			for _, fe := range err.(*ValidationError).Errors {
				if fe.Field == tc.field {
					found = true
				}
			}
			if !found {
				t.Errorf("want an error on %s, got %v", tc.field, err.(*ValidationError).Fields())
			}
		})
	}
}

// TestARepeaterIDCannotBeUsedTwice. One ID meaning two stations makes a private
// call to it routable to two places, which shows up as intermittent misrouting
// rather than as an error.
func TestARepeaterIDCannotBeUsedTwice(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{}
	c.DMR.Subscription = Subscription{
		Enabled: true,
		Timeout: Duration(time.Minute),
		Static:  []StaticAttachment{{Peer: 3132910, Talkgroup: 9, Timeslot: 2}},
	}
	c.DMR.Upstreams = []Upstream{homebrewUpstream()} // also 3132910

	err := c.Validate()
	if err == nil {
		t.Fatal("a link claimed a DMR ID a local peer already uses")
	}
	var found bool
	for _, fe := range err.(*ValidationError).Errors {
		if fe.Field == "dmr.upstreams[0].repeater_id" {
			found = true
		}
	}
	if !found {
		t.Errorf("want a repeater_id collision error, got %v", err.(*ValidationError).Fields())
	}
}

func TestUnknownProtocolIsRefused(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{}
	u := homebrewUpstream()
	u.Protocol = "ipsc"
	c.DMR.Upstreams = []Upstream{u}

	c.DMR.Bridges = bridgesReaching(c.DMR.Upstreams...)

	err := c.Validate()
	if err == nil {
		t.Fatal("an unknown protocol was accepted")
	}
	if !strings.Contains(err.Error(), "protocol") {
		t.Errorf("the error should name the protocol field: %v", err)
	}
}

// TestADisabledHomebrewLinkIsBarelyChecked lets an operator write down a
// configuration before they have the password, the same way a disabled
// OpenBridge link is treated.
func TestADisabledHomebrewLinkIsBarelyChecked(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{}
	c.DMR.Upstreams = []Upstream{{Name: "xlx950", Protocol: "homebrew", Enabled: false}}

	c.DMR.Bridges = bridgesReaching(c.DMR.Upstreams...)

	if err := c.Validate(); err != nil {
		t.Errorf("a disabled homebrew link was rejected: %v", err)
	}
}

func TestHomebrewUpstreamRoundTripsThroughJSON(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{}
	u := homebrewUpstream()
	u.Identity.Location = "Denton, TX"
	u.Identity.Latitude = 33.2148
	u.Identity.Longitude = -97.1331
	c.DMR.Upstreams = []Upstream{u}

	c.DMR.Bridges = bridgesReaching(c.DMR.Upstreams...)

	var buf bytes.Buffer
	if err := Save(&buf, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.DMR.Upstreams) != 1 || got.DMR.Upstreams[0].Identity == nil {
		t.Fatal("the identity did not survive the round trip")
	}
	if got.DMR.Upstreams[0].Identity.Callsign != "K9MLS" {
		t.Errorf("the callsign was lost: %+v", got.DMR.Upstreams[0].Identity)
	}
	// A secret must never be written into the document.
	if strings.Contains(buf.String(), "passw") && !strings.Contains(buf.String(), "password_file") {
		t.Error("something password-like reached the configuration document")
	}
}

// TestALinkNothingRoutesToIsRefused.
//
// **This is the fault that cost an afternoon, and nothing could have caught
// it.** Two instances were peered over OpenBridge. Both sockets opened, the far
// end authenticated, keepalives flowed both ways for an hour, and six
// transmissions were routed to local peers and to nothing else — because
// `config.Endpoint` had no way to name a link, so no configuration a person
// could write could put a frame on one.
//
// `Upstream.Export` and `Upstream.Import` were validated, stored, documented,
// and read by no code that has ever run.
func TestALinkNothingRoutesToIsRefused(t *testing.T) {
	c := withUpstreams(validUpstream())
	c.DMR.Bridges = nil

	err := c.Validate()
	if err == nil {
		t.Fatal("a link with nothing routing to it was accepted; it would open, " +
			"authenticate and carry nothing")
	}
	if !strings.Contains(err.Error(), "no bridge sends anything") {
		t.Errorf("the error does not say what is wrong: %v", err)
	}
}

// TestABridgeCanNameALink, which is the whole point.
func TestABridgeCanNameALink(t *testing.T) {
	c := withUpstreams(validUpstream())
	if err := c.Validate(); err != nil {
		t.Fatalf("a bridge naming a link was rejected: %v", err)
	}
}

// TestAnEndpointIsAPeerOrALinkAndNotBoth. routing.Endpoint has always said so;
// configuration could not express either half until now.
func TestAnEndpointIsAPeerOrALinkAndNotBoth(t *testing.T) {
	c := withUpstreams(validUpstream())
	c.DMR.Bridges[0].Endpoints[1].Peer = 3132910

	err := c.Validate()
	if err == nil {
		t.Fatal("an endpoint naming both a peer and a link was accepted")
	}
	if !strings.Contains(err.Error(), "not a peer") {
		t.Errorf("the error does not explain: %v", err)
	}
}

// TestABridgeCannotNameALinkThatIsNotThere, because a typo in a link name
// otherwise produces a bridge that routes to nowhere and says nothing.
func TestABridgeCannotNameALinkThatIsNotThere(t *testing.T) {
	c := withUpstreams(validUpstream())
	c.DMR.Bridges[0].Endpoints[1].Upstream = "pairr"

	err := c.Validate()
	if err == nil {
		t.Fatal("a bridge naming a link that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "does not match any enabled link") {
		t.Errorf("the error does not explain: %v", err)
	}
}

// TestTheConfigurationThatStoppedALiveNetwork.
//
// A link with a bridge naming it and no export or import lists. It satisfied
// the rule that a link must be reachable and failed a second rule requiring
// export or import — two checks that did not know about each other — and the
// failure was a service that would not start, on a network carrying a member.
//
// The second rule is gone. This is the shape of the configuration that broke,
// and it must load.
func TestTheConfigurationThatStoppedALiveNetwork(t *testing.T) {
	u := validUpstream()

	c := withUpstreams(u)
	if err := c.Validate(); err != nil {
		t.Fatalf("the configuration is refused again: %v", err)
	}
}

// TestOneCallsignServesEveryLink.
//
// **UpstreamIdentity is per link, and only per link.** An instance with three
// links stated its callsign three times with nothing keeping them consistent,
// and the first peering an operator ever attempted had none of them — because
// an instance with no links had no identity either.
//
// A station has one callsign and one position.
func TestOneCallsignServesEveryLink(t *testing.T) {
	c := withUpstreams(homebrewUpstream())
	c.DMR.Identity = Identity{Callsign: "K9MLS", Latitude: 33.238, Longitude: -97.1134}
	c.DMR.Upstreams[0].Identity = &UpstreamIdentity{}

	if err := c.Validate(); err != nil {
		t.Fatalf("a link with no callsign of its own was refused: %v", err)
	}

	got := c.DMR.Identity.Merge(*c.DMR.Upstreams[0].Identity)
	if got.Callsign != "K9MLS" {
		t.Errorf("the link announces %q rather than the instance's callsign", got.Callsign)
	}
	if got.Latitude != 33.238 {
		t.Errorf("the link announces latitude %v rather than the instance's", got.Latitude)
	}
}

// TestALinkMayDisagreeDeliberately.
//
// Per-link wins where it is set: an administrator who states something on one
// link means it, and defaulting over the top would silently discard a choice.
func TestALinkMayDisagreeDeliberately(t *testing.T) {
	i := Identity{Callsign: "K9MLS", Location: "Denton, TX"}
	got := i.Merge(UpstreamIdentity{Callsign: "K9MLS/P", Location: "Portable"})

	if got.Callsign != "K9MLS/P" || got.Location != "Portable" {
		t.Errorf("the instance's identity overwrote the link's: %+v", got)
	}
}

// TestAnUnidentifiedHomebrewLinkIsRefused, because a blank callsign appears on
// the far end's dashboard as an unidentified station.
func TestAnUnidentifiedHomebrewLinkIsRefused(t *testing.T) {
	c := withUpstreams(homebrewUpstream())
	c.DMR.Identity = Identity{}
	c.DMR.Upstreams[0].Identity = &UpstreamIdentity{}

	err := c.Validate()
	if err == nil {
		t.Fatal("a link with no callsign anywhere was accepted")
	}
	if !strings.Contains(err.Error(), "identity.callsign") {
		t.Errorf("the error does not explain: %v", err)
	}
}

// TestOpenBridgeNeedsNoCallsign. It sends no configuration message and has no
// dashboard to appear on; requiring one would refuse a working link over a
// field it never transmits.
func TestOpenBridgeNeedsNoCallsign(t *testing.T) {
	c := withUpstreams(validUpstream())
	c.DMR.Identity = Identity{}

	if err := c.Validate(); err != nil {
		t.Fatalf("an OpenBridge link was refused for having no callsign: %v", err)
	}
}

// TestAnOpenBridgeEndpointMustBeTimeslot1 is the day no audio crossed.
//
// OpenBridge passes all traffic on TS1 — openbridge.Encode forces it on every
// frame sent — so an endpoint naming an OpenBridge link on TS2 matches nothing
// arriving and produces nothing that can leave. Both ends of a peering were
// configured, both links reported healthy, and the counters read Sent 50 /
// Received 0 on one side and Received 28 / Sent 0 on the other, which looks
// exactly like a network fault and is not one.
//
// Refused rather than quietly corrected. The configuration this rejects is one
// in which the link already carries nothing, so a startup error naming the fix
// replaces a day of healthy counters and silence.
func TestAnOpenBridgeEndpointMustBeTimeslot1(t *testing.T) {
	c := withUpstreams(validUpstream())
	c.DMR.Bridges[0].Endpoints[1].Timeslot = 2

	msg := upstreamProblems(t, c)
	if !strings.Contains(msg, "endpoints[1].timeslot") {
		t.Errorf("a bridge endpoint naming an OpenBridge link on TS2 was accepted:\n%s", msg)
	}
	// The advice has to name the slot, because the operator reading it is
	// looking at a link that authenticated and carried nothing.
	if !strings.Contains(msg, "TS1") {
		t.Errorf("the refusal does not say which slot to use:\n%s", msg)
	}
}

// TestTheLocalEndpointKeepsItsOwnSlot, because only the link is forced.
//
// A club's talkgroup lives on TS2 by convention and that is a real choice about
// this network's own peers. Forcing both endpoints would move every member's
// traffic to slot 1 to satisfy a rule about somebody else's link.
func TestTheLocalEndpointKeepsItsOwnSlot(t *testing.T) {
	c := withUpstreams(validUpstream())
	c.DMR.Bridges[0].Endpoints[0].Timeslot = 2
	c.DMR.Bridges[0].Endpoints[1].Timeslot = OpenBridgeTimeslot

	if msg := upstreamProblems(t, c); msg != "" {
		t.Errorf("a local endpoint on TS2 beside an OpenBridge link on TS1 was rejected:\n%s", msg)
	}
}

// TestAHomebrewLinkKeepsBothSlots.
//
// The rule is OpenBridge's, not every link's. A homebrew link logs into another
// master as a peer and carries both timeslots exactly as a repeater does, so an
// endpoint on TS2 is ordinary there. Scoping this wrongly would refuse every
// XLX and DMR+ configuration on the network.
func TestAHomebrewLinkKeepsBothSlots(t *testing.T) {
	u := validUpstream()
	u.Protocol = UpstreamHomebrew
	u.ListenAddress = ""
	u.PassphraseFile = ""
	u.RepeaterID = 3132911
	u.PasswordFile = "/var/lib/qsp/xlx.pass"
	u.Identity = &UpstreamIdentity{Callsign: "K9MLS"}

	c := withUpstreams(u)
	c.DMR.Bridges[0].Endpoints[1].Timeslot = 2

	if msg := upstreamProblems(t, c); strings.Contains(msg, "endpoints[1].timeslot") {
		t.Errorf("a homebrew link was refused for carrying TS2:\n%s", msg)
	}
}

// qspLink is a peering with another QSP server (ADR-0051).
//
// It dials out, so it takes the same fields a homebrew link to XLX does: an
// address, a DMR ID, a password file and a callsign. No listen address, and
// nothing describing what crosses.
func qspLink() Upstream {
	u := validUpstream()
	u.Name = "blake"
	u.Protocol = UpstreamQSP
	u.Address = "blake.example.org:62031"
	u.ListenAddress = ""
	u.PassphraseFile = ""
	u.NetworkID = 0
	u.RepeaterID = 3132911
	u.PasswordFile = "/var/lib/qsp/blake.pass"
	u.Identity = &UpstreamIdentity{Callsign: "K9MLS"}
	return u
}

// TestAQSPLinkNeedsNoBridge is the configuration an administrator writes after
// agreeing a peering, and it is the whole of it.
//
// A link with no bridge is normally the fault that cost an afternoon: the
// socket opens, the far end authenticates, and no configuration on this side
// could ever have put a frame on it. A QSP link is exempt because repeat
// reaches it the way repeat reaches a hotspot. Demanding a bridge would
// reintroduce the endpoint that carries a timeslot, which is the fault
// ADR-0051 was written after.
func TestAQSPLinkNeedsNoBridge(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "peer.pass"
	c.DMR.Upstreams = []Upstream{qspLink()}
	c.DMR.Bridges = nil

	if msg := upstreamProblems(t, c); msg != "" {
		t.Errorf("a QSP link with no bridge was rejected:\n%s", msg)
	}
}

// TestAnOpenBridgeLinkStillNeedsABridge scopes the exemption.
//
// The rule it lifts is a real one and it stays for every other link: a link
// nothing routes to opens, authenticates, reports healthy and carries nothing,
// and the advice sends an operator to check somebody else's address.
func TestAnOpenBridgeLinkStillNeedsABridge(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "peer.pass"
	c.DMR.Upstreams = []Upstream{validUpstream()}
	c.DMR.Bridges = nil

	if msg := upstreamProblems(t, c); !strings.Contains(msg, "no bridge sends anything") {
		t.Errorf("an OpenBridge link with no bridge was accepted:\n%s", msg)
	}
}

// TestAQSPLinkDialsOut, which is what makes it need no port forward.
//
// The homebrew peer conversation is client-initiated: this server logs into
// the other one, and the router holds the mapping open for the replies. Only
// the side that offered the peering needs a reachable address, and it already
// has one because it serves hotspots. Answering false here would send the link
// down the OpenBridge path, which listens instead of dialling.
func TestAQSPLinkDialsOut(t *testing.T) {
	if !qspLink().HomebrewProtocol() {
		t.Error("a QSP link does not take the dial-out path, so it would need a port forward")
	}
	if !qspLink().QSPLink() {
		t.Error("a QSP link does not report itself as one")
	}
	if (Upstream{Protocol: UpstreamHomebrew}).QSPLink() {
		t.Error("a homebrew link to XLX reports itself as a QSP link")
	}
	// An empty protocol is OpenBridge, for documents written before any of
	// this existed. It must not become a QSP link by default.
	if (Upstream{}).QSPLink() {
		t.Error("a link with no protocol defaults to being a QSP link")
	}
}

// TestQSPIsAProtocolQSPSpeaks, because an unrecognised value is refused with
// advice, and the advice has to name the one an administrator wants.
func TestQSPIsAProtocolQSPSpeaks(t *testing.T) {
	u := qspLink()
	u.Protocol = "qsp-link"
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "peer.pass"
	c.DMR.Upstreams = []Upstream{u}

	msg := upstreamProblems(t, c)
	if !strings.Contains(msg, "protocol") {
		t.Fatalf("an unknown protocol was accepted:\n%s", msg)
	}
	if !strings.Contains(msg, "\"qsp\"") {
		t.Errorf("the advice does not offer \"qsp\":\n%s", msg)
	}
}

// qspConfig wraps a set of links in a configuration that is otherwise valid.
func qspConfig(us ...Upstream) Config {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "peer.pass"
	c.DMR.Upstreams = us
	c.DMR.Bridges = nil
	return c
}

// TestALinkMayNotCarryTheMastersOwnID is the fault that reports nothing.
//
// A station refuses to register with a master announcing the station's own ID,
// and retries forever with no indication of cause. It has cost this project
// time twice. On 2026-09-08 both K9MLS servers were announcing IPSC master ID
// 3132911 at once while a link between them was being configured by hand, and
// nothing anywhere would have said so.
//
// The rule already existed for `ipsc.allowed_peers`. A link is the same station
// wearing a different hat.
func TestALinkMayNotCarryTheMastersOwnID(t *testing.T) {
	u := qspLink()
	u.RepeaterID = 3132911

	c := qspConfig(u)
	c.IPSC.Enabled = true
	c.IPSC.MasterID = 3132911
	c.IPSC.ListenAddress = "0.0.0.0:50000"
	cc := uint8(1)
	c.IPSC.ColourCode = &cc

	msg := upstreamProblems(t, c)
	if !strings.Contains(msg, "repeater_id") || !strings.Contains(msg, "master_id") {
		t.Errorf("a link carrying the master's own ID was accepted:\n%s", msg)
	}
	// The advice has to say why nothing will report it, because the operator's
	// symptom is silence.
	if !strings.Contains(msg, "retries silently") {
		t.Errorf("the refusal does not say the failure is silent:\n%s", msg)
	}
}

// TestTwoLinksMayNotShareAnID, because neither end reports that either.
//
// The far end registers by ID, so a second link with the same one replaces the
// first: one link goes quiet and both sides report healthy. At ten servers that
// is a hole nobody owns.
func TestTwoLinksMayNotShareAnID(t *testing.T) {
	a, b := qspLink(), qspLink()
	b.Name = "paul"
	b.Address = "paul.example.org:62031"
	b.PasswordFile = "/var/lib/qsp/paul.pass"
	// Same ID as a.
	b.RepeaterID = a.RepeaterID

	msg := upstreamProblems(t, qspConfig(a, b))
	if !strings.Contains(msg, "repeater_id") {
		t.Errorf("two links sharing a DMR ID were accepted:\n%s", msg)
	}
	if !strings.Contains(msg, "blake") {
		t.Errorf("the refusal does not name the other link:\n%s", msg)
	}
}

// TestTwoLinksWithTheirOwnIDsAreFine, so the rule refuses a collision and not
// a network with several links in it — which is what ADR-0051 is for.
func TestTwoLinksWithTheirOwnIDsAreFine(t *testing.T) {
	a, b := qspLink(), qspLink()
	b.Name = "paul"
	b.Address = "paul.example.org:62031"
	b.PasswordFile = "/var/lib/qsp/paul.pass"
	b.RepeaterID = a.RepeaterID + 1

	if msg := upstreamProblems(t, qspConfig(a, b)); msg != "" {
		t.Errorf("two links with distinct IDs were rejected:\n%s", msg)
	}
}
