package callsigns

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// pollInterval is how often the service looks for something to resolve.
//
// Short enough that a name appears while somebody is still looking at the page,
// long enough that an idle instance costs nothing. The resolver's own interval
// is what actually spaces requests to the registry; this only decides how
// promptly a queued ID is noticed.
const pollInterval = time.Second

// Service owns a Resolver and the goroutine that feeds it.
//
// **The Resolver is single-writer and this is what makes that true.** It is
// read by every console request and written by one background goroutine, so
// every path goes through this type's mutex — which is held across a map read
// and never across a network call.
type Service struct {
	log     *slog.Logger
	fetcher Fetcher
	store   Store

	mu       sync.Mutex
	resolver *Resolver

	// failures counts consecutive fetch errors, for backing off.
	failures int
}

// NewService constructs the service.
func NewService(log *slog.Logger, r *Resolver, f Fetcher, store Store) *Service {
	return &Service{log: log, resolver: r, fetcher: f, store: store}
}

// Lookup returns what is known about an ID, queueing it if nothing is.
//
// Safe from any goroutine, and never reaches the network: a console request
// must not wait on a third party, and a routing decision must not touch one at
// all.
func (s *Service) Lookup(id uint32) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolver.Lookup(id)
}

// Pending reports how many IDs are waiting, for the health report.
func (s *Service) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolver.Pending()
}

// Cached reports how many entries are held.
func (s *Service) Cached() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolver.Cached()
}

// Run resolves queued IDs until the context ends.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.resolveOne(ctx)
		}
	}
}

// resolveOne asks about at most one ID.
//
// One per tick rather than draining the queue: the registry asks callers to be
// gentle, and a burst of thirty unknown IDs arriving at once is the opposite of
// that however well the resolver spaces them internally.
func (s *Service) resolveOne(ctx context.Context) {
	s.mu.Lock()
	id, ok := s.resolver.Next()
	s.mu.Unlock()
	if !ok {
		return
	}

	// **The fetch happens outside the lock.** Holding it across a network call
	// would stall every console request behind a registry that is slow, which
	// is exactly the coupling this design exists to avoid.
	entry, err := s.fetcher.Fetch(id)

	if err != nil {
		s.failures++
		// Logged once as it starts, then quietly. A registry that is down
		// produces one line an operator can act on rather than one per minute
		// they learn to skim past.
		if s.failures == 1 {
			s.log.Warn("cannot resolve a radio ID",
				slog.Uint64("radio_id", uint64(id)),
				slog.String("error", err.Error()),
				slog.Bool("temporary", IsTemporary(err)),
			)
		}
		return
	}
	if s.failures > 0 {
		s.log.Info("radio ID lookups are working again",
			slog.Int("after_failures", s.failures))
		s.failures = 0
	}

	s.mu.Lock()
	stored, worth := s.resolver.Record(id, entry, nil)
	s.mu.Unlock()

	if !worth || s.store == nil {
		return
	}
	if err := s.store.Save(stored); err != nil {
		// The name is already in memory and will be used; failing to cache it
		// costs a lookup after the next restart and nothing else.
		s.log.Warn("cannot cache a resolved radio ID",
			slog.Uint64("radio_id", uint64(id)), slog.String("error", err.Error()))
	}
}
