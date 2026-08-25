package routing_test

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

func mustTriggers(t *testing.T, triggers ...routing.Trigger) *routing.Triggers {
	t.Helper()
	out, err := routing.NewTriggers(triggers)
	if err != nil {
		t.Fatalf("NewTriggers: %v", err)
	}
	return out
}

func demandTrigger() routing.Trigger {
	return routing.Trigger{
		Bridge:   "on-demand",
		On:       []routing.Endpoint{ep(peerA, 3148, hbp.Timeslot1)},
		HangTime: 3 * time.Minute,
		Enabled:  true,
	}
}

// TestTransmissionOpensTheBridgeAndHangTimeClosesIt is the whole feature.
func TestTransmissionOpensTheBridgeAndHangTimeClosesIt(t *testing.T) {
	tr := mustTriggers(t, demandTrigger())

	if tr.ActiveAt(t0)["on-demand"] {
		t.Fatal("the bridge was open before anybody transmitted")
	}

	opened := tr.Observe(ep(peerA, 3148, hbp.Timeslot1), t0)
	if len(opened) != 1 || opened[0] != "on-demand" {
		t.Fatalf("Observe returned %v, want [on-demand]", opened)
	}
	if !tr.ActiveAt(t0)["on-demand"] {
		t.Fatal("the bridge is not open after a transmission")
	}

	// Still open within the hang time.
	if !tr.ActiveAt(t0.Add(2*time.Minute + 59*time.Second))["on-demand"] {
		t.Error("the bridge closed before its hang time elapsed")
	}
	// Closed after it.
	if tr.ActiveAt(t0.Add(3*time.Minute + time.Second))["on-demand"] {
		t.Error("the bridge is still open after its hang time")
	}
}

// TestConversationKeepsTheBridgeOpen.
//
// People pause between overs. A bridge that closed the instant somebody unkeyed
// would drop the reply, which is the failure this hang time exists to prevent.
func TestConversationKeepsTheBridgeOpen(t *testing.T) {
	tr := mustTriggers(t, demandTrigger())
	at := t0

	tr.Observe(ep(peerA, 3148, hbp.Timeslot1), at)
	for i := 0; i < 10; i++ {
		at = at.Add(2 * time.Minute) // a long pause, inside the hang time
		opened := tr.Observe(ep(peerA, 3148, hbp.Timeslot1), at)
		if len(opened) != 0 {
			t.Errorf("over %d reported the bridge as newly opened; it was already open", i)
		}
		if !tr.ActiveAt(at)["on-demand"] {
			t.Fatalf("the bridge closed during a conversation, at over %d", i)
		}
	}

	// The conversation ends.
	if tr.ActiveAt(at.Add(4 * time.Minute))["on-demand"] {
		t.Error("the bridge stayed open after the conversation stopped")
	}
}

// TestReopeningAfterTheHangTimeIsReported.
func TestReopeningAfterTheHangTimeIsReported(t *testing.T) {
	tr := mustTriggers(t, demandTrigger())

	tr.Observe(ep(peerA, 3148, hbp.Timeslot1), t0)
	later := t0.Add(10 * time.Minute)

	opened := tr.Observe(ep(peerA, 3148, hbp.Timeslot1), later)
	if len(opened) != 1 {
		t.Errorf("reopening after the hang time was not reported: %v", opened)
	}
}

// TestOnlyTriggerEndpointsOpenTheBridge.
//
// A club may want their own repeater to open a link outward without letting the
// wider network open it inward.
func TestOnlyTriggerEndpointsOpenTheBridge(t *testing.T) {
	tr := mustTriggers(t, demandTrigger())

	for _, from := range []routing.Endpoint{
		ep(peerB, 3148, hbp.Timeslot1), // different peer
		ep(peerA, 91, hbp.Timeslot1),   // different talkgroup
		ep(peerA, 3148, hbp.Timeslot2), // different timeslot
	} {
		if opened := tr.Observe(from, t0); len(opened) != 0 {
			t.Errorf("%s opened the bridge; it is not a trigger endpoint", from)
		}
	}
	if tr.ActiveAt(t0)["on-demand"] {
		t.Error("the bridge is open despite no trigger endpoint being used")
	}
}

// TestAnyPeerTrigger.
func TestAnyPeerTrigger(t *testing.T) {
	tr := mustTriggers(t, routing.Trigger{
		Bridge:  "club-link",
		On:      []routing.Endpoint{ep(routing.AnyPeer, 3148, hbp.Timeslot1)},
		Enabled: true,
	})
	for _, peer := range []hbp.RepeaterID{peerA, peerB, peerC} {
		tr2 := mustTriggers(t, routing.Trigger{
			Bridge:  "club-link",
			On:      []routing.Endpoint{ep(routing.AnyPeer, 3148, hbp.Timeslot1)},
			Enabled: true,
		})
		if opened := tr2.Observe(ep(peer, 3148, hbp.Timeslot1), t0); len(opened) != 1 {
			t.Errorf("peer %d did not open an AnyPeer trigger", peer)
		}
	}
	_ = tr
}

