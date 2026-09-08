package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/config"
	"github.com/k9mls/qsp/internal/health"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// testConfig returns a configuration safe to build an app from.
//
// The DSN is redirected into the test's temporary directory. config.Default
// points at a relative "qsp.db", and now that a driver is registered every test
// calling this would otherwise create a real database beside the source and
// leak state between runs.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Server.ListenAddress = "127.0.0.1:0"
	cfg.Database.DSN = filepath.Join(t.TempDir(), "qsp.db")
	return cfg
}

// testConfigNoDriver asks for a driver that cannot exist, to exercise the
// path where persistence is absent. Naming a bogus driver is more honest than
// removing the real one, because it tests the code that runs when an operator
// configures something this binary was not built with.
func testConfigNoDriver(t *testing.T) config.Config {
	t.Helper()
	cfg := testConfig(t)
	cfg.Database.Driver = "no-such-driver"
	return cfg
}

func TestBuildSucceedsWithoutADatabaseDriver(t *testing.T) {
	// Constitution §3: an absent capability says so rather than failing or
	// pretending. This binary registers sqlite, so the case is reached by
	// configuring a driver that does not exist — which is exactly what an
	// operator pointing at postgres would hit.
	a, err := build(context.Background(), testConfigNoDriver(t), "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() {
		if err := a.shutdown(context.Background()); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	}()

	if a.db != nil {
		t.Error("a database handle was created despite no driver being registered")
	}
	if a.srv == nil {
		t.Error("no server was constructed")
	}
	if a.bus == nil {
		t.Error("no event bus was constructed")
	}
}

func TestHealthReportsUnbuiltSubsystemsHonestly(t *testing.T) {
	a, err := build(context.Background(), testConfig(t), "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	if err := a.srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	resp, err := http.Get("http://" + a.srv.Address() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var report health.Report
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decoding report: %v", err)
	}

	byName := make(map[string]health.Result, len(report.Results))
	for _, r := range report.Results {
		byName[r.Name] = r
	}

	// Every subsystem the blueprint names must appear, so an operator can see
	// what exists and what does not.
	for _, name := range []string{
		"process", "database", "network", "peers", "routing", "scheduler",
		"ipsc", "p25", "vocoder", "allstar", "zello", "echolink",
	} {
		if _, ok := byName[name]; !ok {
			t.Errorf("subsystem %q is missing from the health report", name)
		}
	}

	// A subsystem that genuinely does not exist yet says which phase brings it.
	// Routing and the scheduler are built, so they are not in this list; their
	// checks describe the instance instead.
	for _, name := range []string{"p25", "vocoder", "allstar", "zello", "echolink"} {
		got := byName[name]
		if got.Status != health.StatusUnavailable {
			t.Errorf("unbuilt subsystem %q reports %q, want %q", name, got.Status, health.StatusUnavailable)
		}
		if !strings.Contains(got.Summary, "phase") {
			t.Errorf("subsystem %q does not say when it arrives: %q", name, got.Summary)
		}
	}

	// Built-but-inactive subsystems name the setting that would turn them on,
	// which is a different statement from "not implemented".
	for _, name := range []string{"routing", "scheduler"} {
		got := byName[name]
		if got.Status != health.StatusUnavailable {
			t.Errorf("inactive subsystem %q reports %q, want %q", name, got.Status, health.StatusUnavailable)
		}
		if strings.Contains(got.Summary, "phase") {
			t.Errorf("built subsystem %q still quotes the phase plan: %q", name, got.Summary)
		}
	}

	// The DMR listener is built but off by default: a fresh install must not
	// start accepting connections before an operator decides it should. It
	// reports unavailable with the setting that would enable it, which is a
	// different statement from "not implemented".
	for _, name := range []string{"network", "peers"} {
		got := byName[name]
		if got.Status != health.StatusUnavailable {
			t.Errorf("disabled subsystem %q reports %q, want %q", name, got.Status, health.StatusUnavailable)
		}
		if !strings.Contains(got.Summary, "dmr.enabled") {
			t.Errorf("subsystem %q does not name the setting that enables it: %q", name, got.Summary)
		}
	}

	// The database is built and configured, so it reports healthy. Its absence
	// is covered by TestBuildSucceedsWithoutADatabaseDriver and
	// TestHealthReportsAnAbsentDriverHonestly.
	if byName["database"].Status == health.StatusUnavailable {
		t.Errorf("database reports unavailable despite a registered driver: %q",
			byName["database"].Summary)
	}

	if byName["process"].Status != health.StatusHealthy {
		t.Errorf("process reports %q, want %q", byName["process"].Status, health.StatusHealthy)
	}

	// Unavailable subsystems must not make a working instance look unwell.
	if report.Status != health.StatusHealthy {
		t.Errorf("overall status = %q, want %q", report.Status, health.StatusHealthy)
	}
	if !report.Ready() {
		t.Error("the instance reports itself not ready")
	}
}

