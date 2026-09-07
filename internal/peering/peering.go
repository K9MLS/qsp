// Package peering turns a link between two QSP instances into something two
// administrators agree to, rather than something that appears because a file
// says so.
//
// # Why this exists
//
// OpenBridge has no connection. There is no login, no accept, no session —
// each side sends UDP datagrams to an address it was told about and
// authenticates them against a shared passphrase. So the consent is real,
// because neither administrator can peer without the other handing them a
// secret, and it is completely invisible: nothing in a console distinguishes a
// link that two people negotiated from a line somebody pasted into a
// configuration.
//
// **A new handshake was the wrong answer.** OpenBridge is worth having because
// BrandMeister requires it and HBlink speaks it; a QSP-only handshake would
// mean QSP peers with QSP and nothing else. Nothing here changes a byte on the
// wire. It makes the out-of-band exchange an artefact instead of a
// conversation, so it can be checked, recorded, and refused.
//
// # The passphrase does not travel with the invitation
//
// An invitation carries where to send, what to expect, and who is asking. It
// carries a *fingerprint* of the passphrase and never the passphrase itself.
//
// The invitation is meant to go by email, which is exactly what the join page
// already refuses to send a peer password over. Splitting them means the token
// in a forwarded thread is not a credential, and typing the wrong secret fails
// immediately against the fingerprint rather than as silence on a link that
// looks configured.
//
// Nothing here does I/O.
package peering

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"time"
)

// Prefix marks a token and names its version. A future format changes the
// number, so an old QSP refuses a new token rather than misreading it.
const Prefix = "QSP-PEER-1."

// Lifetime is how long an invitation stays valid.
//
// **An invitation is a standing offer to send audio to a network.** Left
// without an expiry, one sits in a mail archive indefinitely and is as good the
// day somebody leaves the club as the day it was written.
const Lifetime = 14 * 24 * time.Hour

// MinPassphraseBytes is the shortest passphrase this will accept.
//
// The passphrase is the entire security boundary: anyone holding it can put
// audio on the network as a peer network. QSP generates them rather than
// letting an administrator type one, and refuses a short one even when typed
// deliberately.
const MinPassphraseBytes = 24

// Talkgroup is one talkgroup an invitation proposes to exchange.
type Talkgroup struct {
	Talkgroup uint32 `json:"talkgroup"`
	Timeslot  int    `json:"timeslot"`
}

// Invitation is what one administrator sends another.
//
// Every field is shown to the accepting administrator before anything is
// written, which is the point: a peering is agreed by somebody who saw what
// they were agreeing to.
type Invitation struct {
	// Network and Callsign say who is asking. A callsign is a licensed
	// identity that can be looked up, which is a stronger claim than a name.
	Network  string `json:"network"`
	Callsign string `json:"callsign"`
	// Address is where the far end sends frames, as host:port.
	Address string `json:"address"`
	// NetworkID is the identifier this instance announces on the link.
	NetworkID uint32 `json:"network_id"`
	// Latitude and Longitude are decimal degrees, as the rest of DMR carries
	// them. Optional.
	Latitude  string `json:"latitude,omitempty"`
	Longitude string `json:"longitude,omitempty"`
	// Export and Import are what this side proposes to send and to receive,
	// in its own numbering.
	Export []Talkgroup `json:"export,omitempty"`
	Import []Talkgroup `json:"import,omitempty"`
	// Fingerprint identifies the passphrase without carrying it.
	Fingerprint string `json:"fingerprint"`
	// Issued is when this was generated, in UTC.
	Issued time.Time `json:"issued"`
}

