package routing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// TestAKeyOverNamesWhoHeldTheDestinationAndWhoWasRefused.
//
// On 2026-10-27 COLLISIONS read 748, amber, and the log said "timeslot already
// carrying TG 2 from peer 0". There is no peer 0: it was Zello, whose audio
// comes out of a transcoder and has no peer, and the reason was built from the
// empty field. Each origin is named here as an operator knows it, in the
// reason and in the fields the console reads.
//
// To see it fail:
//   - put `held.source.peer` back in the timeslot reason, with "peer %d": the
//     transcoder, link and server cases say "peer 0" or "peer 4294967295";
//   - drop the transcoder case from sourceKey.String: the Zello cases name
//     "peer 0";
//   - delete `src.transcoder = origin.Transcoder`: the same, by the other road;
//   - make keyOver leave Contention nil: every case fails as "not a key-over".
func TestAKeyOverNamesWhoHeldTheDestinationAndWhoWasRefused(t *testing.T) {
	const tg = 2
	bridge := routing.Bridge{
		Name: "net", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, tg, hbp.Timeslot2),
			ep(peerB, tg, hbp.Timeslot2),
			ep(peerC, tg, hbp.Timeslot2),
			{Transcoder: "zello", Talkgroup: tg, Timeslot: hbp.Timeslot2},
			{Upstream: "BCARA", Talkgroup: tg, Timeslot: hbp.Timeslot2},
		},
	}
	frame := func(stream hbp.StreamID) hbp.Data {
		return voiceFrame(stream, tg, hbp.Timeslot2, hbp.FrameTypeSync)
	}
	fromPeer := func(id hbp.RepeaterID) func(*routing.Core, hbp.Data, time.Time) routing.Result {
		return func(c *routing.Core, f hbp.Data, at time.Time) routing.Result { return c.Route(id, f, at) }
	}
	fromZello := func(c *routing.Core, f hbp.Data, at time.Time) routing.Result {
		return c.RouteFromTranscoder("zello", f, at)
	}
	fromLink := func(c *routing.Core, f hbp.Data, at time.Time) routing.Result {
		return c.RouteFromUpstream("BCARA", f, at)
	}
	fromServer := func(c *routing.Core, f hbp.Data, at time.Time) routing.Result {
		return c.RouteFromServer(f, at)
	}

	for _, tc := range []struct {
		name    string
		first   func(*routing.Core, hbp.Data, time.Time) routing.Result
		second  func(*routing.Core, hbp.Data, time.Time) routing.Result
		holder  string
		refused string
	}{
		{"a hotspot keys over Zello, which is 2026-10-27", fromZello, fromPeer(peerB),
			`transcoder "zello"`, "peer 3121380"},
		{"Zello keys over a hotspot", fromPeer(peerA), fromZello,
			"peer 3132910", `transcoder "zello"`},
		{"a hotspot keys over a link", fromLink, fromPeer(peerB),
			"link BCARA", "peer 3121380"},
		{"a hotspot keys over this server", fromServer, fromPeer(peerB),
			"this server", "peer 3121380"},
		{"a hotspot keys over a hotspot", fromPeer(peerA), fromPeer(peerB),
			"peer 3132910", "peer 3121380"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tab, err := routing.NewTable([]routing.Bridge{bridge}, routing.WithPermissions(
				map[string]routing.Permission{"zello": {All: true}}))
			if err != nil {
				t.Fatalf("NewTable: %v", err)
			}
			c := newCore(t, tab, peerA, peerB, peerC)
			if res := tc.first(c, frame(0x1111), t0); len(res.Deliveries) == 0 {
				t.Fatalf("the first transmission reached no peer: %q %+v", res.Reason, res.Drops)
			}
			res := tc.second(c, frame(0x2222), t0.Add(100*time.Millisecond))

			var keyOvers []routing.Drop
			for _, d := range res.Drops {
				if strings.Contains(d.Reason, "peer 0") || strings.Contains(d.Reason, "peer 4294967295") {
					t.Errorf("the reason names a peer that does not exist: %q", d.Reason)
				}
				if d.Contention != nil {
					keyOvers = append(keyOvers, d)
				}
			}
			if len(keyOvers) == 0 {
				t.Fatalf("not a key-over: no drop says who held the destination: %+v", res.Drops)
			}
			for _, d := range keyOvers {
				if d.Contention.Holder != tc.holder {
					t.Errorf("to %s: held by %q, want %q", d.To, d.Contention.Holder, tc.holder)
				}
				if d.Contention.Refused != tc.refused {
					t.Errorf("to %s: refused %q, want %q", d.To, d.Contention.Refused, tc.refused)
				}
				if d.Contention.Talkgroup != tg {
					t.Errorf("to %s: held TG %d, want %d", d.To, d.Contention.Talkgroup, tg)
				}
				if !strings.Contains(d.Reason, tc.holder) {
					t.Errorf("to %s: the reason does not name %s: %q", d.To, tc.holder, d.Reason)
				}
			}
		})
	}
}

// TestARefusalThatIsNotAKeyOverSaysNothingAboutContention: the fields are a
// claim that somebody held the destination, so a policy refusal leaves them
// empty and the console does not call it a key-over.
//
// To see it fail: build the "has not opted in to transcoded audio" drop in
// core.go with keyOver, and the first case fails; the second holds that a
// transmission is never a key-over of itself.
func TestARefusalThatIsNotAKeyOverSaysNothingAboutContention(t *testing.T) {
	bridge := routing.Bridge{Name: "net", Enabled: true, Endpoints: []routing.Endpoint{
		ep(peerA, 2, hbp.Timeslot2),
		ep(peerB, 2, hbp.Timeslot2),
		{Transcoder: "zello", Talkgroup: 2, Timeslot: hbp.Timeslot2},
	}}
	for _, tc := range []struct {
		name  string
		route func(*routing.Core) routing.Result
		drops bool
	}{
		{"Zello to a hotspot that has not opted in", func(c *routing.Core) routing.Result {
			return c.RouteFromTranscoder("zello", voiceFrame(0x1111, 2, hbp.Timeslot2, hbp.FrameTypeSync), t0)
		}, true},
		{"a transmission's own next frame", func(c *routing.Core) routing.Result {
			_ = c.Route(peerA, voiceFrame(0x1111, 2, hbp.Timeslot2, hbp.FrameTypeSync), t0)
			return c.Route(peerA, voiceFrame(0x1111, 2, hbp.Timeslot2, hbp.FrameTypeVoice), t0.Add(60*time.Millisecond))
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := tc.route(newCore(t, mustTable(t, bridge), peerA, peerB))
			if tc.drops && len(res.Drops) == 0 {
				t.Fatalf("nothing was refused, so this says nothing: %q", res.Reason)
			}
			for _, d := range res.Drops {
				if d.Contention != nil {
					t.Errorf("reported as a key-over: %s, %q", d.To, d.Reason)
				}
			}
		})
	}
}
