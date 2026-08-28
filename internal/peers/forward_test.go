package peers_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

const peerTwo = hbp.RepeaterID(3121380)

// readyFromMaster adapts the master for the routing core.
type readyFromMaster struct{ m *peers.Master }

func (r readyFromMaster) Ready(id hbp.RepeaterID) bool {
	p, ok := r.m.Lookup(id)
	return ok && p.State.CanPassTraffic()
}

func (r readyFromMaster) ReadyPeers() []hbp.RepeaterID {
	var out []hbp.RepeaterID
	for _, p := range r.m.Peers() {
		if p.State.CanPassTraffic() {
			out = append(out, p.ID)
		}
	}
	return out
}

// startForwarding brings up a listener that relays TG3148/TS1 to TG91/TS2.
func startForwarding(t *testing.T) *peers.Listener { return startMulti(t, true) }

// startObserving brings up the same listener with forwarding switched off,
// which is QSP's default posture.
func startObserving(t *testing.T) *peers.Listener { return startMulti(t, false) }

// startMulti builds a listener whose master accepts more than one peer, which
// the single-peer helper in listener_test.go deliberately does not.
func startMulti(t *testing.T, forwarding bool) *peers.Listener {
	t.Helper()

	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}

	table, err := routing.NewTable([]routing.Bridge{{
		Name: "test-net", Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: testID, Talkgroup: 3148, Timeslot: hbp.Timeslot1},
			{Peer: peerTwo, Talkgroup: 91, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}

	var core *routing.Core
	if forwarding {
		core, err = routing.NewCore(routing.CoreOptions{Table: table, Peers: readyFromMaster{m: master}})
		if err != nil {
			t.Fatalf("NewCore: %v", err)
		}
	}

	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
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
	return l
}

// register drives a full handshake for one synthetic hotspot.
func register(t *testing.T, addr string, id hbp.RepeaterID, callsign string) *client {
	t.Helper()
	c := dial(t, addr)
	c.send(hbp.Login{RepeaterID: id})
	ack, ok := c.recv().(hbp.Ack)
	if !ok {
		t.Fatalf("%s: login was not answered with RPTACK", callsign)
	}
	c.send(hbp.Key{RepeaterID: id, Digest: hbp.Digest(ack.Salt(), []byte(testPassword))})
	c.recv()
	c.send(hbp.Config{RepeaterID: id, Callsign: callsign, ColorCode: "11"})
	c.recv()
	return c
}

// TestTrafficIsRelayedBetweenTwoRealPeers is the whole point of QSP, exercised
// over real sockets.
//
// One hotspot transmits on one talkgroup and timeslot; a second, bridged to a
// different talkgroup and timeslot, must receive it — rewritten, attributed to
// the original radio, and never echoed back to the sender.
func TestTrafficIsRelayedBetweenTwoRealPeers(t *testing.T) {
	l := startForwarding(t)
	addr := l.Address()

	sender := register(t, addr, testID, "K9MLS")
	receiver := register(t, addr, peerTwo, "W5ABC")

	sender.send(hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: 3148,
		Timeslot: hbp.Timeslot1, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeSync, StreamID: 0xFEEDFACE,
		Trailing: []byte{0x11, 0x22},
	})

	msg := receiver.recv()
	got, ok := msg.(hbp.Data)
	if !ok {
		t.Fatalf("the receiver got %s, want a DMRD frame", msg.Kind())
	}

	if got.TargetID != 91 {
		t.Errorf("talkgroup = %d, want it translated to 91", got.TargetID)
	}
	if got.Timeslot != hbp.Timeslot2 {
		t.Errorf("timeslot = %s, want TS2", got.Timeslot)
	}
	if got.RepeaterID != peerTwo {
		t.Errorf("repeater ID = %d, want the receiving peer %d", got.RepeaterID, peerTwo)
	}
	if got.SourceID != 3132910 {
		t.Errorf("source radio = %d, want the originator preserved", got.SourceID)
	}
	if got.StreamID != 0xFEEDFACE {
		t.Errorf("stream ID = %s, want it preserved", got.StreamID)
	}

	// The sender must not hear itself.
	sender.silence(300 * time.Millisecond)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && l.Stats().Forwarded == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if s := l.Stats(); s.Forwarded != 1 {
		t.Errorf("forwarded = %d, want 1", s.Forwarded)
	}
}

