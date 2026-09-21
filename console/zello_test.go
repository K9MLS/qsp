package console

import (
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/config"
)

// TestTheZelloPageWritesOnlyKeysTheServerReads ties the Zello page's transcoder
// fields to the Go type that decodes them.
//
// **A key the server does not know is dropped without a word.** The page saves
// the whole configuration as JSON, and a field whose tag was renamed on the Go
// side -- `alias` to `talker_alias`, say -- would still be written by the page,
// ignored by the decoder, and reported as saved. The operator would see their
// alias in the box, "1 change saved", and a radio showing a bare ID, with
// nothing anywhere to say why. That is the failure 0422 fixed for the alias
// setting itself, and this is the same shape one layer out.
//
// So every `t.<key> =` in collect() must be a JSON tag on config.Transcoder,
// read by reflection rather than listed here, and the alias must be among them.
//
// To see it fail: rename the Alias field's tag in internal/config, or make the
// page write t.talker_alias.
func TestTheZelloPageWritesOnlyKeysTheServerReads(t *testing.T) {
	src, err := os.ReadFile("static/zello.js")
	if err != nil {
		t.Fatalf("reading the Zello page's script: %v", err)
	}

	body := functionBody(t, string(src), "collect")
	written := map[string]bool{}
	for _, m := range regexp.MustCompile(`\bt\.([a-z_]+)\s*=[^=]`).FindAllStringSubmatch(body, -1) {
		written[m[1]] = true
	}
	if len(written) == 0 {
		t.Fatal("collect() writes no transcoder field; this test would pass by finding nothing")
	}

	tags := jsonTags(reflect.TypeFor[config.Transcoder]())

	tests := []struct {
		name  string
		check func() []string
	}{
		{"every key the page writes is one the server decodes", func() []string {
			var bad []string
			for key := range written {
				if !tags[key] {
					bad = append(bad, key+" is written by the page and decoded by nothing")
				}
			}
			return bad
		}},
		{"the alias is one of them", func() []string {
			if !written["alias"] {
				return []string{"the page does not write the alias, so the field on it saves nothing"}
			}
			if !tags["alias"] {
				return []string{"config.Transcoder has no alias tag for the page to write"}
			}
			return nil
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			problems := tc.check()
			sort.Strings(problems)
			for _, p := range problems {
				t.Error(p)
			}
		})
	}
}

// TestTheAliasFieldIsOnThePageAndReadBack checks the field exists in the markup
// and that render() fills it, so a saved alias is visible when the page is next
// opened rather than looking like it was never set.
func TestTheAliasFieldIsOnThePageAndReadBack(t *testing.T) {
	html, err := os.ReadFile("static/zello.html")
	if err != nil {
		t.Fatalf("reading the Zello page: %v", err)
	}
	js, err := os.ReadFile("static/zello.js")
	if err != nil {
		t.Fatalf("reading the Zello page's script: %v", err)
	}

	tests := []struct {
		name string
		ok   bool
		why  string
	}{
		{"the input exists", strings.Contains(string(html), `id="zello-alias"`),
			"there is no zello-alias input on the page"},
		{"it is capped at what the standard can state", strings.Contains(string(html), `maxlength="31"`),
			"the alias input does not stop at 31 characters"},
		{"render() reads it back", strings.Contains(functionBody(t, string(js), "render"), "alias.value = t.alias"),
			"render() does not fill the field, so a saved alias looks unset"},
		{"collect() writes it", strings.Contains(functionBody(t, string(js), "collect"), "t.alias ="),
			"collect() does not write the field, so saving it does nothing"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.ok {
				t.Error(tc.why)
			}
		})
	}
}

// functionBody returns the source of a top-level `function name(` in a script,
// from its opening brace to the matching close.
func functionBody(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "function "+name+"(")
	if start < 0 {
		t.Fatalf("no function %s in the script", name)
	}
	open := strings.Index(src[start:], "{")
	if open < 0 {
		t.Fatalf("function %s has no body", name)
	}
	depth := 0
	for i := start + open; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[start : i+1]
			}
		}
	}
	t.Fatalf("function %s never closes", name)
	return ""
}

// jsonTags returns the JSON names a struct decodes.
func jsonTags(typ reflect.Type) map[string]bool {
	out := map[string]bool{}
	for i := range typ.NumField() {
		tag := typ.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name != "" && name != "-" {
			out[name] = true
		}
	}
	return out
}
