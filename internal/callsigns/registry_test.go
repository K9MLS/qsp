package callsigns_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/callsigns"
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
