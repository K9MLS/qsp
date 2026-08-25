// Package auth provides QSP's credential primitives.
//
// This phase establishes password hashing and verification only. Sessions,
// roles and authorisation are designed before the console becomes functional,
// per Constitution §8, and are not implemented here.
//
// The hashing algorithm is PBKDF2-HMAC-SHA256, implemented against RFC 8018
// using only the standard library. Argon2id would be the stronger choice, but
// it lives in golang.org/x/crypto, and this build has no module access; see
// docs/adr/ADR-0006 for the decision and the migration path.
//
// Because a stored hash carries its own algorithm and parameters, an existing
// deployment can be upgraded to a different algorithm without invalidating
// anyone's password: NeedsRehash reports when a stored hash is weaker than
// current policy, and the application rehashes on the next successful login.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Algorithm identifies a password hashing algorithm.
type Algorithm string

// AlgorithmPBKDF2SHA256 is PBKDF2 with HMAC-SHA256, per RFC 8018.
const AlgorithmPBKDF2SHA256 Algorithm = "pbkdf2-sha256"

// Default parameters.
//
// Iterations follows contemporary guidance for PBKDF2-HMAC-SHA256. It is stored
// with each hash, so raising it later upgrades existing users on next login
// rather than locking them out.
const (
	DefaultIterations = 600_000
	DefaultSaltLength = 16
	DefaultKeyLength  = 32

	// MinIterations is the lowest value this package will accept when
	// verifying. A stored hash below it is treated as needing a rehash.
	MinIterations = 100_000

	// MinPasswordLength is the shortest password that may be hashed.
	MinPasswordLength = 12
	// MaxPasswordLength bounds work per hash attempt. Without it, an
	// unauthenticated caller could force arbitrary CPU consumption.
	MaxPasswordLength = 1024
)

// Errors returned by this package.
var (
	// ErrMismatch means the password does not match the hash. It is
	// deliberately indistinguishable from an unknown-user outcome at the
	// caller's level, so that timing and messaging do not disclose which
	// accounts exist.
	ErrMismatch = errors.New("password does not match")
	// ErrMalformedHash means a stored hash could not be parsed.
	ErrMalformedHash = errors.New("stored password hash is malformed")
	// ErrPasswordTooShort means the password is below MinPasswordLength.
	ErrPasswordTooShort = errors.New("password is too short")
	// ErrPasswordTooLong means the password exceeds MaxPasswordLength.
	ErrPasswordTooLong = errors.New("password is too long")
	// ErrUnsupportedAlgorithm means the hash names an algorithm this build
	// cannot verify.
	ErrUnsupportedAlgorithm = errors.New("unsupported password hashing algorithm")
)

// Params are the tunable cost parameters.
type Params struct {
	Iterations int
	SaltLength int
	KeyLength  int
}

// DefaultParams returns the current recommended parameters.
func DefaultParams() Params {
	return Params{
		Iterations: DefaultIterations,
		SaltLength: DefaultSaltLength,
		KeyLength:  DefaultKeyLength,
	}
}

// ValidatePassword checks a candidate password against length policy.
//
// It deliberately enforces length only. Composition rules (mixed case, symbols)
// push people toward predictable substitutions and away from length, which is
// what actually resists offline attack.
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength {
		return fmt.Errorf("%w: use at least %d characters (a passphrase of several words works well)",
			ErrPasswordTooShort, MinPasswordLength)
	}
	if len(password) > MaxPasswordLength {
		return fmt.Errorf("%w: use at most %d bytes", ErrPasswordTooLong, MaxPasswordLength)
	}
	return nil
}

// Hash derives a password hash using the supplied parameters.
//
// The result is a self-describing string of the form
//
//	$pbkdf2-sha256$i=600000$<base64 salt>$<base64 key>
//
// which records everything needed to verify it and to decide later whether it
// should be upgraded.
func Hash(password string, p Params) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	if p.Iterations <= 0 {
		p.Iterations = DefaultIterations
	}
	if p.SaltLength <= 0 {
		p.SaltLength = DefaultSaltLength
	}
	if p.KeyLength <= 0 {
		p.KeyLength = DefaultKeyLength
	}

	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("cannot generate a password salt: %w", err)
	}

	key := pbkdf2([]byte(password), salt, p.Iterations, p.KeyLength, sha256.New)
	return encode(AlgorithmPBKDF2SHA256, p.Iterations, salt, key), nil
}

