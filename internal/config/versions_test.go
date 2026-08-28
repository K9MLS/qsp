package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// The configuration writer. See ADR-0027: the file is the source of truth, so
// what happens to it when a save goes wrong is the interesting part.

func writable(t *testing.T) (string, *config.Writer) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qsp.json")

	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := config.Save(f, config.Default()); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	_ = f.Close()

	w, err := config.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return path, w
}

func TestSaveReplacesTheFile(t *testing.T) {
	path, w := writable(t)

	cfg := config.Default()
	cfg.Events.HistorySize = 512
	if err := w.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()

	got, err := config.Load(f)
	if err != nil {
		t.Fatalf("the saved configuration does not load again: %v", err)
	}
	if got.Events.HistorySize != 512 {
		t.Errorf("history size is %d, want 512", got.Events.HistorySize)
	}
}

// TestSaveLeavesNoTemporaryFiles. A directory slowly filling with half-written
// configurations is its own problem, and the one left behind by a failure is
// the one somebody eventually mistakes for the real file.
func TestSaveLeavesNoTemporaryFiles(t *testing.T) {
	path, w := writable(t)
	dir := filepath.Dir(path)

	if err := w.Save(config.Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// And after a refused save, too.
	bad := config.Default()
	bad.Events.HistorySize = -1
	if err := w.Save(bad); err == nil {
		t.Fatal("an invalid configuration was written")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".qsp-") {
			t.Errorf("a temporary file was left behind: %s", e.Name())
		}
	}
}

// TestAnInvalidConfigurationIsNeverWritten. Writing something that cannot be
// loaded again strands the operator at the next restart, with a service that
// will not start and a file they did not knowingly break.
func TestAnInvalidConfigurationIsNeverWritten(t *testing.T) {
	path, w := writable(t)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}

	bad := config.Default()
	bad.Server.ListenAddress = ""
	if err := w.Save(bad); err == nil {
		t.Fatal("an invalid configuration was saved")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if string(before) != string(after) {
		t.Error("a refused save changed the file anyway")
	}
}

// TestSaveKeepsTheFileMode. A configuration that was 0600 must not become
// world-readable because somebody pressed save.
func TestSaveKeepsTheFileMode(t *testing.T) {
	path, w := writable(t)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if err := w.Save(config.Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode became %v, want 0600", perm)
	}
}

// TestWritableRefusesAReadOnlyDirectory is what lets the console say it is
// read-only up front, rather than after somebody has filled in a form.
func TestWritableRefusesAReadOnlyDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		// Root ignores the permission bits, so the check would pass and prove
		// nothing about the case it exists for.
		t.Skip("running as root; directory permissions are not enforced")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "qsp.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	w, err := config.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	err = w.Writable()
	if err == nil {
		t.Fatal("a read-only directory reported as writable")
	}
	if !errors.Is(err, config.ErrNotWritable) {
		t.Errorf("want ErrNotWritable, got %v", err)
	}
	// The message must name the directory: the write is a rename, so a
	// writable file in an unwritable directory still cannot be saved, and that
	// is not obvious.
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the error does not name the directory: %v", err)
	}
}

func TestWritableAcceptsAWritableFile(t *testing.T) {
	_, w := writable(t)
	if err := w.Writable(); err != nil {
		t.Errorf("a writable file reported as not: %v", err)
	}
}

// TestNoConfigFileMeansNoSaving. An instance started without -config is running
// on defaults; saving would have to invent a location, and a file appearing
// somewhere the operator never chose is worse than a refusal.
func TestNoConfigFileMeansNoSaving(t *testing.T) {
	_, err := config.NewWriter("")
	if err == nil {
		t.Fatal("a writer was built with no path")
	}
	if !errors.Is(err, config.ErrNotWritable) {
		t.Errorf("want ErrNotWritable, got %v", err)
	}
	if !strings.Contains(err.Error(), "-config") {
		t.Errorf("the error should say how to fix it: %v", err)
	}
}

// TestNeedsRestartNamesFields. "Restart required" tells an operator to
// interrupt their network without saying what for, and they will reasonably
// want to know whether it can wait until the net is over.
func TestNeedsRestartNamesFields(t *testing.T) {
	base := config.Default()

	for _, tc := range []struct {
		name   string
		change func(*config.Config)
		field  string
	}{
		{"listen address", func(c *config.Config) { c.Server.ListenAddress = "0.0.0.0:9000" },
			"server.listen_address"},
		{"dmr listen address", func(c *config.Config) { c.DMR.ListenAddress = "0.0.0.0:62032" },
			"dmr.listen_address"},
		{"dmr enabled", func(c *config.Config) { c.DMR.Enabled = !c.DMR.Enabled },
			"dmr.enabled"},
		{"password file", func(c *config.Config) { c.DMR.PasswordFile = "/other" },
			"dmr.password_file"},
		{"database dsn", func(c *config.Config) { c.Database.DSN = "other.db" },
			"database.dsn"},
		{"logging level", func(c *config.Config) { c.Logging.Level = "debug" },
			"logging.level"},
		{"behind proxy", func(c *config.Config) { c.Server.BehindProxy = true },
			"server.behind_proxy"},
		{"an upstream", func(c *config.Config) {
			c.DMR.Upstreams = []config.Upstream{{Name: "x"}}
		}, "dmr.upstreams"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := base
			tc.change(&after)

			fields := config.NeedsRestart(base, after)
			var found bool
			for _, f := range fields {
				if f == tc.field {
					found = true
				}
			}
			if !found {
				t.Errorf("changing %s gave %v, want it to name %s", tc.name, fields, tc.field)
			}
		})
	}
}

