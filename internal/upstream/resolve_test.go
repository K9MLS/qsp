package upstream_test

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/homebrew"
	"github.com/k9mls/qsp/internal/upstream"
)

// scriptedResolver answers each lookup with the next step of a script and
// repeats the last one. A nil step is a lookup that fails.
type scriptedResolver struct {
	mu    sync.Mutex
	steps []*net.UDPAddr
	calls int
}

func (r *scriptedResolver) resolve(string) (*net.UDPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := min(r.calls, len(r.steps)-1)
	r.calls++
	if r.steps[i] == nil {
		return nil, errors.New("no such host")
	}
	return r.steps[i], nil
}

func udpAddr(t *testing.T, s string) *net.UDPAddr {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		t.Fatalf("resolve %s: %v", s, err)
	}
	return a
}

// deadPort is an address nothing listens on.
func deadPort(t *testing.T) *net.UDPAddr {
	t.Helper()
	c, err := net.ListenUDP("udp", udpAddr(t, "127.0.0.1:0"))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	a := c.LocalAddr().(*net.UDPAddr)
	_ = c.Close()
	return a
}

// A link's far end is looked up more than once. Each case is what the name
// resolves to over time; in every one the link must start, and must be
// connected once the name points at the master.
//
// Both halves were broken: a name that did not resolve at boot stopped QSP
// starting, one absent neighbour taking every local repeater with it; and a
// name that moved was dialled at the old address until QSP restarted.
//
// To see it fail: remove the l.maybeRedial() call from serve's ticker, and
// the two cases that do not begin at the master never connect.
func TestAHomebrewLinkFollowsItsFarEndsName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps func(master, dead *net.UDPAddr) []*net.UDPAddr
	}{
		{"resolves at once", func(m, _ *net.UDPAddr) []*net.UDPAddr { return []*net.UDPAddr{m} }},
		{"does not resolve at start, then does", func(m, _ *net.UDPAddr) []*net.UDPAddr { return []*net.UDPAddr{nil, nil, m} }},
		{"moves to a new address", func(m, d *net.UDPAddr) []*net.UDPAddr { return []*net.UDPAddr{d, d, m} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master := newFakeMaster(t)
			r := &scriptedResolver{steps: tc.steps(udpAddr(t, master.address()), deadPort(t))}
			hb, err := homebrew.New(homebrew.Config{
				Name: "far", RepeaterID: linkID, Password: []byte("secret"),
				Identity:   homebrew.Identity{Callsign: "K9MLS"},
				MinBackoff: 20 * time.Millisecond, MaxBackoff: 40 * time.Millisecond, Timeout: 100 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("homebrew.New: %v", err)
			}
			l, err := upstream.NewPeer(logging.Discard(), upstream.PeerConfig{
				Name: "far", TargetAddress: "far.example.org:62031", Link: hb,
				Receive:      func(string, hbp.Data) {},
				TickInterval: 5 * time.Millisecond, Resolve: r.resolve, ResolveInterval: 20 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("NewPeer: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := l.Start(ctx); err != nil {
				t.Fatalf("Start refused a link whose name %s: %v", tc.name, err)
			}
			defer func() { _ = l.Close() }()
			waitFor(t, "the link to connect to the master", func() bool {
				return l.State() == homebrew.StateConnected
			})
		})
	}
}

// The same for an OpenBridge link, which has no handshake: a frame sent once
// the name points at the far end arrives there.
//
// To see it fail: remove `go l.keepResolving(ctx)` from Link.Start.
func TestAnOpenBridgeLinkFollowsItsFarEndsName(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps func(far, dead *net.UDPAddr) []*net.UDPAddr
	}{
		{"resolves at once", func(f, _ *net.UDPAddr) []*net.UDPAddr { return []*net.UDPAddr{f} }},
		{"does not resolve at start, then does", func(f, _ *net.UDPAddr) []*net.UDPAddr { return []*net.UDPAddr{nil, nil, f} }},
		{"moves to a new address", func(f, d *net.UDPAddr) []*net.UDPAddr { return []*net.UDPAddr{d, d, f} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := &received{}
			far, err := upstream.New(logging.Discard(), upstream.Config{
				Name: "far", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1",
				NetworkID: 3129100, Passphrase: []byte(passphrase), Receive: got.add,
			})
			if err != nil {
				t.Fatalf("New far: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := far.Start(ctx); err != nil {
				t.Fatalf("Start far: %v", err)
			}
			defer func() { _ = far.Close() }()

			r := &scriptedResolver{steps: tc.steps(udpAddr(t, far.Address()), deadPort(t))}
			near, err := upstream.New(logging.Discard(), upstream.Config{
				Name: "near", ListenAddress: "127.0.0.1:0", TargetAddress: "far.example.org:62035",
				NetworkID: 3132910, Passphrase: []byte(passphrase), Receive: func(string, hbp.Data) {},
				Resolve: r.resolve, ResolveInterval: 20 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("New near: %v", err)
			}
			if err := near.Start(ctx); err != nil {
				t.Fatalf("Start refused a link whose name %s: %v", tc.name, err)
			}
			defer func() { _ = near.Close() }()

			stream := hbp.StreamID(0x9000)
			waitFor(t, "a frame to arrive at the far end", func() bool {
				stream++
				_ = near.Send(voice(stream, 2))
				return got.count() > 0
			})
		})
	}
}

// A malformed address is still a mistake worth refusing to start over.
func TestAMalformedLinkAddressStillStopsTheStart(t *testing.T) {
	for _, addr := range []string{"far.example.org", "far.example.org:", ":62031", "far.example.org:70000", "far.example.org:dmr"} {
		hb, err := homebrew.New(homebrew.Config{Name: "far", RepeaterID: linkID, Password: []byte("secret"),
			Identity: homebrew.Identity{Callsign: "K9MLS"}})
		if err != nil {
			t.Fatalf("homebrew.New: %v", err)
		}
		l, err := upstream.NewPeer(logging.Discard(), upstream.PeerConfig{
			Name: "far", TargetAddress: addr, Link: hb, Receive: func(string, hbp.Data) {},
		})
		if err != nil {
			t.Fatalf("NewPeer: %v", err)
		}
		if err := l.Start(context.Background()); err == nil {
			_ = l.Close()
			t.Errorf("a link to %q started", addr)
		}
	}
}
