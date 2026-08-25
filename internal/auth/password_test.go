package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// Fast parameters for tests. Production uses DefaultParams; running 600,000
// iterations in every test would make the suite unusable without testing
// anything the known-answer vectors do not already cover.
func testParams() Params {
	return Params{Iterations: 1000, SaltLength: 16, KeyLength: 32}
}

func TestPBKDF2KnownAnswers(t *testing.T) {
	// Generated from CPython hashlib.pbkdf2_hmac, an OpenSSL-backed
	// reference implementation. Do not edit by hand.
	vectors := []struct {
		password, salt string
		iter, keyLen   int
		expectHex      string
	}{
		{"password", "salt", 1, 32, "120fb6cffcf8b32c43e7225256c4f837a86548c92ccc35480805987cb70be17b"},
		{"password", "salt", 2, 32, "ae4d0c95af6b46d32d0adff928f06dd02a303f8ef3c251dfd6e2d85a95474c43"},
		{"password", "salt", 4096, 32, "c5e478d59288c841aa530db6845c4c8d962893a001ce4e11a4963873aa98134a"},
		{"passwordPASSWORDpassword", "saltSALTsaltSALTsaltSALTsaltSALTsalt", 4096, 40, "348c89dbcbd32b2f32d814b8116e84cf2b17347ebc1800181c4e2a1fb8dd53e1c635518c7dac47e9"},
		{"pass\x00word", "sa\x00lt", 4096, 16, "89b69d0516f829893c696226650a8687"},
		{"K9MLS-qsp-test-passphrase", "qspsalt0123456789", 1000, 64, "529ab3e0d924e17e9c2288ca03da79edaaea62400cda0fdcc8a56142553fc6ca9614bd528b4a4499d77e601295bb4347dfe23827a182b6d551a1b2f37da1b947"},
	}

	for _, v := range vectors {
		got := pbkdf2([]byte(v.password), []byte(v.salt), v.iter, v.keyLen, sha256.New)
		if hex.EncodeToString(got) != v.expectHex {
			t.Errorf("pbkdf2(%q, %q, %d, %d):\n got  %s\n want %s",
				v.password, v.salt, v.iter, v.keyLen, hex.EncodeToString(got), v.expectHex)
		}
	}
}

