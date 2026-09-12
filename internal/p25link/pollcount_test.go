package p25link

import (
	"net"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/p25"
)

// TestAPollIsNotAVoiceFrame is the defect an operator found by reading a
// number, four hours after the last one of the same shape.
//
// `poll()` and `voice()` both ended in `Received++`, and the health report
// summed that field and called it "frame(s) received". So a reflector with one
// gateway linked and nobody on the air counted twelve received frames a
// minute, for as long as it stayed up.
//
// **Measured before it was fixed**, on the test server on 2026-09-12: 12 in 60
// seconds with the radio untouched, and twenty consecutive datagrams captured
// at one length and a 5.006-second interval. This test is that measurement,
// made cheap enough to keep.
func TestAPollIsNotAVoiceFrame(t *testing.T) {
	l := newTestListener(t)
	from := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 155), Port: 42010}

	for range 12 {
		l.poll(pollDatagram(t, "K9MLS"), from)
	}

	gws := l.Gateways()
	if len(gws) != 1 {
		t.Fatalf("polling registered %d gateways, want 1", len(gws))
	}
	if gws[0].Polls != 12 {
		t.Errorf("12 polls counted as %d polls", gws[0].Polls)
	}
	if gws[0].Frames != 0 {
		t.Errorf("a gateway that has only polled reports %d voice frames; an "+
			"idle reflector must not accumulate audio it never carried",
			gws[0].Frames)
	}
}

// TestTheHealthSummaryReportsVoiceRatherThanPolls covers the sentence an
// operator actually reads.
//
// Splitting the counter is only half the fix: the summary said "frame(s)
// received" and was the thing that misled. It has to say which.
func TestTheHealthSummaryReportsVoiceRatherThanPolls(t *testing.T) {
	l := newTestListener(t)
	from := &net.UDPAddr{IP: net.IPv4(192, 168, 1, 155), Port: 42010}
	for range 12 {
		l.poll(pollDatagram(t, "K9MLS"), from)
	}

	res := HealthCheck{Listener: l}.Check(t.Context())
	if got := res.Detail["frames"]; got != "0" {
		t.Errorf("the health detail reports %q voice frames for a gateway that "+
			"has only polled, want \"0\"", got)
	}
	if got := res.Detail["polls"]; got != "12" {
		t.Errorf("the health detail reports %q polls, want \"12\"; polls must be "+
			"visible rather than merely excluded, or an idle link looks dead", got)
	}
}

// pollDatagram builds a registration poll the way a gateway sends one.
//
// Poll.Marshal is the encoder the frame tests already round-trip against the
// captures, so this is the real wire shape rather than a hand-built byte slice
// that could agree with a wrong reading.
func pollDatagram(t *testing.T, callsign string) []byte {
	t.Helper()
	return p25.Poll{Callsign: callsign}.Marshal()
}

// newTestListener builds a listener that is not serving, with a socket its
// callers do not use.
//
// poll() writes its echo to l.conn, so the tests above set one that discards.
func newTestListener(t *testing.T) *Listener {
	t.Helper()
	l, err := New(logging.Discard(), Config{
		ListenAddress: "127.0.0.1:0",
		Callsign:      "K9MLS",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("test socket: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	l.conn = conn
	// **Running, because the health check's first branch is "not serving".**
	// Without this the report is Degraded with no Detail at all, and the
	// assertions below read empty strings rather than wrong numbers — a test
	// that fails for the wrong reason is one step from a test that passes for
	// the wrong reason.
	l.running.Store(true)
	l.now = func() time.Time { return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC) }
	return l
}
