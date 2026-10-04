package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// Break it: drop the entry from moved, and a server that saved the old name
// refuses to start on the build that renamed it.
func TestASectionSavedUnderItsOldNameStillLoads(t *testing.T) {
	base := func(section string) string {
		doc, err := json.Marshal(Default())
		if err != nil {
			t.Fatalf("marshalling the default: %v", err)
		}
		return strings.Replace(string(doc), "{", "{"+section+",", 1)
	}
	cases := []struct {
		name    string
		section string
		ok      bool
		site    uint8
		address string
	}{
		{"the old name alone",
			`"quantar":{"enabled":true,"listen_address":"0.0.0.0:1994","allowed_routers":["192.0.2.4"],"site":7}`,
			true, 7, "0.0.0.0:1994"},
		{"the current name alone",
			`"p25_repeaters":{"enabled":true,"listen_address":"0.0.0.0:1995","allowed_routers":[],"site":9}`,
			true, 9, "0.0.0.0:1995"},
		{"both, and the current name wins",
			`"quantar":{"enabled":true,"listen_address":"0.0.0.0:1994","allowed_routers":[],"site":7},` +
				`"p25_repeaters":{"enabled":true,"listen_address":"0.0.0.0:1995","allowed_routers":[],"site":9}`,
			true, 9, "0.0.0.0:1995"},
		{"a name that was never one is still refused",
			`"quantars":{"enabled":true}`, false, 0, ""},
		{"an unknown field inside the old name is still refused",
			`"quantar":{"enabled":true,"listen_adress":"0.0.0.0:1994"}`, false, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := Load(strings.NewReader(base(tc.section)))
			if (err == nil) != tc.ok {
				t.Fatalf("Load returned %v", err)
			}
			if !tc.ok {
				return
			}
			if !c.P25Repeaters.Enabled || c.P25Repeaters.Site != tc.site ||
				c.P25Repeaters.ListenAddress != tc.address {
				t.Errorf("loaded %+v", c.P25Repeaters)
			}
		})
	}
}
