package server

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// TestARemovedLinkStopsLookingHealthy is the defect, from the running system.
//
// Two links were removed from the configuration, correctly, and went on being
// listed for three hours: sockets bound, counters shown, a "Nothing yet" pill
// indistinguishable from a live link on a quiet network. `DELETE` answered 404
// for both, which was also correct. **The page and the remover read different
// sources and nothing compared them**, so the honest handler looked broken and
// the stale display looked authoritative.
func TestARemovedLinkStopsLookingHealthy(t *testing.T) {
	running := []LinkStatus{{
		Name: "Test Server", Protocol: "openbridge", FarEnd: "qsp.hopto.me:62045",
		Open: true, Summary: "no traffic has ever arrived on this link",
		Advice: "check the far end has this server's address",
	}}
	var cfg config.Config // no upstreams: the link was removed

	out := reconcileLinks(running, cfg)
	if len(out) != 1 {
		t.Fatalf("got %d links, want the open one reported", len(out))
	}
	if out[0].Configured {
		t.Error("a link absent from the configuration is reported as configured")
	}
	if out[0].PendingRestart == "" {
		t.Error("nothing says a restart would close it")
	}
	// **The advice is replaced, not kept.** Telling an operator to check the
	// far end's address and firewall is expensive and wrong for a link they
	// deliberately deleted.
	if strings.Contains(out[0].Advice, "far end") {
		t.Errorf("a removed link still advises checking the far end: %q", out[0].Advice)
	}
}

// TestAnAcceptedLinkIsListedBeforeItIsOpen is the same gap, other direction.
//
// Upstreams are built once at startup, so a peering agreed since then has no
// socket. Omitting it would leave an operator who has just agreed a link
// unable to see it at all, which is the silence this page exists to end.
func TestAnAcceptedLinkIsListedBeforeItIsOpen(t *testing.T) {
	var cfg config.Config
	cfg.DMR.Upstreams = []config.Upstream{{
		Name: "cameron", Protocol: "openbridge",
		Address: "kb9tyc.example.com:62045", ListenAddress: "0.0.0.0:62045",
		NetworkID: 3127045,
	}}

	out := reconcileLinks(nil, cfg)
	if len(out) != 1 {
		t.Fatalf("got %d links, want the configured one listed", len(out))
	}
	l := out[0]
	if !l.Configured {
		t.Error("a link in the configuration is reported as unconfigured")
	}
	if l.Open {
		t.Error("a link with no socket is reported as open")
	}
	if l.PendingRestart == "" {
		t.Error("nothing says a restart would open it")
	}
	// The facts an operator needs to check their end are carried across from
	// the configuration rather than left blank.
	if l.FarEnd == "" || l.Listening == "" || l.NetworkID == 0 {
		t.Errorf("a not-yet-open link is missing its configured facts: %+v", l)
	}
}

// TestALinkThatIsBothRunningAndConfiguredSaysNothingExtra.
//
// The notice has to be absent in the ordinary case. A banner that is always
// there is one nobody reads, and then it is not a banner.
func TestALinkThatIsBothRunningAndConfiguredSaysNothingExtra(t *testing.T) {
	running := []LinkStatus{{
		Name: "cameron", Open: true, EverReceived: true,
		Summary: "carrying traffic", Advice: "",
	}}
	var cfg config.Config
	cfg.DMR.Upstreams = []config.Upstream{{Name: "cameron"}}

	out := reconcileLinks(running, cfg)
	if len(out) != 1 {
		t.Fatalf("got %d links, want one", len(out))
	}
	if !out[0].Configured {
		t.Error("a running, configured link is not reported as configured")
	}
	if out[0].PendingRestart != "" {
		t.Errorf("a healthy link claims a restart is pending: %q", out[0].PendingRestart)
	}
	if out[0].Summary != "carrying traffic" {
		t.Errorf("the link's own summary was overwritten: %q", out[0].Summary)
	}
}

// TestTheNamesAreMatchedTheWayTheRemoverMatchesThem.
//
// handleRemoveLink uses strings.EqualFold. If this compared case-sensitively,
// a link named "Test Server" in the document and reported as "test server" by
// the running set would show as both removed and not-yet-open at once — two
// rows for one link, and both wrong.
func TestTheNamesAreMatchedTheWayTheRemoverMatchesThem(t *testing.T) {
	running := []LinkStatus{{Name: "Test Server", Open: true}}
	var cfg config.Config
	cfg.DMR.Upstreams = []config.Upstream{{Name: "test server"}}

	out := reconcileLinks(running, cfg)
	if len(out) != 1 {
		t.Fatalf("one link became %d rows; the names did not match", len(out))
	}
	if !out[0].Configured || out[0].PendingRestart != "" {
		t.Errorf("a configured link was reported as removed: %+v", out[0])
	}
}
