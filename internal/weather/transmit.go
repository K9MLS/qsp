package weather

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// Sender puts a group text on the air on this server's own stations: its
// hotspots and its Motorola repeaters, and never a link, a linked QSP server
// or a transcoder. *peers.Listener satisfies it through SendLocalText.
//
// **Weather is local.** A Denton tornado warning is for Denton's stations; a
// server in Iowa linked to this one has its own Weather page for its own
// counties. So an alert is sent through a path that cannot leave this server,
// rather than through the one an announcement takes, which reaches everybody.
type Sender interface {
	SendLocalText(slot hbp.Timeslot, m tms.Message) error
}

// Pacing is how fast alerts go out.
type Pacing struct {
	// Gap is the least time between two alerts, so a radio has finished
	// showing one before the next arrives. Zero selects 15 seconds.
	Gap time.Duration
	// Limit is the most alerts sent in any Window. An outbreak issues
	// warnings faster than a talkgroup should carry them; the rest wait,
	// warnings first, and are dropped only if they expire. Zero selects 6.
	Limit int
	// Window is the period Limit counts over. Zero selects ten minutes.
	Window time.Duration
	// Retry is how often a waiting alert is tried again. Zero selects five
	// seconds.
	Retry time.Duration
}

func (p Pacing) withDefaults() Pacing {
	if p.Gap <= 0 {
		p.Gap = 15 * time.Second
	}
	if p.Limit <= 0 {
		p.Limit = 6
	}
	if p.Window <= 0 {
		p.Window = 10 * time.Minute
	}
	if p.Retry <= 0 {
		p.Retry = 5 * time.Second
	}
	return p
}

// queued is an alert waiting to go on the air.
type queued struct {
	id, text, event string
	until, issued   time.Time
}

// Waiting reasons, in the operator's words.
const (
	waitingQueued  = "waiting to go out"
	waitingExpired = "expired before it could be sent"
	waitingNoDMR   = "cannot be sent: this server's DMR listener is off, so there is nothing to send it on"
)

// ErrCannotTransmit is returned by SendTest on a server with no way to send.
var ErrCannotTransmit = errors.New("weather: this server's DMR listener is off, so nothing can be sent")

// TestText is what Send test puts on the air. Plainly a test, from the same
// ID and on the same talkgroup a real alert would use, so it proves the whole
// path to a radio's screen.
const TestText = "QSP WEATHER TEST: this is only a test"

// rank orders alerts in the queue: warnings, then watches, then the rest. In
// an outbreak, a tornado warning must not wait behind a watch.
func rank(event string) int {
	e := strings.ToLower(event)
	switch {
	case strings.Contains(e, "warning"):
		return 0
	case strings.Contains(e, "watch"):
		return 1
	}
	return 2
}

