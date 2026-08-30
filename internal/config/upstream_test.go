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
		Export:         []UpstreamTalkgroup{{Talkgroup: 3148, Timeslot: 2}},
		Import:         []UpstreamTalkgroup{{Talkgroup: 3148, Timeslot: 2}},
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
		out = append(out, Bridge{
			Name:    "to-" + u.Name,
			Enabled: true,
			Endpoints: []Endpoint{
				{Talkgroup: 9, Timeslot: 2},
				{Upstream: u.Name, Talkgroup: 9, Timeslot: 2},
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
	u.Export = nil
	u.Import = nil

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

// TestUpstreamAllowsOneDirection.
//
// Export-only and import-only are both legitimate: a club may feed its net
// upstream without accepting anything back, or take a nationwide talkgroup
// without contributing to it.
func TestUpstreamAllowsOneDirection(t *testing.T) {
	exportOnly := validUpstream()
	exportOnly.Import = nil
	if msg := upstreamProblems(t, withUpstreams(exportOnly)); msg != "" {
		t.Errorf("an export-only link was rejected:\n%s", msg)
	}

	importOnly := validUpstream()
	importOnly.Export = nil
	if msg := upstreamProblems(t, withUpstreams(importOnly)); msg != "" {
		t.Errorf("an import-only link was rejected:\n%s", msg)
	}
}

func TestUpstreamChecksTalkgroupsAndTimeslots(t *testing.T) {
	u := validUpstream()
	u.Export = []UpstreamTalkgroup{{Talkgroup: 0, Timeslot: 2}}
	u.Import = []UpstreamTalkgroup{{Talkgroup: 3148, Timeslot: 3}}

	msg := upstreamProblems(t, withUpstreams(u))
	if !strings.Contains(msg, "export[0].talkgroup") {
		t.Errorf("talkgroup 0 was accepted:\n%s", msg)
	}
	if !strings.Contains(msg, "import[0].timeslot") {
		t.Errorf("timeslot 3 was accepted:\n%s", msg)
	}
}

// TestUpstreamTimeslotErrorExplainsTheTS1Rule.
//
// An operator who has read the OpenBridge documentation knows traffic goes on
// TS1 and may think they should say so here. These entries name the *local*
// talkgroup, and QSP applies the TS1 rule. The error is where that gets
// explained, because that is where they will be looking.
func TestUpstreamTimeslotErrorExplainsTheTS1Rule(t *testing.T) {
	u := validUpstream()
	u.Export = []UpstreamTalkgroup{{Talkgroup: 3148, Timeslot: 0}}

	msg := upstreamProblems(t, withUpstreams(u))
	if !strings.Contains(msg, "TS1") {
		t.Errorf("the timeslot error does not explain the TS1 translation:\n%s", msg)
	}
}

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
		Export:       []UpstreamTalkgroup{{Talkgroup: 9, Timeslot: 2}},
		Import:       []UpstreamTalkgroup{{Talkgroup: 9, Timeslot: 2}},
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
	u.Export = nil
	u.Import = nil

	c := withUpstreams(u)
	if err := c.Validate(); err != nil {
		t.Fatalf("the configuration is refused again: %v", err)
	}
}
