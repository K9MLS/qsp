package callsigns_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/callsigns"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

// memoryStore is the cache, without a database.
type memoryStore struct {
	entries []callsigns.Entry
	saved   []callsigns.Entry
	loadErr error
}

func (s *memoryStore) Load() ([]callsigns.Entry, error) { return s.entries, s.loadErr }
func (s *memoryStore) Save(e callsigns.Entry) error {
	s.saved = append(s.saved, e)
	return nil
}

func newResolver(t *testing.T, store callsigns.Store, opts ...func(*callsigns.Options)) (*callsigns.Resolver, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)}
	o := callsigns.Options{Contact: "k9mls@example.org", Now: c.now}
	for _, f := range opts {
		f(&o)
	}
	r, err := callsigns.New(o, store)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, c
}

// TestAContactAddressIsRequired. The registry asks automated clients to
// identify themselves, and QSP has no business inventing an address on an
// operator's behalf — it is the operator making the requests.
func TestAContactAddressIsRequired(t *testing.T) {
	for _, contact := range []string{"", "   "} {
		_, err := callsigns.New(callsigns.Options{Contact: contact}, nil)
		if !errors.Is(err, callsigns.ErrNoContact) {
			t.Errorf("contact %q gave %v, want ErrNoContact", contact, err)
		}
	}
}

// TestAnUnknownIDIsQueuedAndNotFetched. A network call has no business anywhere
// near a routing decision, so Lookup queues and returns.
func TestAnUnknownIDIsQueuedAndNotFetched(t *testing.T) {
	r, _ := newResolver(t, nil)

	if _, ok := r.Lookup(3155408); ok {
		t.Error("an unknown ID resolved without anybody asking the registry")
	}
	if r.Pending() != 1 {
		t.Errorf("%d pending, want 1", r.Pending())
	}
}

// TestOneTransmissionQueuesOneRequest. A radio ID appears in every frame of a
// transmission, and thirty frames must not become thirty requests.
func TestOneTransmissionQueuesOneRequest(t *testing.T) {
	r, _ := newResolver(t, nil)

	for i := 0; i < 30; i++ {
		r.Lookup(3155408)
	}
	if r.Pending() != 1 {
		t.Errorf("%d pending after one transmission, want 1", r.Pending())
	}
}

func TestAResolvedNameIsReturnedFromCache(t *testing.T) {
	r, _ := newResolver(t, nil)

	id, ok := r.Next()
	if ok {
		t.Fatalf("Next returned %d with nothing queued", id)
	}
	r.Lookup(3155408)

	id, ok = r.Next()
	if !ok || id != 3155408 {
		t.Fatalf("Next gave %d, %v", id, ok)
	}

	r.Record(id, callsigns.Entry{Callsign: "KB9TYC", Name: "Paul", Known: true}, nil)

	e, found := r.Lookup(3155408)
	if !found {
		t.Fatal("a resolved ID was not returned from the cache")
	}
	if e.Display() != "KB9TYC Paul" {
		t.Errorf("display is %q", e.Display())
	}
	if r.Pending() != 0 {
		t.Error("a resolved ID was queued again")
	}
}

// TestAnAbsenceIsRemembered. Otherwise every unregistered radio becomes a
// request on every transmission, which is the excessive use the registry asks
// people to avoid.
func TestAnAbsenceIsRemembered(t *testing.T) {
	r, c := newResolver(t, nil, func(o *callsigns.Options) {
		o.NegativeTTL = time.Hour
	})

	r.Lookup(9999999)
	id, _ := r.Next()
	r.Record(id, callsigns.Entry{Known: false}, nil)

	for i := 0; i < 10; i++ {
		r.Lookup(9999999)
	}
	if r.Pending() != 0 {
		t.Errorf("%d pending; an absence was not remembered", r.Pending())
	}

	// And it is re-asked once it ages out, so somebody who registers today is
	// named tomorrow.
	c.advance(2 * time.Hour)
	r.Lookup(9999999)
	if r.Pending() != 1 {
		t.Error("an aged-out absence was not re-queued")
	}
}

// TestAFailedRequestIsNotAnAbsence. A registry that was briefly unreachable has
// said nothing about the ID, and remembering that as an absence would hide a
// real name for a day.
func TestAFailedRequestIsNotAnAbsence(t *testing.T) {
	r, _ := newResolver(t, nil)

	r.Lookup(3155408)
	id, _ := r.Next()
	if _, saved := r.Record(id, callsigns.Entry{}, errors.New("connection refused")); saved {
		t.Error("a failed request was recorded")
	}

	r.Lookup(3155408)
	if r.Pending() != 1 {
		t.Error("a failed request was remembered as an absence")
	}
}