func TestConsoleIsServedFromEmbeddedAssets(t *testing.T) {
	a, err := build(context.Background(), testConfig(t), "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	if err := a.srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	for _, path := range []string{"/", "/tokens.css", "/console.css", "/console.js", "/mark.svg"} {
		resp, err := http.Get("http://" + a.srv.Address() + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if err := resp.Body.Close(); err != nil {
			t.Errorf("closing body for %s: %v", path, err)
		}
	}
}

func TestRunStopsOnContextCancellation(t *testing.T) {
	a, err := build(context.Background(), testConfig(t), "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.run(ctx) }()

	// Allow the listener to bind before cancelling.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after its context was cancelled")
	}

	if err := a.shutdown(context.Background()); err != nil {
		t.Errorf("shutdown: %v", err)
	}
}

func TestShutdownIsCleanWithoutStart(t *testing.T) {
	a, err := build(context.Background(), testConfig(t), "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := a.shutdown(context.Background()); err != nil {
		t.Errorf("shutdown on an unstarted app returned %v, want nil", err)
	}
}

func TestBuildVersionAlwaysReturnsSomething(t *testing.T) {
	if buildVersion() == "" {
		t.Error("buildVersion returned an empty string")
	}
}

func TestLoadConfigDefaultsWithoutAPath(t *testing.T) {
	cfg, err := loadConfig("")
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if !reflect.DeepEqual(cfg, config.Default()) {
		t.Error("loadConfig(\"\") did not return the defaults")
	}
}

func TestLoadConfigReportsMissingFileClearly(t *testing.T) {
	_, err := loadConfig("/nonexistent/qsp.json")
	if err == nil {
		t.Fatal("expected an error for a missing configuration file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/qsp.json") {
		t.Errorf("error should name the path, got: %v", err)
	}
}

func TestDMRListenerStartsWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	passwordFile := filepath.Join(dir, "peer.pass")
	if err := os.WriteFile(passwordFile, []byte("  a-shared-peer-password\n"), 0o600); err != nil {
		t.Fatalf("writing the password file: %v", err)
	}

	cfg := testConfig(t)
	cfg.DMR.Enabled = true
	cfg.DMR.ListenAddress = "127.0.0.1:0"
	cfg.DMR.PasswordFile = passwordFile

	a, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if a.dmr == nil {
		t.Fatal("no DMR listener was constructed with dmr.enabled set")
	}
	if err := a.dmr.Start(ctx); err != nil {
		t.Fatalf("starting the DMR listener: %v", err)
	}

	if err := a.srv.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	resp, err := http.Get("http://" + a.srv.Address() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var report health.Report
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("decoding report: %v", err)
	}
	for _, r := range report.Results {
		if r.Name == "network" {
			if r.Status != health.StatusHealthy {
				t.Errorf("network reports %q, want healthy: %s", r.Status, r.Summary)
			}
			if r.Detail["address"] == "" {
				t.Error("network health omits the bound address")
			}
		}
	}
}

func TestDMREnabledWithoutAPasswordFileIsFatal(t *testing.T) {
	// A master that authenticates nobody is worse than no master at all, so
	// this must stop startup rather than silently disable the listener.
	cfg := testConfig(t)
	cfg.DMR.Enabled = true
	cfg.DMR.PasswordFile = filepath.Join(t.TempDir(), "does-not-exist")

	if _, err := build(context.Background(), cfg, "", logging.Discard()); err == nil {
		t.Fatal("startup succeeded with an unreadable peer password file")
	}
}

func TestDMREnabledWithAnEmptyPasswordFileIsFatal(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "peer.pass")
	if err := os.WriteFile(empty, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("writing: %v", err)
	}
	cfg := testConfig(t)
	cfg.DMR.Enabled = true
	cfg.DMR.PasswordFile = empty

	_, err := build(context.Background(), cfg, "", logging.Discard())
	if err == nil {
		t.Fatal("startup succeeded with an empty peer password file")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Errorf("error should say the file is empty, got: %v", err)
	}
}

