package main

import (
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// TestTheJoinSettingsCarryTheP25PortOnlyWhenItIsOn: the DVSwitch guide
// prints the port a node in P25 mode points at, and says the network takes
// no P25 gateways when the port is zero.
//
// To see it fail: drop the `if cfg.P25.Enabled` test in joinSettings, and the
// first case prints a port nothing answers on; set P25Port from the DMR
// listen address, and the others give 62031.
func TestTheJoinSettingsCarryTheP25PortOnlyWhenItIsOn(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
		listen  string
		want    int
	}{
		{"the listener off", false, "0.0.0.0:41000", 0},
		{"the usual port", true, "0.0.0.0:41000", 41000},
		{"another port", true, "0.0.0.0:41009", 41009},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.P25.Enabled = tc.enabled
			cfg.P25.ListenAddress = tc.listen
			if got := joinSettings(cfg).P25Port; got != tc.want {
				t.Errorf("P25 port %d, want %d", got, tc.want)
			}
		})
	}
}
