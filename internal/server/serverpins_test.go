package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// pinnedLinks is a set of links with fixed statuses.
type pinnedLinks []LinkStatus

func (p pinnedLinks) LinkStatuses() []LinkStatus { return p }

func at(lat, lon float64) (*float64, *float64) { return &lat, &lon }

// The servers /api/peers lists for the map: this one when it has been given a
// position, and the servers it dialled that said where they are.
//
// Break it: list a link whose far end announced no position, list one that is
// not open, or name a pin after the link's name in this server's own
// configuration, and a row fails.
func TestTheServersOnTheMap(t *testing.T) {
	wausauLat, wausauLon := at(44.9591, -89.6301)
	boiseLat, boiseLon := at(43.615, -116.2023)
	self := &ServerPin{Name: "K9MLS-01", Callsign: "K9MLS", Location: "Denton, TX",
		Latitude: 33.2148, Longitude: -97.1331, Self: true}

	tests := []struct {
		name  string
		self  *ServerPin
		links pinnedLinks
		want  []ServerPin
	}{
		{"a server with no position and no links", nil, nil, []ServerPin{}},
		{"this server alone", self, nil, []ServerPin{*self}},
		{"this server and one it dialled", self,
			pinnedLinks{{Name: "to-wisconsin", Network: "KD9EJA-01", Callsign: "KD9EJA", Location: "Wausau, WI",
				Open: true, Latitude: wausauLat, Longitude: wausauLon}},
			[]ServerPin{*self, {Name: "KD9EJA-01", Callsign: "KD9EJA", Location: "Wausau, WI",
				Latitude: 44.9591, Longitude: -89.6301}}},
		{"a far end that said nothing about where it is", self,
			pinnedLinks{{Name: "to-wisconsin", Network: "KD9EJA-01", Open: true}},
			[]ServerPin{*self}},
		{"a link that is not open", self,
			pinnedLinks{{Name: "to-idaho", Network: "AD0MI-01", Open: false, Latitude: boiseLat, Longitude: boiseLon}},
			[]ServerPin{*self}},
		{"a server with no position, linked to one that has", nil,
			pinnedLinks{{Name: "to-idaho", Network: "AD0MI-01", Open: true, Latitude: boiseLat, Longitude: boiseLon}},
			[]ServerPin{{Name: "AD0MI-01", Latitude: 43.615, Longitude: -116.2023}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.NewBus(nil, events.Options{})
			t.Cleanup(bus.Close)
			opts := Options{ListenAddress: "127.0.0.1:0", Map: MapSettings{Self: tc.self}}
			if tc.links != nil {
				opts.Links = tc.links
			}
			srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, opts)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))
			var body struct {
				Servers []ServerPin    `json:"servers"`
				Map     map[string]any `json:"map"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			if body.Servers == nil {
				t.Fatal("servers is null; the page reads its length")
			}
			if len(body.Servers) != len(tc.want) {
				t.Fatalf("listed %+v, want %+v", body.Servers, tc.want)
			}
			for i := range tc.want {
				if body.Servers[i] != tc.want[i] {
					t.Errorf("pin %d is %+v, want %+v", i, body.Servers[i], tc.want[i])
				}
			}
		})
	}
}
