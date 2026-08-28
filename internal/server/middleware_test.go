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
	}), withRequestID(), withLogging(log))

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
	}), withRequestID(), withLogging(log))

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
	}), withRequestID(), withLogging(log))

	req := httptest.NewRequest(http.MethodGet, "/join.html", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), "http request") {
		t.Error("an ordinary request was not logged")
	}
}
