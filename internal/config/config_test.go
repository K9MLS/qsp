package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("the recommended configuration does not validate: %v", err)
	}
}

func TestDefaultRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := Save(&buf, Default()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(&buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Config holds a slice of bridges, so it is not comparable with ==.
	if !reflect.DeepEqual(got, Default()) {
		t.Errorf("round trip changed the configuration:\n got %+v\nwant %+v", got, Default())
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	// An operator fixing a form should see all problems at once, not one per
	// attempt.
	c := Default()
	c.Server.ListenAddress = ""
	c.Database.DSN = ""
	c.Logging.Level = "verbose"
	c.Events.HistorySize = 0

	err := c.Validate()
	if err == nil {
		t.Fatal("expected validation to fail")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	want := []string{"database.dsn", "events.history_size", "logging.level", "server.listen_address"}
	got := ve.Fields()
	if len(got) != len(want) {
		t.Fatalf("reported fields %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestEveryFieldErrorCarriesAFix(t *testing.T) {
	// Constitution §12: never merely say a value is invalid.
	c := Config{}
	err := c.Validate()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Errors) == 0 {
		t.Fatal("an empty configuration produced no errors")
	}
	for _, fe := range ve.Errors {
		if strings.TrimSpace(fe.Problem) == "" {
			t.Errorf("field %q has no problem description", fe.Field)
		}
		if strings.TrimSpace(fe.Fix) == "" {
			t.Errorf("field %q has no fix guidance", fe.Field)
		}
	}
}

func TestValidateRejectsFutureSchemaVersion(t *testing.T) {
	c := Default()
	c.Version = SchemaVersion + 1
	err := c.Validate()
	if err == nil {
		t.Fatal("expected a future schema version to be rejected")
	}
	if !strings.Contains(err.Error(), "upgrade QSP") {
		t.Errorf("error should tell the operator to upgrade, got: %v", err)
	}
}

func TestValidateRejectsMalformedListenAddress(t *testing.T) {
	c := Default()
	c.Server.ListenAddress = "8080"
	if err := c.Validate(); err == nil {
		t.Fatal("expected a bare port to be rejected")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	// A typo must not silently leave the default in place.
	in := `{"version":1,"servr":{"listen_address":"127.0.0.1:8080"}}`
	if _, err := Load(strings.NewReader(in)); err == nil {
		t.Fatal("expected an unknown field to be rejected")
	}
}

func TestLoadAppliesDefaultsForOmittedFields(t *testing.T) {
	in := `{"version":1,"server":{"listen_address":"0.0.0.0:9000"}}`
	got, err := Load(strings.NewReader(in))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Server.ListenAddress != "0.0.0.0:9000" {
		t.Errorf("listen address = %q, want 0.0.0.0:9000", got.Server.ListenAddress)
	}
	if got.Database.DSN != Default().Database.DSN {
		t.Errorf("omitted field did not take its default: dsn = %q", got.Database.DSN)
	}
	if got.Server.ReadHeaderTimeout != Default().Server.ReadHeaderTimeout {
		t.Errorf("omitted duration did not take its default: %v", got.Server.ReadHeaderTimeout)
	}
}

func TestLoadRejectsInvalidDocument(t *testing.T) {
	in := `{"version":1,"logging":{"level":"chatty"}}`
	if _, err := Load(strings.NewReader(in)); err == nil {
		t.Fatal("Load accepted an invalid configuration")
	}
}

func TestSaveRefusesInvalidConfiguration(t *testing.T) {
	c := Default()
	c.Server.ListenAddress = ""
	var buf bytes.Buffer
	if err := Save(&buf, c); err == nil {
		t.Fatal("Save wrote an invalid configuration")
	}
	if buf.Len() != 0 {
		t.Errorf("Save emitted %d bytes despite refusing", buf.Len())
	}
}

func TestDurationSerialisesAsString(t *testing.T) {
	b, err := json.Marshal(Duration(90 * time.Second))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(b) != `"1m30s"` {
		t.Errorf("got %s, want \"1m30s\"", b)
	}
}

func TestDurationRejectsNumericJSON(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte("30000000000"), &d)
	if err == nil {
		t.Fatal("expected a numeric duration to be rejected")
	}
	if !strings.Contains(err.Error(), "30s") {
		t.Errorf("error should show the expected form, got: %v", err)
	}
}

func TestDurationRejectsUnparseableString(t *testing.T) {
	var d Duration
	err := json.Unmarshal([]byte(`"half an hour"`), &d)
	if err == nil {
		t.Fatal("expected an unparseable duration to be rejected")
	}
	if !strings.Contains(err.Error(), "500ms") {
		t.Errorf("error should offer example values, got: %v", err)
	}
}

func TestChecksumIsStable(t *testing.T) {
	a, err := Checksum(Default())
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	b, err := Checksum(Default())
	if err != nil {
		t.Fatalf("Checksum: %v", err)
	}
	if a != b {
		t.Errorf("checksum is not stable: %s != %s", a, b)
	}
}

func TestChecksumChangesWithContent(t *testing.T) {
	c := Default()
	c.Server.ListenAddress = "0.0.0.0:8080"
	a, _ := Checksum(Default())
	b, _ := Checksum(c)
	if a == b {
		t.Error("checksum did not change when the configuration changed")
	}
}

func TestNewVersionRefusesInvalidConfiguration(t *testing.T) {
	c := Default()
	c.Database.MaxOpenConns = 0
	_, err := NewVersion(1, "K9MLS", "test", c, time.Now())
	if err == nil {
		t.Fatal("an invalid configuration entered the version history")
	}
}

func TestNewVersionNormalisesTimeToUTC(t *testing.T) {
	loc := time.FixedZone("CST", -6*3600)
	v, err := NewVersion(1, "K9MLS", "", Default(), time.Date(2026, 8, 23, 9, 0, 0, 0, loc))
	if err != nil {
		t.Fatalf("NewVersion: %v", err)
	}
	if v.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt is in %v, want UTC", v.CreatedAt.Location())
	}
}

func TestSameContent(t *testing.T) {
	now := time.Now()
	v1, _ := NewVersion(1, "a", "", Default(), now)
	v2, _ := NewVersion(2, "b", "different summary", Default(), now.Add(time.Hour))
	if !SameContent(v1, v2) {
		t.Error("versions with identical configuration reported as different")
	}

	c := Default()
	c.Logging.Level = "debug"
	v3, _ := NewVersion(3, "a", "", c, now)
	if SameContent(v1, v3) {
		t.Error("versions with different configuration reported as identical")
	}
}

func TestSameContentIsFalseForZeroVersions(t *testing.T) {
	if SameContent(Version{}, Version{}) {
		t.Error("two unchecksummed versions must not compare equal")
	}
}

func TestDiffReportsChangedFieldsOnly(t *testing.T) {
	to := Default()
	to.Logging.Level = "debug"
	to.Server.ListenAddress = "0.0.0.0:8080"

	changes, err := Diff(Default(), to)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("got %d changes, want 2: %+v", len(changes), changes)
	}
	if changes[0].Field != "logging.level" {
		t.Errorf("changes[0].Field = %q, want logging.level", changes[0].Field)
	}
	if changes[0].From != `"info"` || changes[0].To != `"debug"` {
		t.Errorf("changes[0] = %+v, want info -> debug", changes[0])
	}
	if changes[1].Field != "server.listen_address" {
		t.Errorf("changes[1].Field = %q, want server.listen_address", changes[1].Field)
	}
}

func TestDiffOfIdenticalConfigurationsIsEmpty(t *testing.T) {
	changes, err := Diff(Default(), Default())
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("got %d changes for identical configurations: %+v", len(changes), changes)
	}
}

func TestDiffIsOrderedByField(t *testing.T) {
	to := Default()
	to.Server.ListenAddress = "0.0.0.0:1"
	to.Logging.Level = "debug"
	to.Database.DSN = "other.db"
	to.Events.HistorySize = 999

	changes, err := Diff(Default(), to)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	for i := 1; i < len(changes); i++ {
		if changes[i-1].Field > changes[i].Field {
			t.Fatalf("changes are not sorted: %q before %q", changes[i-1].Field, changes[i].Field)
		}
	}
}

// TestTheOrdinaryClubNetworkIsValid.
//
// **This test asserted the opposite until now**, and its name said so:
// forwarding with no bridges was rejected as "almost certainly a mistake". It
// was, while bridging was the whole routing model. ADR-0019 made a repeating
// master the ordinary case, the health check was corrected for it, and this was
// not — so the Forwarding field's own documentation described a configuration
// its validator refused, twenty lines apart in one file.
//
// Four hotspots on one talkgroup hearing each other needs no bridges and is
// what most clubs run.
func TestTheOrdinaryClubNetworkIsValid(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	// An empty block is the deliberate permit-everything of ADR-0020: the
	// operator has said so, which is what the startup check is about.
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = true

	if err := c.Validate(); err != nil {
		t.Fatalf("a repeating master with no bridges was rejected: %v", err)
	}
}

// TestObservingWithoutRelayingIsValid. Forwarding is off by default so an
// operator can run QSP as a master and watch peers connect before it puts audio
// on anybody's repeater. Refusing that — which one version of this rule did —
// removes the mode the field exists to provide.
func TestObservingWithoutRelayingIsValid(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = false

	if err := c.Validate(); err != nil {
		t.Fatalf("observation mode was rejected: %v", err)
	}
}

func TestBridgeValidation(t *testing.T) {
	base := func() Config {
		c := Default()
		c.DMR.Enabled = true
		// An empty block is the deliberate permit-everything of ADR-0020: the
		// operator has said so, which is what the startup check is about.
		c.DMR.Access = &Access{}
		c.DMR.PasswordFile = "/etc/qsp/peer.pass"
		return c
	}
	valid := []Endpoint{
		{Peer: 3132910, Talkgroup: 3148, Timeslot: 1},
		{Peer: 0, Talkgroup: 91, Timeslot: 2},
	}

	t.Run("valid bridge passes", func(t *testing.T) {
		c := base()
		c.DMR.Bridges = []Bridge{{Name: "net", Enabled: true, Endpoints: valid}}
		if err := c.Validate(); err != nil {
			t.Errorf("a valid bridge was rejected: %v", err)
		}
	})

	cases := map[string]Bridge{
		"no name":        {Endpoints: valid},
		"one endpoint":   {Name: "lonely", Endpoints: valid[:1]},
		"talkgroup zero": {Name: "tg0", Endpoints: []Endpoint{{Talkgroup: 0, Timeslot: 1}, valid[1]}},
		"bad timeslot":   {Name: "ts3", Endpoints: []Endpoint{{Talkgroup: 1, Timeslot: 3}, valid[1]}},
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			c := base()
			c.DMR.Bridges = []Bridge{b}
			if err := c.Validate(); err == nil {
				t.Errorf("accepted an invalid bridge: %s", name)
			}
		})
	}

	t.Run("duplicate names", func(t *testing.T) {
		c := base()
		c.DMR.Bridges = []Bridge{
			{Name: "Net", Endpoints: valid},
			{Name: "net", Endpoints: valid},
		}
		if err := c.Validate(); err == nil {
			t.Error("two bridges with names differing only in case were accepted")
		}
	})
}

