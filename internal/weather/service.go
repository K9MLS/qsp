package weather

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// The two modes. In Preview an alert that passes every check is shown and
// logged and nothing is transmitted; in Transmit it goes on the air.
const (
	ModePreview  = "preview"
	ModeTransmit = "transmit"
)

// recentLimit bounds the list of recent decisions the page shows.
const recentLimit = 50

// sentMemory is how long an alert that was sent is remembered after it
// expires, so a late update that references it is still recognised as one.
const sentMemory = 24 * time.Hour

// Options configures a Service.
type Options struct {
	// Log receives one line per alert that would go on the air and one per
	// change in whether NWS can be reached. Required.
	Log *slog.Logger
	// Version is QSP's, sent in the User-Agent.
	Version string
	// BaseURL overrides DefaultBaseURL, for tests.
	BaseURL string
	// Now overrides the clock, for tests.
	Now func() time.Time
	// Interval overrides PollInterval, for tests.
	Interval time.Duration
	// Sender puts an alert on the air on this server's stations. Nil when
	// the DMR listener is not running, which the page reports.
	Sender Sender
	// Pace overrides the transmit pacing, for tests.
	Pace Pacing
}

// AlertView is one alert as the Weather page shows it.
type AlertView struct {
	ID      string    `json:"id"`
	Event   string    `json:"event"`
	Area    string    `json:"area"`
	Until   time.Time `json:"until,omitzero"`
	Verdict Verdict   `json:"verdict"`
	// Reason says why an alert was held; empty for one that was sent.
	Reason string `json:"reason,omitempty"`
	// Text is the alert exactly as it would read on a radio.
	Text      string    `json:"text"`
	DecidedAt time.Time `json:"decided_at"`
	// SentAt is when it went on the air, in Transmit.
	SentAt time.Time `json:"sent_at,omitzero"`
	// Waiting says why an alert to be sent has not gone yet: the timeslot is
	// busy, the limit per ten minutes is reached, or it could not be sent.
	Waiting string `json:"waiting,omitempty"`
}

// Status is what the Weather page shows.
type Status struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
	// Zones says what NWS called each configured code, or why it could not.
	Zones       []ZoneCheck `json:"zones"`
	LastPoll    time.Time   `json:"last_poll,omitzero"`
	LastSuccess time.Time   `json:"last_success,omitzero"`
	// LastError is the most recent failure to read NWS, cleared by the next
	// success.
	LastError string `json:"last_error,omitempty"`
	// Active are the alerts NWS lists for the configured areas right now.
	Active []AlertView `json:"active"`
	// Recent are the latest decisions, newest first, including alerts that
	// have since expired.
	Recent    []AlertView `json:"recent"`
	Talkgroup uint32      `json:"talkgroup"`
	Timeslot  int         `json:"timeslot"`
	SenderID  uint32      `json:"sender_id"`
	// CanTransmit is false when this server has no way to put a text on the
	// air (the DMR listener is off), so the page can say so before the
	// operator switches to Transmit and wonders why nothing arrives.
	CanTransmit bool `json:"can_transmit"`
	// Queued is how many alerts are waiting to go out.
	Queued int `json:"queued"`
	// Limit is the most alerts sent in any ten minutes.
	Limit int `json:"limit"`
}

// Service polls NWS and decides.
type Service struct {
	opts Options
	log  *slog.Logger
	wake chan struct{}

	mu       sync.Mutex
	settings Settings
	client   *Client
	// zones caches NWS's answer for each code; zoneProblems holds why a code
	// could not be looked up.
	zones        map[string]Zone
	zoneProblems map[string]string
	// baselined is false until the first successful poll after the service
	// was turned on or its areas changed. That poll sends nothing.
	baselined bool
	// sent maps the ID of every alert that went on the air (or would have,
	// before the baseline) to when it stops applying.
	sent map[string]time.Time
	// decided holds the view of every alert still active, so each is decided
	// once rather than once a minute.
	decided map[string]AlertView
	active  []AlertView
	recent  []AlertView

	lastPoll, lastSuccess time.Time
	lastError             string

	// queue holds alerts waiting to go on the air, in the order they go;
	// sentTimes are when recent ones went, for the limit per ten minutes.
	queue     []queued
	sentTimes []time.Time
	reference uint32

	// generation counts Applys. A poll reads it with the settings and its
	// result is discarded if it changed while the poll was out at NWS:
	// otherwise a poll under the old area finishes after a save, marks the
	// new area baselined, and the next poll sends what was already running
	// in a county just added.
	generation uint64
}

