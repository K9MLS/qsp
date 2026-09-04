package parrot_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestParrotDoesNotSwallowATextMessage covers a defect that text created.
//
// `Handles` tested the talkgroup, the call type and the timeslot, which was
// complete while IP Site Connect carried only voice. Since ADR-0045 a text
// arrives as a data burst and one addressed to the parrot number matched every
// condition.
//
// **The private case lost messages.** A private call to the parrot number
// matches on either timeslot, so any private text to that radio ID was
// consumed, never routed and never delivered — while the sender's radio still
// reported success, because the repeater acknowledges on RF one hop away and a
// master is not part of that. Silent, plausible, invisible.
func TestParrotDoesNotSwallowATextMessage(t *testing.T) {
	const parrotTG = 9990

	r, err := parrot.New(parrot.Config{
		Talkgroup: parrotTG, Timeslot: hbp.Timeslot2,
		MaxDuration: 30 * time.Second, Gap: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	for _, tc := range []struct {
		name  string
		frame hbp.Data
		want  bool
		why   string
	}{
		{
			name: "audio on the parrot talkgroup",
			frame: hbp.Data{TargetID: parrotTG, CallType: hbp.CallGroup,
				Timeslot: hbp.Timeslot2, FrameType: hbp.FrameTypeVoiceSync},
			want: true,
			why:  "this is what parrot is for",
		},
		{
			name: "the voice header that opens it",
			frame: hbp.Data{TargetID: parrotTG, CallType: hbp.CallGroup,
				Timeslot: hbp.Timeslot2, FrameType: hbp.FrameTypeSync, DataType: 0x1},
			want: true,
			why: "a header belongs to the transmission; refusing it would send " +
				"the start of an echo test to the whole network",
		},
		{
			name: "the terminator that closes it",
			frame: hbp.Data{TargetID: parrotTG, CallType: hbp.CallGroup,
				Timeslot: hbp.Timeslot2, FrameType: hbp.FrameTypeSync, DataType: 0x2},
			want: true,
			why:  "a terminator belongs to the transmission for the same reason",
		},
		{
			name: "a text to the parrot talkgroup",
			frame: hbp.Data{TargetID: parrotTG, CallType: hbp.CallGroup,
				Timeslot: hbp.Timeslot2, FrameType: hbp.FrameTypeSync, DataType: 0x3},
			want: false,
			why:  "parrot would record a text and play it back, which answers nobody",
		},
		{
			name: "a private text to the parrot number, other timeslot",
			frame: hbp.Data{TargetID: parrotTG, CallType: hbp.CallPrivate,
				Timeslot: hbp.Timeslot1, FrameType: hbp.FrameTypeSync, DataType: 0x7},
			want: false,
			why: "this is the case that lost messages: consumed, never routed, " +
				"never delivered, and the sender's radio said it worked",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Handles(tc.frame); got != tc.want {
				t.Errorf("Handles is %v, want %v: %s", got, tc.want, tc.why)
			}
		})
	}
}
