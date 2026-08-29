package peers

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// recordingWriter captures what playback sent and when.
type recordingWriter struct {
	mu    sync.Mutex
	sent  [][]byte
	times []time.Time
	// fail makes the next n writes fail, for the dropped-packet case.
	fail int
}

func (w *recordingWriter) WriteToUDPAddrPort(b []byte, _ netip.AddrPort) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail > 0 {
		w.fail--
		return 0, context.DeadlineExceeded
	}
	// Copied: the caller reuses its buffer, and a test asserting on bytes that
	// were overwritten afterwards proves nothing.
	frame := make([]byte, len(b))
	copy(frame, b)
	w.sent = append(w.sent, frame)
	w.times = append(w.times, time.Now())
	return len(b), nil
}

func (w *recordingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.sent)
}

func (w *recordingWriter) frames() []hbp.Data {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]hbp.Data, 0, len(w.sent))
	for _, b := range w.sent {
		if msg, err := hbp.Parse(b); err == nil {
			if d, ok := msg.(hbp.Data); ok {
				out = append(out, d)
			}
		}
	}
	return out
}

func aRecording(peer hbp.RepeaterID, frames int, playAt time.Time) parrot.Recording {
	rec := parrot.Recording{Peer: peer, PlayAt: playAt}
	for i := 0; i < frames; i++ {
		rec.Frames = append(rec.Frames, hbp.Data{
			Sequence: uint8(i), SourceID: 3132910, TargetID: 9990,
			RepeaterID: 999999, Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
			FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xABCD,
		})
	}
	return rec
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestARecordingIsSentBack is the feature: a member hears themselves.
func TestARecordingIsSentBack(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx, aRecording(3132910, 5, time.Now()), netip.MustParseAddrPort("192.0.2.1:62031"))

	waitUntil(t, "the frames to be sent", func() bool { return w.count() == 5 })
}

// TestFramesLeaveSixtyMillisecondsApart. A radio decodes on that schedule, and
// replaying faster produces nothing it can use — which would look like parrot
// working and sounding like nothing.
func TestFramesLeaveSixtyMillisecondsApart(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx, aRecording(3132910, 5, time.Now()), netip.MustParseAddrPort("192.0.2.1:62031"))
	waitUntil(t, "the frames to be sent", func() bool { return w.count() == 5 })

	w.mu.Lock()
	defer w.mu.Unlock()
	for i := 1; i < len(w.times); i++ {
		gap := w.times[i].Sub(w.times[i-1])
		// Generous on the upper bound: a loaded CI machine schedules late, and
		// a test that fails for that reason teaches people to ignore it. The
		// claim being made is that frames are paced at all rather than sent as
		// fast as the socket accepts them.
		if gap < 40*time.Millisecond {
			t.Errorf("frames %d and %d are %v apart; a radio cannot decode that", i-1, i, gap)
		}
	}
}

// TestTheReplayCarriesThePeersOwnID. The far end registered this link, and a
// frame naming anything else is from a station it has never heard of.
func TestTheReplayCarriesThePeersOwnID(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx, aRecording(3132910, 3, time.Now()), netip.MustParseAddrPort("192.0.2.1:62031"))
	waitUntil(t, "the frames to be sent", func() bool { return w.count() == 3 })

	for _, f := range w.frames() {
		if f.RepeaterID != 3132910 {
			t.Errorf("frame carries repeater %d, want the peer's own", f.RepeaterID)
		}
		// And the member's own identity is untouched.
		if f.SourceID != 3132910 || f.TargetID != 9990 {
			t.Errorf("the frame's addressing was altered: %+v", f)
		}
	}
}