// New builds a service that is off until Apply turns it on.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval <= 0 {
		opts.Interval = PollInterval
	}
	opts.Pace = opts.Pace.withDefaults()
	return &Service{
		opts:         opts,
		log:          opts.Log,
		wake:         make(chan struct{}, 1),
		zones:        map[string]Zone{},
		zoneProblems: map[string]string{},
		sent:         map[string]time.Time{},
		decided:      map[string]AlertView{},
	}
}

// Apply adopts new settings, from the Weather page's save or at startup.
//
// **Turning the service on, or changing its areas or the alert types chosen,
// starts a new baseline**: the next poll records what is already in effect
// without sending it. A restart during a flood watch must not put a day-old
// watch on the air, and neither must adding a county.
func (s *Service) Apply(set Settings) {
	set.Zones = NormalizeZones(set.Zones)

	s.mu.Lock()
	prev := s.settings
	s.settings = set
	s.generation++
	if !set.Enabled || !prev.Enabled || !slices.Equal(prev.Zones, set.Zones) || !sameEvents(prev.Events, set.Events) {
		s.baselined = false
		s.decided = map[string]AlertView{}
		s.active = nil
		// Decided under the old settings; the new baseline decides again.
		s.queue = nil
	}
	if !set.Enabled || !set.Transmit {
		// Nothing waits to go out once alerts are off or back in Preview.
		s.queue = nil
	}
	if !slices.Equal(prev.Zones, set.Zones) {
		// A code NWS did not know is asked about again once the operator
		// has changed the list, and not every minute before that.
		s.zoneProblems = map[string]string{}
	}
	if !set.Enabled {
		s.lastError = ""
	}
	if s.client == nil || prev.Contact != set.Contact {
		s.client = nil
		if c, err := NewClient(s.opts.BaseURL, s.opts.Version, set.Contact); err == nil {
			s.client = c
		}
	}
	s.mu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// sameEvents compares two event lists as sets, without regard to case.
func sameEvents(a, b []string) bool {
	norm := func(in []string) []string {
		out := make([]string, 0, len(in))
		for _, e := range in {
			out = append(out, strings.ToLower(strings.TrimSpace(e)))
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(norm(a), norm(b))
}

// Run polls until ctx ends: at once when turned on, then every interval.
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.opts.Interval)
	defer ticker.Stop()
	// The first poll below covers any Apply made before Run started, so a
	// wake it left behind is dropped rather than causing a second poll at
	// once.
	select {
	case <-s.wake:
	default:
	}
	// Alerts waiting to go out are tried far more often than NWS is read:
	// a warning should not wait a minute behind a call that ends now.
	flush := time.NewTicker(s.opts.Pace.Retry)
	defer flush.Stop()
	for {
		s.Poll(ctx)
		s.Flush(ctx)
		if !s.waitForPoll(ctx, ticker.C, flush.C) {
			return
		}
	}
}

// waitForPoll sends what is queued as the flush ticker fires, until it is
// time to read NWS again. It reports false when ctx ends.
func (s *Service) waitForPoll(ctx context.Context, poll, flush <-chan time.Time) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-poll:
			return true
		case <-s.wake:
			return true
		case <-flush:
			s.Flush(ctx)
		}
	}
}

