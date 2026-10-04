package v24link

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/p25calls"
	"github.com/k9mls/qsp/internal/p25link"
	"github.com/k9mls/qsp/internal/protocol/p25"
)

func contextWithCancel(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithCancel(context.Background())
}

func finishedCalls(tr *p25calls.Tracker) []p25calls.Call {
	_, recent := tr.Snapshot()
	return recent
}

// The three captured transmissions, as Last heard keeps them.
//
// Break it: report a call to the tracker more than once, name the repeater
// before it has said what it is, or lose the talkgroup, and this fails.
func TestTheCapturedCallsReachLastHeard(t *testing.T) {
	tr := p25calls.NewTracker(p25calls.Options{})
	l, _ := start(t, Config{Calls: tr})
	c := dial(t, l)
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(c) }()
	if _, err := c.Write(readFile(t, voiceFixture)); err != nil {
		t.Fatalf("write: %v", err)
	}
	waitFor(t, "three calls in Last heard", func() bool { return len(finishedCalls(tr)) == 3 })

	active, recent := tr.Snapshot()
	if len(active) != 0 {
		t.Errorf("%d calls still in progress", len(active))
	}
	// Newest first: the capture's calls were 135, 81 and 297 voice frames.
	for i, frames := range []int{297, 81, 135} {
		c := recent[i]
		want := p25calls.Call{Source: 8080303, Talkgroup: 1, Frames: frames,
			ViaKind: p25calls.ViaRepeater, Via: "Quantar, site 1", Carried: true, EndReason: p25calls.EndMarked}
		if c.Started.IsZero() || !c.Ended.After(c.Started) {
			t.Errorf("call %d ran from %v to %v", i, c.Started, c.Ended)
		}
		c.Started, c.Ended = time.Time{}, time.Time{}
		if c != want {
			t.Errorf("call %d is %+v, want %+v", i, c, want)
		}
	}
}

// How a repeater's call is recorded for each way it can go.
//
// Break it: record a held call as carried, record a call cut off by its link
// as ended normally, or leave a call in progress when its tunnel closes, and
// a row fails.
func TestHowARepeatersCallIsRecorded(t *testing.T) {
	tests := []struct {
		name    string
		held    bool
		send    []byte
		close   bool
		wait    bool // for the quiet timer
		carried bool
		reason  p25calls.EndReason
		frames  int
	}{
		{"closed by the repeater", false, join(callStart, voiceRecord(1), voiceRecord(2), callEnd, callEnd),
			false, false, true, p25calls.EndMarked, 2},
		{"heard while another station was talking", true, join(callStart, voiceRecord(1), callEnd, callEnd),
			false, false, false, p25calls.EndMarked, 1},
		{"it went quiet", false, join(callStart, voiceRecord(1)), false, true, true, p25calls.EndQuiet, 1},
		{"its tunnel closed", false, join(callStart, voiceRecord(1)), true, false, true, p25calls.EndLinkClosed, 1},
		{"a start and an end with no voice between", false, join(callStart, callEnd, callEnd),
			false, false, true, p25calls.EndMarked, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := p25calls.NewTracker(p25calls.Options{})
			floor := &p25link.Floor{}
			l, _ := start(t, Config{Keepalive: time.Hour, Request: 50 * time.Millisecond, Calls: tr, Floor: floor})
			c := link(t, l, 1)
			waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })
			go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(c) }()
			if tc.held {
				floor.Take(p25link.GatewayFloor, time.Now())
			}
			_, _ = c.Write(tc.send)
			if tc.close || tc.wait {
				waitFor(t, "the call in progress", func() bool { a, _ := tr.Snapshot(); return len(a) == 1 })
			}
			if tc.close {
				_ = c.Close()
			}
			waitFor(t, "the call in Last heard", func() bool { return len(finishedCalls(tr)) == 1 })

			got := finishedCalls(tr)[0]
			if got.Carried != tc.carried || got.EndReason != tc.reason || got.Frames != tc.frames {
				t.Errorf("carried %v, ended %q, %d frames", got.Carried, got.EndReason, got.Frames)
			}
			if got.Via != "Quantar, site 1" || got.ViaKind != p25calls.ViaRepeater {
				t.Errorf("came in through %q (%s)", got.Via, got.ViaKind)
			}
			if a, _ := tr.Snapshot(); len(a) != 0 {
				t.Errorf("%d calls left in progress", len(a))
			}
		})
	}
}

