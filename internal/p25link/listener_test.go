package p25link_test

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/p25link"
	"github.com/k9mls/qsp/internal/protocol/p25"
)

// A real socket on the loopback, because the thing being tested is a UDP
// server. The IPSC listener's tests took the same view: a fake connection would
// test the handler and leave the part that has never worked untested.
func serve(t *testing.T, cfg p25link.Config) (*p25link.Listener, string, func()) {
	t.Helper()

	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1:0"
	}
	if cfg.Callsign == "" {
		cfg.Callsign = "K9MLS"
	}

	// Port zero, then read back what was bound: a fixed port makes tests fail
	// when something else on the machine holds it.
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("finding a free port: %v", err)
	}
	addr := probe.LocalAddr().String()
	_ = probe.Close()
	cfg.ListenAddress = addr

	l, err := p25link.New(logging.Discard(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := l.Start(ctx); err != nil {
			t.Errorf("Start: %v", err)
		}
	}()

	for i := 0; i < 200 && !l.Running(); i++ {
		time.Sleep(time.Millisecond)
	}
	if !l.Running() {
		cancel()
		t.Fatal("the listener never started")
	}
	return l, addr, func() { cancel(); <-done }
}

func dial(t *testing.T, addr string) *net.UDPConn {
	t.Helper()

	remote, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	c, err := net.DialUDP("udp", nil, remote)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	return c
}

// **The poll is the registration**, and the reply is the identical datagram.
// `p25-register.pcap` shows 96 polls out and 96 back, byte for byte, with no
// login of any kind — which is the finding this whole listener rests on, and
// the one that would have been most expensive to guess at.
func TestAPollIsAnsweredWithTheIdenticalDatagram(t *testing.T) {
	l, addr, stop := serve(t, p25link.Config{})
	defer stop()

	c := dial(t, addr)
	sent := p25.NewPoll("KD9EJA").Marshal()
	if _, err := c.Write(sent); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("no answer to a poll: %v", err)
	}
	if got := buf[:n]; string(got) != string(sent) {
		t.Errorf("the answer differs from the poll:\n sent %x\n got  %x", sent, got)
	}

	gws := l.Gateways()
	if len(gws) != 1 {
		t.Fatalf("%d gateways registered, want 1", len(gws))
	}
	if gws[0].Callsign != "KD9EJA" {
		t.Errorf("registered %q, want KD9EJA", gws[0].Callsign)
	}
}

// **A gateway's own padding comes back.** QSP does not rewrite what it carries,
// and this is the same rule the parser follows — a poll padded with NUL rather
// than space is answered as it arrived, which is the bug the fuzzer found in
// the parser and would be just as wrong here.
func TestAPollIsAnsweredWithItsOwnPadding(t *testing.T) {
	_, addr, stop := serve(t, p25link.Config{})
	defer stop()

	c := dial(t, addr)
	// NUL padded rather than space padded.
	sent := append([]byte{0xF0}, []byte("KD9EJA\x00\x00\x00\x00")...)
	if _, err := c.Write(sent); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 64)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("no answer: %v", err)
	}
	if got := buf[:n]; string(got) != string(sent) {
		t.Errorf("the padding was rewritten:\n sent %x\n got  %x", sent, got)
	}
}

// **A refused gateway is not answered at all.** One that received a reply would
// believe it had registered and sit there sending voice nobody carries — which
// looks like a QSP fault from its end.
func TestARefusedGatewayGetsNoReply(t *testing.T) {
	l, addr, stop := serve(t, p25link.Config{AllowedCallsigns: []string{"K9MLS"}})
	defer stop()

	c := dial(t, addr)
	_ = c.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := c.Write(p25.NewPoll("NOTALLOWED").Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}

	buf := make([]byte, 64)
	if n, err := c.Read(buf); err == nil {
		t.Fatalf("a refused gateway was answered with %d bytes", n)
	}
	if len(l.Gateways()) != 0 {
		t.Error("a refused gateway was registered")
	}

	count, who := l.Refused()
	if count == 0 {
		t.Error("the refusal was not counted")
	}
	// A lifetime counter cannot answer the question an operator has; the IPSC
	// listener learned that on 2026-09-02 from 2144 refusals naming none.
	if who != "NOTALLOWED" {
		t.Errorf("the most recent refusal is %q, want NOTALLOWED", who)
	}
}

// Case must not decide admission. 0301 shipped this defect one layer up — a
// link whose name was not lowercase could not be sent to — and refusing
// somebody for their shift key would be the same mistake in a new place.
func TestAnAllowListIgnoresCase(t *testing.T) {
	l, addr, stop := serve(t, p25link.Config{AllowedCallsigns: []string{"kd9eja"}})
	defer stop()

	c := dial(t, addr)
	if _, err := c.Write(p25.NewPoll("KD9EJA").Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		t.Fatalf("a gateway was refused for its case: %v", err)
	}
	if len(l.Gateways()) != 1 {
		t.Error("the gateway did not register")
	}
}

