package peers_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// client is a synthetic hotspot: a real UDP socket speaking real HBP.
//
// These tests exercise the listener over the loopback interface rather than
// against mocks, because the point of this layer is the socket. Nothing here
// simulates the protocol; it speaks it.
type client struct {
	t    *testing.T
	conn *net.UDPConn
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("resolve %s: %v", addr, err)
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &client{t: t, conn: conn}
}

func (c *client) send(msg hbp.Message) {
	c.t.Helper()
	if _, err := c.conn.Write(msg.Marshal()); err != nil {
		c.t.Fatalf("write: %v", err)
	}
}

// recv reads one reply, failing the test if none arrives.
func (c *client) recv() hbp.Message {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		c.t.Fatalf("set deadline: %v", err)
	}
	buf := make([]byte, 1500)
	n, err := c.conn.Read(buf)
	if err != nil {
		c.t.Fatalf("no reply from the listener: %v", err)
	}
	msg, err := hbp.Parse(buf[:n])
	if err != nil {
		c.t.Fatalf("listener sent an unparseable reply %x: %v", buf[:n], err)
	}
	return msg
}

// silence asserts that nothing is sent back within a short window.
func (c *client) silence(d time.Duration) {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(d)); err != nil {
		c.t.Fatalf("set deadline: %v", err)
	}
	buf := make([]byte, 1500)
	if n, err := c.conn.Read(buf); err == nil {
		c.t.Fatalf("expected no reply, got %d bytes: %x", n, buf[:n])
	}
}

func startListener(t *testing.T) (*peers.Listener, *events.Bus, string) {
	t.Helper()

	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(id hbp.RepeaterID) ([]byte, bool) {
			if id == testID {
				return []byte(testPassword), true
			}
			return nil, false
		},
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}

	bus := events.NewBus(logging.Discard(), events.Options{HistorySize: 32, SubscriberBuffer: 32})
	t.Cleanup(bus.Close)

	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0",
		Master:        master,
		Bus:           bus,
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

	return l, bus, l.Address()
}

// TestListenerCompletesARealHandshakeOverUDP is the closest thing to the
// Phase 1 gate that can be run without hardware: a real socket, real datagrams,
// a real handshake.
func TestListenerCompletesARealHandshakeOverUDP(t *testing.T) {
	l, bus, addr := startListener(t)

	sub, _ := bus.Subscribe()
	defer sub.Close()

	c := dial(t, addr)

	c.send(hbp.Login{RepeaterID: testID})
	ack, ok := c.recv().(hbp.Ack)
	if !ok {
		t.Fatal("login was not answered with RPTACK")
	}
	issued := ack.Salt()

	c.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(issued, []byte(testPassword))})
	if got, ok := c.recv().(hbp.Ack); !ok || got.RepeaterID() != testID {
		t.Fatalf("authentication was not acknowledged: %+v", got)
	}

	c.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS", ColorCode: "11"})
	if got, ok := c.recv().(hbp.Ack); !ok || got.RepeaterID() != testID {
		t.Fatalf("configuration was not acknowledged: %+v", got)
	}

	// Keepalive round trip.
	c.send(hbp.Ping{RepeaterID: testID})
	if pong, ok := c.recv().(hbp.Pong); !ok || pong.RepeaterID != testID {
		t.Fatalf("keepalive was not answered with MSTPONG: %+v", pong)
	}

	// A voice frame is accepted.
	c.send(hbp.Data{
		RepeaterID: testID, SourceID: uint32(testID), TargetID: 3100,
		Timeslot: hbp.Timeslot2, StreamID: 0xabcdef01, Trailing: []byte{0, 0},
	})
	// Frames are not acknowledged, so give the loop a moment and check counters.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if l.Stats().Frames > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	s := l.Stats()
	if s.Frames != 1 {
		t.Errorf("frames accepted = %d, want 1", s.Frames)
	}
	if s.ConfiguredPeers != 1 {
		t.Errorf("configured peers = %d, want 1", s.ConfiguredPeers)
	}
	if s.Received < 5 {
		t.Errorf("datagrams received = %d, want at least 5", s.Received)
	}
	if s.WriteErrors != 0 {
		t.Errorf("write errors = %d, want 0", s.WriteErrors)
	}

	// A peer.connected event reached the bus.
	select {
	case ev := <-sub.C():
		if ev.Type != events.TypePeerConnected {
			t.Errorf("first event is %s, want peer.connected", ev.Type)
		}
	case <-time.After(3 * time.Second):
		t.Error("no peer.connected event was published")
	}
}

