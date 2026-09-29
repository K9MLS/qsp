package peers_test

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// The master is what the application hands routing as its LinkedServers.
var _ routing.LinkedServers = (*peers.Master)(nil)

// registerAs drives a full handshake for one peer with a given package ID.
func registerAs(t *testing.T, h *harness, id hbp.RepeaterID, from netip.AddrPort, pkg string) {
	t.Helper()
	out := h.send(hbp.Login{RepeaterID: id}, from)
	ack, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("challenge is unparseable: %v", err)
	}
	h.send(hbp.Key{RepeaterID: id, Digest: hbp.Digest(ack.(hbp.Ack).Salt(), []byte(testPassword))}, from)
	h.send(hbp.Config{RepeaterID: id, Callsign: "K9MLS", ColorCode: "1", PackageID: pkg}, from)
}

// **Only a peer that registered as a QSP server is a linked server** (ADR-0069).
//
// A private call to a radio not heard here is offered to every one of these,
// so a hotspot in this list would be keyed for calls it can never carry, and
// a server missing from it is the fault this exists to fix: a private call
// between a hotspot on one server and a repeater on another stopping at the
// first.
//
// Driven through the master's handshake rather than a predicate the test
// builds, for the reason identity_test.go gives.
//
// To see it fail: in LinkedServerPeers, replace m.cfg.IsQSPLink(*p.Config)
// with true, and the hotspot appears among the servers.
func TestOnlyARegisteredQSPServerIsALinkedServer(t *testing.T) {
	const linkPackage = "QSP-LINK production"
	const (
		hotspot  = hbp.RepeaterID(3132910)
		server   = hbp.RepeaterID(3139001)
		otherSrv = hbp.RepeaterID(3139000)
		halfway  = hbp.RepeaterID(3139002)
	)
	anyID := func(c *peers.MasterConfig) {
		c.Password = func(hbp.RepeaterID) ([]byte, bool) { return []byte(testPassword), true }
		c.IsQSPLink = func(cfg hbp.Config) bool { return cfg.PackageID == linkPackage }
	}

	cases := []struct {
		name string
		opts []func(*peers.MasterConfig)
		want []hbp.RepeaterID
	}{
		{"servers, ordered, and no hotspot", []func(*peers.MasterConfig){anyID}, []hbp.RepeaterID{otherSrv, server}},
		{"no way to tell means none", []func(*peers.MasterConfig){anyID, func(c *peers.MasterConfig) { c.IsQSPLink = nil }}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, tc.opts...)
			registerAs(t, h, hotspot, netip.MustParseAddrPort("192.168.1.155:56955"), "MMDVM_MMDVM_HS_Dual_Hat")
			registerAs(t, h, server, netip.MustParseAddrPort("192.168.1.27:62031"), linkPackage)
			registerAs(t, h, otherSrv, netip.MustParseAddrPort("192.168.1.28:62031"), linkPackage)
			// Logged in but never configured: it has announced nothing, so
			// it is neither ready nor anything's server.
			h.send(hbp.Login{RepeaterID: halfway}, netip.MustParseAddrPort("192.168.1.29:62031"))

			if got := h.m.LinkedServerPeers(); !slices.Equal(got, tc.want) {
				t.Errorf("LinkedServerPeers() = %v, want %v", got, tc.want)
			}
		})
	}
}
