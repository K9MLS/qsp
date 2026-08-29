// Package callsigns resolves radio IDs to the names their operators registered.
//
// **QSP already names the radios it can without asking anybody.** A hotspot
// announces its own callsign when it registers, and on most hotspots the
// operator's radio carries the same DMR ID, so an exact match needs no network
// and no cache. This package covers the rest: a member transmitting through
// somebody else's hotspot, or a second radio on a different ID.
//
// It looks each unknown ID up once, caches the answer, and never blocks a
// frame. See docs/adr/ADR-0030-radio-id-lookup.md, which is mostly about the
// registry's data use policy rather than its endpoint — they ask automated
// clients to identify themselves and to be gentle, and both shape this design.
//
// # Shape
//
// Resolver holds no sockets and performs no I/O. It decides what is worth
// asking for and what has already been answered; a Fetcher supplied by the
// caller does the asking. That is what makes every rule here testable without a
// network.
package callsigns

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Defaults for Options.
const (
	// DefaultNegativeTTL is how long an unknown ID is remembered as unknown.
	//
	// **Without this, every unregistered radio becomes a request on every
	// transmission**, which is precisely the excessive use the registry asks
	// people to avoid. A day is long enough to stop that and short enough that
	// somebody who registers today is named tomorrow.
	DefaultNegativeTTL = 24 * time.Hour

	// DefaultInterval is the minimum gap between requests.
	//
	// A club has a few dozen members and will resolve them once. Spacing the
	// requests costs nothing when there is nothing to ask and keeps a burst of
	// unknown IDs from arriving at the registry all at once.
	DefaultInterval = 2 * time.Second

	// DefaultQueueDepth bounds how many unresolved IDs are held.
	//
	// A queue that grows without limit turns a busy network, or a registry that
	// is down, into memory that is never returned.
	DefaultQueueDepth = 256
)

// ErrNoContact means lookups were enabled without a contact address.
var ErrNoContact = errors.New("callsigns: a contact address is required")

// Entry is what the registry says about one ID.
type Entry struct {
	// ID is the radio ID.
	ID uint32
	// Callsign, Name and Country as registered. Any may be empty.
	Callsign string
	Name     string
	Country  string
	// Known reports whether the registry had a record. A false entry is a
	// remembered absence, not a failed request.
	Known bool
	// FetchedAt is when this was learned.
	FetchedAt time.Time
}

// Display renders an entry for a console.
//
// Callsign first because it is what an operator recognises, then the given
// name. **Not the full registered name**: "Michael" beside a callsign is a
// person, and a surname and town beside every transmission is a list nobody can
// scan.
func (e Entry) Display() string {
	switch {
	case e.Callsign != "" && e.Name != "":
		return e.Callsign + " " + firstWord(e.Name)
	case e.Callsign != "":
		return e.Callsign
	default:
		return firstWord(e.Name)
	}
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ' '); i > 0 {
		return s[:i]
	}
	return s
}

// Fetcher asks the registry about one ID.
//
// An interface so the resolver can be tested without a network, and so the
// transport that knows about HTTP, User-Agent headers and JSON lives somewhere
// this package's rules do not.
type Fetcher interface {
	// Fetch returns what the registry says. A record that does not exist is
	// reported as an Entry with Known false and no error: an ID nobody has
	// registered is an ordinary answer, not a failure.
	Fetch(id uint32) (Entry, error)
}

// Store keeps resolved entries across restarts.
//
// **Restarting must not re-ask for everything QSP already knew.** An interface
// for the same reason as Fetcher: what is cached and for how long is decided
// here, and the SQL is elsewhere.
type Store interface {
	// Load returns every cached entry.
	Load() ([]Entry, error)
	// Save records one.
	Save(e Entry) error
}

// Options configures a Resolver.
type Options struct {
	// Contact is the address sent to the registry so it knows who is asking.
	// Required: their policy asks automated clients to identify themselves,
	// and QSP has no business inventing one on an operator's behalf.
	Contact string
	// NegativeTTL is how long an unknown ID stays unknown. Zero selects the
	// default.
	NegativeTTL time.Duration
	// Interval is the minimum gap between requests. Zero selects the default.
	Interval time.Duration
	// QueueDepth bounds unresolved IDs held. Zero selects the default.
	QueueDepth int
	// Now supplies the clock. Nil means time.Now.
	Now func() time.Time
}

