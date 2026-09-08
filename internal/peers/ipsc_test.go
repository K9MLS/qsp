package peers_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/access"
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

	// **Recording and routing are two calls now, in this order**, which is how
	// the IPSC listener drives them: it observes every converted burst and
	// then offers it to parrot, and only what parrot leaves is delivered. A
	// single method that did both would record nothing for a frame parrot
	// takes, and recording it in both places is how one transmission came to
	// appear in Last heard twice.
	frame := hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE03,
	}
	l.ObserveFromIPSC(motorola, frame)
	l.DeliverFromIPSC(motorola, frame)

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

// TestTheIPSCSinkIsNotSilentlyUnwired is the guard for a defect that shipped.
//
// `SetIPSCSink` was written, exported, tested here — and never called from
// cmd/qsp, because a text edit anchored on the wrong indentation and failed
// silently. **An exported method nobody invokes compiles, passes vet, passes
// staticcheck and passes every unit test**, and the only symptom was no audio
// reaching a Motorola repeater, which is indistinguishable from the inference
// in ADR-0041 being wrong.
//
// §8a names this shape: what is declared and read by nothing. A unit test here
// cannot see cmd/qsp, so this asserts the half it can — that a listener with no
// sink is inert rather than panicking, and that a listener with one is reached.
// The other half is a startup line that now always prints one of two states.
func TestTheIPSCSinkIsNotSilentlyUnwired(t *testing.T) {
	l, _ := startWithIPSC(t)
	register(t, l.Address(), testID, "K9MLS")

	frame := hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0xC0FFEE07,
	}

	// Unwired: inert, not a panic, and nothing reaches the Motorola side.
	l.DeliverFromIPSC(motorola, frame)

	// Wired: reached.
	var calls int
	l.SetIPSCSink(func(origin uint32, f hbp.Data) { calls++ })
	l.DeliverFromIPSC(motorola, frame)
	if calls == 0 {
		t.Fatal("a sink set through SetIPSCSink was never called, so wiring it has no effect")
	}
}

// TestARunOfDataBurstsIsOneLineInTheJournal is the console and the journal
// agreeing about how many things happened.
//
// Every burst of a text message or of CSBK signalling carries its own stream
// ID, so each is its own call and each wrote a "call started" line at info.
// One press of one button on one radio produced seventeen of them inside a
// second on 2026-09-05, while the history — which has merged runs of data
// bursts into one entry since the text work — showed one. Two numbers for one
// event, from the same process.
func TestARunOfDataBurstsIsOneLineInTheJournal(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
		Calls: calls.NewTracker(calls.Options{}),
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

	burst := func(stream uint32) hbp.Data {
		return hbp.Data{
			RepeaterID: motorola, SourceID: 3132910, TargetID: 3155373,
			Timeslot: hbp.Timeslot2, CallType: hbp.CallPrivate,
			FrameType: hbp.FrameTypeSync, StreamID: hbp.StreamID(stream),
		}
	}
	for i := 0; i < 8; i++ {
		l.ObserveFromIPSC(motorola, burst(uint32(0x1000+i)))
	}
	if got := strings.Count(buf.String(), `msg="call started"`); got != 1 {
		t.Errorf("a run of eight data bursts wrote %d call-started lines, want 1:\n%s",
			got, buf.String())
	}

	// **Voice always starts a run of its own.** A transmission is an event
	// however soon it follows a text, and suppressing one would hide the thing
	// this journal exists to report.
	buf.Reset()
	voice := burst(0x2000)
	voice.FrameType = hbp.FrameTypeVoiceSync
	l.ObserveFromIPSC(motorola, voice)
	if got := strings.Count(buf.String(), `msg="call started"`); got != 1 {
		t.Errorf("a voice transmission after a data run wrote %d call-started lines, want 1", got)
	}
}

// TestAFinishedDataRunDoesNotWarn is the other half of the same lesson.
//
// A text message reserves its destinations like anything else and then ends
// without a terminator, because data has no terminator and is not meant to. The
// routing reaper warned about each one: two text messages wrote eight
// "released a destination held by an abandoned transmission" lines on a live
// network on 2026-09-05.
//
// **That warning is also how a peer that lost power mid-over is noticed**, and
// one an operator has learned to scroll past does not do that job.
func TestAFinishedDataRunDoesNotWarn(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
		Table: table, Peers: alwaysReady{},
		Timeout: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	l, err := peers.NewListener(log, peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
		Calls: calls.NewTracker(calls.Options{}),
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
		FrameType: hbp.FrameTypeSync, StreamID: 0x7001,
	})
	// **The reservation is what makes this test mean anything.** Without a
	// destination held there is nothing to release, nothing to warn about, and
	// a test that passes whether or not the fix is present.
	if core.BusyCount() == 0 {
		t.Fatal("the data burst reserved nothing; this test would prove nothing")
	}

	deadline := time.Now().Add(2 * time.Second)
	for core.BusyCount() > 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if core.BusyCount() > 0 {
		t.Fatal("the reaper never ran; this test proves nothing")
	}
	if strings.Contains(buf.String(), "abandoned transmission") {
		t.Errorf("a finished data burst was reported as an abandoned transmission:\n%s", buf.String())
	}
}

