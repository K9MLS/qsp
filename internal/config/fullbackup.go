package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"time"
)

// The full backup: configuration and secrets, encrypted with a passphrase.
//
// # Why there are two backups
//
// [ADR-0065] reopened [ADR-0054] on the operator's argument: you should be
// able to back up everything, and if there is a security issue you change the
// passwords. That answers ADR-0054's strongest objection — every copy of the
// file becoming a credential is a description of a risk, and rotation is the
// standard response to it, while nothing answers a server reconstructed from
// memory.
//
// **The shareable export is unchanged and cannot be replaced.** Its value is
// being safe to email to somebody helping with a broken server, and that value
// ends the moment it holds a password. So this is a second kind, and the two
// must not be confusable: different magic, different extension, and an import
// that says which it is looking at.
//
// # The passphrase, and the way to lose everything
//
// An encrypted backup is exactly as recoverable as its key. ADR-0065 requires
// QSP to say so **when it creates the file** rather than in a manual, because a
// full backup nobody can decrypt is worse than a partial one: the operator
// believes they are covered. [PassphraseWarning] is that sentence, and the
// caller is expected to show it.
//
// # The cryptography, and what it is not
//
// AES-256-GCM, with the key derived from the passphrase by PBKDF2-HMAC-SHA256.
//
// **Argon2id would be the better choice and is not in the standard library.**
// It resists a GPU or ASIC attack far better than PBKDF2 for the same elapsed
// time, because it is memory-hard. It lives in `golang.org/x/crypto`, and this
// project's dependency rule is the standard library — so the honest position is
// PBKDF2 with a high iteration count, and a note saying what would be better if
// that rule ever changes.
//
// The iteration count follows OWASP's guidance for PBKDF2-HMAC-SHA256 rather
// than a number that felt large.

// FullBackupMagic identifies an encrypted full backup.
//
// Deliberately unlike the shareable export's format, so that an operator about
// to email a file, and QSP about to import one, can both tell which is which.
// **Mailing the wrong one publishes every password on the server**, which is
// the first thing to get right about having two.

var FullBackupMagic = [8]byte{'Q', 'S', 'P', 'F', 'U', 'L', 'L', '1'}

// FullBackupFormat is the envelope version. An older one migrates; a newer one
// is refused, for ADR-0054's reason: importing three-quarters of a
// configuration is worse than importing none, because the quarter that was
// dropped is invisible.
const FullBackupFormat uint16 = 1

// PBKDF2 parameters.
const (
	// FullBackupIterations is OWASP's 2023 recommendation for
	// PBKDF2-HMAC-SHA256. It is a number with a source rather than one that
	// felt large, and it costs a fraction of a second once per backup.
	FullBackupIterations = 600_000
	// FullBackupSaltBytes is the per-file salt, so two backups of the same
	// server under the same passphrase share no derived key.
	FullBackupSaltBytes = 16
	// fullBackupKeyBytes is AES-256.
	fullBackupKeyBytes = 32
)

// PassphraseWarning is what an operator must be told when a full backup is
// created, per ADR-0065.
//
// It is a constant here so that every caller says the same thing and none has
// to compose it — and so that a test can require it be shown.
const PassphraseWarning = "Keep this passphrase somewhere other than this " +
	"server. QSP does not store it, and without it this backup cannot be " +
	"opened by anyone, including you."

// FullBackup is everything needed to rebuild a server.
type FullBackup struct {
	// Backup is the same content the shareable export carries.
	Backup Backup `json:"backup"`
	// Secrets are the credentials, by the name configuration refers to them
	// by.
	//
	// **This is the whole difference from the shareable export**, and the
	// reason the file is encrypted and must never be sent to anybody.
	Secrets map[string]string `json:"secrets"`
}

// SecretNames lists the secrets carried, sorted.
//
// Useful to show after a restore — an operator who can see that four
// credentials came back has a different confidence from one told "restored".
func (f FullBackup) SecretNames() []string {
	out := make([]string, 0, len(f.Secrets))
	for name := range f.Secrets {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// WriteFullBackup encrypts a full backup to w.
//
// The header travels in the clear because a reader needs the salt and the
// iteration count before it can derive anything — and it is **authenticated**,
// so an attacker cannot rewrite the iteration count down to 1 and hand the
// file back for a cheaper attack. That is what passing it as additional
// authenticated data buys.
func WriteFullBackup(w io.Writer, f FullBackup, passphrase string) error {
	if passphrase == "" {
		return errors.New(
			"config: a full backup needs a passphrase; it carries every " +
				"credential on this server and an unencrypted one would be a " +
				"file nobody could safely keep")
	}

	payload, err := json.Marshal(f)
	if err != nil {
		return fmt.Errorf("config: cannot encode a full backup: %w", err)
	}

	salt := make([]byte, FullBackupSaltBytes)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return fmt.Errorf("config: cannot generate a salt: %w", err)
	}

	key, err := pbkdf2.Key(sha256.New, passphrase, salt,
		FullBackupIterations, fullBackupKeyBytes)
	if err != nil {
		return fmt.Errorf("config: cannot derive a key: %w", err)
	}

	aead, err := newAEAD(key)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("config: cannot generate a nonce: %w", err)
	}

	header := fullBackupHeader(salt, FullBackupIterations)
	sealed := aead.Seal(nil, nonce, payload, header)

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("config: cannot write a full backup: %w", err)
	}
	if _, err := w.Write(nonce); err != nil {
		return fmt.Errorf("config: cannot write a full backup: %w", err)
	}
	if _, err := w.Write(sealed); err != nil {
		return fmt.Errorf("config: cannot write a full backup: %w", err)
	}
	return nil
}

