package auth

import (
	"net/netip"
	"sync"
	"time"
)

// Limits on what the login flow remembers and spends.
const (
	// maxThrottledSources is how many source addresses the login flow remembers
	// at once.
	//
	// **A table an anonymous sender can grow is a table that needs a ceiling.**
	// Each entry is a failed attempt from an address not seen before, so
	// without one a flood from many addresses is paid for in memory here. At
	// the ceiling the entry heard from longest ago is forgotten, which costs a
	// guesser's count and nothing else.
	maxThrottledSources = 4096

	// maxConcurrentVerifies is how many passwords are checked at once.
	//
	// A password check is deliberately expensive (ADR-0006), and the login form
	// is reachable by anybody. The per-address limit bounds what one address
	// can make QSP spend; this bounds what every address together can, so a
	// flood of unknown names takes a fixed share of the machine rather than all
	// of it while a bridge is carrying voice. Attempts beyond it wait their
	// turn rather than being refused.
	maxConcurrentVerifies = 2
)

// throttleKey is what a source address is counted under.
//
// **An IPv6 address is counted by its /64.** One subscriber is routinely handed
// a whole /64, so counting single addresses would give one person eighteen
// quintillion fresh sets of attempts. An address that does not parse is counted
// as written, which is still one key per distinct sender.
func throttleKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}

// sourceState is what is remembered about one source address.
type sourceState struct {
	// failures is how many attempts have failed since first.
	failures int
	// first is when the current run of failures began.
	first time.Time
	// last is when the address was last heard from, for eviction.
	last time.Time
	// until is when the address is answered again; zero when it is not being
	// refused.
	until time.Time
	// marked is the account the refusal was written on, zero when it was
	// written on none. It is what lets `qsp unlock` reach a refusal held in
	// another process; see Service.Authenticate.
	//
	// **Set only once the mark is in the database**, by wrote. Until 0.1.325
	// this was the last real account the address had failed against, whether
	// or not anything was written on it. Four guesses at a real name and a
	// fifth at a name nobody holds tripped the refusal with no mark written,
	// and the next guess at the real name found an account with no mark,
	// took that for `qsp unlock`, and was forgiven: unlimited guesses, four
	// in every five of them real.
	marked int64
}

// sourceThrottle counts failed logins by where they came from.
//
// **By source and not by account**, because a count kept on the account is a
// switch anybody can throw: five wrong passwords for somebody else's name, sent
// again every lockout period, kept a real administrator out of their own
// console indefinitely. Counted by source, the person guessing is the person
// refused.
//
// It lives in memory. A restart forgets it, which hands whoever is guessing one
// fresh set of attempts per restart — and they cannot restart the server.
type sourceThrottle struct {
	mu      sync.Mutex
	sources map[string]*sourceState
	limit   int
	window  time.Duration
	max     int
}

func newSourceThrottle(limit int, window time.Duration) *sourceThrottle {
	return &sourceThrottle{
		sources: make(map[string]*sourceState),
		limit:   limit,
		window:  window,
		max:     maxThrottledSources,
	}
}

// refused reports whether an address is being refused, until when, and the
// account that refusal was written on.
func (t *sourceThrottle) refused(key string, now time.Time) (until time.Time, marked int64, refused bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	st, ok := t.sources[key]
	if !ok {
		return time.Time{}, 0, false
	}
	if st.expired(now, t.window) {
		delete(t.sources, key)
		return time.Time{}, 0, false
	}
	if st.until.IsZero() {
		return time.Time{}, 0, false
	}
	st.last = now
	return st.until, st.marked, true
}

// fail counts one failed attempt. It returns when the address will be answered
// again if this attempt was the one that used up its allowance, and the zero
// time otherwise.
func (t *sourceThrottle) fail(key string, now time.Time) time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()

	st, ok := t.sources[key]
	if ok && st.expired(now, t.window) {
		ok = false
	}
	if !ok {
		if len(t.sources) >= t.max {
			t.makeRoom(now)
		}
		st = &sourceState{first: now}
		t.sources[key] = st
	}
	st.failures++
	st.last = now
	if st.failures >= t.limit {
		st.until = now.Add(t.window)
	}
	return st.until
}

// wrote records that an address's refusal is now written on an account.
func (t *sourceThrottle) wrote(key string, account int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if st, ok := t.sources[key]; ok && !st.until.IsZero() {
		st.marked = account
	}
}

// forget drops what is remembered about an address.
func (t *sourceThrottle) forget(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.sources, key)
}

// size is how many addresses are remembered.
func (t *sourceThrottle) size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sources)
}

// expired reports whether an entry no longer says anything: its refusal has
// ended, or its run of failures began longer ago than the window.
func (s *sourceState) expired(now time.Time, window time.Duration) bool {
	if !s.until.IsZero() {
		return !now.Before(s.until)
	}
	return now.Sub(s.first) >= window
}

// makeRoom frees at least one slot. The caller holds the lock.
//
// Everything expired goes first. If the table is still full, the address heard
// from longest ago goes: an entry has to lose, and the one that has been quiet
// longest is the one least likely to be mid-guess.
func (t *sourceThrottle) makeRoom(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for key, st := range t.sources {
		if st.expired(now, t.window) {
			delete(t.sources, key)
			continue
		}
		if oldestKey == "" || st.last.Before(oldest) {
			oldestKey, oldest = key, st.last
		}
	}
	if len(t.sources) >= t.max && oldestKey != "" {
		delete(t.sources, oldestKey)
	}
}
