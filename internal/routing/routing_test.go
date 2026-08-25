package routing_test

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

const (
	peerA = hbp.RepeaterID(3132910)
	peerB = hbp.RepeaterID(3121380)
	peerC = hbp.RepeaterID(3100001)
)

func ep(peer hbp.RepeaterID, tg uint32, ts hbp.Timeslot) routing.Endpoint {
	return routing.Endpoint{Peer: peer, Talkgroup: tg, Timeslot: ts}
}

func mustTable(t *testing.T, bridges ...routing.Bridge) *routing.Table {
	t.Helper()
	tab, err := routing.NewTable(bridges)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tab
}

// TestCallIsNeverRoutedBackToItsSource is the single most important property
// here.
//
// Delivering a call back to the peer that sent it is an echo; on a repeater it
// is feedback. Every other behaviour in this package is negotiable. This one is
// not.
func TestCallIsNeverRoutedBackToItsSource(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "texas", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1),
			ep(peerB, 3148, hbp.Timeslot1),
			ep(peerC, 3148, hbp.Timeslot1),
		},
	})

	from := ep(peerA, 3148, hbp.Timeslot1)
	d := tab.Route(from)

	if len(d.Targets) != 2 {
		t.Fatalf("got %d targets, want 2: %v", len(d.Targets), d.Targets)
	}
	for _, target := range d.Targets {
		if target == from {
			t.Fatal("the call was routed back to its source")
		}
	}
}

// TestAnyPeerEndpointDoesNotEchoToTheSource covers the subtler version of the
// same bug.
//
// An AnyPeer endpoint matches the source peer too. A naive implementation that
// only compared endpoints for equality would deliver the call straight back.
func TestAnyPeerEndpointDoesNotEchoToTheSource(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "club-wide", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(routing.AnyPeer, 3148, hbp.Timeslot1),
			ep(peerC, 9, hbp.Timeslot2),
		},
	})

	d := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
	for _, target := range d.Targets {
		if target.Matches(ep(peerA, 3148, hbp.Timeslot1)) {
			t.Fatalf("an AnyPeer endpoint echoed the call back to its source: %s", target)
		}
	}
	if len(d.Targets) != 1 || d.Targets[0] != ep(peerC, 9, hbp.Timeslot2) {
		t.Errorf("targets = %v, want the single non-source endpoint", d.Targets)
	}
}

// TestOverlappingBridgesDeliverOneCopy.
//
// Two bridges joining the same pair would otherwise send the audio twice, which
// on a repeater is audible.
func TestOverlappingBridgesDeliverOneCopy(t *testing.T) {
	tab := mustTable(t,
		routing.Bridge{Name: "net-a", Enabled: true, Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 3148, hbp.Timeslot1),
		}},
		routing.Bridge{Name: "net-b", Enabled: true, Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 3148, hbp.Timeslot1),
		}},
	)

	d := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
	if len(d.Targets) != 1 {
		t.Fatalf("got %d targets, want 1; overlapping bridges duplicated delivery: %v", len(d.Targets), d.Targets)
	}
	if len(d.Bridges) != 2 {
		t.Errorf("both contributing bridges should be reported, got %v", d.Bridges)
	}
}

// TestDisabledBridgeCarriesNothing, and says so.
func TestDisabledBridgeCarriesNothing(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "tuesday-net", Enabled: false,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 3148, hbp.Timeslot1),
		},
	})

	d := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
	if d.Routed() {
		t.Fatalf("a disabled bridge carried traffic: %v", d.Targets)
	}
	if !strings.Contains(d.Reason, "disabled") {
		t.Errorf("reason = %q, want it to say the bridge is disabled", d.Reason)
	}
	if !strings.Contains(d.Reason, "tuesday-net") {
		t.Errorf("reason should name the bridge, got %q", d.Reason)
	}
}

// TestEveryUnroutedCallHasAReason.
//
// "Why didn't this call route?" is the question this package exists to answer.
func TestEveryUnroutedCallHasAReason(t *testing.T) {
	cases := map[string]*routing.Table{
		"empty table": mustTable(t),
		"no matching bridge": mustTable(t, routing.Bridge{
			Name: "elsewhere", Enabled: true,
			Endpoints: []routing.Endpoint{
				ep(peerB, 91, hbp.Timeslot2), ep(peerC, 91, hbp.Timeslot2),
			},
		}),
		"disabled": mustTable(t, routing.Bridge{
			Name: "off", Enabled: false,
			Endpoints: []routing.Endpoint{
				ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 3148, hbp.Timeslot1),
			},
		}),
	}
	for name, tab := range cases {
		t.Run(name, func(t *testing.T) {
			d := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
			if d.Routed() {
				t.Fatal("expected no targets")
			}
			if strings.TrimSpace(d.Reason) == "" {
				t.Error("a call went nowhere with no explanation")
			}
		})
	}
}

