package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/auth"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// fixedPeers is a PeerSource that returns what it was given.
type fixedPeers struct {
	peers          []PeerView
	active, recent []CallView
	traffic        Traffic
}

func (f fixedPeers) PeerViews(time.Time) []PeerView { return f.peers }
func (f fixedPeers) CallViews(time.Time) (active, recent []CallView) {
	return f.active, f.recent
}
func (f fixedPeers) Traffic() Traffic { return f.traffic }

func peersServer(t *testing.T, dmr, ipsc PeerSource) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	a := newStubAuth()
	a.sessions["signed-in"] = auth.Session{
		Token: "signed-in", Username: "operator",
		ExpiresAt: time.Now().Add(time.Hour).UTC(),
	}
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Peers:         dmr,
		IPSCPeers:     ipsc,
		Auth:          a,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func peersBody(t *testing.T, srv *Server) peersResponse {
	t.Helper()
	return peersBodyAs(t, srv, false)
}

// peersBodyAs fetches the peer list as a visitor or as a signed-in operator.
//
// The two differ: an address is an artefact of the connection rather than
// something a station announced, so it is withheld from a public caller.
func peersBodyAs(t *testing.T, srv *Server, signedIn bool) peersResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/peers", nil)
	if signedIn {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: "signed-in"})
	}
	srv.Handler().ServeHTTP(rec, req)
	var body peersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	return body
}

// TestAMotorolaRepeaterReachesThePeerList is the whole point of the second
// source.
//
// The IPSC listener has held peers and calls since it was written and nothing
// read them, so a repeater appeared in /healthz and the journal and nowhere an
// operator looks. That is the "declared and read by nothing" shape §8a names,
// and it had gone unnoticed because the data was correct and merely unreachable.
func TestAMotorolaRepeaterReachesThePeerList(t *testing.T) {
	srv := peersServer(t,
		fixedPeers{peers: []PeerView{{ID: 3155413, Protocol: ProtocolHomebrew, Callsign: "KB9TYC"}}},
		fixedPeers{peers: []PeerView{{ID: 315544, Protocol: ProtocolIPSC, ColorCode: "11"}}},
	)

	body := peersBody(t, srv)
	if len(body.Peers) != 2 {
		t.Fatalf("the list holds %d peers, want both listeners' peers", len(body.Peers))
	}

	byID := map[uint32]PeerView{}
	for _, p := range body.Peers {
		byID[p.ID] = p
	}
	repeater, ok := byID[315544]
	if !ok {
		t.Fatal("the Motorola repeater is missing from the peer list")
	}
	if repeater.Protocol != ProtocolIPSC {
		t.Errorf("the repeater's protocol is %q, want %q", repeater.Protocol, ProtocolIPSC)
	}
	if hotspot := byID[3155413]; hotspot.Protocol != ProtocolHomebrew {
		t.Errorf("the hotspot's protocol is %q, want %q", hotspot.Protocol, ProtocolHomebrew)
	}
}

// TestTheMergedPeerListStaysOrderedByID keeps the documented contract true.
//
// Appending one source after the other would order by protocol and call it an
// ID order, which is the sort of quietly false claim §7 treats as a bug.
func TestTheMergedPeerListStaysOrderedByID(t *testing.T) {
	srv := peersServer(t,
		fixedPeers{peers: []PeerView{{ID: 500, Protocol: ProtocolHomebrew}, {ID: 900, Protocol: ProtocolHomebrew}}},
		fixedPeers{peers: []PeerView{{ID: 100, Protocol: ProtocolIPSC}, {ID: 700, Protocol: ProtocolIPSC}}},
	)

	got := peersBody(t, srv).Peers
	want := []uint32{100, 500, 700, 900}
	if len(got) != len(want) {
		t.Fatalf("the list holds %d peers, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].ID != w {
			ids := make([]uint32, len(got))
			for j, p := range got {
				ids[j] = p.ID
			}
			t.Fatalf("the merged list is ordered %v, want %v", ids, want)
		}
	}
}