// alwaysReady stands in for a peer table in which every destination can take
// traffic, so a test about the reaper is not really a test about registration.
type alwaysReady struct{}

func (alwaysReady) Ready(hbp.RepeaterID) bool    { return true }
func (alwaysReady) ReadyPeers() []hbp.RepeaterID { return []hbp.RepeaterID{motorola, testID} }

// TestAPrivateCallReachesTheOtherRepeater is what an operator keyed and did not
// hear.
//
// A private call from one Motorola repeater to a radio behind another was
// refused by a lookup that has no bearing on it. Routing resolves a private
// call by locating the called radio among the Homebrew peers; a radio living
// behind an IPSC repeater is not there, so the result carried a reason, and
// sendToIPSC bailed on any reason at all.
//
// **The repeater path never needed that lookup.** A Motorola repeater receives
// everything and filters in its own codeplug, which is exactly how a group call
// between two of them works. On 2026-09-06 the same call in the opposite
// direction worked, because that radio happened to sit on a hotspot and could
// be located — so the defect hid behind a network where one operator was on a
// hotspot and the other was not.
func TestAPrivateCallReachesTheOtherRepeater(t *testing.T) {
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
		Table: table, Peers: alwaysReady{}, Subscribers: master,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	var toIPSC int
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
		Calls: calls.NewTracker(calls.Options{}),
		IPSC:  func(uint32, hbp.Data) { toIPSC++ },
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

	// A private call to a radio no hotspot has ever heard: exactly the call
	// that vanished.
	l.DeliverFromIPSC(motorola, hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 3155373,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallPrivate,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0x6135,
	})
	if toIPSC == 0 {
		t.Error("a private call between two Motorola repeaters reached no repeater; " +
			"the operator keys up and nothing happens")
	}

	// **The complementary half, and the reason this is not simply ignoring the
	// verdict.** A refusal that stopped being a refusal here would carry a
	// judged transmission to every repeater on the network.
	//
	// It uses a talkgroup the access lists forbid rather than a banned radio:
	// a banned radio is stopped before routing is reached, so asserting that
	// it never reaches a repeater passes whether this guard exists or not.
	// The first version of this test did exactly that and proved nothing.
	forbidden, err := routing.NewCore(routing.CoreOptions{
		Table: table, Peers: alwaysReady{}, Subscribers: master,
		Access: deniedTalkgroup(t, 99),
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	var refusedToIPSC int
	judged, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: forbidden,
		Calls: calls.NewTracker(calls.Options{}),
		IPSC:  func(uint32, hbp.Data) { refusedToIPSC++ },
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}
	if err := judged.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = judged.Close() })

	judged.DeliverFromIPSC(motorola, hbp.Data{
		RepeaterID: motorola, SourceID: 3132910, TargetID: 99,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 0x6136,
	})
	if refusedToIPSC != 0 {
		t.Errorf("a talkgroup the access lists forbid reached %d repeaters", refusedToIPSC)
	}
}

// deniedTalkgroup builds access lists that permit everything but one talkgroup.
func deniedTalkgroup(t *testing.T, tg uint32) access.Lists {
	t.Helper()
	l, err := access.Parse("dmr.access.talkgroups", access.Talkgroup, access.ModeDeny,
		[]string{fmt.Sprint(tg)})
	if err != nil {
		t.Fatalf("access.Parse: %v", err)
	}
	return access.Lists{Talkgroup1: l, Talkgroup2: l}
}

// noHomebrewPeers is a peer table with nothing in it.
//
// It is the test server as it actually stood on 2026-09-08: one Motorola
// repeater on the IPSC listener, no hotspots, and an OpenBridge link to
// another QSP instance. Every hotspot-shaped assumption in routing resolves to
// nothing here, which is why a configuration nobody had run before found a
// defect that three stations and two repeaters had not.
type noHomebrewPeers struct{}

func (noHomebrewPeers) Ready(hbp.RepeaterID) bool    { return false }
func (noHomebrewPeers) ReadyPeers() []hbp.RepeaterID { return nil }

// linkOnlyListener builds a listener with a link, no Homebrew peers, and a
// counter on the Motorola side.
func linkOnlyListener(t *testing.T, bridges []routing.Bridge, lists access.Lists) (*peers.Listener, *int) {
	t.Helper()
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	table, err := routing.NewTable(bridges)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{
		Table: table, Peers: noHomebrewPeers{}, Access: lists,
	})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}
	toIPSC := new(int)
	l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
		ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
		Calls: calls.NewTracker(calls.Options{}),
		IPSC:  func(uint32, hbp.Data) { *toIPSC++ },
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
	return l, toIPSC
}