// TestListenerIgnoresGarbageWithoutReplying proves a scanner gets nothing back
// and cannot disturb the loop.
func TestListenerIgnoresGarbageWithoutReplying(t *testing.T) {
	l, _, addr := startListener(t)
	c := dial(t, addr)

	for _, junk := range [][]byte{
		[]byte("hello"),
		make([]byte, 1400),
		[]byte("RPTL"),
		{0xFF, 0xFF, 0xFF, 0xFF},
	} {
		if _, err := c.conn.Write(junk); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	c.silence(300 * time.Millisecond)

	if l.Stats().Dropped < 4 {
		t.Errorf("dropped = %d, want at least 4", l.Stats().Dropped)
	}

	// The listener is still healthy and still serving.
	c.send(hbp.Login{RepeaterID: testID})
	if _, ok := c.recv().(hbp.Ack); !ok {
		t.Error("the listener stopped serving after receiving garbage")
	}
}

// TestListenerRejectsUnknownPeerOverTheWire.
//
// A refused peer must be told, over the socket, rather than left to retry into
// silence until it gives up.
func TestListenerRejectsUnknownPeerOverTheWire(t *testing.T) {
	_, _, addr := startListener(t)
	c := dial(t, addr)

	c.send(hbp.Login{RepeaterID: 9999999})
	msg := c.recv()
	nak, ok := msg.(hbp.Nak)
	if !ok {
		t.Fatalf("refused login answered with %s, want MSTNAK", msg.Kind())
	}
	if nak.RepeaterID != 9999999 {
		t.Errorf("MSTNAK carries repeater ID %d, want 9999999", nak.RepeaterID)
	}
}

// TestListenerHandlesCleanDisconnect end to end.
func TestListenerHandlesCleanDisconnect(t *testing.T) {
	l, bus, addr := startListener(t)
	sub, _ := bus.Subscribe()
	defer sub.Close()

	c := dial(t, addr)
	c.send(hbp.Login{RepeaterID: testID})
	ack := c.recv().(hbp.Ack)
	c.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(ack.Salt(), []byte(testPassword))})
	c.recv()
	c.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS"})
	c.recv()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && l.Stats().ConfiguredPeers == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if l.Stats().ConfiguredPeers != 1 {
		t.Fatal("the peer never registered")
	}

	// The hotspot shuts down cleanly.
	c.send(hbp.RepeaterClose{RepeaterID: testID})

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && l.Stats().ConfiguredPeers != 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if l.Stats().ConfiguredPeers != 0 {
		t.Error("the peer is still registered after closing cleanly; it should not wait for a timeout")
	}
}

// TestListenerBindFailureIsReportedClearly.
func TestListenerBindFailureIsReportedClearly(t *testing.T) {
	first, _, addr := startListener(t)
	_ = first

	master, _ := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	second, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: addr,
		Master:        master,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	err = second.Start(context.Background())
	if err == nil {
		_ = second.Close()
		t.Fatal("binding an address already in use succeeded")
	}
	if !contains(err.Error(), "port is free") {
		t.Errorf("error should tell the operator what to check, got: %v", err)
	}
}

