package peers_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// The sequence from production at 12:47:57 on 2026-10-03, and what must and
// must not be treated like it.
//
// A hotspot carrying radio 3132910 on TG 2 lost it for an instant and picked
// it up again as a new stream with no header — and with the radio's Talker
// Alias read as its IDs, so the rest of the over arrived as radio 5002016
// calling talkgroup 4929869. Every frame of it was refused as a collision or
// sent to a talkgroup nobody has. Each case is what follows the first call's
// frames, and what the master hands to routing for it.
//
// To see it fail: in Peer.continuing, make the `join && !header` case never
// match, and the first two cases come through under the IDs they arrived
// with; or keep calls that have ended in the open list (drop `c.open &&`),
// and a call after a terminator is joined to the one before it.
func TestACallThatRestartsWithoutAHeaderIsTheSameCall(t *testing.T) {
	const (
		caller   uint32       = 3132910
		original hbp.StreamID = 0x18CDEE
		restart  hbp.StreamID = 0xE65320
	)
	frame := func(stream hbp.StreamID, source, target uint32, ft hbp.FrameType, dt uint8) hbp.Data {
		return hbp.Data{
			RepeaterID: testID, SourceID: source, TargetID: target, StreamID: stream,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup, FrameType: ft, DataType: dt,
		}
	}
	type want struct {
		source, target uint32
		stream         hbp.StreamID
	}
	same := want{caller, 2, original}

	cases := []struct {
		name string
		// gap is how long after the first call's last frame the next arrives.
		gap time.Duration
		// endFirst sends the first call's terminator before the gap.
		endFirst bool
		next     hbp.Data
		want     want
	}{
		{"picked up mid-call with the alias read as IDs", 60 * time.Millisecond, false,
			frame(restart, 5002016, 4929869, hbp.FrameTypeVoice, 0), same},
		{"picked up mid-call with the right IDs and a new stream", 60 * time.Millisecond, false,
			frame(restart, caller, 2, hbp.FrameTypeVoiceSync, 0), same},
		{"a new call, with its header", 60 * time.Millisecond, false,
			frame(restart, 3121001, 9, hbp.FrameTypeSync, hbp.DataTypeVoiceLCHeader), want{3121001, 9, restart}},
		{"after the first call ended properly", 60 * time.Millisecond, true,
			frame(restart, 3121001, 9, hbp.FrameTypeVoice, 0), want{3121001, 9, restart}},
		{"three seconds later", 3 * time.Second, false,
			frame(restart, 3121001, 9, hbp.FrameTypeVoice, 0), want{3121001, 9, restart}},
		{"on the other timeslot", 60 * time.Millisecond, false,
			func() hbp.Data {
				f := frame(restart, 3121001, 9, hbp.FrameTypeVoice, 0)
				f.Timeslot = hbp.Timeslot1
				return f
			}(), want{3121001, 9, restart}},
		{"a text burst, which is its own stream by design", 60 * time.Millisecond, false,
			frame(restart, 3121001, 9, hbp.FrameTypeSync, 0x7), want{3121001, 9, restart}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.login(addrA)

			h.send(frame(original, caller, 2, hbp.FrameTypeSync, hbp.DataTypeVoiceLCHeader), addrA)
			for range 16 {
				h.c.advance(60 * time.Millisecond)
				if out := h.send(frame(original, caller, 2, hbp.FrameTypeVoice, 0), addrA); out.Data == nil {
					t.Fatalf("the first call was refused: %s", out.Dropped)
				}
			}
			if tc.endFirst {
				h.send(frame(original, caller, 2, hbp.FrameTypeSync, hbp.DataTypeTerminator), addrA)
			}

			h.c.advance(tc.gap)
			check := func(what string, f hbp.Data) {
				t.Helper()
				out := h.send(f, addrA)
				if out.Data == nil {
					t.Fatalf("%s was refused: %s", what, out.Dropped)
				}
				got := want{out.Data.SourceID, out.Data.TargetID, out.Data.StreamID}
				if got != tc.want {
					t.Errorf("%s is carried as %+v, want %+v", what, got, tc.want)
				}
			}
			check("the frame", tc.next)

			// The rest of the restarted stream goes the same way, and its
			// terminator ends the call it was put back into.
			h.c.advance(60 * time.Millisecond)
			check("the next frame of that stream", tc.next)
			end := tc.next
			if !end.IsUserData() {
				end.FrameType, end.DataType = hbp.FrameTypeSync, hbp.DataTypeTerminator
				h.c.advance(60 * time.Millisecond)
				check("its terminator", end)
			}
			if tc.want == same {
				// The terminator ended the call that was rejoined, so what
				// follows, header or not, is a call of its own.
				h.c.advance(60 * time.Millisecond)
				after := frame(0xABCDEF, 3121002, 91, hbp.FrameTypeVoice, 0)
				after.Timeslot = tc.next.Timeslot
				out := h.send(after, addrA)
				if out.Data == nil || out.Data.SourceID != 3121002 || out.Data.TargetID != 91 {
					t.Errorf("a call after the terminator was not carried as itself: %+v", out.Data)
				}
			}
		})
	}
}