// Verify reports whether password matches encoded.
//
// It returns ErrMismatch for a wrong password and ErrMalformedHash for a hash
// that cannot be parsed. Comparison is constant-time.
func Verify(password, encoded string) error {
	alg, iterations, salt, want, err := decode(encoded)
	if err != nil {
		return err
	}
	if alg != AlgorithmPBKDF2SHA256 {
		return fmt.Errorf("%w: %q", ErrUnsupportedAlgorithm, alg)
	}
	if len(password) > MaxPasswordLength {
		return ErrMismatch
	}

	got := pbkdf2([]byte(password), salt, iterations, len(want), sha256.New)
	if subtle.ConstantTimeCompare(got, want) != 1 {
		return ErrMismatch
	}
	return nil
}

// NeedsRehash reports whether a stored hash is weaker than current policy.
//
// Call it after a successful Verify; when it returns true, rehash the password
// the caller has just proven and store the new value.
func NeedsRehash(encoded string, p Params) bool {
	alg, iterations, salt, key, err := decode(encoded)
	if err != nil {
		return true
	}
	if alg != AlgorithmPBKDF2SHA256 {
		return true
	}
	if iterations < p.Iterations || iterations < MinIterations {
		return true
	}
	if len(salt) < p.SaltLength || len(key) < p.KeyLength {
		return true
	}
	return false
}

func encode(alg Algorithm, iterations int, salt, key []byte) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$%s$i=%d$%s$%s", alg, iterations, b64.EncodeToString(salt), b64.EncodeToString(key))
}

func decode(encoded string) (Algorithm, int, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// A well-formed value splits into: "", alg, params, salt, key.
	if len(parts) != 5 || parts[0] != "" {
		return "", 0, nil, nil, fmt.Errorf("%w: expected four $-separated fields", ErrMalformedHash)
	}

	alg := Algorithm(parts[1])

	if !strings.HasPrefix(parts[2], "i=") {
		return "", 0, nil, nil, fmt.Errorf("%w: missing iteration count", ErrMalformedHash)
	}
	iterations, err := strconv.Atoi(strings.TrimPrefix(parts[2], "i="))
	if err != nil || iterations <= 0 {
		return "", 0, nil, nil, fmt.Errorf("%w: iteration count is not a positive integer", ErrMalformedHash)
	}

	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[3])
	if err != nil {
		return "", 0, nil, nil, fmt.Errorf("%w: salt is not valid base64", ErrMalformedHash)
	}
	key, err := b64.DecodeString(parts[4])
	if err != nil {
		return "", 0, nil, nil, fmt.Errorf("%w: key is not valid base64", ErrMalformedHash)
	}
	if len(salt) == 0 || len(key) == 0 {
		return "", 0, nil, nil, fmt.Errorf("%w: salt and key must not be empty", ErrMalformedHash)
	}
	return alg, iterations, salt, key, nil
}

// pbkdf2 implements PBKDF2 as specified in RFC 8018 section 5.2.
//
// The standard library provides HMAC and SHA-256 but not PBKDF2, and this build
// has no access to golang.org/x/crypto. The construction below is a direct
// transcription of the specification.
func pbkdf2(password, salt []byte, iterations, keyLength int, h func() hash.Hash) []byte {
	prf := hmac.New(h, password)
	hashLen := prf.Size()
	blocks := (keyLength + hashLen - 1) / hashLen

	out := make([]byte, 0, blocks*hashLen)
	buf := make([]byte, 4)
	block := make([]byte, hashLen)

	for i := 1; i <= blocks; i++ {
		prf.Reset()
		prf.Write(salt)
		// The block index is appended as a four-byte big-endian integer.
		buf[0] = byte(i >> 24)
		buf[1] = byte(i >> 16)
		buf[2] = byte(i >> 8)
		buf[3] = byte(i)
		prf.Write(buf)
		u := prf.Sum(nil)
		copy(block, u)

		for n := 2; n <= iterations; n++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			for j := range block {
				block[j] ^= u[j]
			}
		}
		out = append(out, block...)
	}
	return out[:keyLength]
}
