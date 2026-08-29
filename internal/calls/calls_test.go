package calls_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

const peerID = hbp.RepeaterID(3132910)

var base = time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)

// frame builds a voice frame of the given type.
func frame(stream hbp.StreamID, ft hbp.FrameType) hbp.Data {
	return hbp.Data{
		RepeaterID: peerID,
		SourceID:   uint32(peerID),
		TargetID:   3100,
		Timeslot:   hbp.Timeslot2,
		CallType:   hbp.CallGroup,
		FrameType:  ft,
		StreamID:   stream,
	}
}

// TestNormalTransmission covers the shape every fixture stream has: a sync
// header, voice frames, a sync terminator.
func TestNormalTransmission(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})
	now := base

	started, ended := tr.Update(peerID, frame(0xAAAA, hbp.FrameTypeSync), now)
	if started == nil {
		t.Fatal("the first frame did not start a call")
	}
	if ended != nil {
		t.Fatal("the opening sync frame ended the call immediately")
	}
	if started.Source != uint32(peerID) || started.Target != 3100 || !started.Group {
		t.Errorf("call fields are wrong: %+v", started)
	}

	for i := 0; i < 20; i++ {
		now = now.Add(60 * time.Millisecond)
		if s, e := tr.Update(peerID, frame(0xAAAA, hbp.FrameTypeVoice), now); s != nil || e != nil {
			t.Fatalf("a mid-stream frame started or ended a call: started=%v ended=%v", s, e)
		}
	}

	now = now.Add(60 * time.Millisecond)
	_, ended = tr.Update(peerID, frame(0xAAAA, hbp.FrameTypeSync), now)
	if ended == nil {
		t.Fatal("the terminator did not end the call")
	}
	if ended.EndReason != calls.EndTerminated {
		t.Errorf("end reason = %q, want %q", ended.EndReason, calls.EndTerminated)
	}
	if ended.Frames != 22 {
		t.Errorf("frames = %d, want 22", ended.Frames)
	}
	// 22 frames span 21 intervals, not 22: the first arrives at t=0.
	if want := 21 * 60 * time.Millisecond; ended.Duration(now) != want {
		t.Errorf("duration = %s, want %s", ended.Duration(now), want)
	}
	if tr.ActiveCount() != 0 {
		t.Error("the call is still active after its terminator")
	}
	if len(tr.History()) != 1 {
		t.Errorf("history holds %d calls, want 1", len(tr.History()))
	}
}

// TestMissingTerminatorTimesOut is the failure this package exists to catch.
//
// A stream that stops without a terminator would otherwise stay open forever,
// which in a bridging context is how a talkgroup gets welded open.
func TestMissingTerminatorTimesOut(t *testing.T) {
	tr := calls.NewTracker(calls.Options{Timeout: 2 * time.Second})
	now := base

	tr.Update(peerID, frame(0xBBBB, hbp.FrameTypeSync), now)
	for i := 0; i < 5; i++ {
		now = now.Add(60 * time.Millisecond)
		tr.Update(peerID, frame(0xBBBB, hbp.FrameTypeVoice), now)
	}
	lastFrame := now

	// The peer vanishes. Nothing more arrives.
	if lost := tr.Expire(now.Add(1900 * time.Millisecond)); len(lost) != 0 {
		t.Fatalf("call expired before the timeout: %+v", lost)
	}

	lost := tr.Expire(now.Add(2100 * time.Millisecond))
	if len(lost) != 1 {
		t.Fatalf("got %d timed-out calls, want 1", len(lost))
	}
	if lost[0].EndReason != calls.EndTimedOut {
		t.Errorf("end reason = %q, want %q", lost[0].EndReason, calls.EndTimedOut)
	}
	// The call ended when its last frame arrived, not when the sweep noticed.
	// Recording the sweep time would inflate every lost call by the timeout.
	if !lost[0].Ended.Equal(lastFrame) {
		t.Errorf("ended at %s, want the last frame's time %s", lost[0].Ended, lastFrame)
	}
	if tr.ActiveCount() != 0 {
		t.Error("the timed-out call is still active")
	}
}

