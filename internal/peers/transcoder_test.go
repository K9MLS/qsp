package peers

import (
	"errors"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// recordingTranscoders records what the listener hands to vocoder channels.
type recordingTranscoders struct {
	got []string
	err error
}

func (r *recordingTranscoders) Send(name string, _ hbp.Data) error {
	r.got = append(r.got, name)
	return r.err
}

// TestTranscoderDeliveriesReachTheSenderAndFailuresAreSaidOnce.
//
// The routing core producing a transcoder delivery is half of the wiring;
// this is the other half, which is where "declared and read by nothing" has
// happened nine times in this project.
//
// To see a row fail:
//   - delete the Transcoders loop in deliver: "a sender" receives nothing
//   - remove the noteRoutingDrop guard around the Send warning: "a full
//     queue" logs thirty lines
func TestTranscoderDeliveriesReachTheSenderAndFailuresAreSaidOnce(t *testing.T) {
	delivery := routing.Result{Transcoders: []routing.TranscoderDelivery{{
		Transcoder: "dvstick", Bridge: "zello",
		Frame: hbp.Data{TargetID: 2, Timeslot: hbp.Timeslot2},
	}}}
	tests := []struct {
		name          string
		sender        *recordingTranscoders
		noSender      bool
		wantSent      int
		wantForwarded uint64
		wantLine      string
	}{
		{name: "a sender receives every frame", sender: &recordingTranscoders{},
			wantSent: 30, wantForwarded: 30},
		{name: "a full queue is logged once, not per frame",
			sender:   &recordingTranscoders{err: errors.New("the transcoder's queue is full")},
			wantSent: 30, wantLine: "cannot hand a frame to a transcoder"},
		{name: "no sender is reported, not ignored", noSender: true,
			wantLine: "no transcoder channels are running"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, buf := journal()
			if !tc.noSender {
				l.cfg.Transcoders = tc.sender
			}
			for range 30 {
				l.deliver(312345, delivery)
			}
			if tc.sender != nil && len(tc.sender.got) != tc.wantSent {
				t.Errorf("the sender received %d frames, want %d", len(tc.sender.got), tc.wantSent)
			}
			if got := l.forwarded.Load(); got != tc.wantForwarded {
				t.Errorf("forwarded %d, want %d", got, tc.wantForwarded)
			}
			if tc.wantLine != "" {
				if n := strings.Count(buf.String(), tc.wantLine); n != 1 {
					t.Errorf("%q logged %d times over 30 frames, want once", tc.wantLine, n)
				}
			}
		})
	}
}

// TestTranscodedAudioIsNeverOfferedToMotorolaRepeaters.
//
// Every other ingress ends in sendToIPSC, which reaches every IPSC repeater
// with no permission check. A Zello user's audio must not.
//
// To see it bite: add `l.sendToIPSC(0, frame, res)` to DeliverFromTranscoder.
func TestTranscodedAudioIsNeverOfferedToMotorolaRepeaters(t *testing.T) {
	table, err := routing.NewTable([]routing.Bridge{{Name: "zello", Enabled: true, Endpoints: []routing.Endpoint{
		{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
		{Transcoder: "dvstick", Talkgroup: 2, Timeslot: hbp.Timeslot2},
	}}}, routing.WithPermissions(map[string]routing.Permission{"dvstick": {All: true}}))
	if err != nil {
		t.Fatalf("building a table: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: noPeers{}})
	if err != nil {
		t.Fatalf("building a core: %v", err)
	}
	l, buf := journal()
	l.cfg.Routing = core
	offered := 0
	l.cfg.IPSC = func(uint32, hbp.Data) { offered++ }

	for range 20 {
		l.DeliverFromTranscoder("dvstick", hbp.Data{SourceID: 3100999, TargetID: 2,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup, FrameType: hbp.FrameTypeVoice, StreamID: 5})
	}
	if offered != 0 {
		t.Errorf("transcoded audio was offered to the Motorola side %d times", offered)
	}
	if n := strings.Count(buf.String(), "not offered to Motorola repeaters"); n != 1 {
		t.Errorf("the withholding was logged %d times, want once", n)
	}
}

type noPeers struct{}

func (noPeers) Ready(hbp.RepeaterID) bool    { return false }
func (noPeers) ReadyPeers() []hbp.RepeaterID { return nil }
