package peering

import (
	"encoding/base64"
	"errors"
	"fmt"
	"hash/crc32"
	"strings"
	"testing"
	"time"
)

// anOffer is a link invitation as the offering side would build one.
func anOffer(t *testing.T) (LinkInvitation, string) {
	t.Helper()

	password, err := NewPassphrase()
	if err != nil {
		t.Fatalf("generating a password: %v", err)
	}
	return LinkInvitation{
		Network:     "QSP Test Server",
		Callsign:    "K9MLS",
		Address:     "qsp.example.com:62031",
		RepeaterID:  3132913,
		Fingerprint: FingerprintOf(password),
		Issued:      time.Date(2026, 9, 8, 19, 0, 0, 0, time.UTC),
	}, password
}

func TestALinkInvitationSurvivesTheRoundTrip(t *testing.T) {
	offer, _ := anOffer(t)

	token, err := EncodeLink(offer)
	if err != nil {
		t.Fatalf("EncodeLink: %v", err)
	}
	got, err := DecodeLink(token)
	if err != nil {
		t.Fatalf("DecodeLink: %v", err)
	}

	if got != offer {
		t.Errorf("the invitation changed in transit:\n sent %+v\n got  %+v", offer, got)
	}
}

// The two token formats must not be readable as each other. An OpenBridge
// peering and a QSP link produce entirely different configuration, and a token
// read by the wrong half of the console would write the wrong one.
func TestTheTwoFormatsRefuseEachOther(t *testing.T) {
	offer, _ := anOffer(t)
	linkToken, err := EncodeLink(offer)
	if err != nil {
		t.Fatalf("EncodeLink: %v", err)
	}

	obToken, err := Encode(Invitation{
		Network:     "QSP Test Server",
		Callsign:    "K9MLS",
		Address:     "qsp.example.com:62045",
		NetworkID:   3132910,
		Fingerprint: offer.Fingerprint,
		Issued:      offer.Issued,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	if _, err := Decode(linkToken); !errors.Is(err, ErrVersion) {
		t.Errorf("Decode read a link token as an OpenBridge peering: %v", err)
	}
	if _, err := DecodeLink(obToken); !errors.Is(err, ErrOpenBridgeToken) {
		t.Errorf("DecodeLink read an OpenBridge token as a link: %v", err)
	}
}

func TestKindOfNamesWhichFormAnOperatorNeeds(t *testing.T) {
	offer, _ := anOffer(t)
	linkToken, err := EncodeLink(offer)
	if err != nil {
		t.Fatalf("EncodeLink: %v", err)
	}
	obToken, err := Encode(Invitation{
		Network: "N", Callsign: "K9MLS", Address: "a.example.com:62045",
		NetworkID: 1, Fingerprint: offer.Fingerprint, Issued: offer.Issued,
	})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for _, tc := range []struct {
		name  string
		token string
		want  Kind
		err   error
	}{
		{"a link", linkToken, KindLink, nil},
		{"an OpenBridge peering", obToken, KindOpenBridge, nil},
		{"a format we do not have", "QSP-PEER-9.abc.00000000", "", ErrVersion},
		{"something else entirely", "hello", "", ErrNotAnInvitation},
		{"surrounded by whitespace", "\n  " + linkToken + "  \n", KindLink, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := KindOf(tc.token)
			if !errors.Is(err, tc.err) {
				t.Fatalf("error is %v, want %v", err, tc.err)
			}
			if got != tc.want {
				t.Errorf("kind is %q, want %q", got, tc.want)
			}
		})
	}
}

// A mail client wraps a long line and an operator pastes half of it. That has
// to say so at the moment of pasting.
func TestATruncatedLinkTokenSaysSo(t *testing.T) {
	offer, _ := anOffer(t)
	token, err := EncodeLink(offer)
	if err != nil {
		t.Fatalf("EncodeLink: %v", err)
	}

	for _, tc := range []struct {
		name  string
		token string
	}{
		{"cut short", token[:len(token)-12]},
		{"checksum removed", token[:strings.LastIndex(token, ".")]},
		{"a character changed in the body", token[:len(LinkPrefix)+4] + "x" + token[len(LinkPrefix)+5:]},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeLink(tc.token); !errors.Is(err, ErrTruncated) {
				t.Errorf("error is %v, want ErrTruncated", err)
			}
		})
	}
}

