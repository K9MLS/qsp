package main

import (
	"testing"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/p25link"
	"github.com/k9mls/qsp/internal/v24link"
)

// Break it: return a source when neither listener runs, and a server with no
// P25 at all draws an empty P25 row; or leave GatewaysOff unset, and a server
// linking only a repeater says no gateways have linked to a listener it is
// not running.
func TestTheP25RowReportsWhicheverListenersRun(t *testing.T) {
	gateways, err := p25link.New(logging.Discard(), p25link.Config{ListenAddress: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("p25link: %v", err)
	}
	repeaters, err := v24link.New(logging.Discard(), v24link.Config{ListenAddress: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("v24link: %v", err)
	}
	tests := []struct {
		name        string
		gateways    *p25link.Listener
		repeaters   *v24link.Listener
		present     bool
		gatewaysOff bool
	}{
		{"neither", nil, nil, false, false},
		{"gateways only", gateways, nil, true, false},
		{"repeaters only", nil, repeaters, true, true},
		{"both", gateways, repeaters, true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := p25Source(tc.gateways, tc.repeaters)
			if (src != nil) != tc.present {
				t.Fatalf("source present is %v", src != nil)
			}
			if src == nil {
				return
			}
			p25 := src.Traffic().P25
			if p25 == nil {
				t.Fatal("the source reports no P25 figures")
			}
			if p25.GatewaysOff != tc.gatewaysOff {
				t.Errorf("gateways off is %v", p25.GatewaysOff)
			}
		})
	}
}
