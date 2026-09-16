package main

import (
	"testing"

	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestATranscodersOutboundTalkgroupIsItsBridgeEndpoint.
//
// To see a row fail: cast the timeslot with hbp.Timeslot() instead of
// timeslot(), and "an unset timeslot" produces 0 where the routing table
// reads TS1, so routing matches nothing the frame says.
func TestATranscodersOutboundTalkgroupIsItsBridgeEndpoint(t *testing.T) {
	bridge := func(name string, tg uint32, ts int) config.Bridge {
		return config.Bridge{Name: "b" + name, Endpoints: []config.Endpoint{
			{Talkgroup: tg, Timeslot: ts},
			{Transcoder: name, Talkgroup: tg, Timeslot: ts},
		}}
	}
	tests := []struct {
		name    string
		bridges []config.Bridge
		lookup  string
		wantTG  uint32
		wantTS  hbp.Timeslot
	}{
		{"one bridge", []config.Bridge{bridge("dvstick", 2, 2)}, "dvstick", 2, hbp.Timeslot2},
		{"names match as configuration matches them", []config.Bridge{bridge("DVstick", 11, 1)}, " dvstick ", 11, hbp.Timeslot1},
		{"the first bridge naming it", []config.Bridge{bridge("other", 9, 1), bridge("dvstick", 3100, 2), bridge("dvstick", 91, 1)}, "dvstick", 3100, hbp.Timeslot2},
		{"an unset timeslot is TS1, as the table reads it", []config.Bridge{bridge("dvstick", 2, 0)}, "dvstick", 2, hbp.Timeslot1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var cfg config.Config
			cfg.DMR.Bridges = tc.bridges
			tg, ts := transcoderEndpoint(cfg, tc.lookup)
			if tg != tc.wantTG || ts != tc.wantTS {
				t.Errorf("got TG%d %s, want TG%d %s", tg, ts, tc.wantTG, tc.wantTS)
			}
		})
	}
}
