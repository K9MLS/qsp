package main

import (
	"context"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/health"
)

// A master that repeats with no bridges is the ordinary club network, not a
// degraded one. This reported degraded until ADR-0019's correction reached it.
func TestRoutingCheckWithNoBridgesIsHealthy(t *testing.T) {
	res := routingCheck{enabled: true, bridges: 0}.Check(context.Background())
	if res.Status != health.StatusHealthy {
		t.Errorf("a repeating master with no bridges reports %q, want healthy", res.Status)
	}
	if !strings.Contains(res.Summary, "hear each other") {
		t.Errorf("the summary does not mention repeat, which is what it mostly does: %q", res.Summary)
	}
}

func TestRoutingCheckWithBridgesMentionsBoth(t *testing.T) {
	res := routingCheck{enabled: true, bridges: 2}.Check(context.Background())
	if res.Status != health.StatusHealthy {
		t.Errorf("status is %q, want healthy", res.Status)
	}
	if !strings.Contains(res.Summary, "hear each other") || !strings.Contains(res.Summary, "2 bridge") {
		t.Errorf("the summary should name repeat and the bridges: %q", res.Summary)
	}
}

func TestRoutingCheckOffSaysWhatIsLost(t *testing.T) {
	res := routingCheck{enabled: false}.Check(context.Background())
	if res.Status != health.StatusUnavailable {
		t.Errorf("status is %q, want unavailable", res.Status)
	}
	if !strings.Contains(res.Summary, "hear each other") {
		t.Errorf("the summary should say what is lost, not just that a flag is off: %q", res.Summary)
	}
}
