// Package secrets holds the credentials an operator types into the console.
//
// # Why this exists
//
// [ADR-0012] keeps secrets out of the configuration document, because a
// document that is versioned, diffed, shown in a console and copied about is
// never the thing that should hold a password. [ADR-0065] keeps that rule and
// changes where an operator types one: the console, not a text editor.
//
// So a credential needs somewhere to live that is not `configuration_versions`
// — which stores the full JSON document for every save, so a secret written
// into configuration would appear in every snapshot, every diff and every
// version the console shows, in plain text, with an author's name on it.
//
// # What the encryption is for, and what it is not
//
// Secrets are stored AES-256-GCM encrypted under a key in a separate file.
// **Stating the limit plainly, because an overstated defence is worse than an
// absent one:**
//
//   - **It protects a copied database.** A SQLite file gets handed around in
//     ways a configuration file does not — sent to somebody diagnosing a
//     problem, included in a storage snapshot, left in a backup of one
//     directory. A copy of the database without the key file yields nothing.
//   - **It does not protect a compromised host.** Anyone who can read the
//     database can almost always read the key beside it. This is
//     defence in depth against copies, not a claim about an attacker with a
//     shell.
//
// That is the honest account. The alternative — plaintext in the table — is
// defensible and was rejected because the copied-database case is real in this
// project, which moves captures, bundles and diagnostic files around
// constantly.
//
// # The key, and the way to lose everything
//
// The key is 32 random bytes in a file of mode 0600, created on first use so
// that there is no setup step to forget. **Losing it loses every secret**, in
// exactly the way ADR-0065 says losing a backup passphrase loses a backup, and
// for the same reason: there is no recovery path and QSP should say so rather
// than imply one.
//
// Recovery from that is rotation, not decryption: the credentials are typed in
// again. It is the same answer the operator gave when reopening ADR-0054.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// KeyBytes is the key length: AES-256.
const KeyBytes = 32

// ErrNotFound is returned when a name holds no secret.
//
// **Distinct from an empty value**, because a link with no password configured
// and a link whose password is the empty string need different answers, and a
// caller that cannot tell them apart reports the wrong one.
var ErrNotFound = errors.New("secrets: no secret of that name")

// Store reads and writes credentials.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
	now  func() time.Time
}

// Options configures a store.
type Options struct {
	// DB is the open database. Required.
	DB *sql.DB
	// KeyPath is the file holding the encryption key. Created on first use.
	KeyPath string
	// Now is the clock, for tests. Nil selects time.Now.
	Now func() time.Time
}

// Open returns a store, creating the key file if it does not exist.
func Open(opts Options) (*Store, error) {
	if opts.DB == nil {
		return nil, errors.New("secrets: no database")
	}
	if opts.KeyPath == "" {
		return nil, errors.New("secrets: no key path")
	}

	key, err := loadOrCreateKey(opts.KeyPath)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secrets: cannot use the key: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secrets: cannot use the key: %w", err)
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Store{db: opts.DB, aead: aead, now: now}, nil
}

// loadOrCreateKey reads the key, or writes a new one.
//
// **A key file readable by anyone is refused rather than used.** A mode that
// lets another account read it makes the encryption pointless while leaving
// everything looking encrypted, which is the worst of both — and it is the
// kind of thing that happens by accident with a umask or a restored archive.
func loadOrCreateKey(path string) ([]byte, error) {
	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.Mode().Perm()&0o077 != 0 {
			return nil, fmt.Errorf(
				"secrets: the key file %s is mode %04o and must not be readable "+
					"by anyone else; run chmod 600 on it", path, info.Mode().Perm())
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("secrets: cannot read the key: %w", err)
		}
		if len(key) != KeyBytes {
			return nil, fmt.Errorf(
				"secrets: the key file %s holds %d bytes and a key is %d; if it "+
					"has been truncated or replaced, every stored secret must be "+
					"entered again", path, len(key), KeyBytes)
		}
		return key, nil

	case errors.Is(err, os.ErrNotExist):
		key := make([]byte, KeyBytes)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("secrets: cannot generate a key: %w", err)
		}
		if dir := filepath.Dir(path); dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, fmt.Errorf("secrets: cannot create %s: %w", dir, err)
			}
		}
		// O_EXCL so that two processes starting together cannot each write a
		// key and leave one of them unable to read what the other stored.
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, fmt.Errorf("secrets: cannot create the key: %w", err)
		}
		if _, err := f.Write(key); err != nil {
			f.Close()
			return nil, fmt.Errorf("secrets: cannot write the key: %w", err)
		}
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("secrets: cannot write the key: %w", err)
		}
		return key, nil
	}
	return nil, fmt.Errorf("secrets: cannot read the key: %w", err)
}

