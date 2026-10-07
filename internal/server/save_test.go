package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/audit"
	"github.com/k9mls/qsp/internal/config"
)

// needsRestartOf reads what an answer says is waiting for a restart.
func needsRestartOf(t *testing.T, body []byte) []string {
	t.Helper()
	var out struct {
		NeedsRestart []string `json:"needs_restart"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out.NeedsRestart
}

// TestEverySaverCallsASaveASave. A configuration that reached the file and
// could not be handed to the running listeners is saved: the next restart
// uses it. One handler said so. The others answered 400, recorded a failure,
// and two of them then deleted the password file the saved configuration
// named (2026-10-07, G10).
//
// To see it fail: in Server.save, return the error it was given.
func TestEverySaverCallsASaveASave(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		// keeps is a file the saved configuration needs, given the root.
		keeps func(root string) string
	}{
		{"callsign lookup", http.MethodPut, "/api/admin/callsigns",
			`{"enabled":true,"contact":"op@example.org"}`, nil},
		{"session lifetime", http.MethodPut, "/api/admin/session-lifetime", `{"seconds":7200}`, nil},
		{"a link's address", http.MethodPut, "/api/links/pete/address",
			`{"address":"pete.example.net:62031"}`, nil},
		{"removing a link", http.MethodDelete, "/api/links/pete", "", nil},
		{"offering a link", http.MethodPost, "/api/links/offer-link",
			`{"address":"here.example.org:62031","repeater_id":3132914,"callsign":"W9XYZ"}`,
			func(root string) string { return filepath.Join(root, "peers", "3132914") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := home(t)
			cm := newStubConfig()
			cm.current.DMR.PasswordFile = filepath.Join(root, "peers.pass")
			cm.current.DMR.Identity.Callsign = "W9XYZ"
			cm.current.DMR.Upstreams = []config.Upstream{{Name: "pete", Protocol: "qsp",
				Address: "pete.example.com:62031", RepeaterID: 3132913,
				PasswordFile: filepath.Join(root, "pete.pass")}}
			cm.applyErr = errors.New("the bridge table could not be rebuilt")
			rec := &recordingAudit{}
			srv, a := newConfigServer(t, cm, rec)

			res := authed(t, srv, a, tc.method, tc.path, tc.body)
			if res.Code != http.StatusOK {
				t.Fatalf("answered %d for a configuration that was saved: %s", res.Code, res.Body)
			}
			if len(cm.saved) == 0 {
				t.Fatal("nothing was saved, so nothing was checked")
			}
			told := false
			for _, n := range needsRestartOf(t, res.Body.Bytes()) {
				if strings.Contains(n, "could not be applied") && strings.Contains(n, "bridge table") {
					told = true
				}
			}
			if !told {
				t.Errorf("the operator is not told it is waiting for a restart, or why: %s", res.Body)
			}
			for _, e := range rec.events {
				if e.Outcome == audit.OutcomeFailure {
					t.Errorf("recorded as a failure: %s", e.Action)
				}
			}
			if tc.keeps != nil {
				if _, err := os.Stat(tc.keeps(root)); err != nil {
					t.Errorf("the saved configuration names a password that was then deleted: %v", err)
				}
			}
		})
	}
}

// TestASaverNamesWhatItsOwnSaveLeftWaiting. Two savers whose answer said
// nothing needed a restart when something did.
//
// The callsign lookup compared the saved configuration with itself (G5). The
// first link a server offers creates the directory of per-station passwords
// and saves where it is; the running server was built without one, so the
// far end was refused with the password it had just been given (G6).
//
// To see each fail: compare with s.opts.Config.Current() after the save in
// handleCallsigns; move dmr.peer_passwords to appliedOnSave in restart.go.
func TestASaverNamesWhatItsOwnSaveLeftWaiting(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, want string
	}{
		{"callsign lookup", http.MethodPut, "/api/admin/callsigns",
			`{"enabled":true,"contact":"op@example.org"}`, "dmr.callsigns.enabled"},
		{"the first link offered", http.MethodPost, "/api/links/offer-link",
			`{"address":"here.example.org:62031","repeater_id":3132914,"callsign":"W9XYZ"}`,
			"dmr.peer_passwords"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := home(t)
			cm := newStubConfig()
			cm.current.DMR.PasswordFile = filepath.Join(root, "peers.pass")
			cm.current.DMR.Identity.Callsign = "W9XYZ"
			srv, a := newConfigServer(t, cm, &recordingAudit{})

			res := authed(t, srv, a, tc.method, tc.path, tc.body)
			if res.Code != http.StatusOK {
				t.Fatalf("answered %d: %s", res.Code, res.Body)
			}
			got := needsRestartOf(t, res.Body.Bytes())
			found := false
			for _, n := range got {
				found = found || n == tc.want
			}
			if !found {
				t.Errorf("%s is read at startup and the answer names %q", tc.want, got)
			}
		})
	}
}

// TestTheSessionLengthSavedIsNotCalledWaiting: it is applied as it is saved,
// so its answer names nothing.
func TestTheSessionLengthSavedIsNotCalledWaiting(t *testing.T) {
	cm := newStubConfig()
	srv, a := newConfigServer(t, cm, &recordingAudit{})
	res := authed(t, srv, a, http.MethodPut, "/api/admin/session-lifetime", `{"seconds":7200}`)
	if res.Code != http.StatusOK {
		t.Fatalf("answered %d: %s", res.Code, res.Body)
	}
	if got := needsRestartOf(t, res.Body.Bytes()); len(got) != 0 {
		t.Errorf("a restart was asked for: %q", got)
	}
}
