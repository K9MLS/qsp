package ipsclink_test

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// TestATimedOutPeerLeavesNothingBehind: with an empty allow list a sender ID
// is whatever a datagram claims, so every map keyed by one has to shrink when
// the peer times out, or a stranger registering under a new ID each time
// grows it for as long as the server runs.
//
// Each row is what was sent to the peers before they went silent, which
// decides which maps hold something to forget.
//
// To see it fail: delete the `delete(l.relayed, id)` line in forgetPeerLocked
// and the relayed rows fail; delete the maps.DeleteFunc line and the rows
// that leave a timeslot claimed fail.
func TestATimedOutPeerLeavesNothingBehind(t *testing.T) {
	const strangers = 5
	voice := func(stream hbp.StreamID, ts hbp.Timeslot) hbp.Data {
		return hbp.Data{SourceID: 3155408, TargetID: 2, Timeslot: ts, CallType: hbp.CallGroup,
			FrameType: hbp.FrameTypeVoice, DataType: 1, StreamID: stream}
	}
	tests := []struct {
		name string
		send func(t *testing.T, l *ipsclink.Listener)
	}{
		{"nothing was ever sent to them", func(*testing.T, *ipsclink.Listener) {}},
		{"a call relayed to all of them, with no terminator", func(_ *testing.T, l *ipsclink.Listener) {
			l.SendVoice(0, voice(7, hbp.Timeslot2))
		}},
		{"calls relayed on both timeslots", func(_ *testing.T, l *ipsclink.Listener) {
			l.SendVoice(0, voice(7, hbp.Timeslot1))
			l.SendVoice(0, voice(8, hbp.Timeslot2))
		}},
		{"a call sent to each of them alone", func(t *testing.T, l *ipsclink.Listener) {
			for i := range strangers {
				if err := l.SendVoiceTo(uint32(peerID+i), voice(9, hbp.Timeslot2)); err != nil {
					t.Fatalf("sending to peer %d: %v", peerID+i, err)
				}
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, conn := start(t, ipsclink.Config{PeerTimeout: time.Hour})
			for i := range strangers {
				send(t, conn, ipsc.KindRegisterRequest, uint32(peerID+i), registerBody())
				expectReply(t, conn, ipsc.KindRegisterReply)
			}
			waitForPeers(t, l, strangers)
			tc.send(t, l)

			if dropped := l.ExpireAt(time.Now().Add(2 * time.Hour)); dropped != strangers {
				t.Fatalf("dropped %d peers, want %d", dropped, strangers)
			}
			peers, bridges, encoders, relayed, slots := l.PerPeerState()
			if peers+bridges+encoders+relayed+slots != 0 {
				t.Errorf("after every peer timed out: %d peers, %d converters, %d encoders, "+
					"%d relayed streams, %d timeslot claims; want none of any",
					peers, bridges, encoders, relayed, slots)
			}
		})
	}
}
