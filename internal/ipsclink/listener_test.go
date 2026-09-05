package ipsclink_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"

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
	return startLogging(t, cfg, logging.Discard())
}

// startLogging is start with a journal the test can read.
//
// Some of what this listener does is only visible in the journal: a superseded
// transmission is ended and then immediately replaced, so no snapshot ever
// shows it, and the log line is the whole of the evidence that it ended at all.
func startLogging(t *testing.T, cfg ipsclink.Config, log *slog.Logger) (*ipsclink.Listener, *net.UDPConn) {
	t.Helper()
	if cfg.ListenAddress == "" {
		cfg.ListenAddress = "127.0.0.1:0"
	}
	if cfg.MasterID == 0 {
		cfg.MasterID = masterID
	}
	l, err := ipsclink.New(log, cfg)
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

// syncBuffer is a journal a test can read while the listener writes it.
//
// The listener logs from its own goroutine and the race detector is a blocking
// gate, so a bare bytes.Buffer would fail the gate rather than the assertion.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// waitForCall returns the peer's current call once it has counted frames.
func waitForCall(t *testing.T, l *ipsclink.Listener, frames uint64) *ipsclink.Call {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		peers := l.Peers()
		if len(peers) == 1 && peers[0].LastCall != nil && peers[0].LastCall.Frames == frames {
			return peers[0].LastCall
		}
		if time.Now().After(deadline) {
			t.Fatalf("no call with %d frames was recorded", frames)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitForJournal fails unless the journal comes to contain a phrase.
func waitForJournal(t *testing.T, journal *syncBuffer, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if strings.Contains(journal.String(), want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("journal never said %q; it said:\n%s", want, journal.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// voiceFrames sends a transmission that stops without its terminator.
func voiceFrames(t *testing.T, conn *net.UDPConn, stream uint16, n int) {
	t.Helper()
	flags := uint16(0x80dd)
	for i := 0; i < n; i++ {
		if i > 0 {
			flags = 0x805d
		}
		send(t, conn, ipsc.KindVoice, peerID, voiceBody(stream, flags, uint16(i)))
	}
}

// TestATransmissionThatStopsWithoutATerminatorIsClosed is the defect that put
// seven hours in front of an operator.
//
// A call ended on its last-frame flag and on nothing else. A peer that keeps
// keepaliving is never dropped, so a transmission whose terminator never
// arrived stayed open for as long as the repeater stayed up: on 2026-09-05 the
// console reported one running for 7h14m18s, in 45 frames, and counted the
// station as transmitting.
func TestATransmissionThatStopsWithoutATerminatorIsClosed(t *testing.T) {
	l, conn := start(t, ipsclink.Config{})
	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	voiceFrames(t, conn, 0x3360, 2)
	waitForCall(t, l, 2)

	if closed := l.ExpireCallsAt(time.Now()); closed != 0 {
		t.Errorf("closed %d transmissions that had just been heard", closed)
	}
	if c := l.Peers()[0].LastCall; !c.Ended.IsZero() {
		t.Error("a transmission still arriving was closed")
	}

	sweep := time.Now().Add(2 * ipsclink.CallTimeout)
	if closed := l.ExpireCallsAt(sweep); closed != 1 {
		t.Fatalf("closed %d transmissions, want 1", closed)
	}

	c := l.Peers()[0].LastCall
	if c.Ended.IsZero() {
		t.Fatal("the transmission is still open after the sweep")
	}
	if !c.Lost {
		t.Error("a transmission that ended without a terminator is not marked lost")
	}
	// **It ended when its last frame arrived, not when the sweep noticed.**
	// Recording the sweep time would stretch every lost transmission by up to
	// the timeout, which is the number an operator reads.
	if got := c.Ended.Sub(c.Started); got >= ipsclink.CallTimeout {
		t.Errorf("a two-frame transmission is recorded as %s long", got)
	}
	if !c.Ended.Before(sweep) {
		t.Error("the transmission is recorded as ending at the sweep")
	}
}

// TestATransmissionIsNotReportedForLongerThanARadioCanTransmit is the second
// mechanism, and it is meant never to fire.
//
// Every radio on an amateur network has a time-out timer — 180 seconds on this
// one — so frames still arriving after four minutes are a stuck record rather
// than a long over. It is keyed on the start time where the silence timeout is
// keyed on the last frame, so the two cannot fail together.
func TestATransmissionIsNotReportedForLongerThanARadioCanTransmit(t *testing.T) {
	l, conn := start(t, ipsclink.Config{})
	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	voiceFrames(t, conn, 0x3360, 2)
	waitForCall(t, l, 2)

	sweep := time.Now().Add(ipsclink.MaxCallDuration + time.Second)
	if closed := l.ExpireCallsAt(sweep); closed != 1 {
		t.Fatalf("closed %d transmissions, want 1", closed)
	}
	c := l.Peers()[0].LastCall
	// **The end time is what says which mechanism fired.** The silence timeout
	// ends a transmission at its last frame; the ceiling ends one at the sweep,
	// because frames may still be arriving.
	if !c.Ended.Equal(sweep) {
		t.Errorf("the transmission ended at %s, want the sweep at %s — the "+
			"silence timeout closed it and the ceiling was never exercised",
			c.Ended, sweep)
	}
	// The console reports an open transmission as running for now minus its
	// start, so an open record past the ceiling is the seven-hour row.
	if c.Ended.IsZero() {
		t.Error("a transmission past the ceiling is still reported as running")
	}
}

// TestASupersededTransmissionIsEndedRatherThanOverwritten is the same defect in
// its quieter form.
//
// A new stream ID replaced the record outright, so a transmission whose
// terminator never arrived left a `call started` with no `call ended` anywhere
// and vanished with nothing said about it. Two are in the journal for
// 2026-09-04.
func TestASupersededTransmissionIsEndedRatherThanOverwritten(t *testing.T) {
	journal := &syncBuffer{}
	l, conn := startLogging(t, ipsclink.Config{},
		logging.New(journal, logging.Options{Level: slog.LevelInfo, Format: logging.FormatJSON}))
	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	voiceFrames(t, conn, 0x3360, 2)
	waitForCall(t, l, 2)
	voiceFrames(t, conn, 0x4471, 1)
	waitForCall(t, l, 1)

	waitForJournal(t, journal, "call ended without a terminator")
	if c := l.Peers()[0].LastCall; c.StreamID != 0x4471 {
		t.Errorf("the current transmission is stream %#04x, want 0x4471", c.StreamID)
	}
}

// voiceBodies returns the bodies of a real transmission's frames, ready to
// re-send at a listener as if the repeater had.
func voiceBodies(tb testing.TB, path string, stream uint16) [][]byte {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out [][]byte
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 40 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		pl := udp[8:int(binary.BigEndian.Uint16(udp[4:6]))]
		msg, err := ipsc.Parse(pl)
		if err != nil || !msg.Kind.IsVoice() {
			continue
		}
		v, ok := msg.AsVoice()
		if !ok || v.StreamID != stream {
			continue
		}
		out = append(out, append([]byte(nil), msg.Body...))
	}
	if len(out) == 0 {
		tb.Fatalf("the fixture holds no transmission with stream %#04x", stream)
	}
	return out
}

// TestATransmissionIsCountedAtEveryLayerItCrosses is the diagnostic a real gap
// went unexplained for want of.
//
// The console reported an IPSC transmission of 45 frames while the DMR side
// recorded 22 for the same stream in the same second, and **neither number said
// where the other 23 went**. Conversion is one for one — measured against
// ipsc-private-voice.pcap across five transmissions and two repeater models —
// so a repeat of that gap is now localised by reading one line instead of by
// theorising.
func TestATransmissionIsCountedAtEveryLayerItCrosses(t *testing.T) {
	const privateVoice = "../../testdata/ipsc/ipsc-private-voice.pcap"

	var mu sync.Mutex
	var delivered int
	l, conn := start(t, ipsclink.Config{
		Bridge: ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true},
		Deliver: func(_ hbp.RepeaterID, _ hbp.Data) {
			mu.Lock()
			delivered++
			mu.Unlock()
		},
	})
	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	bodies := voiceBodies(t, privateVoice, 0x310d)
	for _, body := range bodies {
		send(t, conn, ipsc.KindVoice, peerID, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		peers := l.Peers()
		if len(peers) == 1 && peers[0].LastCall != nil &&
			peers[0].LastCall.Frames == uint64(len(bodies)) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the listener did not count all %d frames", len(bodies))
		}
		time.Sleep(5 * time.Millisecond)
	}

	c := l.Peers()[0].LastCall
	if c.Converted == 0 {
		t.Fatal("a transmission that crossed the bridge converted nothing")
	}
	// **Delivery is what the counter is for.** It is incremented outside the
	// peer lock, after the frames have gone, which is the whole reason the end
	// of a transmission is logged from noteDelivery rather than from the frame
	// that carried the terminator.
	if c.Delivered != c.Converted {
		t.Errorf("converted %d frames and delivered %d", c.Converted, c.Delivered)
	}
	mu.Lock()
	got := delivered
	mu.Unlock()
	if uint64(got) != c.Delivered {
		t.Errorf("the DMR side received %d frames and the counter says %d", got, c.Delivered)
	}
	// Conversion is one for one on audio: this fixture's transmission is 88
	// frames, of which 4 are headers and terminators, and it comes out as 84
	// bursts with a header and a terminator of QSP's own.
	if c.Converted != 86 {
		t.Errorf("%d frames converted from %d received, want 86", c.Converted, len(bodies))
	}
}
