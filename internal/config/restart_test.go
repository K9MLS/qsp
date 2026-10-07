package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// change alters the one setting at path, by whatever its kind allows, and
// reports whether it found it.
func change(t *testing.T, cfg *Config, path string) {
	t.Helper()
	found := false
	alter(reflect.ValueOf(cfg).Elem(), "", path, &found)
	if !found {
		t.Fatalf("no setting %s to change", path)
	}
}

// alter is walkSettings over a value that can be written to.
func alter(v reflect.Value, at, path string, found *bool) {
	t := v.Type()
	if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
		if v.IsNil() {
			v.Set(reflect.New(t.Elem()))
		}
		v = v.Elem()
		t = v.Type()
	}
	if t.Kind() != reflect.Struct {
		if at != path {
			return
		}
		*found = true
		switch t.Kind() {
		case reflect.Bool:
			v.SetBool(!v.Bool())
		case reflect.String:
			v.SetString(v.String() + "x")
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			v.SetInt(v.Int() + 1)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			v.SetUint(v.Uint() + 1)
		case reflect.Float32, reflect.Float64:
			v.SetFloat(v.Float() + 1)
		case reflect.Slice:
			v.Set(reflect.Append(v, reflect.Zero(t.Elem())))
		case reflect.Map:
			m := reflect.MakeMap(t)
			for _, k := range v.MapKeys() {
				m.SetMapIndex(k, v.MapIndex(k))
			}
			m.SetMapIndex(reflect.New(t.Key()).Elem(), reflect.Zero(t.Elem()))
			v.Set(m)
		case reflect.Pointer:
			// A number that may be absent: another number than it had.
			next := reflect.New(t.Elem())
			if !v.IsNil() {
				next.Elem().Set(v.Elem())
			}
			switch t.Elem().Kind() {
			case reflect.Int:
				next.Elem().SetInt(next.Elem().Int() + 7)
			default:
				next.Elem().SetUint(next.Elem().Uint() + 7)
			}
			v.Set(next)
		default:
			panic("a setting of a kind this test cannot change: " + at + " " + t.String())
		}
		return
	}
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		if at != "" {
			name = at + "." + name
		}
		alter(v.Field(i), name, path, found)
	}
}

// TestEverySettingSaysWhenItTakesEffect. **This is the test that fails when a
// setting is added and nobody decides.** Until 0.1.335 the list of settings
// that need a restart was written by hand, and ten that nobody had written
// were reported as in force the moment they were saved.
//
// To see it fail: add a field to any struct in config.go, or delete a line
// from either list in restart.go.
func TestEverySettingSaysWhenItTakesEffect(t *testing.T) {
	paths := SettingPaths()
	if len(paths) < 90 {
		t.Fatalf("only %d settings were found; the walk is not reaching them", len(paths))
	}
	lists := map[string][]string{
		"appliedOnSave": appliedOnSave, "appliedAtStart": appliedAtStart, "toldToLinks": toldToLinks,
	}
	for _, path := range paths {
		var in []string
		for name, list := range lists {
			if _, ok := covering(list, path); ok {
				in = append(in, name)
			}
		}
		slices.Sort(in)
		switch len(in) {
		case 1:
		case 0:
			t.Errorf("%s is in no list in restart.go: decide whether a running server takes it "+
				"up when it is saved, and say so there", path)
		default:
			t.Errorf("%s is in %v; it takes effect at one time, not two", path, in)
		}
	}
	// And no line that names nothing, left behind when a setting was renamed.
	for name, list := range lists {
		for _, entry := range list {
			if !slices.ContainsFunc(paths, func(p string) bool {
				return p == entry || strings.HasPrefix(p, entry+".")
			}) {
				t.Errorf("%s names %q, and there is no such setting", name, entry)
			}
		}
	}
}

// TestASettingReadAtStartIsNamedWhenItChanges changes every setting there is,
// one at a time, and asks what a restart is needed for. A setting applied at
// start is named, by its own name and alone; one applied on save is not.
//
// To see it fail: in NeedsRestart, skip any path, or `continue` for one that
// is not live.
func TestASettingReadAtStartIsNamedWhenItChanges(t *testing.T) {
	for _, path := range SettingPaths() {
		t.Run(path, func(t *testing.T) {
			base := Default()
			after := base.Clone()
			change(t, &after, path)

			got := NeedsRestart(base, after)
			live, known := AppliedOnSave(path)
			if !known {
				t.Skip("in no list; TestEverySettingSaysWhenItTakesEffect reports it")
			}
			if live && len(got) != 0 {
				t.Errorf("a change to %s is applied on save, and a restart was asked for: %v", path, got)
			}
			if !live && !slices.Equal(got, []string{path}) {
				t.Errorf("a change to %s is not applied until a restart, and NeedsRestart said %v", path, got)
			}
			if again := NeedsRestart(after, after); len(again) != 0 {
				t.Errorf("nothing changed and a restart was asked for: %v", again)
			}
		})
	}
}

// TestWhatALinkWasToldNeedsARestartOnlyWithALinkToTell. The pin moved on this
// server's map at once and stayed where it was on its neighbour's, with
// nothing to say a restart would move it (G8).
//
// To see it fail: have dialsALink return false, or true.
func TestWhatALinkWasToldNeedsARestartOnlyWithALinkToTell(t *testing.T) {
	dialled := Upstream{Name: "north", Enabled: true, Protocol: UpstreamQSP}
	for _, tc := range []struct {
		name  string
		links []Upstream
		want  bool
	}{
		{"no links", nil, false},
		{"a link this server dials", []Upstream{dialled}, true},
		{"the same link switched off", []Upstream{{Name: "north", Protocol: UpstreamQSP}}, false},
		{"a link that announces nothing", []Upstream{{Name: "ob", Enabled: true, Protocol: "openbridge"}}, false},
	} {
		for _, path := range []string{"dmr.identity.latitude", "dmr.identity.callsign", "dmr.join.network_name"} {
			t.Run(tc.name+"/"+path, func(t *testing.T) {
				base := Default()
				base.DMR.Upstreams = tc.links
				after := base.Clone()
				change(t, &after, path)

				got := NeedsRestart(base, after)
				if tc.want && !slices.Equal(got, []string{path}) {
					t.Errorf("the far end still has the old %s, and NeedsRestart said %v", path, got)
				}
				if !tc.want && len(got) != 0 {
					t.Errorf("nobody is left to tell, and a restart was asked for: %v", got)
				}
			})
		}
	}
}

// TestAnEmptyListIsOneValue. A configuration read from a file has `[]` where
// one built in the program has nothing.
//
// To see it fail: remove the Len() == 0 case from Settings.
func TestAnEmptyListIsOneValue(t *testing.T) {
	a, b := Default(), Default()
	a.DMR.Upstreams, b.DMR.Upstreams = nil, []Upstream{}
	a.P25.AllowedCallsigns, b.P25.AllowedCallsigns = nil, []string{}
	a.IPSC.PeerNames, b.IPSC.PeerNames = nil, map[uint32]string{}
	if got := NeedsRestart(a, b); len(got) != 0 {
		t.Errorf("nothing and an empty list were told apart: %v", got)
	}
	// And the hold left out is the default, not a change from it.
	held := DefaultRepeaterHoldMS
	b.P25Repeaters.HoldMS = &held
	if got := NeedsRestart(a, b); len(got) != 0 {
		t.Errorf("the default hold spelled out was called a change: %v", got)
	}
}