// A call in progress is in Last heard while it is being heard, with who is
// talking once the voice has said so.
//
// Break it: report only finished calls, and Last heard shows nobody
// transmitting until they have stopped.
func TestACallInProgressIsShownWhileItRuns(t *testing.T) {
	tr := p25calls.NewTracker(p25calls.Options{})
	l, _ := start(t, Config{Keepalive: time.Hour, Calls: tr})
	c := link(t, l, 1)
	waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })

	record := func(kind p25.Kind, lc ...byte) []byte {
		lengths := map[p25.Kind]int{p25.KindVoice1: 22, p25.KindVoice3: 17, p25.KindVoice4: 17, p25.KindVoice5: 17}
		raw := make([]byte, lengths[kind])
		raw[0] = byte(kind)
		copy(raw[1:], lc)
		return tunnel(append([]byte{0x07, 0x03}, raw...))
	}
	_, _ = c.Write(join(callStart,
		record(p25.KindVoice1),
		record(p25.KindVoice3, 0x00, 0x00, 0x04),
		record(p25.KindVoice4, 0x00, 0x00, 0x01),
		record(p25.KindVoice5, 0x7B, 0x4B, 0xAF)))
	waitFor(t, "the talker named while still talking", func() bool {
		a, _ := tr.Snapshot()
		return len(a) == 1 && a[0].Source == 8080303 && a[0].Talkgroup == 1 && a[0].Frames == 4 && a[0].Carried
	})
	if len(finishedCalls(tr)) != 0 {
		t.Error("a call still running is in the finished list")
	}
}

// **One event, one record.** A call that crosses between a repeater and a
// gateway is recorded where it came into QSP and nowhere else: the mistake
// ADR-0059 records IPSC making, each transmission drawn twice.
//
// Break it: have the listener a call is relayed to record it as well, and
// either row finds two calls where one was made.
func TestACallRelayedBetweenTheTwoIsRecordedOnce(t *testing.T) {
	voice := append([]byte{0x63}, bytes.Repeat([]byte{7}, 13)...)
	terminator := append([]byte{0x80}, make([]byte, 16)...)

	tests := []struct {
		name string
		key  func(t *testing.T, repeater net.Conn, gateway *net.UDPConn)
		via  string
		kind string
	}{
		{"keyed on the repeater", func(t *testing.T, repeater net.Conn, _ *net.UDPConn) {
			_, _ = repeater.Write(join(callStart, tunnel(append([]byte{0x07, 0x03}, voice...)), callEnd, callEnd))
		}, "Quantar, site 1", p25calls.ViaRepeater},
		{"keyed on the gateway", func(t *testing.T, _ net.Conn, gateway *net.UDPConn) {
			_, _ = gateway.Write(voice)
			_, _ = gateway.Write(terminator)
		}, "N0CALL", p25calls.ViaGateway},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := p25calls.NewTracker(p25calls.Options{})
			floor := &p25link.Floor{}

			// The repeater link first, as the daemon builds it; the gateway
			// listener is handed it, and reached back through a late binding.
			var gateways *p25link.Listener
			repeaters, _ := start(t, Config{Keepalive: time.Hour, Calls: tr, Floor: floor,
				Gateways: lateGateways{&gateways}})

			probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatalf("finding a port: %v", err)
			}
			addr := probe.LocalAddr().(*net.UDPAddr)
			_ = probe.Close()
			gateways, err = p25link.New(logging.Discard(), p25link.Config{
				ListenAddress: addr.String(), Callsign: "K9MLS", Calls: tr, Floor: floor, Repeaters: repeaters})
			if err != nil {
				t.Fatalf("p25link: %v", err)
			}
			ctx, cancel := contextWithCancel(t)
			defer cancel()
			if err := gateways.Start(ctx); err != nil {
				t.Fatalf("starting the gateway listener: %v", err)
			}

			repeater := link(t, repeaters, 1)
			waitFor(t, "the link up", func() bool { return repeaters.LinksUp() == 1 })
			go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(repeater) }()

			gateway, err := net.DialUDP("udp", nil, addr)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer gateway.Close()
			_, _ = gateway.Write(p25.NewPoll("N0CALL").Marshal())
			buf := make([]byte, 64)
			_ = gateway.SetReadDeadline(time.Now().Add(2 * time.Second))
			if _, err := gateway.Read(buf); err != nil {
				t.Fatalf("the poll went unanswered: %v", err)
			}

			tc.key(t, repeater, gateway)
			waitFor(t, "the call in Last heard", func() bool { return len(finishedCalls(tr)) >= 1 })
			// Long enough for a second record to arrive, if one were coming.
			time.Sleep(150 * time.Millisecond)

			active, recent := tr.Snapshot()
			if len(recent) != 1 || len(active) != 0 {
				t.Fatalf("%d finished and %d in progress, want one call", len(recent), len(active))
			}
			if recent[0].Via != tc.via || recent[0].ViaKind != tc.kind || !recent[0].Carried {
				t.Errorf("recorded as %+v", recent[0])
			}
		})
	}
}

// lateGateways is the gateway listener, looked up when a call arrives, as the
// daemon's own binding does.
type lateGateways struct{ l **p25link.Listener }

func (g lateGateways) FromRepeater(frame []byte) int {
	if *g.l == nil {
		return 0
	}
	return (*g.l).FromRepeater(frame)
}

func (g lateGateways) EndFromRepeater() int {
	if *g.l == nil {
		return 0
	}
	return (*g.l).EndFromRepeater()
}
