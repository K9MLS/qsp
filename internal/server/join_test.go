package server

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/console"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// joinSettings is a club network as an admin would configure one.
func joinSettings() JoinSettings {
	return JoinSettings{
		NetworkName: "Denton County ARA",
		Address:     "192.168.1.135",
		Port:        62031,
		Talkgroups: []JoinTalkgroup{
			{Name: "Club chat", Dialled: 11, Arrives: 9, Timeslot: 2},
			{Name: "Nets", Dialled: 12, Arrives: 91, Timeslot: 2},
		},
	}
}

func getJoin(t *testing.T, srv *Server, remoteAddr string) map[string]any {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/join", nil)
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/join returned %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %v", err)
	}
	return body
}

// TestJoinNeverReturnsTheSharedPassword is the assertion that lets this
// endpoint be shown to a club's members at all.
//
// Everything else /api/join reports is safe for anyone who can already reach
// the console. The shared peer secret is not, and the whole design rests on it
// never appearing here — so the test looks for it in the raw bytes rather than
// trusting the struct to have no field for it.
func TestJoinNeverReturnsTheSharedPassword(t *testing.T) {
	const secret = "hunter2-club-secret"

	srv := newJoinServer(t, Options{
		ListenAddress: "127.0.0.1:0",
		Join:          joinSettings(),
		Peers:         stubPeers{},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/join", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), secret) {
		t.Fatal("the shared peer password appeared in /api/join")
	}
	for _, forbidden := range []string{"password", "secret", "passphrase"} {
		if strings.Contains(strings.ToLower(rec.Body.String()), forbidden) {
			t.Errorf("/api/join mentions %q; this endpoint is shown to members and must carry no credential", forbidden)
		}
	}
}

// TestJoinShowsBothTalkgroupNumbers guards the mistake that cost two sessions.
//
// Under WPSD's automatic rewrite rules the number a member dials is not the
// number that arrives. A page showing only one of them sends people to a
// talkgroup that goes nowhere, and they conclude the software is broken.
func TestJoinShowsBothTalkgroupNumbers(t *testing.T) {
	srv := newJoinServer(t, Options{
		ListenAddress: "127.0.0.1:0",
		Join:          joinSettings(),
		Peers:         stubPeers{},
	})

	body := getJoin(t, srv, "")
	settings, ok := body["settings"].(map[string]any)
	if !ok {
		t.Fatal("no settings in the response")
	}
	tgs, ok := settings["talkgroups"].([]any)
	if !ok || len(tgs) != 2 {
		t.Fatalf("got %v talkgroups, want 2", len(tgs))
	}

	first, _ := tgs[0].(map[string]any)
	if first["dialled"] != float64(11) {
		t.Errorf("dialled = %v, want 11", first["dialled"])
	}
	if first["arrives"] != float64(9) {
		t.Errorf("arrives = %v, want 9 — the rewrite must be visible", first["arrives"])
	}
	if first["timeslot"] != float64(2) {
		t.Errorf("timeslot = %v, want 2", first["timeslot"])
	}
}

// TestJoinSaysSoWhenTheListenerIsOff.
//
// A member following instructions against a disabled listener sees nothing
// happen and has no way to learn why. Constitution §3: the absence says so.
func TestJoinSaysSoWhenTheListenerIsOff(t *testing.T) {
	srv := newJoinServer(t, Options{
		ListenAddress:       "127.0.0.1:0",
		Join:                joinSettings(),
		PeersDisabledReason: "the DMR listener is disabled; set dmr.enabled to accept peers",
	})

	body := getJoin(t, srv, "")
	if body["enabled"] != false {
		t.Error("enabled is true with no peer source")
	}
	reason, _ := body["reason"].(string)
	if !strings.Contains(reason, "dmr.enabled") {
		t.Errorf("reason %q does not name the setting that would fix it", reason)
	}
}

// TestJoinIdentifiesTheCallersHotspot is what makes this a page rather than a
// PDF: the machine confirms it worked.
func TestJoinIdentifiesTheCallersHotspot(t *testing.T) {
	srv := newJoinServer(t, Options{
		ListenAddress: "127.0.0.1:0",
		Join:          joinSettings(),
		Peers: stubPeers{views: []PeerView{
			{ID: 3132910, Callsign: "K9MLS", Address: "192.168.1.155:45582", Ready: true},
			{ID: 3121380, Callsign: "W5ABC", Address: "192.168.1.200:51000", Ready: true},
		}},
	})

	body := getJoin(t, srv, "192.168.1.200:60000")
	you, ok := body["you"].(map[string]any)
	if !ok {
		t.Fatal("the caller's own hotspot was not identified")
	}
	if you["callsign"] != "W5ABC" {
		t.Errorf("identified %v, want W5ABC — matched on the wrong address", you["callsign"])
	}
	if body["connected"] != float64(2) {
		t.Errorf("connected = %v, want 2", body["connected"])
	}
}

// TestJoinOmitsYouWhenNoHotspotMatches.
//
// Claiming a member's hotspot is connected when it is not would be worse than
// saying nothing: they would stop troubleshooting.
func TestJoinOmitsYouWhenNoHotspotMatches(t *testing.T) {
	srv := newJoinServer(t, Options{
		ListenAddress: "127.0.0.1:0",
		Join:          joinSettings(),
		Peers: stubPeers{views: []PeerView{
			{ID: 3132910, Callsign: "K9MLS", Address: "192.168.1.155:45582", Ready: true},
		}},
	})

	body := getJoin(t, srv, "10.9.9.9:41000")
	if _, present := body["you"]; present {
		t.Error("a hotspot was claimed for a caller whose address matches none")
	}
	if body["connected"] != float64(1) {
		t.Errorf("connected = %v, want 1", body["connected"])
	}
}

// newJoinServer builds a server with the dependencies New requires.
func newJoinServer(t *testing.T, opts Options) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{HistorySize: 8, SubscriberBuffer: 4})
	t.Cleanup(bus.Close)
	if opts.ShutdownTimeout == 0 {
		opts.ShutdownTimeout = time.Second
	}
	srv, err := New(nil, stubRegistry{report: health.Report{}}, bus, opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

// TestJoinPathRedirects: /join is the URL an admin sends to fifty members.
// Making them type join.html would be a small tax collected fifty times.
func TestJoinPathRedirects(t *testing.T) {
	assets, err := console.Assets()
	if err != nil {
		t.Fatalf("console.Assets: %v", err)
	}
	srv := newJoinServer(t, Options{
		ListenAddress: "127.0.0.1:0",
		ConsoleAssets: assets,
		Join:          joinSettings(),
	})

	req := httptest.NewRequest(http.MethodGet, "/join", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("GET /join returned %d, want 302", rec.Code)
	}
	if loc := rec.Header().Get("Location"); loc != "/join.html" {
		t.Errorf("Location = %q, want /join.html", loc)
	}
}

// TestJoinPageIsEmbedded guards against the page existing in the tree but not
// in the binary, which is the failure mode of //go:embed.
func TestJoinPageIsEmbedded(t *testing.T) {
	assets, err := console.Assets()
	if err != nil {
		t.Fatalf("console.Assets: %v", err)
	}
	for _, name := range []string{"join.html", "join.css", "join.js"} {
		if _, err := fs.Stat(assets, name); err != nil {
			t.Errorf("%s is not embedded in the console assets: %v", name, err)
		}
	}
}
