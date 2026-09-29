package peers_test

import (
	"context"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
	"github.com/k9mls/qsp/internal/tms"
)

// ADR-0069, end to end through a real socket: a private call or text from a
// hotspot to a radio this server has never heard goes to the QSP server that
// dialled in, and to no hotspot.
//
// This is production's side of 2026-09-29: the R7 on the Pi-Star called
// 3132911, a radio on the Motorola repeater behind the test server, and the
// capture showed the call arrive from the Pi-Star and go nowhere.

const (
	linkPackage  = "QSP-LINK test"
	linkedServer = hbp.RepeaterID(3139001)
	farRadio     = uint32(3132911)
)

func startLinkedServerRouting(t *testing.T) *peers.Listener {
	t.Helper()
	master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
		Password:  func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
		IsQSPLink: func(c hbp.Config) bool { return c.PackageID == linkPackage },
	})
	if err != nil {
		t.Fatalf("NewMaster: %v", err)
	}
	// Wired as cmd/qsp wires it.
	core, err := routing.NewCore(routing.CoreOptions{
		Peers:         readyFromMaster{m: master},
		Subscribers:   master,
		LinkedServers: master,
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
	return l
}

func registerPackage(t *testing.T, addr string, id hbp.RepeaterID, callsign, pkg string) *client {
	t.Helper()
	c := dial(t, addr)
	c.send(hbp.Login{RepeaterID: id})
	ack, ok := c.recv().(hbp.Ack)
	if !ok {
		t.Fatalf("%s: login was not answered with RPTACK", callsign)
	}
	c.send(hbp.Key{RepeaterID: id, Digest: hbp.Digest(ack.Salt(), []byte(testPassword))})
	c.recv()
	c.send(hbp.Config{RepeaterID: id, Callsign: callsign, ColorCode: "1", PackageID: pkg})
	// The ACK. No Identity is configured, so a QSP server is sent nothing
	// more.
	c.recv()
	return c
}

// privateText is a real text's bursts addressed to one radio: sixteen data
// preambles, a header and its blocks.
func privateText(t *testing.T, to uint32) []hbp.Data {
	t.Helper()
	var n hbp.StreamID = 0x7000
	frames, err := tms.Frames(tms.Message{From: 3132910, To: 2, Group: true, Reference: 0x80, Text: "QSP 0446"},
		hbp.Timeslot2, 1, func() hbp.StreamID { n++; return n })
	if err != nil {
		t.Fatalf("%v", err)
	}
	for i := range frames {
		frames[i].CallType = hbp.CallPrivate
		frames[i].TargetID = to
		frames[i].SourceID = 3132910
		frames[i].RepeaterID = testID
	}
	return frames
}

// To see it fail: set LinkedServers to nil above (the server receives
// nothing), or remove the gate exemption in Listener.deliver (the server
// is sent one preamble where the hotspot sent sixteen, and the test times out
// waiting for the rest).
func TestAPrivateTextToAnUnknownRadioReachesTheLinkedServerWhole(t *testing.T) {
	l := startLinkedServerRouting(t)
	addr := l.Address()

	sender := registerPackage(t, addr, testID, "K9MLS", "MMDVM_MMDVM_HS_Dual_Hat")
	hotspot := registerPackage(t, addr, peerTwo, "W5ABC", "MMDVM_MMDVM_HS_Dual_Hat")
	server := registerPackage(t, addr, linkedServer, "KD9EJA", linkPackage)

	frames := privateText(t, farRadio)
	for _, f := range frames {
		sender.send(f)
	}

	preambles := 0
	for i := range frames {
		got, ok := server.recv().(hbp.Data)
		if !ok {
			t.Fatalf("frame %d: the linked server was sent something other than DMRD", i)
		}
		if got.TargetID != farRadio || got.CallType != hbp.CallPrivate || got.Timeslot != hbp.Timeslot2 {
			t.Fatalf("frame %d crossed as target %d type %v TS%d", i, got.TargetID, got.CallType, got.Timeslot)
		}
		if got.RepeaterID != linkedServer {
			t.Errorf("frame %d addressed to %d, want the server %d", i, got.RepeaterID, linkedServer)
		}
		if got.DataType == 0x3 {
			preambles++
		}
	}
	if preambles != tms.Preambles {
		t.Errorf("the linked server was sent %d preambles, want all %d", preambles, tms.Preambles)
	}
	server.silence(200 * time.Millisecond)
	// A hotspot that has never carried the radio hears nothing of it.
	hotspot.silence(200 * time.Millisecond)
}

// A voice private call takes the same path.
func TestAPrivateCallToAnUnknownRadioReachesTheLinkedServer(t *testing.T) {
	l := startLinkedServerRouting(t)
	addr := l.Address()

	sender := registerPackage(t, addr, testID, "K9MLS", "MMDVM_MMDVM_HS_Dual_Hat")
	hotspot := registerPackage(t, addr, peerTwo, "W5ABC", "MMDVM_MMDVM_HS_Dual_Hat")
	server := registerPackage(t, addr, linkedServer, "KD9EJA", linkPackage)

	sender.send(hbp.Data{
		RepeaterID: testID, SourceID: 3132910, TargetID: farRadio,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallPrivate,
		FrameType: hbp.FrameTypeSync, DataType: 0x1, StreamID: 0xCDB3BAB7,
	})

	got, ok := server.recv().(hbp.Data)
	if !ok || got.TargetID != farRadio || got.CallType != hbp.CallPrivate {
		t.Fatalf("the linked server got %+v, want the private call to %d", got, farRadio)
	}
	hotspot.silence(200 * time.Millisecond)
}
