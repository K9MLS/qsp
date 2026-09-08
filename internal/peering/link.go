package peering

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"time"
)

// LinkPrefix marks an invitation to a QSP-to-QSP link, and names its version.
//
// # Why a second token format rather than a field in the first
//
// `Decode` rejects unknown fields, deliberately: a token it does not fully
// understand is refused rather than half-read. That is also why a new field
// cannot be added to Invitation — every QSP already deployed would report a
// QSP link invitation as malformed JSON, which tells an operator nothing about
// what is actually wrong.
//
// A distinct prefix gets the behaviour the existing comment already claims: an
// older QSP sees `QSP-PEER-` with a version it does not know and says *this was
// written by a newer QSP*, which is true and actionable.
const LinkPrefix = "QSP-PEER-2."

// LinkInvitation is an offer of a link to another QSP server.
//
// # Why this is so much smaller than Invitation
//
// An OpenBridge peering is symmetric and has no connection: both ends must know
// the other's address and network ID before either carries anything, so both
// ends are configured and the exchange takes two round trips (ADR-0050).
//
// **A QSP link is a peer registration** (ADR-0051). One side dials and the
// other listens, exactly as a hotspot does, and only the dialling side is
// configured at all. So there is nothing for the accepting side to send back,
// and this invitation is the whole exchange.
//
// What follows from that shape:
//
//   - **The listening side offers.** It is the side that has something to
//     allocate — a DMR ID in its registration list and a password against that
//     ID — and the dialling side has nothing the listener needs to be told.
//     The OpenBridge flow offers in the other direction, which is why moving to
//     this one is not a rename.
//   - **No talkgroup lists.** Everything crosses and each server's own access
//     lists decide what it keeps (ADR-0052 rule 1). `Export` and `Import` were
//     consulted by no routing code even on the OpenBridge path.
//   - **No timeslot.** The slot crosses unchanged; there is no endpoint to
//     match, which is what made the OpenBridge accept form ask a question with
//     no right answer.
//   - **No reply.** `Invitation.Reply` exists to end a two-legged exchange.
//     One leg cannot loop.
type LinkInvitation struct {
	// Network and Callsign say who is offering. Network is the display name
	// the offering server announces about itself, which is what both consoles
	// head the link with (ADR-0052 rule 2, as amended).
	Network  string `json:"network"`
	Callsign string `json:"callsign"`
	// Address is where the accepting side dials, as host:port. It is the
	// offering server's peer listener — the same port its hotspots use.
	Address string `json:"address"`
	// RepeaterID is the DMR ID the accepting side must present, allocated by
	// the offering side because that is where the registration list lives.
	//
	// **A station whose ID is not in that list is refused with MSTNAK, which
	// carries no reason.** The dialling end can then say only *check the
	// password and the repeater ID*, and the answer is in the far end's log.
	// Allocating the ID in the same act that writes the list entry is what
	// stops the two disagreeing.
	RepeaterID uint32 `json:"repeater_id"`
	// Fingerprint identifies the password without carrying it, for the reason
	// in this package's doc comment: the token travels by email and the
	// password does not.
	Fingerprint string `json:"fingerprint"`
	// Issued is when this was generated, in UTC.
	Issued time.Time `json:"issued"`
}

// Errors particular to a link invitation.
var (
	// ErrNoRepeaterID is a link invitation that allocates nothing.
	ErrNoRepeaterID = errors.New("peering: a link invitation needs the DMR ID the far end should " +
		"present; it is what this server's registration list will allow")
	// ErrLinkToken is an offer of a QSP link read as an OpenBridge peering.
	//
	// Distinguished from a malformed token because the two want opposite
	// advice: this one is valid and was read by the wrong half of the console.
	ErrLinkToken = errors.New("peering: this is an invitation to a QSP link, not an OpenBridge peering")
	// ErrOpenBridgeToken is the same mistake the other way round.
	ErrOpenBridgeToken = errors.New("peering: this is an OpenBridge peering invitation, not an " +
		"invitation to a QSP link")
)

// Kind names which sort of peering a token offers.
type Kind string

const (
	// KindOpenBridge is a peering with a network that speaks OpenBridge.
	KindOpenBridge Kind = "openbridge"
	// KindLink is a link to another QSP server.
	KindLink Kind = "qsp"
)