func TestDisplayAddrUnmapsIPv4(t *testing.T) {
	// A socket on the IPv6 wildcard reports IPv4 clients as IPv4-mapped, so the
	// same hotspot would otherwise appear two different ways depending on how
	// the listener happened to bind.
	mapped := netip.MustParseAddrPort("[::ffff:192.168.1.155]:46458")
	if got := displayAddr(mapped); got != "192.168.1.155:46458" {
		t.Errorf("displayAddr(mapped) = %q, want 192.168.1.155:46458", got)
	}
	plain := netip.MustParseAddrPort("192.168.1.155:46458")
	if got := displayAddr(plain); got != "192.168.1.155:46458" {
		t.Errorf("displayAddr(plain) = %q", got)
	}
	v6 := netip.MustParseAddrPort("[2001:db8::1]:62031")
	if got := displayAddr(v6); got != "[2001:db8::1]:62031" {
		t.Errorf("displayAddr(v6) = %q; a genuine IPv6 address must be preserved", got)
	}
}

// TestHealthSummariesDescribeTheInstanceNotAPlan.
//
// Three summaries once said a subsystem "arrives in phase N" long after it had
// arrived. A health report that quotes a roadmap rather than the running
// instance is the same failure as fake data in slower motion, so this asserts
// the summaries talk about configuration instead.
func TestHealthSummariesDescribeTheInstanceNotAPlan(t *testing.T) {
	run := func(t *testing.T, mutate func(*config.Config)) map[string]health.Result {
		t.Helper()
		cfg := testConfig(t)
		mutate(&cfg)
		a, err := build(context.Background(), cfg, "", logging.Discard())
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		t.Cleanup(func() { _ = a.shutdown(context.Background()) })
		if err := a.srv.Start(); err != nil {
			t.Fatalf("Start: %v", err)
		}
		resp, err := http.Get("http://" + a.srv.Address() + "/healthz")
		if err != nil {
			t.Fatalf("GET: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		var report health.Report
		if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
			t.Fatalf("decode: %v", err)
		}
		out := make(map[string]health.Result, len(report.Results))
		for _, r := range report.Results {
			out[r.Name] = r
		}
		return out
	}

	t.Run("no summary quotes a phase for a built subsystem", func(t *testing.T) {
		got := run(t, func(*config.Config) {})
		for _, name := range []string{"routing", "scheduler", "network", "peers"} {
			if strings.Contains(got[name].Summary, "phase") {
				t.Errorf("%s summary still quotes the phase plan: %q", name, got[name].Summary)
			}
		}
	})

	t.Run("routing off names the setting", func(t *testing.T) {
		got := run(t, func(*config.Config) {})
		if got["routing"].Status != health.StatusUnavailable {
			t.Errorf("routing = %q, want unavailable", got["routing"].Status)
		}
		if !strings.Contains(got["routing"].Summary, "dmr.forwarding") {
			t.Errorf("routing summary should name the setting: %q", got["routing"].Summary)
		}
	})

	t.Run("scheduled windows with forwarding off is degraded", func(t *testing.T) {
		got := run(t, func(c *config.Config) {
			c.DMR.Schedule = []config.Window{{
				Bridge: "x", Days: []int{2}, Start: "20:00",
				Duration: config.Duration(time.Hour), Timezone: "UTC", Enabled: true,
			}}
		})
		if got["scheduler"].Status != health.StatusDegraded {
			t.Errorf("scheduler = %q, want degraded: a schedule that relays nothing is worth flagging",
				got["scheduler"].Status)
		}
		if got["scheduler"].Fix == "" {
			t.Error("the degraded scheduler check offers no fix")
		}
	})
}

// TestPersistenceIsRealNow covers what registering a driver actually bought.
//
// Until 0.1.6 the database was configured, wired and migrated by code no build
// ever executed, because no driver was registered. Every one of these
// assertions would have been unreachable.
func TestPersistenceIsRealNow(t *testing.T) {
	cfg := testConfig(t)

	a, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	if a.db == nil {
		t.Fatal("no database handle despite a registered sqlite driver")
	}

	// The migrations are embedded and applied at startup. If they did not run,
	// the schema version is zero and nothing else here is meaningful.
	if _, err := os.Stat(cfg.Database.DSN); err != nil {
		t.Errorf("the database file was not created at %s: %v", cfg.Database.DSN, err)
	}
}

// TestSchemaSurvivesARestart is the property the two-week soak depends on.
//
// A restart at day nine must not lose the first nine days. Building twice
// against one DSN proves the schema is durable and that re-running migrations
// against an already-migrated database is safe rather than an error.
func TestSchemaSurvivesARestart(t *testing.T) {
	cfg := testConfig(t)

	first, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if err := first.shutdown(context.Background()); err != nil {
		t.Fatalf("first shutdown: %v", err)
	}

	second, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("second build against an existing database: %v", err)
	}
	defer func() { _ = second.shutdown(context.Background()) }()

	if second.db == nil {
		t.Fatal("no database handle on the second build")
	}
}