func TestDisabledTriggerNeverOpens(t *testing.T) {
	trig := demandTrigger()
	trig.Enabled = false
	tr := mustTriggers(t, trig)

	if opened := tr.Observe(ep(peerA, 3148, hbp.Timeslot1), t0); len(opened) != 0 {
		t.Error("a disabled trigger opened its bridge")
	}
	if tr.ActiveAt(t0)["on-demand"] {
		t.Error("a disabled trigger reports its bridge open")
	}
}

// TestExpireForgetsClosedBridges.
//
// Without forgetting, lastUsed grows for every bridge ever triggered, and a
// bridge removed from the configuration keeps an entry forever.
func TestExpireForgetsClosedBridges(t *testing.T) {
	tr := mustTriggers(t, demandTrigger())
	tr.Observe(ep(peerA, 3148, hbp.Timeslot1), t0)

	if closed := tr.Expire(t0.Add(time.Minute)); len(closed) != 0 {
		t.Errorf("expired inside the hang time: %v", closed)
	}
	closed := tr.Expire(t0.Add(4 * time.Minute))
	if len(closed) != 1 || closed[0] != "on-demand" {
		t.Fatalf("Expire returned %v, want [on-demand]", closed)
	}
	// Idempotent: a second sweep reports nothing.
	if again := tr.Expire(t0.Add(5 * time.Minute)); len(again) != 0 {
		t.Errorf("a second sweep reported %v", again)
	}
}

func TestOpenForCountsDown(t *testing.T) {
	tr := mustTriggers(t, demandTrigger())
	if tr.OpenFor("on-demand", t0) != 0 {
		t.Error("a closed bridge reports remaining time")
	}

	tr.Observe(ep(peerA, 3148, hbp.Timeslot1), t0)
	if got := tr.OpenFor("on-demand", t0); got != 3*time.Minute {
		t.Errorf("OpenFor = %s, want 3m", got)
	}
	if got := tr.OpenFor("on-demand", t0.Add(time.Minute)); got != 2*time.Minute {
		t.Errorf("OpenFor after a minute = %s, want 2m", got)
	}
	if got := tr.OpenFor("on-demand", t0.Add(5*time.Minute)); got != 0 {
		t.Errorf("OpenFor past the hang time = %s, want 0", got)
	}
}

func TestDefaultHangTimeApplies(t *testing.T) {
	trig := demandTrigger()
	trig.HangTime = 0
	tr := mustTriggers(t, trig)

	tr.Observe(ep(peerA, 3148, hbp.Timeslot1), t0)
	if got := tr.OpenFor("on-demand", t0); got != routing.DefaultHangTime {
		t.Errorf("OpenFor = %s, want the default %s", got, routing.DefaultHangTime)
	}
}

func TestValidationRejectsUnusableTriggers(t *testing.T) {
	cases := map[string]routing.Trigger{
		"no bridge":      {On: []routing.Endpoint{ep(peerA, 1, hbp.Timeslot1)}},
		"no endpoints":   {Bridge: "b"},
		"bad endpoint":   {Bridge: "b", On: []routing.Endpoint{{Talkgroup: 0, Timeslot: 1}}},
		"negative hang":  {Bridge: "b", On: []routing.Endpoint{ep(peerA, 1, hbp.Timeslot1)}, HangTime: -time.Second},
		"excessive hang": {Bridge: "b", On: []routing.Endpoint{ep(peerA, 1, hbp.Timeslot1)}, HangTime: time.Hour},
	}
	for name, trig := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := routing.NewTriggers([]routing.Trigger{trig}); err == nil {
				t.Errorf("accepted an invalid trigger: %s", name)
			}
		})
	}
}

func TestExcessiveHangTimeIsExplained(t *testing.T) {
	trig := demandTrigger()
	trig.HangTime = time.Hour
	_, err := routing.NewTriggers([]routing.Trigger{trig})
	if err == nil {
		t.Fatal("a one-hour hang time was accepted")
	}
	if !strings.Contains(err.Error(), "unattended") {
		t.Errorf("error should explain what the limit protects against, got: %v", err)
	}
}

func TestNilTriggersAreSafe(t *testing.T) {
	var tr *routing.Triggers
	if len(tr.ActiveAt(t0)) != 0 || tr.Observe(ep(peerA, 1, hbp.Timeslot1), t0) != nil ||
		tr.Expire(t0) != nil || tr.Bridges() != nil || tr.OpenFor("x", t0) != 0 {
		t.Error("a nil trigger set was not inert")
	}
}

func TestTriggersAreImmutableAfterConstruction(t *testing.T) {
	triggers := []routing.Trigger{demandTrigger()}
	tr := mustTriggers(t, triggers...)

	triggers[0].Enabled = false
	triggers[0].On[0] = ep(peerC, 999, hbp.Timeslot2)

	if opened := tr.Observe(ep(peerA, 3148, hbp.Timeslot1), t0); len(opened) != 1 {
		t.Error("mutating the input slice changed a live trigger set")
	}
}

func TestEndpointOfBuildsFromAFrame(t *testing.T) {
	frame := hbp.Data{TargetID: 3148, Timeslot: hbp.Timeslot1}
	if got := routing.EndpointOf(peerA, frame); got != ep(peerA, 3148, hbp.Timeslot1) {
		t.Errorf("EndpointOf = %s", got)
	}
}
