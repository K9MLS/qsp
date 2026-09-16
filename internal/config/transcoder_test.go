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
		// An ID of the gateway's own, not the XPR8300's 999999 nor the
		// Pi-Star's 3132910. A Zello user has no radio and so no ID, and a
		// frame with source 0 is not a DMR frame — ADR-0064.
		RadioID: 3132911,
		// Where qsp-zello is: ADR-0009 puts it beside QSP on the same host.
		USRPListen: "127.0.0.1:32001",
		USRPPeer:   "127.0.0.1:32002",
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

// TestThePermissionIsClosedWhenEmpty is the one list in this configuration
// that denies by default.
//
// ADR-0062: a Zello user is not necessarily licensed and their audio reaches
// RF, so an unlicensed transmission on a licensed operator's repeater is that
// operator's problem and not something a default may arrange for them. Every
// access list in this configuration is permissive when empty; this one is the
// other way round, because an access list governs a network the operator
// already runs.
func TestThePermissionIsClosedWhenEmpty(t *testing.T) {
	c := withTranscoder(t, nil)
	if got := c.DMR.Transcoders[0].PermitPeers; len(got) != 0 {
		t.Fatalf("the fixture already permits %v", got)
	}
	if c.DMR.Transcoders[0].PermitAllPeers {
		t.Fatal("the fixture already permits every peer")
	}
	// An empty permission is a valid configuration: a mapping that carries
	// audio into the vocoder and nothing back out is a perfectly ordinary
	// thing to run, and refusing it would force an operator to grant a
	// permission in order to start.
	if err := c.Validate(); err != nil {
		t.Errorf("a transcoder permitting nobody was refused: %v", err)
	}
}

// TestAZeroInThePermissionListIsRefused guards the convention clash.
//
// 0 means "every peer" everywhere else in this configuration, so somebody will
// eventually write it here meaning that. Ignoring it would leave an operator
// believing they had permitted a repeater while the repeater received nothing;
// honouring it would silently permit every repeater on the network to carry
// possibly unlicensed audio.
func TestAZeroInThePermissionListIsRefused(t *testing.T) {
	err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].PermitPeers = []uint32{312345, 0}
	}).Validate()
	if err == nil {
		t.Fatal("a zero in permit_peers was accepted")
	}
	if !strings.Contains(err.Error(), "permit_all_peers") {
		t.Errorf("the refusal does not point at the deliberate way to say it: %v", err)
	}
}

// TestPermittingEverybodyAndSomebodyIsRefused, because the document would then
// say two things and do one.
func TestPermittingEverybodyAndSomebodyIsRefused(t *testing.T) {
	err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].PermitAllPeers = true
		c.DMR.Transcoders[0].PermitPeers = []uint32{312345}
	}).Validate()
	if err == nil {
		t.Fatal("a transcoder permitting every peer and also listing one was accepted")
	}
	if !strings.Contains(err.Error(), "no effect") {
		t.Errorf("the refusal does not say which half is inert: %v", err)
	}

	// Either on its own is fine.
	if err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].PermitAllPeers = true
	}).Validate(); err != nil {
		t.Errorf("permitting every peer was refused: %v", err)
	}
	if err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].PermitPeers = []uint32{312345, 315544}
	}).Validate(); err != nil {
		t.Errorf("permitting two peers was refused: %v", err)
	}
}