// TestTheResolverIsBuiltBeforeAnythingReadsIt.
//
// **The console's view source captures a.names by value.** It was built before
// the resolver was assigned, so the view held nil: no radio ID was ever queued,
// the cache stayed empty, and the instance logged "radio ID lookups enabled" the
// whole time. Nothing failed — the feature was simply never reached.
//
// Ordering inside one function is not something the compiler checks and not
// something a unit test can reach, so this reads the source.
func TestTheResolverIsBuiltBeforeAnythingReadsIt(t *testing.T) {
	body, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("reading app.go: %v", err)
	}
	src := string(body)

	built := strings.Index(src, "a.names = callsigns.NewService")
	if built < 0 {
		t.Fatal("the resolver is no longer built here; this check is blind")
	}

	// Every place that captures it must come later.
	for _, reader := range []string{"names: a.names", "srv.SetLogins"} {
		at := strings.Index(src, reader)
		if at < 0 {
			continue
		}
		if at < built {
			t.Errorf("%q reads a.names before it is built; the value captured is nil",
				reader)
		}
	}
}

// TestTheConnectionDoesNotTakeSQLitesDefaults.
//
// **None of these were being set.** sql.Open was given a bare DSN and the
// connection took SQLite's defaults, which are chosen for a single-process
// command line tool rather than a server:
//
//   - busy_timeout 0, so any lock contention failed immediately rather than
//     waiting. database.busy_timeout was documented, defaulted to five seconds,
//     validated on startup, and applied to nothing — the third field found this
//     way, after the link export lists and the target-size token.
//   - journal_mode DELETE, under which a writer blocks every reader for the
//     length of its transaction. QSP writes an audit event whenever a peer
//     connects and reads a session on every console request, against a pool of
//     four connections.
//   - foreign_keys OFF, so the ON DELETE CASCADE migration 0003 declares on
//     sessions.user_id has never fired.
func TestTheConnectionDoesNotTakeSQLitesDefaults(t *testing.T) {
	cfg := testConfig(t)

	a, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	if a.db == nil {
		t.Fatal("no database handle despite a registered sqlite driver")
	}

	var timeout int
	if err := a.db.SQL().QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatalf("reading busy_timeout: %v", err)
	}
	if timeout <= 0 {
		t.Errorf("busy_timeout is %d, so contention fails rather than waits", timeout)
	}

	var journal string
	if err := a.db.SQL().QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("reading journal_mode: %v", err)
	}
	if !strings.EqualFold(journal, "wal") {
		t.Errorf("journal_mode is %q, so a writer blocks every reader", journal)
	}

	var fk int
	if err := a.db.SQL().QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("reading foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Error("foreign_keys is off, so the cascade migration 0003 declares does nothing")
	}
}

