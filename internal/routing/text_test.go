package routing_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestATextBurstDoesNotReleaseTheSlot covers a defect the IPSC text work
// created.
//
// Contention was released on any data sync frame that was not the opening one.
// That was complete while a data burst could only be a voice LC header or a
// terminator.
//
// **A text message is a run of data bursts**, so every one after the first
// released the reservation mid-message. Somebody else could then key up and
// interleave with a transmission still in progress, which is the exact thing
// contention exists to prevent — and it would present as two people talking
// over each other with no explanation in the journal.
func TestATextBurstDoesNotReleaseTheSlot(t *testing.T) {
	const a, b hbp.RepeaterID = 3100001, 3100002
	core := noBridges(t, a, b)

	// A text opens and reserves its destination.
	core.Route(a, textBurst(0x7777, 9, hbp.Timeslot2), t0)
	if core.BusyCount() == 0 {
		t.Fatal("a text reserved nothing; it is a transmission like any other")
	}

	// Every later burst must leave the reservation alone.
	for range 5 {
		core.Route(a, textBurst(0x7777, 9, hbp.Timeslot2), t0)
		if core.BusyCount() == 0 {
			t.Fatal("a text burst released the slot mid-message; another " +
				"station could key up and interleave with it")
		}
	}

	// And a real terminator still releases.
	core.Route(a, groupTerminator(0x7777, 9, hbp.Timeslot2), t0)
	if core.BusyCount() != 0 {
		t.Errorf("a terminator left %d reservations behind", core.BusyCount())
	}
}

// textBurst is one burst of a text message: a data burst whose data type is a
// CSBK rather than a voice header or a terminator.
func textBurst(stream hbp.StreamID, tg uint32, slot hbp.Timeslot) hbp.Data {
	d := groupCall(stream, tg, slot, hbp.FrameTypeSync)
	d.DataType = 0x3
	return d
}
