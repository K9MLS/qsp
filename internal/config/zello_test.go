package config

import (
	"strings"
	"testing"
)

// TestTheZelloLogonSocketIsChecked.
//
// To see a row fail, remove its case from the zello block in Validate.
func TestTheZelloLogonSocketIsChecked(t *testing.T) {
	tests := []struct {
		name      string
		zello     Zello
		wantField string
	}{
		{"not configured", Zello{}, ""},
		{"on and complete", Zello{Enabled: true, LogonSocket: "/run/qsp/zello.sock", Issuer: "iss", Channel: "c"}, ""},
		{"on with a relative socket", Zello{Enabled: true, LogonSocket: "run/zello.sock", Issuer: "iss", Channel: "c"}, "zello.logon_socket"},
		{"on with no socket", Zello{Enabled: true, Issuer: "iss", Channel: "c"}, "zello.logon_socket"},
		{"on with no issuer", Zello{Enabled: true, LogonSocket: "/run/qsp/zello.sock", Channel: "c"}, "zello.issuer"},
		{"on with no channel", Zello{Enabled: true, LogonSocket: "/run/qsp/zello.sock", Issuer: "iss"}, "zello.channel"},
		{"off keeps half-entered values without complaint", Zello{LogonSocket: "relative", Channel: ""}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.Zello = tc.zello
			err := c.Validate()
			if tc.wantField == "" {
				if err != nil && strings.Contains(err.Error(), "zello.") {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantField) {
				t.Fatalf("error %v, want a refusal naming %s", err, tc.wantField)
			}
		})
	}
}