// TestSingleFrameTransmissionStartsAndEnds covers the degenerate case.
func TestSingleFrameTransmissionStartsAndEnds(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})

	started, ended := tr.Update(peerID, frame(0xCCCC, hbp.FrameTypeSync), base)
	if started == nil {
		t.Fatal("no call started")
	}
	if ended != nil {
		t.Fatal("a lone opening frame ended the call; it cannot be both header and terminator")
	}

	// A second sync frame closes it.
	_, ended = tr.Update(peerID, frame(0xCCCC, hbp.FrameTypeSync), base.Add(60*time.Millisecond))
	if ended == nil {
		t.Fatal("the second sync frame did not end the call")
	}
	if ended.Frames != 2 {
		t.Errorf("frames = %d, want 2", ended.Frames)
	}
}

// TestConcurrentTimeslots proves DMR's two simultaneous conversations per peer
// are tracked separately.
func TestConcurrentTimeslots(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})

	ts1 := frame(0xD1D1, hbp.FrameTypeSync)
	ts1.Timeslot = hbp.Timeslot1
	ts2 := frame(0xD2D2, hbp.FrameTypeSync)
	ts2.Timeslot = hbp.Timeslot2

	if s, _ := tr.Update(peerID, ts1, base); s == nil {
		t.Fatal("timeslot 1 call did not start")
	}
	if s, _ := tr.Update(peerID, ts2, base); s == nil {
		t.Fatal("timeslot 2 call did not start")
	}
	if tr.ActiveCount() != 2 {
		t.Fatalf("active = %d, want 2 simultaneous calls", tr.ActiveCount())
	}

	// Ending one leaves the other running.
	tr.Update(peerID, ts1, base.Add(60*time.Millisecond))
	if tr.ActiveCount() != 1 {
		t.Errorf("active = %d after ending one timeslot, want 1", tr.ActiveCount())
	}
}

// TestSameStreamIDFromDifferentPeersIsDistinct.
//
// Stream IDs are chosen by the transmitting peer and are not globally unique,
// so peer must be part of the identity.
func TestSameStreamIDFromDifferentPeersIsDistinct(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})

	if s, _ := tr.Update(3132910, frame(0xEEEE, hbp.FrameTypeSync), base); s == nil {
		t.Fatal("first peer's call did not start")
	}
	if s, _ := tr.Update(3121380, frame(0xEEEE, hbp.FrameTypeSync), base); s == nil {
		t.Fatal("second peer's call with the same stream ID was merged into the first")
	}
	if tr.ActiveCount() != 2 {
		t.Errorf("active = %d, want 2", tr.ActiveCount())
	}
}

// TestHistoryIsBoundedAndOrdered.
func TestHistoryIsBoundedAndOrdered(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 3})

	for i := 0; i < 10; i++ {
		stream := hbp.StreamID(0x1000 + i)
		at := base.Add(time.Duration(i) * time.Second)
		tr.Update(peerID, frame(stream, hbp.FrameTypeSync), at)
		tr.Update(peerID, frame(stream, hbp.FrameTypeSync), at.Add(100*time.Millisecond))
	}

	h := tr.History()
	if len(h) != 3 {
		t.Fatalf("history holds %d calls, want 3", len(h))
	}
	// Most recent first.
	if h[0].Key.Stream != 0x1009 {
		t.Errorf("history[0] stream = %s, want the most recent", h[0].Key.Stream)
	}
	if h[2].Key.Stream != 0x1007 {
		t.Errorf("history[2] stream = %s, want the oldest retained", h[2].Key.Stream)
	}
}

// TestActiveCallLimitBoundsMemory.
//
// A peer sending frames with an endless supply of stream IDs must not be able
// to exhaust memory through the observer.
func TestActiveCallLimitBoundsMemory(t *testing.T) {
	tr := calls.NewTracker(calls.Options{MaxActive: 4})

	for i := 0; i < 50; i++ {
		tr.Update(peerID, frame(hbp.StreamID(i), hbp.FrameTypeSync), base)
	}
	if tr.ActiveCount() > 4 {
		t.Errorf("active = %d, want at most 4", tr.ActiveCount())
	}
}

