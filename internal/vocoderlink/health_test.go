package vocoderlink

import (
	"context"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/health"
)

// TestHealthIsGreenOnlyWhenBothDirectionsHaveCarried.
//
// To see a row fail: return Healthy whenever either count is non-zero, and
// "one direction only" goes green on half a link.
func TestHealthIsGreenOnlyWhenBothDirectionsHaveCarried(t *testing.T) {
	tests := []struct {
		name       string
		to, from   uint64
		badFEC     uint64
		wantStatus health.Status
		wantFix    string
	}{
		{"nothing yet", 0, 0, 0, health.StatusDegraded, "nothing has crossed"},
		{"DMR out only", 3, 0, 0, health.StatusDegraded, "talk from Zello once"},
		{"USRP in only", 0, 2, 0, health.StatusDegraded, "key up once on a talkgroup"},
		{"both ways", 3, 2, 0, health.StatusHealthy, ""},
		{"both ways with frames needing FEC", 3, 2, 5, health.StatusDegraded, "not DMR's"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ch := newChannel(t, &fakeChip{rate: ambe.RateIndexDMR}, &fakeRadio{})
			ch.calls.Store(tc.to)
			ch.txCalls.Store(tc.from)
			ch.txBadFEC.Store(tc.badFEC)
			res := ch.Check(context.Background())
			if res.Status != tc.wantStatus {
				t.Errorf("status %q, want %q (%s)", res.Status, tc.wantStatus, res.Summary)
			}
			if !strings.Contains(res.Fix, tc.wantFix) {
				t.Errorf("fix %q does not say %q", res.Fix, tc.wantFix)
			}
			// **Since when, every time.** Counts start with QSP, so twice on
			// 2026-09-16 a working transcoder read as broken after a restart.
			if !strings.Contains(res.Summary, "since QSP started at") {
				t.Errorf("summary %q does not say since when it counts", res.Summary)
			}
			if tc.wantStatus == health.StatusDegraded && tc.badFEC == 0 &&
				!strings.Contains(res.Fix, "expected, not a fault") {
				t.Errorf("fix %q does not say a not-yet is expected after a restart", res.Fix)
			}
		})
	}
}