// Flush sends the next waiting alert if the pacing allows. Run calls it
// every few seconds; it does nothing when nothing waits.
func (s *Service) Flush(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	now := s.opts.Now()
	pace := s.opts.Pace

	kept := s.queue[:0]
	for _, q := range s.queue {
		if !q.until.IsZero() && !now.Before(q.until) {
			s.updateView(q.id, func(v *AlertView) { v.Waiting = waitingExpired })
			continue
		}
		kept = append(kept, q)
	}
	s.queue = kept
	if len(s.queue) == 0 || !s.settings.Enabled || !s.settings.Transmit {
		s.mu.Unlock()
		return
	}
	if s.opts.Sender == nil {
		for _, q := range s.queue {
			s.updateView(q.id, func(v *AlertView) { v.Waiting = waitingNoDMR })
		}
		s.mu.Unlock()
		return
	}

	recent := s.sentTimes[:0]
	for _, t := range s.sentTimes {
		if now.Sub(t) < pace.Window {
			recent = append(recent, t)
		}
	}
	s.sentTimes = recent
	if n := len(s.sentTimes); n > 0 && now.Sub(s.sentTimes[n-1]) < pace.Gap {
		s.mu.Unlock()
		return
	}
	sort.SliceStable(s.queue, func(i, j int) bool {
		ri, rj := rank(s.queue[i].event), rank(s.queue[j].event)
		if ri != rj {
			return ri < rj
		}
		return s.queue[i].issued.Before(s.queue[j].issued)
	})
	if len(s.sentTimes) >= pace.Limit {
		limit := fmt.Sprintf("waiting: %d alerts have gone out in the last %s, which is the limit; "+
			"it goes as the oldest of them ages out", pace.Limit, pace.Window)
		for _, q := range s.queue {
			s.updateView(q.id, func(v *AlertView) { v.Waiting = limit })
		}
		s.mu.Unlock()
		return
	}
	q := s.queue[0]
	m := s.message(q.text)
	slot := hbp.Timeslot(s.settings.Timeslot)
	s.mu.Unlock()

	// Outside the lock: the sender takes the listener's locks, and a status
	// read must not wait on it.
	err := s.opts.Sender.SendLocalText(slot, m)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		// Usually a call on the timeslot or another text going out; it is
		// tried again in a few seconds, and the page says why it waits.
		why := "waiting: " + strings.TrimPrefix(err.Error(), "peers: ")
		s.updateView(q.id, func(v *AlertView) { v.Waiting = why })
		return
	}
	for i, w := range s.queue {
		if w.id == q.id {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			break
		}
	}
	s.sentTimes = append(s.sentTimes, now)
	s.updateView(q.id, func(v *AlertView) { v.SentAt, v.Waiting = now, "" })
	s.log.Info("weather alert sent to this server's stations",
		slog.String("text", q.text),
		slog.Uint64("talkgroup", uint64(m.To)),
		slog.String("timeslot", slot.String()),
		slog.Uint64("from", uint64(m.From)),
		slog.String("nws_id", q.id))
}

// SendTest puts TestText on the air now, the way an alert goes. It is an
// administrator's explicit action, so it does not wait for Transmit or count
// toward the limit; it does need somewhere to send.
func (s *Service) SendTest(context.Context) error {
	s.mu.Lock()
	set := s.settings
	sender := s.opts.Sender
	m := s.message(TestText)
	s.mu.Unlock()

	switch {
	case sender == nil:
		return ErrCannotTransmit
	case set.Talkgroup == 0 || (set.Timeslot != 1 && set.Timeslot != 2) || set.SenderID == 0:
		return errors.New("weather: choose a talkgroup, timeslot and sender ID and save them first")
	}
	if err := sender.SendLocalText(hbp.Timeslot(set.Timeslot), m); err != nil {
		return fmt.Errorf("weather: sending the test: %w", err)
	}
	s.log.Info("weather test sent to this server's stations",
		slog.Uint64("talkgroup", uint64(m.To)), slog.Int("timeslot", set.Timeslot), slog.Uint64("from", uint64(m.From)))
	return nil
}

// message builds the group text for one alert. Called with the lock held.
func (s *Service) message(text string) tms.Message {
	s.reference++
	return tms.Message{
		From:  s.settings.SenderID,
		To:    s.settings.Talkgroup,
		Group: true,
		// As the console's own texts: the IP identification only has to
		// differ between messages, and the reference counts in the range
		// every captured message used.
		IPID:      uint16(rand.Uint32()),
		Reference: 0x80 | byte(s.reference&0x7f),
		Text:      text,
	}
}

// updateView changes one alert wherever the page reads it. Called with the
// lock held.
func (s *Service) updateView(id string, change func(*AlertView)) {
	if v, ok := s.decided[id]; ok {
		change(&v)
		s.decided[id] = v
	}
	for i := range s.active {
		if s.active[i].ID == id {
			change(&s.active[i])
		}
	}
	for i := range s.recent {
		if s.recent[i].ID == id {
			change(&s.recent[i])
		}
	}
}
