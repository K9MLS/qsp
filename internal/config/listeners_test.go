package config

import (
	"strings"
	"testing"
)

// TestTwoLinksCannotShareAPort is the defect a bug hunt found and every gate
// missed.
//
// Upstream names were checked for duplicates and listen addresses were not.
// Accepting two peerings without restarting in between — taking the page's own
// suggested 0.0.0.0:62045 both times — wrote two links on one port. The accept
// handler's bind probe could not catch it, because an accepted link opens no
// socket until a restart. `-check` could not, because it binds each address and
// closes it before trying the next. Startup caught it by refusing to start, and
// systemd crash-looped to its start limit.
func TestTwoLinksCannotShareAPort(t *testing.T) {
	cfg := Default()
	cfg.DMR.Upstreams = []Upstream{
		{Name: "first", Enabled: true, Address: "a.example.com:62045",
			ListenAddress: "0.0.0.0:62045", NetworkID: 1, PassphraseFile: "/tmp/a.pass"},
		{Name: "second", Enabled: true, Address: "b.example.com:62045",
			ListenAddress: "0.0.0.0:62045", NetworkID: 2, PassphraseFile: "/tmp/b.pass"},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("two links on one port validated; qsp will refuse to start on it")
	}
	if !strings.Contains(err.Error(), "dmr.upstreams[1].listen_address") {
		t.Errorf("the error does not name the link the operator can still change: %v", err)
	}
}

// TestEveryInterfaceCollidesWithASpecificAddress is the half that is easy to
// get wrong.
//
// 0.0.0.0 is every interface on this machine, so it includes 192.168.1.27 on
// the same port. Comparing hosts for equality would let this pair validate and
// then fight at bind time, which is the failure the check exists to prevent.
func TestEveryInterfaceCollidesWithASpecificAddress(t *testing.T) {
	cfg := Default()
	cfg.DMR.Enabled = true
	cfg.DMR.ListenAddress = "0.0.0.0:62031"
	cfg.DMR.Upstreams = []Upstream{{
		Name: "clash", Enabled: true, Address: "far.example.com:62045",
		ListenAddress: "192.168.1.27:62031", NetworkID: 1, PassphraseFile: "/tmp/a.pass",
	}}

	// **Asserted on the collision message, not on err != nil.** This
	// configuration is invalid for unrelated reasons — no password file, no
	// bridge — so a bare nil check passes whether or not the rule works, which
	// is exactly how a test comes to be believed for the wrong reason.
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "already bound by") {
		t.Fatalf("a link took the DMR listener's port on a specific address "+
			"and no collision was reported: %v", err)
	}
}

// TestDifferentPortsAndProtocolsDoNotCollide keeps the rule from being one
// nobody can satisfy. The console is TCP and the listeners are UDP, so they
// share port numbers harmlessly, and a working configuration must stay valid.
func TestDifferentPortsAndProtocolsDoNotCollide(t *testing.T) {
	cfg := Default()
	cfg.Server.ListenAddress = "0.0.0.0:62031" // TCP
	cfg.DMR.Enabled = true
	cfg.DMR.ListenAddress = "0.0.0.0:62031" // UDP
	cfg.DMR.Upstreams = []Upstream{
		{Name: "one", Enabled: true, Address: "a.example.com:62045",
			ListenAddress: "0.0.0.0:62045", NetworkID: 1, PassphraseFile: "/tmp/a.pass"},
		{Name: "two", Enabled: true, Address: "b.example.com:62046",
			ListenAddress: "0.0.0.0:62046", NetworkID: 2, PassphraseFile: "/tmp/b.pass"},
		// Disabled, and a homebrew link binds nothing at all: neither may
		// produce a collision an operator cannot act on.
		{Name: "off", Enabled: false, ListenAddress: "0.0.0.0:62045"},
		{Name: "hb", Enabled: true, Protocol: UpstreamHomebrew,
			Address: "c.example.com:62031", ListenAddress: "0.0.0.0:62045",
			NetworkID: 3, PasswordFile: "/tmp/c.pass", Identity: &UpstreamIdentity{Callsign: "K9MLS"}},
	}

	// Asserted on the collision specifically. This configuration is
	// incomplete in other ways — no password file, no bridges — and checking
	// Validate for nil would make this test fail for reasons that have nothing
	// to do with ports.
	if err := cfg.Validate(); err != nil && strings.Contains(err.Error(), "already bound by") {
		t.Fatalf("a working set of listeners was reported as colliding: %v", err)
	}
}

// TestALinkNameCannotBecomeAPathOutsideTheDataDirectory.
//
// The name becomes filepath.Join(dir, name+".pass") when a peering is accepted,
// and that path is deleted when the link is removed. Nothing constrained it.
func TestALinkNameCannotBecomeAPathOutsideTheDataDirectory(t *testing.T) {
	for _, name := range []string{
		"../../etc/qsp",
		"a/b",
		`a\b`,
		"..",
		strings.Repeat("x", MaxUpstreamNameLength+1),
		"",
	} {
		if err := ValidUpstreamName(name); err == nil {
			t.Errorf("%q was accepted as a link name and becomes a file path", name)
		}
	}
	for _, name := range []string{"cameron", "Test Server", "kb9tyc-cameron", "test_2"} {
		if err := ValidUpstreamName(name); err != nil {
			t.Errorf("%q is a name somebody would choose and was refused: %v", name, err)
		}
	}
}

// TestListenersAreNotListedTwice. cmd/qsp built its own copy of this list to
// probe with; two lists of the same thing drift, which is the shape of most of
// this project's defects.
func TestListenersAreNotListedTwice(t *testing.T) {
	cfg := Default()
	cfg.DMR.Enabled = true
	cfg.IPSC.Enabled = true
	if strings.TrimSpace(cfg.IPSC.ListenAddress) == "" {
		cfg.IPSC.ListenAddress = "0.0.0.0:50000"
	}
	cfg.DMR.Upstreams = []Upstream{{
		Name: "one", Enabled: true, ListenAddress: "0.0.0.0:62045",
	}}

	var fields []string
	for _, l := range cfg.Listeners() {
		fields = append(fields, l.Field)
	}
	for _, want := range []string{
		"server.listen_address", "dmr.listen_address", "ipsc.listen_address",
		"dmr.upstreams[0].listen_address",
	} {
		var seen bool
		for _, f := range fields {
			if f == want {
				seen = true
			}
		}
		if !seen {
			t.Errorf("%s is bound at startup and is not in Listeners(): %v", want, fields)
		}
	}
}
