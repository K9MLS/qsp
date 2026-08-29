package callsigns_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/callsigns"
	"github.com/k9mls/qsp/internal/logging"
)

// The registry client, against a fake registry. The SQL is checked against the
// migration separately, as internal/auth's is.

func TestTheRequestIdentifiesQSPAndTheOperator(t *testing.T) {
	var agent, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		agent = r.Header.Get("User-Agent")
		query = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	}))
	defer srv.Close()

	f := fetcherAgainst(t, srv.URL)
	if _, err := f.Fetch(3132910); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	if !strings.Contains(agent, "QSP/") {
		t.Errorf("the request does not identify QSP: %q", agent)
	}
	if !strings.Contains(agent, "k9mls@example.org") {
		t.Errorf("the request does not carry the contact: %q", agent)
	}
	if !strings.Contains(query, "id=3132910") {
		t.Errorf("the request asks for %q", query)
	}
}

func TestARecordIsParsed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":1,"results":[
			{"id":3155408,"callsign":"KB9TYC","fname":"Paul","surname":"Smith",
			 "country":"United States"}]}`))
	}))
	defer srv.Close()

	e, err := fetcherAgainst(t, srv.URL).Fetch(3155408)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !e.Known {
		t.Fatal("a record was returned as unknown")
	}
	if e.Callsign != "KB9TYC" || e.Name != "Paul" {
		t.Errorf("parsed %+v", e)
	}
	// The given name only. A surname and town beside every transmission is a
	// list nobody can scan.
	if e.Display() != "KB9TYC Paul" {
		t.Errorf("display is %q", e.Display())
	}
}

// TestAnEmptyResultIsAnAbsence, which is worth remembering rather than retrying.
func TestAnEmptyResultIsAnAbsence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":0,"results":[]}`))
	}))
	defer srv.Close()

	e, err := fetcherAgainst(t, srv.URL).Fetch(9999999)
	if err != nil {
		t.Fatalf("Fetch returned an error for an unknown ID: %v", err)
	}
	if e.Known {
		t.Error("an empty result was treated as a record")
	}
}

// TestRateLimitingIsAnErrorNotAnAbsence.
//
// Their policy says they may rate-limit at any time, and recording that as
// "this ID does not exist" would cache their refusal as a fact about the
// operator's own members.
func TestRateLimitingIsAnErrorNotAnAbsence(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := fetcherAgainst(t, srv.URL).Fetch(3132910)
	if err == nil {
		t.Fatal("a rate-limit answer was accepted as an absence")
	}
	if !strings.Contains(err.Error(), "rate-limiting") {
		t.Errorf("the error does not say what happened: %v", err)
	}
	if !callsigns.IsTemporary(err) {
		t.Error("a rate-limit answer was not marked as worth retrying")
	}
}

func TestAServerErrorIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if _, err := fetcherAgainst(t, srv.URL).Fetch(3132910); err == nil {
		t.Error("a server error was accepted as an answer")
	}
}

// TestAnOversizedAnswerIsRefused. A response far larger than one record is not
// one record, and reading it costs memory for nothing.
func TestAnOversizedAnswerIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":1,"results":[{"id":1,"callsign":"` +
			strings.Repeat("A", 2<<20) + `"}]}`))
	}))
	defer srv.Close()

	if _, err := fetcherAgainst(t, srv.URL).Fetch(3132910); err == nil {
		t.Error("an oversized answer was parsed")
	}
}

func TestAFetcherNeedsAContact(t *testing.T) {
	if _, err := callsigns.NewHTTPFetcher("v1", ""); err == nil {
		t.Error("a fetcher was built with no contact address")
	}
}

// fetcherAgainst builds a fetcher pointed at a test server.
//
// The endpoint is a constant in the package, so the test overrides the client's
// destination rather than the package's idea of the registry — which is the
// thing being kept out of configuration.
func fetcherAgainst(t *testing.T, base string) callsigns.Fetcher {
	t.Helper()
	f, err := callsigns.NewHTTPFetcherAt(base+"/", "v0.1.9", "k9mls@example.org")
	if err != nil {
		t.Fatalf("NewHTTPFetcherAt: %v", err)
	}
	return f
}

// stubFetcher answers without a network.
type stubFetcher struct {
	mu      sync.Mutex
	entries map[uint32]callsigns.Entry
	err     error
	calls   int
}

func (f *stubFetcher) Fetch(id uint32) (callsigns.Entry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return callsigns.Entry{}, f.err
	}
	e, ok := f.entries[id]
	if !ok {
		return callsigns.Entry{Known: false}, nil
	}
	return e, nil
}

func (f *stubFetcher) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestTheServiceResolvesWhatIsQueued is the whole wiring: an ID seen in a
// transmission gets a name without anything waiting on it.
func TestTheServiceResolvesWhatIsQueued(t *testing.T) {
	r, err := callsigns.New(callsigns.Options{
		Contact: "k9mls@example.org", Interval: time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f := &stubFetcher{entries: map[uint32]callsigns.Entry{
		3155408: {Callsign: "KB9TYC", Name: "Paul", Known: true},
	}}
	store := &memoryStore{}
	svc := callsigns.NewService(logging.Discard(), r, f, store)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	// Unknown at first, and queued rather than fetched inline.
	if _, ok := svc.Lookup(3155408); ok {
		t.Fatal("an unknown ID resolved before anybody asked the registry")
	}

	// **Resolution and caching are separate moments.** The entry is in memory
	// as soon as it is recorded and written to the store just after, so
	// asserting both at the same instant is a flaky test — which is what the
	// first version of this was, passing alone and failing in a full run.
	var resolved callsigns.Entry
	waitFor(t, "the ID to resolve", func() bool {
		e, ok := svc.Lookup(3155408)
		resolved = e
		return ok
	})
	if resolved.Display() != "KB9TYC Paul" {
		t.Errorf("resolved to %q", resolved.Display())
	}

	waitFor(t, "the entry to be cached", func() bool { return store.count() > 0 })
}

// TestAFetchFailureDoesNotStopTheService. A registry that is down must cost a
// name and nothing else.
func TestAFetchFailureDoesNotStopTheService(t *testing.T) {
	r, err := callsigns.New(callsigns.Options{
		Contact: "k9mls@example.org", Interval: time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f := &stubFetcher{err: errors.New("connection refused")}
	svc := callsigns.NewService(logging.Discard(), r, f, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	svc.Lookup(3155408)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && f.count() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if f.count() == 0 {
		t.Fatal("the service never asked")
	}
	// Still running, and the ID is still unresolved rather than remembered as
	// absent.
	if _, ok := svc.Lookup(3155408); ok {
		t.Error("a failed fetch produced a resolution")
	}
}

// TestLookupsAreSafeFromManyGoroutines. Every console request reads the
// resolver while one background goroutine writes it.
func TestLookupsAreSafeFromManyGoroutines(t *testing.T) {
	r, err := callsigns.New(callsigns.Options{
		Contact: "k9mls@example.org", Interval: time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	f := &stubFetcher{entries: map[uint32]callsigns.Entry{
		3155408: {Callsign: "KB9TYC", Known: true},
	}}
	svc := callsigns.NewService(logging.Discard(), r, f, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc.Run(ctx)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				svc.Lookup(uint32(3155400 + n))
				svc.Pending()
				svc.Cached()
			}
		}(i)
	}
	wg.Wait()
}

// waitFor polls until cond holds. A fixed sleep is either flaky or slow, and on
// a loaded machine usually both.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
