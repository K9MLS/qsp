package parrot_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

const (
	testPeer  = hbp.RepeaterID(3132910)
	testRadio = 3132910
	parrotTG  = 9990
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newRecorder(t *testing.T, opts ...func(*parrot.Config)) (*parrot.Recorder, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)}
	cfg := parrot.Config{
		Talkgroup: parrotTG,
		Timeslot:  hbp.Timeslot2,
		Now:       c.now,
	}
	for _, o := range opts {
		o(&cfg)
	}
	r, err := parrot.New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, c
}

func voice(stream hbp.StreamID, target uint32) hbp.Data {
	return hbp.Data{
		SourceID: testRadio, TargetID: target, RepeaterID: testPeer,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: stream,
	}
}

// transmit sends n frames, 60ms apart, and returns anything Observe produced.
func transmit(r *parrot.Recorder, c *clock, stream hbp.StreamID, n int) *parrot.Recording {
	var out *parrot.Recording
	for i := 0; i < n; i++ {
		if rec := r.Observe(testPeer, voice(stream, parrotTG)); rec != nil {
			out = rec
		}
		c.advance(parrot.FrameInterval)
	}
	return out
}

// TestATransmissionComesBack is the whole feature: a member keys up and hears
// themselves, with nobody else awake.
func TestATransmissionComesBack(t *testing.T) {
	r, c := newRecorder(t)

	if rec := transmit(r, c, 0x1234, 20); rec != nil {
		t.Fatal("a recording completed while the transmission was still running")
	}

	// The frames stop.
	c.advance(3 * time.Second)
	done := r.Expire(c.now())

	if len(done) != 1 {
		t.Fatalf("got %d recordings, want 1", len(done))
	}
	if len(done[0].Frames) != 20 {
		t.Errorf("recorded %d frames, want 20", len(done[0].Frames))
	}
	if done[0].Peer != testPeer {
		t.Errorf("the recording belongs to peer %d", done[0].Peer)
	}
}

// TestTheReplayCarriesANewStreamID. A repeated stream ID is a duplicate
// transmission to a radio and is discarded as one, so the member would hear
// nothing and have no idea why.
func TestTheReplayCarriesANewStreamID(t *testing.T) {
	r, c := newRecorder(t)
	const original = hbp.StreamID(0x1234)

	transmit(r, c, original, 10)
	c.advance(3 * time.Second)
	done := r.Expire(c.now())

	if len(done) != 1 {
		t.Fatalf("got %d recordings", len(done))
	}
	for _, f := range done[0].Frames {
		if f.StreamID == original {
			t.Fatal("the replay reuses the transmission's stream ID")
		}
	}
	// And every frame shares the new one, or a radio sees a stream per frame.
	first := done[0].Frames[0].StreamID
	for i, f := range done[0].Frames {
		if f.StreamID != first {
			t.Fatalf("frame %d has a different stream ID", i)
		}
	}
}

// TestTheMembersOwnIdentityIsPreserved. Their display should show what it
// showed when they transmitted, not the network's idea of who is speaking.
func TestTheMembersOwnIdentityIsPreserved(t *testing.T) {
	r, c := newRecorder(t)
	transmit(r, c, 0x1234, 5)
	c.advance(3 * time.Second)
	done := r.Expire(c.now())

	for _, f := range done[0].Frames {
		if f.SourceID != testRadio {
			t.Errorf("source is %d, want the member's own %d", f.SourceID, testRadio)
		}
		if f.TargetID != parrotTG {
			t.Errorf("target is %d, want the parrot talkgroup", f.TargetID)
		}
	}
}

func TestFramesAreNumberedInOrder(t *testing.T) {
	r, c := newRecorder(t)
	transmit(r, c, 0x1234, 6)
	c.advance(3 * time.Second)
	done := r.Expire(c.now())

	for i, f := range done[0].Frames {
		if f.Sequence != uint8(i) {
			t.Errorf("frame %d has sequence %d", i, f.Sequence)
		}
	}
}