func TestBridgesRoundTripThroughJSON(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	// An empty block is the deliberate permit-everything of ADR-0020: the
	// operator has said so, which is what the startup check is about.
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = true
	c.DMR.Bridges = []Bridge{{
		Name: "tuesday-net", Enabled: true,
		Endpoints: []Endpoint{
			{Peer: 3132910, Talkgroup: 3148, Timeslot: 1},
			{Peer: 0, Talkgroup: 91, Timeslot: 2},
		},
	}}

	var buf bytes.Buffer
	if err := Save(&buf, c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(&buf)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("bridges did not round trip:\n got %+v\nwant %+v", got.DMR.Bridges, c.DMR.Bridges)
	}
}

// TestAJoinTalkgroupNeedsNoBridgeWhenTheMasterRepeats.
//
// **This refused to save a configuration describing a working network.** The
// rule complained that no bridge carried a published talkgroup, which is true
// and irrelevant: with dmr.forwarding on, a repeating master carries every
// talkgroup between peers and no bridge is involved. It is how most clubs run.
//
// Third instance of one mistake — a rule written when bridging was the whole
// routing model and left behind by ADR-0019. The other two stopped a live
// network from starting.
func TestAJoinTalkgroupNeedsNoBridgeWhenTheMasterRepeats(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = true
	c.DMR.Join = Join{
		NetworkName: "BCARA",
		Address:     "qsp.example",
		Talkgroups:  []JoinTalkgroup{{Name: "Club", Dialled: 2, Timeslot: 2}},
	}
	// A bridge exists, for something else entirely. Its presence is what used
	// to switch the rule on.
	c.DMR.Bridges = []Bridge{{
		Name: "elsewhere", Enabled: true,
		Endpoints: []Endpoint{
			{Talkgroup: 91, Timeslot: 2},
			{Talkgroup: 92, Timeslot: 2},
		},
	}}

	if err := c.Validate(); err != nil {
		t.Fatalf("a published talkgroup on a repeating master was refused: %v", err)
	}
}

