package config

import (
	"strings"
	"testing"
)

// Break it: drop the max_gateways check from Validate.
func TestP25GatewayLimitValidation(t *testing.T) {
	cases := []struct {
		name  string
		limit int
		on    bool
		field string // empty: valid
	}{
		{"left out, which is the default", 0, true, ""},
		{"one", 1, true, ""},
		{"the most allowed", 10000, true, ""},
		{"more than the most", 10001, true, "p25.max_gateways"},
		{"fewer than none", -1, true, "p25.max_gateways"},
		{"nonsense with the listener off is not looked at", -1, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.P25 = P25{Enabled: tc.on, ListenAddress: "127.0.0.1:41000", MaxGateways: tc.limit}
			err := c.Validate()
			switch {
			case tc.field == "" && err != nil:
				t.Errorf("refused: %v", err)
			case tc.field != "" && (err == nil || !strings.Contains(err.Error(), tc.field)):
				t.Errorf("got %v, want an error naming %s", err, tc.field)
			}
		})
	}
}
