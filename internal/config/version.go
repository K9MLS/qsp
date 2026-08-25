package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Version is one immutable point in a configuration's history.
//
// The blueprint requires that every save produce a snapshot an operator can
// inspect, diff and roll back to. This type is that snapshot's identity and
// metadata; persistence belongs to the storage layer, which is why there are no
// database concerns here.
//
// Number is assigned by the store and increases monotonically. Checksum
// identifies the content, so an operator can tell whether two versions differ
// without reading them, and a rollback can be verified.
type Version struct {
	// Number is the monotonically increasing version identifier.
	Number int64 `json:"number"`
	// CreatedAt is when the version was recorded, in UTC.
	CreatedAt time.Time `json:"created_at"`
	// Author identifies who made the change. It is a username, not a session
	// token or credential.
	Author string `json:"author"`
	// Summary is the operator's description of the change. It may be empty.
	Summary string `json:"summary,omitempty"`
	// Checksum is the SHA-256 of the canonical encoding of Config.
	Checksum string `json:"checksum"`
	// Config is the configuration as it stood at this version.
	Config Config `json:"config"`
}

// Checksum computes the content identity of a configuration.
//
// It is derived from the canonical JSON encoding, so two configurations that
// differ only in field order or whitespace produce the same checksum.
// Go's encoding/json emits struct fields in declaration order and map keys in
// sorted order, which makes the encoding deterministic for this model.
func Checksum(c Config) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("cannot compute configuration checksum: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// NewVersion builds a Version for c, computing its checksum.
//
// It refuses invalid configurations: an invalid document must never enter the
// version history, because rolling back to it would be a trap.
func NewVersion(number int64, author, summary string, c Config, now time.Time) (Version, error) {
	if err := c.Validate(); err != nil {
		return Version{}, fmt.Errorf("refusing to version an invalid configuration: %w", err)
	}
	sum, err := Checksum(c)
	if err != nil {
		return Version{}, err
	}
	return Version{
		Number:    number,
		CreatedAt: now.UTC(),
		Author:    author,
		Summary:   summary,
		Checksum:  sum,
		Config:    c,
	}, nil
}

// SameContent reports whether two versions hold identical configuration.
//
// The console uses this to avoid recording a new version when an operator saves
// a form without having changed anything.
func SameContent(a, b Version) bool {
	return a.Checksum != "" && a.Checksum == b.Checksum
}

// Change is one field-level difference between two configurations.
type Change struct {
	// Field is the JSON path of the changed field.
	Field string `json:"field"`
	// From is the previous value, rendered as JSON.
	From string `json:"from"`
	// To is the new value, rendered as JSON.
	To string `json:"to"`
}

// Diff reports the field-level differences between two configurations.
//
// The result is ordered by field path so that rendering is stable across calls.
// Diff walks the JSON encoding rather than reflecting over struct fields, which
// keeps the paths identical to the ones validation reports and the console
// displays.
func Diff(from, to Config) ([]Change, error) {
	fromMap, err := toMap(from)
	if err != nil {
		return nil, err
	}
	toMapped, err := toMap(to)
	if err != nil {
		return nil, err
	}

	changes := make([]Change, 0)
	walkDiff("", fromMap, toMapped, &changes)
	sortChanges(changes)
	return changes, nil
}

func toMap(c Config) (map[string]any, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, fmt.Errorf("cannot encode configuration for diff: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("cannot decode configuration for diff: %w", err)
	}
	return m, nil
}

func walkDiff(prefix string, from, to map[string]any, out *[]Change) {
	seen := make(map[string]bool, len(from)+len(to))
	for k := range from {
		seen[k] = true
	}
	for k := range to {
		seen[k] = true
	}

	for k := range seen {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		fv, inFrom := from[k]
		tv, inTo := to[k]

		fNested, fIsMap := fv.(map[string]any)
		tNested, tIsMap := tv.(map[string]any)
		if inFrom && inTo && fIsMap && tIsMap {
			walkDiff(path, fNested, tNested, out)
			continue
		}

		fs := renderValue(fv, inFrom)
		ts := renderValue(tv, inTo)
		if fs != ts {
			*out = append(*out, Change{Field: path, From: fs, To: ts})
		}
	}
}

func renderValue(v any, present bool) string {
	if !present {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func sortChanges(c []Change) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].Field < c[j-1].Field; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}
