package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// fakeCallStore keeps the call store's retention rule and no database: rows
// are the ages of the calls it holds.
type fakeCallStore struct {
	retain time.Duration
	ages   []time.Duration
	err    error
	pruned int
}

// Enabled is what calls.Store reports: nothing is kept at zero retention.
func (f *fakeCallStore) Enabled() bool { return f.retain > 0 }

func (f *fakeCallStore) Prune(context.Context, time.Time) (int64, error) {
	f.pruned++
	if f.err != nil {
		return 0, f.err
	}
	var kept []time.Duration
	for _, age := range f.ages {
		if f.retain > 0 && age <= f.retain {
			kept = append(kept, age)
		}
	}
	removed := len(f.ages) - len(kept)
	f.ages = kept
	return int64(removed), nil
}

// TestStartupPrunesWhateverTheRetention: `dmr.calls.retain: 0` is documented
// as keeping nothing, and startup used to skip pruning for exactly that
// setting -- a store with zero retention is "not enabled" -- so the month a
// club had already kept stayed in the database, and in the console, for ever.
//
// To see it fail: make the first line of pruneCallsAtStart
// `if e, ok := store.(interface{ Enabled() bool }); ok && !e.Enabled() { return }`
// and the zero-retention row keeps both of its calls.
func TestStartupPrunesWhateverTheRetention(t *testing.T) {
	day := 24 * time.Hour
	tests := []struct {
		name     string
		retain   time.Duration
		ages     []time.Duration
		err      error
		wantLeft int
	}{
		{"zero retention removes what an earlier setting kept", 0, []time.Duration{time.Hour, 20 * day}, nil, 0},
		{"thirty days keeps what is inside the window", 30 * day, []time.Duration{time.Hour, 20 * day, 40 * day}, nil, 2},
		{"an empty history is not an error", 0, nil, nil, 0},
		{"a database that refuses is reported, not fatal", 0, []time.Duration{time.Hour}, errors.New("database is locked"), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeCallStore{retain: tc.retain, ages: tc.ages, err: tc.err}
			pruneCallsAtStart(context.Background(), store, logging.Discard(), time.Now().UTC())
			if store.pruned != 1 {
				t.Errorf("Prune was called %d time(s), want once", store.pruned)
			}
			if len(store.ages) != tc.wantLeft {
				t.Errorf("%d call(s) left, want %d", len(store.ages), tc.wantLeft)
			}
		})
	}
}

// TestSettingRetentionToZeroRemovesTheHistoryAlreadyKept is the same thing
// through the real database: keep a call, restart with retention at zero, and
// the call is gone.
//
// To see it fail: the same change as TestStartupPrunesWhateverTheRetention.
func TestSettingRetentionToZeroRemovesTheHistoryAlreadyKept(t *testing.T) {
	cfg := testConfig(t)
	cfg.DMR.Calls.Retain = config.Duration(time.Hour)
	ctx := context.Background()
	now := time.Now().UTC()

	a, err := build(ctx, cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if a.callStore == nil {
		_ = a.shutdown(ctx)
		t.Skip("this binary has no database driver; there is no history to remove")
	}
	start := now.Add(-10 * time.Minute)
	if err := a.callStore.Record(ctx, calls.Call{
		Key:     calls.Key{Peer: 1, Stream: 7, Timeslot: hbp.Timeslot2},
		Source:  3132910,
		Target:  2,
		Started: start,
		Ended:   start.Add(time.Second),
	}); err != nil {
		t.Fatalf("recording: %v", err)
	}
	if err := a.shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	cfg.DMR.Calls.Retain = 0
	b, err := build(ctx, cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build with zero retention: %v", err)
	}
	defer func() { _ = b.shutdown(ctx) }()
	left, err := b.callStore.Since(ctx, now.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(left) != 0 {
		t.Errorf("%d call(s) survived a restart with dmr.calls.retain at zero", len(left))
	}
}
