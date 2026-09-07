package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

func envFrom(pairs map[string]string) func(string) string {
	return func(k string) string { return pairs[k] }
}

// TestAFirstRunWritesAConfigurationThatValidates is the whole point.
//
// A container starts with an empty volume: no configuration, no password file.
// If what QSP writes into it does not validate, the operator's first
// experience is an error message about a file they never touched.
func TestAFirstRunWritesAConfigurationThatValidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")

	written, err := bootstrapConfig(path, envFrom(map[string]string{
		peerPasswordEnv: "a-shared-secret",
		allowedPeersEnv: "3132910, 3155413",
	}))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !written {
		t.Fatal("nothing was written into an empty directory")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer func() { _ = f.Close() }()
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatalf("the configuration QSP wrote does not load: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the configuration QSP wrote does not validate: %v", err)
	}

	// **The database has to be in the volume.** The default DSN is relative
	// and a scratch image has no working directory, so "qsp.db" resolved to
	// /qsp.db in the container's writable layer — every account and every call
	// record discarded on the next rebuild, silently. Found by listing the
	// volume on a machine that had run it.
	if !filepath.IsAbs(cfg.Database.DSN) {
		t.Errorf("the database DSN is %q, which is relative to a working directory "+
			"the container does not have", cfg.Database.DSN)
	}
	if filepath.Dir(cfg.Database.DSN) != dir {
		t.Errorf("the database is at %q, outside the directory holding the configuration",
			cfg.Database.DSN)
	}

	if !cfg.DMR.Enabled {
		t.Error("the Homebrew listener is off, so nothing can connect at all")
	}
	// IP Site Connect needs a negotiated relationship at both ends. An idle
	// listener on 50000 is attack surface with no benefit to somebody who has
	// just installed this.
	if cfg.IPSC.Enabled {
		t.Error("IP Site Connect starts enabled; it should not")
	}
	// Under host networking, 127.0.0.1 means a newcomer sees nothing until
	// they build an SSH tunnel.
	if !strings.HasPrefix(cfg.Server.ListenAddress, "0.0.0.0:") {
		t.Errorf("the console binds to %q, which a newcomer cannot reach",
			cfg.Server.ListenAddress)
	}
}

// TestTheStartingPolicyPermitsOnlyWhatItWasTold is the security property.
//
// **A permit list refuses anything it does not name**, so an instance reachable
// from the internet carries nothing until its operator says what it may carry.
// The alternative — starting open — is a problem for the people it relays to as
// much as for whoever installed it.
func TestTheStartingPolicyPermitsOnlyWhatItWasTold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")

	if _, err := bootstrapConfig(path, envFrom(map[string]string{
		peerPasswordEnv: "a-shared-secret",
		allowedPeersEnv: "3132910",
	})); err != nil {
		t.Fatalf("%v", err)
	}

	f, _ := os.Open(path)
	defer func() { _ = f.Close() }()
	cfg, err := config.Load(f)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if cfg.DMR.Access == nil {
		t.Fatal("no access block was written; the listener would carry everything")
	}
	if got := cfg.DMR.Access.Registration.Mode; got != "permit" {
		t.Errorf("registration mode is %q; a deny list allows what it does not name", got)
	}
	if got := cfg.DMR.Access.Registration.IDs; len(got) != 1 || got[0] != "3132910" {
		t.Errorf("registration permits %v, want only the ID that was supplied", got)
	}
	// **Talkgroups carry everything; registration is the boundary.** Which
	// talkgroups an instance carries is a refinement an operator makes once
	// they know what their members use.
	if got := cfg.DMR.Access.Talkgroups.Timeslot2.Mode; got != "deny" {
		t.Errorf("timeslot 2 talkgroup mode is %q, want a deny list carrying all", got)
	}
}

// TestASecondRunChangesNothing keeps the promise the comment makes.
//
// After the first run the configuration is the operator's, including any
// mistake in it. A program that rewrites what somebody edited is a program
// nobody can configure.
func TestASecondRunChangesNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")
	env := envFrom(map[string]string{
		peerPasswordEnv: "a-shared-secret",
		allowedPeersEnv: "3132910",
	})

	if _, err := bootstrapConfig(path, env); err != nil {
		t.Fatalf("%v", err)
	}
	edited := []byte(`{"version":1,"note":"the operator changed this"}`)
	if err := os.WriteFile(path, edited, 0o600); err != nil {
		t.Fatalf("%v", err)
	}

	written, err := bootstrapConfig(path, envFrom(map[string]string{
		peerPasswordEnv: "a-different-secret",
		allowedPeersEnv: "9999999",
	}))
	if err != nil {
		t.Fatalf("%v", err)
	}
	if written {
		t.Error("a second run reported writing a configuration")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, edited) {
		t.Errorf("the operator's file was rewritten:\n got %s\nwant %s", after, edited)
	}
}

