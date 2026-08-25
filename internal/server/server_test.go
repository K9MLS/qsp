package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/events"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
)

type stubRegistry struct{ report health.Report }

func (s stubRegistry) Run(context.Context) health.Report { return s.report }

func newTestServer(t *testing.T, report health.Report) (*Server, *events.Bus) {
	t.Helper()
	bus := events.NewBus(nil, events.Options{HistorySize: 8, SubscriberBuffer: 4})
	t.Cleanup(bus.Close)

	srv, err := New(nil, stubRegistry{report: report}, bus, Options{
		ListenAddress:   "127.0.0.1:0",
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, bus
}

func TestNewRequiresDependencies(t *testing.T) {
	bus := events.NewBus(nil, events.Options{})
	defer bus.Close()

	if _, err := New(nil, nil, bus, Options{ListenAddress: "127.0.0.1:0"}); err == nil {
		t.Error("New accepted a nil health registry")
	}
	if _, err := New(nil, stubRegistry{}, nil, Options{ListenAddress: "127.0.0.1:0"}); err == nil {
		t.Error("New accepted a nil event bus")
	}
	if _, err := New(nil, stubRegistry{}, bus, Options{}); err == nil {
		t.Error("New accepted an empty listen address")
	}
}

func TestHealthzReportsStatus(t *testing.T) {
	srv, _ := newTestServer(t, health.Report{
		Status:  health.StatusHealthy,
		Results: []health.Result{{Name: "database", Status: health.StatusUnavailable, Summary: "no driver"}},
	})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	var body health.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if body.Status != health.StatusHealthy {
		t.Errorf("status = %q, want healthy", body.Status)
	}
	if len(body.Results) != 1 || body.Results[0].Status != health.StatusUnavailable {
		t.Errorf("the unavailable subsystem was not reported: %+v", body.Results)
	}
}

func TestHealthzReturns503WhenFailing(t *testing.T) {
	srv, _ := newTestServer(t, health.Report{Status: health.StatusFailing})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestReadyz(t *testing.T) {
	cases := []struct {
		status    health.Status
		wantCode  int
		wantReady bool
	}{
		{health.StatusHealthy, http.StatusOK, true},
		{health.StatusDegraded, http.StatusOK, true},
		{health.StatusUnavailable, http.StatusOK, true},
		{health.StatusFailing, http.StatusServiceUnavailable, false},
	}
	for _, c := range cases {
		t.Run(string(c.status), func(t *testing.T) {
			srv, _ := newTestServer(t, health.Report{Status: c.status})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

			if rec.Code != c.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, c.wantCode)
			}
			var body struct {
				Ready bool `json:"ready"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("response is not valid JSON: %v", err)
			}
			if body.Ready != c.wantReady {
				t.Errorf("ready = %v, want %v", body.Ready, c.wantReady)
			}
		})
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	srv, _ := newTestServer(t, health.Report{Status: health.StatusHealthy})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	want := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("CSP does not restrict default-src: %q", csp)
	}
	if strings.Contains(csp, "unsafe-inline") {
		t.Errorf("CSP permits inline script or style: %q", csp)
	}
}

func TestRequestIDIsAssignedAndEchoed(t *testing.T) {
	srv, _ := newTestServer(t, health.Report{Status: health.StatusHealthy})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Header().Get("X-Request-Id") == "" {
		t.Error("no correlation ID was assigned")
	}
}

func TestRequestIDsAreDistinct(t *testing.T) {
	srv, _ := newTestServer(t, health.Report{Status: health.StatusHealthy})

	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		id := rec.Header().Get("X-Request-Id")
		if seen[id] {
			t.Fatalf("correlation ID %q was reused", id)
		}
		seen[id] = true
	}
}

func TestPanicInHandlerBecomes500(t *testing.T) {
	// A broken console endpoint must not take down a bridge carrying traffic.
	h := chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("deliberate test panic")
	}), withRequestID(), withRecovery(logging.Discard()))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/boom", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestMissingConsoleAssetsReportedHonestly(t *testing.T) {
	// Constitution §3: an absent capability says so.
	srv, _ := newTestServer(t, health.Report{Status: health.StatusHealthy})

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "no console assets") {
		t.Errorf("response does not explain the absence: %q", rec.Body.String())
	}
}

func TestClientIPIgnoresForwardingHeadersUnlessBehindProxy(t *testing.T) {
	// Trusting these unconditionally would let any client forge its address in
	// the audit trail.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.10:54321"
	r.Header.Set("X-Forwarded-For", "203.0.113.5")

	if got := clientIP(r, false); got != "192.0.2.10" {
		t.Errorf("clientIP without proxy = %q, want the socket address", got)
	}
	if got := clientIP(r, true); got != "203.0.113.5" {
		t.Errorf("clientIP behind proxy = %q, want the forwarded address", got)
	}
}

func TestClientIPTakesFirstOfForwardedChain(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.10:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.5, 198.51.100.7")
	if got := clientIP(r, true); got != "203.0.113.5" {
		t.Errorf("clientIP = %q, want 203.0.113.5", got)
	}
}

func TestStartAndShutdown(t *testing.T) {
	srv, _ := newTestServer(t, health.Report{Status: health.StatusHealthy})

	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	addr := srv.Address()
	if addr == "127.0.0.1:0" || addr == "" {
		t.Fatalf("Address() = %q; the bound port was not resolved", addr)
	}

	resp, err := http.Get("http://" + addr + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("closing body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	if err := srv.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown: %v", err)
	}
}

func TestStartReportsPortConflictClearly(t *testing.T) {
	first, _ := newTestServer(t, health.Report{Status: health.StatusHealthy})
	if err := first.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = first.Shutdown(context.Background()) }()

	bus := events.NewBus(nil, events.Options{})
	defer bus.Close()
	second, err := New(nil, stubRegistry{}, bus, Options{ListenAddress: first.Address()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = second.Start()
	if err == nil {
		t.Fatal("expected a port conflict to be reported")
	}
	if !strings.Contains(err.Error(), "port is free") {
		t.Errorf("error should tell the operator what to check, got: %v", err)
	}
}

// readSSEFrames reads frames until the reader is exhausted or n frames are read.
func readSSEFrames(t *testing.T, r *bufio.Reader, n int) []string {
	t.Helper()
	var frames []string
	var current strings.Builder
	for len(frames) < n {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		if line == "\n" {
			if current.Len() > 0 {
				frames = append(frames, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteString(line)
	}
	return frames
}

func TestEventStreamTellsNewClientToSnapshot(t *testing.T) {
	// A client with no prior position must be told to take a snapshot rather
	// than assume the stream is the whole story.
	srv, bus := newTestServer(t, health.Report{Status: health.StatusHealthy})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	resp, err := http.Get("http://" + srv.Address() + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	br := bufio.NewReader(resp.Body)
	frames := readSSEFrames(t, br, 1)
	if len(frames) == 0 {
		t.Fatal("no frame was received")
	}
	if !strings.Contains(frames[0], "event: resync") {
		t.Errorf("first frame is not a resync instruction: %q", frames[0])
	}
	if !strings.Contains(frames[0], "snapshot") {
		t.Errorf("resync frame does not tell the client what to do: %q", frames[0])
	}

	bus.Publish(events.TypeCallStarted, map[string]any{"talkgroup": 3100})
	frames = readSSEFrames(t, br, 1)
	if len(frames) == 0 {
		t.Fatal("the published event was not streamed")
	}
	if !strings.Contains(frames[0], "event: call.started") {
		t.Errorf("frame = %q, want a call.started event", frames[0])
	}
	if !strings.Contains(frames[0], "id: 1") {
		t.Errorf("frame carries no sequence id: %q", frames[0])
	}
}

func TestEventStreamReplaysFromLastEventID(t *testing.T) {
	srv, bus := newTestServer(t, health.Report{Status: health.StatusHealthy})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	for i := 0; i < 4; i++ {
		bus.Publish(events.TypePeerConnected, i)
	}

	req, err := http.NewRequest(http.MethodGet, "http://"+srv.Address()+"/api/events?last_event_id=2", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	frames := readSSEFrames(t, bufio.NewReader(resp.Body), 2)
	if len(frames) < 2 {
		t.Fatalf("got %d frames, want 2 replayed events", len(frames))
	}
	if !strings.Contains(frames[0], "id: 3") {
		t.Errorf("first replayed frame = %q, want sequence 3", frames[0])
	}
	if !strings.Contains(frames[1], "id: 4") {
		t.Errorf("second replayed frame = %q, want sequence 4", frames[1])
	}
}

func TestEventStreamSendsResyncWhenHistoryGapped(t *testing.T) {
	// The central honesty property of the stream: a gap is never silent.
	srv, bus := newTestServer(t, health.Report{Status: health.StatusHealthy})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = srv.Shutdown(context.Background()) }()

	// The test bus retains 8 events; publish well past that.
	for i := 0; i < 40; i++ {
		bus.Publish(events.TypePeerConnected, i)
	}

	resp, err := http.Get("http://" + srv.Address() + "/api/events?last_event_id=1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	frames := readSSEFrames(t, bufio.NewReader(resp.Body), 1)
	if len(frames) == 0 {
		t.Fatal("no frame received")
	}
	if !strings.Contains(frames[0], "event: resync") {
		t.Errorf("expected a resync instruction after a history gap, got: %q", frames[0])
	}
}

func TestEventPayloadContainsNoRawNewlines(t *testing.T) {
	// A literal newline in the data field would corrupt the SSE frame.
	ev := events.Event{Seq: 1, Type: events.TypeCallStarted, Data: map[string]string{
		"note": "line one\nline two",
	}}
	payload, err := marshalEvent(ev)
	if err != nil {
		t.Fatalf("marshalEvent: %v", err)
	}
	if strings.Contains(string(payload), "\n") {
		t.Errorf("payload contains a raw newline: %q", payload)
	}
}

// stubPeers is a fixed peer list for testing the endpoint without a socket.
type stubPeers struct {
	views   []PeerView
	active  []CallView
	recent  []CallView
	traffic Traffic
}

func (s stubPeers) PeerViews(time.Time) []PeerView { return s.views }

func (s stubPeers) CallViews(time.Time) ([]CallView, []CallView) { return s.active, s.recent }

func (s stubPeers) Traffic() Traffic { return s.traffic }

func newPeerServer(t *testing.T, src PeerSource, reason string) *Server {
	t.Helper()
	bus := events.NewBus(nil, events.Options{})
	t.Cleanup(bus.Close)
	srv, err := New(nil, stubRegistry{report: health.Report{Status: health.StatusHealthy}}, bus, Options{
		ListenAddress:       "127.0.0.1:0",
		Peers:               src,
		PeersDisabledReason: reason,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func TestPeersEndpointDistinguishesDisabledFromEmpty(t *testing.T) {
	// An empty list and a disabled listener look identical to a naive client
	// but mean completely different things to an operator.
	t.Run("disabled", func(t *testing.T) {
		srv := newPeerServer(t, nil, "the DMR listener is disabled; set dmr.enabled")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))

		var body struct {
			Enabled bool       `json:"enabled"`
			Reason  string     `json:"reason"`
			Peers   []PeerView `json:"peers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("response is not valid JSON: %v", err)
		}
		if body.Enabled {
			t.Error("a disabled listener reported enabled")
		}
		if !strings.Contains(body.Reason, "dmr.enabled") {
			t.Errorf("reason should name the setting, got %q", body.Reason)
		}
		if body.Peers == nil {
			t.Error("peers is null; it must always be an array")
		}
	})

	t.Run("enabled but empty", func(t *testing.T) {
		srv := newPeerServer(t, stubPeers{}, "")
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))

		var body struct {
			Enabled bool       `json:"enabled"`
			Reason  string     `json:"reason"`
			Peers   []PeerView `json:"peers"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if !body.Enabled {
			t.Error("an enabled listener reported disabled")
		}
		if body.Reason != "" {
			t.Errorf("an enabled listener gave a reason: %q", body.Reason)
		}
		if len(body.Peers) != 0 {
			t.Errorf("got %d peers, want 0", len(body.Peers))
		}
	})
}

func TestPeersEndpointReturnsPeers(t *testing.T) {
	srv := newPeerServer(t, stubPeers{views: []PeerView{{
		ID: 3132910, Callsign: "K9MLS", Address: "192.0.2.10:54663",
		State: "configured", Ready: true, ConnectedFor: "5m0s", IdleFor: "3s",
		ColorCode: "11",
	}}}, "")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/peers", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Peers []PeerView `json:"peers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(body.Peers) != 1 {
		t.Fatalf("got %d peers, want 1", len(body.Peers))
	}
	p := body.Peers[0]
	if p.ID != 3132910 || p.Callsign != "K9MLS" || !p.Ready {
		t.Errorf("peer decoded incorrectly: %+v", p)
	}
}

// TestPeerViewCannotCarryASalt is a structural guard.
//
// The registry holds each challenged peer's outstanding salt. PeerView is a
// hand-written projection precisely so that a credential cannot reach the
// network by someone adding a JSON tag to the domain type.
func TestPeerViewCannotCarryASalt(t *testing.T) {
	encoded, err := json.Marshal(PeerView{ID: 1, Callsign: "K9MLS"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, forbidden := range []string{"salt", "digest", "password", "secret"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Errorf("PeerView exposes a %q field: %s", forbidden, encoded)
		}
	}
}

func TestPeersEndpointIsReadOnly(t *testing.T) {
	// No state-changing endpoint exists until authorisation is designed.
	srv := newPeerServer(t, stubPeers{}, "")
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(method, "/api/peers", nil))
		if rec.Code == http.StatusOK {
			t.Errorf("%s /api/peers returned 200; the endpoint must be read-only", method)
		}
	}
}

// TestShutdownIsNotBlockedByAnOpenEventStream.
//
// http.Server.Shutdown waits for active connections but does not cancel their
// request contexts. The SSE handler blocks on r.Context().Done(), so without
// an explicit cancellation a single open console tab holds shutdown until the
// timeout expires.
//
// This was found by an operator on the first real run, not by any test here:
// every existing shutdown test closed a server with no client attached.
func TestShutdownIsNotBlockedByAnOpenEventStream(t *testing.T) {
	srv, bus := newTestServer(t, health.Report{Status: health.StatusHealthy})
	if err := srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Open a stream and read the first frame, so the handler is definitely
	// inside its select loop.
	resp, err := http.Get("http://" + srv.Address() + "/api/events")
	if err != nil {
		t.Fatalf("GET /api/events: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	br := bufio.NewReader(resp.Body)
	if frames := readSSEFrames(t, br, 1); len(frames) == 0 {
		t.Fatal("no opening frame from the event stream")
	}
	bus.Publish(events.TypeCallStarted, nil)
	readSSEFrames(t, br, 1)

	start := time.Now()
	err = srv.Shutdown(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Errorf("Shutdown returned %v", err)
	}
	// The configured shutdown timeout in this harness is one second; anything
	// approaching it means the stream blocked rather than being cancelled.
	if elapsed > 900*time.Millisecond {
		t.Errorf("shutdown took %s with an open event stream; it should be near-instant", elapsed)
	}
}
