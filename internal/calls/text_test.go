package calls_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestATextIsOneCallNotTen covers a defect the IPSC text work created.
//
// A call ended on any data sync frame after the first. That was complete while
// a data burst could only be a voice LC header or a terminator, and it survived
// the Homebrew text shape by accident: those bursts each carry their own stream
// ID and are one frame each, so the frame count never passed one and this never
// fired.
//
// **An IPSC text is a run of bursts sharing one stream ID.** Every second one
// ended the call and opened another, and one message produced ten entries in a
// fifty-entry history — which is how voice gets pushed out of the list this
// exists to show, and is the same damage that was fixed once already for the
// other shape.
func TestATextIsOneCallNotTen(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 50, Timeout: 2 * time.Second})
	now := time.Now()

	// Twenty bursts, one stream ID, as ipscbridge.ConvertText produces.
	for i := range 20 {
		tr.Update(999999, hbp.Data{
			RepeaterID: 999999, SourceID: 3132910, TargetID: 2,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
			FrameType: hbp.FrameTypeSync, DataType: 0x3, // CSBK
			StreamID: 0xAAAA,
		}, now.Add(time.Duration(i)*60*time.Millisecond))
	}

	if got := len(tr.History()); got != 0 {
		t.Errorf("a text in progress produced %d completed calls; it is one "+
			"transmission and has not finished", got)
	}
	if got := tr.ActiveCount(); got != 1 {
		t.Fatalf("a text produced %d active calls, want exactly one", got)
	}

	// And it must not be marked as voice, so the console can tell them apart.
	for _, c := range tr.Active() {
		if c.Voice {
			t.Error("a text message was recorded as voice")
		}
		if c.Frames != 20 {
			t.Errorf("the call counted %d frames, want all 20 bursts", c.Frames)
		}
	}
}

// TestAVoiceTransmissionStillEndsOnItsTerminator is the other half.
//
// Requiring a real terminator must not stop one working, or audio would sit
// open until the timeout and the next person would wait to key up.
func TestAVoiceTransmissionStillEndsOnItsTerminator(t *testing.T) {
	tr := calls.NewTracker(calls.Options{History: 50, Timeout: 2 * time.Second})
	now := time.Now()

	tr.Update(peerID, frame(0x1234, hbp.FrameTypeSync), now) // voice LC header
	for i := range 10 {
		tr.Update(peerID, frame(0x1234, hbp.FrameTypeVoice),
			now.Add(time.Duration(i+1)*60*time.Millisecond))
	}
	_, ended := tr.Update(peerID, terminator(0x1234), now.Add(time.Second))

	if ended == nil {
		t.Fatal("the terminator did not end the transmission")
	}
	if ended.EndReason != calls.EndTerminated {
		t.Errorf("end reason %q, want %q", ended.EndReason, calls.EndTerminated)
	}
	if !ended.Voice {
		t.Error("a voice transmission was not recorded as voice")
	}
}