// KindOf reports which sort of invitation a token is, without decoding it.
//
// The console has one box an operator pastes into, and which form to show
// depends on this. Asking the operator to say which kind of token they are
// holding is asking them to read base64.
func KindOf(token string) (Kind, error) {
	token = strings.TrimSpace(token)
	switch {
	case strings.HasPrefix(token, LinkPrefix):
		return KindLink, nil
	case strings.HasPrefix(token, Prefix):
		return KindOpenBridge, nil
	case strings.HasPrefix(token, "QSP-PEER-"):
		return "", ErrVersion
	default:
		return "", ErrNotAnInvitation
	}
}

// Validate reports whether a link invitation is complete enough to send.
//
// The address checks are the OpenBridge ones and are here rather than shared
// because they are checks about *this* field: an operator typing a link's
// address makes the same two mistakes — a scheme on the front, and the address
// the server listens on rather than the one the far end can reach.
func (inv LinkInvitation) Validate() error {
	address := strings.TrimSpace(inv.Address)
	switch {
	case address == "":
		return ErrNoAddress
	case strings.Contains(address, "://"):
		return ErrSchemeInAddress
	case strings.HasPrefix(address, "0.0.0.0:"), strings.HasPrefix(address, "[::]:"):
		return ErrBindAddress
	case strings.TrimSpace(inv.Callsign) == "":
		return ErrNoCallsign
	case inv.RepeaterID == 0:
		return ErrNoRepeaterID
	}
	return nil
}

// Accept checks a password against a link invitation, and the invitation
// against the clock.
//
// Order matters, for the reason Invitation.Accept gives: an expired invitation
// says so even when the password is also wrong, because that sends the operator
// to ask for a new one rather than hunting an email thread for a secret that
// would not have worked.
func (inv LinkInvitation) Accept(password string, now time.Time) error {
	if now.After(inv.Issued.Add(Lifetime)) {
		return ErrExpired
	}
	if len(password) < MinPassphraseBytes {
		return ErrWeak
	}
	if FingerprintOf(password) != inv.Fingerprint {
		return ErrFingerprint
	}
	return nil
}

// EncodeLink renders a link invitation as a single line of text.
func EncodeLink(inv LinkInvitation) (string, error) {
	if err := inv.Validate(); err != nil {
		return "", err
	}
	inv.Issued = inv.Issued.UTC().Truncate(time.Second)

	body, err := json.Marshal(inv)
	if err != nil {
		return "", fmt.Errorf("peering: encoding link invitation: %w", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	sum := crc32.ChecksumIEEE([]byte(payload))
	return fmt.Sprintf("%s%s.%08x", LinkPrefix, payload, sum), nil
}

// DecodeLink reads a link invitation, and says which way it was wrong.
func DecodeLink(token string) (LinkInvitation, error) {
	token = strings.TrimSpace(token)

	if !strings.HasPrefix(token, LinkPrefix) {
		if strings.HasPrefix(token, Prefix) {
			return LinkInvitation{}, ErrOpenBridgeToken
		}
		if strings.HasPrefix(token, "QSP-PEER-") {
			return LinkInvitation{}, ErrVersion
		}
		return LinkInvitation{}, ErrNotAnInvitation
	}

	payload, err := split(token[len(LinkPrefix):])
	if err != nil {
		return LinkInvitation{}, err
	}

	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return LinkInvitation{}, ErrTruncated
	}

	var inv LinkInvitation
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&inv); err != nil {
		return LinkInvitation{}, fmt.Errorf("peering: reading link invitation: %w", err)
	}
	if err := inv.Validate(); err != nil {
		return LinkInvitation{}, err
	}
	return inv, nil
}

// split separates a token body from its checksum and checks it.
//
// A truncated paste is the failure this catches, and it is worth catching at
// the moment of pasting: a three-hundred-character line in a mail client wraps,
// and half of one decodes as neither JSON nor anything else recognisable days
// later.
func split(rest string) (string, error) {
	dot := strings.LastIndex(rest, ".")
	if dot < 0 {
		return "", ErrTruncated
	}
	payload, want := rest[:dot], rest[dot+1:]
	if fmt.Sprintf("%08x", crc32.ChecksumIEEE([]byte(payload))) != want {
		return "", ErrTruncated
	}
	return payload, nil
}