// TestRecentCallsFromBothListenersInterleave is the reason CallView carries
// EndedAt.
//
// "Most recent first" across two sources cannot be done with Ago, which is a
// rendered string. Without a real timestamp the list would be two lists end to
// end, each internally correct and the pair misleading about what happened when.
func TestRecentCallsFromBothListenersInterleave(t *testing.T) {
	base := time.Date(2026, 9, 2, 21, 0, 0, 0, time.UTC)
	srv := peersServer(t,
		fixedPeers{recent: []CallView{
			{Source: 1, EndedAt: base.Add(-10 * time.Second)},
			{Source: 3, EndedAt: base.Add(-30 * time.Second)},
		}},
		fixedPeers{recent: []CallView{
			{Source: 2, EndedAt: base.Add(-20 * time.Second)},
			{Source: 4, EndedAt: base.Add(-40 * time.Second)},
		}},
	)

	got := peersBody(t, srv).Recent
	want := []uint32{1, 2, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("%d recent calls, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Source != w {
			order := make([]uint32, len(got))
			for j, c := range got {
				order[j] = c.Source
			}
			t.Fatalf("recent calls are ordered %v, want %v — most recent first", order, want)
		}
	}
}

// TestNoIPSCListenerIsNotAnEmptyOne checks that an instance running only
// Homebrew is unchanged.
//
// A nil second source must add nothing at all, rather than an empty section
// that would make the console draw a heading for repeaters that cannot connect.
func TestNoIPSCListenerIsNotAnEmptyOne(t *testing.T) {
	srv := peersServer(t,
		fixedPeers{peers: []PeerView{{ID: 3155413, Protocol: ProtocolHomebrew}}},
		nil,
	)
	body := peersBody(t, srv)
	if len(body.Peers) != 1 {
		t.Fatalf("the list holds %d peers, want only the Homebrew one", len(body.Peers))
	}
	if body.Peers[0].Protocol != ProtocolHomebrew {
		t.Errorf("protocol is %q, want %q", body.Peers[0].Protocol, ProtocolHomebrew)
	}
}

// TestTheMotorolaListenersFramesAreReportedSeparately covers a panel that was
// telling the operator something false.
//
// The Traffic panel read its voice frame count from the DMR listener alone, so
// a network whose only traffic was Motorola repeaters showed zero — and the
// console's hint then advised checking a hotspot that had nothing to do with
// it. A confidently wrong hint is worse than none: an operator who learns to
// disbelieve one warning stops reading all of them.
func TestTheMotorolaListenersFramesAreReportedSeparately(t *testing.T) {
	srv := peersServer(t,
		fixedPeers{traffic: Traffic{FramesAccepted: 0, DatagramsIn: 400}},
		fixedPeers{traffic: Traffic{IPSC: &IPSCTraffic{VoiceFrames: 288, Ignored: 3}}},
	)

	got := peersBody(t, srv).Traffic
	if got.IPSC == nil {
		t.Fatal("the Motorola listener's figures are missing from the payload")
	}
	if got.IPSC.VoiceFrames != 288 {
		t.Errorf("IPSC voice frames %d, want 288", got.IPSC.VoiceFrames)
	}
	if got.IPSC.Ignored != 3 {
		t.Errorf("IPSC ignored %d, want 3", got.IPSC.Ignored)
	}
	// The DMR listener's own figures must be untouched: they are documented
	// counters for one socket and summing two into them would change what an
	// existing number means without saying so.
	if got.FramesAccepted != 0 {
		t.Errorf("the DMR frame count became %d; it should still be 0", got.FramesAccepted)
	}
	if got.DatagramsIn != 400 {
		t.Errorf("the DMR datagram count became %d, want 400", got.DatagramsIn)
	}
}

