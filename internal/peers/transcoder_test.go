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

// TestTranscodedAudioReachesTheMotorolaRepeatersThatAgreed.
//
// Until 2026-09-16 transcoded audio went to no Motorola repeater, and a Zello
// user was never heard on a repeater whose owner had agreed. It goes to the
// ones the transcoder's permission covers, and no others.
//
// To see rows fail, break the implementation deliberately:
//   - make offerTranscodedToIPSC send to IPSCPeers() whatever the permission:
//     "a list" also reaches 315000, which never agreed
//   - put back the early return that withheld from all Motorola repeaters:
//     every row that wants a send fails
func TestTranscodedAudioReachesTheMotorolaRepeatersThatAgreed(t *testing.T) {
	const (
		blake  = uint32(315544)
		other  = uint32(315000)
		hotspt = uint32(3132910) // a Homebrew peer, not a Motorola repeater
	)
	tests := []struct {
		name     string
		perm     routing.Permission
		want     []uint32
		wantLogs int
	}{
		{"every repeater agreed", routing.Permission{All: true}, []uint32{blake, other}, 0},
		{"a list naming one Motorola repeater", routing.Permission{Peers: []hbp.RepeaterID{hbp.RepeaterID(blake)}}, []uint32{blake}, 0},
		{"a list naming only a Homebrew peer", routing.Permission{Peers: []hbp.RepeaterID{hbp.RepeaterID(hotspt)}}, nil, 0},
		{"nobody agreed", routing.Permission{}, nil, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			table, err := routing.NewTable([]routing.Bridge{{Name: "zello", Enabled: true, Endpoints: []routing.Endpoint{
				{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
				{Transcoder: "dvstick", Talkgroup: 2, Timeslot: hbp.Timeslot2},
			}}}, routing.WithPermissions(map[string]routing.Permission{"dvstick": tc.perm}))
			if err != nil {
				t.Fatalf("building a table: %v", err)
			}
			core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: noPeers{}})
			if err != nil {
				t.Fatal(err)
			}
			l, buf := journal()
			l.cfg.Routing = core
			sent := map[uint32]int{}
			l.SetIPSCTargets(func(target uint32, _ hbp.Data) error {
				if target != blake && target != other {
					return nil // what the app's wrapper does with ErrNoSuchPeer
				}
				sent[target]++
				return nil
			}, func() []uint32 { return []uint32{blake, other} })

			for range 20 {
				l.DeliverFromTranscoder("dvstick", hbp.Data{SourceID: 3155408, TargetID: 2,
					Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup, FrameType: hbp.FrameTypeVoice, StreamID: 5})
			}
			for _, id := range tc.want {
				if sent[id] != 20 {
					t.Errorf("repeater %d received %d of 20 frames", id, sent[id])
				}
			}
			if len(sent) != len(tc.want) {
				t.Errorf("sent to %v, want only %v", sent, tc.want)
			}
			if n := strings.Count(buf.String(), "not sent to a Motorola repeater"); n != tc.wantLogs {
				t.Errorf("logged %d refusals, want %d", n, tc.wantLogs)
			}
		})
	}
}

// TestABusyMotorolaSlotIsSaidOnce: every frame of the transmission is refused,
// and one line says so.
func TestABusyMotorolaSlotIsSaidOnce(t *testing.T) {
	table, _ := routing.NewTable([]routing.Bridge{{Name: "zello", Enabled: true, Endpoints: []routing.Endpoint{
		{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot2},
		{Transcoder: "dvstick", Talkgroup: 2, Timeslot: hbp.Timeslot2},
	}}}, routing.WithPermissions(map[string]routing.Permission{"dvstick": {All: true}}))
	core, _ := routing.NewCore(routing.CoreOptions{Table: table, Peers: noPeers{}})
	l, buf := journal()
	l.cfg.Routing = core
	l.SetIPSCTargets(func(uint32, hbp.Data) error {
		return errors.New("that repeater's timeslot is carrying another transmission")
	},
		func() []uint32 { return []uint32{315544} })
	for range 20 {
		l.DeliverFromTranscoder("dvstick", hbp.Data{SourceID: 3155408, TargetID: 2, Timeslot: hbp.Timeslot2,
			CallType: hbp.CallGroup, FrameType: hbp.FrameTypeVoice, StreamID: 5})
	}
	if n := strings.Count(buf.String(), "not sent to a Motorola repeater"); n != 1 {
		t.Errorf("logged %d times over 20 frames, want once", n)
	}
}

type noPeers struct{}

func (noPeers) Ready(hbp.RepeaterID) bool    { return false }
func (noPeers) ReadyPeers() []hbp.RepeaterID { return nil }

// failingUpstreams refuses every frame, as a stopped link does.
type failingUpstreams struct{}

func (failingUpstreams) Send(string, hbp.Data) error {
	return errors.New(`upstream "BCARA": the link is not running`)
}

// TestADeadUpstreamIsReportedOnceNotPerFrame: on 2026-09-16 this warning ran at
// one line every 60 ms and buried the line that said why BCARA had stopped.
//
// To see it bite: remove the noteRoutingDrop guard around "cannot send a frame
// upstream" and fifty frames log fifty lines.
func TestADeadUpstreamIsReportedOnceNotPerFrame(t *testing.T) {
	l, buf := journal()
	l.cfg.Upstreams = failingUpstreams{}
	res := routing.Result{Upstreams: []routing.UpstreamDelivery{{Upstream: "BCARA", Bridge: "tg2",
		Frame: hbp.Data{TargetID: 2, Timeslot: hbp.Timeslot2}}}}
	for range 50 {
		l.deliver(312345, res)
	}
	if n := strings.Count(buf.String(), "cannot send a frame upstream"); n != 1 {
		t.Errorf("logged %d times over 50 frames, want once", n)
	}
	if got := l.writeErr.Load(); got != 50 {
		t.Errorf("counted %d failures, want all 50: the count is what the console shows", got)
	}
}