// TestNilTableIsSafe: the routing core may hold no table before its first
// configuration is applied.
func TestNilTableIsSafe(t *testing.T) {
	var tab *routing.Table
	d := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
	if d.Routed() {
		t.Error("a nil table routed a call")
	}
	if d.Reason == "" {
		t.Error("a nil table gave no reason")
	}
	if tab.EnabledCount() != 0 {
		t.Error("a nil table reported enabled bridges")
	}
	if tab.Bridges() != nil {
		t.Error("a nil table returned bridges")
	}
}

// TestTimeslotMustMatchExactly.
//
// Delivering a talkgroup onto the wrong timeslot would collide with whatever
// else is using it.
func TestTimeslotMustMatchExactly(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "ts1-only", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 3148, hbp.Timeslot1),
		},
	})

	if d := tab.Route(ep(peerA, 3148, hbp.Timeslot2)); d.Routed() {
		t.Errorf("a call on timeslot 2 was routed by a timeslot 1 bridge: %v", d.Targets)
	}
}

// TestRoutingIsDeterministic keeps logs and tests comparable.
func TestRoutingIsDeterministic(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "wide", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerC, 3148, hbp.Timeslot1),
			ep(peerA, 3148, hbp.Timeslot1),
			ep(peerB, 3148, hbp.Timeslot1),
		},
	})

	first := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
	for i := 0; i < 50; i++ {
		got := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
		if len(got.Targets) != len(first.Targets) {
			t.Fatalf("target count changed between identical calls")
		}
		for j := range got.Targets {
			if got.Targets[j] != first.Targets[j] {
				t.Fatalf("target order changed between identical calls: %v then %v", first.Targets, got.Targets)
			}
		}
	}
	// Ordered by peer ID, not by declaration order: peerC's ID (3100001) is
	// lower than peerB's (3121380).
	if first.Targets[0].Peer != peerC || first.Targets[1].Peer != peerB {
		t.Errorf("targets are not ordered by peer ID: %v", first.Targets)
	}
}

// TestTableRejectsInvalidBridges reports every problem at once.
func TestTableRejectsInvalidBridges(t *testing.T) {
	cases := map[string]routing.Bridge{
		"no name": {Enabled: true, Endpoints: []routing.Endpoint{
			ep(peerA, 1, hbp.Timeslot1), ep(peerB, 1, hbp.Timeslot1)}},
		"one endpoint": {Name: "lonely", Endpoints: []routing.Endpoint{ep(peerA, 1, hbp.Timeslot1)}},
		"no endpoints": {Name: "empty"},
		"talkgroup zero": {Name: "tg0", Endpoints: []routing.Endpoint{
			ep(peerA, 0, hbp.Timeslot1), ep(peerB, 1, hbp.Timeslot1)}},
		"bad timeslot": {Name: "ts9", Endpoints: []routing.Endpoint{
			{Peer: peerA, Talkgroup: 1, Timeslot: 9}, ep(peerB, 1, hbp.Timeslot1)}},
		"duplicate endpoint": {Name: "dupe", Endpoints: []routing.Endpoint{
			ep(peerA, 1, hbp.Timeslot1), ep(peerA, 1, hbp.Timeslot1)}},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := routing.NewTable([]routing.Bridge{b}); err == nil {
				t.Errorf("accepted an invalid bridge: %s", name)
			}
		})
	}
}

func TestTableRejectsDuplicateNames(t *testing.T) {
	_, err := routing.NewTable([]routing.Bridge{
		{Name: "Net", Enabled: true, Endpoints: []routing.Endpoint{
			ep(peerA, 1, hbp.Timeslot1), ep(peerB, 1, hbp.Timeslot1)}},
		{Name: "net", Enabled: true, Endpoints: []routing.Endpoint{
			ep(peerA, 2, hbp.Timeslot1), ep(peerB, 2, hbp.Timeslot1)}},
	})
	if err == nil {
		t.Fatal("two bridges with names differing only in case were accepted")
	}
	if !strings.Contains(err.Error(), "unique") {
		t.Errorf("error should explain the rule, got: %v", err)
	}
}

