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
		{"a socket and an issuer", Zello{LogonSocket: "/run/qsp/zello.sock", Issuer: "iss"}, ""},
		{"a relative socket", Zello{LogonSocket: "run/zello.sock", Issuer: "iss"}, "zello.logon_socket"},
		{"a socket and no issuer", Zello{LogonSocket: "/run/qsp/zello.sock"}, "zello.issuer"},
		{"an issuer alone serves nothing and is harmless", Zello{Issuer: "iss"}, ""},
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
