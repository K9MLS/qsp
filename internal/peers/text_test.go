package peers_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// aText is the message every test here sends: a group text to talkgroup 2
// from an ID that is not the receiving radio's.
var aText = tms.Message{From: 9990, To: 2, Group: true, IPID: 1, Reference: 0x80, Text: "QSP"}

// startTextListener is a listener with a call tracker, so that an active call
// can be made to exist, and one peer registered over a real socket with
// colour code 11.
func startTextListener(t *testing.T) (*peers.Listener, *client) {
	t.Helper()
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(id hbp.RepeaterID) ([]byte, bool) {
			return []byte(testPassword), id == testID
		},
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Calls: calls.NewTracker(calls.Options{}),
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	c := dial(t, l.Address())
	c.send(hbp.Login{RepeaterID: testID})
	ack, ok := c.recv().(hbp.Ack)
	if !ok {
		t.Fatal("login was not answered")
	}
	c.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(ack.Salt(), []byte(testPassword))})
	if _, ok := c.recv().(hbp.Ack); !ok {
		t.Fatal("key was not acknowledged")
	}
	c.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS", ColorCode: "11"})
	if _, ok := c.recv().(hbp.Ack); !ok {
		t.Fatal("configuration was not acknowledged")
	}
	// The peer snapshot is published by the serve loop; wait for it.
	waitFor(t, "the peer to be registered", func() bool {
		for _, p := range l.Snapshot() {
			if p.ID == testID && p.State.CanPassTraffic() {
				return true
			}
		}
		return false
	})
	return l, c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestAComposedTextReachesThePeer sends one text over a real socket and reads
// what arrives: every frame of it, addressed to the peer, in order, coded
// with the peer's own colour code.
//
// To see it fail: pass a fixed colour code of 1 to tms.Frames in SendText
// instead of the one colourCodeOf read, and every frame fails on colour code
// and burst.
func TestAComposedTextReachesThePeer(t *testing.T) {
	l, c := startTextListener(t)
	if err := l.SendText(hbp.Timeslot2, aText, testID); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	all, err := tms.Frames(aText, hbp.Timeslot2, 11, func() hbp.StreamID { return 1 })
	if err != nil {
		t.Fatalf("%v", err)
	}
	// A hotspot is sent the last preamble only; see preamble.go.
	want := all[tms.Preambles-1:]
	for i := range want {
		d, ok := c.recv().(hbp.Data)
		if !ok {
			t.Fatalf("frame %d is not DMRD", i)
		}
		if d.RepeaterID != testID {
			t.Errorf("frame %d names repeater %d, want the peer's own %d", i, d.RepeaterID, testID)
		}
		if d.Sequence != want[i].Sequence || d.DataType != want[i].DataType || d.Payload != want[i].Payload {
			t.Errorf("frame %d: seq %d type %#x, want seq %d type %#x, bursts equal %v",
				i, d.Sequence, d.DataType, want[i].Sequence, want[i].DataType, d.Payload == want[i].Payload)
		}
		if cc, _, _ := dmrfec.SlotTypeOf(d.Payload[:]); cc != 11 {
			t.Errorf("frame %d carries colour code %d, and the peer announced 11", i, cc)
		}
	}
	c.silence(200 * time.Millisecond)
}

// TestASendIsRefusedWhereItCouldNotArrive covers every refusal, each on a
// fresh listener so one case cannot leave another busy.
//
// To see it fail: delete the loop over l.Calls().Active in SendText and the
// active-call row sends into the call; delete the Busy check and the second
// send starts over the first.
func TestASendIsRefusedWhereItCouldNotArrive(t *testing.T) {
	private := aText
	private.Group = false

	tests := []struct {
		name  string
		setup func(t *testing.T, l *peers.Listener, c *client)
		peer  hbp.RepeaterID
		slot  hbp.Timeslot
		m     tms.Message
		is    error
	}{
		{
			name: "a peer that is not registered",
			peer: testID + 1, slot: hbp.Timeslot2, m: aText,
			is: peers.ErrTextUnknownPeer,
		},
		{
			name: "the whole network, on an instance that does not forward",
			peer: 0, slot: hbp.Timeslot2, m: aText,
			is: peers.ErrTextNoRouting,
		},
		{
			name: "a private text",
			peer: testID, slot: hbp.Timeslot2, m: private,
			is: tms.ErrPrivateNotYet,
		},
		{
			name: "a second text while the first is still going out",
			setup: func(t *testing.T, l *peers.Listener, _ *client) {
				if err := l.SendText(hbp.Timeslot2, aText, testID); err != nil {
					t.Fatalf("the first send: %v", err)
				}
			},
			peer: testID, slot: hbp.Timeslot2, m: aText,
			is: peers.ErrTextBusy,
		},
		{
			name: "a call on the same timeslot",
			setup: func(t *testing.T, l *peers.Listener, c *client) {
				activeCall(t, l, c, hbp.Timeslot2)
			},
			peer: testID, slot: hbp.Timeslot2, m: aText,
			is: peers.ErrTextChannelBusy,
		},
		{
			name: "a call on the other timeslot does not block",
			setup: func(t *testing.T, l *peers.Listener, c *client) {
				activeCall(t, l, c, hbp.Timeslot1)
			},
			peer: testID, slot: hbp.Timeslot2, m: aText,
			is: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, c := startTextListener(t)
			if tc.setup != nil {
				tc.setup(t, l, c)
			}
			err := l.SendText(tc.slot, tc.m, tc.peer)
			if tc.is == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.is) {
				t.Fatalf("error %v, want %v", err, tc.is)
			}
		})
	}
}

