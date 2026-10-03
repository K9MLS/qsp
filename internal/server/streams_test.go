package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
)

// TestEventStreamsAreCapped. The stream needs no sign-in and each one holds a
// goroutine, a subscription and a connection for as long as its client likes,
// so the number open was whatever a stranger chose. The cap has three parts and
// each row is one of them: a ceiling on everything, a lower ceiling on
// anonymous streams so administrators keep a way in, and a per-address ceiling
// so one machine cannot take every anonymous place.
//
// To see it fail: in streamSlots.take, delete the `if !signedIn { ... return
// false }` refusal (rows two to four), or the `s.total >= maxEventStreams`
// check (row one).
func TestEventStreamsAreCapped(t *testing.T) {
	type holder struct {
		source   string
		signedIn bool
		count    int
	}
	// spread holds n anonymous streams without any one address reaching its
	// own ceiling.
	spread := func(n int) []holder {
		var out []holder
		for i := 0; n > 0; i++ {
			take := min(n, maxAnonymousEventStreamsPerSource-1)
			out = append(out, holder{source: "192.0.2." + string(rune('0'+i)), count: take})
			n -= take
		}
		return out
	}

	tests := []struct {
		name     string
		held     []holder
		source   string
		signedIn bool
		want     bool
	}{
		{
			name: "nobody gets in past the overall ceiling, signed in or not",
			held: append(spread(maxAnonymousEventStreams),
				holder{signedIn: true, count: maxEventStreams - maxAnonymousEventStreams}),
			source: "198.51.100.7", signedIn: true, want: false,
		},
		{
			name:   "an anonymous client is refused once the anonymous places are gone",
			held:   spread(maxAnonymousEventStreams),
			source: "198.51.100.7", want: false,
		},
		{
			name:   "an administrator still gets in when strangers hold every anonymous place",
			held:   spread(maxAnonymousEventStreams),
			source: "198.51.100.7", signedIn: true, want: true,
		},
		{
			name:   "one address cannot hold more than its share anonymously",
			held:   []holder{{source: "203.0.113.5", count: maxAnonymousEventStreamsPerSource}},
			source: "203.0.113.5", want: false,
		},
		{
			name:   "which leaves room for everybody else",
			held:   []holder{{source: "203.0.113.5", count: maxAnonymousEventStreamsPerSource}},
			source: "198.51.100.7", want: true,
		},
		{
			name:   "and does not limit that address once it has signed in",
			held:   []holder{{source: "203.0.113.5", count: maxAnonymousEventStreamsPerSource}},
			source: "203.0.113.5", signedIn: true, want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var slots streamSlots
			for _, h := range tc.held {
				for range h.count {
					if !slots.take(h.source, h.signedIn) {
						t.Fatalf("setting up: %s could not hold %d streams", h.source, h.count)
					}
				}
			}
			if got := slots.take(tc.source, tc.signedIn); got != tc.want {
				t.Fatalf("take = %v, want %v", got, tc.want)
			}

			// Everything given back leaves nothing counted, or the cap would
			// tighten by itself over a day of tabs opening and closing.
			if tc.want {
				slots.release(tc.source, tc.signedIn)
			}
			for _, h := range tc.held {
				for range h.count {
					slots.release(h.source, h.signedIn)
				}
			}
			if slots.total != 0 || slots.anonymous != 0 || len(slots.bySource) != 0 {
				t.Errorf("after every release: total %d, anonymous %d, addresses %d; want none",
					slots.total, slots.anonymous, len(slots.bySource))
			}
		})
	}
}

// TestAFullEventStreamIsRefusedWithAStatus. A refusal has to be something a
// client can act on: a status, and when to come back.
//
// To see it fail: delete the `if !s.streams.take(...)` block from handleEvents.
func TestAFullEventStreamIsRefusedWithAStatus(t *testing.T) {
	srv, _ := newTestServer(t, health.Report{Status: health.StatusHealthy})
	for range maxAnonymousEventStreamsPerSource {
		srv.streams.take("192.0.2.10", false)
	}

	// Bounded, so that a stream wrongly let in ends the test rather than
	// hanging it.
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	req.RemoteAddr = "192.0.2.10:54321"
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("returned %d, want 503", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("the refusal does not say when to try again")
	}
	if !strings.Contains(rec.Body.String(), "too many") {
		t.Errorf("the refusal does not say why: %s", rec.Body)
	}
	if n := srv.streams.bySource["192.0.2.10"]; n != maxAnonymousEventStreamsPerSource {
		t.Errorf("a refused stream changed the count to %d", n)
	}
}

// TestTheWriteTimeoutAppliesToResponsesAndNotToTheLifeOfAStream.
// server.write_timeout was read from the configuration, validated, and never
// given to the HTTP server, so a client that stopped reading held its
// connection for ever. Applying it is half the fix; the other half is that an
// event stream, which is one response lasting hours, must outlive it — bounded
// per write instead.
//
// To see it fail: delete `WriteTimeout: opts.WriteTimeout,` in New (the first
// assertion), or the SetWriteDeadline call in handleEvents' keepWriting (the
// stream is cut off and the second frame never arrives).
func TestTheWriteTimeoutAppliesToResponsesAndNotToTheLifeOfAStream(t *testing.T) {
	const writeTimeout = 300 * time.Millisecond

	bus := events.NewBus(nil, events.Options{HistorySize: 8, SubscriberBuffer: 4})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress:   "127.0.0.1:0",
		ShutdownTimeout: time.Second,
		WriteTimeout:    writeTimeout,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv.http.WriteTimeout != writeTimeout {
		t.Fatalf("the HTTP server's write timeout is %s, want the configured %s",
			srv.http.WriteTimeout, writeTimeout)
	}
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(t.Context()) })

	resp, err := http.Get("http://" + srv.Address() + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)
	if frames := readSSEFrames(t, br, 1); len(frames) == 0 {
		t.Fatal("no opening frame from the event stream")
	}

	// Well past the timeout, counted from the request.
	time.Sleep(2 * writeTimeout)
	bus.Publish(events.TypeCallStarted, nil)
	frames := readSSEFrames(t, br, 1)
	if len(frames) == 0 || !strings.Contains(frames[0], string(events.TypeCallStarted)) {
		t.Fatalf("the stream did not outlive the write timeout; read %q", frames)
	}
}
