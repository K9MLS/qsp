package config

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"
)

type fakeInfo struct{ mode fs.FileMode }

func (f fakeInfo) Name() string       { return "" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

func files(m map[string]string, mode fs.FileMode) (func(string) ([]byte, error), func(string) (fs.FileInfo, error)) {
	read := func(path string) ([]byte, error) {
		v, ok := m[path]
		if !ok {
			return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
		}
		return []byte(v), nil
	}
	stat := func(path string) (fs.FileInfo, error) {
		if _, ok := m[path]; !ok {
			return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
		}
		return fakeInfo{mode: mode}, nil
	}
	return read, stat
}

// TestAPeerWithoutAFileUsesTheSharedPassword. A club that does not want the
// bookkeeping keeps exactly what it has, and per-peer passwords are opt-in per
// peer rather than a migration everybody performs.
func TestAPeerWithoutAFileUsesTheSharedPassword(t *testing.T) {
	read, stat := files(map[string]string{"/pw/3132910": "mine"}, 0o600)
	p := NewPeerPasswords("/pw", []byte("shared"), read, stat)

	got, err := p.For(3155413)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if string(got) != "shared" {
		t.Errorf("a peer with no file of its own got %q", got)
	}
}

// TestAPerPeerPasswordOverridesRatherThanAdds.
//
// **This is the property revocation depends on.** If the shared password still
// worked for a peer that has its own, deleting somebody's file would silently
// return them to the secret they already know — and an administrator would
// believe they had revoked access they had in fact restored. Quiet, and it looks
// like success.
func TestAPerPeerPasswordOverridesRatherThanAdds(t *testing.T) {
	read, stat := files(map[string]string{"/pw/3132910": "mine"}, 0o600)
	p := NewPeerPasswords("/pw", []byte("shared"), read, stat)

	got, err := p.For(3132910)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if string(got) != "mine" {
		t.Errorf("a peer with its own password got %q", got)
	}
	if string(got) == "shared" {
		t.Error("the shared password still works for a peer that has its own")
	}
}

// TestAWorldReadableFileIsRefused. A directory of member credentials readable by
// anybody is worse than one shared secret, and this must not be the change that
// introduces it.
func TestAWorldReadableFileIsRefused(t *testing.T) {
	read, stat := files(map[string]string{"/pw/3132910": "mine"}, 0o644)
	p := NewPeerPasswords("/pw", []byte("shared"), read, stat)

	_, err := p.For(3132910)
	if err == nil {
		t.Fatal("a world-readable password file was accepted")
	}
	if !strings.Contains(err.Error(), "chmod") {
		t.Errorf("the error does not say how to fix it: %v", err)
	}
}

// TestAnUnreadableFileIsARefusalNotAFallback. Falling back on a permissions
// mistake turns it into a silently weakened network.
func TestAnUnreadableFileIsARefusalNotAFallback(t *testing.T) {
	read := func(string) ([]byte, error) { return nil, errors.New("permission denied") }
	stat := func(string) (fs.FileInfo, error) { return fakeInfo{mode: 0o600}, nil }
	p := NewPeerPasswords("/pw", []byte("shared"), read, stat)

	got, err := p.For(3132910)
	if err == nil {
		t.Fatalf("an unreadable file fell back to the shared password (%q)", got)
	}
}

// TestNoDirectoryMeansTheSharedPasswordThroughout, which is what every club has
// today and what this must not change for them.
func TestNoDirectoryMeansTheSharedPasswordThroughout(t *testing.T) {
	p := NewPeerPasswords("", []byte("shared"), nil, nil)

	got, err := p.For(3132910)
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if string(got) != "shared" {
		t.Errorf("got %q", got)
	}
	if p.PerPeer() {
		t.Error("an instance with no directory reports per-peer passwords")
	}
}

// TestAnEmptyFileIsRefused, and says how to undo it. An empty file would
// otherwise authenticate a peer that guessed an empty password, which is
// ADR-0012's reasoning applied to one member instead of all of them.
func TestAnEmptyFileIsRefused(t *testing.T) {
	read, stat := files(map[string]string{"/pw/3132910": "   "}, 0o600)
	p := NewPeerPasswords("/pw", []byte("shared"), read, stat)

	_, err := p.For(3132910)
	if err == nil {
		t.Fatal("an empty password file was accepted")
	}
	if !strings.Contains(err.Error(), "delete it") {
		t.Errorf("the error does not say how to return the peer to the shared password: %v", err)
	}
}

// TestThePeerPasswordDirectoryMustBeAbsolute.
//
// A relative path resolves against wherever systemd happened to start the
// process, so a member's password would be written somewhere nobody thinks to
// look and read from somewhere else after a change to the unit file.
func TestThePeerPasswordDirectoryMustBeAbsolute(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = true
	c.DMR.PeerPasswords = "peers"

	err := c.Validate()
	if err == nil {
		t.Fatal("a relative peer password directory was accepted")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("the error does not explain: %v", err)
	}
}

// TestThePeerPasswordDirectoryIsNotTheSharedFile. A directory and a file are
// different things, and pointing one at the other would put a member's password
// where the shared one lives.
func TestThePeerPasswordDirectoryIsNotTheSharedFile(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = true
	c.DMR.PeerPasswords = "/etc/qsp/peer.pass"

	err := c.Validate()
	if err == nil {
		t.Fatal("the peer password directory was allowed to be the shared password file")
	}
	if !strings.Contains(err.Error(), "same path") {
		t.Errorf("the error does not explain: %v", err)
	}
}

// TestNoPeerPasswordDirectoryIsFine, which is what every club has today.
func TestNoPeerPasswordDirectoryIsFine(t *testing.T) {
	c := Default()
	c.DMR.Enabled = true
	c.DMR.Access = &Access{}
	c.DMR.PasswordFile = "/etc/qsp/peer.pass"
	c.DMR.Forwarding = true

	if err := c.Validate(); err != nil {
		t.Fatalf("an instance with one shared password was refused: %v", err)
	}
}