// Joining a call must not be a way round a ban, in either direction: a banned
// radio's frames never open a call for somebody else's to join, and a banned
// radio's frames are never carried under the call before them.
//
// To see it fail: move the Subscriber.Allows check in handleData to after
// p.continuing, and the banned radio is carried as the permitted one.
func TestABannedRadioCannotRideOnACall(t *testing.T) {
	ban, err := access.Parse("dmr.access.subscribers", access.Subscriber, access.ModeDeny, []string{"3121077"})
	if err != nil {
		t.Fatalf("access.Parse: %v", err)
	}
	h := newHarness(t, func(c *peers.MasterConfig) { c.Access = access.Lists{Subscriber: ban} })
	h.login(addrA)

	// A permitted radio's call, left without a terminator.
	if out := h.send(voice(3121001, 0x1111, 0), addrA); out.Data == nil {
		t.Fatalf("a permitted radio was refused: %s", out.Dropped)
	}
	h.c.advance(60 * time.Millisecond)
	if out := h.send(voice(3121077, 0x2222, 0), addrA); out.Data != nil {
		t.Fatalf("a banned radio was carried as radio %d", out.Data.SourceID)
	}

	// And the other way: the banned radio's frames first.
	g := newHarness(t, func(c *peers.MasterConfig) { c.Access = access.Lists{Subscriber: ban} })
	g.login(addrA)
	g.send(voice(3121077, 0x3333, 0), addrA)
	g.c.advance(60 * time.Millisecond)
	out := g.send(voice(3121001, 0x4444, 0), addrA)
	if out.Data == nil || out.Data.SourceID != 3121001 || out.Data.StreamID != 0x4444 {
		t.Errorf("a permitted radio after a banned one was carried as %+v (%s)", out.Data, out.Dropped)
	}
}

// A hotspot's Talker Alias datagrams are ordinary traffic, not something to
// count as ignored. Each case is who sent one and whether it is accepted.
//
// To see it fail: remove the hbp.TalkerAlias case from Master.Handle, and the
// registered hotspot's own alias is dropped as "not a message a master acts on".
func TestATalkerAliasFromAHotspotIsNotIgnored(t *testing.T) {
	// As DMRGateway sends it: tag, repeater ID, radio ID, block, and the
	// first three octets of text.
	alias := hbp.TalkerAlias{RepeaterID: testID, Rest: []byte{0x2f, 0xcd, 0xee, 0x00, 'K', '9', 'M'}}

	h := newHarness(t)
	if out := h.send(alias, addrA); out.Dropped == "" {
		t.Error("an alias from a peer that never logged in was accepted")
	}
	h.login(addrA)
	h.c.advance(50 * time.Second)
	if out := h.send(alias, addrA); out.Dropped != "" {
		t.Errorf("the hotspot's own alias was dropped: %s", out.Dropped)
	}
	if out := h.send(alias, addrB); out.Dropped == "" {
		t.Error("an alias for this peer from another address was accepted")
	}
	// It counts as hearing from the hotspot.
	h.c.advance(50 * time.Second)
	h.m.Expire()
	if _, ok := h.m.Lookup(testID); !ok {
		t.Error("a hotspot heard from 50 seconds ago was timed out")
	}
}

// A peer carrying several calls at once interleaves their frames, and one
// call's frame between another's is not that other call restarting. Nor is
// anything a linked QSP server sends ever rejoined: the far server already
// sorted it out.
//
// To see it fail: drop `len(open) == 1` from the join case in continuing (the
// third call's audio is carried as the first's), or pass true for join
// whatever the peer is (the linked server's second call is).
func TestCallsSentAtOnceAreNotJoined(t *testing.T) {
	v := func(stream hbp.StreamID, source, tg uint32, ft hbp.FrameType, dt uint8) hbp.Data {
		return hbp.Data{RepeaterID: testID, SourceID: source, TargetID: tg, StreamID: stream,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup, FrameType: ft, DataType: dt}
	}
	carriedAs := func(t *testing.T, h *harness, f hbp.Data) uint32 {
		t.Helper()
		out := h.send(f, addrA)
		if out.Data == nil {
			t.Fatalf("refused: %s", out.Dropped)
		}
		return out.Data.TargetID
	}

	t.Run("two calls interleaved, then a third without its header", func(t *testing.T) {
		h := newHarness(t)
		h.login(addrA)
		carriedAs(t, h, v(0x1, 3121001, 2, hbp.FrameTypeSync, hbp.DataTypeVoiceLCHeader))
		carriedAs(t, h, v(0x2, 3121002, 9, hbp.FrameTypeSync, hbp.DataTypeVoiceLCHeader))
		for range 5 {
			h.c.advance(30 * time.Millisecond)
			if tg := carriedAs(t, h, v(0x1, 3121001, 2, hbp.FrameTypeVoice, 0)); tg != 2 {
				t.Fatalf("the first call's frame was carried on TG %d", tg)
			}
			h.c.advance(30 * time.Millisecond)
			if tg := carriedAs(t, h, v(0x2, 3121002, 9, hbp.FrameTypeVoice, 0)); tg != 9 {
				t.Fatalf("the second call's frame was carried on TG %d", tg)
			}
		}
		if tg := carriedAs(t, h, v(0x3, 3121003, 91, hbp.FrameTypeVoice, 0)); tg != 91 {
			t.Errorf("with two calls open, a third without a header was carried on TG %d, want its own 91", tg)
		}
	})

	t.Run("a linked QSP server", func(t *testing.T) {
		h := newHarness(t, func(c *peers.MasterConfig) {
			c.IsQSPLink = func(cfg hbp.Config) bool { return cfg.PackageID == "QSP-LINK:test" }
		})
		h.login(addrA)
		h.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS", ColorCode: "11", PackageID: "QSP-LINK:test"}, addrA)
		carriedAs(t, h, v(0x1, 3121001, 2, hbp.FrameTypeVoice, 0))
		h.c.advance(60 * time.Millisecond)
		if tg := carriedAs(t, h, v(0x2, 3121002, 9, hbp.FrameTypeVoice, 0)); tg != 9 {
			t.Errorf("a linked server's second call was carried on TG %d, want its own 9", tg)
		}
	})
}
