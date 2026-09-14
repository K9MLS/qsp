package config

import (
	"strings"
	"testing"
)

// Configuring a transcoder, and refusing a mapping that cannot work: ADR-0063.

// withTranscoder returns a valid configuration carrying a transcoder and a
// bridge that routes a talkgroup to it.
func withTranscoder(t *testing.T, mutate func(*Config)) Config {
	t.Helper()
	// Transcoders are validated inside the DMR block, which is where they
	// belong: a vocoder that carries no DMR carries nothing. enabledDMR
	// supplies the listener and its password so that each case below states
	// only what it is about.
	c := enabledDMR()
	c.DMR.ListenAddress = "127.0.0.1:62031"
	c.DMR.Transcoders = []Transcoder{{
		Name: "dvstick", Enabled: true, Address: "192.168.1.247:2460",
	}}
	c.DMR.Bridges = []Bridge{{
		Name: "zello", Enabled: true,
		Endpoints: []Endpoint{
			{Peer: 0, Talkgroup: 2, Timeslot: 2},
			{Transcoder: "dvstick", Talkgroup: 2, Timeslot: 2},
		},
	}}
	if mutate != nil {
		mutate(&c)
	}
	return c
}

// TestATranscoderAndItsBridgeValidate is the shape an operator writes.
func TestATranscoderAndItsBridgeValidate(t *testing.T) {
	if err := withTranscoder(t, nil).Validate(); err != nil {
		t.Fatalf("a transcoder with a bridge routing to it was refused: %v", err)
	}
}

// TestTheRateDefaultsToTheDmrOne, because it has to be set at all.
//
// The operator's DVstick 30 boots with every RATE pin low and therefore not at
// the DMR rate, so a transcoder that names no rate must still get one. Table
// 115 index 33 is 3600/2450/1150, the rate interoperable with DMR and APCO P25
// half rate.
func TestTheRateDefaultsToTheDmrOne(t *testing.T) {
	if got := (Transcoder{}).RateIndex(); got != 33 {
		t.Errorf("a transcoder naming no rate uses index %d, want 33", got)
	}
	if got := (Transcoder{Rate: 34}).RateIndex(); got != 34 {
		t.Errorf("a transcoder naming rate 34 uses %d", got)
	}
	if DefaultTranscoderRate != 33 {
		t.Errorf("the default rate index is %d, want 33", DefaultTranscoderRate)
	}
}

// TestAMappingThatCannotWorkIsRefused covers both halves of the mismatch.
//
// **A bridge that cannot carry is worse than no bridge**, and a transcoder
// nothing routes to is the same fault with a different subsystem name on it: a
// link nothing routed to cost this project an afternoon, during which the
// socket opened, the far end authenticated, and the advice sent an operator to
// check somebody else's address.
func TestAMappingThatCannotWorkIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"a bridge naming a transcoder that is not configured",
			func(c *Config) { c.DMR.Transcoders = nil },
			"does not match any enabled transcoder"},
		{"a bridge naming a transcoder that is disabled",
			func(c *Config) { c.DMR.Transcoders[0].Enabled = false },
			"does not match any enabled transcoder"},
		{"a transcoder no bridge routes to",
			func(c *Config) { c.DMR.Bridges = nil },
			"no bridge routes a talkgroup to it"},
		{"a transcoder with no address",
			func(c *Config) { c.DMR.Transcoders[0].Address = "" },
			"must not be empty"},
		{"a transcoder with an address that is not host:port",
			func(c *Config) { c.DMR.Transcoders[0].Address = "192.168.1.247" },
			"not a host:port"},
		{"a transcoder with no name",
			func(c *Config) { c.DMR.Transcoders[0].Name = "" },
			"must not be empty"},
		{"two transcoders with one name",
			func(c *Config) {
				c.DMR.Transcoders = append(c.DMR.Transcoders, Transcoder{
					Name: "dvstick", Enabled: true, Address: "127.0.0.1:2460",
				})
			},
			"used by more than one transcoder"},
		{"an endpoint naming a peer and a transcoder",
			func(c *Config) { c.DMR.Bridges[0].Endpoints[1].Peer = 312345 },
			"both a peer and a transcoder"},
		{"an endpoint naming a link and a transcoder",
			func(c *Config) { c.DMR.Bridges[0].Endpoints[1].Upstream = "bm" },
			"both a link and a transcoder"},
	} {
		err := withTranscoder(t, tc.mutate).Validate()
		if err == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s was refused without mentioning %q: %v", tc.name, tc.want, err)
		}
	}
}

// TestARateOutsideTableOneFifteenIsRefusedRatherThanClamped.
//
// The chip accepts an out-of-range index, then produces frames of a width
// nothing expects, and the result presents as bad audio rather than as a
// configuration error. A run went out at 2400 bps once because a code path
// skipped the rate packet, and the only evidence was the width of the frames.
func TestARateOutsideTableOneFifteenIsRefusedRatherThanClamped(t *testing.T) {
	for _, rate := range []int{-1, 62, 100} {
		err := withTranscoder(t, func(c *Config) { c.DMR.Transcoders[0].Rate = rate }).Validate()
		if err == nil {
			t.Errorf("rate index %d was accepted; Table 115 stops at 61", rate)
			continue
		}
		if !strings.Contains(err.Error(), "0 to 61") {
			t.Errorf("the refusal for rate %d does not say the range: %v", rate, err)
		}
	}
	// The ends of the table are valid, and so is the DMR one.
	for _, rate := range []int{1, 33, 61} {
		if err := withTranscoder(t, func(c *Config) {
			c.DMR.Transcoders[0].Rate = rate
		}).Validate(); err != nil {
			t.Errorf("rate index %d was refused: %v", rate, err)
		}
	}
}

// TestADisabledTranscoderNeedsNoBridge, because an operator turning one off
// should not then have to dismantle their bridges to start the server.
func TestADisabledTranscoderNeedsNoBridge(t *testing.T) {
	err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].Enabled = false
		c.DMR.Bridges = nil
	}).Validate()
	if err != nil {
		t.Errorf("a disabled transcoder with no bridge was refused: %v", err)
	}
}
