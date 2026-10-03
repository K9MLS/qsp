package config

import (
	"fmt"
	"reflect"
	"testing"
)

// fillValue puts a distinct non-zero value in every exported field under v,
// making each slice, map and pointer it meets.
func fillValue(v reflect.Value, n *int) {
	*n++
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(*n%100 + 1))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(*n%100 + 1))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(*n) + 0.5)
	case reflect.String:
		v.SetString(fmt.Sprintf("value-%d", *n))
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillValue(v.Elem(), n)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 2, 2))
		for i := range v.Len() {
			fillValue(v.Index(i), n)
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		key := reflect.New(v.Type().Key()).Elem()
		fillValue(key, n)
		elem := reflect.New(v.Type().Elem()).Elem()
		fillValue(elem, n)
		v.SetMapIndex(key, elem)
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillValue(v.Field(i), n)
			}
		}
	}
}

// shared walks two values together and reports every slice, map and pointer
// the second still shares with the first.
func shared(path string, a, b reflect.Value, out *[]string) {
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		shared(path, a.Elem(), b.Elem(), out)
	case reflect.Slice:
		if a.Len() == 0 || b.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		for i := range min(a.Len(), b.Len()) {
			shared(fmt.Sprintf("%s[%d]", path, i), a.Index(i), b.Index(i), out)
		}
	case reflect.Map:
		if a.Len() == 0 || b.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
		}
	case reflect.Struct:
		for i := range a.NumField() {
			shared(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i), out)
		}
	}
}

// A clone is equal to its original and shares nothing with it.
//
// **Every field, found by reflection rather than listed**, so a slice, map or
// pointer added to the configuration later fails here until Clone copies it —
// the alternative being a list in a test that is exactly as easy to forget as
// the line in Clone.
//
// To see it fail: delete any one line of Clone, for instance
// `out.DMR.Upstreams = slices.Clone(c.DMR.Upstreams)`.
func TestACloneSharesNothingWithItsOriginal(t *testing.T) {
	var original Config
	n := 0
	fillValue(reflect.ValueOf(&original).Elem(), &n)
	// Not reachable by reflection, and what a JSON round trip would lose.
	original.DMR.Access.openedAllowOnly = true
	original.DMR.Access.allowOnlyNamed = 7

	clone := original.Clone()

	if !reflect.DeepEqual(original, clone) {
		t.Fatalf("the clone differs from its original:\n%+v\n%+v", original, clone)
	}
	var aliases []string
	shared("Config", reflect.ValueOf(original), reflect.ValueOf(clone), &aliases)
	for _, path := range aliases {
		t.Errorf("%s is shared between a configuration and its clone", path)
	}
}

// A clone keeps the difference between a list that is empty and one that is
// absent, which Diff and the JSON document both show.
//
// To see it fail: build the clone with a JSON round trip, or replace a
// slices.Clone in Clone with append([]string(nil), ...).
func TestACloneKeepsEmptyAndAbsentApart(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
	}{
		{"absent", nil},
		{"empty", []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			c.DMR.Access = &Access{Registration: ACL{Mode: "deny", IDs: tc.in}}
			c.Weather.Zones = tc.in
			got := c.Clone()
			if (got.DMR.Access.Registration.IDs == nil) != (tc.in == nil) {
				t.Errorf("registration IDs came back as %#v", got.DMR.Access.Registration.IDs)
			}
			if (got.Weather.Zones == nil) != (tc.in == nil) {
				t.Errorf("weather zones came back as %#v", got.Weather.Zones)
			}
		})
	}
}
