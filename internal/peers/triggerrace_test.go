package peers_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// TestABridgeIsOpenedOnDemandFromBothListeners. A hotspot keys an on-demand
// talkgroup on this listener's goroutine and a Motorola repeater keys it on
// the IPSC listener's. Both recorded the transmission in one unlocked map
// and both replaced the listener's note of which bridges are open; on a
// machine with more than one core the first of those stopped the server.
//
// The second case saves the configuration while they do it, which replaces
// the triggers and the schedule from the sweep.
//
// To see it fail: delete the two lines that take and release schedMu in
// Listener.trigger. The race detector names this test. The map itself has
// its own lock and its own test, TestTriggersAreSharedByTwoListeners in
// internal/routing.
func TestABridgeIsOpenedOnDemandFromBothListeners(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reload bool
	}{
		{"two listeners keying", false},
		{"and a configuration saved meanwhile", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			master, err := peers.NewMaster(logging.Discard(), peers.MasterConfig{
				Password: func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true },
			})
			if err != nil {
				t.Fatalf("NewMaster: %v", err)
			}
			bridges := []routing.Bridge{{
				Name: "net", Enabled: true,
				Endpoints: []routing.Endpoint{
					{Peer: motorola, Talkgroup: 2, Timeslot: hbp.Timeslot2},
					{Peer: testID, Talkgroup: 2, Timeslot: hbp.Timeslot2},
				},
			}}
			table, err := routing.NewTable(bridges)
			if err != nil {
				t.Fatalf("NewTable: %v", err)
			}
			core, err := routing.NewCore(routing.CoreOptions{Table: table, Peers: readyFromMaster{m: master}})
			if err != nil {
				t.Fatalf("NewCore: %v", err)
			}
			newTriggers := func() *routing.Triggers {
				tr, err := routing.NewTriggers([]routing.Trigger{{
					Bridge: "net", Enabled: true, HangTime: time.Minute,
					On: []routing.Endpoint{
						{Peer: motorola, Talkgroup: 2, Timeslot: hbp.Timeslot2},
						{Peer: testID, Talkgroup: 2, Timeslot: hbp.Timeslot2},
					},
				}})
				if err != nil {
					t.Fatalf("NewTriggers: %v", err)
				}
				return tr
			}
			rebuild := func(time.Time) (*routing.Table, error) { return routing.NewTable(bridges) }
			triggers := newTriggers()
			l, err := peers.NewListener(logging.Discard(), peers.ListenerConfig{
				ListenAddress: "127.0.0.1:0", Master: master, Routing: core,
				Triggers: triggers,
				// What cmd/qsp does with the triggers.
				ScheduleState: triggers.ActiveAt,
				Rebuild:       rebuild,
			})
			if err != nil {
				t.Fatalf("NewListener: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if err := l.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer l.Close()

			hotspot := register(t, l.Address(), testID, "K9MLS")
			go func() {
				buf := make([]byte, 1500)
				for {
					if _, err := hotspot.conn.Read(buf); err != nil {
						return
					}
				}
			}()

			var wg sync.WaitGroup
			wg.Go(func() { // the IPSC listener's goroutine
				for i := range 3000 {
					l.DeliverFromIPSC(motorola, hbp.Data{
						RepeaterID: motorola, SourceID: 3132910, TargetID: 2,
						Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
						FrameType: hbp.FrameTypeVoice, StreamID: hbp.StreamID(0xC0FFEE00 + i/50),
					})
				}
			})
			wg.Go(func() { // a hotspot, through the socket
				for i := range 3000 {
					_, _ = hotspot.conn.Write(hbp.Data{
						RepeaterID: testID, SourceID: 3132911, TargetID: 2,
						Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
						FrameType: hbp.FrameTypeVoice, StreamID: hbp.StreamID(0xBEEF0000 + i/50),
					}.Marshal())
				}
			})
			if tc.reload {
				wg.Go(func() { // an operator pressing Save
					for range 3 {
						tr := newTriggers()
						l.Apply(&peers.Reload{Triggers: tr, ScheduleState: tr.ActiveAt,
							Rebuild: rebuild, Author: "K9MLS"})
						// The sweep that applies it runs every second.
						time.Sleep(1100 * time.Millisecond)
					}
				})
			}
			wg.Wait()
		})
	}
}
