package routing

import (
	"fmt"
	"sort"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// DefaultHangTime is how long a triggered bridge stays open after the last
// transmission on it.
//
// It has to outlast the gaps in a conversation. People pause between overs, and
// a bridge that closed the instant somebody unkeyed would drop the reply. Three
// minutes is the convention in this ecosystem and is long enough that a
// exchange survives while short enough that an idle link releases promptly.
const DefaultHangTime = 3 * time.Minute

// MaxHangTime bounds the failsafe.
//
// A triggered bridge held open by a mistyped hang time is the same failure the
// scheduler's maximum duration guards against: a talkgroup welded open with
// nobody watching.
const MaxHangTime = 30 * time.Minute

// Trigger opens a bridge when somebody transmits on one of its endpoints.
//
// This is the other half of the feature QSP exists for. A scheduled bridge is
// open because the calendar says so; a triggered bridge is open because
// somebody is using it, and closes itself when they stop. Clubs want both: a
// net at a fixed time, and a link that appears on demand rather than tying two
// talkgroups together permanently.
type Trigger struct {
	// Bridge names the bridge this trigger opens.
	Bridge string
	// On are the endpoints that open it. A transmission arriving at any of
	// them starts the hang timer.
	//
	// This is usually a subset of the bridge's own endpoints: a club might let
	// their local repeater trigger the link outward without letting the wider
	// network trigger it inward.
	On []Endpoint
	// HangTime is how long the bridge stays open after the last transmission.
	// Zero selects DefaultHangTime.
	HangTime time.Duration
	// Enabled allows a trigger to be suspended without deleting it.
	Enabled bool
}

// Validate reports whether the trigger is usable.
func (t Trigger) Validate() error {
	if t.Bridge == "" {
		return fmt.Errorf("a trigger must name the bridge it opens")
	}
	if len(t.On) == 0 {
		return fmt.Errorf("trigger for bridge %q has no endpoints, so nothing could ever open it", t.Bridge)
	}
	for i, e := range t.On {
		if err := e.Validate(); err != nil {
			return fmt.Errorf("trigger for bridge %q endpoint %d: %w", t.Bridge, i, err)
		}
	}
	if t.HangTime < 0 {
		return fmt.Errorf("trigger for bridge %q has a negative hang time", t.Bridge)
	}
	if t.HangTime > MaxHangTime {
		return fmt.Errorf("trigger for bridge %q has a hang time of %s; the maximum is %s, "+
			"which exists so that a mistyped value cannot hold a talkgroup open unattended",
			t.Bridge, t.HangTime, MaxHangTime)
	}
	return nil
}

// hangTime returns the effective hang time.
func (t Trigger) hangTime() time.Duration {
	if t.HangTime <= 0 {
		return DefaultHangTime
	}
	return t.HangTime
}

// matches reports whether a transmission arriving at from should open this
// trigger's bridge.
func (t Trigger) matches(from Endpoint) bool {
	for _, e := range t.On {
		if e.Matches(from) {
			return true
		}
	}
	return false
}

// Triggers tracks which bridges are currently held open by recent activity.
//
// Like the scheduler, it is level-triggered: it is asked which bridges should
// be open at an instant rather than remembering that it opened one. The state
// it does keep — when each bridge was last used — is the minimum a hang timer
// requires, and it is derived only from traffic that actually arrived.
//
// Not safe for concurrent use; owned by the goroutine that reads the socket.
type Triggers struct {
	triggers []Trigger
	// lastUsed maps a bridge to the time of the most recent transmission that
	// triggered it.
	lastUsed map[string]time.Time
}

// NewTriggers validates and builds a trigger set.
func NewTriggers(triggers []Trigger) (*Triggers, error) {
	out := make([]Trigger, 0, len(triggers))
	for _, t := range triggers {
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("invalid trigger: %w", err)
		}
		cp := t
		cp.On = append([]Endpoint(nil), t.On...)
		out = append(out, cp)
	}
	return &Triggers{triggers: out, lastUsed: make(map[string]time.Time)}, nil
}

// Observe records a transmission and reports any bridge it opened.
//
// It returns the names of bridges that were closed before this transmission and
// are open because of it, so the caller can log and announce them. A
// transmission on an already-open bridge simply extends its hang time and
// returns nothing.
func (t *Triggers) Observe(from Endpoint, now time.Time) []string {
	if t == nil {
		return nil
	}
	var opened []string
	for _, trig := range t.triggers {
		if !trig.Enabled || !trig.matches(from) {
			continue
		}
		last, held := t.lastUsed[trig.Bridge]
		if !held || now.Sub(last) > trig.hangTime() {
			opened = append(opened, trig.Bridge)
		}
		t.lastUsed[trig.Bridge] = now
	}
	sort.Strings(opened)
	return opened
}

// ActiveAt reports which bridges are currently held open.
//
// The shape matches scheduler.Schedule.ActiveAt so that a caller can merge the
// two without caring which mechanism opened a bridge.
func (t *Triggers) ActiveAt(now time.Time) map[string]bool {
	active := make(map[string]bool)
	if t == nil {
		return active
	}
	for _, trig := range t.triggers {
		if !trig.Enabled {
			continue
		}
		if last, held := t.lastUsed[trig.Bridge]; held && now.Sub(last) <= trig.hangTime() {
			active[trig.Bridge] = true
		}
	}
	return active
}

// Expire forgets bridges whose hang time has elapsed and reports them.
//
// Forgetting matters as well as reporting: without it, lastUsed grows for every
// bridge ever triggered, and a bridge that fell out of the configuration would
// keep an entry forever.
func (t *Triggers) Expire(now time.Time) []string {
	if t == nil {
		return nil
	}
	hang := make(map[string]time.Duration, len(t.triggers))
	for _, trig := range t.triggers {
		hang[trig.Bridge] = trig.hangTime()
	}

	var closed []string
	for bridge, last := range t.lastUsed {
		limit, known := hang[bridge]
		if !known || now.Sub(last) > limit {
			closed = append(closed, bridge)
			delete(t.lastUsed, bridge)
		}
	}
	sort.Strings(closed)
	return closed
}

// Bridges returns every bridge name a trigger refers to, sorted.
func (t *Triggers) Bridges() []string {
	if t == nil {
		return nil
	}
	seen := make(map[string]bool, len(t.triggers))
	var out []string
	for _, trig := range t.triggers {
		if !seen[trig.Bridge] {
			seen[trig.Bridge] = true
			out = append(out, trig.Bridge)
		}
	}
	sort.Strings(out)
	return out
}

// OpenFor reports how much longer a bridge will stay open, or zero if it is
// closed. The console shows this as a countdown.
func (t *Triggers) OpenFor(bridge string, now time.Time) time.Duration {
	if t == nil {
		return 0
	}
	for _, trig := range t.triggers {
		if trig.Bridge != bridge || !trig.Enabled {
			continue
		}
		if last, held := t.lastUsed[bridge]; held {
			if remaining := trig.hangTime() - now.Sub(last); remaining > 0 {
				return remaining
			}
		}
	}
	return 0
}

// EndpointOf builds an endpoint from a frame's arrival details.
func EndpointOf(peer hbp.RepeaterID, frame hbp.Data) Endpoint {
	return Endpoint{Peer: peer, Talkgroup: frame.TargetID, Timeslot: frame.Timeslot}
}
