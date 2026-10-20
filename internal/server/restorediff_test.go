package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

const (
	idProduction = "0123456789abcdef0123456789abcdef"
	idTest       = "fedcba9876543210fedcba9876543210"
)

// named is a configuration with an identifier, a network name, links, bridges
// and the stations allowed to register.
func named(id, name string, links, bridges, stations []string) config.Config {
	cfg := config.Default()
	cfg.Server.Identifier = id
	cfg.DMR.Join.NetworkName = name
	for _, l := range links {
		cfg.DMR.Upstreams = append(cfg.DMR.Upstreams, config.Upstream{Name: l})
	}
	for _, b := range bridges {
		cfg.DMR.Bridges = append(cfg.DMR.Bridges, config.Bridge{Name: b})
	}
	if stations != nil {
		cfg.DMR.Access = &config.Access{Registration: config.ACL{Mode: "permit", IDs: stations}}
	}
	return cfg
}

// TestARestoreSaysWhereTheBackupCameFromAndWhatItRemoves is the comparison
// itself, one case per thing it has to tell apart.
//
// The first case is 2026-10-18: production's full backup restored onto the
// test server, which dialled production and BCARA and lost both at the next
// restart with nothing having said so.
//
// To see it fail:
//   - swap removed and added in compareRestore's call to difference for links:
//     the first case names the links as added;
//   - make compareRestore treat an empty identifier as this server's own: the
//     two unknown cases report this_server;
//   - drop the thereName == hereName branch: the same-name case loses "another
//     server, also called".
func TestARestoreSaysWhereTheBackupCameFromAndWhatItRemoves(t *testing.T) {
	for _, tc := range []struct {
		name       string
		running    config.Config
		incoming   config.Config
		incomingID string
		origin     string
		note       []string
		want       restoreChanges
	}{
		{
			name: "another server's backup, which removes this server's links",
			running: named(idTest, "QSP Test Server", []string{"production", "BCARA"},
				[]string{"bcara", "local"}, []string{"3132910"}),
			incoming: named(idProduction, "QSP Server Prod", nil,
				[]string{"bcara"}, []string{"3132910", "3155413"}),
			incomingID: idProduction,
			origin:     originAnotherServer,
			note:       []string{"made by QSP Server Prod", "not by this server (QSP Test Server)"},
			want: restoreChanges{
				LinksRemoved:   []string{"BCARA", "production"},
				BridgesRemoved: []string{"local"},
				StationsAdded:  []string{"3155413"},
			},
		},
		{
			name:       "this server's own backup, older",
			running:    named(idTest, "QSP Test Server", []string{"production", "BCARA"}, nil, nil),
			incoming:   named(idTest, "QSP Test Server", []string{"production"}, nil, nil),
			incomingID: idTest,
			origin:     originThisServer,
			note:       []string{"made by this server"},
			want:       restoreChanges{LinksRemoved: []string{"BCARA"}},
		},
		{
			name:       "this server's own backup, nothing changed",
			running:    named(idTest, "Club", []string{"production"}, []string{"local"}, []string{"1"}),
			incoming:   named(idTest, "Club", []string{"production"}, []string{"local"}, []string{"1"}),
			incomingID: idTest,
			origin:     originThisServer,
			note:       []string{"made by this server"},
		},
		{
			name:       "another server with the same name",
			running:    named(idTest, "Club", nil, nil, nil),
			incoming:   named(idProduction, "Club", []string{"far"}, nil, nil),
			incomingID: idProduction,
			origin:     originAnotherServer,
			note:       []string{"another server, also called Club"},
			want:       restoreChanges{LinksAdded: []string{"far"}},
		},
		{
			name:     "a backup from before identifiers, which is not a match",
			running:  named(idTest, "Club", nil, nil, nil),
			incoming: named("", "Club", nil, nil, nil),
			origin:   originUnknown,
			note:     []string{"does not say which server made it"},
		},
		{
			name:     "this server has no identifier yet",
			running:  named("", "Club", nil, nil, nil),
			incoming: named(idProduction, "Club", nil, nil, nil),
			origin:   originUnknown,
			note:     []string{"cannot tell"},
		},
		{
			name:       "the identifier beside the configuration is the one used",
			running:    named(idTest, "Club", nil, nil, nil),
			incoming:   named("", "Other", nil, nil, nil),
			incomingID: idProduction,
			origin:     originAnotherServer,
			note:       []string{"made by Other"},
		},
		{
			name:     "an unnamed server is called by its identifier",
			running:  named(idTest, "Club", nil, nil, nil),
			incoming: named(idProduction, "", nil, nil, nil),
			origin:   originAnotherServer,
			note:     []string{"made by the server 01234567"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := compareRestore(tc.running, tc.incoming, tc.incomingID)
			if got.Origin != tc.origin {
				t.Errorf("origin %q, want %q (%s)", got.Origin, tc.origin, got.OriginNote)
			}
			for _, must := range tc.note {
				if !strings.Contains(got.OriginNote, must) {
					t.Errorf("the note does not say %q: %s", must, got.OriginNote)
				}
			}
			for _, pair := range []struct {
				what      string
				got, want []string
			}{
				{"links removed", got.LinksRemoved, tc.want.LinksRemoved},
				{"links added", got.LinksAdded, tc.want.LinksAdded},
				{"bridges removed", got.BridgesRemoved, tc.want.BridgesRemoved},
				{"bridges added", got.BridgesAdded, tc.want.BridgesAdded},
				{"stations removed", got.StationsRemoved, tc.want.StationsRemoved},
				{"stations added", got.StationsAdded, tc.want.StationsAdded},
			} {
				if !slices.Equal(pair.got, pair.want) {
					t.Errorf("%s: %q, want %q", pair.what, pair.got, pair.want)
				}
			}
		})
	}
}

