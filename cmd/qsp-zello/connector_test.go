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
	"github.com/k9mls/qsp/internal/routing"
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
		frames := audio.NewQueue(16)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go c.pump(ctx, s, mustBridge(t, s, &radioLog{}), frames)

		frames.Push(audio.Frame{PTT: true})
		for range 6 {
			frames.Push(pcmFrame())
		}
		frames.Push(audio.Frame{PTT: false})
		waitFor(t, "the stream to close", func() bool { return strings.HasSuffix(s.shape(), "stop") })
		if got := s.shape(); got != "start audio audio stop" {
			t.Errorf("Zello saw %q, want two 60 ms packets between start and stop", got)
		}
	})

	t.Run("audio with no keyup still opens the stream", func(t *testing.T) {
		s := newFakeSession()
		c := testConnector(t, s)
		frames := audio.NewQueue(16)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go c.pump(ctx, s, mustBridge(t, s, &radioLog{}), frames)
		for range 3 {
			frames.Push(pcmFrame())
		}
		frames.Push(audio.Frame{PTT: false})
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
			go c.pump(ctx, s, mustBridge(t, s, radio), audio.NewQueue(16))
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

// TestAZelloStreamOfLongPacketsReachesQSP is the first real connection's
// failure, through the real bridge: the Zello app sent packets carrying more
// than one 60 ms frame, and every one was refused before reaching QSP.
//
// To see it bite: size internal/opus's decoder buffer with SamplesPerFrame,
// and QSP receives a keyup, no audio and a release.
func TestAZelloStreamOfLongPacketsReachesQSP(t *testing.T) {
	single := opusPacket(t)
	if single[0]&3 != 0 {
		t.Fatalf("TOC code %d; the doubling below needs a single-frame packet", single[0]&3)
	}
	// A legal two-frame Opus packet (RFC 6716 §3.2.2, code 1): 120 ms.
	long := append([]byte{single[0]&^3 | 1}, single[1:]...)
	long = append(long, single[1:]...)

	s := newFakeSession()
	c := testConnector(t, s)
	radio := &radioLog{}
	s.audio <- zello.IncomingPacket{StreamID: 11, PacketID: 1, Opus: long}
	s.audio <- zello.IncomingPacket{StreamID: 11, PacketID: 2, Opus: long}
	s.events <- zello.Event{Command: zello.EventStreamStop, StreamID: 11}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.pump(ctx, s, mustBridge(t, s, radio), audio.NewQueue(16))

	waitFor(t, "the release toward QSP", func() bool { return strings.HasSuffix(radio.String(), "R") })
	want := "K" + strings.Repeat("A", 12) + "R"
	if got := radio.String(); got != want {
		t.Errorf("QSP received %q, want %q: two 120 ms packets are twelve 20 ms frames", got, want)
	}
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
	frames := audio.NewQueue(16)
	done := make(chan struct{})
	go func() { c.pump(context.Background(), s, mustBridge(t, s, radio), frames); close(done) }()

	frames.Push(audio.Frame{PTT: true})
	frames.Push(pcmFrame())
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
	frames := audio.NewQueue(16)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.pump(ctx, s, mustBridge(t, s, &radioLog{}), frames)
	frames.Push(audio.Frame{PTT: true})
	frames.Push(pcmFrame())
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
	err := c.once(context.Background(), audio.NewQueue(16), &radioLog{})
	state, _ := classify(err, 0)
	if dialled {
		t.Error("Zello was dialled with no credentials")
	}
	if state != stateCredentialsMissing {
		t.Errorf("state %q", state)
	}
}

// countingBridge records what pump asks of the bridge, with no codec behind
// it, so a test can say exactly which packets were played and when a
// transmission toward QSP was ended.
type countingBridge struct {
	mu      sync.Mutex
	played  []zello.IncomingPacket
	stopped []time.Time
}

func (b *countingBridge) RadioKeyup() error        { return nil }
func (b *countingBridge) RadioFrame([]int16) error { return nil }
func (b *countingBridge) RadioRelease() error      { return nil }
func (b *countingBridge) Close()                   {}
func (b *countingBridge) ZelloPacket(p zello.IncomingPacket) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.played = append(b.played, p)
	return nil
}
func (b *countingBridge) ZelloStreamStopped(uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = append(b.stopped, time.Now())
	return nil
}
func (b *countingBridge) snapshot() (played []zello.IncomingPacket, stopped []time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]zello.IncomingPacket(nil), b.played...), append([]time.Time(nil), b.stopped...)
}

