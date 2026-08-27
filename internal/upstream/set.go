package upstream

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"

	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// Set holds the configured links and routes sends to the right one.
//
// It exists so that callers name a link the way a bridge does — by the string
// an administrator wrote in the configuration — rather than holding pointers.
// A bridge naming a link that does not exist is a configuration error, and the
// error says which name was not found rather than failing silently.
type Set struct {
	log   *slog.Logger
	mu    sync.RWMutex
	links map[string]*Link
	order []string
}

// NewSet creates an empty set.
func NewSet(log *slog.Logger) *Set {
	return &Set{log: log, links: make(map[string]*Link)}
}

// Add registers a link. Names must be unique.
func (s *Set) Add(l *Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	name := l.cfg.Name
	if _, exists := s.links[name]; exists {
		return fmt.Errorf("upstream: two links are named %q", name)
	}
	s.links[name] = l
	s.order = append(s.order, name)
	sort.Strings(s.order)
	return nil
}

// Len reports how many links are configured.
func (s *Set) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.links)
}

// Names returns the configured link names, sorted.
func (s *Set) Names() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.order...)
}

// Send carries a frame over the named link.
//
// An unknown name is an error rather than a silent discard: a bridge pointing
// at a link that no longer exists would otherwise appear to work while carrying
// nothing, which is the hardest kind of fault to notice.
func (s *Set) Send(name string, frame hbp.Data) error {
	s.mu.RLock()
	link, ok := s.links[name]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("upstream: no link named %q is configured", name)
	}
	return link.Send(frame)
}

// Start opens every link. A failure stops the rest, because a partially opened
// set is a configuration an operator did not ask for.
func (s *Set) Start(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, name := range s.order {
		if err := s.links[name].Start(ctx); err != nil {
			return err
		}
	}
	return nil
}

// Close stops every link, reporting the first failure but attempting all of
// them: a shutdown that gives up halfway leaks sockets.
func (s *Set) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var first error
	for _, name := range s.order {
		if err := s.links[name].Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Statuses returns each link's status, in name order.
func (s *Set) Statuses() []Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Status, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.links[name].Status())
	}
	return out
}

// HealthCheck reports one link's state.
//
// Each link gets its own check rather than one aggregate, because an operator
// with two links needs to know which one is quiet. An aggregate would say
// "degraded" and leave them to work out which.
type HealthCheck struct {
	// Link is the link to report on.
	Link *Link
}

// Name implements health.Checker.
func (h HealthCheck) Name() string { return "upstream:" + h.Link.cfg.Name }

// Check implements health.Checker.
//
// A stale link is degraded rather than unhealthy. QSP does not know it is
// broken — OpenBridge has no keep-alive — and reporting a fault it cannot
// confirm would teach an operator to ignore the report.
func (h HealthCheck) Check(context.Context) health.Result {
	st := h.Link.Status()

	switch {
	case !st.Open:
		return health.Unavailable(st.Summary)

	case !st.EverReceived && st.Stats.Rejected > 0:
		return health.Degraded(st.Summary,
			"check the passphrase file matches what the far end was given; "+
				"the two must be byte-identical, including any trailing newline")

	case !st.EverReceived:
		return health.Degraded(st.Summary,
			"confirm the far end has this server's current public address and that "+
				"UDP reaches this port; an address change breaks an OpenBridge link silently")

	case st.Stale:
		return health.Degraded(st.Summary,
			"if the talkgroup is genuinely quiet, raise stale_after; if not, "+
				"check the link with the far end's operator")

	default:
		return health.Healthy(st.Summary)
	}
}

// CheckFor returns the health check for a named link.
//
// It returns a check that reports the absence rather than nil, so a caller
// registering checks for every name in Names cannot accidentally register
// nothing.
func (s *Set) CheckFor(name string) health.Checker {
	s.mu.RLock()
	link, ok := s.links[name]
	s.mu.RUnlock()

	if !ok {
		return missingLink(name)
	}
	return HealthCheck{Link: link}
}

// missingLink reports a link that was named but not configured.
type missingLink string

func (m missingLink) Name() string { return "upstream:" + string(m) }

func (m missingLink) Check(context.Context) health.Result {
	return health.Unavailable(fmt.Sprintf("no link named %q is configured", string(m)))
}
