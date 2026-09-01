package config

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

// PeerPasswords resolves the password a peer authenticates against.
//
// **A member can be removed without changing everybody's password.** One shared
// secret means removing one person requires a new password and every remaining
// member reconfiguring their hotspot on the same evening, and it gets worse with
// every member who joins. See ADR-0035.
//
// A directory of files rather than a table, for ADR-0012's reasons: the
// configuration document is versioned, exportable and diffable, so a password in
// it is a password in the version history, in every backup, and rendered on
// screen in a diff. It also means removal is `rm`, which works when the console
// is down and the person doing it is on a phone over SSH — and the moment a
// credential most needs revoking is not the moment to depend on the most
// machinery.
type PeerPasswords struct {
	// dir is where per-peer files live, empty when the club uses one shared
	// password.
	dir string
	// shared is the fallback, which every peer without a file of its own uses.
	shared []byte
	// read is injected so the lookup can be tested without a filesystem.
	read func(string) ([]byte, error)
	// stat reports a file's mode, so a world-readable credential is refused
	// rather than used.
	stat func(string) (fs.FileInfo, error)
}

// NewPeerPasswords builds a resolver.
//
// An empty dir means every peer uses the shared password, which is what a club
// that does not want the bookkeeping keeps.
func NewPeerPasswords(dir string, shared []byte,
	read func(string) ([]byte, error),
	stat func(string) (fs.FileInfo, error)) *PeerPasswords {
	return &PeerPasswords{
		dir:    strings.TrimSpace(dir),
		shared: shared,
		read:   read,
		stat:   stat,
	}
}

// For returns the password a peer must present.
//
// **A per-peer password overrides rather than adds.** If a peer has a file, the
// shared password does not work for it — otherwise deleting somebody's file
// would silently return them to the shared secret they already know, and an
// administrator would believe they had revoked access they had in fact restored.
// That is the worst failure available here, because it is quiet and it looks
// like success.
//
// A file that exists and cannot be read is a refusal, never a fallback: falling
// back on a permissions mistake turns it into a silently weakened network.
func (p *PeerPasswords) For(peer uint32) ([]byte, error) {
	if p == nil {
		return nil, fmt.Errorf("no peer password is configured")
	}
	if p.dir == "" {
		return p.shared, nil
	}

	path := filepath.Join(p.dir, strconv.FormatUint(uint64(peer), 10))

	if p.stat != nil {
		info, err := p.stat(path)
		switch {
		case err == nil:
			// A world-readable directory of member credentials is worse than
			// one shared secret, and this must not be the change that
			// introduces it.
			if mode := info.Mode().Perm(); mode&0o077 != 0 {
				return nil, fmt.Errorf("the password file for peer %d is mode %#o; "+
					"it must not be readable by anybody else (chmod 600 %s)", peer, mode, path)
			}
		case isNotExist(err):
			// No file of its own, so the shared password applies.
			return p.shared, nil
		default:
			return nil, fmt.Errorf("cannot check the password file for peer %d: %w", peer, err)
		}
	}

	raw, err := p.read(path)
	if err != nil {
		if isNotExist(err) {
			return p.shared, nil
		}
		return nil, fmt.Errorf("cannot read the password file for peer %d: %w "+
			"(the peer is refused rather than falling back to the shared password)", peer, err)
	}

	password := strings.TrimSpace(string(raw))
	if password == "" {
		return nil, fmt.Errorf("the password file for peer %d is empty; "+
			"delete it to return the peer to the shared password, or put a password in it", peer)
	}
	return []byte(password), nil
}

// PerPeer reports whether any peer has a password of its own.
func (p *PeerPasswords) PerPeer() bool { return p != nil && p.dir != "" }

// isNotExist recognises a missing file through whatever wrapping it arrives in.
//
// **errors.Is rather than matching the message.** os.ReadFile wraps
// fs.ErrNotExist in a *PathError, so this works through the wrapping and does
// not depend on the operating system's wording — and getting it wrong here would
// turn a missing file into a refusal, locking out every peer that was meant to
// use the shared password.
func isNotExist(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
