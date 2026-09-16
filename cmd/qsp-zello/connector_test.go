//go:build zello

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/opus"
	"github.com/k9mls/qsp/internal/zello"
	"github.com/k9mls/qsp/internal/zellobridge"
	"github.com/k9mls/qsp/internal/zellologon"
)

// fakeSession records what the bridge sends to Zello and lets a test push
// what Zello sends back.
type fakeSession struct {
	mu     sync.Mutex
	calls  []string
	audio  chan zello.IncomingPacket
	events chan zello.Event
	done   chan struct{}
	closed bool
}

func newFakeSession() *fakeSession {
	return &fakeSession{audio: make(chan zello.IncomingPacket, 64),
		events: make(chan zello.Event, 8), done: make(chan struct{})}
}

func (f *fakeSession) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeSession) StartStream() (uint32, error)       { f.record("start"); return 1, nil }
func (f *fakeSession) SendAudio([]byte) error             { f.record("audio"); return nil }
func (f *fakeSession) StopStream() error                  { f.record("stop"); return nil }
func (f *fakeSession) Audio() <-chan zello.IncomingPacket { return f.audio }
func (f *fakeSession) Events() <-chan zello.Event         { return f.events }
func (f *fakeSession) Done() <-chan struct{}              { return f.done }
func (f *fakeSession) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSession) shape() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, " ")
}

// radioLog records what reaches QSP: K keyup, A audio frame, R release.
type radioLog struct {
	mu    sync.Mutex
	shape strings.Builder
}

func (r *radioLog) add(b byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.shape.WriteByte(b)
}
func (r *radioLog) Keyup() error            { r.add('K'); return nil }
func (r *radioLog) SendFrame([]int16) error { r.add('A'); return nil }
func (r *radioLog) Release() error          { r.add('R'); return nil }
func (r *radioLog) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shape.String()
}

func realBridge(s session, r zellobridge.Radio) (bridge, error) {
	return zellobridge.New(zellobridge.Options{Stream: s, Radio: r})
}

func testConnector(t *testing.T, s *fakeSession) *connector {
	t.Helper()
	return &connector{
		cfg: Config{},
		log: slog.New(slog.DiscardHandler),
		fetch: func(context.Context) (zellologon.Logon, error) {
			return zellologon.Logon{Token: "t", Username: "u", Password: "p", Channel: "K9MLS Gateway"}, nil
		},
		dial:  func(context.Context, zello.Options) (session, error) { return s, nil },
		newBr: realBridge,
	}
}

func pcmFrame() audio.Frame {
	s := make([]int16, audio.SamplesPerFrame)
	for i := range s {
		s[i] = int16(3000 * ((i % 16) - 8) / 8)
	}
	return audio.Frame{PTT: true, Samples: s}
}