// TestOnlyTheParrotTalkgroupIsHandled. A frame parrot handles never reaches the
// routing core, so handling one it should not would swallow real traffic.
func TestOnlyTheParrotTalkgroupIsHandled(t *testing.T) {
	r, _ := newRecorder(t)

	if !r.Handles(voice(1, parrotTG)) {
		t.Error("the parrot talkgroup is not handled")
	}
	if r.Handles(voice(1, 9)) {
		t.Error("an ordinary talkgroup was swallowed by parrot")
	}

	// Wrong timeslot.
	other := voice(1, parrotTG)
	other.Timeslot = hbp.Timeslot1
	if r.Handles(other) {
		t.Error("the parrot talkgroup on the other timeslot was handled")
	}

	// A private call to the parrot number is parrot traffic, on either
	// timeslot. **This test previously asserted the opposite**, which encoded
	// the assumption that parrot is a group service — and a private call is
	// how most networks do it and how most operators program their radios,
	// because it lets somebody test without the whole club hearing them.
	private := voice(1, parrotTG)
	private.CallType = hbp.CallPrivate
	if !r.Handles(private) {
		t.Error("a private call to the parrot number was not handled")
	}
	private.Timeslot = hbp.Timeslot1
	if !r.Handles(private) {
		t.Error("a private call was refused for being on the other timeslot; " +
			"a private call is addressed to a number, not carried on a talkgroup")
	}

	// But a private call to somebody else is nothing to do with parrot.
	elsewhere := voice(1, 3155408)
	elsewhere.CallType = hbp.CallPrivate
	if r.Handles(elsewhere) {
		t.Error("a private call to another radio was swallowed by parrot")
	}
}

// TestAPrivateReplayIsAddressedBackToTheRadio. A radio un-mutes a private call
// only when the target is its own ID, so replaying one with the original
// addressing produces frames it receives and refuses to play — parrot
// appearing to work and sounding like nothing.
func TestAPrivateReplayIsAddressedBackToTheRadio(t *testing.T) {
	r, c := newRecorder(t)

	for i := 0; i < 6; i++ {
		f := voice(0x1234, parrotTG)
		f.CallType = hbp.CallPrivate
		r.Observe(testPeer, f)
		c.advance(parrot.FrameInterval)
	}
	c.advance(3 * time.Second)
	done := r.Expire(c.now())

	if len(done) != 1 {
		t.Fatalf("got %d recordings", len(done))
	}
	for _, f := range done[0].Frames {
		if f.TargetID != testRadio {
			t.Errorf("the replay is addressed to %d, not the radio that called (%d)",
				f.TargetID, testRadio)
		}
		if f.SourceID != parrotTG {
			t.Errorf("the replay comes from %d, want the parrot number %d",
				f.SourceID, parrotTG)
		}
		if f.CallType != hbp.CallPrivate {
			t.Error("the replay is not a private call")
		}
	}
}

// TestAGroupReplayKeepsItsAddressing. The member's display should show what it
// showed when they transmitted, and the talkgroup is what their radio listens
// to.
func TestAGroupReplayKeepsItsAddressing(t *testing.T) {
	r, c := newRecorder(t)
	transmit(r, c, 0x1234, 5)
	c.advance(3 * time.Second)
	done := r.Expire(c.now())

	for _, f := range done[0].Frames {
		if f.SourceID != testRadio || f.TargetID != parrotTG {
			t.Errorf("a group replay was re-addressed: %+v", f)
		}
	}
}

// TestALongTransmissionIsTruncatedNotDiscarded. Hearing thirty seconds of your
// own audio answers the question a member was asking.
func TestALongTransmissionIsTruncatedNotDiscarded(t *testing.T) {
	r, c := newRecorder(t, func(cfg *parrot.Config) { cfg.MaxDuration = time.Second })

	// Two seconds of frames: past the limit.
	rec := transmit(r, c, 0x1234, 34)
	if rec == nil {
		t.Fatal("a transmission past the limit did not complete")
	}
	if !rec.Truncated {
		t.Error("the recording is not marked truncated")
	}
	if len(rec.Frames) == 0 {
		t.Fatal("a long transmission was discarded rather than truncated")
	}
	if len(rec.Frames) > 20 {
		t.Errorf("recorded %d frames for a one-second limit", len(rec.Frames))
	}
}

// TestKeyingUpAgainStartsOver. Pressing PTT again while a recording is being
// made means "start over" far more often than it means anything else.
func TestKeyingUpAgainStartsOver(t *testing.T) {
	r, c := newRecorder(t)

	transmit(r, c, 0x1111, 10)
	// A new stream, without the first having ended.
	transmit(r, c, 0x2222, 4)
	c.advance(3 * time.Second)
	done := r.Expire(c.now())

	if len(done) != 1 {
		t.Fatalf("got %d recordings, want the second only", len(done))
	}
	if len(done[0].Frames) != 4 {
		t.Errorf("the recording has %d frames, want the 4 from the second keyup",
			len(done[0].Frames))
	}
}

