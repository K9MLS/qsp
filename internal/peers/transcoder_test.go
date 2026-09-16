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
