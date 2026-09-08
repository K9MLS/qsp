package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// A permit list refuses everything it does not name, so offering a link means
// adding the ID to it. This is the failure that cost a round trip through the
// far end's journal on 2026-09-08: the ID was not in the list, and MSTNAK
// carries no reason for the end that was refused.
func TestOfferingALinkAddsTheIDToAPermitList(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration.Mode = "permit"
	cfg.DMR.Access.Registration.IDs = []string{"3132910"}

	if err := allowRegistration(&cfg, 3132913); err != nil {
		t.Fatalf("allowRegistration: %v", err)
	}

	got := cfg.DMR.Access.Registration.IDs
	if len(got) != 2 || got[1] != "3132913" {
		t.Fatalf("the registration list is %v, want the new ID appended", got)
	}
	if got[0] != "3132910" {
		t.Errorf("the existing entry changed to %q", got[0])
	}
}

// A deny list allows anything it does not name, so there is nothing to add and
// nothing to change. A list that grew an entry here would be denying the link
// it was asked to permit.
func TestOfferingALinkLeavesAPermissiveListAlone(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration.Mode = "deny"
	cfg.DMR.Access.Registration.IDs = []string{}

	if err := allowRegistration(&cfg, 3132913); err != nil {
		t.Fatalf("allowRegistration: %v", err)
	}
	if len(cfg.DMR.Access.Registration.IDs) != 0 {
		t.Errorf("a permissive list gained %v", cfg.DMR.Access.Registration.IDs)
	}
}

// An ID inside a denied range is refused rather than fixed. Removing one ID
// from "3132900-3132999" means splitting a range in a live access list, and an
// edit of that shape has taken a club's network down for an hour in this
// project. The operator is told which way it is in and left to decide.
func TestOfferingALinkRefusesADeniedIDRatherThanRewritingTheRange(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration.Mode = "deny"
	cfg.DMR.Access.Registration.IDs = []string{"3132900-3132999"}
	before := append([]string(nil), cfg.DMR.Access.Registration.IDs...)

	err := allowRegistration(&cfg, 3132913)
	if err == nil {
		t.Fatal("a denied ID was accepted")
	}
	if !strings.Contains(err.Error(), "3132913") {
		t.Errorf("the refusal does not name the ID: %v", err)
	}
	if diff := cfg.DMR.Access.Registration.IDs; len(diff) != len(before) || diff[0] != before[0] {
		t.Errorf("the deny list was rewritten to %v", diff)
	}
}

// **A server with no access block is the default**, and every list permits
// everything when one is absent. `DMR.Access` is a pointer, so the first
// version of this handler dereferenced nil and panicked the offer on exactly
// the servers that had never restricted anything. Found by writing this test,
// not by reading the code.
func TestOfferingALinkOnAServerWithNoAccessBlock(t *testing.T) {
	cfg := config.Config{}
	if cfg.DMR.Access != nil {
		t.Fatal("the zero configuration has an access block; this test is testing nothing")
	}

	if err := allowRegistration(&cfg, 3132913); err != nil {
		t.Fatalf("allowRegistration: %v", err)
	}
	if cfg.DMR.Access != nil {
		t.Error("an access block was created where the operator had none")
	}
}

// Three collisions that produce silence rather than an error. Each has cost
// this project time, and none of them reports anything on the end that fails.
func TestAnIDThatAlreadyMeansSomethingIsRefused(t *testing.T) {
	base := func() config.Config {
		cfg := config.Config{}
		cfg.IPSC.Enabled = true
		cfg.IPSC.MasterID = 3132911
		cfg.DMR.Upstreams = []config.Upstream{
			{Name: "test-server", RepeaterID: 3132912},
		}
		cfg.DMR.Subscription.Static = []config.StaticAttachment{{Peer: 3132910}}
		return cfg
	}

	for _, tc := range []struct {
		name string
		id   uint32
		want string
	}{
		{"the IPSC master's own ID", 3132911, "IPSC master"},
		{"another link's ID", 3132912, "test-server"},
		{"a peer on this network", 3132910, "already a peer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			why := idAlreadyMeansSomethingElse(base(), tc.id)
			if why == "" {
				t.Fatalf("%d was accepted", tc.id)
			}
			if !strings.Contains(why, tc.want) {
				t.Errorf("the refusal is %q, which does not say %q", why, tc.want)
			}
		})
	}

	if why := idAlreadyMeansSomethingElse(base(), 3132913); why != "" {
		t.Errorf("a free ID was refused: %s", why)
	}
}

