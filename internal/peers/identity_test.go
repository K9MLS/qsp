package peers_test

import (
	"net/netip"
	"testing"

	"github.com/k9mls/qsp/internal/peers"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// withIdentity makes the master answer a QSP link with what this server is.
func withIdentity(pkg string) func(*peers.MasterConfig) {
	return func(c *peers.MasterConfig) {
		c.Identity = func() hbp.Identity {
			return hbp.Identity{Network: "KD9EJA-01", Callsign: "KD9EJA", Software: "QSP 0.1.139"}
		}
		c.IsQSPLink = func(cfg hbp.Config) bool { return cfg.PackageID == pkg }
	}
}

// handshake drives login, key and configuration, and returns the responses to
// the configuration.
func handshake(t *testing.T, h *harness, from netip.AddrPort, pkg string) []peers.Response {
	t.Helper()

	out := h.send(hbp.Login{RepeaterID: testID}, from)
	ack, err := hbp.Parse(out.Responses[0].Payload)
	if err != nil {
		t.Fatalf("challenge is unparseable: %v", err)
	}
	h.send(hbp.Key{RepeaterID: testID, Digest: hbp.Digest(ack.(hbp.Ack).Salt(), []byte(testPassword))}, from)

	return h.send(hbp.Config{
		RepeaterID: testID, Callsign: "K9MLS", ColorCode: "11", PackageID: pkg,
	}, from).Responses
}

// identityIn returns the identity among a set of responses, if there is one.
func identityIn(t *testing.T, responses []peers.Response) (hbp.Identity, bool) {
	t.Helper()

	for _, r := range responses {
		msg, err := hbp.Parse(r.Payload)
		if err != nil {
			t.Fatalf("the master sent something unparseable: %v", err)
		}
		if ident, ok := msg.(hbp.Identity); ok {
			return ident, true
		}
	}
	return hbp.Identity{}, false
}

// **A hotspot must never receive an identity, and a QSP link must always get
// one.** The whole safety of sending a tag HBP does not define rests on this:
// the reply goes only to a peer that announced itself as a QSP server, so
// nothing that was not expecting it ever sees one.
//
// Driven through the master rather than through a predicate the test defines,
// because a test that builds the thing it then asserts about cannot fail —
// which this project has now written five times.
func TestOnlyAQSPLinkIsToldWhatThisServerIs(t *testing.T) {
	const linkPackage = "QSP-LINK production"
	from := netip.MustParseAddrPort("192.168.1.27:62031")

	t.Run("a QSP link is answered", func(t *testing.T) {
		h := newHarness(t, withIdentity(linkPackage))
		ident, ok := identityIn(t, handshake(t, h, from, linkPackage))
		if !ok {
			t.Fatal("a QSP link registered and was told nothing about this server")
		}
		if ident.Network != "KD9EJA-01" || ident.Callsign != "KD9EJA" {
			t.Errorf("the identity is %+v, want this server's name and callsign", ident)
		}
		// Addressed, so a server with several links knows which one answered.
		if ident.RepeaterID != testID {
			t.Errorf("the identity is addressed to %d, want %d", ident.RepeaterID, testID)
		}
	})

	t.Run("a hotspot is not", func(t *testing.T) {
		h := newHarness(t, withIdentity(linkPackage))
		if _, ok := identityIn(t, handshake(t, h, from, "Pi-Star_v4.1.6")); ok {
			t.Error("a hotspot was sent a QSP identity, which it never asked for")
		}
	})

	t.Run("an instance with no identity configured says nothing", func(t *testing.T) {
		h := newHarness(t)
		if _, ok := identityIn(t, handshake(t, h, from, linkPackage)); ok {
			t.Error("an instance with no identity announced empty fields")
		}
	})
}

// The HBP handshake must complete exactly as it did before: the identity is an
// extra datagram after the ACK, not instead of it.
func TestTheAckStillComesFirst(t *testing.T) {
	const linkPackage = "QSP-LINK production"
	h := newHarness(t, withIdentity(linkPackage))

	responses := handshake(t, h, netip.MustParseAddrPort("192.168.1.27:62031"), linkPackage)
	if len(responses) != 2 {
		t.Fatalf("configuration produced %d responses, want the ACK and the identity", len(responses))
	}
	first, err := hbp.Parse(responses[0].Payload)
	if err != nil {
		t.Fatalf("the first response is unparseable: %v", err)
	}
	if first.Kind() != hbp.KindAck {
		t.Errorf("the first response is %s, want %s — a peer waiting for its ACK must get it first",
			first.Kind(), hbp.KindAck)
	}
}
