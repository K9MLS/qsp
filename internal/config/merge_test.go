package config

import (
	"maps"
	"slices"
	"testing"
)

// What is saved when a page posts the whole configuration having changed
// part of it, for what happened elsewhere while the page was open.
//
// Break it: take the page's value whatever the others did, as a plain save
// does, and every "elsewhere" row loses the other change; take the current
// value whatever the page did, and the page's own change is lost; blend two
// lists, and the list row stops being a conflict.
func TestMergeKeepsWhatAPageDidNotChange(t *testing.T) {
	type edit func(c *Config)
	none := func(*Config) {}
	tests := []struct {
		name      string
		mine      edit // what the page changed
		theirs    edit // what changed elsewhere meanwhile
		check     func(t *testing.T, got Config)
		kept      []string
		conflicts []string
	}{
		{
			name: "nothing happened elsewhere",
			mine: func(c *Config) { c.DMR.Forwarding = !c.DMR.Forwarding }, theirs: none,
			check: func(t *testing.T, got Config) {
				if got.DMR.Forwarding == Default().DMR.Forwarding {
					t.Error("the page's change was not saved")
				}
			},
		},
		{
			name: "the page changed nothing, and a setting changed elsewhere",
			mine: none, theirs: func(c *Config) { c.Weather.Talkgroup = 3100 },
			check: func(t *testing.T, got Config) {
				if got.Weather.Talkgroup != 3100 {
					t.Error("a change made elsewhere was undone")
				}
			},
			kept: []string{"weather.talkgroup"},
		},
		{
			name:   "different settings in different sections",
			mine:   func(c *Config) { c.P25Repeaters.Site = 9 },
			theirs: func(c *Config) { c.Weather.Talkgroup = 3100 },
			check: func(t *testing.T, got Config) {
				if got.P25Repeaters.Site != 9 || got.Weather.Talkgroup != 3100 {
					t.Errorf("site %d, talkgroup %d: want both changes", got.P25Repeaters.Site, got.Weather.Talkgroup)
				}
			},
			kept: []string{"weather.talkgroup"},
		},
		{
			name:   "different settings in the same section",
			mine:   func(c *Config) { c.P25Repeaters.Site = 9 },
			theirs: func(c *Config) { c.P25Repeaters.PresentAs = "console" },
			check: func(t *testing.T, got Config) {
				if got.P25Repeaters.Site != 9 || got.P25Repeaters.PresentAs != "console" {
					t.Errorf("%+v: want both changes", got.P25Repeaters)
				}
			},
			kept: []string{"p25_repeaters.present_as"},
		},
		{
			name:   "a link added elsewhere survives a save from a page open before it",
			mine:   func(c *Config) { c.DMR.Forwarding = !c.DMR.Forwarding },
			theirs: func(c *Config) { c.DMR.Upstreams = append(c.DMR.Upstreams, Upstream{Name: "a link"}) },
			check: func(t *testing.T, got Config) {
				if len(got.DMR.Upstreams) != 1 {
					t.Errorf("%d links after the save, want the one added elsewhere", len(got.DMR.Upstreams))
				}
			},
			kept: []string{"dmr.upstreams"},
		},
		{
			name:   "the same setting, to the same value",
			mine:   func(c *Config) { c.Weather.Talkgroup = 3100 },
			theirs: func(c *Config) { c.Weather.Talkgroup = 3100 },
			check: func(t *testing.T, got Config) {
				if got.Weather.Talkgroup != 3100 {
					t.Error("two changes that agree were not saved")
				}
			},
		},
		{
			name:      "the same setting, to different values",
			mine:      func(c *Config) { c.Weather.Talkgroup = 3100 },
			theirs:    func(c *Config) { c.Weather.Talkgroup = 3200 },
			conflicts: []string{"weather.talkgroup"},
		},
		{
			name:      "the same list, changed on both sides",
			mine:      func(c *Config) { c.P25Repeaters.AllowedRouters = []string{"192.0.2.4"} },
			theirs:    func(c *Config) { c.P25Repeaters.AllowedRouters = []string{"192.0.2.9"} },
			conflicts: []string{"p25_repeaters.allowed_routers"},
		},
		{
			name:   "a setting the page set where there was none",
			mine:   func(c *Config) { c.P25Repeaters.HoldMS = new(120) },
			theirs: func(c *Config) { c.Weather.Talkgroup = 3100 },
			check: func(t *testing.T, got Config) {
				if got.P25Repeaters.HoldMS == nil || *got.P25Repeaters.HoldMS != 120 {
					t.Error("a setting added by the page was not saved")
				}
			},
			kept: []string{"weather.talkgroup"},
		},
		{
			name: "a setting removed elsewhere stays removed",
			mine: func(c *Config) { c.Weather.Talkgroup = 3100 },
			theirs: func(c *Config) {
				c.P25Repeaters.RecordDir = ""
			},
			check: func(t *testing.T, got Config) {
				if got.P25Repeaters.RecordDir != "" {
					t.Error("a setting cleared elsewhere came back")
				}
			},
			kept: []string{"p25_repeaters.record_dir"},
		},
		{
			name:   "two settings in a section neither document had yet",
			mine:   func(c *Config) { c.Weather.Talkgroup = 3100 },
			theirs: func(c *Config) { c.Weather.Contact = "k9mls@example.org" },
			check: func(t *testing.T, got Config) {
				if got.Weather.Talkgroup != 3100 || got.Weather.Contact != "k9mls@example.org" {
					t.Errorf("%+v: want both changes", got.Weather)
				}
			},
			kept: []string{"weather.contact"},
		},
		{
			name:   "one turns a section on while the other sets something in it",
			mine:   func(c *Config) { c.Weather.Enabled = true },
			theirs: func(c *Config) { c.Weather.Talkgroup = 3100 },
			check: func(t *testing.T, got Config) {
				if !got.Weather.Enabled || got.Weather.Talkgroup != 3100 {
					t.Errorf("%+v: want both changes", got.Weather)
				}
			},
			kept: []string{"weather.talkgroup"},
		},
		{
			name:      "no hold and a hold of nothing are different settings",
			mine:      func(c *Config) { c.P25Repeaters.HoldMS = new(0) },
			theirs:    func(c *Config) { c.P25Repeaters.HoldMS = new(120) },
			conflicts: []string{"p25_repeaters.hold_ms"},
		},
		{
			name:   "a hold of nothing set by the page is saved, not read as untouched",
			mine:   func(c *Config) { c.P25Repeaters.HoldMS = new(0) },
			theirs: none,
			check: func(t *testing.T, got Config) {
				if got.P25Repeaters.HoldMS == nil || *got.P25Repeaters.HoldMS != 0 {
					t.Error("a hold of 0 was lost; absent is the default of 60, which is not what was asked")
				}
			},
		},
		{
			name:   "an empty list the page sent where the file had none is not a change",
			mine:   func(c *Config) { c.P25Repeaters.AllowedRouters = []string{} },
			theirs: func(c *Config) { c.P25Repeaters.AllowedRouters = []string{"192.0.2.9"} },
			check: func(t *testing.T, got Config) {
				if len(got.P25Repeaters.AllowedRouters) != 1 {
					t.Error("a router allowed elsewhere was removed by a page that never touched the list")
				}
			},
			kept: []string{"p25_repeaters.allowed_routers"},
		},
		{
			name:   "a zero the page filled in where a field was left out is not a change",
			mine:   func(c *Config) { c.P25Repeaters.Site = 0 },
			theirs: func(c *Config) { c.P25Repeaters.Site = 7 },
			check: func(t *testing.T, got Config) {
				if got.P25Repeaters.Site != 7 {
					t.Errorf("site %d: the page's untouched zero undid a change made elsewhere", got.P25Repeaters.Site)
				}
			},
			kept: []string{"p25_repeaters.site"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := Default()
			base.P25Repeaters = P25Repeaters{Enabled: true, ListenAddress: "0.0.0.0:1994", RecordDir: "/var/lib/qsp/r"}
			mine, current := base.Clone(), base.Clone()
			tc.mine(&mine)
			tc.theirs(&current)

			got, kept, conflicts, err := Merge(base, mine, current)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if !slices.Equal(conflicts, tc.conflicts) {
				t.Fatalf("conflicts %v, want %v", conflicts, tc.conflicts)
			}
			if !slices.Equal(kept, tc.kept) {
				t.Errorf("kept %v, want %v", kept, tc.kept)
			}
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

// With nothing changed anywhere, what comes out is what went in: the merge
// goes through JSON and back and must lose nothing on the way.
func TestMergingNothingChangesNothing(t *testing.T) {
	base := Default()
	base.DMR.Upstreams = []Upstream{{Name: "one"}, {Name: "two"}}
	base.P25Repeaters = P25Repeaters{Enabled: true, ListenAddress: "0.0.0.0:1994", HoldMS: new(0)}

	got, kept, conflicts, err := Merge(base, base.Clone(), base.Clone())
	if err != nil || len(kept) != 0 || len(conflicts) != 0 {
		t.Fatalf("Merge: %v, kept %v, conflicts %v", err, kept, conflicts)
	}
	changes, err := Diff(base, got)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("merging nothing changed %v", changes)
	}
}

// paths is every setting in a tree, by its path.
func paths(prefix string, m map[string]any, out map[string]bool) {
	for k, v := range m {
		p := k
		if prefix != "" {
			p = prefix + "." + k
		}
		out[p] = true
		if nested, ok := v.(map[string]any); ok {
			paths(p, nested, out)
		}
	}
}

// whole walks the configuration's types itself, where everything else asks
// encoding/json. If it named a setting differently, or passed one over, a
// merge would drop that setting from every save made through a page.
//
// Break it: skip a field in wholeValue, or take its Go name where the file
// uses a tag.
func TestWholeLeavesOutNothingTheFileWrites(t *testing.T) {
	c := Default()
	c.DMR.Upstreams = []Upstream{{Name: "one", Identity: &UpstreamIdentity{}}}
	c.DMR.Access = &Access{}
	c.P25Repeaters = P25Repeaters{Enabled: true, ListenAddress: "0.0.0.0:1994",
		AllowedRouters: []string{"192.0.2.4"}, RecordDir: "/r", Site: 2, PresentAs: "console",
		SendHeader: true, HoldMS: new(0)}
	c.Weather.Enabled, c.Weather.Zones, c.Weather.Talkgroup = true, []string{"TXC121"}, 3100

	file, err := toMap(c)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := whole(c)
	if err != nil {
		t.Fatal(err)
	}
	written, held := map[string]bool{}, map[string]bool{}
	paths("", file, written)
	paths("", tree, held)
	if len(written) < 40 {
		t.Fatalf("only %d settings in the file's encoding; this check is not checking much", len(written))
	}
	for _, p := range slices.Sorted(maps.Keys(written)) {
		if !held[p] {
			t.Errorf("the file writes %q and the merge does not see it", p)
		}
	}

	// And back: what the tree holds is a configuration, and the same one.
	got, kept, conflicts, err := Merge(c, c.Clone(), c.Clone())
	if err != nil || len(kept)+len(conflicts) != 0 {
		t.Fatalf("Merge: %v, kept %v, conflicts %v", err, kept, conflicts)
	}
	if changes, _ := Diff(c, got); len(changes) != 0 {
		t.Errorf("through the merge and back changed %v", changes)
	}
}

// TestAMapNobodyHadIsAnEmptyOne. The Access page sends repeater names as {}
// on a server that has none, and the file holds no map at all. The two were
// compared as different values, so the page was taken to have changed the
// names, and a repeater named meanwhile on the Network page made the Access
// page's save a conflict about a setting it had not touched (2026-10-07, H1).
//
// To see it fail: remove the nil-map case from wholeValue.
func TestAMapNobodyHadIsAnEmptyOne(t *testing.T) {
	for _, tc := range []struct {
		name         string
		base, mine   map[uint32]string
		current      map[uint32]string
		want         map[uint32]string
		conflictFree bool
	}{
		{"named elsewhere, the page sent {}", nil, map[uint32]string{},
			map[uint32]string{313291: "Tower"}, map[uint32]string{313291: "Tower"}, true},
		{"the page named one, another was named elsewhere", nil, map[uint32]string{313292: "Barn"},
			map[uint32]string{313291: "Tower"}, map[uint32]string{313291: "Tower", 313292: "Barn"}, true},
		{"both named the same repeater differently", nil, map[uint32]string{313291: "Barn"},
			map[uint32]string{313291: "Tower"}, nil, false},
		{"the page removed one", map[uint32]string{313291: "Tower"}, map[uint32]string{},
			map[uint32]string{313291: "Tower"}, map[uint32]string{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, mine, current := Default(), Default(), Default()
			base.IPSC.PeerNames, mine.IPSC.PeerNames, current.IPSC.PeerNames = tc.base, tc.mine, tc.current
			mine.DMR.Forwarding = !base.DMR.Forwarding

			got, _, conflicts, err := Merge(base, mine, current)
			if err != nil {
				t.Fatalf("Merge: %v", err)
			}
			if tc.conflictFree != (len(conflicts) == 0) {
				t.Fatalf("conflicts %v, want none: %v", conflicts, tc.conflictFree)
			}
			if !tc.conflictFree {
				return
			}
			if len(got.IPSC.PeerNames) != len(tc.want) {
				t.Errorf("the names are %v, want %v", got.IPSC.PeerNames, tc.want)
			}
			for id, name := range tc.want {
				if got.IPSC.PeerNames[id] != name {
					t.Errorf("the names are %v, want %v", got.IPSC.PeerNames, tc.want)
				}
			}
			if got.DMR.Forwarding == base.DMR.Forwarding {
				t.Error("the page's own change was not saved")
			}
		})
	}
}

// TestASectionNobodyHadStaysOut. A page sends an access block whether or not
// the server has one. Merged, a server with none was saved with an empty one,
// which permits exactly the same, and which silenced the startup warning that
// a listener reachable from beyond this host has no access block (H2).
//
// To see it fail: remove the empty(merged) test from mergeMaps.
func TestASectionNobodyHadStaysOut(t *testing.T) {
	base, current := Default(), Default()
	base.DMR.Access, current.DMR.Access = nil, nil

	sentEmpty := Default()
	sentEmpty.DMR.Access = &Access{}
	sentEmpty.DMR.Forwarding = !base.DMR.Forwarding
	got, _, conflicts, err := Merge(base, sentEmpty, current)
	if err != nil || len(conflicts) > 0 {
		t.Fatalf("Merge: %v %v", err, conflicts)
	}
	if got.DMR.Access != nil {
		t.Errorf("a server with no access block was given one: %+v", *got.DMR.Access)
	}

	// One the page filled in is kept, of course.
	sentFull := sentEmpty.Clone()
	sentFull.DMR.Access = &Access{Registration: ACL{Mode: "deny", IDs: []string{"3132913"}}}
	got, _, _, err = Merge(base, sentFull, current)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.DMR.Access == nil || got.DMR.Access.Registration.Mode != "deny" {
		t.Errorf("the access block the page filled in was lost: %+v", got.DMR.Access)
	}

	// And an empty one a server already has stays.
	had := Default()
	had.DMR.Access = &Access{}
	got, _, _, err = Merge(had, sentEmpty, had.Clone())
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if got.DMR.Access == nil {
		t.Error("an empty access block the server had was taken away")
	}
}
