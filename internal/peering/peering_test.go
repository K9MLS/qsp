package peering

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func offer() Invitation {
	return Invitation{
		Network:     "BCARA",
		Callsign:    "K9MLS",
		Address:     "qsp.hopto.me:62045",
		NetworkID:   3132910,
		Latitude:    "33.2148",
		Longitude:   "-97.1331",
		Export:      []Talkgroup{{Talkgroup: 2, Timeslot: 2}},
		Import:      []Talkgroup{{Talkgroup: 2, Timeslot: 2}},
		Fingerprint: FingerprintOf("a-passphrase-long-enough-to-pass"),
		Issued:      time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
	}
}

// TestAnInvitationSurvivesBeingEmailed. It goes into a mail client, through
// whatever that does to whitespace, and out of another one.
func TestAnInvitationSurvivesBeingEmailed(t *testing.T) {
	token, err := Encode(offer())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.ContainsAny(token, " \t\n") {
		t.Error("the token contains whitespace, so a mail client may wrap it")
	}

	// Leading and trailing whitespace is what a paste actually looks like.
	got, err := Decode("  \n" + token + "\n  ")
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Callsign != "K9MLS" || got.NetworkID != 3132910 {
		t.Errorf("round trip lost fields: %+v", got)
	}
	if len(got.Export) != 1 || got.Export[0].Talkgroup != 2 {
		t.Errorf("round trip lost the proposed talkgroups: %+v", got.Export)
	}
}

// TestTheInvitationNeverCarriesThePassphrase.
//
// **This is the property the whole split exists for.** The token goes by email,
// which is exactly what the join page refuses to send a peer password over. If
// the secret is in here, the two channels were one channel.
func TestTheInvitationNeverCarriesThePassphrase(t *testing.T) {
	const secret = "a-passphrase-long-enough-to-pass"

	inv := offer()
	inv.Fingerprint = FingerprintOf(secret)

	token, err := Encode(inv)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if strings.Contains(token, secret) {
		t.Fatal("the passphrase is in the token verbatim")
	}

	// And not merely absent from the text — absent from the decoded structure,
	// so a future field cannot smuggle it back.
	got, err := Decode(token)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if strings.Contains(got.Fingerprint, secret) {
		t.Error("the fingerprint contains the passphrase")
	}
	if len(got.Fingerprint) >= len(secret) {
		t.Errorf("the fingerprint is %d characters, long enough to be the secret itself",
			len(got.Fingerprint))
	}
}

// TestATruncatedPasteSaysSo.
//
// Half a token in an email is the ordinary accident, and without a checksum it
// decodes into nonsense or a plausible-looking invitation with a wrong address.
// Either way an administrator debugs a link instead of a paste.
func TestATruncatedPasteSaysSo(t *testing.T) {
	token, err := Encode(offer())
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for _, cut := range []int{len(token) - 1, len(token) - 12, len(token) / 2} {
		if _, err := Decode(token[:cut]); !errors.Is(err, ErrTruncated) &&
			!errors.Is(err, ErrNotAnInvitation) {
			t.Errorf("a token cut to %d characters gave %v, not a truncation", cut, err)
		}
	}

	// A single altered character inside the payload is the same class of fault.
	broken := []byte(token)
	at := len(Prefix) + 4
	if broken[at] == 'A' {
		broken[at] = 'B'
	} else {
		broken[at] = 'A'
	}
	if _, err := Decode(string(broken)); err == nil {
		t.Error("a corrupted token was accepted")
	}
}

// TestAWrongPassphraseFailsAtThePaste, rather than as silence on a link that
// reports itself configured. That silence is indistinguishable from a firewall,
// a NAT rebind, or a far end that has not been started yet.
func TestAWrongPassphraseFailsAtThePaste(t *testing.T) {
	const right = "a-passphrase-long-enough-to-pass"
	now := time.Date(2026, 8, 30, 13, 0, 0, 0, time.UTC)

	inv := offer()
	if err := inv.Accept(right, now); err != nil {
		t.Fatalf("the right passphrase was refused: %v", err)
	}
	if err := inv.Accept("another-passphrase-long-enough!!", now); !errors.Is(err, ErrFingerprint) {
		t.Errorf("a wrong passphrase gave %v", err)
	}
}

// TestAShortPassphraseIsRefused. The passphrase is the entire security
// boundary: anyone holding it can put audio on the network as a peer network.
func TestAShortPassphraseIsRefused(t *testing.T) {
	now := time.Date(2026, 8, 30, 13, 0, 0, 0, time.UTC)
	inv := offer()
	inv.Fingerprint = FingerprintOf("hunter2")

	// Even though it matches, which is the point: matching is not enough.
	if err := inv.Accept("hunter2", now); !errors.Is(err, ErrWeak) {
		t.Errorf("a seven-character passphrase gave %v", err)
	}
}

// TestAnInvitationExpires. It is a standing offer to send audio to a network,
// and one left in a mail archive is as good the day somebody leaves the club as
// the day it was written.
func TestAnInvitationExpires(t *testing.T) {
	const secret = "a-passphrase-long-enough-to-pass"
	inv := offer()

	fresh := inv.Issued.Add(Lifetime - time.Hour)
	if err := inv.Accept(secret, fresh); err != nil {
		t.Errorf("a valid invitation was refused: %v", err)
	}

	stale := inv.Issued.Add(Lifetime + time.Hour)
	if err := inv.Accept(secret, stale); !errors.Is(err, ErrExpired) {
		t.Errorf("an expired invitation gave %v", err)
	}

	// Expiry is reported before the passphrase, so an administrator is told to
	// ask for a new invitation rather than sent hunting for a secret that would
	// not have worked anyway.
	if err := inv.Accept("wrong-but-also-long-enough-yes!!", stale); !errors.Is(err, ErrExpired) {
		t.Errorf("an expired invitation with a wrong passphrase reported %v", err)
	}
}