func TestALinkInvitationIsRefusedWhenItIsIncomplete(t *testing.T) {
	base, _ := anOffer(t)

	for _, tc := range []struct {
		name   string
		change func(*LinkInvitation)
		want   error
	}{
		{"no address", func(i *LinkInvitation) { i.Address = "" }, ErrNoAddress},
		{"a scheme on the address", func(i *LinkInvitation) {
			i.Address = "https://qsp.example.com:62031"
		}, ErrSchemeInAddress},
		{"the address it listens on", func(i *LinkInvitation) { i.Address = "0.0.0.0:62031" }, ErrBindAddress},
		{"every interface, spelled the other way", func(i *LinkInvitation) {
			i.Address = "[::]:62031"
		}, ErrBindAddress},
		{"no callsign", func(i *LinkInvitation) { i.Callsign = " " }, ErrNoCallsign},
		{"no repeater ID", func(i *LinkInvitation) { i.RepeaterID = 0 }, ErrNoRepeaterID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := base
			tc.change(&inv)
			if _, err := EncodeLink(inv); !errors.Is(err, tc.want) {
				t.Errorf("error is %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAcceptingALinkChecksThePasswordAndTheClock(t *testing.T) {
	offer, password := anOffer(t)
	fresh := offer.Issued.Add(time.Hour)

	if err := offer.Accept(password, fresh); err != nil {
		t.Fatalf("the right password was refused: %v", err)
	}

	other, err := NewPassphrase()
	if err != nil {
		t.Fatalf("generating a password: %v", err)
	}
	if err := offer.Accept(other, fresh); !errors.Is(err, ErrFingerprint) {
		t.Errorf("a wrong password gave %v, want ErrFingerprint", err)
	}

	if err := offer.Accept("short", fresh); !errors.Is(err, ErrWeak) {
		t.Errorf("a short password gave %v, want ErrWeak", err)
	}

	stale := offer.Issued.Add(Lifetime + time.Second)
	if err := offer.Accept(password, stale); !errors.Is(err, ErrExpired) {
		t.Errorf("an expired invitation gave %v, want ErrExpired", err)
	}

	// Expiry is reported ahead of the password, because "ask for a new one"
	// and "find the right secret" send an operator to different places.
	if err := offer.Accept(other, stale); !errors.Is(err, ErrExpired) {
		t.Errorf("expired and wrong gave %v, want ErrExpired", err)
	}
}

// A token carrying a field this build does not know is refused rather than
// half-read. It is also why the link format has its own prefix: an older QSP
// meeting one of these reports a version it does not have, which is true,
// instead of malformed JSON, which is not.
func TestALinkTokenWithAnUnknownFieldIsRefused(t *testing.T) {
	offer, _ := anOffer(t)
	token, err := EncodeLink(offer)
	if err != nil {
		t.Fatalf("EncodeLink: %v", err)
	}

	body, sum, ok := strings.Cut(strings.TrimPrefix(token, LinkPrefix), ".")
	if !ok {
		t.Fatalf("the token has no checksum: %q", token)
	}
	_ = sum

	// Rebuild with an extra field, checksum and all, so this tests the JSON
	// rule rather than the checksum.
	raw := decodeForTest(t, body)
	raw = strings.TrimSuffix(raw, "}") + `,"hop_count":3}`
	rebuilt := encodeForTest(t, raw)

	if _, err := DecodeLink(rebuilt); err == nil {
		t.Fatal("a token with an unknown field was accepted")
	} else if !strings.Contains(err.Error(), "hop_count") {
		t.Errorf("the error does not name the field it refused: %v", err)
	}
}

// decodeForTest and encodeForTest take a token body apart and put it back
// together with a valid checksum, so a test about the JSON rule is not
// answered by the checksum rule instead.
func decodeForTest(t *testing.T, payload string) string {
	t.Helper()

	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decoding the token body: %v", err)
	}
	return string(body)
}

func encodeForTest(t *testing.T, body string) string {
	t.Helper()

	payload := base64.RawURLEncoding.EncodeToString([]byte(body))
	return fmt.Sprintf("%s%s.%08x", LinkPrefix, payload, crc32.ChecksumIEEE([]byte(payload)))
}
