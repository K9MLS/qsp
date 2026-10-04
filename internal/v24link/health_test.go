package v24link

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
)

// Break it: call a tunnel whose repeater is still asking healthy, or call a
// listener nobody has dialled a fault, and a row fails.
func TestHealthSaysHowFarTheLinkHasCome(t *testing.T) {
	t.Run("off", func(t *testing.T) {
		res := HealthCheck{DisabledReason: "off, and why"}.Check(context.Background())
		if res.Status != health.StatusUnavailable || res.Summary != "off, and why" {
			t.Errorf("%s: %q", res.Status, res.Summary)
		}
	})
	t.Run("configured and never started", func(t *testing.T) {
		l, err := New(logging.Discard(), Config{ListenAddress: "127.0.0.1:0"})
		if err != nil {
			t.Fatal(err)
		}
		if res := (HealthCheck{Listener: l}).Check(context.Background()); res.Status != health.StatusDegraded {
			t.Errorf("%s: %q", res.Status, res.Summary)
		}
	})

	tests := []struct {
		name   string
		send   []byte // nil dials nothing
		up     int
		status health.Status
	}{
		{"listening, and no router yet", nil, 0, health.StatusHealthy},
		{"a router connected and the repeater still asking", linkRequest, 0, health.StatusDegraded},
		{"a link open",
			join(linkRequest, theirAcceptance, tunnel(stationIntroduction), tunnel([]byte{0xFD, 0x01})),
			1, health.StatusHealthy},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := start(t, Config{})
			if tc.send != nil {
				c := dial(t, l)
				go func() { _, _ = io.Copy(io.Discard, c) }()
				_, _ = c.Write(tc.send)
				deadline := time.Now().Add(2 * time.Second)
				for (len(l.Repeaters()) != 1 || l.LinksUp() != tc.up) && time.Now().Before(deadline) {
					time.Sleep(5 * time.Millisecond)
				}
				time.Sleep(30 * time.Millisecond)
			}
			res := HealthCheck{Listener: l}.Check(context.Background())
			if res.Status != tc.status {
				t.Errorf("%s: %q", res.Status, res.Summary)
			}
			if res.Detail["links_up"] == "" || res.Detail["routers"] == "" {
				t.Errorf("detail is %v", res.Detail)
			}
		})
	}
}
