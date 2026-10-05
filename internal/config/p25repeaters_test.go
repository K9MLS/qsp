package config

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func repeatersOn() Config {
	c := Default()
	c.P25Repeaters = P25Repeaters{Enabled: true, ListenAddress: "0.0.0.0:1994"}
	return c
}

// Break it: drop a check in Validate, and its row passes a document the
// listener would refuse at startup.
func TestP25RepeatersValidation(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(c *Config)
		field string // empty: valid
	}{
		{"on with a port", func(*Config) {}, ""},
		{"off with nothing set", func(c *Config) { c.P25Repeaters = P25Repeaters{} }, ""},
		{"off keeps a half-finished address", func(c *Config) { c.P25Repeaters = P25Repeaters{ListenAddress: "nonsense"} }, ""},
		{"a router by address", func(c *Config) { c.P25Repeaters.AllowedRouters = []string{"192.0.2.4"} }, ""},
		{"the largest site", func(c *Config) { c.P25Repeaters.Site = 127 }, ""},
		{"a site an introduction cannot carry", func(c *Config) { c.P25Repeaters.Site = 128 }, "p25_repeaters.site"},
		{"presented as a console", func(c *Config) { c.P25Repeaters.PresentAs = "console" }, ""},
		{"presented as a repeater, by name", func(c *Config) { c.P25Repeaters.PresentAs = "repeater" }, ""},
		{"presented as something else", func(c *Config) { c.P25Repeaters.PresentAs = "Console" }, "p25_repeaters.present_as"},
		{"no address", func(c *Config) { c.P25Repeaters.ListenAddress = " " }, "p25_repeaters.listen_address"},
		{"no port", func(c *Config) { c.P25Repeaters.ListenAddress = "0.0.0.0" }, "p25_repeaters.listen_address"},
		{"a router by name", func(c *Config) { c.P25Repeaters.AllowedRouters = []string{"192.0.2.4", "router1"} }, "p25_repeaters.allowed_routers[1]"},
		{"the console's own port", func(c *Config) { c.P25Repeaters.ListenAddress = c.Server.ListenAddress }, "p25_repeaters.listen_address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := repeatersOn()
			tc.edit(&c)
			err := c.Validate()
			switch {
			case tc.field == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.field != "" && (err == nil || !strings.Contains(err.Error(), tc.field)):
				t.Errorf("got %v, want an error naming %s", err, tc.field)
			}
		})
	}
}

// Break it: leave the section out of Listeners or NeedsRestart, and -check
// never probes the port, or a change is reported as live when it is not.
func TestP25RepeatersAreAListenerAndNeedARestart(t *testing.T) {
	on := repeatersOn()
	if !slices.ContainsFunc(on.Listeners(), func(l Listener) bool {
		return l.Field == "p25_repeaters.listen_address" && l.Network == "tcp"
	}) {
		t.Error("the enabled listener is not in Listeners")
	}
	if slices.ContainsFunc(Default().Listeners(), func(l Listener) bool {
		return l.Field == "p25_repeaters.listen_address"
	}) {
		t.Error("the listener is listed while off")
	}

	cases := []struct {
		name  string
		edit  func(c *Config)
		field string
	}{
		{"turned off", func(c *Config) { c.P25Repeaters.Enabled = false }, "p25_repeaters.enabled"},
		{"another port", func(c *Config) { c.P25Repeaters.ListenAddress = "0.0.0.0:1995" }, "p25_repeaters.listen_address"},
		{"a router named", func(c *Config) { c.P25Repeaters.AllowedRouters = []string{"192.0.2.4"} }, "p25_repeaters.allowed_routers"},
		{"another site", func(c *Config) { c.P25Repeaters.Site = 9 }, "p25_repeaters.site"},
		{"another form", func(c *Config) { c.P25Repeaters.PresentAs = "console" }, "p25_repeaters.present_as"},
		{"the header turned on", func(c *Config) { c.P25Repeaters.SendHeader = true }, "p25_repeaters.send_header"},
		{"the hold turned off", func(c *Config) { c.P25Repeaters.HoldMS = new(0) }, "p25_repeaters.hold_ms"},
		{"a longer hold", func(c *Config) { c.P25Repeaters.HoldMS = new(120) }, "p25_repeaters.hold_ms"},
		{"recording turned on", func(c *Config) { c.P25Repeaters.RecordDir = "/tmp/q" }, "p25_repeaters.record_dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			after := repeatersOn()
			tc.edit(&after)
			if got := NeedsRestart(on, after); !slices.Contains(got, tc.field) {
				t.Errorf("NeedsRestart returned %v, want %s", got, tc.field)
			}
		})
	}
	if got := NeedsRestart(on, repeatersOn()); len(got) != 0 {
		t.Errorf("an unchanged document needs a restart for %v", got)
	}
}

// Absent and zero are different holds: one is the default and the other is
// none. A plain number could not tell them apart, and a configuration that
// never mentioned a hold would have been given none.
//
// Break it: make HoldMS an int, or have Hold return zero for absent.
func TestTheRepeaterHoldDefaultsWhenAbsentAndIsNoneAtZero(t *testing.T) {
	tests := []struct {
		name    string
		hold    *int
		want    time.Duration
		refused bool
	}{
		{"absent is the default", nil, 60 * time.Millisecond, false},
		{"zero is none", new(0), 0, false},
		{"a value is itself", new(120), 120 * time.Millisecond, false},
		{"the most allowed", new(200), 200 * time.Millisecond, false},
		{"more than the most", new(201), 201 * time.Millisecond, true},
		{"less than nothing", new(-20), -20 * time.Millisecond, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := repeatersOn()
			c.P25Repeaters.HoldMS = tc.hold
			if got := c.P25Repeaters.Hold(); got != tc.want {
				t.Errorf("Hold() = %s, want %s", got, tc.want)
			}
			err := c.Validate()
			if refused := err != nil && strings.Contains(err.Error(), "p25_repeaters.hold_ms"); refused != tc.refused {
				t.Errorf("Validate gave %v", err)
			}
		})
	}

	// A copy handed out must not share the number with the original.
	c := repeatersOn()
	c.P25Repeaters.HoldMS = new(80)
	copied := c.Clone()
	*copied.P25Repeaters.HoldMS = 0
	if *c.P25Repeaters.HoldMS != 80 {
		t.Error("changing a clone's hold changed the original's")
	}
}
