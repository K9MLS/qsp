package config_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/config"
)

// aServer is a configuration with every kind of secret a backup cannot carry.
func aServer(t *testing.T) config.Config {
	t.Helper()

	cfg := config.Default()
	cfg.Server.Identifier = strings.Repeat("0f", 16)
	cfg.DMR.Enabled = true
	cfg.DMR.PasswordFile = "/var/lib/qsp/peer.pass"
	cfg.DMR.PeerPasswords = "/var/lib/qsp/peers"
	cfg.DMR.Identity.Callsign = "K9MLS"
	// A listener reachable from beyond the host must say what it accepts, so
	// the fixture is a configuration that would actually run.
	cfg.DMR.Access = &config.Access{}
	cfg.DMR.Access.Registration = config.ACL{Mode: "deny", IDs: []string{}}
	cfg.DMR.Upstreams = []config.Upstream{
		{
			Name: "kd9eja-01", Protocol: config.UpstreamQSP, Enabled: true,
			Address: "qsp.example.com:62031", RepeaterID: 3132913,
			PasswordFile: "/var/lib/qsp/kd9eja-01.pass",
			Identity:     &config.UpstreamIdentity{Callsign: "K9MLS"},
		},
	}
	return cfg
}

// **The whole point of ADR-0054.** A backup that carried secrets would stop
// being safe to email, keep in a repository, or hand to somebody helping —
// which is what it exists for.
func TestABackupCarriesNoSecret(t *testing.T) {
	cfg := aServer(t)

	var out bytes.Buffer
	if err := config.WriteBackup(&out, config.NewBackup(cfg, "0.1.148", time.Now())); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}
	written := out.String()

	// Paths are fine — they are where a secret lives, not the secret. A value
	// is not.
	for _, secret := range []string{"bcham2026", "hunter2", "passphrase\":\"", "password\":\""} {
		if strings.Contains(written, secret) {
			t.Errorf("the export contains something that looks like a secret: %q", secret)
		}
	}
	if !strings.Contains(written, "/var/lib/qsp/peer.pass") {
		t.Error("the export does not name where the peer password lives, so a restore " +
			"cannot tell an operator what to reissue")
	}
}

// **A restore needs a checklist, not a mystery.** A naively restored server
// comes up with every link configured, every link unable to log in, and a page
// reporting them as configured and not open — which reads as a network fault.
func TestABackupNamesEveryCredentialItCannotCarry(t *testing.T) {
	missing := config.MissingCredentials(aServer(t))

	if len(missing) != 3 {
		t.Fatalf("the export names %d missing credentials, want 3: the shared password, "+
			"the per-member directory, and the link", len(missing))
	}

	var links, peers int
	for _, m := range missing {
		if m.Path == "" {
			t.Errorf("%q does not say where its credential lived", m.Name)
		}
		if m.Fix == "" {
			t.Errorf("%q does not say what to do about it", m.Name)
		}
		switch m.Kind {
		case "link":
			links++
		case "peers":
			peers++
		default:
			t.Errorf("%q has kind %q, which nothing renders", m.Name, m.Kind)
		}
	}
	if links != 1 || peers != 2 {
		t.Errorf("the export names %d links and %d peer credentials, want 1 and 2", links, peers)
	}
}

// **A stable order, so a diff between two backups shows what changed rather
// than what moved.** The first attempt at breaking this test reversed the list
// and the test still passed, which meant the ordering the code claims was
// asserted nowhere.
func TestTheChecklistIsInAStableOrder(t *testing.T) {
	cfg := aServer(t)
	cfg.DMR.Upstreams = append(cfg.DMR.Upstreams, config.Upstream{
		Name: "aardvark", Protocol: config.UpstreamQSP, Enabled: true,
		Address: "a.example.com:62031", RepeaterID: 3132914,
		PasswordFile: "/var/lib/qsp/aardvark.pass",
		Identity:     &config.UpstreamIdentity{Callsign: "K9MLS"},
	})

	missing := config.MissingCredentials(cfg)
	for i := 1; i < len(missing); i++ {
		prev, cur := missing[i-1], missing[i]
		if prev.Kind > cur.Kind {
			t.Fatalf("kinds are out of order: %q before %q", prev.Kind, cur.Kind)
		}
		if prev.Kind == cur.Kind && prev.Name > cur.Name {
			t.Fatalf("names are out of order within %q: %q before %q",
				cur.Kind, prev.Name, cur.Name)
		}
	}
	if len(missing) < 4 {
		t.Fatalf("only %d entries; this test needs two links to order", len(missing))
	}
}