// TestTheAuditTrailReachesTheDatabase.
//
// **It never did.** Migration 0002 created audit_events with two indexes, the
// schema reached version 4 carrying it, SECURITY.md said the audit trail
// records who did what and that roles are absent because of it — and
// audit.LogRecorder was the only implementation of audit.Recorder in the
// program. A production instance running for weeks held zero rows in that
// table and could not have held any.
//
// Every part existed: the table, the migration, the interface, the redactor,
// the event type, the actions. Nothing joined them. This asserts the join
// rather than any of the parts, because each part was already correct.
func TestTheAuditTrailReachesTheDatabase(t *testing.T) {
	cfg := testConfig(t)

	a, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	if a.db == nil {
		t.Fatal("no database handle despite a registered sqlite driver")
	}

	if err := a.audit.Record(context.Background(), audit.Event{
		OccurredAt: time.Now().UTC(),
		Actor:      "K9MLS",
		Action:     audit.ActionUserLogin,
		Outcome:    audit.OutcomeSuccess,
		SourceIP:   "192.0.2.1",
		Detail:     map[string]string{"note": "kept", "password": "must not survive"},
	}); err != nil {
		t.Fatalf("recording: %v", err)
	}

	var count int
	if err := a.db.SQL().QueryRow(
		`SELECT COUNT(*) FROM audit_events WHERE action = ?`,
		string(audit.ActionUserLogin)).Scan(&count); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if count != 1 {
		t.Fatalf("audit_events holds %d login rows; the trail does not reach the database", count)
	}

	// Redacted on the way in. A secret written to an append-only table is a
	// secret in every backup of it, and the table's own comment makes that the
	// recorder's job.
	var detail string
	if err := a.db.SQL().QueryRow(
		`SELECT detail FROM audit_events WHERE action = ?`,
		string(audit.ActionUserLogin)).Scan(&detail); err != nil {
		t.Fatalf("reading detail: %v", err)
	}
	if strings.Contains(detail, "must not survive") {
		t.Errorf("a secret was written to the audit trail: %s", detail)
	}
	if !strings.Contains(detail, "kept") {
		t.Errorf("redaction removed a value that was not sensitive: %s", detail)
	}
}

// TestACompletedCallSurvivesARestart.
//
// **The last-heard list held fifty calls in memory and lost them on restart.**
// That is a display and it works as one — until a net control station uses it to
// recover a check-in they missed, which is the use it was actually being put
// to. Then it is the only record of who was on the net, and it has to survive
// the deploy that follows the evening. See ADR-0033.
func TestACompletedCallSurvivesARestart(t *testing.T) {
	cfg := testConfig(t)

	a, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if a.callStore == nil || !a.callStore.Enabled() {
		t.Fatal("no call history is kept despite a registered driver and a retention window")
	}

	started := time.Now().UTC().Add(-30 * time.Second)
	call := calls.Call{
		Key:     calls.Key{Peer: 3132910, Stream: 889716896, Timeslot: hbp.Timeslot2},
		Source:  3132910,
		Target:  2,
		Group:   true,
		Voice:   true,
		Frames:  148,
		Started: started,
		Ended:   started.Add(3 * time.Second),
	}
	if err := a.callStore.Record(context.Background(), call); err != nil {
		t.Fatalf("recording: %v", err)
	}
	if err := a.shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	// A second instance against the same database is what a deploy looks like.
	b, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	defer func() { _ = b.shutdown(context.Background()) }()

	got, err := b.callStore.Since(context.Background(), started.Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("history holds %d calls after a restart; the evening is gone", len(got))
	}
	if got[0].Source != 3132910 || got[0].Target != 2 || !got[0].Voice {
		t.Errorf("the call came back wrong: %+v", got[0])
	}
	if got[0].Frames != 148 {
		t.Errorf("frame count is %d, not 148", got[0].Frames)
	}
}

