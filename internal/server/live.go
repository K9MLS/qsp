package server

import "sync/atomic"

// live holds the options a saved configuration can change under a running
// server.
//
// **These were captured once at construction and never updated**, so a saved
// change to the join page or the map applied to nothing while
// `config.NeedsRestart` reported that no restart was needed. Both statements
// were individually reasonable and together they were a lie: the operator was
// told the change was live, and it was not.
//
// Found by changing the network name through the API and watching the join page
// keep the old one.
type live struct {
	join       atomic.Pointer[JoinSettings]
	mapping    atomic.Pointer[MapSettings]
	forwarding atomic.Bool
	// logins reports refused logins. Attached after construction because the
	// peer master is built after the server that reports on it, and reordering
	// the two would trade a clear dependency for a circular one.
	logins atomic.Pointer[LoginReporter]
}

// Join returns the current join settings.
func (s *Server) Join() JoinSettings {
	if v := s.live.join.Load(); v != nil {
		return *v
	}
	return s.opts.Join
}

// MapSettings returns the current map settings.
func (s *Server) MapSettings() MapSettings {
	if v := s.live.mapping.Load(); v != nil {
		return *v
	}
	return s.opts.Map
}

// Forwarding reports whether this instance relays traffic.
func (s *Server) Forwarding() bool {
	if s.live.join.Load() == nil {
		// Nothing has been applied yet, so the constructed value stands. The
		// join pointer is the marker rather than a second flag, because two
		// things recording "has a configuration been applied" is one more than
		// can stay in step.
		return s.opts.Forwarding
	}
	return s.live.forwarding.Load()
}

// ApplyConfig updates the settings a running server can change.
//
// It is called after a configuration is saved, from whichever goroutine did the
// saving, and is safe against the handlers reading concurrently — which is why
// these are atomics rather than fields under a mutex the handlers would have to
// take on every request.
//
// **It does not change anything that needs a restart.** The listen address, the
// proxy setting and the console assets are not here, and `config.NeedsRestart`
// names them so an operator is told rather than left to notice.
func (s *Server) ApplyConfig(join JoinSettings, mapping MapSettings, forwarding bool) {
	s.live.mapping.Store(&mapping)
	s.live.forwarding.Store(forwarding)
	// Stored last: it is what Forwarding reads to decide whether anything has
	// been applied, so the others must already be in place when it appears.
	s.live.join.Store(&join)
}

// SetLogins attaches the peer master's refusal reporting.
//
// Called once, after the master exists. Reading it through an atomic rather
// than a field because every handler reads it and the writer is a different
// goroutine — the same reason the settings above are atomics.
func (s *Server) SetLogins(r LoginReporter) {
	s.live.logins.Store(&r)
}

// logins returns the reporter, or nil when there is none.
func (s *Server) loginReporter() LoginReporter {
	if v := s.live.logins.Load(); v != nil {
		return *v
	}
	return s.opts.Logins
}