// TestAnEnabledTranscoderNeedsAnIdOfItsOwn is the field a Zello user borrows.
//
// **Radios embed the source ID in every burst** — the voice LC header, the
// terminator and the embedded LC — so a frame without one is not a DMR frame.
// A Zello user has no radio and therefore no ID, which makes this the only
// source a transcoded transmission can carry. Refused at startup rather than
// discovered as silence on the air.
func TestAnEnabledTranscoderNeedsAnIdOfItsOwn(t *testing.T) {
	err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].RadioID = 0
	}).Validate()
	if err == nil {
		t.Fatal("an enabled transcoder with no radio ID was accepted")
	}
	// The advice has to say *its own*, because sharing a repeater's is the
	// mistake somebody will make: it works, and makes transcoded traffic
	// indistinguishable from that machine's in Last heard and on every
	// linked server.
	if !strings.Contains(err.Error(), "of its own") {
		t.Errorf("the refusal does not warn against sharing an ID: %v", err)
	}

	// A disabled one needs nothing, so turning a channel off does not force
	// an operator to invent an ID in order to start.
	if err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].Enabled = false
		c.DMR.Transcoders[0].RadioID = 0
		c.DMR.Bridges = nil
	}).Validate(); err != nil {
		t.Errorf("a disabled transcoder with no radio ID was refused: %v", err)
	}
}

// TestARadioIdOutsideTheDmrRangeIsRefused.
//
// ETSI TS 102 361-1 Annex A: source IDs run to 16776415, and the top of the
// 24-bit space is reserved for non-addressable gateways. An ID above it is a
// source nothing can match.
func TestARadioIdOutsideTheDmrRangeIsRefused(t *testing.T) {
	for _, id := range []uint32{16776416, 16777215, 1 << 24} {
		err := withTranscoder(t, func(c *Config) {
			c.DMR.Transcoders[0].RadioID = id
		}).Validate()
		if err == nil {
			t.Errorf("radio ID %d was accepted; the range stops at 16776415", id)
			continue
		}
		if !strings.Contains(err.Error(), "16776415") {
			t.Errorf("the refusal for %d does not state the range: %v", id, err)
		}
	}
	// The ends of the range are valid.
	for _, id := range []uint32{1, 3132911, 16776415} {
		if err := withTranscoder(t, func(c *Config) {
			c.DMR.Transcoders[0].RadioID = id
		}).Validate(); err != nil {
			t.Errorf("radio ID %d was refused: %v", id, err)
		}
	}
}

// TestTheAliasIsOptionalAndBounded.
//
// Talker Alias is display data, not station identification — the operator
// identifies by voice, as on an EchoLink-equipped repeater (ADR-0064). So an
// empty alias is an ordinary configuration and not an omission.
//
// The bound is what a radio will send: Motorola's Inband Caller Alias allows
// 31 characters, and the field is fragmented across a voice superframe. A
// longer string is truncated somewhere an operator cannot see, which is worse
// than a refusal at startup.
func TestTheAliasIsOptionalAndBounded(t *testing.T) {
	if err := withTranscoder(t, nil).Validate(); err != nil {
		t.Errorf("a transcoder with no alias was refused: %v", err)
	}
	if err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].Alias = "K9MLS"
	}).Validate(); err != nil {
		t.Errorf("a transcoder with a callsign alias was refused: %v", err)
	}
	if err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].Alias = strings.Repeat("A", 31)
	}).Validate(); err != nil {
		t.Errorf("a 31-character alias was refused: %v", err)
	}
	err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].Alias = strings.Repeat("A", 32)
	}).Validate()
	if err == nil {
		t.Fatal("a 32-character alias was accepted")
	}
	if !strings.Contains(err.Error(), "31") {
		t.Errorf("the refusal does not state the limit: %v", err)
	}
	// Counted in characters rather than bytes, because a callsign is not the
	// only thing anybody will put here.
	if err := withTranscoder(t, func(c *Config) {
		c.DMR.Transcoders[0].Alias = strings.Repeat("é", 31)
	}).Validate(); err != nil {
		t.Errorf("31 multi-byte characters were refused as too long: %v", err)
	}
}

