package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSuccessfulPollsAreNotLoggedAtInfo.
//
// The join page polls every three seconds, per open browser. One member
// watching overnight is roughly 28,000 lines; a club's worth during a net would
// rotate the journal past the evidence an operator needs — which during a
// fourteen-day soak is the entire record of whether it passed.
func TestSuccessfulPollsAreNotLoggedAtInfo(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), withRequestID(), withLogging(log, false))

	for _, path := range []string{"/api/join", "/api/peers", "/healthz", "/readyz"} {
		buf.Reset()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if strings.Contains(buf.String(), "http request") {
			t.Errorf("a successful poll of %s was logged at info:\n%s", path, buf.String())
		}
	}
}

// TestAFailingPollIsStillLogged. Quietening the successful case must not
// quieten the case worth seeing.
func TestAFailingPollIsStillLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}), withRequestID(), withLogging(log, false))

	req := httptest.NewRequest(http.MethodGet, "/api/join", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), "http request") {
		t.Error("a failing poll was not logged")
	}
}

// TestOrdinaryRequestsAreStillLogged: only the polled paths are quietened.
func TestOrdinaryRequestsAreStillLogged(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	handler := chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), withRequestID(), withLogging(log, false))

	req := httptest.NewRequest(http.MethodGet, "/join.html", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), "http request") {
		t.Error("an ordinary request was not logged")
	}
}

// TestAScannerIsNotAWarning. On the first day of 0.1.338, 148 of
// production's 157 warnings were scanners asking the console for /.env and
// the like, every one answered 404. Each line still says where it came from,
// so the log can answer "who is asking".
//
// To see it fail: remove the 404 case from withLogging, or the "from" field.
func TestAScannerIsNotAWarning(t *testing.T) {
	for _, tc := range []struct {
		status int
		level  string
	}{
		{http.StatusNotFound, "INFO"},
		{http.StatusMethodNotAllowed, "INFO"},
		{http.StatusForbidden, "WARN"},
		{http.StatusBadRequest, "WARN"},
		{http.StatusInternalServerError, "ERROR"},
	} {
		var buf bytes.Buffer
		log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
		handler := chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
		}), withRequestID(), withLogging(log, false))

		req := httptest.NewRequest(http.MethodGet, "/.env", nil)
		req.RemoteAddr = "203.0.113.9:51515"
		handler.ServeHTTP(httptest.NewRecorder(), req)

		line := buf.String()
		if !strings.Contains(line, "level="+tc.level) {
			t.Errorf("a %d was logged as %q, want %s", tc.status, line, tc.level)
		}
		if !strings.Contains(line, "from=203.0.113.9") {
			t.Errorf("a %d does not say where it came from: %q", tc.status, line)
		}
	}
}
