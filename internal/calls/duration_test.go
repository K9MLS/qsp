package calls_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestAMergedCallCannotEndBeforeItStarted reproduces a negative duration seen
// on a live dashboard.
//
// Expire sorted by peer and stream ID, for reproducibility, which is not
// chronological. Merging assigns the finished call's end time to the entry it
// merges into, so a call whose last frame arrived *earlier* but expired *later*
// dragged that end time backwards — and the console rendered **-470ms**.
//
// The stream IDs below are chosen so that sorting by ID puts them in the
// opposite order to time, which is exactly the arrangement a real text message
// produces: MMDVM's stream IDs are effectively random.
func TestAMergedCallCannotEndBeforeItStarted(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 50, Timeout: time.Second})
	base := time.Now().UTC()

	// Three one-frame data bursts, arriving in time order, with stream IDs
	// that sort the other way.
	for i, sid := range []hbp.StreamID{0xF000, 0x8000, 0x1000} {
		tr.Update(peerID, hbp.Data{
			RepeaterID: peerID, SourceID: uint32(peerID), TargetID: 3100,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
			FrameType: hbp.FrameTypeSync, DataType: 0x3, // a text burst
			StreamID: sid,
		}, base.Add(time.Duration(i)*100*time.Millisecond))
	}

	for _, c := range tr.Expire(base.Add(5 * time.Second)) {
		if c.Ended.Before(c.Started) {
			t.Errorf("a call ended %v before it started; the console renders "+
				"that as a negative duration",
				c.Started.Sub(c.Ended))
		}
		if d := c.Duration(base.Add(5 * time.Second)); d < 0 {
			t.Errorf("duration is %v; a transmission cannot last less than no time", d)
		}
	}

	for _, c := range tr.History() {
		if c.Ended.Before(c.Started) {
			t.Errorf("a history entry ends %v before it starts",
				c.Started.Sub(c.Ended))
		}
	}
}

// TestExpiryIsOldestFirst pins the ordering the fix depends on.
//
// Reproducible order was the original goal and is kept — peer and stream still
// break ties — but time comes first, because merging assumes the calls arrive
// in the order they happened.
func TestExpiryIsOldestFirst(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 50, Timeout: time.Second})
	base := time.Now().UTC()

	// Voice streams, so nothing merges and each expires as its own entry.
	for i, sid := range []hbp.StreamID{0xF000, 0x8000, 0x1000} {
		tr.Update(peerID, hbp.Data{
			RepeaterID: peerID, SourceID: uint32(peerID), TargetID: 3100,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
			FrameType: hbp.FrameTypeVoice, StreamID: sid,
		}, base.Add(time.Duration(i)*100*time.Millisecond))
	}

	lost := tr.Expire(base.Add(5 * time.Second))
	if len(lost) != 3 {
		t.Fatalf("%d calls expired, want 3", len(lost))
	}
	for i := 1; i < len(lost); i++ {
		if lost[i].Ended.Before(lost[i-1].Ended) {
			t.Errorf("expired call %d ended before call %d; the sweep must "+
				"report them oldest first", i, i-1)
		}
	}
}