// TestTheUSRPAddressesOfAnEnabledTranscoderAreChecked.
//
// **A transcoder with nowhere to send audio decodes every call into nothing**,
// and the peer address is the only protection USRP has, so both are refused at
// startup rather than discovered as silence or as a stranger keying up.
//
// To see a row fail, remove its case from the transcoder block in Validate.
func TestTheUSRPAddressesOfAnEnabledTranscoderAreChecked(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*Transcoder)
		wantField string // empty means valid
	}{
		{"both given", func(*Transcoder) {}, ""},
		{"a wildcard listen address is allowed, for the container install",
			func(tr *Transcoder) { tr.USRPListen = "0.0.0.0:32001" }, ""},
		{"no listen address", func(tr *Transcoder) { tr.USRPListen = "" }, "usrp_listen"},
		{"no peer", func(tr *Transcoder) { tr.USRPPeer = "" }, "usrp_peer"},
		{"a hostname peer", func(tr *Transcoder) { tr.USRPPeer = "zello.lan:32002" }, "usrp_peer"},
		{"a wildcard peer", func(tr *Transcoder) { tr.USRPPeer = "0.0.0.0:32002" }, "usrp_peer"},
		{"a peer with port 0", func(tr *Transcoder) { tr.USRPPeer = "127.0.0.1:0" }, "usrp_peer"},
		{"the peer is the listen address", func(tr *Transcoder) { tr.USRPPeer = tr.USRPListen }, "usrp_peer"},
		{"the peer is AMBEserver", func(tr *Transcoder) { tr.USRPPeer = tr.Address }, "usrp_peer"},
		{"a disabled transcoder needs neither", func(tr *Transcoder) {
			tr.Enabled = false
			tr.USRPListen, tr.USRPPeer = "", ""
		}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := withTranscoder(t, func(c *Config) { tc.mutate(&c.DMR.Transcoders[0]) })
			err := c.Validate()
			if tc.wantField == "" {
				// A disabled transcoder is reported as unrouted-to by nothing,
				// but the bridge naming it is refused; only USRP is at issue.
				if err != nil && strings.Contains(err.Error(), "usrp_") {
					t.Fatalf("refused over USRP: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "dmr.transcoders[0]."+tc.wantField) {
				t.Fatalf("error %v, want a refusal naming %s", err, tc.wantField)
			}
		})
	}
}

// TestTheUSRPListenAddressIsAListener, so -check tries to bind it and the
// collision rule refuses a second thing on its port.
func TestTheUSRPListenAddressIsAListener(t *testing.T) {
	c := withTranscoder(t, nil)
	found := false
	for _, l := range c.Listeners() {
		if l.Address == "127.0.0.1:32001" && l.Network == "udp" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the USRP listen address is not among %+v; -check would not try it", c.Listeners())
	}
	clash := withTranscoder(t, func(c *Config) { c.DMR.Transcoders[0].USRPListen = c.DMR.ListenAddress })
	if err := clash.Validate(); err == nil {
		t.Error("a USRP socket on the DMR listener's own port validated")
	}
}

// TestAPausedBridgeMayNameAPausedTranscoder: switching Zello off on its page
// disables both, and must not have to delete the bridge — and with it the
// talkgroup and repeaters an operator chose.
//
// To see it bite: delete the paused-bridge case in Validate.
func TestAPausedBridgeMayNameAPausedTranscoder(t *testing.T) {
	tests := []struct {
		name          string
		bridgeOn      bool
		transcoderOn  bool
		transcoderSet bool
		wantRefusal   bool
	}{
		{"both on", true, true, true, false},
		{"both paused", false, false, true, false},
		{"a live bridge to a paused transcoder is still refused", true, false, true, true},
		{"a paused bridge to a transcoder that does not exist is still refused", false, false, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := withTranscoder(t, func(c *Config) {
				c.DMR.Transcoders[0].Enabled = tc.transcoderOn
				c.DMR.Bridges[0].Enabled = tc.bridgeOn
				if !tc.transcoderSet {
					c.DMR.Transcoders = nil
				}
			})
			err := c.Validate()
			refused := err != nil && strings.Contains(err.Error(), "does not match any enabled transcoder")
			if refused != tc.wantRefusal {
				t.Fatalf("refused = %v, want %v: %v", refused, tc.wantRefusal, err)
			}
		})
	}
}
