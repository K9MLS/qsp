package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

func newForwardingServer(t *testing.T, src PeerSource, forwarding bool) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Peers:         src,
		Forwarding:    forwarding,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func forwardingFrom(t *testing.T, srv *Server) (value bool, present bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	raw, ok := body["forwarding"]
	if !ok {
		return false, false
	}
	v, ok := raw.(bool)
	if !ok {
		t.Fatalf("forwarding is %T, want bool", raw)
	}
	return v, true
}

// TestPeersReportsForwarding is the fix for a console that told an operator the
// opposite of the truth. The notice was static markup reading "Forwarding is
// off", and an instance that had been repeating for eleven hours still showed
// it. The console can only stop saying that if the API tells it.
func TestPeersReportsForwarding(t *testing.T) {
	for _, forwarding := range []bool{true, false} {
		srv := newForwardingServer(t, stubPeers{}, forwarding)
		got, present := forwardingFrom(t, srv)
		if !present {
			t.Fatal("the response omits forwarding, so the console cannot tell")
		}
		if got != forwarding {
			t.Errorf("forwarding = %v, want %v", got, forwarding)
		}
	}
}

// TestForwardingIsNotOmittedWhenFalse matters because the console reads it as a
// plain boolean. An omitted field and a false one are the same to that code,
// but only one of them survives a future change to the notice's default.
func TestForwardingIsNotOmittedWhenFalse(t *testing.T) {
	srv := newForwardingServer(t, stubPeers{}, false)
	if _, present := forwardingFrom(t, srv); !present {
		t.Error("forwarding is omitted when false; it must always be present")
	}
}

// TestDisabledListenerIsNotForwarding keeps the two flags consistent. A
// listener that is not running cannot be relaying, and reporting otherwise
// would put the console back to describing something that is not happening.
func TestDisabledListenerIsNotForwarding(t *testing.T) {
	srv := newForwardingServer(t, nil, true)
	got, _ := forwardingFrom(t, srv)
	if got {
		t.Error("a disabled listener reported that it is forwarding")
	}
}

// TestPeerViewCarriesPositionOptionally covers the pointer fields. A station on
// the equator must be distinguishable from one that announced nothing, or a map
// either loses it or draws it in the Gulf of Guinea.
func TestPeerViewCarriesPositionOptionally(t *testing.T) {
	equator := 0.0
	meridianish := -97.1331

	views := []PeerView{
		{ID: 1, Callsign: "NOPOS"},
		{ID: 2, Callsign: "NAMED", Location: "Denton, TX"},
		{ID: 3, Callsign: "PINNED", Location: "Equator", Latitude: &equator, Longitude: &meridianish},
	}
	srv := newForwardingServer(t, stubPeers{views: views}, true)

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))

	var body struct {
		Peers []map[string]any `json:"peers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Peers) != 3 {
		t.Fatalf("got %d peers, want 3", len(body.Peers))
	}

	if _, ok := body.Peers[0]["location"]; ok {
		t.Error("a peer with no position carried a location field")
	}
	if _, ok := body.Peers[0]["latitude"]; ok {
		t.Error("a peer with no position carried a latitude field")
	}

	if body.Peers[1]["location"] != "Denton, TX" {
		t.Errorf("the place name did not survive: %v", body.Peers[1]["location"])
	}
	if _, ok := body.Peers[1]["latitude"]; ok {
		t.Error("a peer with a name but no coordinates carried a latitude")
	}

	// The one that matters: latitude 0 must be present, not omitted.
	lat, ok := body.Peers[2]["latitude"]
	if !ok {
		t.Fatal("a station on the equator lost its latitude to omitempty")
	}
	if lat.(float64) != 0 {
		t.Errorf("latitude is %v, want 0", lat)
	}
}
