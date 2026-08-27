package openbridge_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/openbridge"
)

const passphrase = "shared-with-the-far-end"

// sampleFrame is a DMRD frame of exactly the 53 bytes OpenBridge carries.
func sampleFrame(t *testing.T) []byte {
	t.Helper()

	data := hbp.Data{
		Sequence:   7,
		SourceID:   3132910,
		TargetID:   3148,
		RepeaterID: 3132910,
		Timeslot:   hbp.Timeslot2,
		CallType:   hbp.CallGroup,
		FrameType:  hbp.FrameTypeVoice,
		StreamID:   0x6CD7506B,
	}
	for i := range data.Payload {
		data.Payload[i] = byte(i)
	}

	frame := data.Marshal()
	if len(frame) != openbridge.FrameSize {
		t.Fatalf("a DMRD frame with no trailing bytes is %d, want %d",
			len(frame), openbridge.FrameSize)
	}
	return frame
}

func TestSignedFrameIsTheRightLength(t *testing.T) {
	packet, err := openbridge.Sign(sampleFrame(t), []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if len(packet) != openbridge.PacketSize {
		t.Errorf("signed packet is %d bytes, want %d", len(packet), openbridge.PacketSize)
	}
}

// TestSignatureMatchesAnIndependentHMAC guards against this package agreeing
// with itself and nobody else.
//
// A round-trip test would pass if Sign and Verify shared the same mistake — the
// wrong hash, a derived key, a truncated output. The far end is not this
// package, so the signature is computed here from the primitives instead.
func TestSignatureMatchesAnIndependentHMAC(t *testing.T) {
	frame := sampleFrame(t)

	packet, err := openbridge.Sign(frame, []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	mac := hmac.New(sha1.New, []byte(passphrase))
	mac.Write(frame)
	want := mac.Sum(nil)

	got := packet[openbridge.FrameSize:]
	if !bytes.Equal(got, want) {
		t.Errorf("signature is\n  %x\nwant\n  %x", got, want)
	}
	if len(want) != openbridge.SignatureSize {
		t.Errorf("HMAC-SHA1 is %d bytes; SignatureSize says %d",
			len(want), openbridge.SignatureSize)
	}
}

func TestVerifyAcceptsWhatSignProduced(t *testing.T) {
	frame := sampleFrame(t)
	packet, err := openbridge.Sign(frame, []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	got, err := openbridge.Verify(packet, []byte(passphrase))
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Error("the verified frame differs from the one signed")
	}
}

// TestVerifyRejectsATamperedFrame. Every byte of the frame is covered by the
// signature, so altering any of them must fail — including the ones a
// mischievous sender would most want to change.
func TestVerifyRejectsATamperedFrame(t *testing.T) {
	frame := sampleFrame(t)
	packet, err := openbridge.Sign(frame, []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	for _, tc := range []struct {
		name  string
		index int
	}{
		{"the magic", 1},
		{"the sequence number", 4},
		{"the source radio ID", 6},
		{"the destination talkgroup", 9},
		{"the sending server's ID", 12},
		{"the flags, which carry the timeslot", 15},
		{"the stream ID", 17},
		{"the voice payload", 30},
		{"the last byte of the frame", openbridge.FrameSize - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tampered := bytes.Clone(packet)
			tampered[tc.index] ^= 0xFF

			if _, err := openbridge.Verify(tampered, []byte(passphrase)); !errors.Is(err, openbridge.ErrBadSignature) {
				t.Errorf("altering %s gave %v, want ErrBadSignature", tc.name, err)
			}
		})
	}
}

func TestVerifyRejectsATamperedSignature(t *testing.T) {
	packet, err := openbridge.Sign(sampleFrame(t), []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	packet[openbridge.PacketSize-1] ^= 0x01

	if _, err := openbridge.Verify(packet, []byte(passphrase)); !errors.Is(err, openbridge.ErrBadSignature) {
		t.Errorf("got %v, want ErrBadSignature", err)
	}
}

// TestVerifyRejectsTheWrongPassphrase is the case an operator actually hits:
// the two ends disagree about the shared secret, everything else is correct,
// and nothing works.
func TestVerifyRejectsTheWrongPassphrase(t *testing.T) {
	packet, err := openbridge.Sign(sampleFrame(t), []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := openbridge.Verify(packet, []byte("not-the-agreed-passphrase")); !errors.Is(err, openbridge.ErrBadSignature) {
		t.Errorf("got %v, want ErrBadSignature", err)
	}
}

// TestEmptyPassphraseIsRefused.
//
// An empty key produces a signature anyone else with an empty key can forge,
// which is worse than no authentication because it looks like authentication.
func TestEmptyPassphraseIsRefused(t *testing.T) {
	if _, err := openbridge.Sign(sampleFrame(t), nil); !errors.Is(err, openbridge.ErrNoPassphrase) {
		t.Errorf("Sign with no passphrase gave %v, want ErrNoPassphrase", err)
	}
	if _, err := openbridge.Verify(make([]byte, openbridge.PacketSize), []byte{}); !errors.Is(err, openbridge.ErrNoPassphrase) {
		t.Errorf("Verify with no passphrase gave %v, want ErrNoPassphrase", err)
	}
}

func TestVerifyRejectsWrongLengths(t *testing.T) {
	packet, err := openbridge.Sign(sampleFrame(t), []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	for _, tc := range []struct {
		name   string
		packet []byte
	}{
		{"empty", nil},
		{"a bare DMRD frame with no signature", packet[:openbridge.FrameSize]},
		{"one byte short", packet[:openbridge.PacketSize-1]},
		{"one byte long", append(bytes.Clone(packet), 0x00)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := openbridge.Verify(tc.packet, []byte(passphrase)); !errors.Is(err, openbridge.ErrShortPacket) {
				t.Errorf("got %v, want ErrShortPacket", err)
			}
		})
	}
}

// TestEncodeForcesTimeslot1 covers the rule that catches every club.
//
// Proper OpenBridge passes all traffic on TS1 with the slot bit clear. Club
// talkgroups are conventionally on TS2, so nearly every exported frame needs
// this, and an administrator should not have to know that.
func TestEncodeForcesTimeslot1(t *testing.T) {
	data := hbp.Data{
		SourceID:   3132910,
		TargetID:   3148,
		RepeaterID: 3132910,
		Timeslot:   hbp.Timeslot2,
		CallType:   hbp.CallGroup,
		FrameType:  hbp.FrameTypeVoice,
		StreamID:   1,
	}

	packet, err := openbridge.Encode(data, 3132910, []byte(passphrase))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	got, err := openbridge.Parse(packet, []byte(passphrase))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got.Timeslot != hbp.Timeslot1 {
		t.Errorf("exported frame is on %s, want TS1", got.Timeslot)
	}
}

// TestEncodeStampsTheNetworkID.
//
// On OpenBridge the repeater ID field identifies the sending server, not the
// repeater a transmission came from. The far end uses it to attribute the frame
// to a link; sending the originating repeater's ID makes it unattributable.
func TestEncodeStampsTheNetworkID(t *testing.T) {
	const networkID hbp.RepeaterID = 3129100

	data := hbp.Data{
		SourceID:   3132910,
		TargetID:   3148,
		RepeaterID: 3132910, // the hotspot the audio came from
		Timeslot:   hbp.Timeslot2,
		CallType:   hbp.CallGroup,
		FrameType:  hbp.FrameTypeVoice,
		StreamID:   1,
	}

	packet, err := openbridge.Encode(data, networkID, []byte(passphrase))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := openbridge.Parse(packet, []byte(passphrase))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got.RepeaterID != networkID {
		t.Errorf("exported frame carries repeater ID %d, want the network ID %d",
			got.RepeaterID, networkID)
	}
	if got.SourceID != 3132910 {
		t.Errorf("the originating radio's ID became %d; it must survive the link", got.SourceID)
	}
}

// TestEncodeTrimsTrailingBytes.
//
// MMDVMHost appends two bytes after the burst, and QSP preserves them so frames
// round-trip exactly. OpenBridge has no room: the datagram is exactly 53 + 20.
// A frame received perfectly well must still be forwardable.
func TestEncodeTrimsTrailingBytes(t *testing.T) {
	data := hbp.Data{
		SourceID: 1, TargetID: 9, RepeaterID: 1,
		Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoice, StreamID: 1,
		Trailing: []byte{0xAA, 0xBB},
	}

	packet, err := openbridge.Encode(data, 3129100, []byte(passphrase))
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if len(packet) != openbridge.PacketSize {
		t.Fatalf("packet is %d bytes, want %d", len(packet), openbridge.PacketSize)
	}
	if _, err := openbridge.Parse(packet, []byte(passphrase)); err != nil {
		t.Errorf("a trimmed frame did not parse: %v", err)
	}
}

// TestParseRejectsNonData. OpenBridge carries DMRD and nothing else.
func TestParseRejectsNonData(t *testing.T) {
	frame := make([]byte, openbridge.FrameSize)
	copy(frame, "RPTL")
	binary.BigEndian.PutUint32(frame[4:8], 3132910)

	packet, err := openbridge.Sign(frame, []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	_, err = openbridge.Parse(packet, []byte(passphrase))
	if err == nil {
		t.Fatal("a non-DMRD frame was accepted")
	}
}

// TestParsedFrameMatchesWhatWasSigned checks the DMRD body survives the
// signing layer unchanged, field by field.
func TestParsedFrameMatchesWhatWasSigned(t *testing.T) {
	want := hbp.Data{
		Sequence: 42, SourceID: 3132910, TargetID: 3148,
		RepeaterID: 3129100, Timeslot: hbp.Timeslot1,
		CallType: hbp.CallGroup, FrameType: hbp.FrameTypeVoice,
		StreamID: 0xDEADBEEF,
	}
	for i := range want.Payload {
		want.Payload[i] = byte(255 - i)
	}

	packet, err := openbridge.Sign(want.Marshal(), []byte(passphrase))
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	got, err := openbridge.Parse(packet, []byte(passphrase))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got.Sequence != want.Sequence || got.SourceID != want.SourceID ||
		got.TargetID != want.TargetID || got.RepeaterID != want.RepeaterID ||
		got.StreamID != want.StreamID || got.Payload != want.Payload {
		t.Errorf("the frame changed crossing the signing layer:\n got %+v\nwant %+v", got, want)
	}
}
