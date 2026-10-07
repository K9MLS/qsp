package v24link

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
)

var (
	linkRequest = []byte{0x08, 0x31, 0x00, 0x00, 0x00, 0x02, 0x01, 0xFD, 0x3F}
	linkAnswer  = []byte{0x08, 0x31, 0x00, 0x00, 0x00, 0x02, 0x01, 0xFD, 0x73}
	// As a repeater QSP's own request is the station's, byte for byte.
	ourRequest = linkRequest
	// The station accepting it is the same bytes as QSP accepting the
	// station's.
	theirAcceptance = linkAnswer
	ourIntroduction = Frame{Op: OpData, Group: 1,
		Payload: []byte{0xFD, 0xBF, 0x01, 0x05, 0xC2, 0, 0, 0, 0, 0xFF}}.Append(nil)
)

// holdInTests is the hold a listener under test runs with unless the test
// says otherwise. **It is what production runs with.** Until 0.1.328 it was
// none, so nearly every test here drove a path production does not take, and
// a fault in the hold went through all of them (D9). TestMain runs the
// package's tests a second time with no hold when QSP_V24_NO_HOLD is set,
// which scripts/check.sh does.
var holdInTests = 60 * time.Millisecond

func init() {
	if os.Getenv("QSP_V24_NO_HOLD") != "" {
		holdInTests = 0
	}
}

