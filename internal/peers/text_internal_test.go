package peers

import (
	"errors"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestAColourCodeIsReadAsAnnounced: RPTC carries it as two ASCII digits.
// Anything else is refused, because a burst with the wrong colour code is
// ignored by the hotspot and the failure would look like a wrong encoder.
func TestAColourCodeIsReadAsAnnounced(t *testing.T) {
	tests := []struct {
		raw  string
		want uint8
		ok   bool
	}{
		{"11", 11, true},
		{"01", 1, true},
		{" 1", 1, true},
		{"00", 0, true},
		{"15", 15, true},
		{"16", 0, false},
		{"", 0, false},
		{"ab", 0, false},
		{"-1", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := colourCodeOf(&hbp.Config{ColorCode: tc.raw})
			if tc.ok {
				if err != nil || got != tc.want {
					t.Fatalf("got %d, %v; want %d", got, err, tc.want)
				}
				return
			}
			if !errors.Is(err, ErrTextColourCode) {
				t.Fatalf("error %v, want %v", err, ErrTextColourCode)
			}
		})
	}
}