// opusPacket is one real 60 ms packet at 16 kHz, as Zello sends.
func opusPacket(t *testing.T) []byte {
	t.Helper()
	enc, err := opus.NewEncoder(16000)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	pkt, err := enc.Encode(make([]int16, 960))
	if err != nil {
		t.Fatal(err)
	}
	return pkt
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAudioCrossesBothWays drives the pump with the real bridge and libopus.
//
// To see it bite: in pump, drop the RadioKeyup on the lost-keyup path, and
// "audio with no keyup" sends nothing; or remove the drain before stopRadio,
// and "a Zello stream" fails most runs as KAAARKAAAR — the last packet played
// as a second transmission. It is ordering by select, so the Zello row runs
// fifty times to make a random pass unlikely.
func TestAudioCrossesBothWays(t *testing.T) {
	t.Run("QSP audio becomes a Zello stream", func(t *testing.T) {
		s := newFakeSession()
		c := testConnector(t, s)
		frames := make(chan audio.Frame, 16)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go c.pump(ctx, s, mustBridge(t, s, &radioLog{}), frames)

		frames <- audio.Frame{PTT: true}
		for range 6 {
			frames <- pcmFrame()
		}
		frames <- audio.Frame{PTT: false}
		waitFor(t, "the stream to close", func() bool { return strings.HasSuffix(s.shape(), "stop") })
		if got := s.shape(); got != "start audio audio stop" {
			t.Errorf("Zello saw %q, want two 60 ms packets between start and stop", got)
		}
	})

	t.Run("audio with no keyup still opens the stream", func(t *testing.T) {
		s := newFakeSession()
		c := testConnector(t, s)
		frames := make(chan audio.Frame, 16)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go c.pump(ctx, s, mustBridge(t, s, &radioLog{}), frames)
		for range 3 {
			frames <- pcmFrame()
		}
		frames <- audio.Frame{PTT: false}
		waitFor(t, "the stream to close", func() bool { return strings.HasSuffix(s.shape(), "stop") })
		if got := s.shape(); got != "start audio stop" {
			t.Errorf("Zello saw %q", got)
		}
	})

	t.Run("a Zello stream becomes QSP audio", func(t *testing.T) {
		pkt := opusPacket(t)
		for run := range 50 {
			s := newFakeSession()
			c := testConnector(t, s)
			radio := &radioLog{}
			ctx, cancel := context.WithCancel(context.Background())
			// All three are queued before the pump starts, which is exactly
			// the moment select is free to take them in the wrong order.
			s.audio <- zello.IncomingPacket{StreamID: 7, PacketID: 1, Opus: pkt}
			s.audio <- zello.IncomingPacket{StreamID: 7, PacketID: 2, Opus: pkt}
			s.events <- zello.Event{Command: zello.EventStreamStop, StreamID: 7}
			go c.pump(ctx, s, mustBridge(t, s, radio), make(chan audio.Frame))
			waitFor(t, "the release toward QSP", func() bool { return strings.HasSuffix(radio.String(), "R") })
			time.Sleep(10 * time.Millisecond)
			cancel()
			if got := radio.String(); got != "KAAAAAAR" {
				t.Fatalf("run %d: QSP received %q, want a keyup, three 20 ms frames per packet, "+
					"one release", run, got)
			}
		}
	})
}

func mustBridge(t *testing.T, s session, r zellobridge.Radio) bridge {
	t.Helper()
	br, err := realBridge(s, r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(br.Close)
	return br
}

// TestNothingIsLeftOpenWhenASessionEnds: a Zello stream left open holds the
// channel against everyone; a transmission left open toward QSP holds a
// repeater keyed.
//
// To see it bite: remove the deferred release/stopRadio in pump.
func TestNothingIsLeftOpenWhenASessionEnds(t *testing.T) {
	s := newFakeSession()
	c := testConnector(t, s)
	radio := &radioLog{}
	frames := make(chan audio.Frame, 16)
	done := make(chan struct{})
	go func() { c.pump(context.Background(), s, mustBridge(t, s, radio), frames); close(done) }()

	frames <- audio.Frame{PTT: true}
	frames <- pcmFrame()
	s.audio <- zello.IncomingPacket{StreamID: 9, PacketID: 1, Opus: opusPacket(t)}
	waitFor(t, "both directions to open", func() bool {
		return strings.Contains(s.shape(), "start") && strings.HasPrefix(radio.String(), "K")
	})
	close(s.done) // Zello dropped the connection
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("pump did not return when the session ended")
	}
	if !strings.HasSuffix(s.shape(), "stop") {
		t.Errorf("the Zello stream was left open: %q", s.shape())
	}
	if !strings.HasSuffix(radio.String(), "R") {
		t.Errorf("the transmission toward QSP was left open: %q", radio.String())
	}
}

// TestAQSPTransmissionWithNoReleaseClosesTheZelloStream: QSP crashing mid-over.
//
// To see it bite: delete the toZello idle check in pump's tick case.
func TestAQSPTransmissionWithNoReleaseClosesTheZelloStream(t *testing.T) {
	s := newFakeSession()
	c := testConnector(t, s)
	frames := make(chan audio.Frame, 16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.pump(ctx, s, mustBridge(t, s, &radioLog{}), frames)
	frames <- audio.Frame{PTT: true}
	frames <- pcmFrame()
	waitFor(t, "the idle close", func() bool { return strings.HasSuffix(s.shape(), "stop") })
}

// TestAReasonToStopBecomesAStateAndAWait.
//
// To see a row fail: make classify retry a fatal Zello refusal on backoff,
// and "Zello refused" waits seconds instead of minutes.
func TestAReasonToStopBecomesAStateAndAWait(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		failures  int
		wantState string
		atLeast   time.Duration
		atMost    time.Duration
	}{
		{"a session that ran", nil, 3, stateConnected, 0, 2 * time.Second},
		{"credentials not entered", &zellologon.Error{Kind: zellologon.KindMissing}, 0, stateCredentialsMissing, 30 * time.Second, 30 * time.Second},
		{"a key that does not parse", &zellologon.Error{Kind: zellologon.KindUnusable}, 0, stateCredentialsBad, time.Minute, time.Minute},
		{"QSP not answering", fmt.Errorf("%w: cannot reach /run/qsp/zello.sock: connection refused", zellologon.ErrUnreachable), 0, stateQSPUnreachable, 2 * time.Second, 2 * time.Second},
		{"Zello refused the logon", &zello.CommandError{Command: "logon", Code: zello.ErrNotAuthorized}, 0, stateZelloRefused, 5 * time.Minute, 5 * time.Minute},
		{"the network, early", errors.New("dial tcp: i/o timeout"), 0, stateZelloUnreachable, 2 * time.Second, 2 * time.Second},
		{"the network, long down, is capped", errors.New("dial tcp: i/o timeout"), 12, stateZelloUnreachable, time.Minute, time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state, wait := classify(tc.err, tc.failures)
			if state != tc.wantState || wait < tc.atLeast || wait > tc.atMost {
				t.Errorf("classify = %s after %s, want %s within [%s, %s]", state, wait, tc.wantState, tc.atLeast, tc.atMost)
			}
		})
	}
}

// TestMissingCredentialsNeverReachZello: with nothing to log on with, the
// connector must not dial Zello at all.
func TestMissingCredentialsNeverReachZello(t *testing.T) {
	dialled := false
	c := &connector{
		cfg: Config{}, log: slog.New(slog.DiscardHandler),
		fetch: func(context.Context) (zellologon.Logon, error) {
			return zellologon.Logon{}, &zellologon.Error{Kind: zellologon.KindMissing, Message: "the credential \"zello-password\" has not been entered"}
		},
		dial:  func(context.Context, zello.Options) (session, error) { dialled = true; return nil, errors.New("x") },
		newBr: realBridge,
	}
	err := c.once(context.Background(), make(chan audio.Frame), &radioLog{})
	state, _ := classify(err, 0)
	if dialled {
		t.Error("Zello was dialled with no credentials")
	}
	if state != stateCredentialsMissing {
		t.Errorf("state %q", state)
	}
}