func join(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// tunnel wraps a station's frame as the router sends it.
func tunnel(payload []byte) []byte {
	return Frame{Op: OpData, Group: 1, Payload: payload}.Append(nil)
}

func start(t *testing.T, cfg Config) (*Listener, context.CancelFunc) {
	t.Helper()
	cfg.ListenAddress = "127.0.0.1:0"
	if cfg.Request == 0 {
		cfg.Request = time.Hour // only the tests of the timer want it
	}
	if cfg.Hold == 0 {
		cfg.Hold = holdInTests
	}
	l, err := New(logging.Discard(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := l.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { cancel(); l.Wait() })
	return l, cancel
}

func dial(t *testing.T, l *Listener) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", l.Addr().String(), 2*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// expect reads exactly want, or fails.
func expect(t *testing.T, c net.Conn, want []byte) {
	t.Helper()
	got := make([]byte, len(want))
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(c, got); err != nil {
		t.Fatalf("waiting for % x: %v", want, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

// silent fails if anything arrives.
func silent(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var b [1]byte
	n, err := c.Read(b[:])
	if n != 0 || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected silence, got %d bytes and %v", n, err)
	}
}

func opening(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	return raw[:HeaderLen+30]
}

// What the router sent, in the pieces a network might deliver it in, and what
// comes back for each.
//
// Break it: answer the opening message, echo what is not understood, read one
// frame per network read, or introduce on a link the station alone has
// opened, and a row here fails.
func TestWhatComesBackForWhatArrives(t *testing.T) {
	tests := []struct {
		name   string
		writes [][]byte
		want   []byte // nil is silence
	}{
		{"the opening message is not answered", [][]byte{opening(t)}, nil},
		{"a link request is accepted, and QSP asks in turn",
			[][]byte{linkRequest}, join(linkAnswer, ourRequest)},
		{"a request split across two reads",
			[][]byte{linkRequest[:4], linkRequest[4:]}, join(linkAnswer, ourRequest)},
		{"two requests in one read", [][]byte{join(linkRequest, linkRequest)},
			join(linkAnswer, ourRequest, linkAnswer, ourRequest)},
		{"the opening and a request together",
			[][]byte{join(opening(t), linkRequest)}, join(linkAnswer, ourRequest)},
		{"a keepalive that asks nothing is not answered",
			[][]byte{tunnel([]byte{0xFD, 0x01})}, nil},
		{"a keepalive that demands an answer gets one",
			[][]byte{tunnel([]byte{0xFD, 0x11})}, tunnel([]byte{0xFD, 0x11})},
		{"an introduction on a link nobody has opened is not answered",
			[][]byte{tunnel(stationIntroduction)}, nil},
		{"an introduction on a link only the station has opened is held",
			[][]byte{linkRequest, tunnel(stationIntroduction)}, join(linkAnswer, ourRequest)},
		{"and answered once the station accepts QSP's request",
			[][]byte{linkRequest, tunnel(stationIntroduction), theirAcceptance},
			join(linkAnswer, ourRequest, ourIntroduction)},
		{"the expected order: request, acceptance, introduction",
			[][]byte{linkRequest, theirAcceptance, tunnel(stationIntroduction)},
			join(linkAnswer, ourRequest, ourIntroduction)},
		{"a repeated introduction is answered again",
			[][]byte{linkRequest, theirAcceptance, tunnel(stationIntroduction), tunnel(stationIntroduction)},
			join(linkAnswer, ourRequest, ourIntroduction, ourIntroduction)},
		{"an acceptance QSP never asked for is not answered", [][]byte{theirAcceptance}, nil},
		{"voice is not answered",
			[][]byte{tunnel([]byte{0x07, 0x03, 0x60, 0x02, 0x04, 0x0C, 0x0B})}, nil},
		{"the group byte is carried back",
			[][]byte{{0x08, 0x31, 0, 0, 0, 2, 7, 0xFD, 0x3F}},
			[]byte{0x08, 0x31, 0, 0, 0, 2, 7, 0xFD, 0x73, 0x08, 0x31, 0, 0, 0, 2, 7, 0xFD, 0x3F}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := start(t, Config{})
			c := dial(t, l)
			for _, w := range tc.writes {
				if _, err := c.Write(w); err != nil {
					t.Fatalf("write: %v", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if tc.want != nil {
				expect(t, c, tc.want)
			}
			silent(t, c)
		})
	}
}

// Break it: present the console with the repeater's address, or leave its
// site at the repeater's default, and this fails.
func TestTheConsoleFormIsThePublishedOne(t *testing.T) {
	l, _ := start(t, Config{PresentAs: "console"})
	c := dial(t, l)
	_, _ = c.Write(join(linkRequest, theirAcceptance, tunnel(stationIntroduction)))
	expect(t, c, join(linkAnswer,
		tunnel([]byte{0x0B, 0x3F}),
		tunnel([]byte{0x0B, 0xBF, 0x01, 0x1B, 0x00, 0, 0, 0, 0, 0xFF})))
	silent(t, c)
}

// QSP asks until the station accepts, and not before the router has sent a
// frame to copy the group byte from.
//
// Break it: keep asking after the acceptance, or ask before anything has
// arrived, and this fails.
func TestQSPAsksUntilItIsAccepted(t *testing.T) {
	l, _ := start(t, Config{Request: 40 * time.Millisecond})
	c := dial(t, l)
	silent(t, c)

	_, _ = c.Write(tunnel([]byte{0xFD, 0x01}))
	expect(t, c, join(ourRequest, ourRequest, ourRequest))

	_, _ = c.Write(theirAcceptance)
	// One already on its way may arrive; after that, nothing.
	time.Sleep(60 * time.Millisecond)
	_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	var drain [64]byte
	_, _ = c.Read(drain[:])
	silent(t, c)
}

// Break it: keep reading after a bad marker, and the request that follows is
// answered from a stream that has lost its place.
func TestAStreamThatIsNotATunnelIsClosed(t *testing.T) {
	l, _ := start(t, Config{})
	c := dial(t, l)
	if _, err := c.Write(append([]byte("GET / HTTP/1.1\r\n\r\n"), linkRequest...)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [16]byte
	if n, err := c.Read(b[:]); n != 0 || err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected the connection closed, got %d bytes and %v", n, err)
	}
}

// Break it: skip the allow list, and the stranger is answered.
func TestOnlyAllowedRoutersAreServed(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		served  bool
	}{
		{"an empty list serves anybody", nil, true},
		{"this address listed", []string{"127.0.0.1"}, true},
		{"only another address listed", []string{"192.0.2.9"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := start(t, Config{AllowedRouters: tc.allowed})
			c := dial(t, l)
			_, _ = c.Write(linkRequest)
			if tc.served {
				expect(t, c, linkAnswer)
				return
			}
			_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
			var b [9]byte
			if n, err := c.Read(b[:]); n != 0 || err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("expected a refusal, got %d bytes and %v", n, err)
			}
			if l.Refused() != 1 {
				t.Errorf("refused count is %d", l.Refused())
			}
		})
	}
}

// introduction is a repeater's introduction for a site, as the Quantar's was
// for site 1.
func introduction(site byte) []byte {
	return tunnel([]byte{0xFD, 0xBF, 0x01, site*2 + 1, 0xC2, 0, 0, 0, 0, 0xFF})
}

// link opens a tunnel and brings its repeater's link up as the given site.
func link(t *testing.T, l *Listener, site byte) net.Conn {
	t.Helper()
	c := dial(t, l)
	_, _ = c.Write(join(linkRequest, theirAcceptance, introduction(site), tunnel([]byte{0xFD, 0x01})))
	expect(t, c, join(linkAnswer, ourRequest, ourIntroduction))
	return c
}

// waitFor polls until ok, and fails with what if it never is.
func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("never happened: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A router that restarts dials again without closing what it had, and a
// router with two serial ports dials twice on purpose.
//
// Break it: keep one tunnel per router address, and the second repeater on a
// router closes the first; never close the older tunnel, and a restarted
// router's repeater is listed twice.
func TestWhichTunnelsFromOneRouterAreTheSameRepeater(t *testing.T) {
	tests := []struct {
		name        string
		second      []byte
		firstCloses bool
		repeaters   int
	}{
		{"the same site again is the same repeater, reconnected",
			join(linkRequest, theirAcceptance, introduction(1)), true, 1},
		{"another site is another repeater",
			join(linkRequest, theirAcceptance, introduction(3)), false, 2},
		{"a tunnel that has not said which it is closes nothing",
			join(linkRequest, theirAcceptance), false, 2},
		{"the same site in another group is another serial port",
			join([]byte{0x08, 0x31, 0, 0, 0, 2, 2, 0xFD, 0x3F},
				[]byte{0x08, 0x31, 0, 0, 0, 2, 2, 0xFD, 0x73},
				Frame{Op: OpData, Group: 2, Payload: []byte{0xFD, 0xBF, 0x01, 0x03, 0xC2, 0, 0, 0, 0, 0xFF}}.Append(nil)),
			false, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := start(t, Config{Keepalive: time.Hour})
			first := link(t, l, 1)
			second := dial(t, l)
			go func() { _, _ = io.Copy(io.Discard, second) }()
			_, _ = second.Write(tc.second)

			waitFor(t, "the repeater count settling", func() bool { return len(l.Repeaters()) == tc.repeaters })
			_ = first.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
			var b [1]byte
			_, err := first.Read(b[:])
			closed := err != nil && !errors.Is(err, os.ErrDeadlineExceeded)
			if closed != tc.firstCloses {
				t.Errorf("the first tunnel closed: %v (%v)", closed, err)
			}
			time.Sleep(50 * time.Millisecond)
			if n := len(l.Repeaters()); n != tc.repeaters {
				t.Errorf("%d repeaters listed", n)
			}
		})
	}
}

// Break it: do not close on cancel, and Wait never returns.
func TestCancellingStopsTheListenerAndItsTunnels(t *testing.T) {
	l, cancel := start(t, Config{})
	c := dial(t, l)
	_, _ = c.Write(linkRequest)
	expect(t, c, linkAnswer)
	addr := l.Addr().String()

	cancel()
	finished := make(chan struct{})
	go func() { l.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the listener did not stop")
	}
	if l.Running() {
		t.Error("the listener still reports running")
	}
	if c2, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		_ = c2.Close()
		t.Error("the port still accepts")
	}
}

// Break it: record only what arrives, and the answer is missing from the file.
func TestTheRecordHoldsBothDirections(t *testing.T) {
	dir := t.TempDir()
	l, cancel := start(t, Config{RecordDir: dir})
	c := dial(t, l)
	_, _ = c.Write(append(linkRequest, 0x08, 0x31, 0, 0, 0, 3, 1, 0xFD, 0x03, 0x01))
	expect(t, c, join(linkAnswer, ourRequest))
	silent(t, c)
	cancel()
	l.Wait()

	files, _ := filepath.Glob(filepath.Join(dir, "v24-*.log"))
	if len(files) != 1 {
		t.Fatalf("found %d record files", len(files))
	}
	body, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	for _, want := range []string{"\trx\t0000\t01\tfd3f\n", "\ttx\t0000\t01\tfd73\n", "\ttx\t0000\t01\tfd3f\n", "\trx\t0000\t01\tfd0301\n"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the record lacks %q:\n%s", want, body)
		}
	}
	if l.Answered() != 2 || l.Unknown() != 1 {
		t.Errorf("answered %d and unknown %d", l.Answered(), l.Unknown())
	}
}

// The link, step by step, as the station's own frames drive it.
//
// Break it: send keepalives before both ends have introduced themselves, keep
// sending them after the station has started again, or count a link up before
// its keepalive is heard, and this fails.
func TestKeepalivesFollowTheLinkAndStopWithIt(t *testing.T) {
	const beat = 40 * time.Millisecond
	l, _ := start(t, Config{Keepalive: beat})
	c := dial(t, l)
	keepalive := tunnel([]byte{0xFD, 0x01})
	waitUp := func(want int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for l.LinksUp() != want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if l.LinksUp() != want {
			t.Fatalf("%d links up, want %d", l.LinksUp(), want)
		}
	}

	// Open from both ends and not yet introduced: nothing is sent unasked.
	_, _ = c.Write(join(linkRequest, theirAcceptance))
	expect(t, c, join(linkAnswer, ourRequest))
	silent(t, c)
	waitUp(0)

	// Introduced: keepalives begin, and the link is not up until the
	// station's own is heard.
	_, _ = c.Write(tunnel(stationIntroduction))
	expect(t, c, join(ourIntroduction, keepalive, keepalive))
	waitUp(0)
	_, _ = c.Write(keepalive)
	waitUp(1)

	// The station starts again: the link is down and the keepalives stop.
	// One already on its way may arrive first.
	_, _ = c.Write(linkRequest)
	got := make([]byte, 9)
	for {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.ReadFull(c, got); err != nil {
			t.Fatalf("waiting for the acceptance: %v", err)
		}
		if bytes.Equal(got, linkAnswer) {
			break
		}
		if !bytes.Equal(got, keepalive) {
			t.Fatalf("got % x", got)
		}
	}
	expect(t, c, ourRequest)
	waitUp(0)
	silent(t, c)

	// It comes back, and a tunnel that closes takes its link with it.
	_, _ = c.Write(join(theirAcceptance, tunnel(stationIntroduction)))
	expect(t, c, ourIntroduction)
	_, _ = c.Write(keepalive)
	waitUp(1)
	_ = c.Close()
	waitUp(0)
}

// Break it: accept any string, and a mistyped address serves nobody without
// saying why.
func TestAConfigurationThatCannotWorkIsRefused(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		ok   bool
	}{
		{"an address and port", Config{ListenAddress: "0.0.0.0:1994"}, true},
		{"no port", Config{ListenAddress: "0.0.0.0"}, false},
		{"empty", Config{}, false},
		{"a router by address", Config{ListenAddress: ":1994", AllowedRouters: []string{" 192.0.2.4 "}}, true},
		{"a router by name", Config{ListenAddress: ":1994", AllowedRouters: []string{"router1"}}, false},
		{"presented as a console", Config{ListenAddress: ":1994", PresentAs: "console"}, true},
		{"presented as something else", Config{ListenAddress: ":1994", PresentAs: "diu"}, false},
		{"the largest site", Config{ListenAddress: ":1994", Site: 127}, true},
		{"a site an introduction cannot carry", Config{ListenAddress: ":1994", Site: 128}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.cfg.Validate(); (err == nil) != tc.ok {
				t.Errorf("Validate returned %v", err)
			}
		})
	}
}

