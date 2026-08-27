// Package openbridge implements the OpenBridge protocol for linking QSP to
// another DMR server.
//
// OpenBridge is deliberately the smallest thing that could work: DMRD frames
// with an HMAC-SHA1 signature appended, over UDP. There is no connection
// establishment, no keep-alive, and no registration. Two servers agree a
// passphrase and a network ID out of band, and then send each other frames.
//
// This is the protocol BrandMeister requires for interconnecting a network, and
// the reason QSP does not instead log into a master as though it were a
// hotspot. See docs/adr/ADR-0018-openbridge.md.
//
// # Provenance
//
// The wire format — field names, offsets, lengths — was taken from the
// BrandMeister wiki's Open Bridge page under the "protocol documents" limb of
// ADR-0008's interim rules. No implementation source was read, and no text,
// table or code was copied. The DMRD body reuses internal/protocol/hbp, which is
// itself validated against traffic captured from a real radio.
//
// # What this package does not do
//
// It does not open sockets, retry, or decide what to forward. It converts
// between bytes and messages and says whether a signature is valid. Everything
// else belongs to the caller, for the same reason ADR-0013 keeps the routing
// decision pure: a function that only transforms values can be tested
// exhaustively without a network.
package openbridge

import (
	"crypto/hmac"
	"crypto/sha1"
	"errors"
	"fmt"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

const (
	// FrameSize is the DMRD portion: the same 53 bytes MMDVM uses.
	FrameSize = 53

	// SignatureSize is the HMAC-SHA1 signature appended to every frame. SHA-1
	// produces 20 bytes and OpenBridge appends all of them untruncated.
	SignatureSize = 20

	// PacketSize is what arrives on the wire.
	PacketSize = FrameSize + SignatureSize

	// DefaultPort is the conventional UDP port for OpenBridge.
	DefaultPort = 62035
)

// Errors returned by Parse. Callers distinguish these because they mean very
// different things operationally: a wrong length is a misconfigured or hostile
// sender, while a bad signature on a correctly sized packet usually means the
// two ends disagree about the passphrase — which is the failure an operator
// spends an evening on if nothing says so.
var (
	// ErrShortPacket means the datagram cannot be an OpenBridge frame.
	ErrShortPacket = errors.New("openbridge: packet is shorter than a signed frame")

	// ErrBadSignature means the frame did not verify against the passphrase.
	ErrBadSignature = errors.New("openbridge: signature does not verify")

	// ErrNotData means the frame carried something other than DMRD.
	// OpenBridge supports DMRD only.
	ErrNotData = errors.New("openbridge: frame is not a DMRD packet")

	// ErrNoPassphrase means the caller supplied an empty key. Signing with an
	// empty passphrase produces a valid-looking signature that any other party
	// with an empty passphrase can forge, so it is refused rather than
	// silently permitted.
	ErrNoPassphrase = errors.New("openbridge: passphrase is empty")
)

// Sign appends an HMAC-SHA1 signature to a DMRD frame, returning the datagram
// to send.
//
// The passphrase is used as the HMAC key directly, in plain text, as the
// protocol specifies. It is not hashed or derived first: doing so would produce
// signatures no other implementation accepts.
func Sign(frame []byte, passphrase []byte) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, ErrNoPassphrase
	}
	if len(frame) != FrameSize {
		return nil, fmt.Errorf("openbridge: frame is %d bytes, want %d", len(frame), FrameSize)
	}

	mac := hmac.New(sha1.New, passphrase)
	mac.Write(frame)

	out := make([]byte, 0, PacketSize)
	out = append(out, frame...)
	return mac.Sum(out), nil
}

// Verify reports whether a datagram carries a valid signature for the
// passphrase, and returns the frame with the signature removed.
//
// The comparison is constant-time. A signature check that leaks timing is a
// signature check an attacker can solve one byte at a time.
func Verify(packet []byte, passphrase []byte) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, ErrNoPassphrase
	}
	if len(packet) < PacketSize {
		return nil, fmt.Errorf("%w: %d bytes, want %d", ErrShortPacket, len(packet), PacketSize)
	}

	// Frames longer than PacketSize are rejected rather than trimmed. A sender
	// producing them disagrees with us about the format, and guessing which
	// bytes are the signature would be guessing.
	if len(packet) > PacketSize {
		return nil, fmt.Errorf("%w: %d bytes, want exactly %d", ErrShortPacket, len(packet), PacketSize)
	}

	frame, sig := packet[:FrameSize], packet[FrameSize:]

	mac := hmac.New(sha1.New, passphrase)
	mac.Write(frame)
	if !hmac.Equal(mac.Sum(nil), sig) {
		return nil, ErrBadSignature
	}
	return frame, nil
}

// Parse verifies a datagram and decodes the DMRD frame inside it.
func Parse(packet []byte, passphrase []byte) (hbp.Data, error) {
	frame, err := Verify(packet, passphrase)
	if err != nil {
		return hbp.Data{}, err
	}

	msg, err := hbp.Parse(frame)
	if err != nil {
		return hbp.Data{}, fmt.Errorf("openbridge: %w", err)
	}
	data, ok := msg.(hbp.Data)
	if !ok {
		return hbp.Data{}, fmt.Errorf("%w: got %s", ErrNotData, msg.Kind())
	}
	return data, nil
}

// Encode prepares a frame for transmission to an OpenBridge peer.
//
// Two adjustments are made, and both are the protocol's rather than the
// caller's:
//
// The repeater ID is replaced with the configured network ID. On OpenBridge
// that field identifies the sending *server*, not the repeater a transmission
// originated from — the far end uses it to decide which link a frame arrived
// on, and sending anything else makes the frame unattributable.
//
// The timeslot is forced to 1. Proper OpenBridge passes all traffic on TS1 with
// the slot bit clear. Club talkgroups are conventionally on TS2, so almost
// every frame QSP exports needs this, and it is applied here rather than left
// for an administrator to remember.
func Encode(data hbp.Data, networkID hbp.RepeaterID, passphrase []byte) ([]byte, error) {
	data.RepeaterID = networkID
	data.Timeslot = hbp.Timeslot1

	frame := data.Marshal()

	// MMDVMHost appends trailing bytes that OpenBridge has no room for: the
	// datagram is exactly 53 + 20. Trim rather than refuse, because the
	// trailing bytes are not protocol content and refusing would make QSP
	// unable to forward frames it received perfectly well.
	if len(frame) > FrameSize {
		frame = frame[:FrameSize]
	}
	if len(frame) < FrameSize {
		return nil, fmt.Errorf("openbridge: encoded frame is %d bytes, want %d", len(frame), FrameSize)
	}

	return Sign(frame, passphrase)
}
