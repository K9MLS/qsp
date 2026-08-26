package config

import (
	"strings"
	"testing"
)

// withJoin builds a valid configuration carrying a club's join settings and a
// bridge that actually carries them.
func withJoin(tgs []JoinTalkgroup, bridges []Bridge) Config {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.PasswordFile = "peer.pass"
	c.DMR.Bridges = bridges
	c.DMR.Join = Join{
		NetworkName: "Denton County ARA",
		Address:     "192.168.1.135",
		Talkgroups:  tgs,
	}
	return c
}

func clubBridge() []Bridge {
	return []Bridge{{
		Name:    "club",
		Enabled: true,
		Endpoints: []Endpoint{
			{Talkgroup: 9, Timeslot: 2},
			{Talkgroup: 91, Timeslot: 2},
		},
	}}
}

func problems(t *testing.T, c Config) string {
	t.Helper()
	err := c.Validate()
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestJoinTalkgroupTargetPrefersTheRewrite.
//
// Arrives is what QSP sees; Dialled is what the member types. When a hotspot
// does not rewrite, they are the same and only one number is configured.
func TestJoinTalkgroupTargetPrefersTheRewrite(t *testing.T) {
	if got := (JoinTalkgroup{Dialled: 11, Arrives: 9}).Target(); got != 9 {
		t.Errorf("Target() = %d, want 9 — the arriving talkgroup is what QSP sees", got)
	}
	if got := (JoinTalkgroup{Dialled: 3148}).Target(); got != 3148 {
		t.Errorf("Target() = %d, want 3148 when no rewrite is configured", got)
	}
}

// TestJoinRejectsATalkgroupNoBridgeCarries is the check worth having.
//
// An admin who mistypes "arrives" sends every member to a talkgroup that goes
// nowhere. They hear silence, conclude QSP is broken, and the admin cannot
// reproduce it without a second radio. This is the one part of onboarding a
// machine can verify.
func TestJoinRejectsATalkgroupNoBridgeCarries(t *testing.T) {
	c := withJoin([]JoinTalkgroup{
		{Name: "Club chat", Dialled: 11, Arrives: 99, Timeslot: 2},
	}, clubBridge())

	msg := problems(t, c)
	if msg == "" {
		t.Fatal("a talkgroup no bridge carries was accepted")
	}
	for _, want := range []string{"no bridge carries", "dmr.join.talkgroups[0]", "DMRGateway"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %q:\n%s", want, msg)
		}
	}
}

// TestJoinAcceptsATalkgroupABridgeCarries.
func TestJoinAcceptsATalkgroupABridgeCarries(t *testing.T) {
	c := withJoin([]JoinTalkgroup{
		{Name: "Club chat", Dialled: 11, Arrives: 9, Timeslot: 2},
		{Name: "Nets", Dialled: 12, Arrives: 91, Timeslot: 2},
	}, clubBridge())

	if msg := problems(t, c); msg != "" {
		t.Errorf("a correctly configured join was rejected:\n%s", msg)
	}
}

// TestJoinChecksTheTimeslotToo.
//
// TG 9 on TS1 is not TG 9 on TS2. A member told the wrong slot reaches nothing
// on a network where everything else looks right, which is the hardest kind of
// fault to talk somebody through over the radio.
func TestJoinChecksTheTimeslotToo(t *testing.T) {
	c := withJoin([]JoinTalkgroup{
		{Name: "Club chat", Dialled: 11, Arrives: 9, Timeslot: 1},
	}, clubBridge())

	if msg := problems(t, c); !strings.Contains(msg, "no bridge carries") {
		t.Errorf("TG 9 on the wrong timeslot was accepted:\n%s", msg)
	}
}

// TestJoinRejectsDuplicateArrivals.
//
// Two entries arriving identically are indistinguishable once they reach QSP,
// so the page would list two names for one destination.
func TestJoinRejectsDuplicateArrivals(t *testing.T) {
	c := withJoin([]JoinTalkgroup{
		{Name: "Club chat", Dialled: 11, Arrives: 9, Timeslot: 2},
		{Name: "Ragchew", Dialled: 21, Arrives: 9, Timeslot: 2},
	}, clubBridge())

	msg := problems(t, c)
	if !strings.Contains(msg, "the same as") {
		t.Errorf("two talkgroups arriving identically were accepted:\n%s", msg)
	}
	if !strings.Contains(msg, "Club chat") {
		t.Errorf("the error does not name the entry it collides with:\n%s", msg)
	}
}

// TestJoinRequiresNameAndNumber.
func TestJoinRequiresNameAndNumber(t *testing.T) {
	c := withJoin([]JoinTalkgroup{
		{Dialled: 0, Timeslot: 3},
	}, clubBridge())

	msg := problems(t, c)
	for _, want := range []string{".name", ".dialled", ".timeslot"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not report %s:\n%s", want, msg)
		}
	}
}

// TestJoinIsOptional.
//
// An instance with no join settings is not misconfigured — /api/join reports
// what it can and tells the member to ask their admin. Requiring it would stop
// existing configurations from starting.
func TestJoinIsOptional(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.PasswordFile = "peer.pass"

	if msg := problems(t, c); msg != "" {
		t.Errorf("a configuration without join settings was rejected:\n%s", msg)
	}
}

// TestJoinSkipsReachabilityWithoutBridges.
//
// An instance with no bridges is observing rather than relaying. Complaining
// that a talkgroup is unreachable would be true but useless.
func TestJoinSkipsReachabilityWithoutBridges(t *testing.T) {
	c := withJoin([]JoinTalkgroup{
		{Name: "Club chat", Dialled: 11, Arrives: 9, Timeslot: 2},
	}, nil)

	if msg := problems(t, c); strings.Contains(msg, "no bridge carries") {
		t.Errorf("reachability was checked against an instance with no bridges:\n%s", msg)
	}
}