// TestRequestsAreSpaced. The registry asks callers to be gentle, and a burst of
// unknown IDs should not all arrive at once.
func TestRequestsAreSpaced(t *testing.T) {
	r, c := newResolver(t, nil, func(o *callsigns.Options) {
		o.Interval = 5 * time.Second
	})

	for _, id := range []uint32{1000001, 1000002, 1000003} {
		r.Lookup(id)
	}

	if _, ok := r.Next(); !ok {
		t.Fatal("the first request was refused")
	}
	if _, ok := r.Next(); ok {
		t.Error("a second request was allowed immediately")
	}

	c.advance(6 * time.Second)
	if _, ok := r.Next(); !ok {
		t.Error("a request was refused after the interval elapsed")
	}
}

// TestTheQueueIsBounded. A queue that grows without limit turns a registry
// outage into memory that is never returned.
func TestTheQueueIsBounded(t *testing.T) {
	r, _ := newResolver(t, nil, func(o *callsigns.Options) {
		o.QueueDepth = 4
	})

	for i := uint32(0); i < 50; i++ {
		r.Lookup(1000000 + i)
	}
	if r.Pending() != 4 {
		t.Errorf("%d pending, want at most 4", r.Pending())
	}

	// The newest survive: a busy network should resolve what it is hearing now
	// rather than what it heard when the queue filled.
	id, _ := r.Next()
	if id < 1000040 {
		t.Errorf("the queue kept %d, which is not among the most recent", id)
	}
}

// TestTheCacheSurvivesARestart. Re-asking for everything QSP already knew is
// precisely what the registry does not want.
func TestTheCacheSurvivesARestart(t *testing.T) {
	store := &memoryStore{entries: []callsigns.Entry{
		{ID: 3155408, Callsign: "KB9TYC", Name: "Paul", Known: true,
			FetchedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
	}}
	r, _ := newResolver(t, store)

	e, found := r.Lookup(3155408)
	if !found {
		t.Fatal("a cached entry was not loaded")
	}
	if e.Callsign != "KB9TYC" {
		t.Errorf("loaded %+v", e)
	}
	if r.Pending() != 0 {
		t.Error("a cached ID was queued anyway")
	}
}

func TestALoadFailureIsReported(t *testing.T) {
	store := &memoryStore{loadErr: errors.New("database is locked")}
	_, err := callsigns.New(callsigns.Options{Contact: "x@example.org"}, store)
	if err == nil {
		t.Error("a cache that could not be read was ignored")
	}
}

// TestDisplayPrefersTheCallsign. A callsign is what an operator recognises, and
// a surname and town beside every transmission is a list nobody can scan.
func TestDisplayPrefersTheCallsign(t *testing.T) {
	for _, tc := range []struct {
		entry callsigns.Entry
		want  string
	}{
		{callsigns.Entry{Callsign: "K9MLS", Name: "Michael L"}, "K9MLS Michael"},
		{callsigns.Entry{Callsign: "K9MLS"}, "K9MLS"},
		{callsigns.Entry{Name: "Michael L"}, "Michael"},
		{callsigns.Entry{}, ""},
	} {
		if got := tc.entry.Display(); got != tc.want {
			t.Errorf("%+v displayed as %q, want %q", tc.entry, got, tc.want)
		}
	}
}

// TestTheUserAgentIdentifiesQSPAndTheOperator. Their policy asks for a clear
// identification and a contact address; the version matters too, so that if one
// release misbehaves they can tell which.
func TestTheUserAgentIdentifiesQSPAndTheOperator(t *testing.T) {
	ua := callsigns.UserAgent("v0.1.9", "k9mls@example.org")

	if !strings.Contains(ua, "QSP/v0.1.9") {
		t.Errorf("the user agent does not name the version: %q", ua)
	}
	if !strings.Contains(ua, "k9mls@example.org") {
		t.Errorf("the user agent does not carry the contact: %q", ua)
	}
}

// TestIDZeroIsNeverLookedUp guards the obvious waste: a frame with no source is
// not a radio anybody registered.
func TestIDZeroIsNeverLookedUp(t *testing.T) {
	r, _ := newResolver(t, nil)
	if _, ok := r.Lookup(0); ok {
		t.Error("ID 0 resolved")
	}
	if r.Pending() != 0 {
		t.Error("ID 0 was queued")
	}
}
