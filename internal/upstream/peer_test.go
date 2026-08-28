package upstream_test

import (
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/homebrew"
	"github.com/k9mls/qsp/internal/upstream"
)

// fakeMaster is the far end: a UDP socket that answers a homebrew handshake.
//
// It speaks over a real socket rather than being stubbed, because the thing
// under test here is the transport, and a transport tested without a socket
// tests nothing that matters.
type fakeMaster struct {
	t    *testing.T
	conn *net.UDPConn

	mu       sync.Mutex
	received []hbp.Message
	// peer is the address the link last spoke from, so the master can push a
	// frame down the same path a real one would.
	peer *net.UDPAddr
	// refuse makes the master NAK every login, for the wrong-password case.
	refuse bool
	// silent makes the master answer nothing, for the timeout case.
	silent bool
}

func newFakeMaster(t *testing.T) *fakeMaster {
	t.Helper()
	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	m := &fakeMaster{t: t, conn: conn}
	t.Cleanup(func() { _ = conn.Close() })
	go m.serve()
	return m
}

func (m *fakeMaster) address() string { return m.conn.LocalAddr().String() }

func (m *fakeMaster) setRefuse(v bool) { m.mu.Lock(); m.refuse = v; m.mu.Unlock() }
func (m *fakeMaster) setSilent(v bool) { m.mu.Lock(); m.silent = v; m.mu.Unlock() }

func (m *fakeMaster) got() []hbp.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]hbp.Message, len(m.received))
	copy(out, m.received)
	return out
}

// countOf reports how many messages of a kind have arrived.
func (m *fakeMaster) countOf(kind hbp.Kind) int {
	var n int
	for _, msg := range m.got() {
		if msg.Kind() == kind {
			n++
		}
	}
	return n
}

func (m *fakeMaster) serve() {
	buf := make([]byte, 1024)
	for {
		n, from, err := m.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		msg, err := hbp.Parse(buf[:n])
		if err != nil {
			continue
		}

		m.mu.Lock()
		m.received = append(m.received, msg)
		m.peer = from
		refuse, silent := m.refuse, m.silent
		m.mu.Unlock()

		if silent {
			continue
		}

		var reply []byte
		switch v := msg.(type) {
		case hbp.Login:
			if refuse {
				// staticcheck suggests hbp.Nak(v), which compiles only because
				// RPTL and MSTNAK happen to share a shape today. They are
				// distinct wire messages and the coincidence is not a
				// contract; a conversion would silently start copying any
				// field later added to both. Same reasoning as the Ping/Pong
				// case in internal/peers.
				//lint:ignore S1016 Login and Nak are distinct messages that share a shape by coincidence
				reply = hbp.Nak{RepeaterID: v.RepeaterID}.Marshal()
			} else {
				reply = hbp.Ack{Payload: [4]byte{1, 2, 3, 4}}.Marshal()
			}
		case hbp.Key, hbp.Config:
			reply = hbp.Ack{}.Marshal()
		case hbp.Ping:
			reply = hbp.Pong{RepeaterID: linkID}.Marshal()
		}
		if reply != nil {
			_, _ = m.conn.WriteToUDP(reply, from)
		}
	}
}

// pushFrame sends a frame down to whoever last spoke to the master, which is
// what a real one does when traffic arrives for a talkgroup the peer carries.
func (m *fakeMaster) pushFrame(frame hbp.Data) {
	m.mu.Lock()
	peer := m.peer
	m.mu.Unlock()
	if peer == nil {
		m.t.Fatal("the master has not heard from the link yet")
	}
	_, _ = m.conn.WriteToUDP(frame.Marshal(), peer)
}

const linkID = hbp.RepeaterID(3132910)