// TestGeneratedPassphrasesAreNotGuessable, and are not each other.
func TestGeneratedPassphrasesAreNotGuessable(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 64; i++ {
		p, err := NewPassphrase()
		if err != nil {
			t.Fatalf("NewPassphrase: %v", err)
		}
		if len(p) < MinPassphraseBytes {
			t.Fatalf("generated a %d character passphrase, below the floor", len(p))
		}
		if seen[p] {
			t.Fatal("NewPassphrase repeated itself")
		}
		seen[p] = true
	}
}

// TestTheReciprocalKeepsTheAgreedSecret.
//
// OpenBridge authenticates every datagram against one shared passphrase. A side
// that generated its own when answering would produce a link that carries
// traffic one way and reports itself healthy from both ends.
func TestTheReciprocalKeepsTheAgreedSecret(t *testing.T) {
	accepted := offer()

	mine := Invitation{
		Network:   "Wisconsin",
		Callsign:  "KB9TYC",
		Address:   "cameron.example:62045",
		NetworkID: 3155413,
		Export:    []Talkgroup{{Talkgroup: 2, Timeslot: 2}},
	}
	back, err := Reciprocal(accepted, mine)
	if err != nil {
		t.Fatalf("Reciprocal: %v", err)
	}
	if back.Fingerprint != accepted.Fingerprint {
		t.Error("the reply proposes a different passphrase from the one agreed")
	}
	if back.Callsign != "KB9TYC" || back.Address != "cameron.example:62045" {
		t.Errorf("the reply lost the accepting side's own details: %+v", back)
	}
	if back.NetworkID == accepted.NetworkID {
		t.Error("both sides announce one network ID")
	}
}

// TestAnIncompleteInvitationIsRefusedBeforeItIsSent, because the failure it
// causes is at the far end, in somebody else's console, hours later.
func TestAnIncompleteInvitationIsRefusedBeforeItIsSent(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Invitation)
		want error
	}{
		{"no address", func(i *Invitation) { i.Address = " " }, ErrNoAddress},
		{"no callsign", func(i *Invitation) { i.Callsign = "" }, ErrNoCallsign},
		{"no network id", func(i *Invitation) { i.NetworkID = 0 }, ErrNoNetworkID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inv := offer()
			c.edit(&inv)
			if _, err := Encode(inv); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

// TestSomethingElseEntirelyIsNotMistakenForAnInvitation, and a newer format is
// distinguished from rubbish, because the two want different advice.
func TestSomethingElseEntirelyIsNotMistakenForAnInvitation(t *testing.T) {
	for _, junk := range []string{"", "hello", "https://example.com", "QSP-PEER"} {
		if _, err := Decode(junk); !errors.Is(err, ErrNotAnInvitation) {
			t.Errorf("Decode(%q) gave %v", junk, err)
		}
	}
	if _, err := Decode("QSP-PEER-9.abc.00000000"); !errors.Is(err, ErrVersion) {
		t.Errorf("a newer format gave %v, which does not tell an operator to upgrade", err)
	}
}

// TestASchemeInTheAddressIsRefused is the mistake an operator makes because
// every other address they type all day has one on the front.
//
// The form accepted "https://qsp.hopto.me:62045" without a word. A peering is
// UDP to a host and a port — there is no URL, no TLS, nothing to speak HTTP to.
// Left alone it produces a link that resolves nothing and a far end waiting in
// silence, which is the hardest kind of fault to find.
func TestASchemeInTheAddressIsRefused(t *testing.T) {
	for _, address := range []string{
		"https://qsp.example.com:62045",
		"http://qsp.example.com:62045",
		"udp://qsp.example.com:62045",
	} {
		inv := Invitation{
			Address:   address,
			Callsign:  "K9MLS",
			NetworkID: 3132910,
		}
		if err := inv.Validate(); !errors.Is(err, ErrSchemeInAddress) {
			t.Errorf("%q was accepted (%v); it is not a host and a port", address, err)
		}
	}

	// And the shape that is correct still passes.
	ok := Invitation{
		Address:   "qsp.example.com:62045",
		Callsign:  "K9MLS",
		NetworkID: 3132910,
	}
	if err := ok.Validate(); err != nil {
		t.Errorf("a valid invitation was refused: %v", err)
	}
}

// TestABindAddressIsRefused is what the accept form put into a real invitation.
//
// "We listen on" is correctly 0.0.0.0:62045 — every interface on this machine.
// The reply copied it straight into the address the far end should send to,
// where it means every interface on *their* machine and reaches nothing.
func TestABindAddressIsRefused(t *testing.T) {
	for _, address := range []string{"0.0.0.0:62045", "[::]:62045"} {
		inv := Invitation{Address: address, Callsign: "K9MLS", NetworkID: 3132910}
		if err := inv.Validate(); !errors.Is(err, ErrBindAddress) {
			t.Errorf("%q was accepted as somewhere to send to (%v)", address, err)
		}
	}
	// A real address on the same port is fine, and so is a name.
	for _, address := range []string{"192.168.1.27:62045", "qsp.example.com:62045"} {
		inv := Invitation{Address: address, Callsign: "K9MLS", NetworkID: 3132910}
		if err := inv.Validate(); err != nil {
			t.Errorf("%q was refused: %v", address, err)
		}
	}
}
