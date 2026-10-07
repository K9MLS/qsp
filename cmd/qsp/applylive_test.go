package main

import (
	"context"
	"testing"

	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/p25link"
)

// TestASaveReachesAServerWithNoDMRListener. Applying a save was wired only
// when there was a DMR listener, and it went through that listener before
// anything else: a server running P25 gateways or Motorola repeaters and no
// hotspots applied nothing it was saved (2026-10-07, G7), and no server
// applied the P25 allow list at all (G1).
//
// To see it fail: in applyToListener, return before SetAllowedCallsigns when
// listener is nil, or remove the call.
func TestASaveReachesAServerWithNoDMRListener(t *testing.T) {
	pl, err := p25link.New(logging.Discard(), p25link.Config{
		ListenAddress: "127.0.0.1:0", Callsign: "W9XYZ", AllowedCallsigns: []string{"K9MLS"},
	})
	if err != nil {
		t.Fatalf("p25link.New: %v", err)
	}
	apply := applyToListener(nil, nil, pl)

	for _, tc := range []struct {
		name    string
		allowed []string
		admits  map[string]bool
	}{
		{"one swapped for another", []string{"KD9EJA"}, map[string]bool{"K9MLS": false, "KD9EJA": true}},
		{"the list emptied", nil, map[string]bool{"K9MLS": true, "KD9EJA": true, "N0CALL": true}},
		{"one named again", []string{"k9mls"}, map[string]bool{"K9MLS": true, "KD9EJA": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.P25.AllowedCallsigns = tc.allowed
			if err := apply(cfg, "K9MLS", "the allow list"); err != nil {
				t.Fatalf("a save was not applied on a server with no DMR listener: %v", err)
			}
			for callsign, want := range tc.admits {
				if got := pl.Admits(callsign); got != want {
					t.Errorf("with %v saved, %s is admitted: %v, want %v", tc.allowed, callsign, got, want)
				}
			}
		})
	}
}

// TestWhatASaveChangesInARunningServer. Three things the configuration
// manager hands the console's server when a configuration is saved.
//
// What this server tells a link about itself was fixed at startup, so a
// server renamed or moved went on announcing the old name and place (G8).
// And Forwarding went the other way: the routing core is built at startup or
// not at all, and the Overview was told the saved setting, so it said traffic
// was relayed from the moment the box was ticked (G2).
//
// To see each fail: in build, drop a.identity.Store from applyServer; pass
// c.DMR.Enabled && c.DMR.Forwarding to ApplyConfig.
func TestWhatASaveChangesInARunningServer(t *testing.T) {
	started := testConfigNoDriver(t)
	started.DMR.Join.NetworkName = "Old Name"
	started.DMR.Identity.Callsign = "W9OLD"
	a, err := build(context.Background(), started, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	if got := a.identity.Load(); got.Network != "Old Name" || got.Callsign != "W9OLD" {
		t.Fatalf("at startup this server says it is %q %q", got.Network, got.Callsign)
	}
	if a.srv.Forwarding() {
		t.Fatal("this server was started with forwarding off and says it is on")
	}

	saved := started.Clone()
	saved.DMR.Join.NetworkName = "New Name"
	saved.DMR.Identity.Callsign = "W9NEW"
	saved.DMR.Identity.Latitude, saved.DMR.Identity.Longitude = 41.5, -87.5
	saved.DMR.Enabled, saved.DMR.Forwarding = true, true
	a.configManager.applyServer(saved)

	got := a.identity.Load()
	if got.Network != "New Name" || got.Callsign != "W9NEW" {
		t.Errorf("after the save a link is told %q %q, want the new name and callsign", got.Network, got.Callsign)
	}
	if !got.Located || got.Latitude != 41.5 {
		t.Errorf("after the save a link is told the server is at %v,%v (located %v)", got.Latitude, got.Longitude, got.Located)
	}
	if a.srv.Join().NetworkName != "New Name" {
		t.Errorf("the join page still says %q", a.srv.Join().NetworkName)
	}
	if a.srv.Forwarding() {
		t.Error("forwarding was saved on, nothing is forwarding until a restart, and the Overview is told it is on")
	}
	if pending := config.NeedsRestart(started, saved); len(pending) == 0 {
		t.Error("and nothing says a restart is needed")
	}
}
