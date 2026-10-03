package config

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/routing"
	"github.com/k9mls/qsp/internal/scheduler"
)

// A configuration that is saved must be one the server starts with. Each of
// these was accepted by Validate and then refused when QSP started — by the
// scheduler, the router or the socket — so a save from the console left a
// server that would not come back from its next restart.
//
// To see it fail: remove the check named by a case's field, and that case is
// accepted.
func TestWhatIsSavedIsWhatStarts(t *testing.T) {
	base := func() Config {
		c := Default()
		c.DMR.Enabled = true
		c.DMR.Access = &Access{}
		c.DMR.PasswordFile = "/etc/qsp/peer.pass"
		c.DMR.Bridges = []Bridge{{Name: "net", Enabled: true, Endpoints: []Endpoint{
			{Peer: 3132910, Talkgroup: 3148, Timeslot: 1},
			{Peer: 0, Talkgroup: 91, Timeslot: 2},
		}}}
		c.DMR.Schedule = []Window{{
			Bridge: "net", Days: []int{2}, Start: "20:00", Duration: Duration(time.Hour),
			Timezone: "America/Chicago", Enabled: true,
		}}
		c.DMR.Triggers = []Trigger{{
			Bridge: "net", On: []Endpoint{{Peer: 3132910, Talkgroup: 3148, Timeslot: 1}},
			HangTime: Duration(3 * time.Minute), Enabled: true,
		}}
		return c
	}
	if err := base().Validate(); err != nil {
		t.Fatalf("the starting point is refused: %v", err)
	}

	cases := []struct {
		name  string
		edit  func(c *Config)
		field string // empty: must be accepted
	}{
		{"a timezone abbreviation", func(c *Config) { c.DMR.Schedule[0].Timezone = "CST" }, "dmr.schedule[0].timezone"},
		{"a window of thirteen hours", func(c *Config) { c.DMR.Schedule[0].Duration = Duration(13 * time.Hour) }, "dmr.schedule[0].duration"},
		{"a window of thirty seconds", func(c *Config) { c.DMR.Schedule[0].Duration = Duration(30 * time.Second) }, "dmr.schedule[0].duration"},
		{"a window of exactly twelve hours", func(c *Config) { c.DMR.Schedule[0].Duration = Duration(12 * time.Hour) }, ""},
		{"a weekday listed twice", func(c *Config) { c.DMR.Schedule[0].Days = []int{1, 1} }, "dmr.schedule[0].days"},
		{"a hang time of 45 minutes", func(c *Config) { c.DMR.Triggers[0].HangTime = Duration(45 * time.Minute) }, "dmr.triggers[0].hang_time"},
		{"a hang time of exactly 30 minutes", func(c *Config) { c.DMR.Triggers[0].HangTime = Duration(30 * time.Minute) }, ""},
		{"a bridge endpoint listed twice", func(c *Config) {
			c.DMR.Bridges[0].Endpoints = append(c.DMR.Bridges[0].Endpoints, c.DMR.Bridges[0].Endpoints[0])
		}, "dmr.bridges[0].endpoints[2]"},
		{"a DMR port of 70000", func(c *Config) { c.DMR.ListenAddress = "0.0.0.0:70000" }, "dmr.listen_address"},
		{"a DMR address with no port after the colon", func(c *Config) { c.DMR.ListenAddress = "0.0.0.0:" }, "dmr.listen_address"},
		{"a console port by name", func(c *Config) { c.Server.ListenAddress = "127.0.0.1:http" }, "server.listen_address"},
		{"port 0, which asks for any free port", func(c *Config) { c.DMR.ListenAddress = "127.0.0.1:0" }, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.edit(&c)
			err := c.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("got %v, want a refusal naming %s", err, tc.field)
			}
			for _, fe := range ve.Errors {
				if fe.Field == tc.field {
					return
				}
			}
			t.Errorf("the refusal does not name %s: %v", tc.field, err)
		})
	}
}

// Configuration restates the scheduler's and the router's limits rather than
// importing them; this holds each pair together.
func TestStartLimitsAgreeWithWhatEnforcesThem(t *testing.T) {
	if minWindowDuration != scheduler.MinWindowDuration || maxWindowDuration != scheduler.MaxWindowDuration {
		t.Errorf("windows: configuration allows %s to %s, the scheduler %s to %s",
			minWindowDuration, maxWindowDuration, scheduler.MinWindowDuration, scheduler.MaxWindowDuration)
	}
	if maxHangTime != routing.MaxHangTime {
		t.Errorf("hang time: configuration allows %s, the router %s", maxHangTime, routing.MaxHangTime)
	}
}
