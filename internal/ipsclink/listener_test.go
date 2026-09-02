package ipsclink_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

const (
	masterID = 3132911
	peerID   = 3132910
)

// start brings up a listener on a port the operating system chooses, so tests
// never collide with each other or with a real one.
func start(t *testing.T, cfg ipsclink.Config) (*ipsclink.Listener, *net.UDPConn) {
	t.Helper()
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1:0"
	}
	if cfg.MasterID == 0 {
		cfg.MasterID = masterID
	}
	l, err := ipsclink.New(logging.Discard(), cfg)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	addr, err := net.ResolveUDPAddr("udp", l.Address())
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return l, conn
}

func send(t *testing.T, conn *net.UDPConn, kind ipsc.Kind, sender uint32, body []byte) {
	t.Helper()
	m := ipsc.Message{Kind: kind, SenderID: sender, Body: body}
	if _, err := conn.Write(m.Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func expectReply(t *testing.T, conn *net.UDPConn, want ipsc.Kind) ipsc.Message {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("expected a %#02x reply: %v", byte(want), err)
	}
	msg, err := ipsc.Parse(buf[:n])
	if err != nil {
		t.Fatalf("reply did not parse: %v", err)
	}
	if msg.Kind != want {
		t.Fatalf("reply was %#02x, want %#02x", byte(msg.Kind), byte(want))
	}
	return msg
}

func waitForPeers(t *testing.T, l *ipsclink.Listener, n int) []ipsclink.Peer {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		peers := l.Peers()
		if len(peers) == n {
			return peers
		}
		if time.Now().After(deadline) {
			t.Fatalf("wanted %d peer(s), have %d", n, len(peers))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func registerBody() []byte {
	b, _ := ipsc.CapturedBody(ipsc.KindKeepaliveReply)
	return b
}

// TestAPeerRegistersAndKeepalivesAreAnswered is the whole point of the
// listener, exercised over a real socket.
func TestAPeerRegistersAndKeepalivesAreAnswered(t *testing.T) {
	l, conn := start(t, ipsclink.Config{})

	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	reply := expectReply(t, conn, ipsc.KindRegisterReply)
	if reply.SenderID != masterID {
		t.Errorf("reply announces %d, want the master's %d", reply.SenderID, masterID)
	}

	send(t, conn, ipsc.KindKeepaliveRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindKeepaliveReply)

	peers := waitForPeers(t, l, 1)
	if peers[0].RadioID != peerID {
		t.Errorf("peer %d, want %d", peers[0].RadioID, peerID)
	}
	if peers[0].Registered.IsZero() {
		t.Error("peer has no registration time")
	}
}

// TestAMasterIDEqualToAPeersIsRefusedAtConfiguration turns six wasted minutes
// into a configuration error.
//
// An XPR8300 pointed at a master announcing the repeater's own ID retried
// thirty-nine times and never registered, with correct replies sent promptly
// and ignored. The failure is indistinguishable from a protocol fault, so it is
// caught where it can be explained.
func TestAMasterIDEqualToAPeersIsRefusedAtConfiguration(t *testing.T) {
	_, err := ipsclink.New(logging.Discard(), ipsclink.Config{
		ListenAddress: "127.0.0.1:0",
		MasterID:      peerID,
		AllowedPeers:  []uint32{peerID},
	})
	if err == nil {
		t.Fatal("a master ID equal to a peer's was accepted")
	}
}

// TestAPeerNotOnTheAllowListIsMetWithSilence records the only refusal that has
// been observed to exist.
//
// ICMP port unreachable is provably ignored by a Motorola repeater — a kernel
// refused every request eighty microseconds later and the cadence did not
// change — and no capture contains an IPSC-level rejection. Silence is
// therefore the whole vocabulary QSP has for "no".
func TestAPeerNotOnTheAllowListIsMetWithSilence(t *testing.T) {
	l, conn := start(t, ipsclink.Config{AllowedPeers: []uint32{999999}})

	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := conn.Read(make([]byte, 2048)); err == nil {
		t.Fatalf("a peer not on the allow list got a %d byte reply", n)
	}
	if len(l.Peers()) != 0 {
		t.Error("a peer not on the allow list was recorded")
	}
	if ignored, _ := l.Counters(); ignored == 0 {
		t.Error("the ignored counter did not move; a silently dropped peer should still be visible")
	}
}

// TestASilentPeerIsDropped covers the absence of a disconnect message.
//
// Nothing in any capture says goodbye. A repeater that is unplugged simply
// stops, so silence past the timeout is the only evidence of departure — and a
// console showing a repeater that left an hour ago is worse than one showing
// none.
func TestASilentPeerIsDropped(t *testing.T) {
	l, conn := start(t, ipsclink.Config{PeerTimeout: time.Hour})

	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)
	waitForPeers(t, l, 1)

	if dropped := l.ExpireAt(time.Now()); dropped != 0 {
		t.Errorf("dropped %d peers that had just been heard from", dropped)
	}
	if dropped := l.ExpireAt(time.Now().Add(2 * time.Hour)); dropped != 1 {
		t.Errorf("dropped %d peers, want 1", dropped)
	}
	if len(l.Peers()) != 0 {
		t.Error("a timed-out peer is still in the snapshot")
	}
}

// TestVoiceIsTrackedAsACallWithABeginningAndAnEnd is what puts a transmission
// in front of an operator.
func TestVoiceIsTrackedAsACallWithABeginningAndAnEnd(t *testing.T) {
	l, conn := start(t, ipsclink.Config{})
	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	for i, flags := range []uint16{0x80dd, 0x805d, 0x805e} {
		send(t, conn, ipsc.KindVoice, peerID, voiceBody(uint16(0x3360), flags, uint16(i)))
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		peers := l.Peers()
		if len(peers) == 1 && peers[0].LastCall != nil && !peers[0].LastCall.Ended.IsZero() {
			c := peers[0].LastCall
			if c.Frames != 3 {
				t.Errorf("call recorded %d frames, want 3", c.Frames)
			}
			if c.Source != peerID {
				t.Errorf("call source %d, want %d", c.Source, peerID)
			}
			if c.StreamID != 0x3360 {
				t.Errorf("call stream %#04x, want 0x3360", c.StreamID)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no completed call was recorded")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// voiceBody builds the body of a voice frame in the captured layout.
//
// Only the fields the listener reads are meaningful; the rest is the shape the
// XPR8300 sent so that the offsets are exercised as they appear on the wire.
func voiceBody(stream, flags, seq uint16) []byte {
	b := make([]byte, 47)
	b[0] = 1                            // call counter
	b[1], b[2], b[3] = 0x2f, 0xcd, 0xee // 24-bit source
	b[4], b[5], b[6] = 0x00, 0x01, 0xc7 // 24-bit destination
	b[10], b[11] = byte(stream>>8), byte(stream)
	b[13], b[14] = byte(flags>>8), byte(flags)
	b[15], b[16] = byte(seq>>8), byte(seq)
	b[25] = 0x8a // frame class: voice
	b[26] = 20   // length of everything from here on
	b[27] = 0x40 // payload class
	return b
}

// TestARefusalIsReportedWithItsRadioID is the defect 2026-09-02 exposed.
//
// The health page said 2144 datagrams came from radio IDs not on the allow
// list and named none of them. The answer was a member's repeater — KB9TYC's,
// radio ID 3155412 — retrying every ten seconds for hours, and finding that out
// took a journal search. A count without a subject is not actionable.
func TestARefusalIsReportedWithItsRadioID(t *testing.T) {
	l, conn := start(t, ipsclink.Config{AllowedPeers: []uint32{peerID}})

	if _, ok := l.LastRefused(time.Now()); ok {
		t.Fatal("a listener that has refused nothing reports a refusal")
	}

	// A repeater that is not on the list knocks, exactly as an unregistered
	// peer does every ten seconds.
	send(t, conn, ipsc.KindRegisterRequest, 3155412, make([]byte, 11))

	deadline := time.Now().Add(2 * time.Second)
	var id uint32
	var ok bool
	for time.Now().Before(deadline) {
		if id, ok = l.LastRefused(time.Now()); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !ok {
		t.Fatal("a refused peer was not reported")
	}
	if id != 3155412 {
		t.Errorf("the refusal names radio ID %d, want 3155412", id)
	}
}

// TestARefusalStopsBeingReported keeps the status able to recover.
//
// A lifetime total never falls, so a subsystem that once turned something away
// reads degraded until the process restarts — and a status that cannot recover
// is a status an operator stops reading.
func TestARefusalStopsBeingReported(t *testing.T) {
	l, conn := start(t, ipsclink.Config{AllowedPeers: []uint32{peerID}})

	send(t, conn, ipsc.KindRegisterRequest, 3155412, make([]byte, 11))
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := l.LastRefused(time.Now()); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := l.LastRefused(time.Now()); !ok {
		t.Fatal("the refusal was never recorded, so recovery cannot be tested")
	}

	// Well past the window, the same listener no longer reports it.
	later := time.Now().Add(ipsclink.RefusalWindow + time.Second)
	if _, ok := l.LastRefused(later); ok {
		t.Error("a refusal from over a minute ago is still reported; " +
			"the status cannot recover and will be ignored")
	}
}
