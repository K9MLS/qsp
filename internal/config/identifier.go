package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// IdentifierBytes is the length of a server identifier before hex encoding.
//
// 128 bits: enough that two servers generating one independently will not
// collide, in a network with no coordinator to prevent it (ADR-0053).
const IdentifierBytes = 16

// NewIdentifier generates a server's identifier.
//
// **Called once, at first run, and never again.** There is deliberately no way
// to change one: "what everybody calls everybody else" and "an operator can
// edit it" cannot both be true, so a server that needs a new identity is a new
// server.
func NewIdentifier() (string, error) {
	b := make([]byte, IdentifierBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("config: generating a server identifier: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ValidIdentifier reports whether a string is a well-formed identifier.
//
// **This is the only function permitted to know its shape**, and it checks the
// shape and nothing else: no prefix, no meaning, no version digit. Everything
// else in QSP compares identifiers for equality and otherwise treats them as
// opaque bytes, which is what lets a later generation replace random bits with
// a key fingerprint without touching anything but this file.
func ValidIdentifier(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("config: a server identifier is empty")
	}
	if len(id) != hex.EncodedLen(IdentifierBytes) {
		return fmt.Errorf("config: a server identifier is %d characters, not %d",
			hex.EncodedLen(IdentifierBytes), len(id))
	}
	if _, err := hex.DecodeString(id); err != nil {
		return fmt.Errorf("config: a server identifier is hexadecimal: %w", err)
	}
	return nil
}

// ShortIdentifier is an identifier trimmed for a screen.
//
// Shown only where identity is the actual question — two links displaying the
// same name, or a details view — and never as a heading. An operator reading a
// page should be reading names.
func ShortIdentifier(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
