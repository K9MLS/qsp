package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/database"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/p25calls"
	"github.com/k9mls/qsp/internal/server"
)

// A P25 call finished before a restart is in Last heard after it.
//
// Break it: build the tracker without seeding it, or without a store to
// write to, and Last heard for P25 begins at nothing on every deploy — which
// is what ADR-0033 was written to stop for DMR.
func TestAP25CallSurvivesARestart(t *testing.T) {
	if !database.DriverRegistered("sqlite") {
		t.Skip("no sqlite driver in this build; this test runs where one is")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, nil, database.Options{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "qsp.db"),
	})
	if err != nil {
		t.Fatalf("opening a database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	store := p25calls.NewStore(db.SQL(), 24*time.Hour)

	// Before the restart: one call heard and finished.
	before := newP25Calls(ctx, store, nil, logging.Discard())
	started := time.Now().UTC().Add(-time.Minute)
	call := p25calls.Call{Started: started, Source: 8080303, Talkgroup: 1, Frames: 135,
		ViaKind: p25calls.ViaRepeater, Via: "Quantar, site 1", Carried: true}
	before.Heard("repeater 1", call)
	before.Finished("repeater 1", call, started.Add(3*time.Second))

	// After it: a new tracker on the same database.
	after := newP25Calls(ctx, store, nil, logging.Discard())
	active, recent := after.Snapshot()
	if len(active) != 0 || len(recent) != 1 {
		t.Fatalf("%d in progress and %d finished after the restart, want the one call", len(active), len(recent))
	}
	got := recent[0]
	if got.Source != 8080303 || got.Talkgroup != 1 || got.Frames != 135 || got.Via != "Quantar, site 1" ||
		!got.Carried || got.EndReason != p25calls.EndMarked {
		t.Errorf("the call came back as %+v", got)
	}
}

// What the console is given for a P25 call.
//
// Break it: set a timeslot, name a radio that was never said, or leave a
// call that lost its turn unmarked, and a row fails.
func TestAP25CallAsTheConsoleIsGivenIt(t *testing.T) {
	now := time.Date(2026, 10, 4, 22, 0, 0, 0, time.UTC)
	names := func(id uint32) string {
		if id == 8080303 {
			return "K9MLS"
		}
		return ""
	}
	tests := []struct {
		name string
		call p25calls.Call
		want server.CallView
	}{
		{"carried, from a repeater, and named",
			p25calls.Call{Started: now.Add(-5 * time.Second), Ended: now.Add(-2 * time.Second), Source: 8080303,
				Talkgroup: 1, Frames: 135, Via: "Quantar, site 1", Carried: true, EndReason: p25calls.EndMarked},
			server.CallView{Source: 8080303, SourceName: "K9MLS", Target: 1, Group: true, Duration: "3s",
				Frames: 135, Voice: true, Via: "Quantar, site 1"}},
		{"not carried, from a gateway, and unnamed",
			p25calls.Call{Started: now.Add(-5 * time.Second), Ended: now.Add(-4 * time.Second), Source: 9990002,
				Talkgroup: 10200, Frames: 45, Via: "N0CALL", Carried: false, EndReason: p25calls.EndMarked},
			server.CallView{Source: 9990002, Target: 10200, Group: true, Duration: "1s",
				Frames: 45, Voice: true, Via: "N0CALL", NotCarried: true}},
		{"too short to say who, and it went quiet",
			p25calls.Call{Started: now.Add(-5 * time.Second), Ended: now.Add(-5*time.Second + 180*time.Millisecond),
				Frames: 9, Via: "Quantar, site 1", Carried: true, EndReason: p25calls.EndQuiet},
			server.CallView{Group: true, Duration: "180ms", Frames: 9, Voice: true, Lost: true,
				Via: "Quantar, site 1"}},
	}
	src := p25GatewaySource{name: names}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := src.callView(tc.call, now)
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
			if got.Timeslot != 0 || got.Mode != "" {
				t.Errorf("a timeslot or a mode was set: %+v", got)
			}
		})
	}
}

// A finished call reaches the console with when it ended, so the two modes'
// lists can be merged in order; one in progress has no end to give.
//
// Break it: leave the end time off a finished call, and the merged list
// sorts every P25 call to the bottom.
func TestP25CallsCarryWhatTheMergeNeeds(t *testing.T) {
	tr := p25calls.NewTracker(p25calls.Options{})
	now := time.Now().UTC()
	tr.Heard("a", p25calls.Call{Started: now.Add(-time.Second), Source: 1, Carried: true})
	tr.Finished("b", p25calls.Call{Started: now.Add(-time.Minute), Source: 2, Carried: true}, now.Add(-30*time.Second))

	active, recent := p25GatewaySource{calls: tr}.CallViews(now)
	if len(active) != 1 || len(recent) != 1 {
		t.Fatalf("%d in progress, %d finished", len(active), len(recent))
	}
	if !active[0].EndedAt.IsZero() || active[0].Ago != "" {
		t.Errorf("a call in progress has an end: %+v", active[0])
	}
	if recent[0].EndedAt.IsZero() || recent[0].Ago != "30s" {
		t.Errorf("a finished call ended at %v, %q ago", recent[0].EndedAt, recent[0].Ago)
	}
}

// With no event bus the tracker still works; with one, a call beginning and
// ending are both announced, so the console redraws without being asked.
//
// Break it: publish nothing, and Last heard shows a P25 call only when
// something else makes the page refresh.
func TestP25CallsAreAnnouncedToTheConsole(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	defer bus.Close()
	sub, _ := bus.Subscribe()
	defer sub.Close()

	tr := newP25Calls(context.Background(), nil, bus, logging.Discard())
	c := p25calls.Call{Started: time.Now().UTC(), Source: 8080303, Talkgroup: 1, Via: "Quantar, site 1", Carried: true}
	tr.Heard("a", c)
	tr.Heard("a", c)
	tr.Finished("a", c, time.Now().UTC())

	var got []events.Type
	deadline := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case e := <-sub.C():
			got = append(got, e.Type)
		case <-deadline:
			t.Fatalf("announced %v, want a start and an end", got)
		}
	}
	if got[0] != events.TypeCallStarted || got[1] != events.TypeCallEnded {
		t.Errorf("announced %v", got)
	}
}