// TestAJoinTalkgroupNeedsABridgeWhenNothingRepeats. With forwarding off,
// bridges are the only path, and a talkgroup no bridge carries is one a member
// is told to dial into silence.
func TestAJoinTalkgroupNeedsABridgeWhenNothingRepeats(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = false
	c.DMR.Join = Join{
		NetworkName: "BCARA",
		Address:     "qsp.example",
		Talkgroups:  []JoinTalkgroup{{Name: "Club", Dialled: 2, Timeslot: 2}},
	}
	c.DMR.Bridges = []Bridge{{
		Name: "elsewhere", Enabled: true,
		Endpoints: []Endpoint{
			{Talkgroup: 91, Timeslot: 2},
			{Talkgroup: 92, Timeslot: 2},
		},
	}}

	err := c.Validate()
	if err == nil {
		t.Fatal("a talkgroup nothing carries was accepted; members would dial into silence")
	}
	if !strings.Contains(err.Error(), "no bridge carries") {
		t.Errorf("the error does not explain: %v", err)
	}
}

// TestANameForARepeaterThatIsNotAdmittedIsRefused is the pattern that has been
// the defect nine times in this codebase.
//
// A setting declared and read by nothing looks exactly like a setting that
// works. A callsign attached to a radio ID the listener never answers is
// never shown, and an operator who typed it has no way to discover that
// except by waiting for a repeater to connect and reading a bare number.
func TestANameForARepeaterThatIsNotAdmittedIsRefused(t *testing.T) {
	base := Default()
	base.IPSC.Enabled = true
	base.IPSC.ListenAddress = "0.0.0.0:50000"
	base.IPSC.MasterID = 3132911
	cc := uint8(11)
	base.IPSC.ColourCode = &cc

	orphan := base
	orphan.IPSC.AllowedPeers = []uint32{315544}
	orphan.IPSC.PeerNames = map[uint32]string{999999: "K9MLS"}
	if err := orphan.Validate(); err == nil {
		t.Error("a callsign for a repeater that is never answered was accepted")
	}

	// **An empty allow list admits everybody**, so a name is never orphaned by
	// one and refusing it there would make naming impossible on a bench.
	open := base
	open.IPSC.AllowedPeers = nil
	open.IPSC.PeerNames = map[uint32]string{999999: "K9MLS"}
	if err := open.Validate(); err != nil {
		t.Errorf("a callsign was refused while every repeater is admitted: %v", err)
	}

	matched := base
	matched.IPSC.AllowedPeers = []uint32{999999}
	matched.IPSC.PeerNames = map[uint32]string{999999: "K9MLS"}
	if err := matched.Validate(); err != nil {
		t.Errorf("a callsign for an admitted repeater was refused: %v", err)
	}

	for name, bad := range map[string]string{
		"blank":  "   ",
		"padded": " K9MLS ",
		"longer than a call sign has any business being": strings.Repeat("K", 21),
	} {
		c := matched
		c.IPSC.PeerNames = map[uint32]string{999999: bad}
		if err := c.Validate(); err == nil {
			t.Errorf("a %s callsign was accepted", name)
		}
	}
}