// Resolver decides what to ask about and remembers the answers.
//
// It is not safe for concurrent use and is owned by whichever goroutine drives
// it, in the single-writer style the rest of QSP uses.
type Resolver struct {
	opts  Options
	now   func() time.Time
	cache map[uint32]Entry
	// pending are IDs seen and not yet resolved, oldest first.
	pending []uint32
	// queued is membership in pending, so the same ID seen thirty times in a
	// transmission is queued once.
	queued map[uint32]bool
	// lastRequest is when the registry was last asked anything.
	lastRequest time.Time
}

// New constructs a Resolver and loads whatever the store already holds.
func New(opts Options, store Store) (*Resolver, error) {
	if strings.TrimSpace(opts.Contact) == "" {
		return nil, fmt.Errorf("%w: the registry asks automated clients to say who "+
			"they are, and QSP will not invent an address for you", ErrNoContact)
	}
	if opts.NegativeTTL <= 0 {
		opts.NegativeTTL = DefaultNegativeTTL
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.QueueDepth <= 0 {
		opts.QueueDepth = DefaultQueueDepth
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	r := &Resolver{
		opts:   opts,
		now:    opts.Now,
		cache:  make(map[uint32]Entry),
		queued: make(map[uint32]bool),
	}

	if store != nil {
		entries, err := store.Load()
		if err != nil {
			return nil, fmt.Errorf("callsigns: cannot read the cache: %w", err)
		}
		for _, e := range entries {
			r.cache[e.ID] = e
		}
	}
	return r, nil
}

// Lookup returns what is known about an ID, and queues it if nothing is.
//
// **It never blocks and never reaches the network.** A radio ID seen in a
// transmission is queued and resolved later; the name appears the next time the
// console asks. A network call has no business anywhere near a routing
// decision.
func (r *Resolver) Lookup(id uint32) (Entry, bool) {
	if id == 0 {
		return Entry{}, false
	}

	e, cached := r.cache[id]
	if cached {
		if e.Known {
			return e, true
		}
		// A remembered absence. Re-asked once it has aged out, so somebody who
		// registers today is named tomorrow.
		if r.now().Sub(e.FetchedAt) < r.opts.NegativeTTL {
			return Entry{}, false
		}
	}

	r.enqueue(id)
	return Entry{}, false
}

// enqueue adds an ID to the queue if it is not already there.
func (r *Resolver) enqueue(id uint32) {
	if r.queued[id] {
		return
	}
	if len(r.pending) >= r.opts.QueueDepth {
		// The oldest is dropped rather than the newest refused. A busy network
		// should resolve what it is hearing now, not what it heard when the
		// queue filled — and a queue that grows without limit turns a registry
		// outage into memory that is never returned.
		oldest := r.pending[0]
		r.pending = r.pending[1:]
		delete(r.queued, oldest)
	}
	r.pending = append(r.pending, id)
	r.queued[id] = true
}

// Next returns the next ID to ask about, or false when there is nothing to ask
// or it is too soon to ask again.
//
// The caller does the asking and reports back with Record. Spacing requests is
// the resolver's job because it is what knows how many are waiting.
func (r *Resolver) Next() (uint32, bool) {
	if len(r.pending) == 0 {
		return 0, false
	}
	now := r.now()
	if !r.lastRequest.IsZero() && now.Sub(r.lastRequest) < r.opts.Interval {
		return 0, false
	}

	id := r.pending[0]
	r.pending = r.pending[1:]
	delete(r.queued, id)
	r.lastRequest = now
	return id, true
}

// Record stores what the registry said, and reports whether it is worth saving.
//
// A failed request is not recorded at all: a registry that was briefly
// unreachable has said nothing about the ID, and remembering that as an absence
// would hide a real name for a day.
func (r *Resolver) Record(id uint32, e Entry, err error) (Entry, bool) {
	if err != nil {
		return Entry{}, false
	}
	e.ID = id
	e.FetchedAt = r.now()
	r.cache[id] = e
	return e, true
}

// Pending reports how many IDs are waiting, for the health report.
func (r *Resolver) Pending() int { return len(r.pending) }

// Cached reports how many entries are held.
func (r *Resolver) Cached() int { return len(r.cache) }

// UserAgent is what QSP tells the registry about itself.
//
// **Their policy asks for a clear identification and a contact address**, and
// this is where that is honoured. The version matters as much as the name: if
// one release of QSP misbehaves, they can tell which.
func UserAgent(version, contact string) string {
	return fmt.Sprintf("QSP/%s (+https://github.com/K9MLS/qsp; %s)", version, contact)
}
