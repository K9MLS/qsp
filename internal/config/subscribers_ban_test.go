package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// 0448: the subscriber list is a ban list only. QSP never limits which radios
// may transmit; it refuses only the radios listed.
//
// The day this is for: a Docker first run wrote an allow-only subscriber list
// naming the login ID, 3132910, and the first radio with an ID of its own,
// 3132911, was refused on every key-up and every text.
//
// To see these fail, break access.go deliberately:
//   - make openSubscribers return at once: the load cases fail, the file is
//     refused ("subscribers is a ban list") and the server would not start;
//   - delete the allow-only refusal in validateAccess: TestAnAllowOnlySubscriber
//     ListCannotBeSaved fails;
//   - clear the IDs of a deny list in openSubscribers too: the ban case fails.

// loadAccess writes a configuration holding the given subscriber list and
// loads it the way the server does at startup.
func loadAccess(t *testing.T, subscribers ACL) (Config, error) {
	t.Helper()
	c := enabledDMR()
	c.DMR.Access = &Access{
		Registration: ACL{Mode: "permit", IDs: []string{"3132910"}},
		Subscribers:  subscribers,
		Talkgroups: Talkgroups{
			Timeslot1: ACL{Mode: "deny", IDs: []string{}},
			Timeslot2: ACL{Mode: "deny", IDs: []string{}},
		},
	}
	// Written as JSON directly, not with Save, because Save refuses an
	// allow-only list — the file under test is one an older version wrote.
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return Load(bytes.NewReader(raw))
}

func TestAnAllowOnlySubscriberListIsOpenedOnLoad(t *testing.T) {
	cases := []struct {
		name      string
		acl       ACL
		allowed   []uint32
		refused   []uint32
		advisory  bool
		wantNamed string
	}{
		{
			// The test server's file, exactly.
			name:      "the Docker first run's list",
			acl:       ACL{Mode: "permit", IDs: []string{"3132910"}},
			allowed:   []uint32{3132910, 3132911, 3155413},
			advisory:  true,
			wantNamed: "naming 1 radio",
		},
		{
			name:      "written in capitals",
			acl:       ACL{Mode: " PERMIT ", IDs: []string{"3132910", "3155000-3155999"}},
			allowed:   []uint32{3132911, 3155413, 3100000},
			advisory:  true,
			wantNamed: "naming 2 radio",
		},
		{
			// A ban list is the operator's decision and survives untouched.
			name:    "a ban list keeps its bans",
			acl:     ACL{Mode: "deny", IDs: []string{"3139999"}},
			allowed: []uint32{3132910, 3132911},
			refused: []uint32{3139999},
		},
		{
			name:    "an empty ban list allows everybody",
			acl:     ACL{Mode: "deny", IDs: []string{}},
			allowed: []uint32{3132910, 3132911},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadAccess(t, tc.acl)
			if err != nil {
				t.Fatalf("a configuration an older QSP wrote no longer loads, so the server "+
					"would not start: %v", err)
			}
			lists, err := cfg.AccessLists()
			if err != nil {
				t.Fatalf("%v", err)
			}
			for _, id := range tc.allowed {
				if !lists.Subscriber.Allows(id) {
					t.Errorf("radio %d is refused", id)
				}
			}
			for _, id := range tc.refused {
				if lists.Subscriber.Allows(id) {
					t.Errorf("banned radio %d is allowed", id)
				}
			}
			// Registration is not touched: who may log in stays as written.
			if lists.Registration.Allows(3139999) {
				t.Error("opening the subscriber list opened registration too")
			}

			var note string
			for _, a := range cfg.AccessAdvisories() {
				if strings.Contains(a, "dmr.access.subscribers") {
					note = a
				}
			}
			switch {
			case tc.advisory && note == "":
				t.Error("the list was opened and the startup log would not say so")
			case tc.advisory && !strings.Contains(note, tc.wantNamed):
				t.Errorf("the advisory %q does not say how many radios the old list named", note)
			case !tc.advisory && note != "":
				t.Errorf("a ban list produced an advisory: %q", note)
			}

			// What the console reads back and would save is the open list.
			if got := cfg.DMR.Access.Subscribers.Mode; got != "deny" {
				t.Errorf("the running configuration holds mode %q, want deny", got)
			}
			var saved bytes.Buffer
			if err := Save(&saved, cfg); err != nil {
				t.Errorf("the opened configuration cannot be saved: %v", err)
			}
		})
	}
}

// Saved from the console or checked with -check, an allow-only list is
// refused with the reason, rather than accepted and quietly opened.
func TestAnAllowOnlySubscriberListCannotBeSaved(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{
		Registration: ACL{Mode: "permit", IDs: []string{"3132910"}},
		Subscribers:  ACL{Mode: "permit", IDs: []string{"3132910"}},
	}
	err := c.Validate()
	if err == nil {
		t.Fatal("an allow-only subscriber list was accepted")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("got %T, want a ValidationError", err)
	}
	var found bool
	for _, fe := range ve.Errors {
		if fe.Field == "dmr.access.subscribers" && strings.Contains(fe.Problem, "ban list") {
			found = true
			if !strings.Contains(fe.Fix, `"deny"`) {
				t.Errorf("the fix %q does not say what to write instead", fe.Fix)
			}
		}
	}
	if !found {
		t.Errorf("the refusal does not name dmr.access.subscribers as a ban list: %v", err)
	}
}

// A backup taken before 0448 restores, and restores open.
//
// To see it fail: remove b.Config.Upgrade() from ReadBackup.
func TestABackupWithAnAllowOnlyListRestoresOpen(t *testing.T) {
	c := enabledDMR()
	c.DMR.Access = &Access{
		Registration: ACL{Mode: "permit", IDs: []string{"3132910"}},
		Subscribers:  ACL{Mode: "permit", IDs: []string{"3132910"}},
	}
	raw, err := json.Marshal(Backup{Format: BackupVersion, Config: c})
	if err != nil {
		t.Fatalf("%v", err)
	}
	b, err := ReadBackup(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("a backup from before 0448 cannot be restored: %v", err)
	}
	lists, err := b.Config.AccessLists()
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !lists.Subscriber.Allows(3132911) {
		t.Error("the restored configuration still refuses radios it does not name")
	}
}
