package server

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// What an anonymous sender can make the audit trail hold.
//
// **Every refused sign-in used to be a row, with the name as typed.** The
// name could be 8 KiB, an address already being refused still got a row for
// every further attempt, and nothing removed any of it, so a script pointed
// at the login form filled the database at whatever rate it could post
// (found 2026-10-07). Three limits, each on a different part of that:
//
//   - a name is recorded up to maxAuditActor characters;
//   - an address being refused is recorded once for each refusal, not once
//     for each attempt made during it;
//   - refused sign-ins from everybody together are recorded up to
//     authAuditPerMinute, and the rest of that minute are counted and
//     recorded as one row saying how many.
//
// A sign-in that succeeds is always recorded: it needs a password.
const (
	// maxAuditActor is twice the longest username an account can have, so a
	// near miss on a real name is recorded whole.
	maxAuditActor = 64

	// authAuditPerMinute is far above an operator mistyping, and a ceiling
	// of about 29,000 rows a day under an attack that never stops.
	authAuditPerMinute = 20

	// maxRefusalsRemembered bounds the once-per-refusal table. Past it the
	// table is emptied; the cost is a second row for an address still being
	// refused, and the per-minute limit holds whatever happens here.
	maxRefusalsRemembered = 1024
)

// auditActor is a name as the trail records it.
func auditActor(name string) string {
	name = strings.TrimSpace(strings.ToValidUTF8(name, "?"))
	if name == "" {
		// A blank actor is refused by the recorder, and "somebody sent no
		// name" is worth a row.
		return "(no name)"
	}
	if utf8.RuneCountInString(name) <= maxAuditActor {
		return name
	}
	cut := 0
	for i := range name {
		if cut == maxAuditActor {
			return name[:i] + "…"
		}
		cut++
	}
	return name
}

// authAuditBudget decides which refused sign-ins are recorded.
type authAuditBudget struct {
	mu sync.Mutex
	// minute is when the current minute began; used and dropped are counted
	// within it.
	minute  time.Time
	used    int
	dropped int
	// refusals is, for each address being refused, when that refusal ends.
	refusals map[string]time.Time
}

// allow reports whether a refused sign-in may be recorded now. unrecorded is
// how many an earlier minute left out, reported once, when the next row is
// about to be written.
func (b *authAuditBudget) allow(now time.Time) (ok bool, unrecorded int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if now.Sub(b.minute) >= time.Minute || now.Before(b.minute) {
		unrecorded = b.dropped
		b.minute, b.used, b.dropped = now, 0, 0
	}
	if b.used >= authAuditPerMinute {
		b.dropped++
		return false, 0
	}
	b.used++
	return true, unrecorded
}

// firstOfRefusal reports whether this is the first attempt seen from source
// during the refusal that ends at until.
func (b *authAuditBudget) firstOfRefusal(source string, until, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if known, ok := b.refusals[source]; ok && known.Equal(until) {
		return false
	}
	if len(b.refusals) >= maxRefusalsRemembered {
		for k, end := range b.refusals {
			if !now.Before(end) {
				delete(b.refusals, k)
			}
		}
		if len(b.refusals) >= maxRefusalsRemembered {
			clear(b.refusals)
		}
	}
	if b.refusals == nil {
		b.refusals = make(map[string]time.Time)
	}
	b.refusals[source] = until
	return true
}
