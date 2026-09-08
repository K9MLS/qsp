package main

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// TestCheckCatchesAnAddressThisHostDoesNotHave is the whole reason -check
// started binding sockets.
//
// It printed "/var/lib/qsp/qsp.json is valid" for a document the process then
// died on, at bind time, on an upstream listen address this machine does not
// hold. systemd crash-looped to its start limit and recovery took two rounds of
// hand-edited JSON on a live server. **A gate that gives false assurance is
// worse than no gate.**
//
// 192.0.2.1 is TEST-NET-1: reserved, never assigned to an interface, no DNS
// needed. It stands in for qsp.hopto.me.
func TestCheckCatchesAnAddressThisHostDoesNotHave(t *testing.T) {
	cfg := config.Default()
	cfg.Server.ListenAddress = ""
	cfg.DMR.Enabled = false
	cfg.IPSC.Enabled = false
	cfg.DMR.Upstreams = []config.Upstream{{
		Name:          "Test Server",
		Enabled:       true,
		ListenAddress: "192.0.2.1:62045",
	}}

	var out bytes.Buffer
	err := checkBinds(&out, cfg)
	if err == nil {
		t.Fatalf("-check passed a configuration qsp cannot start on:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "Test Server") {
		t.Errorf("the failure does not name the link: %v", err)
	}
	if !strings.Contains(out.String(), "CANNOT BIND") {
		t.Errorf("the report does not say which address:\n%s", out.String())
	}
}

// TestAnAddressInUseIsNotReportedAsAFailure decides whether -check can be run
// at all.
//
// It is run against a live configuration while the service holding those ports
// is running, so **every address in the document is in use by QSP itself**. If
// that came back as a fault, an honest check on a healthy server would report
// three, and an operator would learn to ignore the output — which is the same
// defect as the false pass, in the other direction.
func TestAnAddressInUseIsNotReportedAsAFailure(t *testing.T) {
	held, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("could not take an address to hold: %v", err)
	}
	defer held.Close()

	cfg := config.Default()
	cfg.Server.ListenAddress = ""
	cfg.IPSC.Enabled = false
	cfg.DMR.Enabled = true
	cfg.DMR.ListenAddress = held.LocalAddr().String()
	cfg.DMR.Upstreams = nil

	var out bytes.Buffer
	if err := checkBinds(&out, cfg); err != nil {
		t.Fatalf("a port held by the running service was reported as a failure: %v", err)
	}
	if !strings.Contains(out.String(), "in use") {
		t.Errorf("the report does not say the address is in use:\n%s", out.String())
	}
}

// TestOnlyTheAddressesQSPBindsAreChecked.
//
// An upstream's Address belongs to the far end and binding it here would prove
// nothing about anything. A homebrew upstream binds no listener at all — it
// logs in to somebody else's master and the socket is outbound. A disabled
// link binds nothing either. Checking any of them would produce failures an
// operator cannot act on, which is how a gate stops being read.
func TestOnlyTheAddressesQSPBindsAreChecked(t *testing.T) {
	cfg := config.Default()
	cfg.Server.ListenAddress = ""
	cfg.DMR.Enabled = false
	cfg.IPSC.Enabled = false
	cfg.DMR.Upstreams = []config.Upstream{
		{Name: "homebrew-link", Enabled: true, Protocol: config.UpstreamHomebrew,
			ListenAddress: "192.0.2.1:62045", Address: "192.0.2.9:62031"},
		{Name: "switched-off", Enabled: false, ListenAddress: "192.0.2.1:62046"},
	}

	var out bytes.Buffer
	if err := checkBinds(&out, cfg); err != nil {
		t.Fatalf("checked an address qsp never binds: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("reported on a listener that does not exist:\n%s", out.String())
	}
}

// TestTheDisabledListenersAreLeftAlone. A configuration with the DMR listener
// off does not bind 62031, and an operator running a console-only instance
// should not be told it cannot.
func TestTheDisabledListenersAreLeftAlone(t *testing.T) {
	cfg := config.Default()
	cfg.Server.ListenAddress = ""
	cfg.DMR.Enabled = false
	cfg.DMR.ListenAddress = "192.0.2.1:62031"
	cfg.IPSC.Enabled = false
	cfg.IPSC.ListenAddress = "192.0.2.1:50000"
	cfg.DMR.Upstreams = nil

	var out bytes.Buffer
	if err := checkBinds(&out, cfg); err != nil {
		t.Fatalf("a disabled listener was checked: %v", err)
	}
}
