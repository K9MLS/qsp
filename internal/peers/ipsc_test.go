package peers_test

import (
	"context"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// motorola is the radio ID of an IPSC repeater. It is deliberately not a
// Homebrew peer and never registers: that is the point of these tests.
const motorola = hbp.RepeaterID(315544)

// startWithIPSC brings up a listener whose routing table names the Motorola
// repeater as an endpoint, which is the configuration most likely to create a
// path back to it if one could exist.
func startWithIPSC(t *testing.T) (*peers.Listener, *routing.Core) {
	t.Helper()

	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}

	table, err := routing.NewTable([]routing.Bridge{{
		Name: "motorola-to-hotspots", Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: motorola, Talkgroup: 2, Timeslot: hbp.Timeslot2},
			{Peer: testID, Talkgroup: 2, Timeslot: hbp.Timeslot2},
		},
	}})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}

	core, err := routing.NewCore(routing.CoreOptions{
		Table: table, Peers: readyFromMaster{m: master},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, core
}

// TestMotorolaAudioReachesAHotspot is the thing this patch exists to do.
//
// A burst converted from an IP Site Connect repeater's audio, handed to the DMR
// listener, must arrive at a registered hotspot on the same talkgroup — with
// the talkgroup unchanged, because 2 is 2 on both sides.
func TestMotorolaAudioReachesAHotspot(t *testing.T) {
	l, _ := startWithIPSC(t)
	hotspot := register(t, l.Address(), testID, "K9MLS")

	l.DeliverFromIPSC(motorola, hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE01,
	})

	msg := hotspot.recv()
	got, ok := msg.(hbp.Data)
	if !ok {
		t.Fatalf("the hotspot got %s, want a DMRD frame", msg.Kind())
	}
	if got.TargetID != 2 {
		t.Errorf("the hotspot was sent TG%d; a talkgroup is never renumbered", got.TargetID)
	}
	if got.Timeslot != hbp.Timeslot2 {
		t.Errorf("the hotspot was sent TS%d, want TS2", got.Timeslot)
	}
	if got.SourceID != 3132910 {
		t.Errorf("the frame is attributed to %d, want the transmitting radio 3132910", got.SourceID)
	}
}

// TestNothingIsEverSentToAMotorolaRepeater is the rule that must not be
// possible to break by configuration.
//
// Nothing has ever captured a master sending voice to an IPSC repeater, so QSP
// does not know what such a frame contains. Rather than trusting a comment, this
// asserts the structural reason: destinations resolve through the Homebrew peer
// table, and a Motorola repeater is not in it — so even a bridge that names it
// explicitly, as the table here does, yields no delivery.
func TestNothingIsEverSentToAMotorolaRepeater(t *testing.T) {
	l, core := startWithIPSC(t)
	register(t, l.Address(), testID, "K9MLS")

	// Give the registration a moment to reach the master's table.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(core.Table().Bridges()) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	res := core.Route(testID, hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE02,
	}, time.Now())

	for _, d := range res.Deliveries {
		if d.Peer == motorola {
			t.Fatalf("a frame was routed to the Motorola repeater %d; "+
				"no capture shows what a master sends to one, so QSP must not invent it", motorola)
		}
	}
}

// TestAudioIsNotCarriedWithoutRouting keeps the honest-failure property.
//
// A listener with no routing core has nowhere to deliver, and must say nothing
// rather than panic or claim success.
func TestAudioIsNotCarriedWithoutRouting(t *testing.T) {
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master,
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	l.DeliverFromIPSC(motorola, hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
	})
}