func TestTableReportsEveryProblem(t *testing.T) {
	_, err := routing.NewTable([]routing.Bridge{
		{Name: "", Endpoints: []routing.Endpoint{ep(peerA, 1, hbp.Timeslot1), ep(peerB, 1, hbp.Timeslot1)}},
		{Name: "lonely", Endpoints: []routing.Endpoint{ep(peerA, 1, hbp.Timeslot1)}},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "name") || !strings.Contains(err.Error(), "lonely") {
		t.Errorf("both problems should be reported, got: %v", err)
	}
}

// TestTableIsImmutableAfterConstruction.
//
// A configuration change produces a new table that the core swaps in
// atomically; a caller mutating its input slice afterwards must not alter a
// table already in use.
func TestTableIsImmutableAfterConstruction(t *testing.T) {
	bridges := []routing.Bridge{{
		Name: "net", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), ep(peerB, 3148, hbp.Timeslot1),
		},
	}}
	tab := mustTable(t, bridges...)

	bridges[0].Enabled = false
	bridges[0].Name = "tampered"
	bridges[0].Endpoints[0] = ep(peerC, 9999, hbp.Timeslot2)

	if tab.EnabledCount() != 1 {
		t.Error("mutating the input slice disabled a live bridge")
	}
	d := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
	if !d.Routed() {
		t.Errorf("mutating the input slice broke routing: %s", d.Reason)
	}

	// And the accessor's result is a copy too.
	got := tab.Bridges()
	got[0].Endpoints[0] = ep(peerC, 1, hbp.Timeslot2)
	if d2 := tab.Route(ep(peerA, 3148, hbp.Timeslot1)); !d2.Routed() {
		t.Error("mutating the Bridges() result changed the table")
	}
}

func TestEndpointMatches(t *testing.T) {
	call := ep(peerA, 3148, hbp.Timeslot1)
	cases := []struct {
		name string
		e    routing.Endpoint
		want bool
	}{
		{"exact", ep(peerA, 3148, hbp.Timeslot1), true},
		{"any peer", ep(routing.AnyPeer, 3148, hbp.Timeslot1), true},
		{"other peer", ep(peerB, 3148, hbp.Timeslot1), false},
		{"other talkgroup", ep(peerA, 91, hbp.Timeslot1), false},
		{"other timeslot", ep(peerA, 3148, hbp.Timeslot2), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.e.Matches(call); got != c.want {
				t.Errorf("Matches = %v, want %v", got, c.want)
			}
		})
	}
}

func TestEndpointString(t *testing.T) {
	if got := ep(peerA, 3148, hbp.Timeslot1).String(); got != "3132910/TG3148/TS1" {
		t.Errorf("String() = %q", got)
	}
	if got := ep(routing.AnyPeer, 3148, hbp.Timeslot2).String(); got != "any/TG3148/TS2" {
		t.Errorf("String() = %q", got)
	}
}

// TestFanOutAcrossTalkgroups covers the case QSP exists for: linking a
// talkgroup on one network to a different talkgroup on another.
func TestFanOutAcrossTalkgroups(t *testing.T) {
	tab := mustTable(t, routing.Bridge{
		Name: "tuesday-net", Enabled: true,
		Endpoints: []routing.Endpoint{
			ep(peerA, 3148, hbp.Timeslot1), // Texas on the club repeater
			ep(peerB, 91, hbp.Timeslot2),   // Worldwide on a hotspot
			ep(peerC, 31673, hbp.Timeslot2),
		},
	})

	d := tab.Route(ep(peerA, 3148, hbp.Timeslot1))
	if len(d.Targets) != 2 {
		t.Fatalf("got %d targets, want 2: %v", len(d.Targets), d.Targets)
	}
	// Talkgroup translation: the call arrives on 3148 and leaves on 91 and 31673.
	tgs := map[uint32]bool{}
	for _, target := range d.Targets {
		tgs[target.Talkgroup] = true
	}
	if !tgs[91] || !tgs[31673] {
		t.Errorf("targets did not translate talkgroups: %v", d.Targets)
	}
	if len(d.Bridges) != 1 || d.Bridges[0] != "tuesday-net" {
		t.Errorf("bridges = %v, want [tuesday-net]", d.Bridges)
	}
}
