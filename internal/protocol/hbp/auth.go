package hbp

import "crypto/sha256"

// Digest computes the authentication response for a login challenge.
//
// The construction is SHA-256 over the four salt bytes from Ack immediately
// followed by the shared password, with no separator, length prefix or text
// encoding:
//
//	digest = SHA-256(salt ‖ password)
//
// This was determined by capturing a real login and confirming the observed
// digest against a locally computed one; it is not inferred from any
// specification or implementation. testdata/hbp/hbp-login-session.pcap contains
// a known-answer case using a published test password, so a regression in this
// function fails a test rather than silently locking every peer out.
//
// The password is not retained.
func Digest(salt [4]byte, password []byte) [DigestSize]byte {
	h := sha256.New()
	h.Write(salt[:])
	h.Write(password)

	var out [DigestSize]byte
	copy(out[:], h.Sum(nil))
	return out
}

// VerifyDigest reports whether got matches the digest computed from salt and
// password.
//
// The comparison is not constant-time and deliberately so: both inputs are
// values a peer has already sent in the clear over UDP, so there is no secret
// to leak through timing. The password itself never takes part in a comparison.
func VerifyDigest(salt [4]byte, password []byte, got [DigestSize]byte) bool {
	return Digest(salt, password) == got
}
