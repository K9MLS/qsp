package routing_test

import (
	"slices"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// ADR-0069: a private call to a radio not heard here is offered to every
// linked QSP server.
//
// The day these exist for: an R7 on a Pi-Star on one server and a radio on a
// Motorola repeater behind a second, linked server. A group call crossed in
// both directions; a private call or a private text stopped at the first
// server, because the only question asked was which of its own peers the
// radio was behind, and the answer was none.
//
// To see these fail, break core.go deliberately and run
// `go test ./internal/routing -run 'Private|LinkedServer'`:
//
//   - delete the sortedQSPLinks loop in the private branch: the dialled-link
//     cases fail, "reached 0 links";
//   - delete the LinkedServerPeers loop: the inbound-server cases fail;
//   - delete the `p == from` skip: TestAPrivateCallIsNotSentBackToTheServerItCameFrom
//     fails, delivering to 7000;
//   - replace `return unlocatedResult(frame, res)` with a fall-through to the
//     judged case: TestABusyLinkDoesNotTakeAPrivateCallFromTheRepeaters fails
//     on NoHomebrewDestination;
//   - remove `frame.CallType != hbp.CallPrivate` from the attachment check:
//     TestAPrivateCallIgnoresTalkgroupAttachment fails.

const (
	hotspot      hbp.RepeaterID = 3132910 // a Pi-Star on this server
	linkedServer hbp.RepeaterID = 7000    // another QSP server that dialled this one
	otherServer  hbp.RepeaterID = 7001
	calledRadio  uint32         = 3132911 // behind the far server's repeater
)

// stubLinkedServers lists the peers that are other QSP servers.
type stubLinkedServers []hbp.RepeaterID

func (s stubLinkedServers) LinkedServerPeers() []hbp.RepeaterID { return slices.Clone(s) }

type privateCoreOptions struct {
	subs    stubSubscribers
	links   []string
	servers stubLinkedServers
	peers   []hbp.RepeaterID
	attach  routing.Subscriptions
}

func privateCore(t *testing.T, o privateCoreOptions) *routing.Core {
	t.Helper()
	opts := routing.CoreOptions{
		Peers:       peersReady(o.peers...),
		Subscribers: o.subs,
		QSPLinks:    o.links,
		Attached:    o.attach,
	}
	if o.servers != nil {
		opts.LinkedServers = o.servers
	}
	c, err := routing.NewCore(opts)
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	return c
}

// privateBursts are the frames a private call or text opens with.
//
// Voice and data take the same path, and the data kinds matter as much: a
// private text is a CSBK preamble, a data header and Rate 3/4 blocks, and
// every one of them has to cross or the far radio assembles nothing.
func privateBursts() map[string]hbp.Data {
	withType := func(dt uint8) hbp.Data {
		f := privateCall(0x5100+hbp.StreamID(dt), calledRadio, hbp.Timeslot2, hbp.FrameTypeSync)
		f.DataType = dt
		return f
	}
	return map[string]hbp.Data{
		"voice header": privateCall(0x5100, calledRadio, hbp.Timeslot2, hbp.FrameTypeSync),
		"voice":        privateCall(0x5101, calledRadio, hbp.Timeslot2, hbp.FrameTypeVoice),
		"csbk":         withType(0x3),
		"data header":  withType(0x6),
		"rate 3/4":     withType(0x8),
	}
}

func deliveredPeers(res routing.Result) []hbp.RepeaterID {
	var out []hbp.RepeaterID
	for _, d := range res.Deliveries {
		out = append(out, d.Peer)
	}
	return out
}

func upstreamNames(res routing.Result) []string {
	var out []string
	for _, u := range res.Upstreams {
		out = append(out, u.Upstream)
	}
	return out
}

func TestAPrivateCallToAnUnknownRadioReachesLinkedServers(t *testing.T) {
	cases := []struct {
		name        string
		opts        privateCoreOptions
		from        hbp.RepeaterID
		wantPeers   []hbp.RepeaterID
		wantLinks   []string
		wantHomebrw bool // NoHomebrewDestination
	}{
		{
			// The test server's side: production is a link it dialled.
			name:      "a dialled link",
			opts:      privateCoreOptions{subs: stubSubscribers{}, links: []string{"production"}, peers: []hbp.RepeaterID{hotspot}},
			from:      hotspot,
			wantLinks: []string{"production"},
		},
		{
			// Production's side: the test server dialled in and is a peer.
			name:      "a server that dialled in",
			opts:      privateCoreOptions{subs: stubSubscribers{}, servers: stubLinkedServers{linkedServer}, peers: []hbp.RepeaterID{hotspot, linkedServer}},
			from:      hotspot,
			wantPeers: []hbp.RepeaterID{linkedServer},
		},
		{
			name: "both kinds at once",
			opts: privateCoreOptions{subs: stubSubscribers{}, links: []string{"alpha", "bravo"},
				servers: stubLinkedServers{linkedServer, otherServer},
				peers:   []hbp.RepeaterID{hotspot, linkedServer, otherServer}},
			from:      hotspot,
			wantPeers: []hbp.RepeaterID{linkedServer, otherServer},
			wantLinks: []string{"alpha", "bravo"},
		},
		{
			// A hotspot is not a server: it must not receive a private call
			// for a radio it has never carried.
			name:      "hotspots are not offered it",
			opts:      privateCoreOptions{subs: stubSubscribers{}, servers: stubLinkedServers{linkedServer}, peers: []hbp.RepeaterID{hotspot, 3132999, linkedServer}},
			from:      hotspot,
			wantPeers: []hbp.RepeaterID{linkedServer},
		},
		{
			// Before ADR-0069, and still, with nobody linked.
			name:        "nothing linked",
			opts:        privateCoreOptions{subs: stubSubscribers{}, peers: []hbp.RepeaterID{hotspot}},
			from:        hotspot,
			wantHomebrw: true,
		},
		{
			// A ready check applies to a linked server like any peer.
			name:        "a linked server that is not ready",
			opts:        privateCoreOptions{subs: stubSubscribers{}, servers: stubLinkedServers{linkedServer}, peers: []hbp.RepeaterID{hotspot}},
			from:        hotspot,
			wantHomebrw: true,
		},
	}
	for _, tc := range cases {
		for kind, frame := range privateBursts() {
			t.Run(tc.name+"/"+kind, func(t *testing.T) {
				res := privateCore(t, tc.opts).Route(tc.from, frame, time.Now())

				if got := deliveredPeers(res); !slices.Equal(got, tc.wantPeers) {
					t.Errorf("delivered to peers %v, want %v (reason %q, drops %+v)", got, tc.wantPeers, res.Reason, res.Drops)
				}
				if got := upstreamNames(res); !slices.Equal(got, tc.wantLinks) {
					t.Errorf("reached links %v, want %v (reason %q)", got, tc.wantLinks, res.Reason)
				}
				for _, d := range res.Deliveries {
					if d.Frame.TargetID != calledRadio || d.Frame.CallType != hbp.CallPrivate || d.Frame.Timeslot != frame.Timeslot {
						t.Errorf("crossed as target %d type %v TS%d, want the called radio, private, TS%d",
							d.Frame.TargetID, d.Frame.CallType, d.Frame.Timeslot, frame.Timeslot)
					}
				}
				for _, u := range res.Upstreams {
					if u.Frame.TargetID != calledRadio || u.Frame.Timeslot != frame.Timeslot {
						t.Errorf("left for %s as target %d TS%d", u.Upstream, u.Frame.TargetID, u.Frame.Timeslot)
					}
				}
				if res.NoHomebrewDestination != tc.wantHomebrw {
					t.Errorf("NoHomebrewDestination = %v, want %v (reason %q)", res.NoHomebrewDestination, tc.wantHomebrw, res.Reason)
				}
				// **The Motorola repeaters here still hear it**: sendToIPSC
				// offers a frame when Reason is empty or nothing judged it.
				if res.Reason != "" && !res.NoHomebrewDestination {
					t.Errorf("the local repeaters would not be offered it: %q", res.Reason)
				}
			})
		}
	}
}

// A radio heard here is delivered here and nowhere else: offering a call to
// every neighbour is for a radio this server cannot place.
func TestALocatedRadioIsNotOfferedToLinkedServers(t *testing.T) {
	c := privateCore(t, privateCoreOptions{
		subs:    stubSubscribers{calledRadio: {peer: hotspot, slot: hbp.Timeslot1}},
		links:   []string{"production"},
		servers: stubLinkedServers{linkedServer},
		peers:   []hbp.RepeaterID{hotspot, 3132999, linkedServer},
	})

	res := c.Route(3132999, privateCall(0x5200, calledRadio, hbp.Timeslot2, hbp.FrameTypeSync), time.Now())

	if got := deliveredPeers(res); !slices.Equal(got, []hbp.RepeaterID{hotspot}) {
		t.Errorf("delivered to %v, want only the hotspot the radio is on", got)
	}
	if len(res.Upstreams) != 0 {
		t.Errorf("a located radio's call also went to links %v", upstreamNames(res))
	}
	if res.Deliveries[0].Frame.Timeslot != hbp.Timeslot1 {
		t.Errorf("delivered on TS%d, want TS1 where the radio was heard", res.Deliveries[0].Frame.Timeslot)
	}
}

// Once the far radio has spoken across the link, this server locates it
// behind the linked server's peer, and the call goes there directly.
func TestARadioLearnedBehindALinkedServerIsReachedThere(t *testing.T) {
	c := privateCore(t, privateCoreOptions{
		subs:    stubSubscribers{calledRadio: {peer: linkedServer, slot: hbp.Timeslot2}},
		servers: stubLinkedServers{linkedServer, otherServer},
		peers:   []hbp.RepeaterID{hotspot, linkedServer, otherServer},
	})

	res := c.Route(hotspot, privateCall(0x5300, calledRadio, hbp.Timeslot2, hbp.FrameTypeSync), time.Now())

	if got := deliveredPeers(res); !slices.Equal(got, []hbp.RepeaterID{linkedServer}) {
		t.Errorf("delivered to %v, want only %d", got, linkedServer)
	}
}

// A call that arrived from a server is never sent back to that server, but
// does go on to the others: a club reaches the network through one
// neighbour, as a group call does.
func TestAPrivateCallIsNotSentBackToTheServerItCameFrom(t *testing.T) {
	c := privateCore(t, privateCoreOptions{
		subs:    stubSubscribers{},
		links:   []string{"production"},
		servers: stubLinkedServers{linkedServer, otherServer},
		peers:   []hbp.RepeaterID{hotspot, linkedServer, otherServer},
	})

	res := c.Route(linkedServer, privateCall(0x5400, calledRadio, hbp.Timeslot2, hbp.FrameTypeSync), time.Now())

	if got := deliveredPeers(res); !slices.Equal(got, []hbp.RepeaterID{otherServer}) {
		t.Errorf("delivered to %v, want only %d", got, otherServer)
	}
	if got := upstreamNames(res); !slices.Equal(got, []string{"production"}) {
		t.Errorf("reached links %v, want production", got)
	}
}

// The test server's side of the live fault, from the link.
func TestAPrivateCallFromALinkIsNotSentBackOverIt(t *testing.T) {
	c := privateCore(t, privateCoreOptions{
		subs:  stubSubscribers{},
		links: []string{"production"},
		peers: []hbp.RepeaterID{hotspot},
	})

	res := c.RouteFromUpstream("production", privateCall(0x5500, calledRadio, hbp.Timeslot2, hbp.FrameTypeSync), time.Now())

	if len(res.Upstreams) != 0 {
		t.Errorf("sent back over the link it arrived on: %v", upstreamNames(res))
	}
	if len(res.Deliveries) != 0 {
		t.Errorf("delivered to hotspots %v that have not heard the radio", deliveredPeers(res))
	}
	// The repeater behind this server is where the radio is, so this is the
	// assertion that matters on the live network.
	if !res.NoHomebrewDestination {
		t.Errorf("the Motorola repeaters would not be offered it: %q", res.Reason)
	}
}

// A link that is already carrying something is a refusal of the link, not a
// verdict on the call, and must not keep it from the repeaters here.
func TestABusyLinkDoesNotTakeAPrivateCallFromTheRepeaters(t *testing.T) {
	c := privateCore(t, privateCoreOptions{
		subs:    stubSubscribers{},
		servers: stubLinkedServers{linkedServer},
		peers:   []hbp.RepeaterID{hotspot, 3132999, linkedServer},
	})
	now := time.Now()

	// Somebody else's private call holds TS2 towards the linked server.
	first := privateCall(0x5600, 3139999, hbp.Timeslot2, hbp.FrameTypeSync)
	first.SourceID = 3130001
	if res := c.Route(3132999, first, now); len(res.Deliveries) != 1 {
		t.Fatalf("setup: the first call was not carried: %q %+v", res.Reason, res.Drops)
	}

	res := c.Route(hotspot, privateCall(0x5601, calledRadio, hbp.Timeslot2, hbp.FrameTypeSync), now.Add(100*time.Millisecond))

	if len(res.Deliveries) != 0 {
		t.Fatalf("delivered onto a held slot: %v", deliveredPeers(res))
	}
	if len(res.Drops) == 0 {
		t.Errorf("the refusal by the busy link is not reported")
	}
	if !res.NoHomebrewDestination {
		t.Errorf("a busy link kept the call from the local repeaters: %q", res.Reason)
	}
}

// Nothing attaches to a radio ID, so a private call cannot be refused for not
// being attached to one.
func TestAPrivateCallIgnoresTalkgroupAttachment(t *testing.T) {
	cases := []struct {
		name string
		subs stubSubscribers
		srv  stubLinkedServers
		want hbp.RepeaterID
	}{
		{"a located hotspot", stubSubscribers{calledRadio: {peer: 3132999, slot: hbp.Timeslot2}}, nil, 3132999},
		{"a radio learned behind a server", stubSubscribers{calledRadio: {peer: linkedServer, slot: hbp.Timeslot2}}, nil, linkedServer},
		{"an unknown radio offered to a server", stubSubscribers{}, stubLinkedServers{linkedServer}, linkedServer},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := privateCore(t, privateCoreOptions{
				subs:    tc.subs,
				servers: tc.srv,
				peers:   []hbp.RepeaterID{hotspot, 3132999, linkedServer},
				attach:  stubAttached{}, // subscription on, nothing attached
			})

			res := c.Route(hotspot, privateCall(0x5700, calledRadio, hbp.Timeslot2, hbp.FrameTypeSync), time.Now())

			if got := deliveredPeers(res); !slices.Equal(got, []hbp.RepeaterID{tc.want}) {
				t.Errorf("delivered to %v, want %d: %q %+v", got, tc.want, res.Reason, res.Drops)
			}
		})
	}

	// And a group call still needs its attachment, which is what the
	// exemption must not widen into.
	c := privateCore(t, privateCoreOptions{peers: []hbp.RepeaterID{hotspot, 3132999}, attach: stubAttached{}})
	if res := c.Route(hotspot, groupCall(0x5710, 2, hbp.Timeslot2, hbp.FrameTypeSync), time.Now()); len(res.Deliveries) != 0 {
		t.Errorf("a group call reached %v with nothing attached", deliveredPeers(res))
	}
}