// TestExpireIsDeterministic keeps logs and tests reproducible.
//
// Voice frames rather than bare sync ones: three single-frame streams from one
// radio to one target within a second are a text message, and the tracker now
// merges those into a single entry. This test is about the order things expire
// in, so it uses transmissions that cannot be mistaken for one event.
func TestExpireIsDeterministic(t *testing.T) {
	tr := calls.NewTracker(calls.Options{Timeout: time.Second})
	for _, s := range []hbp.StreamID{0x30, 0x10, 0x20} {
		tr.Update(peerID, frame(s, hbp.FrameTypeVoiceSync), base)
	}
	lost := tr.Expire(base.Add(2 * time.Second))
	if len(lost) != 3 {
		t.Fatalf("got %d, want 3", len(lost))
	}
	for i, want := range []hbp.StreamID{0x10, 0x20, 0x30} {
		if lost[i].Key.Stream != want {
			t.Errorf("lost[%d] = %s, want %s (order must be stable)", i, lost[i].Key.Stream, want)
		}
	}
}

// TestPrivateCallIsRecorded.
func TestPrivateCallIsRecorded(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})
	f := frame(0xF0F0, hbp.FrameTypeSync)
	f.CallType = hbp.CallPrivate

	started, _ := tr.Update(peerID, f, base)
	if started == nil {
		t.Fatal("no call started")
	}
	if started.Group {
		t.Error("a private call was recorded as a group call")
	}
}

// TestInProgressAndDuration.
func TestInProgressAndDuration(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})
	tr.Update(peerID, frame(0xABCD, hbp.FrameTypeSync), base)

	active := tr.Active()
	if len(active) != 1 {
		t.Fatalf("active = %d, want 1", len(active))
	}
	if !active[0].InProgress() {
		t.Error("an unfinished call reports itself complete")
	}
	if got := active[0].Duration(base.Add(3 * time.Second)); got != 3*time.Second {
		t.Errorf("in-progress duration = %s, want 3s", got)
	}
}

// TestUpdateReturnsSnapshotsNotLiveState.
func TestUpdateReturnsSnapshotsNotLiveState(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})
	started, _ := tr.Update(peerID, frame(0x1234, hbp.FrameTypeSync), base)

	started.Frames = 9999
	started.Source = 1

	active := tr.Active()
	if active[0].Frames != 1 {
		t.Errorf("tracker state was changed through a returned call: frames = %d", active[0].Frames)
	}
	if active[0].Source != uint32(peerID) {
		t.Error("tracker state was changed through a returned call: source")
	}
}

// TestTimesAreUTC keeps the console and logs consistent.
func TestTimesAreUTC(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})
	local := time.Date(2026, 8, 23, 15, 0, 0, 0, time.FixedZone("CDT", -5*3600))

	started, _ := tr.Update(peerID, frame(0x5555, hbp.FrameTypeSync), local)
	if started.Started.Location() != time.UTC {
		t.Errorf("call start is in %v, want UTC", started.Started.Location())
	}
}

// TestADataBurstIsNotVoice.
//
// A text message is a handful of one-frame data bursts, each with its own
// stream ID, so each becomes a call. Fifty of them buried the voice traffic
// the console exists to show, and every one was marked "no terminator" — which
// is a false alarm, because a single burst has no terminator and is not meant
// to have one.
func TestADataBurstIsNotVoice(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	burst := hbp.Data{
		SourceID: 3155413, TargetID: 3132910, Timeslot: hbp.Timeslot2,
		CallType: hbp.CallPrivate, FrameType: hbp.FrameTypeSync, StreamID: 0x1111,
	}
	started, _ := tr.Update(3155413, burst, now)
	if started == nil {
		t.Fatal("the burst was not tracked")
	}
	if started.Voice {
		t.Error("a data burst was recorded as voice")
	}
}

func TestAVoiceTransmissionIsVoice(t *testing.T) {
	tr := calls.NewTracker(calls.Options{})
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)

	header := hbp.Data{
		SourceID: 3132910, TargetID: 2, Timeslot: hbp.Timeslot2,
		CallType: hbp.CallGroup, FrameType: hbp.FrameTypeSync, StreamID: 0x2222,
	}
	tr.Update(3132910, header, now)

	voice := header
	voice.FrameType = hbp.FrameTypeVoiceSync
	tr.Update(3132910, voice, now.Add(60*time.Millisecond))

	active := tr.Active()
	if len(active) != 1 {
		t.Fatalf("%d active calls", len(active))
	}
	if !active[0].Voice {
		t.Error("a voice transmission was not recorded as voice")
	}
}

