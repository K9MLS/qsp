package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The cryptography and the key file, tested without a database.
//
// **This is the half that can be verified here.** The store's SQL needs a
// SQLite driver, which this development container does not have — the same
// reason seven cmd/qsp tests cannot run in it. The encryption is the part where
// a plausible mistake is least visible and most expensive, so it is separated
// out and exercised properly rather than left to a test nobody can run.

// aeadFor builds the same AEAD the store uses, from a known key.
func aeadFor(t *testing.T, key []byte) cipher.AEAD {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("gcm: %v", err)
	}
	return aead
}

// TestACiphertextCannotBeMovedToAnotherName is the property the name-as-AAD
// buys, and the reason it is there.
//
// The secret's name is the additional authenticated data. Without it, a
// ciphertext is just bytes and **copying the Zello credential into the
// peer-password row would silently offer the wrong secret to a link** — a link
// that then fails to log in for a reason nothing can explain. With it, the move
// fails to decrypt.
func TestACiphertextCannotBeMovedToAnotherName(t *testing.T) {
	key := make([]byte, KeyBytes)
	for i := range key {
		key[i] = byte(i)
	}
	aead := aeadFor(t, key)

	nonce := make([]byte, aead.NonceSize())
	const secret = "the zello password"
	sealed := aead.Seal(nil, nonce, []byte(secret), []byte("transcoder.dvstick.zello"))

	// Its own name opens it.
	plain, err := aead.Open(nil, nonce, sealed, []byte("transcoder.dvstick.zello"))
	if err != nil {
		t.Fatalf("a secret did not open under its own name: %v", err)
	}
	if string(plain) != secret {
		t.Fatalf("the secret opened as %q", plain)
	}

	// Another name does not.
	for _, other := range []string{"dmr.password", "upstream.bcara.passphrase", ""} {
		if _, err := aead.Open(nil, nonce, sealed, []byte(other)); err == nil {
			t.Errorf("a ciphertext stored under one name opened under %q; a "+
				"credential could be moved between rows and offered to the "+
				"wrong link", other)
		}
	}
}

// TestAnAlteredCiphertextIsRefusedRatherThanReturningRubbish.
//
// GCM authenticates, so a single flipped bit fails rather than decrypting to
// noise. That matters because the alternative — a mode without authentication —
// would hand a link a password made of random bytes, and the failure would look
// like a wrong password rather than a corrupted row.
func TestAnAlteredCiphertextIsRefusedRatherThanReturningRubbish(t *testing.T) {
	key := make([]byte, KeyBytes)
	aead := aeadFor(t, key)
	nonce := make([]byte, aead.NonceSize())
	sealed := aead.Seal(nil, nonce, []byte("secret"), []byte("name"))

	for i := range sealed {
		bad := bytes.Clone(sealed)
		bad[i] ^= 1
		if _, err := aead.Open(nil, nonce, bad, []byte("name")); err == nil {
			t.Fatalf("flipping bit 0 of byte %d still decrypted; the mode is not "+
				"authenticating", i)
		}
	}

	// And a wrong key fails rather than producing something.
	other := make([]byte, KeyBytes)
	other[0] = 1
	if _, err := aeadFor(t, other).Open(nil, nonce, sealed, []byte("name")); err == nil {
		t.Error("a different key opened the secret")
	}
}

// TestTheKeyIsTwoHundredAndFiftySixBitsAndTheNonceIsGcmsOwnSize.
//
// Asserted rather than assumed: a 16-byte key is AES-128, which is not what
// this package claims, and a nonce of the wrong length is rejected by GCM at
// use rather than at review.
func TestTheKeyIsTwoHundredAndFiftySixBitsAndTheNonceIsGcmsOwnSize(t *testing.T) {
	if KeyBytes != 32 {
		t.Errorf("the key is %d bytes; AES-256 is 32", KeyBytes)
	}
	aead := aeadFor(t, make([]byte, KeyBytes))
	if got := aead.NonceSize(); got != 12 {
		t.Errorf("the nonce is %d bytes and GCM's standard size is 12", got)
	}
	// The overhead is the authentication tag, and it is what makes an altered
	// row fail rather than decrypt.
	if got := aead.Overhead(); got != 16 {
		t.Errorf("the tag is %d bytes, want 16", got)
	}
}