// TestUnbridgedTrafficIsNotRelayed.
// TestUnbridgedTrafficIsRepeatedToOtherPeers, over real sockets.
//
// This test used to assert the opposite, and asserting it was the bug: a
// talkgroup no bridge covers was expected to reach nobody. Two hotspots on one
// master, one transmitting, the other hearing it, is what a DMR network is for
// — and it needs no bridge. See docs/adr/ADR-0019-master-repeats.md.
func TestUnbridgedTrafficIsRepeatedToOtherPeers(t *testing.T) {
	l := startForwarding(t)
	addr := l.Address()

	sender := register(t, addr, testID, "K9MLS")
	receiver := register(t, addr, peerTwo, "W5ABC")

	// A talkgroup no bridge covers.
	sender.send(hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: 31673,
		Timeslot: hbp.Timeslot1, FrameType: hbp.FrameTypeSync,
		StreamID: 0x1234, Trailing: []byte{0, 0},
	})

	msg := receiver.recv()
	got, ok := msg.(hbp.Data)
	if !ok {
		t.Fatalf("the second hotspot received %s, want a DMRD frame", msg.Kind())
	}
	if got.TargetID != 31673 {
		t.Errorf("repeated on TG %d, want 31673 — the talkgroup it arrived on", got.TargetID)
	}
	if got.Timeslot != hbp.Timeslot1 {
		t.Errorf("repeated on %s, want TS1", got.Timeslot)
	}
	if got.SourceID != 3132910 {
		t.Errorf("the originating radio's ID became %d", got.SourceID)
	}
	if got.RepeaterID != peerTwo {
		t.Errorf("frame carries repeater ID %d, want it rewritten to %d", got.RepeaterID, peerTwo)
	}
	if s := l.Stats(); s.Forwarded != 1 {
		t.Errorf("forwarded = %d, want 1", s.Forwarded)
	}
}

// TestForwardingDisabledRelaysNothing.
//
// The default posture: QSP accepts and observes traffic without putting audio
// on anybody's repeater.
func TestForwardingDisabledRelaysNothing(t *testing.T) {
	l := startObserving(t)
	addr := l.Address()
	sender := register(t, addr, testID, "K9MLS")
	receiver := register(t, addr, peerTwo, "W5ABC")

	sender.send(hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: 3148,
		Timeslot: hbp.Timeslot1, FrameType: hbp.FrameTypeSync,
		StreamID: 0x999, Trailing: []byte{0, 0},
	})

	receiver.silence(300 * time.Millisecond)
	if s := l.Stats(); s.Forwarded != 0 {
		t.Errorf("forwarded = %d with forwarding disabled, want 0", s.Forwarded)
	}
}

// TestWholeTransmissionIsRelayed covers a complete keyup, not one frame.
func TestWholeTransmissionIsRelayed(t *testing.T) {
	l := startForwarding(t)
	addr := l.Address()

	sender := register(t, addr, testID, "K9MLS")
	receiver := register(t, addr, peerTwo, "W5ABC")

	frames := []hbp.FrameType{hbp.FrameTypeSync}
	for i := 0; i < 8; i++ {
		frames = append(frames, hbp.FrameTypeVoice)
	}
	frames = append(frames, hbp.FrameTypeSync)

	for i, ft := range frames {
		sender.send(hbp.Data{
			RepeaterID: testID, SourceID: 3132910, TargetID: 3148,
			Timeslot: hbp.Timeslot1, FrameType: ft, StreamID: 0xC0FFEE,
			Sequence: uint8(i), Trailing: []byte{0, 0},
		})
	}

	received := 0
	for i := 0; i < len(frames); i++ {
		msg := receiver.recv()
		if d, ok := msg.(hbp.Data); ok && d.TargetID == 91 {
			received++
		}
	}
	if received != len(frames) {
		t.Errorf("received %d frames, want all %d", received, len(frames))
	}

	// The terminator released the destination, so the next keyup works.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && l.Stats().Forwarded < uint64(len(frames)) {
		time.Sleep(10 * time.Millisecond)
	}
	if s := l.Stats(); s.Forwarded != uint64(len(frames)) {
		t.Errorf("forwarded = %d, want %d", s.Forwarded, len(frames))
	}
}

