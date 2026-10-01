package main

import (
	"slices"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// The Weather block reaches the service whole, and the contact falls back to
// the one radio ID lookups already use, so an operator is not asked twice.
//
// To see it fail: pass c.Weather.Contact instead of c.WeatherContact() in
// weatherSettings, and the fallback case gets an empty contact.
func TestWeatherSettingsReachTheService(t *testing.T) {
	c := config.Default()
	c.Weather = config.Weather{
		Enabled: true, Transmit: true, Zones: []string{"TXC121"}, Events: []string{"Tornado Warning"},
		Talkgroup: 2, Timeslot: 2, SenderID: 9990,
	}
	c.DMR.Callsigns.Contact = "k9mls@example.org"

	s := weatherSettings(c)
	if !s.Enabled || !s.Transmit || !slices.Equal(s.Zones, []string{"TXC121"}) || !slices.Equal(s.Events, []string{"Tornado Warning"}) ||
		s.Talkgroup != 2 || s.Timeslot != 2 || s.SenderID != 9990 {
		t.Errorf("settings %+v", s)
	}
	if s.Contact != "k9mls@example.org" {
		t.Errorf("contact %q, want the radio ID lookup contact", s.Contact)
	}
	c.Weather.Contact = "wx@example.org"
	if got := weatherSettings(c).Contact; got != "wx@example.org" {
		t.Errorf("the page's own contact was replaced: %q", got)
	}
}
