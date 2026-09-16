package zello

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Signing the token a logon carries.
//
// # What Zello wants
//
// `auth_token` on the logon command is a JSON Web Token signed RS256 with the
// private key from the developer portal. Its shape, read from a token Zello's
// portal issued:
//
//	header  {"typ":"JWT","alg":"RS256"}
//	claims  {"iss":"<issuer>","exp":<unix>,"azp":"dev"}
//
// The issuer string's first segment is itself base64 and decodes to
// `ZC:<account>:<key number>` — Zello Channels, the account, which key. So the
// issuer identifies the key pair and the signature proves possession of it.
//
// # QSP mints its own rather than storing one
//
// A token from the portal carries an expiry a month out. **Storing that would
// mean a credential that silently stops working**, on a date nothing records,
// at a moment nobody chose — and the failure would be a connector that has
// been fine for weeks refusing to log on.
//
// So the private key is the stored credential and the token is made fresh for
// each connection. That also means a reconnection after an outage is never
// blocked by an expiry that passed while the link was down.
//
// # The claim this package cannot verify
//
// `azp` is `dev` in a developer token. **Whether a production gateway needs
// something else is not something the specification says**, and guessing would
// produce a logon refused for a reason that looks like bad credentials. It is
// configurable with `dev` as the default, and the first real connection is
// what settles it.

// TokenAudience is the `azp` claim a developer token carries.
const TokenAudience = "dev"

// TokenLifetime is how long a minted token is valid.
//
// Short, because a token is made per connection and there is nothing to gain
// from one that outlives the session it was made for. Long enough that a slow
// handshake or a clock a little out of step does not refuse a valid login.
const TokenLifetime = time.Hour

// Signer makes tokens from a private key.
type Signer struct {
	issuer   string
	key      *rsa.PrivateKey
	audience string
	now      func() time.Time
}

// SignerOptions configures a signer.
type SignerOptions struct {
	// Issuer is the issuer string from the developer portal. Required.
	Issuer string
	// PrivateKeyPEM is the PEM-encoded RSA private key. Required.
	PrivateKeyPEM string
	// Audience is the `azp` claim. Empty selects TokenAudience.
	Audience string
	// Now is the clock, for tests.
	Now func() time.Time
}

// NewSigner parses a private key and returns a signer.
//
// **The key is parsed once, at startup.** A key that cannot be read is a
// configuration error and should be reported when it is entered, not at the
// first reconnection at two in the morning.
func NewSigner(opts SignerOptions) (*Signer, error) {
	if strings.TrimSpace(opts.Issuer) == "" {
		return nil, errors.New(
			"zello: a token needs the issuer string from the developer portal; " +
				"the signature alone does not say which key pair made it")
	}
	key, err := parseRSAPrivateKey(opts.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if opts.Audience == "" {
		opts.Audience = TokenAudience
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Signer{
		issuer:   strings.TrimSpace(opts.Issuer),
		key:      key,
		audience: opts.Audience,
		now:      opts.Now,
	}, nil
}

// MinimumKeyBits is the smallest RSA key this will sign with.
//
// 2048 is the floor every current recommendation gives, and a key below it is
// far more likely to be a test key somebody pasted by mistake than a
// deliberate choice.
const MinimumKeyBits = 2048

// parseRSAPrivateKey reads a PEM private key in either common encoding.
//
// **Both PKCS#8 and PKCS#1**, because the two headers look almost identical —
// `BEGIN PRIVATE KEY` against `BEGIN RSA PRIVATE KEY` — and an operator
// pasting whichever their tool produced should not have to know which they
// have.
func parseRSAPrivateKey(text string) (*rsa.PrivateKey, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, errors.New("zello: no private key was given")
	}

	block, _ := pem.Decode([]byte(trimmed))
	if block == nil {
		// **The most common reason, named.** A PEM header is five dashes on
		// each side, and a copy that lost one is unreadable everywhere while
		// looking entirely normal.
		hint := ""
		if strings.Contains(trimmed, "----BEGIN") && !strings.Contains(trimmed, "-----BEGIN") {
			hint = "; the header begins with four dashes and a PEM header has five, " +
				"so the key was probably damaged when it was copied"
		}
		return nil, fmt.Errorf("zello: the private key is not readable PEM%s", hint)
	}

	var key *rsa.PrivateKey
	switch parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes); {
	case err == nil:
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf(
				"zello: the private key is %T and RS256 needs an RSA key", parsed)
		}
		key = rsaKey
	default:
		// Not PKCS#8; try PKCS#1 before giving up.
		rsaKey, err1 := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err1 != nil {
			return nil, fmt.Errorf(
				"zello: the private key is neither PKCS#8 (%v) nor PKCS#1 (%v)",
				err, err1)
		}
		key = rsaKey
	}

	if bits := key.N.BitLen(); bits < MinimumKeyBits {
		return nil, fmt.Errorf(
			"zello: the private key is %d bits and %d is the minimum; a key this "+
				"small is more likely to be a test key pasted by mistake",
			bits, MinimumKeyBits)
	}
	if err := key.Validate(); err != nil {
		return nil, fmt.Errorf("zello: the private key is not self-consistent: %w", err)
	}
	return key, nil
}

// Token mints a token valid for TokenLifetime.
func (s *Signer) Token() (string, error) {
	header, err := json.Marshal(map[string]string{"typ": "JWT", "alg": "RS256"})
	if err != nil {
		return "", fmt.Errorf("zello: cannot encode a token header: %w", err)
	}
	claims, err := json.Marshal(map[string]any{
		"iss": s.issuer,
		"exp": s.now().Add(TokenLifetime).Unix(),
		"azp": s.audience,
	})
	if err != nil {
		return "", fmt.Errorf("zello: cannot encode token claims: %w", err)
	}

	// **Base64url without padding**, which is what a JWT requires: standard
	// base64's `+`, `/` and `=` are not safe in a token that travels in
	// headers and URLs, and a padded segment is rejected by strict parsers.
	signing := encodeSegment(header) + "." + encodeSegment(claims)

	digest := sha256.Sum256([]byte(signing))
	signature, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("zello: cannot sign a token: %w", err)
	}
	return signing + "." + encodeSegment(signature), nil
}

// encodeSegment renders one part of a token.
func encodeSegment(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// Issuer reports the issuer the signer uses, for a log line.
//
// **The issuer and never the key.** An issuer identifies a key pair and is
// safe to log; the key is the credential.
func (s *Signer) Issuer() string { return s.issuer }