func newPeerLink(t *testing.T, target string, receive func(string, hbp.Data)) *upstream.PeerLink {
	t.Helper()
	hb, err := homebrew.New(homebrew.Config{
		Name:       "far",
		RepeaterID: linkID,
		Password:   []byte("secret"),
		Identity:   homebrew.Identity{Callsign: "K9MLS"},
	})
	if err != nil {
		t.Fatalf("homebrew.New: %v", err)
	}
	if receive == nil {
		receive = func(string, hbp.Data) {}
	}
	l, err := upstream.NewPeer(logging.Discard(), upstream.PeerConfig{
		Name:          "far",
		TargetAddress: target,
		Link:          hb,
		Receive:       receive,
		TickInterval:  10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}
	return l
}

// TestAPeerLinkCompletesTheHandshake is the transport doing its job: driving
// the state machine over a real socket until the far end accepts it.
func TestAPeerLinkCompletesTheHandshake(t *testing.T) {
	master := newFakeMaster(t)
	l := newPeerLink(t, master.address(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()

	waitFor(t, "the link to connect", func() bool {
		return l.State() == homebrew.StateConnected
	})

	// The full sequence reached the far end, in order.
	if master.countOf(hbp.KindLogin) == 0 {
		t.Error("no RPTL reached the master")
	}
	if master.countOf(hbp.KindKey) == 0 {
		t.Error("no RPTK reached the master")
	}
	if master.countOf(hbp.KindConfig) == 0 {
		t.Error("no RPTC reached the master")
	}
}

func TestKeepalivesReachTheFarEnd(t *testing.T) {
	master := newFakeMaster(t)
	hb, err := homebrew.New(homebrew.Config{
		Name: "far", RepeaterID: linkID, Password: []byte("secret"),
		Identity:  homebrew.Identity{Callsign: "K9MLS"},
		Keepalive: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("homebrew.New: %v", err)
	}
	l, err := upstream.NewPeer(logging.Discard(), upstream.PeerConfig{
		Name: "far", TargetAddress: master.address(), Link: hb,
		Receive:      func(string, hbp.Data) {},
		TickInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPeer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()

	waitFor(t, "keepalives to flow", func() bool { return master.countOf(hbp.KindPing) >= 3 })
}

// TestFramesReachTheFarEnd covers the outbound direction, including that the
// link stamps its own ID: the far end registered this link, not the peer the
// frame originally came from.
func TestFramesReachTheFarEnd(t *testing.T) {
	master := newFakeMaster(t)
	l := newPeerLink(t, master.address(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()
	waitFor(t, "the link to connect", func() bool { return l.State() == homebrew.StateConnected })

	frame := hbp.Data{
		SourceID: 3121001, TargetID: 9, RepeaterID: 999999,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup, StreamID: 0x1234,
	}
	if err := l.Send(frame); err != nil {
		t.Fatalf("Send: %v", err)
	}

	waitFor(t, "the frame to arrive", func() bool { return master.countOf(hbp.KindData) == 1 })
	for _, msg := range master.got() {
		if d, ok := msg.(hbp.Data); ok {
			if d.RepeaterID != linkID {
				t.Errorf("the frame names repeater %d, want the link's %d", d.RepeaterID, linkID)
			}
			if d.SourceID != 3121001 || d.TargetID != 9 {
				t.Error("the frame's own addressing was altered")
			}
		}
	}
}

// TestSendBeforeConnectingIsRefused. The transport must not pretend to have
// sent something it dropped.
func TestSendBeforeConnectingIsRefused(t *testing.T) {
	master := newFakeMaster(t)
	master.setSilent(true)
	l := newPeerLink(t, master.address(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()

	err := l.Send(hbp.Data{SourceID: 1, TargetID: 9, Timeslot: hbp.Timeslot2})
	if err == nil {
		t.Fatal("a frame was accepted on a link that has not connected")
	}
}

// TestFramesFromTheFarEndAreDelivered covers the inbound direction: a frame the
// master pushes down reaches the Receive callback unaltered.
func TestFramesFromTheFarEndAreDelivered(t *testing.T) {
	master := newFakeMaster(t)

	var mu sync.Mutex
	var got []hbp.Data
	l := newPeerLink(t, master.address(), func(name string, f hbp.Data) {
		mu.Lock()
		got = append(got, f)
		mu.Unlock()
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()
	waitFor(t, "the link to connect", func() bool { return l.State() == homebrew.StateConnected })

	master.pushFrame(hbp.Data{
		SourceID: 3121002, TargetID: 91, Timeslot: hbp.Timeslot1, CallType: hbp.CallGroup,
	})

	waitFor(t, "the frame to be delivered", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	})

	mu.Lock()
	defer mu.Unlock()
	if got[0].TargetID != 91 || got[0].SourceID != 3121002 {
		t.Errorf("the delivered frame was altered: %+v", got[0])
	}
	if got[0].Timeslot != hbp.Timeslot1 {
		t.Errorf("the timeslot was altered: %s", got[0].Timeslot)
	}
}

// TestARefusedLinkSaysWhatToCheck. A link that has never connected is usually a
// credential, and the status should say so rather than leave an operator
// guessing between that and the network.
func TestARefusedLinkSaysWhatToCheck(t *testing.T) {
	master := newFakeMaster(t)
	master.setRefuse(true)
	l := newPeerLink(t, master.address(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = l.Close() }()

	waitFor(t, "the link to be refused", func() bool {
		return l.State() == homebrew.StateBackoff
	})

	st := l.Status()
	if st.Summary == "" {
		t.Fatal("a refused link reported no summary")
	}
	if !strings.Contains(st.Summary, "never connected") {
		t.Errorf("the summary should distinguish never-connected from dropped: %q", st.Summary)
	}
	if !strings.Contains(st.Advice, "password") {
		t.Errorf("the advice should say what to check: %q", st.Advice)
	}
	if !st.Degraded() {
		t.Error("a link that has never connected did not report itself degraded")
	}
}

func TestClosingTellsTheFarEnd(t *testing.T) {
	master := newFakeMaster(t)
	l := newPeerLink(t, master.address(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitFor(t, "the link to connect", func() bool { return l.State() == homebrew.StateConnected })

	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	waitFor(t, "RPTCL to reach the master", func() bool {
		return master.countOf(hbp.KindRepeaterClose) == 1
	})
	// Closing twice must not write a second time or panic.
	if err := l.Close(); err != nil {
		t.Errorf("closing an already closed link: %v", err)
	}
}

// TestAPeerLinkIsAConnection keeps it usable in a Set beside OpenBridge.
func TestAPeerLinkIsAConnection(t *testing.T) {
	var _ upstream.Connection = (*upstream.PeerLink)(nil)
}

func TestNewPeerRequiresItsDependencies(t *testing.T) {
	hb, err := homebrew.New(homebrew.Config{
		Name: "far", RepeaterID: linkID, Password: []byte("secret"),
		Identity: homebrew.Identity{Callsign: "K9MLS"},
	})
	if err != nil {
		t.Fatalf("homebrew.New: %v", err)
	}
	for _, tc := range []struct {
		name string
		cfg  upstream.PeerConfig
	}{
		{"no name", upstream.PeerConfig{Link: hb, Receive: func(string, hbp.Data) {}}},
		{"no link", upstream.PeerConfig{Name: "far", Receive: func(string, hbp.Data) {}}},
		{"no receiver", upstream.PeerConfig{Name: "far", Link: hb}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := upstream.NewPeer(logging.Discard(), tc.cfg); err == nil {
				t.Error("a link was constructed without its dependencies")
			}
		})
	}
}