// Poll reads NWS once and decides every alert not yet decided. It does
// nothing while the service is off. Exported for tests; Run calls it.
func (s *Service) Poll(ctx context.Context) {
	s.mu.Lock()
	set := s.settings
	client := s.client
	gen := s.generation
	var lookup []string
	for _, code := range set.Zones {
		_, known := s.zones[code]
		if !known && !unknownProblem(s.zoneProblems[code]) {
			lookup = append(lookup, code)
		}
	}
	s.mu.Unlock()

	if !set.Enabled || len(set.Zones) == 0 {
		return
	}
	now := s.opts.Now()
	if client == nil {
		s.failed(now, ErrNoContact.Error())
		return
	}

	// Names and time zones first, outside the lock: they are what an alert
	// is written with, and a code NWS does not know is left out of the alert
	// request rather than failing it for every other code.
	found := map[string]Zone{}
	problems := map[string]string{}
	for _, code := range lookup {
		z, err := client.Zone(ctx, code)
		switch {
		case err == nil:
			found[code] = z
		case errors.Is(err, ErrUnknownZone):
			problems[code] = problemUnknown
		default:
			problems[code] = "could not be checked: " + err.Error()
		}
	}

	s.mu.Lock()
	for code, z := range found {
		s.zones[code] = z
		delete(s.zoneProblems, code)
	}
	for code, p := range problems {
		s.zoneProblems[code] = p
	}
	var ask []string
	for _, code := range set.Zones {
		if !unknownProblem(s.zoneProblems[code]) {
			ask = append(ask, code)
		}
	}
	s.mu.Unlock()

	if len(ask) == 0 {
		s.failed(now, "none of the codes is one the National Weather Service knows")
		return
	}
	alerts, err := client.ActiveAlerts(ctx, ask)
	if err != nil {
		s.failed(now, err.Error())
		return
	}
	s.decide(gen, set, alerts, now)
}

// problemUnknown is what a code NWS does not have is reported as.
const problemUnknown = "the National Weather Service does not know this code"

func unknownProblem(p string) bool { return p == problemUnknown }

// failed records a poll that did not reach NWS, logging only a change.
func (s *Service) failed(now time.Time, problem string) {
	s.mu.Lock()
	changed := s.lastError != problem
	s.lastPoll = now
	s.lastError = problem
	s.mu.Unlock()
	if changed {
		s.log.Warn("weather alerts cannot be read", slog.String("problem", problem))
	}
}

// decide runs every alert not yet decided through Decide and records it,
// unless the settings changed while the poll was out (see generation).
func (s *Service) decide(gen uint64, set Settings, alerts []Alert, now time.Time) {
	// Oldest first, so an alert and the update that references it, arriving
	// in one poll, are decided in the order NWS issued them.
	sort.SliceStable(alerts, func(i, j int) bool { return alerts[i].Sent.Before(alerts[j].Sent) })

	s.mu.Lock()
	defer s.mu.Unlock()

	if gen != s.generation {
		// Decided under settings that no longer apply. The save that changed
		// them woke the loop, and the next poll decides under the new ones.
		return
	}
	recovered := s.lastError != ""
	s.lastPoll, s.lastSuccess, s.lastError = now, now, ""

	sentNow := make(map[string]bool, len(s.sent))
	for id := range s.sent {
		sentNow[id] = true
	}

	// Only the areas configured now name an alert, in the operator's order;
	// the cache may hold codes an operator has since removed.
	zones := make([]Zone, 0, len(set.Zones))
	for _, code := range set.Zones {
		if z, ok := s.zones[code]; ok {
			zones = append(zones, z)
		}
	}

	active := make([]AlertView, 0, len(alerts))
	stillActive := make(map[string]bool, len(alerts))
	for _, a := range alerts {
		stillActive[a.ID] = true
		if v, ok := s.decided[a.ID]; ok {
			active = append(active, v)
			continue
		}
		verdict, reason := Decide(a, set, sentNow, now)
		if verdict == Send && !s.baselined {
			verdict, reason = Hold, ReasonBaseline
		}
		// Recorded as sent for both: a baseline alert was on the air before
		// QSP looked, and an update chain must keep being recognised.
		if (verdict == Send || reason == ReasonBaseline || reason == ReasonUpdate) && a.ID != "" {
			// Remembered until it stops applying; one with no end NWS gave
			// is remembered for a day from now, so it is still forgotten.
			until := a.until()
			if until.IsZero() {
				until = now
			}
			s.sent[a.ID] = until
			sentNow[a.ID] = true
		}
		v := AlertView{
			ID:        a.ID,
			Event:     a.Event,
			Area:      areaName(a, zones),
			Until:     a.until(),
			Verdict:   verdict,
			Reason:    reason,
			Text:      Format(a, zones, now),
			DecidedAt: now,
		}
		if verdict == Send && set.Transmit {
			v.Waiting = waitingQueued
			s.queue = append(s.queue, queued{id: a.ID, text: v.Text, event: a.Event, until: a.until(), issued: a.Sent})
		}
		s.decided[a.ID] = v
		active = append(active, v)
		s.recent = append([]AlertView{v}, s.recent...)
		if len(s.recent) > recentLimit {
			s.recent = s.recent[:recentLimit]
		}
		if verdict == Send && !set.Transmit {
			s.log.Info("weather alert would be sent (preview only; nothing transmitted)",
				slog.String("text", v.Text),
				slog.Uint64("talkgroup", uint64(set.Talkgroup)),
				slog.Int("timeslot", set.Timeslot),
				slog.Uint64("from", uint64(set.SenderID)),
				slog.String("nws_id", a.ID))
		}
	}
	s.baselined = true
	s.active = active

	for id := range s.decided {
		if !stillActive[id] {
			delete(s.decided, id)
		}
	}
	for id, until := range s.sent {
		if now.Sub(until) > sentMemory {
			delete(s.sent, id)
		}
	}
	if recovered {
		s.log.Info("weather alerts can be read again")
	}
}

