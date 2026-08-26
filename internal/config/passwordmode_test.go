package config

import (
	"io/fs"
	"strings"
	"testing"
)

// A peer password file holds a shared secret. These cases are the difference
// between "only the owner can read it" and "every account on the box can".
func TestCheckPeerPasswordMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    fs.FileMode
		wantErr bool
	}{
		{"0600 owner read-write", 0o600, false},
		{"0400 owner read-only", 0o400, false},
		{"0700 owner all", 0o700, false},
		{"0000 unreadable by anyone", 0o000, false},

		{"0640 group can read", 0o640, true},
		{"0604 world can read", 0o604, true},
		{"0644 the default umask result", 0o644, true},
		{"0660 group read-write", 0o660, true},
		{"0666 everyone", 0o666, true},
		{"0610 group execute only", 0o610, true},
		{"0777 everything", 0o777, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckPeerPasswordMode(tc.mode)
			if tc.wantErr && err == nil {
				t.Errorf("mode %#o is readable beyond its owner but was accepted", tc.mode)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("mode %#o is owner-only but was rejected: %v", tc.mode, err)
			}
		})
	}
}

// Type bits ride in the same FileMode as the permission bits. A directory or a
// symlink must not be accepted merely because its type bits are set, nor
// rejected because of them — only the permission bits carry the disclosure.
func TestCheckPeerPasswordModeIgnoresTypeBits(t *testing.T) {
	if err := CheckPeerPasswordMode(fs.ModeDir | 0o600); err != nil {
		t.Errorf("0600 with the directory bit set was rejected: %v", err)
	}
	if err := CheckPeerPasswordMode(fs.ModeSymlink | 0o644); err == nil {
		t.Error("0644 with the symlink bit set was accepted")
	}
}

// The error has to tell an operator what to do. A rejection they cannot act on
// is a rejection they will work around.
func TestCheckPeerPasswordModeErrorNamesTheFix(t *testing.T) {
	err := CheckPeerPasswordMode(0o644)
	if err == nil {
		t.Fatal("0644 was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"chmod 600", "0644"} {
		if !contains(msg, want) {
			t.Errorf("the error does not mention %q: %s", want, msg)
		}
	}
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