func TestPBKDF2MultiBlockOutput(t *testing.T) {
	// A key longer than one SHA-256 block exercises the block-concatenation
	// path; the 64-byte vector above covers it, and this asserts the length
	// contract for a non-multiple of the hash size.
	got := pbkdf2([]byte("password"), []byte("salt"), 10, 47, sha256.New)
	if len(got) != 47 {
		t.Errorf("got %d bytes, want 47", len(got))
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	const password = "correct horse battery staple"
	encoded, err := Hash(password, testParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if err := Verify(password, encoded); err != nil {
		t.Errorf("Verify with the correct password: %v", err)
	}
}

func TestVerifyRejectsWrongPassword(t *testing.T) {
	encoded, err := Hash("correct horse battery staple", testParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	err = Verify("incorrect horse battery staple", encoded)
	if !errors.Is(err, ErrMismatch) {
		t.Errorf("got %v, want ErrMismatch", err)
	}
}

func TestHashIsSaltedSoIdenticalPasswordsDiffer(t *testing.T) {
	const password = "correct horse battery staple"
	a, err := Hash(password, testParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	b, err := Hash(password, testParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if a == b {
		t.Error("two hashes of the same password are identical; the salt is not random")
	}
	if err := Verify(password, a); err != nil {
		t.Errorf("first hash does not verify: %v", err)
	}
	if err := Verify(password, b); err != nil {
		t.Errorf("second hash does not verify: %v", err)
	}
}

func TestHashIsSelfDescribing(t *testing.T) {
	encoded, err := Hash("correct horse battery staple", testParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$pbkdf2-sha256$i=1000$") {
		t.Errorf("hash does not carry its algorithm and cost: %q", encoded)
	}
	if strings.Count(encoded, "$") != 4 {
		t.Errorf("hash has %d separators, want 4: %q", strings.Count(encoded, "$"), encoded)
	}
}

func TestHashNeverContainsThePassword(t *testing.T) {
	const password = "an-unmistakable-passphrase-value"
	encoded, err := Hash(password, testParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if strings.Contains(encoded, password) {
		t.Fatal("the encoded hash contains the plaintext password")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("got %v, want ErrPasswordTooShort", err)
	}
	if err := ValidatePassword(strings.Repeat("a", MaxPasswordLength+1)); !errors.Is(err, ErrPasswordTooLong) {
		t.Errorf("got %v, want ErrPasswordTooLong", err)
	}
	if err := ValidatePassword("twelvechars!"); err != nil {
		t.Errorf("a %d-character password was rejected: %v", MinPasswordLength, err)
	}
}

func TestValidatePasswordCountsRunesNotBytes(t *testing.T) {
	// Twelve multi-byte runes are twelve characters to the person typing them.
	if err := ValidatePassword("パスワード１２３４５６７"); err != nil {
		t.Errorf("a 12-rune password was rejected: %v", err)
	}
}

func TestValidatePasswordErrorSuggestsAFix(t *testing.T) {
	err := ValidatePassword("short")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "passphrase") {
		t.Errorf("error should suggest what to do, got: %v", err)
	}
}

func TestHashRejectsShortPassword(t *testing.T) {
	if _, err := Hash("short", testParams()); !errors.Is(err, ErrPasswordTooShort) {
		t.Errorf("got %v, want ErrPasswordTooShort", err)
	}
}

func TestVerifyRejectsOverlongPasswordWithoutHashing(t *testing.T) {
	// Bounding work per attempt prevents an unauthenticated caller from
	// forcing arbitrary CPU consumption.
	encoded, err := Hash("correct horse battery staple", testParams())
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	err = Verify(strings.Repeat("a", MaxPasswordLength+1), encoded)
	if !errors.Is(err, ErrMismatch) {
		t.Errorf("got %v, want ErrMismatch", err)
	}
}

func TestVerifyRejectsMalformedHashes(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"no leading dollar":  "pbkdf2-sha256$i=1000$c2FsdA$a2V5",
		"too few fields":     "$pbkdf2-sha256$i=1000$c2FsdA",
		"missing iterations": "$pbkdf2-sha256$x=1000$c2FsdA$a2V5",
		"zero iterations":    "$pbkdf2-sha256$i=0$c2FsdA$a2V5",
		"negative iters":     "$pbkdf2-sha256$i=-5$c2FsdA$a2V5",
		"bad base64 salt":    "$pbkdf2-sha256$i=1000$!!!!$a2V5",
		"bad base64 key":     "$pbkdf2-sha256$i=1000$c2FsdA$!!!!",
		"empty salt":         "$pbkdf2-sha256$i=1000$$a2V5",
		"garbage":            "not a hash at all",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			err := Verify("correct horse battery staple", encoded)
			if !errors.Is(err, ErrMalformedHash) {
				t.Errorf("got %v, want ErrMalformedHash", err)
			}
		})
	}
}

func TestVerifyRejectsUnknownAlgorithm(t *testing.T) {
	err := Verify("correct horse battery staple", "$bcrypt$i=1000$c2FsdA$a2V5")
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Errorf("got %v, want ErrUnsupportedAlgorithm", err)
	}
}

func TestNeedsRehash(t *testing.T) {
	current := Params{Iterations: 200_000, SaltLength: 16, KeyLength: 32}

	weak, err := Hash("correct horse battery staple", Params{Iterations: 1000, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !NeedsRehash(weak, current) {
		t.Error("a hash below current cost was not flagged for rehash")
	}

	strong, err := Hash("correct horse battery staple", current)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if NeedsRehash(strong, current) {
		t.Error("a hash meeting current cost was flagged for rehash")
	}
}

func TestNeedsRehashOnUnparseableHash(t *testing.T) {
	if !NeedsRehash("garbage", DefaultParams()) {
		t.Error("an unparseable hash should be flagged for rehash")
	}
}

func TestNeedsRehashOnUnknownAlgorithm(t *testing.T) {
	if !NeedsRehash("$scrypt$i=600000$c2FsdA$a2V5", DefaultParams()) {
		t.Error("a hash from another algorithm should be flagged for rehash")
	}
}

func TestHashAppliesDefaultsForZeroParams(t *testing.T) {
	encoded, err := Hash("correct horse battery staple", Params{})
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.Contains(encoded, "i="+itoa(DefaultIterations)) {
		t.Errorf("zero params did not take the default iteration count: %q", encoded)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
