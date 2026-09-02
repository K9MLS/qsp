package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// fixedPeers is a PeerSource that returns what it was given.
type fixedPeers struct {
	peers          []PeerView
	active, recent []CallView
}

func (f fixedPeers) PeerViews(time.Time) []PeerView { return f.peers }
func (f fixedPeers) CallViews(time.Time) (active, recent []CallView) {
	return f.active, f.recent
}
func (f fixedPeers) Traffic() Traffic { return Traffic{} }

func peersServer(t *testing.T, dmr, ipsc PeerSource) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress: "127.0.0.1:0",
		Peers:         dmr,
		IPSCPeers:     ipsc,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func peersBody(t *testing.T, srv *Server) peersResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))
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
