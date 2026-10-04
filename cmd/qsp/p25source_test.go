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

// Break it: store a nil listener in the interface, and the gateway listener
// calls into a repeater link that does not exist.
func TestNoRepeaterLinkIsNoSink(t *testing.T) {
	if repeaterSink(nil) != nil {
		t.Error("a nil repeater link became a sink")
	}
	l, err := v24link.New(logging.Discard(), v24link.Config{ListenAddress: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("v24link: %v", err)
	}
	if repeaterSink(l) == nil {
		t.Error("a repeater link is not a sink")
	}

	// And the other way: the gateways are looked up when a call arrives, so
	// before there are any a repeater's frame goes nowhere and breaks nothing.
	late := gatewaysWhenBuilt{&app{}}
	if late.FromRepeater([]byte{0x63}) != 0 || late.EndFromRepeater() != 0 {
		t.Error("a frame was carried to gateways that do not exist")
	}
}
