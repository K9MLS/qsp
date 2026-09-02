package peers

import (
	"sort"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// DefaultSubscriberTimeout is how long a radio's location is trusted after it
// was last heard.
//
// It is much longer than a peer timeout, because the two answer different
// questions. A peer that stops sending keepalives is gone; a radio that stops
// transmitting is merely quiet, and quiet is the normal state of a radio. Too
// short and a private call to somebody who spoke five minutes ago fails for no
// reason a user can see. Too long and a call follows an operator to the hotspot
// they were at this morning rather than the one they are at now.
//
// Two hours is a working shift. It is a guess informed by nothing but
// plausibility, and is configurable so an operator who finds it wrong can say
// so.
const DefaultSubscriberTimeout = 2 * time.Hour

// Location is where a radio was last heard.
//
// **It is learned from traffic and never configured.** Which radio is behind
// which hotspot changes when somebody drives to work, so there is nothing an
// operator could usefully write down. See
// docs/adr/ADR-0021-private-calls-and-data.md.
type Location struct {
	// Subscriber is the radio ID.
	Subscriber uint32
	// Peer is the repeater or hotspot it was last heard through.
	Peer hbp.RepeaterID
	// Timeslot is the slot it last used. Recorded because a peer's two slots
	// are independent paths and a private call has to pick one.
	Timeslot hbp.Timeslot
	// FirstSeen is when this radio was first heard anywhere, in UTC.
	FirstSeen time.Time
	// LastHeard is when it last transmitted, in UTC.
	LastHeard time.Time
}

// Idle reports how long since the radio transmitted.
func (l Location) Idle(now time.Time) time.Duration { return now.Sub(l.LastHeard) }

// observe records that a radio was heard through a peer.
//
// A radio moving between hotspots is ordinary rather than suspicious — it is
// somebody driving — so the newest sighting simply wins. There is no
// confirmation step and no preference for the incumbent: preferring the older
// record would send private calls to the hotspot an operator has just left,
// which is the failure this exists to prevent.
func (m *Master) observe(subscriber uint32, peer hbp.RepeaterID, slot hbp.Timeslot, now time.Time) {
	if subscriber == 0 {
		// Radio ID 0 is not a station. It appears in malformed frames and must
		// not become a routable destination.
		return
	}
	if existing, ok := m.subscribers[subscriber]; ok {
		existing.Peer = peer
		existing.Timeslot = slot
		existing.LastHeard = now
		return
	}
	m.subscribers[subscriber] = &Location{
		Subscriber: subscriber,
		Peer:       peer,
		Timeslot:   slot,
		FirstSeen:  now,
		LastHeard:  now,
	}
}

// Locate reports where a radio was last heard.
//
// It returns false for a radio never heard, one whose location has aged out,
// and one whose peer has since disconnected. That last case matters: a stale
// pointer to a departed peer would route a private call to a socket nobody is
// listening on, and the caller would hear nothing with no explanation.
func (m *Master) Locate(subscriber uint32) (Location, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.locate(subscriber)
}

// locate is Locate without the lock, for callers that already hold it.
func (m *Master) locate(subscriber uint32) (Location, bool) {
	loc, ok := m.subscribers[subscriber]
	if !ok {
		return Location{}, false
	}
	if loc.Idle(m.cfg.Now().UTC()) > m.cfg.SubscriberTimeout {
		return Location{}, false
	}
	if p, ok := m.peers[loc.Peer]; !ok || !p.State.CanPassTraffic() {
		return Location{}, false
	}
	return *loc, true
}

// Locations returns every remembered radio, ordered by ID.
//
// The result is a copy. Entries whose peer has gone are included, because "this
// radio was here and its hotspot has left" is information an operator wants
// rather than something to hide; Locate is the routing question and applies the
// stricter test.
func (m *Master) Locations() []Location {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Location, 0, len(m.subscribers))
	for _, loc := range m.subscribers {
		out = append(out, *loc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Subscriber < out[j].Subscriber })
	return out
}

// SubscriberCount returns how many radios are currently remembered.
func (m *Master) SubscriberCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.subscribers)
}

// expireSubscribers forgets radios not heard within the timeout.
//
// Without it the map grows for the life of the process, one entry per radio
// ever heard, which on a busy network is unbounded — and every entry is a
// location claim that gets less true with age.
func (m *Master) expireSubscribers(now time.Time) []Location {
	var stale []Location
	for id, loc := range m.subscribers {
		if loc.Idle(now) > m.cfg.SubscriberTimeout {
			stale = append(stale, *loc)
			delete(m.subscribers, id)
		}
	}
	sort.Slice(stale, func(i, j int) bool { return stale[i].Subscriber < stale[j].Subscriber })
	return stale
}

// LocateFor implements routing.SubscriberLookup.
//
// A separate method from Locate rather than a changed signature: Locate returns
// the whole record because the console and the operator want it, and the
// routing core wants only the two fields it can act on. Widening the core's
// dependency to a struct it does not need would make it harder to test with a
// stub.
func (m *Master) LocateFor(subscriber uint32) (hbp.RepeaterID, hbp.Timeslot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	loc, ok := m.locate(subscriber)
	if !ok {
		return 0, 0, false
	}
	return loc.Peer, loc.Timeslot, true
}