// TestATextMessageIsOneEntry.
//
// A text is a sequence of one-frame data bursts, each with its own stream ID,
// so each completed as its own call — and fifty of them pushed every voice
// transmission out of a history that holds fifty. Observed on a live network
// while two members exchanged messages.
func TestATextMessageIsOneEntry(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 50, Timeout: time.Second})
	at := base

	for i := 0; i < 30; i++ {
		burst := frame(hbp.StreamID(0x2000+i), hbp.FrameTypeSync)
		tr.Update(peerID, burst, at)
		at = at.Add(120 * time.Millisecond)
	}
	tr.Expire(at.Add(2 * time.Second))

	h := tr.History()
	if len(h) != 1 {
		t.Fatalf("history holds %d entries for one text message, want 1", len(h))
	}
	if h[0].Frames != 30 {
		t.Errorf("the merged entry counts %d frames, want 30", h[0].Frames)
	}
	if h[0].Voice {
		t.Error("a merged data entry was marked as voice")
	}
}

// TestVoiceIsNotEvictedByATextMessage is the damage the merge prevents: a
// history that holds fifty entries loses everything that matters when one
// message arrives.
func TestVoiceIsNotEvictedByATextMessage(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 5, Timeout: time.Second})
	at := base

	// Somebody talks.
	tr.Update(peerID, frame(0x1111, hbp.FrameTypeVoiceSync), at)
	at = at.Add(60 * time.Millisecond)
	tr.Update(peerID, frame(0x1111, hbp.FrameTypeVoiceSync), at)
	at = at.Add(3 * time.Second)
	tr.Expire(at)

	// Then a text message goes by.
	for i := 0; i < 30; i++ {
		tr.Update(peerID, frame(hbp.StreamID(0x3000+i), hbp.FrameTypeSync), at)
		at = at.Add(120 * time.Millisecond)
	}
	tr.Expire(at.Add(2 * time.Second))

	var sawVoice bool
	for _, c := range tr.History() {
		if c.Voice {
			sawVoice = true
		}
	}
	if !sawVoice {
		t.Error("the voice transmission was evicted by a text message")
	}
}

// TestTwoMessagesApartAreTwoEntries. Two texts a minute apart are two things
// that happened and should read as two.
func TestTwoMessagesApartAreTwoEntries(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 50, Timeout: time.Second})

	at := base
	for i := 0; i < 3; i++ {
		tr.Update(peerID, frame(hbp.StreamID(0x4000+i), hbp.FrameTypeSync), at)
		at = at.Add(120 * time.Millisecond)
	}
	tr.Expire(at.Add(2 * time.Second))

	at = at.Add(time.Minute)
	for i := 0; i < 3; i++ {
		tr.Update(peerID, frame(hbp.StreamID(0x5000+i), hbp.FrameTypeSync), at)
		at = at.Add(120 * time.Millisecond)
	}
	tr.Expire(at.Add(2 * time.Second))

	if h := tr.History(); len(h) != 2 {
		t.Errorf("history holds %d entries for two messages a minute apart, want 2", len(h))
	}
}

// TestAMultiFrameStreamIsNotMerged. Only a single-frame burst merges; anything
// with structure is a transmission that carried no audio rather than a burst of
// data, and merging those would hide them.
func TestAMultiFrameStreamIsNotMerged(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 50, Timeout: time.Second})
	at := base

	for i := 0; i < 3; i++ {
		stream := hbp.StreamID(0x6000 + i)
		tr.Update(peerID, frame(stream, hbp.FrameTypeSync), at)
		tr.Update(peerID, frame(stream, hbp.FrameTypeSync), at.Add(60*time.Millisecond))
		at = at.Add(200 * time.Millisecond)
	}
	tr.Expire(at.Add(2 * time.Second))

	if h := tr.History(); len(h) != 3 {
		t.Errorf("history holds %d entries, want 3; multi-frame streams were merged", len(h))
	}
}