// TestWithNoIPSCListenerTheTrafficPayloadIsUnchanged keeps a Homebrew-only
// instance exactly as it was, so the console draws nothing new for an operator
// who runs no repeaters.
func TestWithNoIPSCListenerTheTrafficPayloadIsUnchanged(t *testing.T) {
	srv := peersServer(t, fixedPeers{traffic: Traffic{FramesAccepted: 12}}, nil)
	got := peersBody(t, srv).Traffic
	if got.IPSC != nil {
		t.Error("an instance with no IPSC listener reported IPSC traffic")
	}
	if got.FramesAccepted != 12 {
		t.Errorf("frames accepted %d, want 12", got.FramesAccepted)
	}
}

// TestALookedUpCallsignIsMarkedAsOne keeps a guess distinguishable from a
// statement.
//
// A Homebrew peer states its callsign at login. An IPSC repeater states
// nothing, so anything shown for one is QSP matching a radio ID against a
// public registry — which can be stale, or can describe the operator rather
// than the repeater. Rendering the two identically would be the shape of fake
// data §7 forbids: a value that looks like it came from the station.
func TestALookedUpCallsignIsMarkedAsOne(t *testing.T) {
	srv := peersServer(t,
		fixedPeers{peers: []PeerView{
			{ID: 3155413, Protocol: ProtocolHomebrew, Callsign: "KB9TYC"},
		}},
		fixedPeers{peers: []PeerView{
			{ID: 315544, Protocol: ProtocolIPSC, Callsign: "KD9EJA", CallsignSource: CallsignFromRegistry},
			{ID: 999999, Protocol: ProtocolIPSC},
		}},
	)

	byID := map[uint32]PeerView{}
	for _, p := range peersBody(t, srv).Peers {
		byID[p.ID] = p
	}

	if got := byID[3155413]; got.CallsignSource == CallsignFromRegistry {
		t.Error("a Homebrew peer's announced callsign is marked as looked up")
	}
	if got := byID[315544]; got.CallsignSource != CallsignFromRegistry {
		t.Error("a repeater's registry callsign is not marked as looked up; " +
			"the console would present a guess as a statement")
	}
	if got := byID[999999]; got.Callsign != "" || got.CallsignSource != "" {
		t.Errorf("a repeater with no registry record shows %q; it should show "+
			"nothing and let the console say why", got.Callsign)
	}
}

// TestAnAddressIsNotPublished is the one field on this endpoint that a peer
// never announced.
//
// /api/peers is deliberately unauthenticated, and the reasoning recorded for
// that covers what a station chose to make public: its callsign, its location,
// its talkgroups. **An address is none of those.** It is an artefact of the
// connection, observed by this server, and it is a member's home internet
// connection together with the fact that they are on the air right now.
//
// A callsign already leads to a name through the licence database, so that is
// not the exposure. What an address adds is precise and actionable: where to
// aim traffic to put one member off the air during a net.
func TestAnAddressIsNotPublished(t *testing.T) {
	srv := peersServer(t,
		fixedPeers{peers: []PeerView{
			{ID: 3155413, Protocol: ProtocolHomebrew, Callsign: "KB9TYC",
				Address: "198.51.100.172:45383"},
		}},
		fixedPeers{peers: []PeerView{
			{ID: 315544, Protocol: ProtocolIPSC, Address: "198.51.100.2:50004"},
		}},
	)

	for _, p := range peersBody(t, srv).Peers {
		if p.Address != "" {
			t.Errorf("peer %d published address %q to a caller who is not signed in",
				p.ID, p.Address)
		}
	}

	// The complementary half. Withholding it from everybody would take a
	// diagnostic away from the one person entitled to it, and an operator
	// chasing a peer that will not connect is signed in already.
	body := peersBodyAs(t, srv, true)
	var withAddress int
	for _, p := range body.Peers {
		if p.Address != "" {
			withAddress++
		}
	}
	if withAddress != 2 {
		t.Errorf("%d of 2 peers showed an address to a signed-in operator", withAddress)
	}
}
