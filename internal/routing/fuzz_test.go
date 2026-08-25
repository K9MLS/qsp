package routing_test

import (
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// FuzzRoute asserts the invariants that must hold for any table and any call.
//
// The first is the one that matters: a call is never delivered back to its
// source. On a repeater that is feedback, and no configuration an operator can
// write should be able to produce it.
func FuzzRoute(f *testing.F) {
	f.Add(uint32(3132910), uint32(3148), uint8(1), uint32(3121380), uint32(3148), uint8(1), true)
	f.Add(uint32(0), uint32(9), uint8(2), uint32(0), uint32(9), uint8(2), true)
	f.Add(uint32(1), uint32(1), uint8(1), uint32(1), uint32(1), uint8(2), false)

	f.Fuzz(func(t *testing.T,
		aPeer, aTG uint32, aTS uint8,
		bPeer, bTG uint32, bTS uint8,
		enabled bool,
	) {
		slot := func(v uint8) hbp.Timeslot {
			if v%2 == 0 {
				return hbp.Timeslot2
			}
			return hbp.Timeslot1
		}
		// Talkgroup 0 is invalid by construction, so shift it into range.
		norm := func(v uint32) uint32 { return v%16777215 + 1 }

		a := routing.Endpoint{Peer: hbp.RepeaterID(aPeer), Talkgroup: norm(aTG), Timeslot: slot(aTS)}
		b := routing.Endpoint{Peer: hbp.RepeaterID(bPeer), Talkgroup: norm(bTG), Timeslot: slot(bTS)}
		if a == b {
			return // rejected by validation; covered by unit tests
		}

		tab, err := routing.NewTable([]routing.Bridge{{
			Name: "fuzz", Enabled: enabled, Endpoints: []routing.Endpoint{a, b},
		}})
		if err != nil {
			return
		}

		for _, from := range []routing.Endpoint{a, b,
			{Peer: hbp.RepeaterID(aPeer), Talkgroup: norm(bTG), Timeslot: slot(aTS)},
		} {
			d := tab.Route(from)

			// 1. Never echo to the source.
			for _, target := range d.Targets {
				if target == from || target.Matches(from) {
					t.Fatalf("call from %s routed back to itself as %s", from, target)
				}
			}

			// 2. No duplicates.
			seen := map[routing.Endpoint]bool{}
			for _, target := range d.Targets {
				if seen[target] {
					t.Fatalf("duplicate target %s for call from %s", target, from)
				}
				seen[target] = true
			}

			// 3. A disabled bridge carries nothing.
			if !enabled && d.Routed() {
				t.Fatalf("a disabled bridge routed a call from %s", from)
			}

			// 4. An empty result always explains itself.
			if !d.Routed() && d.Reason == "" {
				t.Fatalf("call from %s went nowhere with no reason", from)
			}
		}
	})
}
