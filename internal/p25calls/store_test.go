package p25calls

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/database"
)

// openStore returns a store on a freshly migrated database, and the database.
// These tests run where a SQLite driver is built in, which the project's own
// check script is.
func openStore(t *testing.T, retain time.Duration) (*Store, *database.DB, context.Context) {
	t.Helper()
	if !database.DriverRegistered("sqlite") {
		t.Skip("no sqlite driver in this build; these tests run where one is")
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
	return NewStore(db.SQL(), retain), db, ctx
}

// Break it: drop a column from the insert or the select, or write the end
// time where the start goes, and what comes back is not what went in.
func TestACallGoesInAndComesBackAsItWas(t *testing.T) {
	s, _, ctx := openStore(t, time.Hour)
	in := []Call{
		{Started: t0, Ended: t0.Add(2800 * time.Millisecond), Source: 8080303, Talkgroup: 1, Frames: 135,
			ViaKind: ViaRepeater, Via: "Quantar, site 1", Carried: true, EndReason: EndMarked},
		{Started: t0.Add(time.Minute), Ended: t0.Add(time.Minute + time.Second), Source: 0, Talkgroup: 0, Frames: 9,
			ViaKind: ViaGateway, Via: "N0CALL", Carried: false, EndReason: EndQuiet},
		{Started: t0.Add(2 * time.Minute), Ended: t0.Add(2*time.Minute + time.Second), Source: 16777215, Talkgroup: 65535,
			Frames: 54, ViaKind: ViaGateway, Via: "K9MLS", Carried: true, EndReason: EndLinkClosed},
	}
	for _, c := range in {
		if err := s.Record(ctx, c); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	got, err := s.Since(ctx, t0.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(got) != len(in) {
		t.Fatalf("%d calls came back, want %d", len(got), len(in))
	}
	// Newest first, so the order is the reverse of how they went in.
	for i, c := range got {
		want := in[len(in)-1-i]
		if !c.Started.Equal(want.Started) || !c.Ended.Equal(want.Ended) {
			t.Errorf("call %d: times %v to %v, want %v to %v", i, c.Started, c.Ended, want.Started, want.Ended)
		}
		c.Started, c.Ended, want.Started, want.Ended = time.Time{}, time.Time{}, time.Time{}, time.Time{}
		if c != want {
			t.Errorf("call %d came back as %+v, want %+v", i, c, want)
		}
	}
}

// Break it: store a call still in progress, and the record holds a row with
// no end that every reader has to guard against.
func TestACallInProgressIsNotStored(t *testing.T) {
	s, _, ctx := openStore(t, time.Hour)
	if err := s.Record(ctx, Call{Started: t0, Source: 1}); err == nil {
		t.Error("a call with no end time was stored")
	}
}

// Break it: read from the start of time whatever was asked, ignore the limit,
// or return oldest first, and a row fails.
func TestSinceIsAWindowNewestFirstAndBounded(t *testing.T) {
	s, _, ctx := openStore(t, 24*time.Hour)
	for i := range 5 {
		start := t0.Add(time.Duration(i) * time.Minute)
		if err := s.Record(ctx, Call{Started: start, Ended: start.Add(time.Second), Source: uint32(i + 1)}); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	tests := []struct {
		name  string
		from  time.Time
		limit int
		want  []uint32
	}{
		{"everything", t0.Add(-time.Hour), 10, []uint32{5, 4, 3, 2, 1}},
		{"from the third call on", t0.Add(2 * time.Minute), 10, []uint32{5, 4, 3}},
		{"the newest two", t0.Add(-time.Hour), 2, []uint32{5, 4}},
		{"after the last", t0.Add(time.Hour), 10, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.Since(ctx, tc.from, tc.limit)
			if err != nil {
				t.Fatalf("Since: %v", err)
			}
			if !equal(sources(got), tc.want) {
				t.Errorf("got %v, want %v", sources(got), tc.want)
			}
		})
	}
}

// Break it: prune by end time, keep everything when nothing is to be kept, or
// delete what is inside the window, and a row fails.
func TestPruningIsByAgeAndCanBeEverything(t *testing.T) {
	now := t0.Add(48 * time.Hour)
	tests := []struct {
		name    string
		retain  time.Duration
		removed int64
		left    []uint32
	}{
		{"a day kept: the two older than a day go", 24 * time.Hour, 2, []uint32{3}},
		{"a week kept: nothing goes", 7 * 24 * time.Hour, 0, []uint32{3, 2, 1}},
		{"nothing kept: everything goes", 0, 3, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Written with a store that keeps, then pruned by the one under
			// test: a store that keeps nothing refuses to write at all.
			writer, db, ctx := openStore(t, time.Hour)
			for i, age := range []time.Duration{47 * time.Hour, 30 * time.Hour, time.Hour} {
				start := now.Add(-age)
				if err := writer.Record(ctx, Call{Started: start, Ended: start.Add(time.Second), Source: uint32(i + 1)}); err != nil {
					t.Fatalf("Record: %v", err)
				}
			}
			s := NewStore(db.SQL(), tc.retain)
			n, err := s.Prune(ctx, now)
			if err != nil || n != tc.removed {
				t.Fatalf("Prune removed %d and returned %v, want %d", n, err, tc.removed)
			}
			left, err := s.Since(ctx, now.Add(-1000*time.Hour), 10)
			if err != nil {
				t.Fatalf("Since: %v", err)
			}
			if !equal(sources(left), tc.left) {
				t.Errorf("left %v, want %v", sources(left), tc.left)
			}
		})
	}
}

// Break it: record with a store that keeps nothing, and a club that turned
// its record off still has one.
func TestAStoreThatKeepsNothingRecordsNothing(t *testing.T) {
	s, db, ctx := openStore(t, 0)
	if s.Enabled() {
		t.Fatal("a store with no retention reports itself enabled")
	}
	if err := s.Record(ctx, Call{Started: t0, Ended: t0.Add(time.Second), Source: 1}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	got, err := NewStore(db.SQL(), time.Hour).Since(ctx, t0.Add(-time.Hour), 10)
	if err != nil || len(got) != 0 {
		t.Errorf("%d calls stored, error %v", len(got), err)
	}
}

// The upgrade: a database that already holds a DMR record gains the P25 table
// and loses nothing.
//
// Break it: alter or rebuild the DMR table in the migration, and a club's
// record of who was on its network does not survive the upgrade.
func TestTheDMRRecordIsUntouchedByTheP25Table(t *testing.T) {
	_, db, ctx := openStore(t, time.Hour)
	if _, err := db.SQL().ExecContext(ctx,
		`INSERT INTO calls (started_at, ended_at, peer_id, stream_id, timeslot, source, target)
		 VALUES ('2026-10-01T00:00:00Z', '2026-10-01T00:00:05Z', 3132910, 77, 2, 3132913, 2)`); err != nil {
		t.Fatalf("writing a DMR call: %v", err)
	}
	var dmr, p25 int
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM calls`).Scan(&dmr); err != nil {
		t.Fatalf("counting DMR calls: %v", err)
	}
	if err := db.SQL().QueryRowContext(ctx, `SELECT COUNT(*) FROM p25_calls`).Scan(&p25); err != nil {
		t.Fatalf("the P25 table is not there: %v", err)
	}
	if dmr != 1 || p25 != 0 {
		t.Errorf("%d DMR calls and %d P25 calls", dmr, p25)
	}
}

// A nil store is no database, and everything it is asked is answered without
// one.
func TestNoStoreIsSafeToAsk(t *testing.T) {
	var s *Store
	ctx := context.Background()
	if s.Enabled() {
		t.Error("a nil store reports itself enabled")
	}
	if err := s.Record(ctx, Call{Started: t0, Ended: t0}); err != nil {
		t.Errorf("Record: %v", err)
	}
	if got, err := s.Since(ctx, t0, 10); got != nil || err != nil {
		t.Errorf("Since: %v, %v", got, err)
	}
	if n, err := s.Prune(ctx, t0); n != 0 || err != nil {
		t.Errorf("Prune: %d, %v", n, err)
	}
}