// TestTwoHotspotsDoNotCollide. A club may have two members testing at once, and
// each should hear themselves.
func TestTwoHotspotsDoNotCollide(t *testing.T) {
	r, c := newRecorder(t)
	const other = hbp.RepeaterID(3155413)

	for i := 0; i < 8; i++ {
		r.Observe(testPeer, voice(0x1111, parrotTG))
		f := voice(0x2222, parrotTG)
		f.RepeaterID = other
		f.SourceID = 3155413
		r.Observe(other, f)
		c.advance(parrot.FrameInterval)
	}
	if r.Active() != 2 {
		t.Fatalf("%d recordings in progress, want 2", r.Active())
	}

	c.advance(3 * time.Second)
	done := r.Expire(c.now())
	if len(done) != 2 {
		t.Fatalf("got %d recordings, want 2", len(done))
	}

	seen := map[hbp.RepeaterID]int{}
	for _, rec := range done {
		seen[rec.Peer] = len(rec.Frames)
	}
	if seen[testPeer] != 8 || seen[other] != 8 {
		t.Errorf("recordings are %v, want 8 frames each", seen)
	}
}

// TestAReplayIsScheduledAfterAGap. A replay that begins instantly is played at
// a radio that has not finished transmitting and is not listening yet.
func TestAReplayIsScheduledAfterAGap(t *testing.T) {
	r, c := newRecorder(t, func(cfg *parrot.Config) { cfg.Gap = 2 * time.Second })

	transmit(r, c, 0x1234, 5)
	c.advance(3 * time.Second)
	at := c.now()
	done := r.Expire(at)

	if len(done) != 1 {
		t.Fatalf("got %d recordings", len(done))
	}
	if !done[0].PlayAt.Equal(at.Add(2 * time.Second)) {
		t.Errorf("replay scheduled for %v, want %v", done[0].PlayAt, at.Add(2*time.Second))
	}
}

// TestAnEmptyRecordingIsNotReplayed guards against a stray frame producing a
// playback of nothing.
func TestAnEmptyRecordingIsNotReplayed(t *testing.T) {
	r, c := newRecorder(t, func(cfg *parrot.Config) { cfg.MaxDuration = time.Nanosecond })

	// The very first frame is already past the limit, so nothing is captured.
	rec := r.Observe(testPeer, voice(0x1234, parrotTG))
	c.advance(3 * time.Second)
	expired := r.Expire(c.now())

	if rec != nil && len(rec.Frames) > 0 {
		t.Error("frames were captured past a zero limit")
	}
	for _, e := range expired {
		if len(e.Frames) == 0 {
			t.Error("an empty recording was returned for replay")
		}
	}
}

// TestCancelAbandonsARecording. A member who keys up on something else should
// not be surprised by a replay later.
func TestCancelAbandonsARecording(t *testing.T) {
	r, c := newRecorder(t)
	transmit(r, c, 0x1234, 10)

	r.Cancel(testPeer)
	if r.Active() != 0 {
		t.Error("Cancel left the recording in progress")
	}

	c.advance(3 * time.Second)
	if done := r.Expire(c.now()); len(done) != 0 {
		t.Errorf("a cancelled recording was replayed: %d", len(done))
	}
}

// TestARecordingStillRunningIsNotExpired. Ending a transmission that is still
// going would cut a member off mid-sentence and play half of it back.
func TestARecordingStillRunningIsNotExpired(t *testing.T) {
	r, c := newRecorder(t)
	transmit(r, c, 0x1234, 10)

	// Less than the silence timeout.
	c.advance(500 * time.Millisecond)
	if done := r.Expire(c.now()); len(done) != 0 {
		t.Errorf("a running transmission was ended early: %d", len(done))
	}
	if r.Active() != 1 {
		t.Error("the recording was dropped")
	}
}

func TestATalkgroupIsRequired(t *testing.T) {
	if _, err := parrot.New(parrot.Config{Timeslot: hbp.Timeslot2}); err == nil {
		t.Error("a recorder was built with no talkgroup")
	}
}

// TestTheDurationExcludesTheSilence. A recording ended by silence would
// otherwise report the timeout as part of the transmission.
func TestTheDurationExcludesTheSilence(t *testing.T) {
	r, c := newRecorder(t)
	transmit(r, c, 0x1234, 10) // 600ms of frames

	c.advance(5 * time.Second)
	done := r.Expire(c.now())

	if len(done) != 1 {
		t.Fatalf("got %d recordings", len(done))
	}
	if done[0].Duration > time.Second {
		t.Errorf("duration is %v; the silence was counted as transmission", done[0].Duration)
	}
}
