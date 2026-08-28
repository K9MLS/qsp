package peers_test

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// A peer's announced position. It is what the station says, not what is true,
// and QSP reports it only when it can stand behind it.

// withConfig registers a peer carrying a given configuration.
func (h *harness) withConfig(cfg hbp.Config) peers.Peer {
	h.t.Helper()
	cfg.RepeaterID = testID
	if cfg.Callsign == "" {
		cfg.Callsign = "K9MLS"
	}

	out := h.send(hbp.Login{RepeaterID: testID}, addrA)
	ack, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		h.t.Fatalf("challenge: %v", err)
	}
	digest := hbp.Digest(ack.(hbp.Ack).Salt(), []byte(testPassword))
	h.send(hbp.Key{RepeaterID: testID, Digest: digest}, addrA)
	h.send(cfg, addrA)

	p, ok := h.m.Lookup(testID)
	if !ok {
		h.t.Fatal("the peer did not register")
	}
	return p
}

func TestAPeerWithNoConfigurationHasNoPosition(t *testing.T) {
	h := newHarness(t)
	h.send(hbp.Login{RepeaterID: testID}, addrA)

	p, ok := h.m.Lookup(testID)
	if !ok {
		t.Fatal("no peer")
	}
	if pos := p.Position(); pos.Located {
		t.Error("a peer that has not sent its configuration reported a position")
	}
}

func TestAnnouncedCoordinatesAreReported(t *testing.T) {
	h := newHarness(t)
	p := h.withConfig(hbp.Config{
		Latitude: "33.2148", Longitude: "-97.1331", Height: "10",
		Location: "Denton, TX",
	})

	pos := p.Position()
	if !pos.Located {
		t.Fatal("plausible coordinates were not reported")
	}
	if pos.Latitude != 33.2148 || pos.Longitude != -97.1331 {
		t.Errorf("coordinates are %v, %v", pos.Latitude, pos.Longitude)
	}
	if pos.Height != 10 {
		t.Errorf("height is %d, want 10", pos.Height)
	}
	if pos.Location != "Denton, TX" {
		t.Errorf("location is %q", pos.Location)
	}
}

// TestRubbishCoordinatesAreNotAPin. A pin in the wrong place is believed; a
// missing pin prompts somebody to ask.
func TestRubbishCoordinatesAreNotAPin(t *testing.T) {
	for _, tc := range []struct{ name, lat, lon string }{
		{"empty", "", ""},
		{"words", "north", "west"},
		{"latitude out of range", "91.0", "0.5"},
		{"longitude out of range", "45.0", "181.0"},
		{"latitude far out", "9999", "0"},
		{"not a number", "33.2.1", "-97.1"},
		{"only one side", "33.2148", ""},
		{"infinity", "Inf", "-97.1"},
		{"not a number literal", "NaN", "-97.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			p := h.withConfig(hbp.Config{Latitude: tc.lat, Longitude: tc.lon, Location: "Somewhere"})

			pos := p.Position()
			if pos.Located {
				t.Errorf("%q, %q was reported as a position: %v, %v",
					tc.lat, tc.lon, pos.Latitude, pos.Longitude)
			}
			// The place name is still worth showing: it is useful even when
			// the coordinates are rubbish.
			if pos.Location != "Somewhere" {
				t.Errorf("the location text was discarded: %q", pos.Location)
			}
		})
	}
}

// TestNullIslandIsNotAPosition. Zero is a real coordinate in the Gulf of Guinea
// and is almost never where a hotspot is; it is what an unset field looks like.
func TestNullIslandIsNotAPosition(t *testing.T) {
	h := newHarness(t)
	p := h.withConfig(hbp.Config{Latitude: "0", Longitude: "0", Location: "Unset"})

	if p.Position().Located {
		t.Error("0,0 was reported as a position")
	}
}

