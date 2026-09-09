package config_test

import (
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// aDocument is a configuration as a server would have written it before 0308,
// with the retired fields present because every save wrote them.
func aDocument(withRetired bool) string {
	upstream := `{
      "name": "brandmeister", "protocol": "openbridge", "enabled": true,
      "address": "bm.example.com:62035", "listen_address": "0.0.0.0:62035",
      "network_id": 3132910, "passphrase_file": "/var/lib/qsp/bm.pass"%s
    }`
	lists := ""
	if withRetired {
		lists = `,
      "export": [{"talkgroup": 3148, "timeslot": 2}],
      "import": [{"talkgroup": 3148, "timeslot": 2}]`
	}
	return `{
  "version": 1,
  "server": {"listen_address": "127.0.0.1:8080"},
  "database": {"driver": "sqlite", "dsn": "/var/lib/qsp/qsp.db"},
  "dmr": {
    "enabled": true,
    "listen_address": "127.0.0.1:62031",
    "password_file": "/var/lib/qsp/peer.pass",
    "identity": {"callsign": "K9MLS"},
    "upstreams": [` + strings.Replace(upstream, "%s", lists, 1) + `],
    "bridges": [{"name": "b", "enabled": true, "endpoints": [
      {"talkgroup": 3148, "timeslot": 2},
      {"upstream": "brandmeister", "talkgroup": 3148, "timeslot": 1}
    ]}]
  }
}`
}

// **The failure this mechanism exists to prevent.** `Load` refuses unknown
// fields, and `export` and `import` were written by every save — so deleting
// them from the struct without this would make every configuration already on
// disk unparseable, and every server holding one would fail at its next
// restart, long after the change that caused it.
func TestAConfigurationWrittenBeforeAFieldWasRetiredStillLoads(t *testing.T) {
	cfg, err := config.Load(strings.NewReader(aDocument(true)))
	if err != nil {
		t.Fatalf("a configuration written before 0308 no longer loads: %v", err)
	}
	if len(cfg.DMR.Upstreams) != 1 {
		t.Fatalf("the link was lost: %d upstreams", len(cfg.DMR.Upstreams))
	}
	// Everything beside the retired fields must survive, or this is a
	// mechanism that drops more than it was asked to.
	u := cfg.DMR.Upstreams[0]
	if u.Name != "brandmeister" || u.NetworkID != 3132910 {
		t.Errorf("the link changed while its retired fields were dropped: %+v", u)
	}
}

// **Not a way to tolerate a typo**, which is the rule this preserves: an
// operator who writes `passwrod_file` must be told, not left believing a
// setting was applied.
func TestATypoIsStillRefused(t *testing.T) {
	doc := strings.Replace(aDocument(false),
		`"password_file"`, `"passwrod_file"`, 1)

	if _, err := config.Load(strings.NewReader(doc)); err == nil {
		t.Fatal("a misspelled field was accepted")
	} else if !strings.Contains(err.Error(), "passwrod_file") {
		t.Errorf("the error does not name the field that is wrong: %v", err)
	}
}

// A field retired at one path must not be dropped at another. `export` under
// an upstream is retired; a field of that name somewhere else is not, and
// dropping it would be this mechanism deciding something nobody asked it to.
func TestRetirementIsByPathAndNotByName(t *testing.T) {
	doc := strings.Replace(aDocument(false),
		`"dmr": {`, `"dmr": {
    "export": [],`, 1)

	if _, err := config.Load(strings.NewReader(doc)); err == nil {
		t.Fatal("a field named export outside an upstream was silently dropped")
	}
}

// A document with none of them loads unchanged and costs nothing.
func TestADocumentWithNoRetiredFieldsIsUntouched(t *testing.T) {
	cfg, err := config.Load(strings.NewReader(aDocument(false)))
	if err != nil {
		t.Fatalf("a current configuration does not load: %v", err)
	}
	if cfg.DMR.Upstreams[0].Name != "brandmeister" {
		t.Errorf("the link changed: %+v", cfg.DMR.Upstreams[0])
	}
}