// TestAKeyFileIsCreatedPrivateAndReadBack.
func TestAKeyFileIsCreatedPrivateAndReadBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "secrets.key")

	first, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatalf("creating a key: %v", err)
	}
	if len(first) != KeyBytes {
		t.Fatalf("the created key is %d bytes, want %d", len(first), KeyBytes)
	}

	// A key of all zeros would mean the random source was not read.
	if bytes.Equal(first, make([]byte, KeyBytes)) {
		t.Fatal("the created key is all zeros; nothing random was written")
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("the key file is mode %04o, want 0600", perm)
	}
	// The directory it created must not be world-readable either, or the file
	// inside it is protected and its location is not.
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("the key directory is mode %04o and should not be readable by "+
			"anyone else", perm)
	}

	// A second call reads the same key rather than replacing it, or every
	// restart would orphan every stored secret.
	second, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatalf("reading the key back: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Error("a second call produced a different key; every secret stored " +
			"before it would be unreadable")
	}
}

// TestAKeyFileAnyoneCanReadIsRefusedRatherThanUsed.
//
// A mode that lets another account read the key makes the encryption pointless
// while leaving everything looking encrypted, which is the worst of both. It
// happens by accident — a umask, a restored archive, a `cp` without `-p` — so
// it is checked on every open rather than only at creation.
func TestAKeyFileAnyoneCanReadIsRefusedRatherThanUsed(t *testing.T) {
	dir := t.TempDir()

	for _, mode := range []os.FileMode{0o644, 0o640, 0o604, 0o666, 0o660} {
		path := filepath.Join(dir, "k")
		if err := os.WriteFile(path, make([]byte, KeyBytes), 0o600); err != nil {
			t.Fatalf("writing: %v", err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatalf("chmod: %v", err)
		}

		_, err := loadOrCreateKey(path)
		if err == nil {
			t.Errorf("a key file of mode %04o was used", mode)
		} else if !strings.Contains(err.Error(), "chmod 600") {
			t.Errorf("the refusal for mode %04o does not say how to fix it: %v",
				mode, err)
		}
		os.Remove(path)
	}

	// 0600 and 0400 are both fine: the second is a key an operator has
	// deliberately made read-only.
	for _, mode := range []os.FileMode{0o600, 0o400} {
		path := filepath.Join(dir, "ok")
		if err := os.WriteFile(path, make([]byte, KeyBytes), mode); err != nil {
			t.Fatalf("writing: %v", err)
		}
		if _, err := loadOrCreateKey(path); err != nil {
			t.Errorf("a key file of mode %04o was refused: %v", mode, err)
		}
		os.Remove(path)
	}
}

// TestATruncatedKeyIsRefusedAndSaysWhatItMeans.
//
// A short key file is not a key. Padding it would produce a cipher that works
// and decrypts nothing that was stored before, which presents as every
// credential being wrong at once — so it is refused with the consequence
// stated: the secrets have to be entered again.
func TestATruncatedKeyIsRefusedAndSaysWhatItMeans(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []int{0, 1, 16, 31, 33, 64} {
		path := filepath.Join(dir, "k")
		if err := os.WriteFile(path, make([]byte, n), 0o600); err != nil {
			t.Fatalf("writing: %v", err)
		}
		_, err := loadOrCreateKey(path)
		if err == nil {
			t.Errorf("a key of %d bytes was accepted", n)
		} else if !strings.Contains(err.Error(), "entered again") {
			t.Errorf("the refusal for %d bytes does not say what it means for "+
				"stored secrets: %v", n, err)
		}
		os.Remove(path)
	}
}

// TestTwoKeysCreatedTogetherDoNotBothWin.
//
// The file is created with O_EXCL, so two processes starting at once cannot
// each write a key and leave one of them unable to read what the other stored.
// The second must fail rather than overwrite.
func TestTwoKeysCreatedTogetherDoNotBothWin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k")

	first, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatalf("creating: %v", err)
	}

	// Simulate the race by removing nothing and creating again: the file now
	// exists, so the second caller must read it rather than replace it.
	second, err := loadOrCreateKey(path)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("the second caller wrote a new key over the first")
	}

	// And O_EXCL is what enforces it, so a direct create on an existing path
	// must fail.
	if f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err == nil {
		f.Close()
		t.Error("creating the key file with O_EXCL succeeded over an existing " +
			"one, so the flag is not doing what this relies on")
	}
}