// The whole of the repeater's side of 2026-10-04, from the router connecting
// to the last transmission, sent to a listener as the router sent it.
//
// Break it: count voice as frames QSP cannot read, leave the link down after
// the repeater's keepalive, or lose a transmission, and this fails.
func TestTheCapturedSessionIsALinkedRepeaterWithThreeCalls(t *testing.T) {
	raw, err := os.ReadFile(voiceFixture)
	if err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	l, _ := start(t, Config{})
	c := dial(t, l)
	go func() { _, _ = io.Copy(io.Discard, c) }() // QSP's answers are not under test here
	if _, err := c.Write(raw); err != nil {
		t.Fatalf("write: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for l.Calls() != 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	rs := l.Repeaters()
	if len(rs) != 1 {
		t.Fatalf("%d repeaters", len(rs))
	}
	r := rs[0]
	if !r.Up || !r.Introduced || r.Site != 1 || StationTypeName(r.StationType) != "Quantar" {
		t.Errorf("up %v, introduced %v, site %d, %s", r.Up, r.Introduced, r.Site, StationTypeName(r.StationType))
	}
	if r.Frames != 513 || r.Calls != 3 || r.Transmitting {
		t.Errorf("%d frames, %d calls, transmitting %v", r.Frames, r.Calls, r.Transmitting)
	}
	if r.Talkgroup != 1 || r.SourceID != 8080303 || r.LastHeard.IsZero() {
		t.Errorf("talkgroup %d, radio %d, last heard %v", r.Talkgroup, r.SourceID, r.LastHeard)
	}
	if l.VoiceFrames() != 513 || l.Calls() != 3 || l.Unknown() != 0 || l.LinksUp() != 1 {
		t.Errorf("%d voice frames, %d calls, %d unread, %d links up",
			l.VoiceFrames(), l.Calls(), l.Unknown(), l.LinksUp())
	}

	// The tunnel closing takes the repeater off the list.
	_ = c.Close()
	for deadline = time.Now().Add(2 * time.Second); len(l.Repeaters()) != 0 && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	if n := len(l.Repeaters()); n != 0 {
		t.Errorf("%d repeaters after the tunnel closed", n)
	}
}

// Break it: never look for a transmission that has gone quiet, and it stays
// open for as long as the tunnel does.
func TestATransmissionWithNoEndIsClosedWhenItGoesQuiet(t *testing.T) {
	l, _ := start(t, Config{Request: 50 * time.Millisecond})
	c := dial(t, l)
	go func() { _, _ = io.Copy(io.Discard, c) }()
	voice := append([]byte{0x07, 0x03, 0x63}, make([]byte, 13)...)
	_, _ = c.Write(join(
		tunnel([]byte{0x07, 0x03, 0x00, 0x02, 0x02, 0x0C, 0x0B, 0, 0, 0, 0, 0}),
		tunnel(voice), tunnel(voice)))

	// **Waited for by what is then asserted.** This waited only for the
	// transmission to begin, which the start marker does, and then asserted
	// both voice frames had been counted — so it failed whenever it looked
	// between the marker and the frames behind it. It did, once, on the
	// machine that runs the project's checks.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if rs := l.Repeaters(); len(rs) == 1 && rs[0].Transmitting && rs[0].Frames == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rs := l.Repeaters(); len(rs) != 1 || !rs[0].Transmitting || rs[0].Frames != 2 {
		t.Fatalf("the transmission was not heard: %+v", rs)
	}
	for deadline = time.Now().Add(CallTimeout + 2*time.Second); l.Calls() != 1 && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
	}
	if rs := l.Repeaters(); l.Calls() != 1 || rs[0].Transmitting || rs[0].Calls != 1 {
		t.Fatalf("%d calls finished, repeater %+v", l.Calls(), rs)
	}
}