// TestRetentionIsByAgeAndCanBeNothing.
//
// By age rather than count, because the question a club asks is "what happened
// at Tuesday's net" — a count means a busy Saturday silently erases it. And zero
// keeps nothing, which is a real answer for a club that would rather not hold a
// record of who transmitted when.
func TestRetentionIsByAgeAndCanBeNothing(t *testing.T) {
	cfg := testConfig(t)
	cfg.DMR.Calls.Retain = config.Duration(time.Hour)

	a, err := build(context.Background(), cfg, "", logging.Discard())
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer func() { _ = a.shutdown(context.Background()) }()

	now := time.Now().UTC()
	for _, age := range []time.Duration{10 * time.Minute, 3 * time.Hour} {
		start := now.Add(-age)
		if err := a.callStore.Record(context.Background(), calls.Call{
			Key:     calls.Key{Peer: 1, Stream: hbp.StreamID(age), Timeslot: hbp.Timeslot2},
			Source:  3132910,
			Target:  2,
			Started: start,
			Ended:   start.Add(time.Second),
		}); err != nil {
			t.Fatalf("recording: %v", err)
		}
	}

	n, err := a.callStore.Prune(context.Background(), now)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruning removed %d calls, want the one outside the window", n)
	}

	left, err := a.callStore.Since(context.Background(), now.Add(-24*time.Hour), 10)
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if len(left) != 1 {
		t.Errorf("%d calls survived a one-hour window", len(left))
	}
}

// TestAQSPLinkAnnouncesItsName is the first half of ADR-0052's third rule.
//
// The far end of a link that dialled in has no name of its own for it: it
// receives a registration, not a configuration. So without the name travelling,
// the two administrators of one link call it different things — production's
// console would show "K9MLS" or a bare DMR ID for a link the other side calls
// "production", and a support conversation about it becomes impossible.
//
// PackageID carries it. It is 40 bytes, conventionally a build identifier, and
// QSP has no separate use for one because SoftwareID already says what this is
// and which version. A station that does not understand the prefix displays a
// harmless string on a dashboard.
func TestAQSPLinkAnnouncesItsName(t *testing.T) {
	name, ok := LinkNameFromPackageID(qspLinkPackageID + "production")
	if !ok {
		t.Fatal("a link's own PackageID is not recognised as carrying a name")
	}
	if name != "production" {
		t.Errorf("recovered %q, want production", name)
	}
}

// TestAnotherImplementationsPackageIDIsNotAName, because the field is somebody
// else's to use.
//
// PackageID conventionally carries a build identifier, and MMDVMHost and
// friends fill it with one. Reading any value there as a link name would put a
// hotspot's build string on the Links page as though it were a linked network.
func TestAnotherImplementationsPackageIDIsNotAName(t *testing.T) {
	for _, pkg := range []string{"", "20210101_MMDVMHost", "QSP", "qsp-link:x"} {
		if name, ok := LinkNameFromPackageID(pkg); ok {
			t.Errorf("PackageID %q was read as the link name %q", pkg, name)
		}
	}
}

// TestAnEmptyNameIsNotAName. A link with no name announces the prefix and
// nothing after it, and the far end must not display an empty label as though
// it were one.
func TestAnEmptyNameIsNotAName(t *testing.T) {
	if name, ok := LinkNameFromPackageID(qspLinkPackageID + "   "); ok {
		t.Errorf("a blank name was accepted as %q", name)
	}
}

// TestALinkAnnouncesItsNetworkNotItsStation.
//
// The thing dialling in is a network, so the description an administrator reads
// beside it should name that network. A repeater description — "Denton
// backyard, 70cm" — is true of the station and says nothing true about what
// arrived.
func TestALinkAnnouncesItsNetworkNotItsStation(t *testing.T) {
	ident := config.UpstreamIdentity{Description: "Denton backyard"}

	if got := linkDescription("BCARA", ident); got != "BCARA" {
		t.Errorf("a link announced %q, want its network name", got)
	}
	// A network with no name falls back rather than announcing nothing: an
	// operator reading a blank field cannot tell it from a fault.
	if got := linkDescription("  ", ident); got != "Denton backyard" {
		t.Errorf("with no network name a link announced %q, want the description", got)
	}
}
