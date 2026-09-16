package upstream_test

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/homebrew"
	"github.com/k9mls/qsp/internal/upstream"
)

// syncBuffer is a log sink the link's goroutines can write to concurrently.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeMasterAt listens on a given address, so a far end can vanish and come
// back where the link expects it.
func fakeMasterAt(t *testing.T, addr string) *fakeMaster {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	var conn *net.UDPConn
	// The port was just released; on a busy machine the kernel can take a
	// moment to let it go.
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err = net.ListenUDP("udp", a)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("listening again on %s: %v", addr, err)
	}
	m := &fakeMaster{t: t, conn: conn}
	t.Cleanup(func() { _ = conn.Close() })
	go m.serve()
	return m
}

// TestAFarEndThatBlinksIsLoggedInAgain is the BCARA link on 2026-09-16.
//
// The far end stopped listening for a moment, a keepalive bounced as
// "connection refused", and the link's read loop returned — stopped for good,
// no retry, no state change, until the process restarted.
//
// To see it bite: put `return` back after the failed read in PeerLink.serve.
// The link is no longer open and never connects again.
func TestAFarEndThatBlinksIsLoggedInAgain(t *testing.T) {
	first := newFakeMaster(t)
	addr := first.address()

	logs := &syncBuffer{}
	hb, err := homebrew.New(homebrew.Config{
		Name: "far", RepeaterID: linkID, Password: []byte("secret"),
		Identity:  homebrew.Identity{Callsign: "K9MLS"},
		Keepalive: 30 * time.Millisecond, Timeout: 150 * time.Millisecond,
		MinBackoff: 30 * time.Millisecond, MaxBackoff: 60 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	l, err := upstream.NewPeer(slog.New(slog.NewTextHandler(logs, nil)), upstream.PeerConfig{
		Name: "far", TargetAddress: addr, Link: hb,
		Receive: func(string, hbp.Data) {}, TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	waitFor(t, "the first connection", func() bool { return l.State() == homebrew.StateConnected })

	// The far end goes away: its port closes, and the next keepalive bounces.
	_ = first.conn.Close()
	waitFor(t, "the link to notice", func() bool {
		return strings.Contains(l.Status().Summary, "not answering")
	})
	if !l.Status().Open {
		t.Fatal("the link stopped when a read failed; nothing will ever reconnect it")
	}
	// Away for longer than two retry delays, so several reads fail and a
	// warning per failed read would show.
	time.Sleep(2*upstream.ReadRetryDelay + 500*time.Millisecond)
	if !l.Status().Open {
		t.Fatal("the link stopped during the outage")
	}

	// And comes back on the same address.
	fakeMasterAt(t, addr)
	deadline := time.Now().Add(5 * time.Second)
	for l.State() != homebrew.StateConnected || strings.Contains(l.Status().Summary, "not answering") {
		if time.Now().After(deadline) {
			t.Fatalf("the link did not log in again; state %s, status %q", l.State(), l.Status().Summary)
		}
		time.Sleep(5 * time.Millisecond)
	}

	out := logs.String()
	if n := strings.Count(out, "the far end is not answering"); n != 1 {
		t.Errorf("the outage was logged %d times, want once", n)
	}
	if !strings.Contains(out, "the far end is answering again") {
		t.Error("the recovery was not logged")
	}
	if n := strings.Count(out, "cannot write to the far end"); n > 1 {
		t.Errorf("failed keepalives were logged %d times during one outage, want at most once", n)
	}
}

// TestClosingALinkStillStopsIt: the loop that now survives a failed read must
// still end when QSP closes the link, or shutdown hangs on it.
//
// To see it bite: drop the net.ErrClosed test and the running check, and Close
// is followed by a loop reading a closed socket once a second forever.
func TestClosingALinkStillStopsIt(t *testing.T) {
	master := newFakeMaster(t)
	logs := &syncBuffer{}
	hb, err := homebrew.New(homebrew.Config{Name: "far", RepeaterID: linkID,
		Password: []byte("secret"), Identity: homebrew.Identity{Callsign: "K9MLS"}})
	if err != nil {
		t.Fatal(err)
	}
	l, err := upstream.NewPeer(slog.New(slog.NewTextHandler(logs, nil)), upstream.PeerConfig{
		Name: "far", TargetAddress: master.address(), Link: hb,
		Receive: func(string, hbp.Data) {}, TickInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a connection", func() bool { return l.State() == homebrew.StateConnected })
	_ = l.Close()
	waitFor(t, "the loop to end", func() bool { return strings.Contains(logs.String(), "outbound link closed") })
	time.Sleep(upstream.ReadRetryDelay + 200*time.Millisecond)
	if strings.Contains(logs.String(), "not answering") {
		t.Error("a closed link treated its own closed socket as a far end that went away")
	}
}
