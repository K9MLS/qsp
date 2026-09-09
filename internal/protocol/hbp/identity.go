package hbp

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
)

// Identity is what a QSP server says about itself to a QSP server that has
// registered with it.
//
// # This tag is QSP's own, and is not HBP
//
// Everything else in this package is a message observed in a capture and
// implemented from what was seen. This one is not: it is an extension, and it
// is here rather than in its own package because `Parse` is the single door
// every datagram comes through and a second parser beside it would be a second
// thing to keep true.
//
// # Why it exists
//
// **Identity rides on registration, and registration goes one way.** A QSP link
// is a peer registration (ADR-0051): the dialling side announces a callsign, a
// network name and a software string in its configuration, and the listening
// side answers `RPTACK` — four bytes and an ID. So the server that listens
// knows its neighbour and the server that dials knows an address.
//
// ADR-0052 rule 3 requires that a server says what it is, and it was satisfied
// in one direction only. On a console at the dialling end, a link to a
// neighbouring network appears as `192.168.1.247:62031` — while the same link
// on the other operator's console shows a name, a network and a version.
//
// # Why sending it is safe
//
// **It is sent only to a peer that announced itself as a QSP link**, which is
// what the package ID already distinguishes (ADR-0051). A hotspot never
// receives one, because a hotspot never claims to be one.
//
// An older QSP that does not know this tag reports an unparseable datagram as a
// note and carries on: `Link.Receive` treats a parse failure as something to
// mention rather than something to fail a link over, and anything well-formed
// or not still counts as the far end being alive. So a new server linking to an
// old one loses the identity and keeps the link, which is the correct order of
// those two outcomes.
//
// # Why the payload is JSON
//
// The fixed-width, comma-separated shape of `RPTC` is what HBP does and it is
// full: adding a field means agreeing an offset with every implementation that
// has ever parsed it. This field set is going to grow — ADR-0053 puts a server
// identifier here next, and a public-key fingerprint may follow it — so unknown
// fields are ignored rather than refused, which is the opposite of the rule for
// an invitation token and for the same reason: a token is read once by a human
// pasting it, and this is read on every link by software that may be older than
// the server sending it.
type Identity struct {
	// RepeaterID is the ID of the peer this is addressed to, so a server with
	// several links can tell which one answered.
	RepeaterID RepeaterID
	// Network is the display name the sending server announces about itself.
	// ADR-0053 calls this the display name: an operator's choice, changeable,
	// and the thing both consoles head a link with.
	Network string
	// Callsign is the operator's callsign at the sending server.
	Callsign string
	// Software is what the sending server runs, verbatim and unverified.
	Software string
	// Description is free text about the server, when it has any.
	Description string
}

// identityPayload is the wire form of the fields above.
//
// Named separately from Identity so that the Go field names and the wire names
// can move independently: renaming a Go field must not silently change a
// protocol.
type identityPayload struct {
	Network     string `json:"network,omitempty"`
	Callsign    string `json:"callsign,omitempty"`
	Software    string `json:"software,omitempty"`
	Description string `json:"description,omitempty"`
}

// identityTag is the four bytes that mark this message.
const identityTag = "QSPI"

// identityMaxPayload caps the JSON a sender may put in one of these.
//
// **A datagram is read from a hostile network.** The cap is generous for the
// field set and small enough that a peer cannot make a listener allocate on
// demand; a longer payload is refused rather than truncated, because half a
// JSON document is not a smaller identity, it is a parse error somewhere less
// obvious.
const identityMaxPayload = 4096

// Kind reports the message type.
func (i Identity) Kind() Kind { return KindIdentity }

// Marshal returns the wire encoding.
func (i Identity) Marshal() []byte { return i.AppendTo(nil) }

// AppendTo appends the wire encoding to dst.
func (i Identity) AppendTo(dst []byte) []byte {
	body, err := json.Marshal(identityPayload{
		Network:     strings.TrimSpace(i.Network),
		Callsign:    strings.TrimSpace(i.Callsign),
		Software:    strings.TrimSpace(i.Software),
		Description: strings.TrimSpace(i.Description),
	})
	if err != nil {
		// The payload is four strings; there is no input that fails to encode.
		// An empty object still parses as an identity that says nothing, which
		// is better than a datagram that is half a message.
		body = []byte("{}")
	}

	dst = append(dst, identityTag...)
	dst = binary.BigEndian.AppendUint32(dst, uint32(i.RepeaterID))
	return append(dst, body...)
}

// parseIdentity decodes a QSP identity message.
func parseIdentity(b []byte) (Message, error) {
	const header = len(identityTag) + 4
	if len(b) < header {
		return nil, fmt.Errorf("%w: got %d bytes, need at least %d for %s",
			ErrShort, len(b), header, identityTag)
	}
	body := b[header:]
	if len(body) > identityMaxPayload {
		return nil, fmt.Errorf("%w: %s payload is %d bytes, over the %d-byte limit",
			ErrFieldFormat, identityTag, len(body), identityMaxPayload)
	}

	out := Identity{RepeaterID: RepeaterID(binary.BigEndian.Uint32(b[len(identityTag):header]))}
	if len(body) == 0 {
		return out, nil
	}

	var p identityPayload
	// **Unknown fields are ignored, deliberately.** A server older than the one
	// it is linked to must read the fields it knows and disregard the rest;
	// refusing would mean a new field in a later QSP breaks every link to an
	// older one, which is exactly the failure a version number exists to avoid.
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("%w: %s payload: %v", ErrFieldFormat, identityTag, err)
	}

	out.Network = p.Network
	out.Callsign = p.Callsign
	out.Software = p.Software
	out.Description = p.Description
	return out, nil
}