// fullBackupHeader renders the cleartext header: magic, format, iterations,
// salt.
func fullBackupHeader(salt []byte, iterations int) []byte {
	h := make([]byte, 0, len(FullBackupMagic)+2+4+len(salt))
	h = append(h, FullBackupMagic[:]...)
	h = binary.BigEndian.AppendUint16(h, FullBackupFormat)
	h = binary.BigEndian.AppendUint32(h, uint32(iterations))
	return append(h, salt...)
}

// fullBackupHeaderBytes is the fixed header length.
const fullBackupHeaderBytes = 8 + 2 + 4 + FullBackupSaltBytes

// ErrWrongPassphrase is returned when a full backup will not open.
//
// **Distinct from a corrupt file**, and the message says both possibilities,
// because GCM cannot tell them apart: a wrong passphrase and an altered byte
// both fail authentication identically. Claiming to know which would be a
// guess presented as a diagnosis.
var ErrWrongPassphrase = errors.New(
	"config: the full backup will not open, so either the passphrase is wrong " +
		"or the file has been altered; these fail identically and QSP cannot " +
		"tell them apart")

// ErrNotAFullBackup is returned for a file of another kind.
//
// Most likely the shareable export, which is the confusion having two formats
// creates — so the error names what was found rather than only what was
// wanted.
var ErrNotAFullBackup = errors.New(
	"config: this is not an encrypted full backup; the shareable export is a " +
		"different format and is imported a different way")

// ReadFullBackup decrypts a full backup from r.
func ReadFullBackup(r io.Reader, passphrase string) (FullBackup, error) {
	var zero FullBackup

	raw, err := io.ReadAll(r)
	if err != nil {
		return zero, fmt.Errorf("config: cannot read a full backup: %w", err)
	}
	if len(raw) < fullBackupHeaderBytes {
		return zero, ErrNotAFullBackup
	}
	if [8]byte(raw[0:8]) != FullBackupMagic {
		return zero, ErrNotAFullBackup
	}

	format := binary.BigEndian.Uint16(raw[8:10])
	if format > FullBackupFormat {
		return zero, fmt.Errorf(
			"config: this full backup is format %d and this build reads %d; "+
				"importing part of a configuration is worse than importing none, "+
				"because the part that was dropped is invisible",
			format, FullBackupFormat)
	}

	iterations := int(binary.BigEndian.Uint32(raw[10:14]))
	if iterations <= 0 {
		return zero, ErrNotAFullBackup
	}
	salt := raw[14:fullBackupHeaderBytes]

	key, err := pbkdf2.Key(sha256.New, passphrase, salt, iterations, fullBackupKeyBytes)
	if err != nil {
		return zero, fmt.Errorf("config: cannot derive a key: %w", err)
	}
	aead, err := newAEAD(key)
	if err != nil {
		return zero, err
	}

	rest := raw[fullBackupHeaderBytes:]
	if len(rest) < aead.NonceSize() {
		return zero, ErrNotAFullBackup
	}
	nonce := rest[:aead.NonceSize()]
	sealed := rest[aead.NonceSize():]

	payload, err := aead.Open(nil, nonce, sealed, raw[:fullBackupHeaderBytes])
	if err != nil {
		return zero, ErrWrongPassphrase
	}

	var f FullBackup
	if err := json.Unmarshal(payload, &f); err != nil {
		return zero, fmt.Errorf("config: a full backup decrypted but did not "+
			"decode: %w", err)
	}
	return f, nil
}

// newAEAD builds the cipher both directions use.
func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("config: cannot build a cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("config: cannot build a cipher: %w", err)
	}
	return aead, nil
}

// NewFullBackup assembles a full backup from a configuration and its secrets.
//
// The secrets are supplied rather than fetched, so this package does not learn
// about the store and the caller decides which credentials belong in a backup.
func NewFullBackup(cfg Config, secrets map[string]string, qspVersion string, now time.Time) FullBackup {
	copied := make(map[string]string, len(secrets))
	for k, v := range secrets {
		copied[k] = v
	}

	b := NewBackup(cfg, qspVersion, now)

	// **The Missing list is cleared, and not because this file carries
	// everything.** NewBackup fills it with every credential the *shareable*
	// export cannot hold — that list is that file's honest answer to "what
	// will a restore be missing", and it identifies each secret by the path it
	// lived at on the machine that wrote it.
	//
	// A full backup identifies secrets by the name configuration refers to
	// them by, because that is what the store is keyed on. The two identities
	// do not correlate reliably, so narrowing the list would mean guessing
	// which path corresponds to which name — and a list that is wrong about a
	// credential is worse than no list, because an import would tell an
	// operator to re-enter a password the file already restored.
	//
	// What replaces it is a positive statement: SecretNames says what came
	// back, which is the question a restore can answer truthfully. A secret
	// the store never held shows up where it already does — configuration
	// validation reports a link with no password, on the restored server, at
	// startup.
	b.Missing = nil

	return FullBackup{Backup: b, Secrets: copied}
}