// TestAClosedAudioChannelIsNotAPacket: the session's reader closes Audio and
// Events when it exits, and a closed channel hands out zero values for ever.
// A zero packet played is stream 0 with no Opus -- audio toward QSP with no
// keyup and, because nothing is keyed, no release: QSP keys a repeater for
// two seconds and logs "USRP audio stopped without a release".
//
// Done is deliberately left open in every row, so the only way pump can
// return is by noticing the closed channel.
//
// To see it fail: in pump, replace the `if !ok { return }` under
// `case p, ok := <-s.Audio():` with nothing; every row plays zero packets.
// Remove the `break drain` on !ok instead, and the last row never returns.
func TestAClosedAudioChannelIsNotAPacket(t *testing.T) {
	real := zello.IncomingPacket{StreamID: 7, PacketID: 1, Opus: []byte{1}}
	stop := zello.Event{Command: zello.EventStreamStop, StreamID: 7}
	tests := []struct {
		name        string
		packets     []zello.IncomingPacket
		events      []zello.Event
		closeEvents bool
	}{
		{name: "closed with nothing queued"},
		{name: "closed behind a queued packet, which is still played", packets: []zello.IncomingPacket{real}},
		{name: "events closed as well, as the real session does", closeEvents: true},
		{name: "closed behind a packet and its stop", packets: []zello.IncomingPacket{real},
			events: []zello.Event{stop}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeSession()
			for _, p := range tc.packets {
				s.audio <- p
			}
			for _, ev := range tc.events {
				s.events <- ev
			}
			close(s.audio)
			if tc.closeEvents {
				close(s.events)
			}
			br := &countingBridge{}
			c := testConnector(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { c.pump(ctx, s, br, audio.NewQueue(16)); close(done) }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				played, _ := br.snapshot()
				t.Fatalf("pump did not return when Audio closed; it played %d packet(s)", len(played))
			}
			played, _ := br.snapshot()
			for _, p := range played {
				if p.StreamID == 0 && p.Opus == nil {
					t.Fatalf("played %d packet(s), one of them the zero value of a closed channel", len(played))
				}
			}
			// What was queued before the close is still delivered, in order.
			if len(played) != len(tc.packets) {
				t.Errorf("played %d packet(s), %d were sent", len(played), len(tc.packets))
			}
		})
	}
}

// TestAZelloStreamWithNoStopIsReleasedBeforeQSPGivesUp: when on_stream_stop is
// missed, this side's fallback release has to reach QSP before QSP's own
// routing.StreamTimeout, or QSP records a fault for an over that was about to
// be released.
//
// To see it fail: in pump's tick case, compare lastZelloAudio against idle
// rather than zelloIdle; the release comes at two seconds or later.
func TestAZelloStreamWithNoStopIsReleasedBeforeQSPGivesUp(t *testing.T) {
	s := newFakeSession()
	br := &countingBridge{}
	c := testConnector(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.pump(ctx, s, br, audio.NewQueue(16))

	sent := time.Now()
	s.audio <- zello.IncomingPacket{StreamID: 7, PacketID: 1, Opus: []byte{1}}
	waitFor(t, "the fallback release", func() bool { _, stopped := br.snapshot(); return len(stopped) > 0 })
	_, stopped := br.snapshot()
	if took := stopped[0].Sub(sent); took >= routing.StreamTimeout {
		t.Errorf("released %s after the last audio; QSP gives up at %s and blames a crash",
			took.Round(10*time.Millisecond), routing.StreamTimeout)
	}
}

// TestAnOverThatOverflowsTheQueueStillEnds: the pump stuck while QSP sent more
// than the queue holds, then running again. The release is the last thing QSP
// sent, and it must be what closes the Zello stream -- not the two-second
// timer, which is what closed it when a full queue refused the release.
//
// To see it bite: in audio.Queue.Push, return without adding the frame when
// the queue is full.
func TestAnOverThatOverflowsTheQueueStillEnds(t *testing.T) {
	tests := []struct {
		name  string
		audio int
	}{
		{"just over what it holds", 16},
		{"several times what it holds", 80},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newFakeSession()
			c := testConnector(t, s)
			frames := audio.NewQueue(16)
			frames.Push(audio.Frame{PTT: true})
			for range tc.audio {
				frames.Push(pcmFrame())
			}
			frames.Push(audio.Frame{PTT: false})
			if frames.Lost() == 0 {
				t.Fatal("the queue did not overflow, so this proves nothing")
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			began := time.Now()
			go c.pump(ctx, s, mustBridge(t, s, &radioLog{}), frames)
			waitFor(t, "the Zello stream to close", func() bool { return strings.HasSuffix(s.shape(), "stop") })
			if took := time.Since(began); took > idle/2 {
				t.Errorf("the stream closed after %s: by the timer, not by the release", took)
			}
			if got := s.shape(); !strings.HasPrefix(got, "start audio") || strings.Count(got, "start") != 1 {
				t.Errorf("Zello was sent %q, want one stream with audio in it", got)
			}
		})
	}
}

// TestWhatArrivedDuringTheLogonIsNotPlayed: nothing empties the queue while
// the connector logs on, so without this an over QSP sent in those seconds
// reaches the channel the moment the session opens.
//
// To see it bite: remove the Discard before pump in once.
func TestWhatArrivedDuringTheLogonIsNotPlayed(t *testing.T) {
	s := newFakeSession()
	c := testConnector(t, s)
	frames := audio.NewQueue(16)
	logon := c.fetch
	c.fetch = func(ctx context.Context) (zellologon.Logon, error) {
		// QSP sends a whole over while the logon is in progress.
		frames.Push(audio.Frame{PTT: true})
		for range 6 {
			frames.Push(pcmFrame())
		}
		frames.Push(audio.Frame{PTT: false})
		return logon(ctx)
	}

	done := make(chan error, 1)
	go func() { done <- c.once(context.Background(), frames, &radioLog{}) }()
	waitFor(t, "the session to connect", func() bool { return c.connected.Load() == 1 })
	time.Sleep(150 * time.Millisecond) // long enough for a pump to play eight frames
	close(s.done)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("once did not return when the session ended")
	}

	if got := s.shape(); got != "" {
		t.Errorf("Zello was sent %q: audio from before the session opened was played", got)
	}
	if got := c.discarded.Load(); got != 8 {
		t.Errorf("%d frames counted as discarded, want the 8 that arrived during the logon", got)
	}
}
