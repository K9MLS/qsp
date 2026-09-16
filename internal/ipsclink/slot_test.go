package ipsclink_test

import (
	"errors"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// TestATargetedTransmissionDoesNotStartOverAnother is the guard that makes
// sending Zello audio to a Motorola repeater safe: one encoder per repeater,
// so two streams on one timeslot interleave into noise on air.
//
// To see rows fail: make claimSlot always return false, and "another stream on
// the same slot" is sent; or drop the terminator case, and "after that call's
// terminator" is refused for a second.
func TestATargetedTransmissionDoesNotStartOverAnother(t *testing.T) {
	voice := func(stream hbp.StreamID, ts hbp.Timeslot) hbp.Data {
		return hbp.Data{SourceID: 3155408, TargetID: 2, Timeslot: ts, CallType: hbp.CallGroup,
			FrameType: hbp.FrameTypeVoice, DataType: 1, StreamID: stream}
	}
	term := func(stream hbp.StreamID, ts hbp.Timeslot) hbp.Data {
		d := voice(stream, ts)
		d.FrameType, d.DataType = hbp.FrameTypeSync, hbp.DataTypeTerminator
		return d
	}
	tests := []struct {
		name    string
		earlier []hbp.Data // sent first, to the same repeater
		pause   time.Duration
		next    hbp.Data
		want    error
	}{
		{"an idle slot", nil, 0, voice(2, hbp.Timeslot2), nil},
		{"the same stream continuing", []hbp.Data{voice(2, hbp.Timeslot2)}, 0, voice(2, hbp.Timeslot2), nil},
		{"another stream on the same slot", []hbp.Data{voice(1, hbp.Timeslot2)}, 0, voice(2, hbp.Timeslot2), ipsclink.ErrSlotBusy},
		{"another stream on the other slot", []hbp.Data{voice(1, hbp.Timeslot1)}, 0, voice(2, hbp.Timeslot2), nil},
		{"right after that call's terminator", []hbp.Data{voice(1, hbp.Timeslot2), term(1, hbp.Timeslot2)}, 0, voice(2, hbp.Timeslot2), nil},
		{"a call that stopped without a terminator, once the hold passes",
			[]hbp.Data{voice(1, hbp.Timeslot2)}, ipsclink.SlotHold + 100*time.Millisecond, voice(2, hbp.Timeslot2), nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, conn := start(t, ipsclink.Config{})
			send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
			expectReply(t, conn, ipsc.KindRegisterReply)

			for _, f := range tc.earlier {
				if err := l.SendVoiceTo(peerID, f); err != nil {
					t.Fatalf("sending the earlier frame: %v", err)
				}
			}
			time.Sleep(tc.pause)
			if err := l.SendVoiceTo(peerID, tc.next); !errors.Is(err, tc.want) {
				t.Errorf("SendVoiceTo = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestSendingToAnIDThatIsNotAMotorolaRepeater is an error a caller can name.
func TestSendingToAnIDThatIsNotAMotorolaRepeater(t *testing.T) {
	l, _ := start(t, ipsclink.Config{})
	err := l.SendVoiceTo(3155408, hbp.Data{StreamID: 1, Timeslot: hbp.Timeslot2})
	if !errors.Is(err, ipsclink.ErrNoSuchPeer) {
		t.Errorf("got %v, want ErrNoSuchPeer", err)
	}
}