// pairBridge is the bridge the accept form writes: this network's own
// talkgroup on one endpoint and the link on the other, both TS1 because
// OpenBridge carries nothing else.
func pairBridge(tg uint32) routing.Bridge {
	return routing.Bridge{
		Name:    "pair-link",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: routing.AnyPeer, Talkgroup: tg, Timeslot: hbp.Timeslot1},
			{Upstream: "pair", Talkgroup: tg, Timeslot: hbp.Timeslot1},
		},
	}
}

// TestAFrameFromALinkReachesTheRepeater is the transmission that was heard
// nowhere.
//
// A hotspot user keyed up on one QSP instance, the frame crossed an OpenBridge
// link to a second instance whose only station is a Motorola repeater, and the
// repeater never transmitted. The journal said:
//
//	call started  subsystem=network peer_id=3132910 talkgroup=2 timeslot=1 stream_id=225593410
//	transmission not carried  subsystem=network peer_id=0 reason="every destination refused the frame"
//
// **Two defects wearing one message.** DeliverFromUpstream never offered the
// frame to the Motorola side at all — forward and DeliverFromIPSC both end in
// sendToIPSC and this third path did not — and had it done so, sendToIPSC
// would have turned it back, because routing reported a refusal for a frame
// nothing refused: there were simply no Homebrew peers to resolve.
//
// The repeater's own transmission three seconds later carried normally, which
// is what made this look like a link fault rather than a routing one.
func TestAFrameFromALinkReachesTheRepeater(t *testing.T) {
	l, toIPSC := linkOnlyListener(t, []routing.Bridge{pairBridge(2)}, access.Lists{})

	// TS1 because OpenBridge forces it; this is the frame as it arrives.
	l.DeliverFromUpstream("pair", hbp.Data{
		RepeaterID: 3132910, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot1, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 225593410,
	})
	if *toIPSC == 0 {
		t.Error("a frame from an OpenBridge link reached no Motorola repeater; " +
			"a member keys up on one instance and the repeater on the other stays silent")
	}
}

// TestAJudgedFrameFromALinkReachesNoRepeater is why the test above is not
// simply ignoring routing's verdict.
//
// The escape hatch has to be narrow: a frame nothing judged goes to the
// repeaters, and a frame something refused goes nowhere by any path. A
// talkgroup the access lists forbid is the case that distinguishes them,
// because it is refused by a judgement rather than by an empty peer table.
func TestAJudgedFrameFromALinkReachesNoRepeater(t *testing.T) {
	l, toIPSC := linkOnlyListener(t, []routing.Bridge{pairBridge(99)}, deniedTalkgroup(t, 99))

	l.DeliverFromUpstream("pair", hbp.Data{
		RepeaterID: 3132910, SourceID: 3132910, TargetID: 99,
		Timeslot: hbp.Timeslot1, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 225593411,
	})
	if *toIPSC != 0 {
		t.Errorf("a talkgroup the access lists forbid reached %d repeaters over a link", *toIPSC)
	}
}

// TestTheLoopRuleIsNotAJudgement covers the second network a club adds.
//
// With one link the routing table excludes the endpoint the frame arrived on,
// so no drop is recorded at all and the frame is unjudged by being untouched.
// With two, the frame is offered to the other link and the loop rule refuses
// it — a rule about links, not a verdict on the transmission. Classifying that
// drop as a refusal would silence every repeater on any server carrying more
// than one link, which is the shape this network is growing into.
func TestTheLoopRuleIsNotAJudgement(t *testing.T) {
	bridge := routing.Bridge{
		Name:    "two-links",
		Enabled: true,
		Endpoints: []routing.Endpoint{
			{Peer: routing.AnyPeer, Talkgroup: 2, Timeslot: hbp.Timeslot1},
			{Upstream: "pair", Talkgroup: 2, Timeslot: hbp.Timeslot1},
			{Upstream: "other", Talkgroup: 2, Timeslot: hbp.Timeslot1},
		},
	}
	table, err := routing.NewTable([]routing.Bridge{bridge})
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: noHomebrewPeers{}})
	if err != nil {
		t.Fatalf("NewCore: %v", err)
	}

	res := core.RouteFromUpstream("pair", hbp.Data{
		RepeaterID: 3132910, SourceID: 3132910, TargetID: 2,
		Timeslot: hbp.Timeslot1, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: 225593412,
	}, time.Now())

	if len(res.Drops) == 0 {
		t.Fatal("the loop rule recorded no drop, so this test is not exercising it")
	}
	for _, d := range res.Drops {
		if !d.NotAJudgement {
			t.Errorf("the loop rule is classified as a judgement: %q", d.Reason)
		}
	}
	if !res.NoHomebrewDestination {
		t.Errorf("a frame only the loop rule touched is reported as refused: %q", res.Reason)
	}
	if res.Reason == "every destination refused the frame" {
		t.Error("the reason claims a refusal nobody made; that sentence cost a day of silence")
	}
}
