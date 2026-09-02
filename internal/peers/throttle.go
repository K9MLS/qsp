package peers

import (
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Defaults for login throttling.
const (
	// DefaultMaxLoginFailures is how many wrong passwords a source may send
	// before QSP stops answering it.
	//
	// A hotspot with a mistyped password retries every ten seconds
	// indefinitely, so this is reached in under a minute by an honest mistake
	// as readily as by an attack. That is the point: both should stop.
	DefaultMaxLoginFailures = 6

	// DefaultLoginLockout is how long QSP then ignores that source.
	//
	// **This bounds guessing rather than preventing it.** Six attempts every
	// five minutes is about seventy an hour, which is hopeless against a
	// password worth having and ruinous against a weak one — the peer password
	// still has to be a real one.
	DefaultLoginLockout = 5 * time.Minute

	// loginFailureWindow is how long a failure counts for. A hotspot that fails
	// once a day for a week is somebody who fixed it and broke it again, not
	// somebody guessing.
	loginFailureWindow = 15 * time.Minute
)

// FailureReason says what went wrong with a login.
//
// **"Authentication failed" covered three different problems**, and an operator
// reading it had to guess which. They need different things done: a wrong
// password is fixed on the hotspot, an unknown ID is fixed in the
// configuration, and a digest from the wrong address is either a network
// oddity or somebody replaying a capture.
type FailureReason string

// Login failure reasons.
const (
	// ReasonWrongPassword means the digest did not match.
	ReasonWrongPassword FailureReason = "wrong password"
	// ReasonUnknownID means no password is configured for that repeater.
	ReasonUnknownID FailureReason = "no password configured for this ID"
	// ReasonWrongAddress means the digest arrived from somewhere other than
	// where the challenge was issued.
	ReasonWrongAddress FailureReason = "answered from a different address"
	// ReasonUnsolicited means a digest arrived with no challenge outstanding.
	ReasonUnsolicited FailureReason = "no challenge was outstanding"
)

// loginAttempts counts failures per source address.
//
// **Per address rather than per repeater ID**, because an ID is whatever the
// caller claims and a determined guesser would vary it. The address is the one
// thing a remote party cannot choose freely.
type loginAttempts struct {
	failures int
	// first is when the current run of failures began, and last when it was
	// extended.
	first time.Time
	last  time.Time
	// until is when the lockout ends, zero when not locked.
	until time.Time
	// reasons counts what went wrong, so the log line an operator eventually
	// reads can say which problem repeated.
	reasons map[FailureReason]int
	// warned records that the run has been reported once, so forty attempts
	// produce one warning rather than forty.
	warned bool
}

// throttle tracks failed logins.
type throttle struct {
	max      int
	lockout  time.Duration
	attempts map[netip.Addr]*loginAttempts
}

func newThrottle(max int, lockout time.Duration) *throttle {
	if max <= 0 {
		max = DefaultMaxLoginFailures
	}
	if lockout <= 0 {
		lockout = DefaultLoginLockout
	}
	return &throttle{max: max, lockout: lockout, attempts: map[netip.Addr]*loginAttempts{}}
}

// locked reports whether a source is currently being ignored.
func (t *throttle) locked(from netip.AddrPort, now time.Time) bool {
	a := t.attempts[from.Addr()]
	return a != nil && !a.until.IsZero() && now.Before(a.until)
}

// fail records a failure and reports whether the source has just been locked
// out, along with a description for the log.
func (t *throttle) fail(from netip.AddrPort, reason FailureReason, now time.Time) (locked bool, summary string) {
	addr := from.Addr()
	a := t.attempts[addr]

	// A run that has gone quiet starts again. A hotspot that fails once a day
	// for a week is somebody who fixed it and broke it again.
	if a == nil || now.Sub(a.last) > loginFailureWindow {
		a = &loginAttempts{first: now, reasons: map[FailureReason]int{}}
		t.attempts[addr] = a
	}

	a.failures++
	a.last = now
	a.reasons[reason]++

	if a.failures < t.max {
		return false, ""
	}

	a.until = now.Add(t.lockout)
	if a.warned {
		// Already reported. Extending a lockout in silence is right: an
		// operator has been told, and the log should not fill with it.
		return true, ""
	}
	a.warned = true

	return true, fmt.Sprintf("%d failed logins from %s in %s (%s); ignoring it for %s",
		a.failures, addr, now.Sub(a.first).Truncate(time.Second),
		describeReasons(a.reasons), t.lockout)
}

// succeed clears a source's history.
//
// A peer that gets in was the honest case, and holding its earlier mistakes
// against it would lock out a member who fixed their password.
func (t *throttle) succeed(from netip.AddrPort) {
	delete(t.attempts, from.Addr())
}

// expire forgets sources whose lockouts and runs have both lapsed.
func (t *throttle) expire(now time.Time) {
	for addr, a := range t.attempts {
		lockedStill := !a.until.IsZero() && now.Before(a.until)
		recent := now.Sub(a.last) <= loginFailureWindow
		if !lockedStill && !recent {
			delete(t.attempts, addr)
		}
	}
}

// blocked reports how many sources are currently locked out, for health.
func (t *throttle) blocked(now time.Time) int {
	var n int
	for _, a := range t.attempts {
		if !a.until.IsZero() && now.Before(a.until) {
			n++
		}
	}
	return n
}

// describeReasons renders the failure counts for an operator.
func describeReasons(reasons map[FailureReason]int) string {
	var out string
	for _, r := range []FailureReason{
		ReasonWrongPassword, ReasonUnknownID, ReasonWrongAddress, ReasonUnsolicited,
	} {
		n := reasons[r]
		if n == 0 {
			continue
		}
		if out != "" {
			out += ", "
		}
		out += fmt.Sprintf("%d %s", n, r)
	}
	if out == "" {
		return "no reason recorded"
	}
	return out
}

// LoginFailure is one refused login, for the console.
type LoginFailure struct {
	// Address is where it came from, without the port: the port changes on
	// every attempt and is noise in a list.
	Address string `json:"address"`
	// RepeaterID is the ID claimed, which may be one nobody holds.
	RepeaterID uint32 `json:"repeater_id"`
	// Reason is what went wrong.
	Reason string `json:"reason"`
	// Failures is how many times this source has failed in the current run.
	Failures int `json:"failures"`
	// Since is when the run began.
	Since time.Time `json:"since"`
	// LockedUntil is when QSP will listen to it again, zero when it is not
	// locked out.
	LockedUntil time.Time `json:"locked_until,omitempty"`
}

// recentFailures returns what is being refused, newest run first.
func (t *throttle) recentFailures(now time.Time, claimed map[netip.Addr]hbp.RepeaterID) []LoginFailure {
	out := make([]LoginFailure, 0, len(t.attempts))
	for addr, a := range t.attempts {
		if now.Sub(a.last) > loginFailureWindow {
			continue
		}
		f := LoginFailure{
			Address:  addr.String(),
			Reason:   describeReasons(a.reasons),
			Failures: a.failures,
			Since:    a.first.UTC(),
		}
		if id, ok := claimed[addr]; ok {
			f.RepeaterID = uint32(id)
		}
		if !a.until.IsZero() && now.Before(a.until) {
			f.LockedUntil = a.until.UTC()
		}
		out = append(out, f)
	}
	return out
}

// noteFailure records a refused login and reports it once per run.
//
// **One warning per run, not one per attempt.** A hotspot with a wrong password
// retries every ten seconds for as long as it is switched on: forty log lines
// saying the same thing is how an operator learns to skim past the one that
// matters. The line names the reason, because "authentication failed" covered
// three different problems and each needs something different done.
func (m *Master) noteFailure(id hbp.RepeaterID, from netip.AddrPort, reason FailureReason, now time.Time) {
	if m.claimedIDs == nil {
		m.claimedIDs = map[netip.Addr]hbp.RepeaterID{}
	}
	m.claimedIDs[from.Addr()] = id

	locked, summary := m.logins.fail(from, reason, now)
	if summary != "" {
		m.log.Warn("refusing logins from this address", slog.String("detail", summary))
		return
	}
	if locked {
		// Already reported; the lockout is simply extended.
		return
	}
	m.log.Warn("authentication failed",
		logging.PeerID(uint32(id)),
		slog.String("from", from.String()),
		slog.String("reason", string(reason)),
	)
}

// LoginFailures reports what is currently being refused.
func (m *Master) LoginFailures(now time.Time) []LoginFailure {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.logins.recentFailures(now, m.claimedIDs)
}

// BlockedSources reports how many addresses are locked out.
func (m *Master) BlockedSources(now time.Time) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.logins.blocked(now)
}