// TestTheIdentityWarningIsNotGivenForThisServersOwnBackup: the general
// warning about two servers claiming one identity stays for every backup that
// is not this server's own, and only there is it replaced.
func TestTheIdentityWarningIsNotGivenForThisServersOwnBackup(t *testing.T) {
	const general = "two servers will claim one identity"
	for _, tc := range []struct {
		origin string
		want   string
	}{
		{originThisServer, "keeps this server's identity"},
		{originAnotherServer, general},
		{originUnknown, general},
	} {
		if got := identityNote(tc.origin, general); !strings.Contains(got, tc.want) {
			t.Errorf("%s: %q, want it to say %q", tc.origin, got, tc.want)
		}
	}
}

// TestBothRestoresSayWhereTheBackupCameFrom goes through the endpoints: a
// backup taken on one server and read on another, and one read back on the
// server that made it, for the shareable restore and the full one.
//
// To see it fail: remove `"changes": changes,` from either handler's 428
// answer, and that restore's cases fail by name.
func TestBothRestoresSayWhereTheBackupCameFrom(t *testing.T) {
	production := named(idProduction, "QSP Server Prod", nil, nil, nil)
	testServer := named(idTest, "QSP Test Server", nil, nil, nil)
	withLinks := testServer
	withLinks.DMR.Upstreams = []config.Upstream{{Name: "production"}, {Name: "BCARA"}}

	for _, full := range []bool{false, true} {
		kind := "shareable"
		if full {
			kind = "full"
		}
		for _, tc := range []struct {
			name      string
			from      config.Config
			onto      config.Config
			origin    string
			removed   []string
			identity  string
			notInNote string
		}{
			{
				name:     "production's backup read on the test server",
				from:     production,
				onto:     withLinks,
				origin:   originAnotherServer,
				removed:  []string{"BCARA", "production"},
				identity: "two servers",
			},
			{
				name:      "the test server's backup read on itself",
				from:      testServer,
				onto:      testServer,
				origin:    originThisServer,
				identity:  "keeps this server's identity",
				notInNote: "two servers",
			},
		} {
			t.Run(kind+"/"+tc.name, func(t *testing.T) {
				source := newStubConfig()
				source.current = tc.from
				srcSrv, srcAuth := newFullBackupServer(t, source, &stubSecrets{values: map[string]string{}})
				file := backupVia(t, srcSrv, srcAuth, full)

				target := newStubConfig()
				target.current = tc.onto
				srv, a := newFullBackupServer(t, target, &stubSecrets{values: map[string]string{}})
				code, body := restoreVia(t, srv, a, full, file, false)
				if code != http.StatusPreconditionRequired {
					t.Fatalf("an unconfirmed restore gave %d: %s", code, body)
				}
				var answer struct {
					Identity string         `json:"identity"`
					Changes  restoreChanges `json:"changes"`
				}
				if err := json.Unmarshal([]byte(body), &answer); err != nil {
					t.Fatalf("decoding %s: %v", body, err)
				}
				if answer.Changes.Origin != tc.origin {
					t.Errorf("origin %q, want %q: %s", answer.Changes.Origin, tc.origin, body)
				}
				if !slices.Equal(answer.Changes.LinksRemoved, tc.removed) {
					t.Errorf("links removed %q, want %q", answer.Changes.LinksRemoved, tc.removed)
				}
				if !strings.Contains(answer.Identity, tc.identity) {
					t.Errorf("the identity paragraph does not say %q: %s", tc.identity, answer.Identity)
				}
				if tc.notInNote != "" && strings.Contains(answer.Identity, tc.notInNote) {
					t.Errorf("the identity paragraph still says %q: %s", tc.notInNote, answer.Identity)
				}
				if len(target.saved) != 0 {
					t.Error("an unconfirmed restore saved a configuration")
				}
			})
		}
	}
}

// TestTheHistoryNamesTheServerABackupCameFrom: the version a restore saves
// says which server made the backup when it was not this one, which is what
// the History page would have needed to show on 2026-10-18.
func TestTheHistoryNamesTheServerABackupCameFrom(t *testing.T) {
	for _, tc := range []struct {
		origin string
		want   string
	}{
		{originAnotherServer, "restored from a full backup of 2026-10-18 made by QSP Server Prod"},
		{originThisServer, "restored from a full backup of 2026-10-18"},
		{originUnknown, "restored from a full backup of 2026-10-18"},
	} {
		got := restoredFrom("restored from a full backup of 2026-10-18",
			restoreChanges{Origin: tc.origin}, named(idProduction, "QSP Server Prod", nil, nil, nil))
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.origin, got, tc.want)
		}
	}
}