// TestLiveChangesNeedNoRestart is the other half, and the one that matters
// daily: adding a bridge must not tell a club to restart mid-net.
func TestLiveChangesNeedNoRestart(t *testing.T) {
	base := config.Default()

	for _, tc := range []struct {
		name   string
		change func(*config.Config)
	}{
		{"a bridge", func(c *config.Config) {
			c.DMR.Bridges = []config.Bridge{{Name: "net", Enabled: true}}
		}},
		{"the schedule", func(c *config.Config) {
			c.DMR.Schedule = []config.Window{{Bridge: "net", Days: []int{2}, Start: "20:00"}}
		}},
		{"access lists", func(c *config.Config) {
			c.DMR.Access = &config.Access{Registration: config.ACL{Mode: "deny"}}
		}},
		{"subscription", func(c *config.Config) {
			c.DMR.Subscription.Enabled = true
		}},
		{"the join page", func(c *config.Config) {
			c.DMR.Join.NetworkName = "BCARA"
		}},
		{"the map", func(c *config.Config) {
			c.Server.Map.TileURL = ""
		}},
		{"forwarding", func(c *config.Config) { c.DMR.Forwarding = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := base
			tc.change(&after)
			if fields := config.NeedsRestart(base, after); len(fields) != 0 {
				t.Errorf("changing %s asked for a restart: %v", tc.name, fields)
			}
		})
	}
}

func TestAnUnchangedConfigurationNeedsNoRestart(t *testing.T) {
	base := config.Default()
	if fields := config.NeedsRestart(base, base); len(fields) != 0 {
		t.Errorf("an unchanged configuration asked for a restart: %v", fields)
	}
}

// TestUpstreamComparisonSeesEveryField. Comparing links by encoding rather than
// field by field means a field added to Upstream later is included without
// anybody remembering to add it here — which is the failure this guards.
func TestUpstreamComparisonSeesEveryField(t *testing.T) {
	base := config.Default()
	base.DMR.Upstreams = []config.Upstream{{
		Name: "bm", Address: "a:62035", NetworkID: 1, PassphraseFile: "/p",
	}}

	for _, change := range []func(*config.Upstream){
		func(u *config.Upstream) { u.Address = "b:62035" },
		func(u *config.Upstream) { u.NetworkID = 2 },
		func(u *config.Upstream) { u.PassphraseFile = "/q" },
		func(u *config.Upstream) { u.Enabled = true },
		func(u *config.Upstream) { u.Protocol = "homebrew" },
		func(u *config.Upstream) { u.StaleAfter = config.Duration(1) },
		func(u *config.Upstream) {
			u.Export = []config.UpstreamTalkgroup{{Talkgroup: 9, Timeslot: 2}}
		},
	} {
		after := base
		after.DMR.Upstreams = []config.Upstream{base.DMR.Upstreams[0]}
		change(&after.DMR.Upstreams[0])

		if fields := config.NeedsRestart(base, after); len(fields) == 0 {
			t.Errorf("a change to an upstream went unnoticed: %+v", after.DMR.Upstreams[0])
		}
	}
}

// TestWritableRefusesAMissingDirectory covers the same refusal as the
// read-only case above, in a way root cannot bypass — so it runs in
// containers, where the permission test skips and proves nothing.
func TestWritableRefusesAMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-directory", "qsp.json")

	w, err := config.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	err = w.Writable()
	if err == nil {
		t.Fatal("a path in a directory that does not exist reported as writable")
	}
	if !errors.Is(err, config.ErrNotWritable) {
		t.Errorf("want ErrNotWritable, got %v", err)
	}
}

// TestSaveFailsLoudlyOnAMissingDirectory. A save that reported success and
// wrote nothing is the worst outcome available here.
func TestSaveFailsLoudlyOnAMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-such-directory", "qsp.json")
	w, err := config.NewWriter(path)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	if err := w.Save(config.Default()); err == nil {
		t.Fatal("Save reported success with nowhere to write")
	}
	if _, err := os.Stat(path); err == nil {
		t.Error("a file appeared where the directory does not exist")
	}
}
