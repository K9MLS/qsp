package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Retiring a configuration field.
//
// # Why this has to exist before a field can be deleted
//
// `Load` refuses unknown fields, deliberately: a typo in a field name must not
// silently leave the default in place, because the operator would believe a
// setting had been applied when it had not. That rule has no exception for a
// field QSP used to have — so deleting one from the Go struct makes **every
// configuration already written unparseable**, and every server holding one
// fails to start on its next restart.
//
// That is not a theoretical cost. On 2026-09-09 the two servers this project
// runs both had `export` and `import` in their configuration, written by every
// save because the fields carried no `omitempty`. Removing the struct fields
// would have stopped both of them, and the failure would have arrived at a
// restart rather than at the change.
//
// # What a retired field is, and what it is not
//
// **A retired field is one QSP has deliberately removed**, named here so that a
// document containing it still loads. It is dropped rather than migrated: these
// are fields that decided nothing, and a migration implies a new home for a
// value that never had one.
//
// **It is not a way to tolerate a typo.** Anything not on this list is still
// refused, which is the whole point of the rule this preserves.
//
// The next save writes the document without the retired key, so a
// configuration cleans itself up the first time an operator changes anything.

// retired names the fields QSP has removed, as paths from the document root.
//
// A segment ending in `[]` means every element of that array. The list is
// deliberately explicit rather than a pattern: a rule that matched by shape
// would eventually match a field somebody meant to add.
var retired = []string{
	// 0308. Neither list ever routed a frame — no code read them to move
	// traffic; a bridge naming the link is what carried it. They described an
	// intended direction filtering that was never built, and an operator who
	// filled them in and expected audio to cross got none. Removed with
	// OpenBridge narrowed to foreign networks, where the filtering they
	// described belongs to the far end.
	"dmr.upstreams[].export",
	"dmr.upstreams[].import",
}

// stripRetired removes retired fields from a configuration document.
//
// Returns the document unchanged when it contains none, so the common path
// costs one map walk and no re-encoding.
func stripRetired(raw []byte) ([]byte, bool, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		// Not an object, or not JSON at all. Left alone: the strict decode
		// that follows reports it better than this could, in terms about the
		// field that is wrong rather than about the document's shape.
		return raw, false, nil
	}

	removed := false
	for _, path := range retired {
		if removeAt(doc, strings.Split(path, ".")) {
			removed = true
		}
	}
	if !removed {
		return raw, false, nil
	}

	out, err := json.Marshal(doc)
	if err != nil {
		return nil, false, fmt.Errorf("config: rewriting a document without its retired fields: %w", err)
	}
	return out, true, nil
}

// removeAt deletes one path from a decoded document, and reports whether it
// found anything.
func removeAt(node any, path []string) bool {
	if len(path) == 0 {
		return false
	}

	head, rest := path[0], path[1:]

	// **`upstreams[]` is one segment, not two.** Splitting the path on dots
	// leaves the brackets attached to the name they belong to, and the first
	// version of this looked for a bare `[]` segment that never appeared — so
	// nothing was ever stripped and the shipped examples stopped loading. The
	// mechanism silently did nothing, which is the failure it exists to
	// prevent, one layer up.
	if name, isList := strings.CutSuffix(head, "[]"); isList {
		obj, ok := node.(map[string]any)
		if !ok {
			return false
		}
		list, ok := obj[name].([]any)
		if !ok {
			return false
		}
		found := false
		for _, item := range list {
			if removeAt(item, rest) {
				found = true
			}
		}
		return found
	}

	obj, ok := node.(map[string]any)
	if !ok {
		return false
	}
	if len(rest) == 0 {
		if _, present := obj[head]; !present {
			return false
		}
		delete(obj, head)
		return true
	}
	child, present := obj[head]
	if !present {
		return false
	}
	return removeAt(child, rest)
}
