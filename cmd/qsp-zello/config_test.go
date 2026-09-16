//go:build zello

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheConnectorsConfigurationIsChecked.
func TestTheConnectorsConfigurationIsChecked(t *testing.T) {
	good := `{"logon_socket":"/run/qsp/zello.sock","channel":"K9MLS","usrp_listen":"127.0.0.1:32002","usrp_peer":"127.0.0.1:32001","health_listen":"127.0.0.1:18090"}`
	tests := []struct {
		name, body, want string
	}{
		{"valid", good, ""},
		{"a misspelled field is refused", strings.Replace(good, `"channel"`, `"chanel"`, 1), "unknown field"},
		{"a relative socket", strings.Replace(good, "/run/qsp/zello.sock", "zello.sock", 1), "logon_socket"},
		{"no channel", strings.Replace(good, `"K9MLS"`, `""`, 1), "channel"},
		{"a wildcard peer", strings.Replace(good, `"127.0.0.1:32001"`, `"0.0.0.0:32001"`, 1), "usrp_peer"},
		{"a health endpoint beyond loopback", strings.Replace(good, "127.0.0.1:18090", "0.0.0.0:18090", 1), "loopback"},
		{"a plain-text endpoint", strings.Replace(good, `"channel"`, `"endpoint":"ws://zello.io/ws","channel"`, 1), "wss://"},
		{"a password has nowhere to go", strings.Replace(good, `"channel"`, `"password":"x","channel"`, 1), "unknown field"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "c.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := loadConfig(path)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want one mentioning %q", err, tc.want)
			}
		})
	}
}

// TestTheShippedExampleIsValid: internal/config validates deploy/*/*.json as
// QSP configurations, so the connector's example is named .json.example and
// checked here instead — a broken example is worse than none.
func TestTheShippedExampleIsValid(t *testing.T) {
	if _, err := loadConfig(filepath.Join("..", "..", "deploy", "systemd", "qsp-zello.json.example")); err != nil {
		t.Fatalf("the shipped example does not load: %v", err)
	}
}