// Errors a caller is expected to distinguish and explain.
var (
	ErrNotAnInvitation = errors.New("peering: this is not a QSP peering invitation")
	ErrVersion         = errors.New("peering: this invitation was written by a newer QSP")
	ErrTruncated       = errors.New("peering: this invitation is incomplete; it was probably " +
		"cut short when it was copied")
	ErrExpired     = errors.New("peering: this invitation has expired")
	ErrFingerprint = errors.New("peering: that passphrase does not match this invitation")
	ErrWeak        = fmt.Errorf("peering: a passphrase must be at least %d characters", MinPassphraseBytes)
	ErrNoAddress   = errors.New("peering: an invitation needs an address for the far end to send to")
	ErrNoCallsign  = errors.New("peering: an invitation needs a callsign, so the other operator " +
		"knows who is asking")
	ErrNoNetworkID = errors.New("peering: an invitation needs a network ID")
	// ErrSchemeInAddress is the mistake an operator makes because every other
	// address they type all day has a scheme on the front.
	//
	// **The form accepted "https://qsp.hopto.me:62045" without a word.** A
	// peering is carried over UDP to a host and a port; there is no URL, no
	// TLS and nothing to speak HTTP to. Left alone it produces a link that
	// resolves nothing and a far end that waits in silence, which is the
	// hardest kind of fault to find.
	ErrSchemeInAddress = errors.New("peering: an address is a host and a UDP port, " +
		"like qsp.example.com:62045 — remove the http:// or https:// from the front")
	// ErrBindAddress is a listen address offered as somewhere to send to.
	//
	// **0.0.0.0 means every interface on this machine**, and it is the right
	// thing in "we listen on". Put into an invitation it tells the far end to
	// send to every interface on *their* machine, which reaches nothing. The
	// accept form copied its listen address straight into the reply, so this
	// went out on a real peering.
	ErrBindAddress = errors.New("peering: 0.0.0.0 is where this server listens, not " +
		"somewhere the other end can reach — give the name or address they should send to")
)