// TestListenerCloseIsIdempotentAndSafeUnstarted.
func TestListenerCloseIsIdempotentAndSafeUnstarted(t *testing.T) {
	master, _ := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close on an unstarted listener: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestListenerHealthReportsHonestly covers all three states.
func TestListenerHealthReportsHonestly(t *testing.T) {
	// Disabled.
	res := peers.HealthCheck{DisabledReason: "the DMR listener is disabled"}.Check(context.Background())
	if res.Status != health.StatusUnavailable {
		t.Errorf("disabled listener reports %s, want unavailable", res.Status)
	}
	if !contains(res.Summary, "disabled") {
		t.Errorf("summary should say why: %q", res.Summary)
	}

	// Running.
	l, _, _ := startListener(t)
	res = peers.HealthCheck{Listener: l}.Check(context.Background())
	if res.Status != health.StatusHealthy {
		t.Errorf("running listener reports %s, want healthy", res.Status)
	}
	if res.Detail["address"] == "" {
		t.Error("health detail omits the bound address")
	}

	// Stopped.
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	res = peers.HealthCheck{Listener: l}.Check(context.Background())
	if res.Status != health.StatusFailing {
		t.Errorf("stopped listener reports %s, want failing", res.Status)
	}
}

// TestEmptyMasterIsHealthyNotDegraded: a quiet club network is not a fault.
func TestEmptyMasterIsHealthyNotDegraded(t *testing.T) {
	l, _, _ := startListener(t)
	master, _ := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	res := peers.PeersHealthCheck{Listener: l, Master: master}.Check(context.Background())
	if res.Status != health.StatusHealthy {
		t.Errorf("a master with no peers reports %s, want healthy", res.Status)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

// TestSnapshotIsSafeUnderConcurrentReads exercises the reason Snapshot exists.
//
// Master is owned by the serve goroutine and carries no locks. Reading its
// registry from an HTTP handler would be a data race; the race detector fails
// this test if the snapshot mechanism is bypassed.
func TestSnapshotIsSafeUnderConcurrentReads(t *testing.T) {
	l, _, addr := startListener(t)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				for _, p := range l.Snapshot() {
					_ = p.Callsign()
					_ = p.ID
				}
				_ = l.Stats()
			}
		}
	}()

	c := dial(t, addr)
	c.send(hbp.Login{RepeaterID: testID})
	ack := c.recv().(hbp.Ack)
	c.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(ack.Salt(), []byte(testPassword))})
	c.recv()
	c.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS"})
	c.recv()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(l.Snapshot()) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	close(stop)
	<-done

	snap := l.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("snapshot holds %d peers, want 1", len(snap))
	}
	if snap[0].Callsign() != "K9MLS" {
		t.Errorf("snapshot callsign = %q, want K9MLS", snap[0].Callsign())
	}
}

// TestSnapshotOnUnstartedListenerIsEmpty.
func TestSnapshotOnUnstartedListenerIsEmpty(t *testing.T) {
	master, _ := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	if got := l.Snapshot(); len(got) != 0 {
		t.Errorf("unstarted listener reports %d peers", len(got))
	}
}

// TestSweepIntervalIsFineEnoughForCallTimeout pins a relationship that is easy
// to break by editing one constant.
//
// The listener sweeps on its read deadline. If that deadline were coarser than
// the call timeout, a transmission that lost its terminator would keep showing
// as live until the next sweep — a phantom on the console that an operator
// cannot tell from somebody actually keyed up.
func TestSweepIntervalIsFineEnoughForCallTimeout(t *testing.T) {
	if peers.SweepInterval() > calls.StreamTimeout {
		t.Fatalf("sweep interval %s is coarser than the call timeout %s; "+
			"a lost transmission would display as live for up to %s",
			peers.SweepInterval(), calls.StreamTimeout, peers.SweepInterval())
	}
}

// TestLostCallIsClosedPromptly exercises the whole path over a real socket:
// frames arrive, the peer stops mid-transmission, and the sweep closes it.
func TestLostCallIsClosedPromptly(t *testing.T) {
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	tracker := calls.NewTracker(calls.Options{Timeout: 300 * time.Millisecond})

	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Calls: tracker,
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

	c := dial(t, l.Address())
	c.send(hbp.Login{RepeaterID: testID})
	ack := c.recv().(hbp.Ack)
	c.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(ack.Salt(), []byte(testPassword))})
	c.recv()
	c.send(hbp.Config{RepeaterID: testID, Callsign: "K9MLS"})
	c.recv()

	// A transmission that stops without its terminator.
	for i, ft := range []hbp.FrameType{hbp.FrameTypeSync, hbp.FrameTypeVoice, hbp.FrameTypeVoice} {
		c.send(hbp.Data{
			RepeaterID: testID, SourceID: uint32(testID), TargetID: 9990,
			Timeslot: hbp.Timeslot2, FrameType: ft, StreamID: 0x7777,
			Sequence: uint8(i), Trailing: []byte{0, 0},
		})
	}

	// It should appear as active, then be closed by the sweep.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && len(l.Calls().Active) == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if len(l.Calls().Active) != 1 {
		t.Fatalf("active calls = %d, want 1", len(l.Calls().Active))
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(l.Calls().Active) > 0 {
		time.Sleep(20 * time.Millisecond)
	}

	snap := l.Calls()
	if len(snap.Active) != 0 {
		t.Fatalf("the lost call is still active after the sweep")
	}
	if len(snap.Recent) != 1 {
		t.Fatalf("recent calls = %d, want 1", len(snap.Recent))
	}
	if snap.Recent[0].EndReason != calls.EndTimedOut {
		t.Errorf("end reason = %q, want %q", snap.Recent[0].EndReason, calls.EndTimedOut)
	}
	if snap.Recent[0].Frames != 3 {
		t.Errorf("frames = %d, want 3", snap.Recent[0].Frames)
	}
}
