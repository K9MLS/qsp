package routing_test

import (
	"slices"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// A weather alert stays on the server that issued it (ADR-0068, as amended).
// A Denton tornado warning is for Denton's stations, not for a server in Iowa
// linked to it, and not for BrandMeister.
//
// The same network either way: two hotspots here, a QSP server that dialled
// in (7000), a QSP link this server dialled ("iowa"), and a bridge from TG 2
// to an outside network ("brandmeister"). An announcement composed on the
// console reaches all of them; a weather alert reaches the two hotspots only.
//
// To see it fail:
//   - drop the upstream/transcoder filter in routeScoped: the alert reaches
//     iowa and brandmeister;
//   - drop the linkedPeers skip: the alert reaches 7000.
func TestAWeatherAlertStaysOnThisServer(t *testing.T) {
	const (
		hotspotA hbp.RepeaterID = 3132910
		hotspotB hbp.RepeaterID = 3155413
		server   hbp.RepeaterID = 7000
	)
	newCore := func(t *testing.T) *routing.Core {
		t.Helper()
		table, err := routing.NewTable([]routing.Bridge{{
			Name: "to-bm", Enabled: true,
			Endpoints: []routing.Endpoint{
				{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
				{Upstream: "brandmeister", Talkgroup: 31672, Timeslot: hbp.Timeslot1},
			},
		}})
		if err != nil {
			t.Fatalf("NewTable: %v", err)
		}
		c, err := routing.NewCore(routing.CoreOptions{
			Table:         table,
			Peers:         peersReady(hotspotA, hotspotB, server),
			QSPLinks:      []string{"iowa"},
			LinkedServers: linkedServers{server},
		})
		if err != nil {
			t.Fatalf("NewCore: %v", err)
		}
		return c
	}
	now := time.Now()

	cases := []struct {
		name      string
		route     func(c *routing.Core, f hbp.Data) routing.Result
		wantPeers []hbp.RepeaterID
		wantLinks []string
	}{
		{
			name:      "an announcement goes everywhere",
			route:     func(c *routing.Core, f hbp.Data) routing.Result { return c.RouteFromServer(f, now) },
			wantPeers: []hbp.RepeaterID{hotspotA, hotspotB, server},
			wantLinks: []string{"brandmeister", "iowa"},
		},
		{
			name:      "a weather alert stays here",
			route:     func(c *routing.Core, f hbp.Data) routing.Result { return c.RouteLocalFromServer(f, now) },
			wantPeers: []hbp.RepeaterID{hotspotA, hotspotB},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A text burst: a data header, which is what a weather alert is
			// made of, on TG 2 TS 2.
			frame := groupCall(0x6600, 2, hbp.Timeslot2, hbp.FrameTypeSync)
			frame.DataType = 0x6
			res := tc.route(newCore(t), frame)

			var peers []hbp.RepeaterID
			for _, d := range res.Deliveries {
				peers = append(peers, d.Peer)
			}
			slices.Sort(peers)
			want := slices.Clone(tc.wantPeers)
			slices.Sort(want)
			if !slices.Equal(peers, want) {
				t.Errorf("delivered to %v, want %v (reason %q)", peers, want, res.Reason)
			}
			var links []string
			for _, u := range res.Upstreams {
				links = append(links, u.Upstream)
			}
			slices.Sort(links)
			if !slices.Equal(links, tc.wantLinks) {
				t.Errorf("sent over links %v, want %v", links, tc.wantLinks)
			}
			// The local Motorola repeaters are offered it by the caller,
			// which does so unless routing refused the frame.
			if res.Reason != "" && !res.NoHomebrewDestination {
				t.Errorf("refused: %q", res.Reason)
			}
		})
	}
}

// A server with only Motorola repeaters still offers the alert to them: no
// hotspot to deliver to is not a refusal.
func TestAWeatherAlertWithNoHotspotsStillReachesTheRepeaters(t *testing.T) {
	c, err := routing.NewCore(routing.CoreOptions{
		Peers:         peersReady(7000),
		QSPLinks:      []string{"iowa"},
		LinkedServers: linkedServers{7000},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	frame := groupCall(0x6700, 2, hbp.Timeslot2, hbp.FrameTypeSync)
	frame.DataType = 0x6
	res := c.RouteLocalFromServer(frame, time.Now())
	if len(res.Deliveries) != 0 || len(res.Upstreams) != 0 {
		t.Fatalf("reached %d peers and %d links", len(res.Deliveries), len(res.Upstreams))
	}
	if res.Reason != "" && !res.NoHomebrewDestination {
		t.Errorf("the repeaters would not be offered it: %q", res.Reason)
	}
}

type linkedServers []hbp.RepeaterID

func (l linkedServers) LinkedServerPeers() []hbp.RepeaterID { return slices.Clone(l) }
