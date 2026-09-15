package peers

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// TestARadioIsNotLoggedAsAPeer is the defect an operator found by reading two
// lines about one transmission.
//
// The journal said this, for a single stream:
//
//	call started           subsystem=network peer_id=3132910 stream_id=1317457390
//	relaying transmission  subsystem=network peer_id=3132913 to=3127045/TG2/TS2
//
// **Both were labelled peer_id and neither was wrong about its value.** The
// first is the radio that keyed up; the second is the peer the frame arrived
// from. `logging.PeerID`'s own documentation invited it — "a DMR/P25 radio or
// peer ID" — and on this operator's network the two numbers are 3132910 and
// 3132913, four apart, which is the worst case for noticing.
//
// A log field's name is a claim, the same way a counter's is. Nothing asserted
// this before, which is why it could drift: the helper was used correctly at
// fifteen sites and incorrectly at six, and every test passed.
func TestARadioIsNotLoggedAsAPeer(t *testing.T) {
	const (
		radio = uint32(3132910) // the radio that keyed up
		peer  = hbp.RepeaterID(3132913)
	)

	l, buf := journal()
	l.deliver(peer, routing.Result{
		StartedStreams: []routing.Endpoint{
			{Peer: 3127045, Talkgroup: 2, Timeslot: hbp.Timeslot2},
		},
	})
	relay := buf.String()

	// The relay line is about the peer the frame came from, so peer_id is
	// right there and the radio must not appear.
	if !strings.Contains(relay, "peer_id=3132913") {
		t.Errorf("the relay line does not name the peer it arrived from: %s", relay)
	}
	if strings.Contains(relay, "source=") {
		t.Errorf("the relay line names a source radio it does not know: %s", relay)
	}

	// And a call line is about the radio, so it must use source and must not
	// put a radio ID under peer_id. Driven through observe, which is the path
	// the journal line actually comes from — a helper invented for the test
	// would be a test of the helper.
	l2, buf2 := journal()
	l2.cfg.Calls = calls.NewTracker(calls.Options{})
	l2.observe(peer, hbp.Data{
		SourceID:  radio,
		TargetID:  2,
		Timeslot:  hbp.Timeslot2,
		CallType:  hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync,
		DataType:  hbp.DataTypeVoiceLCHeader,
		StreamID:  0x4e86cdee,
	})
	call := buf2.String()
	if !strings.Contains(call, "call started") {
		t.Fatalf("observe logged no call: %s", call)
	}
	if !strings.Contains(call, "source=3132910") {
		t.Errorf("the call line does not name the radio that keyed up under "+
			"source: %s", call)
	}
	if strings.Contains(call, "peer_id=3132910") {
		t.Errorf("the call line labels the radio that keyed up as a peer; a "+
			"radio ID survives relaying and a peer ID does not, so they cannot "+
			"share a name: %s", call)
	}
}
