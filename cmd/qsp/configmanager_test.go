package main

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// managedConfig is a configuration with something behind every kind of field
// a handler edits in place: a link, a bridge and an access list.
func managedConfig() config.Config {
	cfg := config.Default()
	cfg.DMR.Upstreams = []config.Upstream{{
		Name: "cameron", Protocol: "openbridge",
		Address: "kb9tyc.example.com:62045", ListenAddress: "0.0.0.0:62045",
		NetworkID: 3127045,
	}}
	cfg.DMR.Access = &config.Access{
		Registration: config.ACL{Mode: "deny", IDs: []string{"3132911"}},
	}
	cfg.IPSC.PeerNames = map[uint32]string{313291: "the repeater"}
	return cfg
}

// What the manager hands out is the caller's to change, and what it is handed
// stays the manager's.
//
// **Current returned the struct, which shares every slice and the Access
// pointer.** A handler editing a link's address edited the running
// configuration and the startup one together, so PendingRestart compared two
// views of the same memory and reported a server in step with a configuration
// it was not running.
//
// To see it fail: make Current return m.current, or drop `cfg = cfg.Clone()`
// from Save.
func TestTheManagerSharesNothingWithItsCallers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"a link's address", func(c *config.Config) { c.DMR.Upstreams[0].Address = "elsewhere.example.com:1" }},
		{"an access list entry", func(c *config.Config) { c.DMR.Access.Registration.IDs[0] = "1" }},
		{"the access block itself", func(c *config.Config) { c.DMR.Access.Registration.Mode = "permit" }},
		{"a repeater's name", func(c *config.Config) { c.IPSC.PeerNames[313291] = "renamed" }},
	} {
		t.Run(tc.name+", through Current", func(t *testing.T) {
			m := &configManager{current: managedConfig(), startup: managedConfig()}

			got := m.Current()
			tc.mutate(&got)

			if !reflect.DeepEqual(m.current, managedConfig()) {
				t.Error("editing what Current returned changed the running configuration")
			}
			if !reflect.DeepEqual(m.startup, managedConfig()) {
				t.Error("editing what Current returned changed the startup configuration")
			}
			if pending := m.PendingRestart(); len(pending) != 0 {
				t.Errorf("nothing was saved, and a restart is pending for %v", pending)
			}
		})

		t.Run(tc.name+", after Save", func(t *testing.T) {
			writer, err := config.NewWriter(filepath.Join(t.TempDir(), "qsp.json"))
			if err != nil {
				t.Fatalf("NewWriter: %v", err)
			}
			m := &configManager{current: config.Default(), startup: config.Default(), writer: writer}

			cfg := managedConfig()
			if _, err := m.Save(context.Background(), cfg, "K9MLS", "a link"); err != nil {
				t.Fatalf("Save: %v", err)
			}
			tc.mutate(&cfg)

			if !reflect.DeepEqual(m.current, managedConfig()) {
				t.Error("editing a configuration after saving it changed the running one")
			}
		})
	}
}