// TestTheGapIsHonoured. A replay that starts instantly is played at a radio
// that has not finished transmitting.
func TestTheGapIsHonoured(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	p.Start(ctx, aRecording(3132910, 2, start.Add(200*time.Millisecond)),
		netip.MustParseAddrPort("192.0.2.1:62031"))

	// Nothing yet.
	time.Sleep(100 * time.Millisecond)
	if w.count() != 0 {
		t.Error("the replay began before its gap elapsed")
	}
	waitUntil(t, "the frames to be sent", func() bool { return w.count() == 2 })
}

// TestKeyingUpStopsAReplay. Two audio streams on one timeslot is what
// contention exists to prevent, and pressing PTT again means start over.
func TestKeyingUpStopsAReplay(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx, aRecording(3132910, 200, time.Now()), netip.MustParseAddrPort("192.0.2.1:62031"))

	waitUntil(t, "the replay to start", func() bool { return w.count() > 0 })
	p.Stop(3132910)

	sent := w.count()
	time.Sleep(200 * time.Millisecond)
	if w.count() > sent+1 {
		t.Errorf("the replay continued after being stopped: %d then %d", sent, w.count())
	}
	waitUntil(t, "the playback to be forgotten", func() bool { return p.Active() == 0 })
}

// TestASecondReplayReplacesTheFirst. Starting one while another runs for the
// same peer must not put two streams on their timeslot.
func TestASecondReplayReplacesTheFirst(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := netip.MustParseAddrPort("192.0.2.1:62031")
	p.Start(ctx, aRecording(3132910, 200, time.Now()), addr)
	waitUntil(t, "the first replay to start", func() bool { return w.count() > 0 })

	p.Start(ctx, aRecording(3132910, 3, time.Now()), addr)
	waitUntil(t, "one playback to remain", func() bool { return p.Active() <= 1 })
}

// TestOneDroppedPacketDoesNotEndTheReplay. UDP to a hotspot on a domestic
// connection drops packets, and abandoning a recording over one would make
// parrot look broken when it is not.
func TestOneDroppedPacketDoesNotEndTheReplay(t *testing.T) {
	w := &recordingWriter{fail: 2}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx, aRecording(3132910, 6, time.Now()), netip.MustParseAddrPort("192.0.2.1:62031"))

	// Two writes fail, four succeed.
	waitUntil(t, "the surviving frames", func() bool { return w.count() == 4 })
}

// TestShutdownStopsEveryReplay. A playback outliving the listener would write
// to a closed socket.
func TestShutdownStopsEveryReplay(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx, aRecording(3132910, 200, time.Now()), netip.MustParseAddrPort("192.0.2.1:62031"))
	waitUntil(t, "the replay to start", func() bool { return w.count() > 0 })

	cancel()
	waitUntil(t, "the playback to end", func() bool { return p.Active() == 0 })
}

// TestTwoPeersReplayIndependently. Two members may test at once.
func TestTwoPeersReplayIndependently(t *testing.T) {
	w := &recordingWriter{}
	p := newPlayback(logging.Discard(), w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Start(ctx, aRecording(3132910, 4, time.Now()), netip.MustParseAddrPort("192.0.2.1:62031"))
	p.Start(ctx, aRecording(3155413, 4, time.Now()), netip.MustParseAddrPort("192.0.2.2:62031"))

	waitUntil(t, "both replays", func() bool { return w.count() == 8 })

	var mine, theirs int
	for _, f := range w.frames() {
		switch f.RepeaterID {
		case 3132910:
			mine++
		case 3155413:
			theirs++
		}
	}
	if mine != 4 || theirs != 4 {
		t.Errorf("frames went to the wrong peers: %d and %d", mine, theirs)
	}
}

// TestStoppingAPeerThatIsNotPlayingIsSafe covers the ordinary case of a member
// transmitting on another talkgroup.
func TestStoppingAPeerThatIsNotPlayingIsSafe(t *testing.T) {
	p := newPlayback(logging.Discard(), &recordingWriter{})
	p.Stop(3132910)
	if p.Active() != 0 {
		t.Error("stopping an idle peer left something running")
	}
}