// The IPSC rule applies only when IPSC is on. A disabled master's ID is not a
// station anybody is talking to.
func TestADisabledIPSCMasterDoesNotReserveItsID(t *testing.T) {
	cfg := config.Config{}
	cfg.IPSC.Enabled = false
	cfg.IPSC.MasterID = 3132911

	if why := idAlreadyMeansSomethingElse(cfg, 3132911); why != "" {
		t.Errorf("a disabled master reserved its ID: %s", why)
	}
}

// **ADR-0035 is why this exists**: a member is removed by deleting one file,
// not by changing everybody's password. An offer that fell back to the shared
// password would give that up, so it settles the directory instead.
func TestOfferingALinkSettlesThePerPeerPasswordDirectory(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.PasswordFile = "/var/lib/qsp/peer.pass"

	dir, changed, err := peerPasswordDirectory(&cfg)
	if err != nil {
		t.Fatalf("peerPasswordDirectory: %v", err)
	}
	if !changed {
		t.Error("the directory was settled but not reported as a change")
	}
	if want := "/var/lib/qsp/peers"; dir != want {
		t.Errorf("the directory is %q, want %q", dir, want)
	}
	if cfg.DMR.PeerPasswords != dir {
		t.Errorf("the configuration was not updated: %q", cfg.DMR.PeerPasswords)
	}

	// Already set: left exactly as the operator has it, and not reported as a
	// change, because a response saying it moved when it did not is worse than
	// saying nothing.
	cfg.DMR.PeerPasswords = "/srv/qsp/creds"
	dir, changed, err = peerPasswordDirectory(&cfg)
	if err != nil {
		t.Fatalf("peerPasswordDirectory: %v", err)
	}
	if changed || dir != "/srv/qsp/creds" {
		t.Errorf("an existing directory was changed to %q (changed=%v)", dir, changed)
	}
}

func TestALinkPasswordIsNotLeftReadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "peers")

	path, err := writePeerPassword(dir, 3132913, "a-password-long-enough-to-be-accepted")
	if err != nil {
		t.Fatalf("writePeerPassword: %v", err)
	}
	if want := filepath.Join(dir, "3132913"); path != want {
		t.Errorf("the password is at %q, want %q", path, want)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("the password file is %v, want 0600", perm)
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat directory: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("the password directory is %v, want 0700", perm)
	}
}

// **The offer form proposed an address no QSP link can reach**, and the accept
// form wrote it: the link path reused the OpenBridge helper, which hardcodes
// 62045 — the port a peering is sent to by prior agreement. A link registers on
// the peer listener, which is the port this server's hotspots already use.
//
// Found on 2026-09-08 by offering a link between two live servers and reading
// the far end off the page afterwards.
func TestAQSPLinkIsOfferedThePortItActuallyDials(t *testing.T) {
	cfg := config.Config{}
	cfg.DMR.Join.Address = "qsp.example.com"
	cfg.DMR.ListenAddress = "0.0.0.0:62031"

	if got, want := defaultQSPLinkAddress(cfg), "qsp.example.com:62031"; got != want {
		t.Errorf("a link is offered %q, want %q", got, want)
	}
	if got := defaultQSPLinkAddress(cfg); got == defaultLinkAddress(cfg) {
		t.Error("a QSP link is offered the OpenBridge port")
	}

	// A server listening somewhere else is offered that port, rather than the
	// one this code would otherwise assume twice.
	cfg.DMR.ListenAddress = "0.0.0.0:62055"
	if got, want := defaultQSPLinkAddress(cfg), "qsp.example.com:62055"; got != want {
		t.Errorf("a server on a nonstandard port offers %q, want %q", got, want)
	}

	// Nothing to guess from is an empty box rather than a wrong address.
	if got := defaultQSPLinkAddress(config.Config{}); got != "" {
		t.Errorf("a server with no join address guessed %q", got)
	}
}