// **Voice is relayed to every other gateway, byte for byte.** ADR-0034 says a
// P25 call crosses QSP without a vocoder, and this is that decision arriving at
// a socket: what goes out is what came in.
func TestVoiceIsRelayedVerbatimToOtherGateways(t *testing.T) {
	_, addr, stop := serve(t, p25link.Config{})
	defer stop()

	a := dial(t, addr)
	b := dial(t, addr)

	for _, c := range []*net.UDPConn{a, b} {
		if _, err := c.Write(p25.NewPoll("GW" + c.LocalAddr().String()[len(c.LocalAddr().String())-4:]).Marshal()); err != nil {
			t.Fatalf("write: %v", err)
		}
		buf := make([]byte, 64)
		if _, err := c.Read(buf); err != nil {
			t.Fatalf("a poll went unanswered: %v", err)
		}
	}

	// A real frame from the capture, with a real talkgroup in it.
	frame := voiceFrame(t)
	if _, err := a.Write(frame); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = b.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := b.Read(buf)
	if err != nil {
		t.Fatalf("the frame was not relayed: %v", err)
	}
	if got := buf[:n]; string(got) != string(frame) {
		t.Errorf("the frame changed in relay:\n in  %x\n out %x", frame, got)
	}

	// And not echoed back to the sender, which would be a loop.
	_ = a.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := a.Read(buf); err == nil {
		t.Errorf("the sender received its own frame back: %d bytes", n)
	}
}

// **Voice from a gateway that has not polled is not carried.** QSP does not
// know who it is, and relaying it would put audio from an unidentified source
// onto somebody's repeater.
func TestVoiceFromAnUnregisteredGatewayIsNotCarried(t *testing.T) {
	_, addr, stop := serve(t, p25link.Config{})
	defer stop()

	listenerSide := dial(t, addr)
	if _, err := listenerSide.Write(p25.NewPoll("KD9EJA").Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := listenerSide.Read(buf); err != nil {
		t.Fatalf("poll unanswered: %v", err)
	}

	// A second socket that never polled.
	stranger := dial(t, addr)
	if _, err := stranger.Write(voiceFrame(t)); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = listenerSide.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := listenerSide.Read(buf); err == nil {
		t.Errorf("voice from an unregistered gateway was relayed: %d bytes", n)
	}
}

// The talkgroup and the radio reach the console, which is what an operator
// reads in Last heard.
func TestTheTalkgroupAndRadioAreRecorded(t *testing.T) {
	l, addr, stop := serve(t, p25link.Config{})
	defer stop()

	c := dial(t, addr)
	if _, err := c.Write(p25.NewPoll("KD9EJA").Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		t.Fatalf("poll unanswered: %v", err)
	}

	for _, f := range linkControlFrames(t) {
		if _, err := c.Write(f); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// The relay is synchronous with the read, so a short wait is enough for
	// the snapshot to have been republished.
	time.Sleep(50 * time.Millisecond)

	gws := l.Gateways()
	if len(gws) != 1 {
		t.Fatalf("%d gateways, want 1", len(gws))
	}
	if gws[0].Talkgroup == 0 {
		t.Error("no talkgroup was recorded from frames that carry one")
	}
	if gws[0].SourceID != 3132910 {
		t.Errorf("recorded radio %d, want 3132910", gws[0].SourceID)
	}
}

// **A gateway that stops polling is forgotten**, after three missed polls
// rather than one: a single dropped datagram on a UDP path says nothing.
func TestAGatewayThatStopsPollingIsForgotten(t *testing.T) {
	l, addr, stop := serve(t, p25link.Config{})
	defer stop()

	c := dial(t, addr)
	if _, err := c.Write(p25.NewPoll("KD9EJA").Marshal()); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 64)
	if _, err := c.Read(buf); err != nil {
		t.Fatalf("poll unanswered: %v", err)
	}
	if len(l.Gateways()) != 1 {
		t.Fatal("the gateway did not register")
	}

	// One missed poll: still there.
	l.ExpireAt(time.Now().Add(p25link.PollInterval + time.Second))
	if len(l.Gateways()) != 1 {
		t.Error("a gateway was forgotten after one missed poll, which on UDP says nothing")
	}

	// Three missed: gone.
	l.ExpireAt(time.Now().Add(p25link.PollInterval*p25link.MissedPollsBeforeGone + time.Second))
	if len(l.Gateways()) != 0 {
		t.Error("a gateway that stopped polling was never forgotten")
	}
}
