package quantar

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

// A router that restarts dials again without closing what it had.
//
// Break it: keep the first connection, and it is never closed.
func TestANewTunnelFromTheSameRouterReplacesTheOld(t *testing.T) {
	l, _ := start(t, Config{})
	first := dial(t, l)
	_, _ = first.Write(linkRequest)
	expect(t, first, join(linkAnswer, ourRequest))

	second := dial(t, l)
	_, _ = second.Write(linkRequest)
	expect(t, second, linkAnswer)

	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	if n, err := first.Read(b[:]); n != 0 || err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("the first tunnel was left open: %d bytes and %v", n, err)
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

	files, _ := filepath.Glob(filepath.Join(dir, "quantar-*.log"))
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