// NewPassphrase returns a fresh passphrase.
//
// Generated rather than chosen. `pair-test-passphrase` was good enough for two
// instances on one desk and is not good enough for a link that carries a
// club's audio, and an administrator asked to invent one will produce something
// closer to the former.
func NewPassphrase() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("peering: generating a passphrase: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// memberAlphabet omits characters that are misread when a password is typed
// from a screen or dictated: 0 and O, 1 and l and I, and the vowels that let a
// generated string spell something unfortunate.
const memberAlphabet = "23456789bcdfghjkmnpqrstvwxz"

// MemberPasswordLength is how many characters NewMemberPassword produces.
//
// Ten characters of this alphabet is a little over 47 bits, which is far beyond
// reach for the offline attack that matters here and short enough to type on a
// phone without a mistake.
const MemberPasswordLength = 10

// NewMemberPassword returns a password for one member's hotspot.
//
// # Why this is not NewPassphrase
//
// NewPassphrase produces 32 random bytes as 43 characters of base64, which is
// right for a link passphrase pasted between two servers and wrong for a person.
// The same function served both, so a screen aimed at a club member handed them
// a mixed-case 43-character string to type into a Pi-Star, probably on a phone.
//
// # Why not something memorable, as other networks use
//
// Homebrew authentication is a challenge-response: the master sends a nonce and
// the peer returns a hash of it with the password, so the password never crosses
// the wire. **That also means anyone who captures one login can try candidates
// offline as fast as they can hash.** A postcode is five digits and falls in
// well under a second; QSP's port faces the internet and has already logged
// thousands of datagrams from radio IDs it does not know.
//
// Ten characters from an unambiguous alphabet is the trade: unguessable, and
// short enough to read aloud and type once.
func NewMemberPassword() (string, error) {
	out := make([]byte, MemberPasswordLength)
	buf := make([]byte, MemberPasswordLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("peering: generating a member password: %w", err)
	}
	// Rejection-free selection would bias the alphabet; the alphabet's length
	// does not divide 256 evenly, so bytes above the largest whole multiple are
	// redrawn rather than folded.
	limit := byte(256 - (256 % len(memberAlphabet)))
	for i := 0; i < len(out); {
		if len(buf) == 0 {
			buf = make([]byte, MemberPasswordLength)
			if _, err := rand.Read(buf); err != nil {
				return "", fmt.Errorf("peering: generating a member password: %w", err)
			}
		}
		b := buf[0]
		buf = buf[1:]
		if b >= limit {
			continue
		}
		out[i] = memberAlphabet[int(b)%len(memberAlphabet)]
		i++
	}
	// Grouped, because that is how people transcribe without losing their place.
	return string(out[0:4]) + "-" + string(out[4:7]) + "-" + string(out[7:10]), nil
}

// FingerprintOf returns a short identifier for a passphrase.
//
// A plain hash is sufficient here and would not be for a chosen secret: these
// carry 256 bits from crypto/rand, so there is no guess to make. It exists to
// catch a mistyped or stale passphrase at the moment of pasting, rather than as
// silence on a link that reports itself configured.
func FingerprintOf(passphrase string) string {
	sum := sha256.Sum256([]byte(passphrase))
	return hex.EncodeToString(sum[:6])
}

// Validate reports whether an invitation is complete enough to send.
func (inv Invitation) Validate() error {
	switch {
	case strings.TrimSpace(inv.Address) == "":
		return ErrNoAddress
	case strings.Contains(inv.Address, "://"):
		return ErrSchemeInAddress
	case strings.HasPrefix(strings.TrimSpace(inv.Address), "0.0.0.0:"),
		strings.HasPrefix(strings.TrimSpace(inv.Address), "[::]:"):
		return ErrBindAddress
	case strings.TrimSpace(inv.Callsign) == "":
		return ErrNoCallsign
	case inv.NetworkID == 0:
		return ErrNoNetworkID
	}
	return nil
}

// Accept checks a passphrase against an invitation, and the invitation against
// the clock.
//
// Order matters. An expired invitation is reported as expired even when the
// passphrase is also wrong, because "this expired" tells an administrator to
// ask for a new one and "wrong passphrase" sends them hunting through an email
// thread for a secret that would not have worked anyway.
func (inv Invitation) Accept(passphrase string, now time.Time) error {
	if now.After(inv.Issued.Add(Lifetime)) {
		return ErrExpired
	}
	if len(passphrase) < MinPassphraseBytes {
		return ErrWeak
	}
	if FingerprintOf(passphrase) != inv.Fingerprint {
		return ErrFingerprint
	}
	return nil
}

// Encode renders an invitation as a single line of text.
//
// One line because it is going into an email, and a block that wraps is a block
// somebody pastes in two pieces. The trailing checksum is what makes a
// truncated paste say so rather than fail as a malformed link days later.
func Encode(inv Invitation) (string, error) {
	if err := inv.Validate(); err != nil {
		return "", err
	}
	inv.Issued = inv.Issued.UTC().Truncate(time.Second)

	body, err := json.Marshal(inv)
	if err != nil {
		return "", fmt.Errorf("peering: encoding invitation: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	sum := crc32.ChecksumIEEE([]byte(payload))
	return fmt.Sprintf("%s%s.%08x", Prefix, payload, sum), nil
}

// Decode reads an invitation, and says which way it was wrong.
func Decode(token string) (Invitation, error) {
	token = strings.TrimSpace(token)

	if !strings.HasPrefix(token, Prefix) {
		// A token from a later format is a different failure from a paste of
		// something that was never an invitation, and the two want different
		// advice.
		if strings.HasPrefix(token, "QSP-PEER-") {
			return Invitation{}, ErrVersion
		}
		return Invitation{}, ErrNotAnInvitation
	}

	rest := token[len(Prefix):]
	dot := strings.LastIndex(rest, ".")
	if dot < 0 {
		return Invitation{}, ErrTruncated
	}
	payload, want := rest[:dot], rest[dot+1:]

	if fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(payload))) != want {
		return Invitation{}, ErrTruncated
	}

	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Invitation{}, ErrTruncated
	}

	var inv Invitation
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&inv); err != nil {
		return Invitation{}, fmt.Errorf("peering: reading invitation: %w", err)
	}
	if err := inv.Validate(); err != nil {
		return Invitation{}, err
	}
	return inv, nil
}

// Reciprocal builds the invitation the accepting side sends back.
//
// **Both directions have to be configured, and only one passphrase exists.**
// OpenBridge authenticates every datagram against one shared secret, so the
// side accepting an invitation is not choosing a new one — it is answering with
// its own address and what it proposes to carry, under the secret already
// agreed. Generating a second passphrase here would produce a link that works
// one way and reports healthy.
func Reciprocal(accepted Invitation, mine Invitation) (Invitation, error) {
	mine.Fingerprint = accepted.Fingerprint
	if mine.Issued.IsZero() {
		mine.Issued = accepted.Issued
	}
	if err := mine.Validate(); err != nil {
		return Invitation{}, err
	}
	return mine, nil
}