// TestOpenRefusesAStoreItCannotUse, so a misconfiguration is a startup error
// rather than a nil dereference on the first credential.
func TestOpenRefusesAStoreItCannotUse(t *testing.T) {
	if _, err := Open(Options{KeyPath: filepath.Join(t.TempDir(), "k")}); err == nil {
		t.Error("a store with no database was opened")
	}
	if _, err := Open(Options{DB: nil, KeyPath: ""}); err == nil {
		t.Error("a store with no key path was opened")
	}
}

// storeWithKey builds a store with no database, for exercising the encryption
// itself. The SQL is tested in store_test.go where a driver exists.
func storeWithKey(t *testing.T, key []byte) *Store {
	t.Helper()
	return &Store{aead: aeadFor(t, key)}
}

// TestTheStoreBindsACiphertextToItsName exercises the real code path, which
// the earlier test did not.
//
// **An earlier version built its own cipher and checked that GCM authenticates
// its additional data** — which is true of GCM and says nothing about whether
// this package passes the name in. Removing the name from `Seal` and `Open`
// passed every test: both sides agreed on nothing, so every round trip
// succeeded and the binding was gone.
//
// So this drives `seal` and `unseal` themselves. The property is the one that
// matters operationally: a row moved from one name to another must fail rather
// than offer the wrong credential to a link.
func TestTheStoreBindsACiphertextToItsName(t *testing.T) {
	s := storeWithKey(t, make([]byte, KeyBytes))

	const name, value = "transcoder.dvstick.zello", "the zello password"
	nonce, ciphertext, err := s.seal(name, value)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}

	got, err := s.unseal(name, nonce, ciphertext)
	if err != nil {
		t.Fatalf("unsealing under its own name: %v", err)
	}
	if got != value {
		t.Fatalf("the secret came back as %q", got)
	}

	// The move that must fail.
	for _, other := range []string{"dmr.password", "upstream.bcara.passphrase", ""} {
		if _, err := s.unseal(other, nonce, ciphertext); err == nil {
			t.Errorf("a ciphertext stored as %q opened as %q; a credential could "+
				"be moved between rows and handed to the wrong link", name, other)
		}
	}
}

// TestEveryEncryptionUsesAFreshNonce is the catastrophic failure, and nothing
// caught it before.
//
// **GCM under a reused nonce and the same key is broken, not merely weaker**:
// two plaintexts encrypted under one nonce leak their exclusive-or, and the
// authentication key can be recovered, which lets an attacker forge. Removing
// the random read left the nonce as twelve zero bytes for every secret, and
// every test passed because round trips still worked.
//
// Two secrets of the same value must therefore differ on the wire, and the
// nonces must differ across many encryptions.
func TestEveryEncryptionUsesAFreshNonce(t *testing.T) {
	s := storeWithKey(t, make([]byte, KeyBytes))

	const name, value = "dmr.password", "the same value twice"
	nonce1, ct1, err := s.seal(name, value)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}
	nonce2, ct2, err := s.seal(name, value)
	if err != nil {
		t.Fatalf("sealing: %v", err)
	}

	if bytes.Equal(nonce1, nonce2) {
		t.Fatal("two encryptions used the same nonce; under GCM that leaks the " +
			"exclusive-or of the plaintexts and permits forgery")
	}
	if bytes.Equal(ct1, ct2) {
		t.Fatal("the same value encrypted twice produced identical ciphertext, " +
			"so the nonce is fixed")
	}
	if bytes.Equal(nonce1, make([]byte, len(nonce1))) {
		t.Fatal("the nonce is all zeros; nothing random was read")
	}

	// Across many, every nonce distinct — a counter that restarts would show
	// up here as a repeat.
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		nonce, _, err := s.seal(name, value)
		if err != nil {
			t.Fatalf("sealing: %v", err)
		}
		if seen[string(nonce)] {
			t.Fatalf("nonce repeated after %d encryptions", i)
		}
		seen[string(nonce)] = true
	}
}