// TestScheduleGatesForwarding proves the schedule decides whether a bridge
// carries traffic, over a real socket, in both states.
//
// The bridge is configured Enabled: false. If forwarding happens while the
// window is open, the schedule overrode that; if it stops when the window
// closes, the schedule closed it.
func TestScheduleGatesForwarding(t *testing.T) {
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}

	endpoints := []routing.Endpoint{
		{Peer: testID, Talkgroup: 3148, Timeslot: hbp.Timeslot1},
		{Peer: peerTwo, Talkgroup: 91, Timeslot: hbp.Timeslot2},
	}
	// Configured OFF. Only the schedule can open it.
	build := func(open bool) (*routing.Table, error) {
		return routing.NewTable([]routing.Bridge{{
			Name: "scheduled-net", Enabled: open, Endpoints: endpoints,
		}})
	}

	closed, err := build(false)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	// NoRepeat because this test is about the bridge being gated, not about the
	// master's own repeat behaviour. Repeat would deliver on TG 3148 whatever
	// the schedule said — correctly, since a closed window closes a bridge and
	// not a talkgroup — and would drown the signal this test is looking for.
	// internal/routing/repeat_test.go covers repeat itself.
	core, err := routing.NewCore(routing.CoreOptions{
		Table: closed, Peers: readyFromMaster{m: master}, NoRepeat: true,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	// A schedule stub whose answer the test controls directly, so the test
	// exercises the gating rather than the calendar arithmetic (which
	// internal/scheduler covers exhaustively).
	var open atomic.Bool
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
		ScheduleState: func(time.Time) map[string]bool {
			return map[string]bool{"scheduled-net": open.Load()}
		},
		Rebuild: func(now time.Time) (*routing.Table, error) { return build(open.Load()) },
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()

	sender := register(t, l.Address(), testID, "K9MLS")
	receiver := register(t, l.Address(), peerTwo, "W5ABC")

	send := func(stream hbp.StreamID) {
		sender.send(hbp.Data{
			RepeaterID: testID, SourceID: 3132910, TargetID: 3148,
			Timeslot: hbp.Timeslot1, FrameType: hbp.FrameTypeSync,
			StreamID: stream, Trailing: []byte{0, 0},
		})
	}

	// Window closed: nothing is relayed.
	send(0x1000)
	receiver.silence(400 * time.Millisecond)
	if l.Stats().Forwarded != 0 {
		t.Fatalf("traffic was relayed while the window was closed")
	}

	// Window opens.
	open.Store(true)
	// Poll the listener's published snapshot, never the Core: Core is owned by
	// the serve goroutine and reaching into it from here is a data race.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && len(l.EnabledBridges()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	send(0x2000)
	if _, ok := receiver.recv().(hbp.Data); !ok {
		t.Fatal("traffic was not relayed while the window was open")
	}

	// Window closes again.
	open.Store(false)
	deadline = time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && len(l.EnabledBridges()) != 0 {
		time.Sleep(20 * time.Millisecond)
	}
	before := l.Stats().Forwarded
	send(0x3000)
	receiver.silence(400 * time.Millisecond)
	if l.Stats().Forwarded != before {
		t.Error("traffic was relayed after the window closed")
	}
}

// TestPTTOpensTheBridgeAndCarriesTheOpeningFrame.
//
// The subtle requirement: the frame that opens a bridge must itself be relayed.
// Opening on the second frame would clip the first syllable of every on-demand
// transmission, which is the exact complaint operators make about systems that
// get this wrong.
func TestPTTOpensTheBridgeAndCarriesTheOpeningFrame(t *testing.T) {
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}

	endpoints := []routing.Endpoint{
		{Peer: testID, Talkgroup: 3148, Timeslot: hbp.Timeslot1},
		{Peer: peerTwo, Talkgroup: 91, Timeslot: hbp.Timeslot2},
	}
	triggers, err := routing.NewTriggers([]routing.Trigger{{
		Bridge:   "on-demand",
		On:       []routing.Endpoint{{Peer: testID, Talkgroup: 3148, Timeslot: hbp.Timeslot1}},
		HangTime: 2 * time.Second,
		Enabled:  true,
	}})
	if err != nil {
		t.Fatalf("NewTriggers: %v", err)
	}

	// Configured closed. Only a transmission can open it.
	build := func(now time.Time) (*routing.Table, error) {
		return routing.NewTable([]routing.Bridge{{
			Name: "on-demand", Enabled: triggers.ActiveAt(now)["on-demand"], Endpoints: endpoints,
		}})
	}
	closed, err := build(time.Now())
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	// NoRepeat because this test is about the bridge being gated, not about the
	// master's own repeat behaviour. Repeat would deliver on TG 3148 whatever
	// the schedule said — correctly, since a closed window closes a bridge and
	// not a talkgroup — and would drown the signal this test is looking for.
	// internal/routing/repeat_test.go covers repeat itself.
	core, err := routing.NewCore(routing.CoreOptions{
		Table: closed, Peers: readyFromMaster{m: master}, NoRepeat: true,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
		Triggers:      triggers,
		ScheduleState: triggers.ActiveAt,
		Rebuild:       build,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()

	sender := register(t, l.Address(), testID, "K9MLS")
	receiver := register(t, l.Address(), peerTwo, "W5ABC")

	// Nothing has been transmitted, so the bridge is closed.
	if len(l.EnabledBridges()) != 0 {
		t.Fatalf("the bridge was open before anybody transmitted: %v", l.EnabledBridges())
	}

	// One keyup. The opening frame must arrive at the far end.
	sender.send(hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: 3148,
		Timeslot: hbp.Timeslot1, FrameType: hbp.FrameTypeSync,
		StreamID: 0x71771, Sequence: 0, Trailing: []byte{0, 0},
	})

	msg := receiver.recv()
	got, ok := msg.(hbp.Data)
	if !ok {
		t.Fatalf("the far end received %s, want the opening frame", msg.Kind())
	}
	if got.TargetID != 91 || got.Timeslot != hbp.Timeslot2 {
		t.Errorf("the opening frame was not rewritten: TG%d %s", got.TargetID, got.Timeslot)
	}
	if got.Sequence != 0 {
		t.Errorf("the frame relayed was sequence %d, not the opening one", got.Sequence)
	}

	// The rest of the transmission follows.
	for i := 1; i <= 5; i++ {
		sender.send(hbp.Data{
			RepeaterID: testID, SourceID: 3132910, TargetID: 3148,
			Timeslot: hbp.Timeslot1, FrameType: hbp.FrameTypeVoice,
			StreamID: 0x71771, Sequence: uint8(i), Trailing: []byte{0, 0},
		})
		if _, ok := receiver.recv().(hbp.Data); !ok {
			t.Fatalf("frame %d was not relayed", i)
		}
	}

	// After the hang time the bridge closes on its own.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && len(l.EnabledBridges()) != 0 {
		time.Sleep(50 * time.Millisecond)
	}
	if len(l.EnabledBridges()) != 0 {
		t.Errorf("the bridge is still open after its hang time: %v", l.EnabledBridges())
	}

	before := l.Stats().Forwarded
	sender.send(hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: 31673,
		Timeslot: hbp.Timeslot1, FrameType: hbp.FrameTypeSync,
		StreamID: 0x9999, Trailing: []byte{0, 0},
	})
	receiver.silence(400 * time.Millisecond)
	if l.Stats().Forwarded != before {
		t.Error("a non-trigger talkgroup was relayed through a closed bridge")
	}
}