// TestOnlySoManyTunnelsAreOpenAtOnce. With no routers named, anybody who can
// reach the port could open tunnels without limit, each a connection held
// with three goroutines behind it.
//
// Break it: remove the `len(l.conns) >= l.cfg.MaxTunnels` test from accept.
func TestOnlySoManyTunnelsAreOpenAtOnce(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		open  int
	}{
		{"a limit of two", 2, 5},
		{"the default", 0, DefaultMaxTunnels + 6},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.limit
			if want == 0 {
				want = DefaultMaxTunnels
			}
			l, _ := start(t, Config{Keepalive: time.Hour, MaxTunnels: tc.limit})
			conns := make([]net.Conn, 0, tc.open)
			for range tc.open {
				conns = append(conns, dial(t, l))
			}
			waitFor(t, "the extra connections refused", func() bool {
				return l.Refused() == uint64(tc.open-want)
			})
			if got := len(l.Repeaters()); got != want {
				t.Fatalf("%d tunnels are open, want %d", got, want)
			}

			// One closes, and there is room for another.
			_ = conns[0].Close()
			waitFor(t, "a tunnel to close", func() bool { return len(l.Repeaters()) == want-1 })
			c := dial(t, l)
			_, _ = c.Write(join(linkRequest))
			expect(t, c, join(linkAnswer, ourRequest))
		})
	}
}
