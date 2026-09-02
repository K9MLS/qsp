package peers_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
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

// TestTheHomebrewSideStillCannotAddressARepeater keeps the half of the old rule
// that is still true.
//
// **Until 0192 nothing could reach a Motorola repeater at all**, and a test here
// asserted it by naming one as a bridge endpoint and requiring no delivery. That
// rule is now half withdrawn: audio does reach repeaters, but not through the
// Homebrew delivery path. It goes out of the IPSC listener's own socket, to
// every registered repeater, because an IPSC peer announces no talkgroups and
// filters by its own codeplug.
//
// So the structural fact this still checks is narrower and worth keeping: a
// Motorola repeater is not a Homebrew peer, is not in the peer table, and
// cannot be resolved as a Homebrew destination. A bridge naming one still
// delivers nothing on that path, and the frame reaching it by the other route
// is deliberate rather than accidental.
func TestTheHomebrewSideStillCannotAddressARepeater(t *testing.T) {
	l, core := startWithIPSC(t)
	register(t, l.Address(), testID, "K9MLS")

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
			t.Fatalf("routing resolved the Motorola repeater %d as a Homebrew destination; "+
				"it is not a Homebrew peer and reaches repeaters by the IPSC socket instead",
				motorola)
		}
	}
}

// TestRelayedAudioReachesTheMotorolaSide is the new direction.
//
// A frame routing accepted is offered to the IPSC side with the peer it came
// from, so that side can send it to every repeater except the origin.
func TestRelayedAudioReachesTheMotorolaSide(t *testing.T) {
	l, _ := startWithIPSC(t)
	register(t, l.Address(), testID, "K9MLS")

	type sent struct {
		origin uint32
		frame  hbp.Data
	}
	var got []sent
	l.SetIPSCSink(func(origin uint32, frame hbp.Data) {
		got = append(got, sent{origin, frame})
	})

	l.DeliverFromIPSC(motorola, hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE06,
	})

	if len(got) == 0 {
		t.Fatal("a routed frame was not offered to the IPSC side; " +
			"repeater to repeater cannot work without it")
	}
	if got[0].origin != uint32(motorola) {
		t.Errorf("the frame was offered with origin %d, want %d so it is not "+
			"sent back to the repeater that transmitted it", got[0].origin, motorola)
	}
	if got[0].frame.TargetID != 2 {
		t.Errorf("the frame offered carries TG%d, want TG2 unchanged", got[0].frame.TargetID)
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

// TestMotorolaTrafficReachesTheCallTracker is the dashboard defect.
//
// A transmission crossed the bridge and left no trace anywhere an operator
// looks: not in last heard, not on the console. The frame was carried and the
// record said nobody had spoken — two statements individually true, together a
// lie, which §8a names as the shape of almost every defect here.
//
// Found by keying up and watching the dashboard stay empty, not by any test.
func TestMotorolaTrafficReachesTheCallTracker(t *testing.T) {
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	table, err := routing.NewTable(nil)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{
		Table: table, Peers: readyFromMaster{m: master},
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	tracker := calls.NewTracker(calls.Options{})

	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core, Calls: tracker,
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
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE03,
	})

	active := tracker.Active()
	if len(active) == 0 {
		t.Fatal("a Motorola transmission left no record; the console shows who is talking " +
			"and this talker would be invisible while their audio was relayed")
	}
	if active[0].Target != 2 {
		t.Errorf("the record says TG%d, want TG2", active[0].Target)
	}
	if active[0].Source != 3132910 {
		t.Errorf("the record attributes the call to %d, want the transmitting radio", active[0].Source)
	}
}

// TestASharedRadioIDIsReported is the afternoon this cost, written down.
//
// Routing never sends a call back to the peer that transmitted it, and decides
// that by comparing IDs. So a hotspot sharing its radio ID with a Motorola
// repeater is excluded from every one of that repeater's transmissions — while
// every other member hears them, and the journal reports the frames relayed.
// The one person most likely to be doing the testing is the one person who
// cannot hear the result, and nothing anywhere said so.
func TestASharedRadioIDIsReported(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	table, err := routing.NewTable(nil)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: readyFromMaster{m: master}})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	l, err := peers.NewListener(log, peers.ListenerConfig{
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

	// A hotspot registers with the same radio ID the IPSC repeater uses.
	register(t, l.Address(), testID, "K9MLS")

	frame := hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE04,
	}
	l.DeliverFromIPSC(testID, frame)

	if !strings.Contains(buf.String(), "share a radio ID") {
		t.Fatalf("a shared radio ID went unreported; the log said:\n%s", buf.String())
	}

	// One line, not one per frame. A three-second transmission is about fifty.
	before := strings.Count(buf.String(), "share a radio ID")
	for i := 0; i < 20; i++ {
		l.DeliverFromIPSC(testID, frame)
	}
	if after := strings.Count(buf.String(), "share a radio ID"); after != before {
		t.Errorf("the warning repeated %d times over 21 frames; it should be said once", after)
	}
}

// TestAnUnsharedRadioIDIsNotReported keeps the warning meaningful.
func TestAnUnsharedRadioIDIsNotReported(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))

	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	table, _ := routing.NewTable(nil)
	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: readyFromMaster{m: master}})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	l, err := peers.NewListener(log, peers.ListenerConfig{
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

	register(t, l.Address(), testID, "K9MLS")

	// The repeater has an ID of its own, which is the configuration being
	// recommended.
	l.DeliverFromIPSC(motorola, hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE05,
	})

	if strings.Contains(buf.String(), "share a radio ID") {
		t.Errorf("a distinct radio ID was reported as a collision:\n%s", buf.String())
	}
}
