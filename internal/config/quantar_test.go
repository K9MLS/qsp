package config

import (
	"slices"
	"strings"
	"testing"
)

func quantarOn() Config {
	c := Default()
	c.Quantar = Quantar{Enabled: true, ListenAddress: "0.0.0.0:1994"}
	return c
}

// Break it: drop a check in Validate, and its row passes a document the
// listener would refuse at startup.
func TestQuantarValidation(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(c *Config)
		field string // empty: valid
	}{
		{"on with a port", func(*Config) {}, ""},
		{"off with nothing set", func(c *Config) { c.Quantar = Quantar{} }, ""},
		{"off keeps a half-finished address", func(c *Config) { c.Quantar = Quantar{ListenAddress: "nonsense"} }, ""},
		{"a router by address", func(c *Config) { c.Quantar.AllowedRouters = []string{"192.0.2.4"} }, ""},
		{"no address", func(c *Config) { c.Quantar.ListenAddress = " " }, "quantar.listen_address"},
		{"no port", func(c *Config) { c.Quantar.ListenAddress = "0.0.0.0" }, "quantar.listen_address"},
		{"a router by name", func(c *Config) { c.Quantar.AllowedRouters = []string{"192.0.2.4", "router1"} }, "quantar.allowed_routers[1]"},
		{"the console's own port", func(c *Config) { c.Quantar.ListenAddress = c.Server.ListenAddress }, "quantar.listen_address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := quantarOn()
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
func TestQuantarIsAListenerAndNeedsARestart(t *testing.T) {
	on := quantarOn()
	if !slices.ContainsFunc(on.Listeners(), func(l Listener) bool {
		return l.Field == "quantar.listen_address" && l.Network == "tcp"
	}) {
		t.Error("the enabled listener is not in Listeners")
	}
	if slices.ContainsFunc(Default().Listeners(), func(l Listener) bool {
		return l.Field == "quantar.listen_address"
	}) {
		t.Error("the listener is listed while off")
	}

	cases := []struct {
		name  string
		edit  func(c *Config)
		field string
	}{
		{"turned off", func(c *Config) { c.Quantar.Enabled = false }, "quantar.enabled"},
		{"another port", func(c *Config) { c.Quantar.ListenAddress = "0.0.0.0:1995" }, "quantar.listen_address"},
		{"a router named", func(c *Config) { c.Quantar.AllowedRouters = []string{"192.0.2.4"} }, "quantar.allowed_routers"},
		{"recording turned on", func(c *Config) { c.Quantar.RecordDir = "/tmp/q" }, "quantar.record_dir"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			after := quantarOn()
			tc.edit(&after)
			if got := NeedsRestart(on, after); !slices.Contains(got, tc.field) {
				t.Errorf("NeedsRestart returned %v, want %s", got, tc.field)
			}
		})
	}
	if got := NeedsRestart(on, quantarOn()); len(got) != 0 {
		t.Errorf("an unchanged document needs a restart for %v", got)
	}
}