// Set stores a secret under a name, replacing any previous value.
//
// **A second write replaces rather than accumulates.** A table of every
// password a server has ever held is a liability, and rotation means the old
// value should stop existing.
//
// The name is the additional authenticated data, so a stored ciphertext cannot
// be moved to another name: copying the Zello credential into the
// peer-password row fails to decrypt rather than quietly offering the wrong
// secret to a link.
func (s *Store) Set(ctx context.Context, name, value, author string) error {
	if name == "" {
		return errors.New("secrets: a secret needs a name")
	}

	nonce, ciphertext, err := s.seal(name, value)
	if err != nil {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO secrets (name, nonce, ciphertext, updated_at, updated_by)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			nonce = excluded.nonce,
			ciphertext = excluded.ciphertext,
			updated_at = excluded.updated_at,
			updated_by = excluded.updated_by`,
		name, nonce, ciphertext, s.now().UTC().Format(time.RFC3339Nano), author)
	if err != nil {
		return fmt.Errorf("secrets: cannot store %q: %w", name, err)
	}
	return nil
}

// Get returns a secret, or ErrNotFound.
//
// **A failure to decrypt is reported as what it is.** The two ways it happens
// are a key that is not the one the secret was written under, and a row that
// has been altered — and both mean the credential must be entered again, which
// is a different message from "there is no such secret".
func (s *Store) Get(ctx context.Context, name string) (string, error) {
	var nonce, ciphertext []byte
	err := s.db.QueryRowContext(ctx,
		`SELECT nonce, ciphertext FROM secrets WHERE name = ?`, name).
		Scan(&nonce, &ciphertext)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("%w: %q", ErrNotFound, name)
	case err != nil:
		return "", fmt.Errorf("secrets: cannot read %q: %w", name, err)
	}

	return s.unseal(name, nonce, ciphertext)
}

// seal encrypts a value under its own name.
//
// **A fresh random nonce every time, and it is not optional.** GCM under a
// reused nonce and the same key leaks the exclusive-or of the two plaintexts
// and lets an attacker forge, so two secrets encrypted under one nonce is not
// a weakness but a break. There is no counter here for that reason: a counter
// that restarts — a restored database, a second process — repeats.
//
// **The name is the additional authenticated data**, so a ciphertext cannot be
// moved between rows. Copying the Zello credential into the peer-password row
// fails to decrypt rather than quietly offering the wrong secret to a link.
//
// Separate from Set so that the encryption is exercised by tests that need no
// database: the development container has no SQLite driver, and the
// cryptography is the part where a mistake is least visible.
func (s *Store) seal(name, value string) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("secrets: cannot generate a nonce: %w", err)
	}
	return nonce, s.aead.Seal(nil, nonce, []byte(value), []byte(name)), nil
}

// unseal decrypts a value, requiring the name it was stored under.
func (s *Store) unseal(name string, nonce, ciphertext []byte) (string, error) {
	plain, err := s.aead.Open(nil, nonce, ciphertext, []byte(name))
	if err != nil {
		return "", fmt.Errorf(
			"secrets: %q cannot be decrypted, so either the key file is not the "+
				"one it was stored under or the row has been altered; the "+
				"credential has to be entered again", name)
	}
	return string(plain), nil
}

// Record is what the console shows about a secret: that it exists, and when it
// was last set. **Never its value**, because a page that displays a password
// is a page that leaks it to anybody looking at the screen, and an operator
// who needs to know the value has it elsewhere or should replace it.
type Record struct {
	Name      string
	UpdatedAt time.Time
	UpdatedBy string
}

// List returns what is stored, by name, without decrypting anything.
//
// Nothing is decrypted because nothing needs to be: the question a console
// asks is "is this configured and when did it change", and answering it
// without touching the key means a listing cannot leak a credential even if
// the page rendering it is wrong.
func (s *Store) List(ctx context.Context) ([]Record, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, updated_at, updated_by FROM secrets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("secrets: cannot list: %w", err)
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var r Record
		var at string
		if err := rows.Scan(&r.Name, &at, &r.UpdatedBy); err != nil {
			return nil, fmt.Errorf("secrets: cannot list: %w", err)
		}
		if t, err := time.Parse(time.RFC3339Nano, at); err == nil {
			r.UpdatedAt = t
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("secrets: cannot list: %w", err)
	}
	return out, nil
}

// Delete removes a secret. Removing one that is not there is not an error:
// the caller wanted it gone and it is gone.
func (s *Store) Delete(ctx context.Context, name string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM secrets WHERE name = ?`, name); err != nil {
		return fmt.Errorf("secrets: cannot delete %q: %w", name, err)
	}
	return nil
}

// Has reports whether a name holds a secret, without decrypting it.
//
// This is what a configuration check wants: whether a link has a password at
// all, which is a different question from what the password is and should not
// require the key to answer.
func (s *Store) Has(ctx context.Context, name string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM secrets WHERE name = ?`, name).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("secrets: cannot check %q: %w", name, err)
	}
	return true, nil
}
