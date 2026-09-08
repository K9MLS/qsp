package server

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/peering"
)

// anOfferedLink is an invitation as the listening side would have made one.
func anOfferedLink(t *testing.T) (peering.LinkInvitation, string, string) {
	t.Helper()

	password, err := peering.NewPassphrase()
	if err != nil {
		t.Fatalf("generating a password: %v", err)
	}
	inv := peering.LinkInvitation{
		Network:     "QSP Test Server",
		Callsign:    "KD9EJA",
		Address:     "192.168.1.27:62031",
		RepeaterID:  3132914,
		Fingerprint: peering.FingerprintOf(password),
		Issued:      time.Now().UTC(),
	}
	token, err := peering.EncodeLink(inv)
	if err != nil {
		t.Fatalf("EncodeLink: %v", err)
	}
	return inv, password, token
}

// **The whole reason this path exists.** The OpenBridge accept form writes a
// link and a bridge, and a bridge joins endpoints, and an endpoint carries a
// timeslot — so every peering agreed through the console produced the
// configuration ADR-0051 exists to prevent. A linked QSP server is a peer, and
// repeat reaches it without any of that.
//
// Asserted against the upstream a link accept builds, rather than through the
// handler, because what matters is the shape of the configuration and not the
// route it arrived by.
func TestAnAcceptedLinkIsOneUpstreamAndNoBridge(t *testing.T) {
	inv, _, _ := anOfferedLink(t)

	before := config.Config{}
	before.DMR.Identity.Callsign = "K9MLS"
	before.DMR.Bridges = []config.Bridge{{Name: "an existing bridge"}}

	cfg := linkConfig(before, "test-server", inv, "/var/lib/qsp/test-server.pass")

	if got, want := len(cfg.DMR.Bridges), len(before.DMR.Bridges); got != want {
		t.Fatalf("accepting a link changed the bridges from %d to %d", want, got)
	}
	if len(cfg.DMR.Upstreams) != 1 {
		t.Fatalf("accepting a link wrote %d upstreams", len(cfg.DMR.Upstreams))
	}

	up := cfg.DMR.Upstreams[0]
	if up.Protocol != config.UpstreamQSP {
		t.Errorf("the protocol is %q, want %q", up.Protocol, config.UpstreamQSP)
	}
	if up.Address != inv.Address {
		t.Errorf("the address is %q, want %q", up.Address, inv.Address)
	}
	if up.RepeaterID != inv.RepeaterID {
		t.Errorf("the DMR ID is %d, want %d", up.RepeaterID, inv.RepeaterID)
	}
	if up.ListenAddress != "" {
		t.Errorf("a dialling link was given a listen address: %q", up.ListenAddress)
	}
	if up.NetworkID != 0 {
		t.Errorf("a qsp link was given a network ID: %d", up.NetworkID)
	}
	if len(up.Export) != 0 || len(up.Import) != 0 {
		t.Errorf("a qsp link was given talkgroup lists: export %v import %v", up.Export, up.Import)
	}
	if up.PassphraseFile != "" {
		t.Errorf("a qsp link was given an OpenBridge passphrase file: %q", up.PassphraseFile)
	}
	if up.PasswordFile == "" {
		t.Error("a qsp link has no password file")
	}
	if up.Identity == nil || up.Identity.Callsign != "K9MLS" {
		t.Errorf("the link announces %+v, want this server's callsign", up.Identity)
	}
}

// The password is never optional on this side. The offering side is a different
// machine, so there is no outstanding offer here to look it up from, and the
// empty-box dead end that stopped the first OpenBridge peering cannot arise.
func TestALinkInvitationCannotBeAcceptedWithoutItsPassword(t *testing.T) {
	inv, password, _ := anOfferedLink(t)
	now := time.Now().UTC()

	if err := inv.Accept("", now); err == nil {
		t.Error("an empty password was accepted")
	}
	if err := inv.Accept(password, now); err != nil {
		t.Errorf("the right password was refused: %v", err)
	}
}

// A token of the wrong kind must be routed, not parsed. Reading an OpenBridge
// peering as a link — or the reverse — would write the wrong configuration
// from a token that is entirely valid.
func TestTheAcceptBoxRoutesByTokenKind(t *testing.T) {
	_, _, linkToken := anOfferedLink(t)

	obToken, err := peering.Encode(peering.Invitation{
		Network:     "Somebody Else",
		Callsign:    "KB9TYC",
		Address:     "qsp.example.com:62045",
		NetworkID:   3155412,
		Fingerprint: peering.FingerprintOf("a-passphrase-long-enough-to-be-accepted"),
		Issued:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if kind, err := peering.KindOf(linkToken); err != nil || kind != peering.KindLink {
		t.Errorf("a link token routed to %q (%v)", kind, err)
	}
	if kind, err := peering.KindOf(obToken); err != nil || kind != peering.KindOpenBridge {
		t.Errorf("an OpenBridge token routed to %q (%v)", kind, err)
	}
	if _, err := peering.KindOf("not a token at all"); err == nil {
		t.Error("something that is not a token was routed somewhere")
	}
}

// The ID the far end allocated has to be free at this end too, and every way it
// can collide fails silently on the wire.
func TestAnAcceptedLinkRefusesAnIDThisServerAlreadyUses(t *testing.T) {
	inv, _, _ := anOfferedLink(t)

	cfg := config.Config{}
	cfg.DMR.Upstreams = []config.Upstream{{Name: "production", RepeaterID: inv.RepeaterID}}

	why := idAlreadyMeansSomethingElse(cfg, inv.RepeaterID)
	if why == "" {
		t.Fatal("an ID already used by another link was accepted")
	}
	if !strings.Contains(why, "production") {
		t.Errorf("the refusal does not name the link in the way: %q", why)
	}
}