// TestNoPasswordStopsRatherThanGuesses is the refusal.
//
// Generating a password nobody knows would produce a running instance that
// refuses every hotspot, with the cause invisible from the console. A server
// that stops and explains itself is kinder.
func TestNoPasswordStopsRatherThanGuesses(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")

	written, err := bootstrapConfig(path, envFrom(nil))
	if written {
		t.Error("a configuration was written with no password to put in it")
	}
	if !errors.Is(err, errNoPeerPassword) {
		t.Fatalf("got %v, want the first-run refusal", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("a file was left behind by a run that refused to start")
	}

	// The message is the only documentation some operators will ever read.
	var out bytes.Buffer
	explainFirstRun(&out, path)
	for _, must := range []string{path, peerPasswordEnv, allowedPeersEnv, "docker compose", "systemd"} {
		if !strings.Contains(out.String(), must) {
			t.Errorf("the first-run message never mentions %q", must)
		}
	}
}

// TestThePasswordIsNotInTheConfiguration checks where the secret ended up.
//
// Configuration gets pasted into forum posts when somebody asks for help, so
// the password lives in a file of its own that only its owner can read.
func TestThePasswordIsNotInTheConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")
	const secret = "a-shared-secret"

	if _, err := bootstrapConfig(path, envFrom(map[string]string{
		peerPasswordEnv: secret,
		allowedPeersEnv: "3132910",
	})); err != nil {
		t.Fatalf("%v", err)
	}

	written, _ := os.ReadFile(path)
	if bytes.Contains(written, []byte(secret)) {
		t.Error("the peer password was written into the configuration file")
	}

	passwordPath := filepath.Join(dir, "peer-password")
	info, err := os.Stat(passwordPath)
	if err != nil {
		t.Fatalf("no password file was written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the password file is mode %04o, want 0600", perm)
	}
	got, _ := os.ReadFile(passwordPath)
	if strings.TrimSpace(string(got)) != secret {
		t.Error("the password file does not contain the password")
	}
}

// TestBootstrapDoesNothingWithoutAConfigPath keeps QSP's existing behaviour.
//
// Run with no -config, QSP uses built-in defaults. Writing a file somebody did
// not ask for would be a surprise rather than a convenience.
func TestBootstrapDoesNothingWithoutAConfigPath(t *testing.T) {
	written, err := bootstrapConfig("", envFrom(map[string]string{
		peerPasswordEnv: "a-shared-secret",
		allowedPeersEnv: "3132910",
	}))
	if written || err != nil {
		t.Errorf("got written=%v err=%v; want nothing done", written, err)
	}
}

// TestNoAllowedPeersStopsRatherThanOpening is the other refusal, and it was not
// in the plan.
//
// The intention was to write an empty permit list so a fresh instance carried
// nothing until its operator filled it in. **The validator refuses that**: a
// permit list with no entries refuses every station, and it treats writing one
// as a mistake rather than a policy.
//
// Being made to ask is the better outcome. An operator states who may connect
// before anything is listening, instead of starting a server that is silently
// useless and discovering it when a hotspot will not register.
func TestNoAllowedPeersStopsRatherThanOpening(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")

	written, err := bootstrapConfig(path, envFrom(map[string]string{
		peerPasswordEnv: "a-shared-secret",
	}))
	if written {
		t.Error("a configuration was written with no access policy")
	}
	if !errors.Is(err, errNoAllowedPeers) {
		t.Fatalf("got %v, want the refusal to start an open master", err)
	}
	// Nothing half-made: no password file either, so a second attempt starts
	// from the same place rather than from a partial one.
	if _, err := os.Stat(filepath.Join(dir, "peer-password")); !os.IsNotExist(err) {
		t.Error("a password file was left behind by a run that refused to start")
	}
}

// TestARefusedFirstRunLeavesNothingBehind is the defect an out-of-range ID
// exposed on a clean machine.
//
// The password file was written before the configuration was validated, so a
// refusal left `peer-password` sitting in the volume with no configuration
// beside it. **A half-made state that survives a refusal is worse than the
// refusal**, because the next attempt starts from somewhere nobody chose — and
// the second run would then find a password file it did not write and a
// configuration that still does not exist.
func TestARefusedFirstRunLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")

	// A subscriber ID is 24 bits. 313291001 is what an operator types when
	// told a hotspot appends a two-digit suffix, and it overflows the field —
	// which is exactly how this was found.
	written, err := bootstrapConfig(path, envFrom(map[string]string{
		peerPasswordEnv: "a-shared-secret",
		allowedPeersEnv: "313291001",
	}))
	if written {
		t.Error("a configuration was written from settings that do not validate")
	}
	if err == nil {
		t.Fatal("an out-of-range ID was accepted")
	}

	for _, name := range []string{"qsp.json", "peer-password"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s was left behind by a run that refused to start", name)
		}
	}
}

// TestTheExampleIDsAreOnesThatCanRegister checks the guidance against the
// field it goes into.
//
// **A version of this taught 313291001**, because QSP prints a startup advisory
// about seven-digit IDs and that advisory was read as ground truth over a
// running network. The network is the evidence: 3132910, 3155413 and 3127045
// are all registered and passing traffic on the author's instance. An advisory
// is a prompt to check, not an error.
func TestTheExampleIDsAreOnesThatCanRegister(t *testing.T) {
	b, err := os.ReadFile("../../deploy/docker/.env.example")
	if err != nil {
		t.Fatalf("%v", err)
	}
	example := string(b)

	var value string
	for _, line := range strings.Split(example, "\n") {
		if rest, ok := strings.CutPrefix(line, allowedPeersEnv+"="); ok {
			value = rest
		}
	}
	if value == "" {
		t.Fatalf(".env.example sets no %s", allowedPeersEnv)
	}

	// The shipped example must produce a configuration that validates, or the
	// first thing a new operator does is fail.
	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")
	if _, err := bootstrapConfig(path, envFrom(map[string]string{
		peerPasswordEnv: "a-shared-secret",
		allowedPeersEnv: value,
	})); err != nil {
		t.Fatalf(".env.example ships %s=%s, which does not produce a valid "+
			"configuration: %v", allowedPeersEnv, value, err)
	}
}
