package peers_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// A weather alert reaches this server's hotspots and Motorola repeaters, and
// no QSP server that dialled in (ADR-0068, as amended). An announcement sent
// the ordinary way reaches the linked server too, which is what makes the
// first half mean something.
//
// To see it fail: route with RouteFromServer in networkSink.Deliver whatever
// local says, and the linked server receives the weather alert.
func TestAWeatherAlertReachesOnlyThisServersStations(t *testing.T) {
	l := startLinkedServerRouting(t)
	addr := l.Address()
	hotspot := registerPackage(t, addr, testID, "K9MLS", "MMDVM_MMDVM_HS_Dual_Hat")
	server := registerPackage(t, addr, linkedServer, "KD9EJA", linkPackage)
	var (
		mu      sync.Mutex
		offered int
	)
	l.SetIPSCSink(func(uint32, hbp.Data) {
		mu.Lock()
		offered++
		mu.Unlock()
	})

	alert := tms.Message{From: 9990, To: 2, Group: true, IPID: 1, Reference: 0x81, Text: "TORNADO WARNING Denton"}
	if err := l.SendLocalText(hbp.Timeslot2, alert); err != nil {
		t.Fatalf("SendLocalText: %v", err)
	}
	if got, ok := hotspot.recv().(hbp.Data); !ok || got.TargetID != 2 || got.SourceID != 9990 {
		t.Fatalf("the hotspot got %+v, want the alert on TG 2 from 9990", got)
	}
	waitFor(t, "the alert to be offered to the Motorola repeaters", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return offered > 0
	})
	server.silence(1500 * time.Millisecond)

	// An announcement composed on the console is for everybody.
	waitFor(t, "the alert to finish going out", func() bool {
		return !errors.Is(l.SendText(hbp.Timeslot2, alert, 0), peers.ErrTextBusy)
	})
	if got, ok := server.recv().(hbp.Data); !ok || got.TargetID != 2 {
		t.Errorf("an announcement did not reach the linked server: %+v", got)
	}
}

// One composed text on the air at a time, whichever way it is going: two
// interleaved on a timeslot are both lost.
//
// To see it fail: drop l.local.Busy from the busy check in sendText.
func TestAWeatherAlertAndAnAnnouncementDoNotOverlap(t *testing.T) {
	l := startForwarding(t)
	register(t, l.Address(), testID, "K9MLS")
	if err := l.SendLocalText(hbp.Timeslot2, aText); err != nil {
		t.Fatalf("SendLocalText: %v", err)
	}
	if err := l.SendText(hbp.Timeslot2, aText, 0); !errors.Is(err, peers.ErrTextBusy) {
		t.Errorf("an announcement during a weather alert: %v, want %v", err, peers.ErrTextBusy)
	}
}
