package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/access"
	"github.com/k9mls/qsp/internal/config"
)

// **A server that cannot refuse a neighbour is not sovereign** (ADR-0052 rule
// 1). Until this, the only way to stop accepting a link that dialled in was to
// ask the other operator to remove their end, or to edit JSON on the server.
func TestAServerWithNoAccessListCanStillRefuseALink(t *testing.T) {
	cfg := config.Config{}
	if cfg.DMR.Access != nil {
		t.Fatal("the zero configuration has an access block; this test is testing nothing")
	}

	refused, why := refuseRegistration(&cfg, 3132912)
	if !refused {
		t.Fatalf("a link could not be refused: %s", why)
	}

	list := parseRegistration(t, cfg)
	if list.Allows(3132912) {
		t.Error("the ID is still accepted")
	}
	// And nobody else is caught by it. An absent block permitted everything,
	// and refusing one link must not take the network off the air.
	if !list.Allows(3155413) {
		t.Error("refusing one link refused another station too")
	}
}

func TestRefusingALinkOnAPermitListTakesOutItsEntry(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration = config.ACL{
		Mode: "permit",
		IDs:  []string{"3132910", "3132912"},
	}

	refused, why := refuseRegistration(&cfg, 3132912)
	if !refused {
		t.Fatalf("a permitted link could not be refused: %s", why)
	}

	list := parseRegistration(t, cfg)
	if list.Allows(3132912) {
		t.Error("the ID is still permitted")
	}
	if !list.Allows(3132910) {
		t.Error("another permitted station was refused too")
	}
}

// The whole network must not go off the air because one link was refused. A
// permit list with no entries refuses every station.
func TestRefusingTheOnlyPermittedStationIsRefused(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration = config.ACL{Mode: "permit", IDs: []string{"3132912"}}

	refused, why := refuseRegistration(&cfg, 3132912)
	if refused {
		t.Fatal("refusing the only permitted station was allowed, which refuses everybody")
	}
	if !strings.Contains(why, "everybody") {
		t.Errorf("the refusal does not say why: %q", why)
	}
	if len(cfg.DMR.Access.Registration.IDs) != 1 {
		t.Errorf("the list was changed anyway: %v", cfg.DMR.Access.Registration.IDs)
	}
}

// An ID inside a permitted range is reported rather than fixed, for the same
// reason the offer path refuses to split one.
func TestAnIDInsideAPermittedRangeIsReportedNotSplit(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration = config.ACL{
		Mode: "permit",
		IDs:  []string{"3132900-3132999"},
	}

	refused, why := refuseRegistration(&cfg, 3132912)
	if refused {
		t.Fatal("a range was split by this page")
	}
	if !strings.Contains(why, "range") {
		t.Errorf("the reason does not mention the range: %q", why)
	}
	if got := cfg.DMR.Access.Registration.IDs; len(got) != 1 || got[0] != "3132900-3132999" {
		t.Errorf("the range was edited: %v", got)
	}
}

func TestRefusingALinkOnADenyListAddsIt(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration = config.ACL{Mode: "deny", IDs: []string{}}

	refused, why := refuseRegistration(&cfg, 3132912)
	if !refused {
		t.Fatalf("a link could not be refused: %s", why)
	}
	if list := parseRegistration(t, cfg); list.Allows(3132912) {
		t.Error("the ID is still accepted")
	}
}

// **Not revoked is not a failure, and the difference matters.** A link written
// before the offer form existed logs in with the shared peer password, so there
// is nothing of its own to delete — and an operator told "revoked" would
// believe a rogue network was locked out when the shared password still admits
// it.
func TestALinkWithNoPasswordOfItsOwnSaysSo(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{}
	cfg.DMR.PeerPasswords = dir

	revoked, err := revokePeerPassword(cfg, 3132912)
	if err != nil {
		t.Fatalf("revokePeerPassword: %v", err)
	}
	if revoked {
		t.Error("a password that was never issued was reported as revoked")
	}

	path := filepath.Join(dir, "3132912")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("writing a password: %v", err)
	}
	revoked, err = revokePeerPassword(cfg, 3132912)
	if err != nil {
		t.Fatalf("revokePeerPassword: %v", err)
	}
	if !revoked {
		t.Error("an issued password was not revoked")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the password file is still there: %v", err)
	}
}

// A server using one shared password has no per-peer directory, and asking for
// one is not an error to report at the operator.
func TestASharedPasswordServerRevokesNothing(t *testing.T) {
	revoked, err := revokePeerPassword(config.Config{}, 3132912)
	if err != nil {
		t.Fatalf("revokePeerPassword: %v", err)
	}
	if revoked {
		t.Error("something was revoked on a server with no per-peer passwords")
	}
}

func parseRegistration(t *testing.T, cfg config.Config) access.List {
	t.Helper()

	if cfg.DMR.Access == nil {
		t.Fatal("no access block was written")
	}
	acl := cfg.DMR.Access.Registration
	list, err := access.Parse("dmr.access.registration", access.Registration,
		access.Mode(acl.Mode), acl.IDs)
	if err != nil {
		t.Fatalf("the registration list this wrote cannot be parsed: %v", err)
	}
	return list
}
