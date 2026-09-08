package server

import (
	"encoding/json"
	"net/http"
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
	// **Enabled, and it was not.** This fixture omitted the flag, so it
	// described a link turned off in the configuration while asserting that a
	// restart would open one — the same mistake reconcileLinks was making, in
	// the test that was supposed to catch it.
	cfg.DMR.Upstreams = []config.Upstream{{
		Name: "cameron", Protocol: "openbridge", Enabled: true,
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

// TestAnAcceptedLinkAppearsOnAnInstanceThatBootedWithNone is the defect a live
// peering found, in code written to prevent exactly this.
//
// cmd/qsp decides the link source is nil at startup, from the startup
// configuration: `len(cfg.DMR.Upstreams) == 0` means no source for the life of
// the process. handleLinks returned on that before reading the configuration at
// all, so a peering accepted afterwards could never appear, whatever the
// document said — and the page reported "this build has no links configured",
// which was true when written and false the moment a link was added.
//
// **That is every fresh install accepting its first peering.** The exchange
// completed across two servers, both audit events were written, the
// configuration was applied, and the operator saw an empty page.
func TestAnAcceptedLinkAppearsOnAnInstanceThatBootedWithNone(t *testing.T) {
	cm := newStubConfig()
	cfg := cm.current
	cfg.DMR.Upstreams = []config.Upstream{{
		Name: "bcara", Protocol: "openbridge", Enabled: true,
		Address: "qsp.hopto.me:62045", ListenAddress: "0.0.0.0:62045",
		NetworkID: 3132910,
	}}
	cm.current = cfg

	srv, a := newConfigServer(t, cm, &recordingAudit{})
	// Booted with no upstreams, so cmd/qsp handed the server a nil source.
	srv.opts.Links = nil

	rec := authed(t, srv, a, http.MethodGet, "/api/links", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("the links page failed with %d", rec.Code)
	}
	var body linksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v", err)
	}
	if len(body.Links) != 1 {
		t.Fatalf("an accepted link is in the configuration and %d are shown: %s",
			len(body.Links), rec.Body.String())
	}
	if !body.Links[0].Configured || body.Links[0].Open {
		t.Errorf("the link is misreported: %+v", body.Links[0])
	}
	if body.Links[0].PendingRestart == "" {
		t.Error("nothing tells the operator a restart will open it")
	}
	// The old message claimed something about the build that the document
	// contradicts.
	if body.Reason != "" {
		t.Errorf("a page listing a link also says %q", body.Reason)
	}
}

// TestAnInstanceWithNoLinksSaysSoAboutTheConfiguration keeps the empty case
// honest, rather than trading one wrong sentence for no sentence.
func TestAnInstanceWithNoLinksSaysSoAboutTheConfiguration(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, &recordingAudit{})
	srv.opts.Links = nil

	rec := authed(t, srv, a, http.MethodGet, "/api/links", "")
	var body linksResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%v", err)
	}
	if len(body.Links) != 0 {
		t.Fatalf("links appeared from an empty configuration: %s", rec.Body.String())
	}
	if body.Reason == "" {
		t.Error("an empty links page explains nothing")
	}
	if strings.Contains(body.Reason, "build") {
		t.Errorf("the reason blames the build for a configuration fact: %q", body.Reason)
	}
}

// TestADisabledLinkIsNotAwaitingARestart is what an operator saw after turning
// a link off.
//
// reconcileLinks never read Enabled, so a disabled link was described as
// "configured and not open; QSP has not been restarted since this link was
// added" and advised to "restart QSP to open this link". Both sentences are
// false, and following the advice means restarting a live network — dropping
// every station on it — to discover that nothing changed.
//
// Observed on the test server on 2026-09-08, on a link that had been disabled
// deliberately half an hour earlier.
func TestADisabledLinkIsNotAwaitingARestart(t *testing.T) {
	var cfg config.Config
	cfg.DMR.Upstreams = []config.Upstream{{
		Name: "QSP Test Server", Protocol: "openbridge", Enabled: false,
		Address: "192.168.1.247:62045", ListenAddress: "0.0.0.0:62045",
		NetworkID: 9999999,
	}}

	out := reconcileLinks(nil, cfg)
	if len(out) != 1 {
		t.Fatalf("got %d links, want the disabled one listed", len(out))
	}
	l := out[0]
	if l.Enabled {
		t.Error("a disabled link reports itself as enabled")
	}
	if l.PendingRestart != "" {
		t.Errorf("a disabled link claims a restart would change it: %q", l.PendingRestart)
	}
	if !strings.Contains(l.Summary, "disabled") {
		t.Errorf("the summary does not say it is disabled: %q", l.Summary)
	}
	if !strings.Contains(l.Advice, "enabled") {
		t.Errorf("the advice does not say how to turn it back on: %q", l.Advice)
	}
	// Still listed. A link an operator turned off and cannot see is the same
	// silence this page exists to end.
	if !l.Configured {
		t.Error("a disabled link is reported as unconfigured, which means removed")
	}
}

// TestAQSPLinkAnnouncesItsDMRID, because it has no network ID and never will.
//
// The page printed the network ID or, absent one, the words "no network ID" —
// so a qsp link carrying traffic displayed a missing field as though it were a
// fault. OpenBridge identifies the sending server by a network ID; a link that
// registers as a station is known by its DMR ID. Same question, different
// field, and the page is not the place to choose between them.
func TestAQSPLinkAnnouncesItsDMRID(t *testing.T) {
	var cfg config.Config
	cfg.DMR.Upstreams = []config.Upstream{
		{Name: "production", Protocol: config.UpstreamQSP, Enabled: true,
			Address: "192.168.1.247:62031", RepeaterID: 3132912},
		{Name: "cameron", Protocol: config.UpstreamOpenBridge, Enabled: true,
			Address: "kb9tyc.example.com:62045", ListenAddress: "0.0.0.0:62045",
			NetworkID: 3127045},
	}

	out := reconcileLinks(nil, cfg)
	if len(out) != 2 {
		t.Fatalf("got %d links, want 2", len(out))
	}
	for _, l := range out {
		switch l.Name {
		case "production":
			if l.Announces != "3132912" {
				t.Errorf("a qsp link announces %q, want its DMR ID 3132912", l.Announces)
			}
		case "cameron":
			if l.Announces != "3127045" {
				t.Errorf("an OpenBridge link announces %q, want its network ID 3127045", l.Announces)
			}
		}
	}
}