// A configuration naming no secret produces no checklist, rather than a list of
// things that do not exist.
func TestAServerWithNoCredentialsNamesNone(t *testing.T) {
	cfg := config.Default()
	cfg.DMR.PasswordFile = ""

	if missing := config.MissingCredentials(cfg); len(missing) != 0 {
		t.Errorf("a server with no credentials named %d: %+v", len(missing), missing)
	}
}

func TestABackupSurvivesTheRoundTrip(t *testing.T) {
	cfg := aServer(t)
	sent := config.NewBackup(cfg, "0.1.148", time.Now())

	var out bytes.Buffer
	if err := config.WriteBackup(&out, sent); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}
	got, err := config.ReadBackup(&out)
	if err != nil {
		t.Fatalf("ReadBackup: %v", err)
	}

	if got.Identifier != sent.Identifier {
		t.Errorf("the identifier changed: %q became %q", sent.Identifier, got.Identifier)
	}
	if len(got.Config.DMR.Upstreams) != 1 {
		t.Fatalf("the restored configuration has %d links, want 1",
			len(got.Config.DMR.Upstreams))
	}
	if got.Config.DMR.Upstreams[0].Name != "kd9eja-01" {
		t.Errorf("the link is %q, want kd9eja-01", got.Config.DMR.Upstreams[0].Name)
	}
	if len(got.Missing) != len(sent.Missing) {
		t.Errorf("the checklist changed in transit: %d became %d",
			len(sent.Missing), len(got.Missing))
	}
}

// **Importing three-quarters of a configuration is worse than importing none**:
// the missing quarter is invisible, and the operator believes they restored a
// server.
func TestABackupFromANewerQSPIsRefusedOutright(t *testing.T) {
	var out bytes.Buffer
	b := config.NewBackup(aServer(t), "9.9.9", time.Now())
	b.Format = config.BackupVersion + 1
	if err := config.WriteBackup(&out, b); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}

	_, err := config.ReadBackup(&out)
	if !errors.Is(err, config.ErrBackupNewer) {
		t.Fatalf("error is %v, want ErrBackupNewer", err)
	}
	// Both versions named, or an operator cannot tell what to upgrade.
	if !strings.Contains(err.Error(), "upgrade QSP") {
		t.Errorf("the refusal does not say what to do: %v", err)
	}
}

func TestSomethingThatIsNotABackupIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"a configuration on its own", `{"version":1,"server":{}}`},
		{"nothing", ``},
		{"not JSON", `hello`},
		{"an object with no format", `{"config":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := config.ReadBackup(strings.NewReader(tc.body)); err == nil {
				t.Error("it was accepted as a backup")
			}
		})
	}
}

// Two exports of one configuration must be the same bytes, or a diff between
// backups shows what moved rather than what changed.
func TestTwoExportsOfOneServerAgree(t *testing.T) {
	cfg := aServer(t)
	at := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)

	var first, second bytes.Buffer
	if err := config.WriteBackup(&first, config.NewBackup(cfg, "0.1.148", at)); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}
	if err := config.WriteBackup(&second, config.NewBackup(cfg, "0.1.148", at)); err != nil {
		t.Fatalf("WriteBackup: %v", err)
	}
	if first.String() != second.String() {
		t.Error("two exports of one configuration differ")
	}
}
