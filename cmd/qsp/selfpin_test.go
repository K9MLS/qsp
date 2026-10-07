package main

import (
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// This server on its own map, and in what it tells the servers linked to it,
// from the position its identity was given.
//
// Break it: draw a server at nought and nought, or have serverIdentity say
// where the server is when it was given no position.
func TestAServerIsDrawnWhereItsIdentitySays(t *testing.T) {
	tests := []struct {
		name     string
		lat, lon float64
		drawn    bool
	}{
		{"a position", 33.2148, -97.1331, true},
		{"none entered", 0, 0, false},
		{"on the equator", 0, -78.5, true},
		{"a latitude off the globe", 133.2, -97.1, false},
		{"a longitude off the globe", 33.2, -197.1, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Default()
			cfg.DMR.Join.NetworkName = "K9MLS-01"
			cfg.DMR.Identity = config.Identity{Callsign: "K9MLS", Location: "Denton, TX",
				Latitude: tc.lat, Longitude: tc.lon}

			pin := mapSettings(cfg).Self
			if (pin != nil) != tc.drawn {
				t.Fatalf("drawn on its own map: %v, want %v", pin != nil, tc.drawn)
			}
			said := serverIdentity(cfg)
			if said.Located != tc.drawn {
				t.Fatalf("tells linked servers where it is: %v, want %v", said.Located, tc.drawn)
			}
			if said.Network != "K9MLS-01" || said.Callsign != "K9MLS" {
				t.Errorf("its name and callsign changed: %+v", said)
			}
			if !tc.drawn {
				return
			}
			if !pin.Self || pin.Name != "K9MLS-01" || pin.Callsign != "K9MLS" || pin.Location != "Denton, TX" ||
				pin.Latitude != tc.lat || pin.Longitude != tc.lon {
				t.Errorf("the pin is %+v", *pin)
			}
			if said.Latitude != tc.lat || said.Longitude != tc.lon || said.Location != "Denton, TX" {
				t.Errorf("it announces %v, %v (%q)", said.Latitude, said.Longitude, said.Location)
			}
		})
	}
}
