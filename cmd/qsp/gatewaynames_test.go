package main

import (
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// TestAZelloCallIsNamedTheWayItWentOut covers how Last heard names a call
// carrying a transcoder's gateway ID.
//
// **It showed a bare number for every Zello call**, because the gateway ID is
// no hotspot's registration and usually nobody's registered DMR ID -- the
// WPSD dashboard on 2026-09-21 showed "9898" for the same reason. QSP put that
// ID on the call, so it can name it without anybody's database, which is the
// rule callsigns() already follows for a hotspot's own ID.
//
// To see rows fail: drop the gateway from knownNames; let a registered peer
// lose to the alias; or keep a disabled transcoder's alias.
func TestAZelloCallIsNamedTheWayItWentOut(t *testing.T) {
	const gateway = 9898

	transcoder := func(enabled bool, id uint32, alias string) config.Config {
		cfg := config.Default()
		cfg.DMR.Transcoders = []config.Transcoder{{
			Name: "zello", Enabled: enabled, RadioID: id, Alias: alias,
		}}
		return cfg
	}

	tests := []struct {
		name       string
		cfg        config.Config
		registered map[uint32]string
		id         uint32
		want       string
	}{
		{
			name: "the gateway ID, with an alias, is named by it",
			cfg:  transcoder(true, gateway, "KD9BXO"),
			id:   gateway, want: "KD9BXO",
		},
		{
			name: "no alias leaves the number, rather than a name no radio shows",
			cfg:  transcoder(true, gateway, ""),
			id:   gateway, want: "",
		},
		{
			name: "whitespace is no alias",
			cfg:  transcoder(true, gateway, "   "),
			id:   gateway, want: "",
		},
		{
			name: "a disabled transcoder puts nothing on the air, so names nothing",
			cfg:  transcoder(false, gateway, "KD9BXO"),
			id:   gateway, want: "",
		},
		{
			name: "a transcoder with no gateway ID names nothing",
			cfg:  transcoder(true, 0, "KD9BXO"),
			id:   0, want: "",
		},
		{
			name:       "a hotspot's own registration is unchanged",
			cfg:        transcoder(true, gateway, "KD9BXO"),
			registered: map[uint32]string{3155413: "KB9TYC"},
			id:         3155413, want: "KB9TYC",
		},
		{
			name:       "a hotspot registered with the gateway's ID wins over the alias",
			cfg:        transcoder(true, gateway, "KD9BXO"),
			registered: map[uint32]string{gateway: "KB9TYC"},
			id:         gateway, want: "KB9TYC",
		},
		{
			name: "an ID nobody knows is left to the registry",
			cfg:  transcoder(true, gateway, "KD9BXO"),
			id:   1234567, want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			known := knownNames(gatewayNames(tc.cfg), tc.registered)
			// resolve with no registry service: the row is about what QSP
			// knows itself, and the registry is the fallback it already had.
			if got := resolve(tc.id, known, nil); got != tc.want {
				t.Errorf("ID %d is named %q, want %q", tc.id, got, tc.want)
			}
		})
	}
}
