package server

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// A page opened, something changing elsewhere, and then the page saved: the
// order that undid the other change until 0.1.319.
//
// Break it: ignore Base in handleSaveConfig, and every row but the last two
// saves the page's document whole and undoes what happened elsewhere.
func TestASaveFromAPageKeepsWhatChangedElsewhere(t *testing.T) {
	type edit func(c *config.Config)
	tests := []struct {
		name      string
		elsewhere edit // after the page was opened
		page      edit // what the page changed
		sendBase  bool
		code      int
		check     func(t *testing.T, saved config.Config)
		conflicts []string
	}{
		{
			name:      "a link accepted while the Network page was open",
			elsewhere: func(c *config.Config) { c.DMR.Upstreams = append(c.DMR.Upstreams, config.Upstream{Name: "a link"}) },
			page:      func(c *config.Config) { c.Events.HistorySize = 512 },
			sendBase:  true, code: http.StatusOK,
			check: func(t *testing.T, saved config.Config) {
				if len(saved.DMR.Upstreams) != 1 {
					t.Errorf("%d links after the save: the one accepted meanwhile was removed", len(saved.DMR.Upstreams))
				}
				if saved.Events.HistorySize != 512 {
					t.Error("the page's own change was not saved")
				}
			},
		},
		{
			name:      "another page saved in another tab",
			elsewhere: func(c *config.Config) { c.Weather.Talkgroup = 3100 },
			page:      func(c *config.Config) { c.Events.HistorySize = 512 },
			sendBase:  true, code: http.StatusOK,
			check: func(t *testing.T, saved config.Config) {
				if saved.Weather.Talkgroup != 3100 || saved.Events.HistorySize != 512 {
					t.Errorf("talkgroup %d, history %d: want both", saved.Weather.Talkgroup, saved.Events.HistorySize)
				}
			},
		},
		{
			name:      "the same setting changed in both places is refused and named",
			elsewhere: func(c *config.Config) { c.Events.HistorySize = 128 },
			page:      func(c *config.Config) { c.Events.HistorySize = 512 },
			sendBase:  true, code: http.StatusConflict,
			conflicts: []string{"events.history_size"},
		},
		{
			name:      "a document sent with no base replaces the configuration, as a restore must",
			elsewhere: func(c *config.Config) { c.Weather.Talkgroup = 3100 },
			page:      func(c *config.Config) { c.Events.HistorySize = 512 },
			sendBase:  false, code: http.StatusOK,
			check: func(t *testing.T, saved config.Config) {
				if saved.Weather.Talkgroup != 0 {
					t.Error("a whole document was merged; restoring a version would no longer restore it")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cm := newStubConfig()
			srv, a := newConfigServer(t, cm, &recordingAudit{})

			opened := cm.Current().Clone() // what the page was given
			tc.elsewhere(&cm.current)
			wanted := opened.Clone()
			tc.page(&wanted)

			req := saveRequest{Config: wanted, Summary: "from a page"}
			if tc.sendBase {
				req.Base = &opened
			}
			body, _ := json.Marshal(req)
			res := authed(t, srv, a, http.MethodPost, "/api/config", string(body))
			if res.Code != tc.code {
				t.Fatalf("returned %d, want %d: %s", res.Code, tc.code, res.Body)
			}

			if tc.code == http.StatusConflict {
				var out conflictResponse
				if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !slices.Equal(out.Conflicts, tc.conflicts) {
					t.Errorf("conflicts %v, want %v", out.Conflicts, tc.conflicts)
				}
				for _, c := range tc.conflicts {
					if !strings.Contains(out.Error, c) {
						t.Errorf("the message does not name %q: %s", c, out.Error)
					}
				}
				if len(cm.saved) != 0 {
					t.Error("something was saved by a save that was refused")
				}
				return
			}
			if len(cm.saved) != 1 {
				t.Fatalf("Save was called %d times", len(cm.saved))
			}
			tc.check(t, cm.saved[0])

			// And what the page is told changed is what it changed, not what
			// happened elsewhere.
			var out saveResponse
			if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if tc.sendBase {
				if len(out.Changes) != 1 || out.Changes[0].Field != "events.history_size" {
					t.Errorf("reported changes %+v, want the page's one", out.Changes)
				}
			}
		})
	}
}
