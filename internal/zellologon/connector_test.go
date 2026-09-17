package zellologon

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/health"
)

// TestTheZelloLineSaysOnTheAirOrWhatToDo.
//
// To see rows fail, break checkConnector deliberately:
//   - return Healthy for every state: "Zello refused" reads healthy
//   - drop the unreachable branch's fix: "not running" tells nobody what to do
//   - accept any JSON: "something else on the port" reads as a connector
func TestTheZelloLineSaysOnTheAirOrWhatToDo(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus health.Status
		wantSays   string
	}{
		{"connected", `{"state":"connected","since":"2026-09-16T19:16:32Z","channel":"QSP Server DMR","connections":5}`,
			health.StatusHealthy, `connected to Zello channel "QSP Server DMR" since 2026-09-16 19:16 UTC`},
		{"refused by Zello", `{"state":"zello_refused","detail":"not a member of the channel"}`,
			health.StatusDegraded, "Zello refused the logon: not a member of the channel"},
		{"no credentials", `{"state":"credentials_missing"}`, health.StatusDegraded, "no Zello credentials"},
		{"QSP unreachable", `{"state":"qsp_unreachable"}`, health.StatusDegraded, "cannot reach QSP's logon socket"},
		{"Zello unreachable", `{"state":"zello_unreachable"}`, health.StatusDegraded, "cannot reach Zello's servers"},
		{"something else on the port", `{"status":"ok"}`, health.StatusDegraded, "not as qsp-zello"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					http.NotFound(w, r)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			res := ConnectorChecker(strings.TrimPrefix(srv.URL, "http://")).Check(context.Background())
			if res.Status != tc.wantStatus {
				t.Errorf("status %q, want %q: %s", res.Status, tc.wantStatus, res.Summary)
			}
			if !strings.Contains(res.Summary, tc.wantSays) {
				t.Errorf("summary %q does not say %q", res.Summary, tc.wantSays)
			}
			if res.Status != health.StatusHealthy && res.Fix == "" {
				t.Error("a line that is not healthy names no action")
			}
		})
	}
}

// TestAConnectorThatIsNotRunningSaysHowToStartIt.
func TestAConnectorThatIsNotRunningSaysHowToStartIt(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close() // nothing listens there now
	res := ConnectorChecker(addr).Check(context.Background())
	if res.Status != health.StatusDegraded || !strings.Contains(res.Summary, "not answering") ||
		!strings.Contains(res.Fix, "systemctl start qsp-zello") {
		t.Errorf("got %q / %q / %q", res.Status, res.Summary, res.Fix)
	}
}
