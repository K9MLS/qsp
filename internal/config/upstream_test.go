package config

import (
	"strings"
	"testing"
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
	c.DMR.PasswordFile = "peer.pass"
	c.DMR.Upstreams = us
	return c
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

// TestUpstreamCarryingNothingIsRefused.
//
// A link with neither export nor import connects, authenticates, and does
// nothing. That is indistinguishable from a broken link to whoever is looking
// at it, and the far end sees a bridge with no traffic — which BrandMeister
// eventually removes.
func TestUpstreamCarryingNothingIsRefused(t *testing.T) {
	u := validUpstream()
	u.Export = nil
	u.Import = nil

	msg := upstreamProblems(t, withUpstreams(u))
	if !strings.Contains(msg, "carries no talkgroups") {
		t.Errorf("a link carrying nothing was accepted:\n%s", msg)
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
	c.DMR.PasswordFile = "peer.pass"

	if msg := upstreamProblems(t, c); msg != "" {
		t.Errorf("a configuration with no upstreams was rejected:\n%s", msg)
	}
}
