package audio

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"
)

// loopbackPair opens a Conn on an ephemeral loopback port whose peer is a
// plain UDP socket the test drives, so both directions are real datagrams.
func loopbackPair(t *testing.T) (*Conn, *net.UDPConn) {
	t.Helper()
	far, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatalf("opening the far side: %v", err)
	}
	t.Cleanup(func() { far.Close() })
	c, err := Listen("127.0.0.1:0", far.LocalAddr().String())
	if err != nil {
		t.Fatalf("opening the USRP socket: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, far
}

// TestAUSRPPeerMustNameOneMachine: the source filter compares against the
// peer, so a peer of "every interface" would compare against nothing real.
//
// To see it bite: remove the IsUnspecified test in Listen and the first two
// rows open successfully.
func TestAUSRPPeerMustNameOneMachine(t *testing.T) {
	tests := []struct {
		name, listen, peer string
		wantUnspecified    bool
		wantErr            bool
	}{
		{"an IPv4 wildcard peer", "127.0.0.1:0", "0.0.0.0:32001", true, true},
		{"an IPv6 wildcard peer", "127.0.0.1:0", "[::]:32001", true, true},
		{"a peer with no port", "127.0.0.1:0", "127.0.0.1:0", true, true},
		{"a hostname is not resolved", "127.0.0.1:0", "localhost:32001", false, true},
		{"a malformed listen address", "127.0.0.1", "127.0.0.1:32001", false, true},
		{"a specific peer on a wildcard listen", "0.0.0.0:0", "127.0.0.1:32001", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Listen(tc.listen, tc.peer)
			if c != nil {
				defer c.Close()
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("Listen(%q, %q) error = %v, want error %v", tc.listen, tc.peer, err, tc.wantErr)
			}
			if got := errors.Is(err, ErrUnspecifiedPeer); got != tc.wantUnspecified {
				t.Errorf("refused as unspecified = %v, want %v (%v)", got, tc.wantUnspecified, err)
			}
		})
	}
}

// TestUSRPFromAnywhereButThePeerIsRefusedAndCounted is the socket's only
// protection, as a test.
//
// To see it bite: make Receive return frames without comparing the sender and
// the stranger's frame comes back first.
func TestUSRPFromAnywhereButThePeerIsRefusedAndCounted(t *testing.T) {
	c, far := loopbackPair(t)

	stranger, err := net.ListenUDP("udp", net.UDPAddrFromAddrPort(netip.MustParseAddrPort("127.0.0.1:0")))
	if err != nil {
		t.Fatalf("opening a stranger: %v", err)
	}
	defer stranger.Close()

	keyup, _ := Keyup(1, 2)
	to := net.UDPAddrFromAddrPort(c.LocalAddr())
	if _, err := stranger.WriteToUDP(keyup, to); err != nil {
		t.Fatalf("stranger sending: %v", err)
	}
	if _, err := far.WriteToUDP([]byte("not usrp at all"), to); err != nil {
		t.Fatalf("peer sending junk: %v", err)
	}
	release, _ := Release(2, 2)
	if _, err := far.WriteToUDP(release, to); err != nil {
		t.Fatalf("peer sending: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f, err := c.Receive(ctx)
	if err != nil {
		t.Fatalf("receiving: %v", err)
	}
	if f.PTT || f.Sequence != 2 {
		t.Errorf("received %+v, want the peer's release with sequence 2; the stranger's keyup "+
			"got through the filter", f)
	}
	_, received, foreign, malformed := c.Counters()
	if received != 1 || foreign != 1 || malformed != 1 {
		t.Errorf("counters received=%d foreign=%d malformed=%d, want 1, 1, 1",
			received, foreign, malformed)
	}
}

// TestUSRPReceiveStopsWhenAskedTo: a receive loop that ignores cancellation
// holds shutdown for as long as the far side stays quiet, which is forever.
func TestUSRPReceiveStopsWhenAskedTo(t *testing.T) {
	c, _ := loopbackPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Receive(ctx)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Receive returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Receive did not return after its context was cancelled")
	}
}

// TestUSRPSendReachesThePeerIntact round-trips a voice frame through the
// socket rather than through Encode alone.
func TestUSRPSendReachesThePeerIntact(t *testing.T) {
	c, far := loopbackPair(t)
	samples := make([]int16, SamplesPerFrame)
	for i := range samples {
		samples[i] = int16(i*37 - 3000)
	}
	if err := c.Send(Frame{Sequence: 9, PTT: true, Talkgroup: 2, Samples: samples}); err != nil {
		t.Fatalf("sending: %v", err)
	}
	buf := make([]byte, 1024)
	_ = far.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := far.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("the peer received nothing: %v", err)
	}
	f, ok := Decode(buf[:n])
	if !ok || f.Sequence != 9 || !f.PTT || f.Talkgroup != 2 || len(f.Samples) != SamplesPerFrame {
		t.Fatalf("the peer received %+v (ok %v)", f, ok)
	}
	for i := range samples {
		if f.Samples[i] != samples[i] {
			t.Fatalf("sample %d is %d, sent %d", i, f.Samples[i], samples[i])
		}
	}
	if sent, _, _, _ := c.Counters(); sent != 1 {
		t.Errorf("sent counter %d, want 1", sent)
	}
}
