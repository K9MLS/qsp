package peers

import (
	"fmt"
	"slices"
	"time"

	"github.com/k9mls/qsp/internal/routing"
)

// CollisionNote is one run of frames a destination refused, kept so that
// COLLISIONS can say what it counted.
//
// **A count with no time and no names reads as a fault.** On 2026-10-27 the
// Overview showed 748 collisions in amber. They were three key-overs the day
// before, each a second or two of somebody talking over Zello, and nothing on
// the page said so: a number that only rises, in the colour for trouble, on a
// network that was working.
//
// One note is one run, not one frame. A key-over is fifty frames a second to
// every destination the talkgroup reaches, and a list of those would be the
// counter again in a longer form.
type CollisionNote struct {
	// Started and At are the first and last refused frame of the run, in UTC.
	Started time.Time `json:"started"`
	At      time.Time `json:"at"`
	// Refusals is the frames refused in this run, across every destination;
	// these are what COLLISIONS counted.
	Refusals uint64 `json:"refusals"`
	// Summary is the run in one sentence, naming who was refused and why.
	Summary string `json:"summary"`
	// KeyOver says another transmission held the destination: somebody
	// talked over somebody. False for every other refusal COLLISIONS counts.
	KeyOver bool `json:"key_over"`
}

// maxCollisionNotes bounds the record, as maxDropNotes does and for the same
// reason: enough to account for the number an operator questions, too few to
// become a log.
const maxCollisionNotes = 20

// collisionRunGap is how long a run may pause and still be one run.
//
// A transmission's frames are 60 ms apart, so a pause of seconds is the
// talker unkeying. Three seconds joins one over without joining the next one
// the same person makes after hearing nobody answer.
const collisionRunGap = 3 * time.Second

// collisionSummary is the sentence for a refused frame.
func collisionSummary(d routing.Drop) (summary string, keyOver bool) {
	if c := d.Contention; c != nil {
		return fmt.Sprintf("%s keyed up while %s was carrying TG %d",
			c.Refused, c.Holder, c.Talkgroup), true
	}
	return fmt.Sprintf("not sent to %s: %s", d.To, d.Reason), false
}

// withCollision adds one refused frame to notes and returns them.
//
// **Joined to the latest run with the same sentence**, not to the last note,
// because one key-over refused at five destinations at once interleaves with
// nothing else and two at once interleave with each other.
func withCollision(notes []CollisionNote, summary string, keyOver bool, now time.Time) []CollisionNote {
	for i := len(notes) - 1; i >= 0; i-- {
		n := &notes[i]
		if n.Summary != summary {
			continue
		}
		if now.Sub(n.At) <= collisionRunGap {
			n.At = now
			n.Refusals++
			return notes
		}
		// Any earlier run with this sentence ended earlier still.
		break
	}
	notes = append(notes, CollisionNote{
		Started: now, At: now, Refusals: 1, Summary: summary, KeyOver: keyOver,
	})
	if len(notes) > maxCollisionNotes {
		notes = slices.Delete(notes, 0, len(notes)-maxCollisionNotes)
	}
	return notes
}

// countCollision counts one refused frame and records it.
func (l *Listener) countCollision(summary string, keyOver bool) {
	l.collided.Add(1)
	now := time.Now().UTC()

	l.collisionNoteMu.Lock()
	defer l.collisionNoteMu.Unlock()
	l.collisionNotes = withCollision(l.collisionNotes, summary, keyOver, now)
}

// RecentCollisions returns the most recent runs of refused frames, newest
// first.
func (l *Listener) RecentCollisions() []CollisionNote {
	l.collisionNoteMu.Lock()
	defer l.collisionNoteMu.Unlock()

	out := slices.Clone(l.collisionNotes)
	slices.Reverse(out)
	return out
}