// activeCall makes a voice call exist on a timeslot: the registered peer
// keys up, over the socket, so the serve loop records it.
//
// **Not through ObserveFromIPSC.** That writes the call tracker from the
// caller's goroutine while the serve loop writes it from its own, which is a
// data race the race detector finds within seconds; see the handover. A test
// that called it here would fail check.sh's race stage for a reason that has
// nothing to do with sending a text.
func activeCall(t *testing.T, l *peers.Listener, c *client, slot hbp.Timeslot) {
	t.Helper()
	c.send(hbp.Data{
		RepeaterID: testID, SourceID: 3155373, TargetID: 2,
		Timeslot: slot, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE10, Trailing: []byte{0, 0},
	})
	waitFor(t, "the call to be active", func() bool {
		for _, c := range l.Calls().Active {
			if c.Key.Timeslot == slot {
				return true
			}
		}
		return false
	})
}

// TestATextBeforeTheListenerStartsIsRefused: there is no socket yet.
func TestATextBeforeTheListenerStartsIsRefused(t *testing.T) {
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return nil, false },
	})
	if err != nil {
		t.Fatalf("%v", err)
	}
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{ListenAddress: "127.0.0.1:0", Master: master})
	if err != nil {
		t.Fatalf("%v", err)
	}
	if err := l.SendText(hbp.Timeslot2, aText, testID); !errors.Is(err, peers.ErrTextNotListening) {
		t.Fatalf("error %v, want %v", err, peers.ErrTextNotListening)
	}
}

// TestATextToTheTalkgroupReachesEveryHotspotOnIt is the ordinary case: sent
// to the network, a text is routed like any transmission on its talkgroup,
// so both registered hotspots receive every frame, each addressed to itself,
// and neither is left out as the "origin".
//
// To see it fail: route with Route(testID, …) instead of RouteFromServer in
// networkSink.Deliver — as if the text came from a hotspot — and repeat
// leaves that hotspot out; or remove the network busy check, and the second
// send is accepted over the first.
func TestATextToTheTalkgroupReachesEveryHotspotOnIt(t *testing.T) {
	l := startForwarding(t)
	a := register(t, l.Address(), testID, "K9MLS")
	b := register(t, l.Address(), peerTwo, "W5ABC")
	waitFor(t, "both peers to be registered", func() bool {
		n := 0
		for _, p := range l.Snapshot() {
			if p.State.CanPassTraffic() {
				n++
			}
		}
		return n == 2
	})

	if err := l.SendText(hbp.Timeslot2, aText, 0); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if err := l.SendText(hbp.Timeslot2, aText, 0); !errors.Is(err, peers.ErrTextBusy) {
		t.Errorf("a second network text while the first is going out: %v, want %v", err, peers.ErrTextBusy)
	}

	all, err := tms.Frames(aText, hbp.Timeslot2, 1, func() hbp.StreamID { return 1 })
	if err != nil {
		t.Fatalf("%v", err)
	}
	// One preamble per text to a hotspot; see preamble.go.
	want := all[tms.Preambles-1:]
	for _, tc := range []struct {
		c  *client
		id hbp.RepeaterID
	}{{a, testID}, {b, peerTwo}} {
		for i := range want {
			d, ok := tc.c.recv().(hbp.Data)
			if !ok {
				t.Fatalf("peer %d, frame %d is not DMRD", tc.id, i)
			}
			if d.RepeaterID != tc.id {
				t.Errorf("peer %d, frame %d names repeater %d", tc.id, i, d.RepeaterID)
			}
			if d.TargetID != 2 || d.SourceID != aText.From || d.DataType != want[i].DataType || d.Payload != want[i].Payload {
				t.Errorf("peer %d, frame %d: %d→%d type %#x, burst equal %v",
					tc.id, i, d.SourceID, d.TargetID, d.DataType, d.Payload == want[i].Payload)
			}
		}
	}
}

// TestATextToTheTalkgroupIsOfferedToTheMotorolaSide: every frame goes on to
// the IPSC listener, from origin 0, so no repeater is excluded as the sender.
// That is the path a hotspot's own texts already take to the repeaters.
//
// To see it fail: remove the sendToIPSC call from networkSink.Deliver, or
// pass routing.ServerOrigin as its origin.
func TestATextToTheTalkgroupIsOfferedToTheMotorolaSide(t *testing.T) {
	l := startForwarding(t)
	var (
		mu      sync.Mutex
		offered []uint32
	)
	l.SetIPSCSink(func(origin uint32, _ hbp.Data) {
		mu.Lock()
		offered = append(offered, origin)
		mu.Unlock()
	})
	if err := l.SendText(hbp.Timeslot2, aText, 0); err != nil {
		t.Fatalf("SendText: %v", err)
	}
	want := tms.Preambles + 1 + 1 // preambles, header, one block for "QSP"
	waitFor(t, "every frame to be offered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(offered) >= want
	})
	mu.Lock()
	defer mu.Unlock()
	for i, o := range offered {
		if o != 0 {
			t.Errorf("frame %d offered from origin %d, want 0", i, o)
		}
	}
}
