package config

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/weather"
)

func weatherOn() Config {
	c := Default()
	c.Weather = Weather{
		Enabled: true, Transmit: true, Zones: []string{"TXC121", "TXZ103"},
		Events:    []string{"Tornado Warning"},
		Talkgroup: 2, Timeslot: 2, SenderID: 9990, Contact: "k9mls@example.org",
	}
	return c
}

// Each rule names its field and says what to do, in the words the Weather
// page shows.
//
// To see it fail: remove the c.validateWeather(v) call from Validate, and
// every refusal case is accepted.
func TestWeatherValidation(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(c *Config)
		field string // empty: valid
	}{
		{"on and complete", func(*Config) {}, ""},
		{"off with nothing set", func(c *Config) { c.Weather = Weather{} }, ""},
		{"off keeps half-finished values", func(c *Config) { c.Weather.Enabled = false; c.Weather.Zones = []string{"nonsense"} }, ""},
		{"contact from radio ID lookups", func(c *Config) { c.Weather.Contact = ""; c.DMR.Callsigns.Contact = "k9mls@example.org" }, ""},
		{"no codes", func(c *Config) { c.Weather.Zones = nil }, "weather.zones"},
		{"a code in the wrong shape", func(c *Config) { c.Weather.Zones = []string{"Denton"} }, "weather.zones[0]"},
		{"a lower-case code", func(c *Config) { c.Weather.Zones = []string{"txc121"} }, "weather.zones[0]"},
		{"no events", func(c *Config) { c.Weather.Events = nil }, "weather.events"},
		{"an empty event", func(c *Config) { c.Weather.Events = []string{" "} }, "weather.events[0]"},
		{"no talkgroup", func(c *Config) { c.Weather.Talkgroup = 0 }, "weather.talkgroup"},
		{"a talkgroup too large", func(c *Config) { c.Weather.Talkgroup = 1 << 24 }, "weather.talkgroup"},
		{"timeslot 3", func(c *Config) { c.Weather.Timeslot = 3 }, "weather.timeslot"},
		{"no sender", func(c *Config) { c.Weather.SenderID = 0 }, "weather.sender_id"},
		{"no contact anywhere", func(c *Config) { c.Weather.Contact = ""; c.DMR.Callsigns.Contact = "" }, "weather.contact"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := weatherOn()
			tc.edit(&c)
			err := c.Validate()
			if tc.field == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			ve, ok := err.(*ValidationError)
			if !ok {
				t.Fatalf("got %v, want a refusal naming %s", err, tc.field)
			}
			for _, fe := range ve.Errors {
				if fe.Field == tc.field {
					if fe.Problem == "" || fe.Fix == "" {
						t.Errorf("%s is refused without saying what is wrong and what to do: %+v", tc.field, fe)
					}
					return
				}
			}
			t.Errorf("the refusal does not name %s: %v", tc.field, err)
		})
	}
}

// Configuration and the service agree on what a code looks like, so the page
// never saves a code the service then rejects, or the other way round.
func TestWeatherCodeShapesAgree(t *testing.T) {
	for _, code := range []string{"TXC121", "TXZ103", "OKZ001", "txc121", "TX121", "TXX121", "TXC1210", ""} {
		if got, want := weatherZone.MatchString(code), weather.ValidZoneCode(code); got != want {
			t.Errorf("%q: configuration says %v, the service says %v", code, got, want)
		}
	}
}

// An instance that never used weather writes no weather block, and one that
// did reads its block back unchanged.
//
// To see it fail: drop omitzero from the Weather field's tag, and every
// saved configuration gains an empty block.
func TestWeatherRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := Save(&buf, Default()); err != nil {
		t.Fatalf("%v", err)
	}
	if strings.Contains(buf.String(), `"weather"`) {
		t.Error("a configuration that never used weather writes a weather block")
	}

	buf.Reset()
	on := weatherOn()
	if err := Save(&buf, on); err != nil {
		t.Fatalf("%v", err)
	}
	back, err := Load(&buf)
	if err != nil {
		t.Fatalf("%v", err)
	}
	a, _ := json.Marshal(on.Weather)
	b, _ := json.Marshal(back.Weather)
	if !bytes.Equal(a, b) {
		t.Errorf("weather came back as %s, want %s", b, a)
	}
}

// A saved list that is exactly what the first Weather page ticked is widened
// to every warning and every watch when it is read; any other list was chosen
// and is kept.
//
// To see it fail: remove the cfg.widenWeather() call from Load, and the five
// names come back as they were saved.
func TestTheFirstDefaultAlertTypesAreWidened(t *testing.T) {
	shuffled := []string{"tornado watch", "Tornado Warning", " Flash Flood Warning", "Severe Thunderstorm Watch", "Severe Thunderstorm Warning"}
	cases := []struct {
		name  string
		saved []string
		want  []string
	}{
		{"the first defaults", weather.NarrowDefaultEvents, weather.DefaultEvents},
		{"the first defaults in another order and case", shuffled, weather.DefaultEvents},
		{"one removed", weather.NarrowDefaultEvents[:4], weather.NarrowDefaultEvents[:4]},
		{"one added", append(slices.Clone(weather.NarrowDefaultEvents), "Flood Watch"), append(slices.Clone(weather.NarrowDefaultEvents), "Flood Watch")},
		{"classes already", weather.DefaultEvents, weather.DefaultEvents},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := weatherOn()
			c.Weather.Events = slices.Clone(tc.saved)
			raw, err := json.Marshal(c)
			if err != nil {
				t.Fatalf("%v", err)
			}
			back, err := Load(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !slices.Equal(back.Weather.Events, tc.want) {
				t.Errorf("read back %q, want %q", back.Weather.Events, tc.want)
			}
		})
	}
}

// Configuration keeps its own copy of the first defaults so it need not
// import the service; this holds the two together.
func TestTheFirstDefaultsAgreeWithTheService(t *testing.T) {
	want := make([]string, 0, len(weather.NarrowDefaultEvents))
	for _, e := range weather.NarrowDefaultEvents {
		want = append(want, strings.ToLower(e))
	}
	slices.Sort(want)
	if !slices.Equal(narrowWeatherEvents, want) {
		t.Errorf("configuration widens %q, the service names %q", narrowWeatherEvents, want)
	}
}
