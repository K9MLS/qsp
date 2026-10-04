// Package p25calls is the record of P25 transmissions: the ones in progress,
// the ones just finished, and the ones kept in the database.
//
// **A tracker of its own, beside the DMR one and not inside it** (ADR-0059).
// The DMR tracker is keyed on a repeater ID, a stream ID and a timeslot. A P25
// call has none of them, so putting one there would mean inventing all three.
// The rule that tracker's history settled is that one event has one record,
// and this keeps it: a P25 call is recorded here and nowhere else.
//
// Two listeners report to it, the P25 gateway listener and the Motorola
// repeater link. **A call is recorded by the listener it came in through, and
// by no other**: a repeater's call relayed to a gateway is the repeater's.
package p25calls

import (
	"slices"
	"sync"
	"time"
)

// DefaultHistory is how many finished calls Last heard keeps in memory, the
// same number the DMR tracker keeps.
const DefaultHistory = 50

// Where a call came into QSP.
const (
	// ViaRepeater is a Motorola repeater linked over V.24.
	ViaRepeater = "repeater"
	// ViaGateway is a hotspot or gateway on the P25 network port.
	ViaGateway = "gateway"
)

// EndReason says how a call finished.
type EndReason string

const (
	// EndMarked is a call its own station closed: a repeater's end marker, a
	// gateway's terminator.
	EndMarked EndReason = "ended"
	// EndQuiet is a call that stopped arriving and was closed by a timer.
	EndQuiet EndReason = "went quiet"
	// EndLinkClosed is a call open when its link closed or QSP stopped.
	EndLinkClosed EndReason = "link closed"
)

// Call is one P25 transmission.
type Call struct {
	Started time.Time
	// Ended is zero while the call is in progress.
	Ended time.Time
	// Source is the radio that keyed up, and Talkgroup where. **Zero is "not
	// said", not an ID**: a call shorter than one voice unit ends before
	// either has been transmitted.
	Source    uint32
	Talkgroup uint16
	// Frames is voice frames heard.
	Frames int
	// ViaKind is ViaRepeater or ViaGateway, and Via what QSP calls the
	// station the call came in through.
	ViaKind string
	Via     string
	// Carried is false for a call that was heard and not relayed, because
	// another station had the floor when it began.
	Carried   bool
	EndReason EndReason
}

// InProgress reports a call that has not ended.
func (c Call) InProgress() bool { return c.Ended.IsZero() }

// Duration is how long the call ran, or has run so far.
func (c Call) Duration(now time.Time) time.Duration {
	if c.InProgress() {
		return now.Sub(c.Started)
	}
	return c.Ended.Sub(c.Started)
}

// Options configures a Tracker.
type Options struct {
	// History is how many finished calls are kept in memory. Zero is
	// DefaultHistory.
	History int
	// OnStart and OnEnd are called, outside the tracker's lock, when a call
	// begins and when one finishes. Either may be nil.
	OnStart func(Call)
	OnEnd   func(Call)
}

// Tracker holds the calls in progress and the last few finished.
//
// A nil *Tracker accepts every call and keeps nothing, so a listener reports
// to it without asking whether there is one.
type Tracker struct {
	opts Options

	mu     sync.Mutex
	active map[string]Call
	recent []Call // newest first
}

// NewTracker returns an empty tracker.
func NewTracker(opts Options) *Tracker {
	if opts.History <= 0 {
		opts.History = DefaultHistory
	}
	return &Tracker{opts: opts, active: make(map[string]Call)}
}

// Heard records a call in progress as it stands now. key names the station it
// is coming in through and must be the same for every frame of the call: one
// station carries one call at a time.
//
// The first Heard for a key is the call beginning.
func (t *Tracker) Heard(key string, c Call) {
	if t == nil {
		return
	}
	c.Ended = time.Time{}
	t.mu.Lock()
	_, known := t.active[key]
	t.active[key] = c
	t.mu.Unlock()
	if !known && t.opts.OnStart != nil {
		t.opts.OnStart(c)
	}
}

// Finished records a call that is over, and returns it as recorded. A call
// with no end time is given at; one that was never Heard is recorded all the
// same, because a call that began and ended inside one report still happened.
func (t *Tracker) Finished(key string, c Call, at time.Time) Call {
	if t == nil {
		return c
	}
	if c.Ended.IsZero() {
		c.Ended = at
	}
	if c.EndReason == "" {
		c.EndReason = EndMarked
	}
	t.mu.Lock()
	delete(t.active, key)
	t.recent = append([]Call{c}, t.recent...)
	if len(t.recent) > t.opts.History {
		t.recent = t.recent[:t.opts.History]
	}
	t.mu.Unlock()
	if t.opts.OnEnd != nil {
		t.opts.OnEnd(c)
	}
	return c
}

// Snapshot returns the calls in progress, oldest first, and the finished
// ones, newest first.
func (t *Tracker) Snapshot() (active, recent []Call) {
	if t == nil {
		return nil, nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	active = make([]Call, 0, len(t.active))
	for _, c := range t.active {
		active = append(active, c)
	}
	slices.SortFunc(active, func(a, b Call) int { return a.Started.Compare(b.Started) })
	return active, slices.Clone(t.recent)
}

// Seed fills the finished calls from the record, newest first, so Last heard
// is not empty after a restart. It replaces nothing already there: a call
// that finished since the tracker was made stays ahead of the record.
func (t *Tracker) Seed(newestFirst []Call) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recent = append(t.recent, newestFirst...)
	if len(t.recent) > t.opts.History {
		t.recent = t.recent[:t.opts.History]
	}
}
