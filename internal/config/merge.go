package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// Merge applies to current only what mine changed from base.
//
// **A console page is given the whole configuration and posts the whole of it
// back**, having changed its own part. Saved as posted, it put every other
// setting back to what it was when the page was opened: a change made on
// another page in another tab, by another administrator, or by the server
// itself when a link was accepted. Nothing said so.
//
// So a page sends base, the document it was given, beside mine, the document
// it wants. A setting mine left as base had it takes whatever current has. A
// setting mine changed is taken from mine, unless current changed it too, to
// something else: that is a conflict, it is named, and the caller saves
// nothing.
//
// kept names the settings that differ between base and current and that mine
// did not touch: what a plain save would have undone.
//
// **A list is one setting.** Two changes to the same list are a conflict,
// never a blend of the two, because QSP cannot know whether an entry missing
// from one side was removed there or added on the other.
//
// The three are compared by the paths Diff reports, with every setting
// present whether or not the file would write it (see whole), so a zero a
// page filled in where the document left a field out is not a change.
func Merge(base, mine, current Config) (merged Config, kept, conflicts []string, err error) {
	b, err := whole(base)
	if err != nil {
		return Config{}, nil, nil, err
	}
	m, err := whole(mine)
	if err != nil {
		return Config{}, nil, nil, err
	}
	c, err := whole(current)
	if err != nil {
		return Config{}, nil, nil, err
	}

	out := mergeMaps("", b, m, c, &kept, &conflicts)
	slices.Sort(kept)
	slices.Sort(conflicts)
	if len(conflicts) > 0 {
		return Config{}, kept, conflicts, nil
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return Config{}, nil, nil, fmt.Errorf("cannot encode the merged configuration: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&merged); err != nil {
		return Config{}, nil, nil, fmt.Errorf("cannot read the merged configuration back: %w", err)
	}
	return merged, kept, nil, nil
}

func mergeMaps(prefix string, base, mine, current map[string]any, kept, conflicts *[]string) map[string]any {
	keys := make(map[string]bool, len(mine)+len(current))
	for _, m := range []map[string]any{base, mine, current} {
		for k := range m {
			keys[k] = true
		}
	}

	out := make(map[string]any, len(keys))
	for k := range keys {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		bv, inBase := base[k]
		mv, inMine := mine[k]
		cv, inCurrent := current[k]

		// Objects are merged setting by setting. The only object that can be
		// missing from one side is one held by a pointer, and there a missing
		// one and an empty one mean the same, so it counts as empty. An
		// object on one side and a value on the other is one setting, like a
		// list.
		bNested, bIsMap := bv.(map[string]any)
		mNested, mIsMap := mv.(map[string]any)
		cNested, cIsMap := cv.(map[string]any)
		if (mIsMap || cIsMap) && (bIsMap || !inBase) && (mIsMap || !inMine) && (cIsMap || !inCurrent) {
			out[k] = mergeMaps(path, bNested, mNested, cNested, kept, conflicts)
			continue
		}

		same := func(x any, xIn bool, y any, yIn bool) bool {
			if xIn != yIn {
				return false
			}
			// No list and an empty list are the same list: a page sends []
			// where the file has null, and that is not a change.
			return reflect.DeepEqual(x, y) || (noList(x) && noList(y))
		}
		mineChanged := !same(bv, inBase, mv, inMine)
		theirsChanged := !same(bv, inBase, cv, inCurrent)

		take, taken := cv, inCurrent
		switch {
		case !mineChanged:
			if theirsChanged {
				*kept = append(*kept, path)
			}
		case !theirsChanged, same(mv, inMine, cv, inCurrent):
			take, taken = mv, inMine
		default:
			*conflicts = append(*conflicts, path)
		}
		if taken {
			out[k] = take
		}
	}
	return out
}

// whole is a configuration as a tree of its settings with none left out.
//
// **The file's own encoding cannot be merged.** It leaves out what is zero: a
// section that is all defaults is not written at all, and then written in
// full, `"enabled": false` included, the moment one setting in it is set. Two
// pages that each set a different thing in such a section would be read as
// both having changed everything in it, and one of them turning it on as a
// conflict with the other leaving it off.
//
// So every setting is present here under the name the file gives it, zero or
// not. **A pointer that is nil is the one thing left out**, because there
// absent and zero are different settings: no `hold_ms` is the default hold and
// a `hold_ms` of 0 is none.
//
// TestWholeLeavesOutNothingTheFileWrites holds this to the real encoding.
func whole(c Config) (map[string]any, error) {
	v, present, err := wholeValue(reflect.ValueOf(c))
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[string]any)
	if !present || !ok {
		return nil, fmt.Errorf("a configuration did not encode as an object")
	}
	return m, nil
}

var marshalerType = reflect.TypeFor[json.Marshaler]()

func wholeValue(v reflect.Value) (value any, present bool, err error) {
	t := v.Type()
	switch {
	case t.Implements(marshalerType):
		// Says for itself how it is written.
	case t.Kind() == reflect.Pointer:
		if v.IsNil() {
			return nil, false, nil
		}
		return wholeValue(v.Elem())
	case t.Kind() == reflect.Struct:
		out := make(map[string]any, t.NumField())
		for i := range t.NumField() {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			fv, ok, err := wholeValue(v.Field(i))
			if err != nil {
				return nil, false, err
			}
			// A nil pointer the file would write as null is a setting that
			// is there and empty; only one the file leaves out is absent.
			if ok || !strings.Contains(opts, "omit") {
				out[name] = fv
			}
		}
		return out, true, nil
	}

	raw, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, false, fmt.Errorf("cannot encode a setting for merging: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false, fmt.Errorf("cannot decode a setting for merging: %w", err)
	}
	return decoded, true, nil
}

// noList reports whether a decoded value is a list with nothing in it, or no
// list at all.
func noList(v any) bool {
	if v == nil {
		return true
	}
	l, ok := v.([]any)
	return ok && len(l) == 0
}