// TestAZeroOnOneAxisIsStillAPosition. The equator and the prime meridian are
// real places, and only both being zero at once is suspicious.
func TestAZeroOnOneAxisIsStillAPosition(t *testing.T) {
	h := newHarness(t)
	p := h.withConfig(hbp.Config{Latitude: "0", Longitude: "-97.1331"})

	pos := p.Position()
	if !pos.Located {
		t.Fatal("a station on the equator was refused a position")
	}
	if pos.Latitude != 0 {
		t.Errorf("latitude is %v, want 0", pos.Latitude)
	}
}

func TestPaddingAndSignsAreTolerated(t *testing.T) {
	h := newHarness(t)
	p := h.withConfig(hbp.Config{
		Latitude: " 33.21 ", Longitude: " +97.13 ", Height: " 25 ", Location: " Denton  ",
	})

	pos := p.Position()
	if !pos.Located {
		t.Fatal("space-padded coordinates were refused")
	}
	if pos.Latitude != 33.21 || pos.Longitude != 97.13 {
		t.Errorf("coordinates are %v, %v", pos.Latitude, pos.Longitude)
	}
	if pos.Height != 25 {
		t.Errorf("height is %d, want 25", pos.Height)
	}
	if pos.Location != "Denton" {
		t.Errorf("location is %q, want it trimmed", pos.Location)
	}
}

// TestABadHeightIsZeroRatherThanAnError. Nothing depends on telling a peer at
// ground level from one that sent rubbish, so the simpler answer is right.
func TestABadHeightIsZeroRatherThanAnError(t *testing.T) {
	h := newHarness(t)
	p := h.withConfig(hbp.Config{
		Latitude: "33.2", Longitude: "-97.1", Height: "abc",
	})

	pos := p.Position()
	if !pos.Located {
		t.Fatal("a bad height cost the peer its coordinates")
	}
	if pos.Height != 0 {
		t.Errorf("height is %d, want 0", pos.Height)
	}
}

// TestSilenceAndRubbishAreDifferentProblems. A hotspot nobody configured and a
// hotspot configured wrongly both produce no pin, and only the second is
// something its owner can fix — if anybody says which happened.
func TestSilenceAndRubbishAreDifferentProblems(t *testing.T) {
	h := newHarness(t)
	p := h.withConfig(hbp.Config{Location: "Denton"})
	if got := p.Position().Refused; got != "" {
		t.Errorf("a peer that announced nothing reported a refusal: %q", got)
	}

	h2 := newHarness(t)
	p2 := h2.withConfig(hbp.Config{Latitude: "north", Longitude: "west"})
	if p2.Position().Refused == "" {
		t.Error("a peer that announced rubbish reported no refusal")
	}
}

// TestNullIslandSaysSo is the case a real hotspot produced. A WPSD hotspot with
// DMRGateway's [Info] block disabled sends "0.000000" and "00.000000", and the
// console said only that nothing had been announced — which was true of the
// position and useless about the cause.
func TestNullIslandSaysSo(t *testing.T) {
	h := newHarness(t)
	// The exact bytes observed on the wire from a WPSD hotspot on 2026-08-28.
	p := h.withConfig(hbp.Config{
		Latitude: "0.000000", Longitude: "00.000000", Height: "000",
		Location: "Denton, EM13kd",
	})

	pos := p.Position()
	if pos.Located {
		t.Fatal("0,0 was drawn as a position")
	}
	if pos.Refused == "" {
		t.Fatal("0,0 was refused without saying so")
	}
	if !strings.Contains(pos.Refused, "hotspot") {
		t.Errorf("the refusal should say where to fix it: %q", pos.Refused)
	}
	// The place name is still worth having.
	if pos.Location != "Denton, EM13kd" {
		t.Errorf("the location text was lost: %q", pos.Location)
	}
}

// TestAGoodPositionIsNotRefused guards the obvious regression.
func TestAGoodPositionIsNotRefused(t *testing.T) {
	h := newHarness(t)
	p := h.withConfig(hbp.Config{Latitude: "33.1481", Longitude: "-97.1201"})

	pos := p.Position()
	if !pos.Located {
		t.Fatal("a real position was refused")
	}
	if pos.Refused != "" {
		t.Errorf("a usable position carried a refusal: %q", pos.Refused)
	}
}
