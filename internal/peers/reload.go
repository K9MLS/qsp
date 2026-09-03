package peers

import (
	"log/slog"
	"time"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/routing"
)

// Reload is a configuration change waiting to be applied.
//
// **It exists because an HTTP handler cannot apply one.** `routing.Core` is
// single-writer and owned by the goroutine that reads the socket, per ADR-0002,
// so a handler calling `SetTable` is a data race — and one the detector would
// only sometimes catch, because the two are genuinely concurrent. See
// docs/adr/ADR-0027-configuration-writes.md.
type Reload struct {
	// Table is the new routing table, already built from the new
	// configuration. Nil leaves the current one alone.
	Table *routing.Table
	// Access is the new talkgroup access. Applied whether or not it changed,
	// because a zero value is a meaningful setting rather than an absence.
	Access access.Lists
	// Triggers replaces the PTT triggers. Nil leaves them alone.
	Triggers *routing.Triggers
	// ScheduleState and Rebuild replace the listener's view of the schedule.
	//
	// Functions rather than a schedule type, matching ListenerConfig: the
	// listener has never needed to know how a schedule is built, and a reload
	// is not the place to teach it.
	ScheduleState func(now time.Time) map[string]bool
	Rebuild       func(now time.Time) (*routing.Table, error)
	// Subscription replaces the per-peer attachment settings.
	Subscription SubscriptionConfig
	// Author is who saved it, for the log line. A reload with no name in it
	// is one an operator cannot attribute later.
	Author string
	// Summary is what they said they were doing, and may be empty.
	Summary string
}

// Apply queues a configuration change.
//
// It returns immediately. The change is applied by the listener's own goroutine
// at the top of its next sweep, which runs every second — not distinguishable
// from immediate to somebody who has just pressed a button, and safe in a way
// that applying it here would not be.
//
// A second call before the first is applied replaces it. Two saves a
// half-second apart should leave the instance running the later one, and
// queueing both would apply the earlier one to no purpose.
func (l *Listener) Apply(r *Reload) {
	l.pending.Store(r)
}

// applyPending installs a queued configuration change.
//
// Called from the sweep, on the goroutine that owns the routing core.
func (l *Listener) applyPending() {
	r := l.pending.Swap(nil)
	if r == nil {
		return
	}

	if l.cfg.Routing != nil {
		if r.Table != nil {
			// In-flight transmissions keep their reservations and the new
			// table applies from the next frame, exactly as a scheduled change
			// does. An operator saving a bridge mid-net does not cut anybody
			// off mid-sentence.
			l.cfg.Routing.SetTable(r.Table)
		}
		// Access is applied unconditionally: its zero value permits everything,
		// which is a setting rather than the absence of one, so skipping it
		// when empty would make "remove every restriction" impossible to save.
		l.cfg.Routing.SetAccess(r.Access)
	}

	if r.Triggers != nil {
		l.cfg.Triggers = r.Triggers
	}
	if r.ScheduleState != nil {
		l.cfg.ScheduleState = r.ScheduleState
	}
	if r.Rebuild != nil {
		l.cfg.Rebuild = r.Rebuild
	}
	if l.cfg.Master != nil {
		l.cfg.Master.SetSubscription(r.Subscription)
		// The registration and subscriber lists, which used to be read once
		// when the master was built. Applied unconditionally for the same
		// reason as routing's: an empty list permits everything, which is a
		// setting rather than the absence of one.
		l.cfg.Master.SetAccess(r.Access)
	}

	// The schedule is reset rather than merged, so that a bridge removed from
	// the configuration stops being tracked instead of lingering as a name the
	// next sweep tries to enable.
	l.scheduleState = nil

	l.log.Info("configuration applied",
		slog.String("author", r.Author),
		slog.String("summary", r.Summary),
	)

	if l.cfg.Bus != nil {
		l.cfg.Bus.Publish(events.TypeRouteChanged, map[string]any{
			"source": "configuration",
			"author": r.Author,
		})
	}
}