// areaName names the first configured area an alert covers.
func areaName(a Alert, zones []Zone) string {
	for _, z := range zones {
		if covers(a, z.Code) {
			return z.Name
		}
	}
	area, _, _ := strings.Cut(a.AreaDesc, ";")
	return strings.TrimSpace(area)
}

// Status reports what the service is doing, for the page.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()

	mode := ModePreview
	if s.settings.Transmit {
		mode = ModeTransmit
	}
	st := Status{
		Enabled:     s.settings.Enabled,
		Mode:        mode,
		CanTransmit: s.opts.Sender != nil,
		Queued:      len(s.queue),
		Limit:       s.opts.Pace.Limit,
		LastPoll:    s.lastPoll,
		LastSuccess: s.lastSuccess,
		LastError:   s.lastError,
		Active:      slices.Clone(s.active),
		Recent:      slices.Clone(s.recent),
		Talkgroup:   s.settings.Talkgroup,
		Timeslot:    s.settings.Timeslot,
		SenderID:    s.settings.SenderID,
	}
	if st.Active == nil {
		st.Active = []AlertView{}
	}
	if st.Recent == nil {
		st.Recent = []AlertView{}
	}
	for _, code := range s.settings.Zones {
		check := ZoneCheck{Code: code}
		if z, ok := s.zones[code]; ok {
			zz := z
			check.OK, check.Zone = true, &zz
		} else if p, ok := s.zoneProblems[code]; ok {
			check.Problem = p
		} else {
			check.Problem = "not checked yet"
		}
		st.Zones = append(st.Zones, check)
	}
	if st.Zones == nil {
		st.Zones = []ZoneCheck{}
	}
	return st
}

// CheckZones asks NWS about each code, for the page's "Check codes" button
// and before a save. contact is used when the service has none yet, because
// an operator checks codes before they have saved anything.
func (s *Service) CheckZones(ctx context.Context, codes []string, contact string) []ZoneCheck {
	s.mu.Lock()
	client := s.client
	s.mu.Unlock()
	if strings.TrimSpace(contact) != "" {
		if c, err := NewClient(s.opts.BaseURL, s.opts.Version, contact); err == nil {
			client = c
		}
	}

	codes = NormalizeZones(codes)
	out := make([]ZoneCheck, 0, len(codes))
	for _, code := range codes {
		check := ZoneCheck{Code: code}
		switch {
		case !ValidZoneCode(code):
			check.Problem = "not the shape of a county code (TXC121) or a zone code (TXZ103)"
		case client == nil:
			check.Problem = "cannot be checked until a contact email is given, because the National Weather Service asks for one"
		default:
			z, err := client.Zone(ctx, code)
			switch {
			case err == nil:
				zz := z
				check.OK, check.Zone = true, &zz
				s.mu.Lock()
				s.zones[code] = z
				delete(s.zoneProblems, code)
				s.mu.Unlock()
			case errors.Is(err, ErrUnknownZone):
				check.Problem = problemUnknown
			default:
				check.Problem = fmt.Sprintf("could not be checked: %v", err)
			}
		}
		out = append(out, check)
	}
	return out
}
