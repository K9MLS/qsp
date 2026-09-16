package hbp

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestAnIdentitySurvivesTheRoundTrip(t *testing.T) {
	want := Identity{
		RepeaterID:  3132912,
		Network:     "KD9EJA-01",
		Callsign:    "KD9EJA",
		Software:    "QSP 0.1.139 (abc1234)",
		Description: "Wisconsin",
	}

	got, err := Parse(want.Marshal())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got != Message(want) {
		t.Errorf("the identity changed in transit:\n sent %+v\n got  %+v", want, got)
	}
	if got.Kind() != KindIdentity {
		t.Errorf("kind is %q, want %q", got.Kind(), KindIdentity)
	}
}

// **A server older than the one it is linked to must keep the link.** A new
// field in a later QSP that broke every link to an older one is exactly the
// failure this shape exists to avoid, so unknown fields are ignored — the
// opposite of the rule for an invitation token, which is read once by a human.
func TestAnIdentityIgnoresFieldsThisBuildDoesNotKnow(t *testing.T) {
	base := Identity{RepeaterID: 3132912, Network: "KD9EJA-01", Callsign: "KD9EJA"}
	wire := base.Marshal()

	// Splice in a field from some later QSP. It has to be a field this build
	// genuinely does not know: an earlier version of this test used server_id,
	// which stopped being unknown the moment ADR-0053 was built, and the test
	// caught it. A key fingerprint is the next field this packet is expected to
	// grow.
	body := wire[8:]
	extended := append([]byte{}, wire[:8]...)
	extended = append(extended, bytes.Replace(body,
		[]byte(`{"network"`), []byte(`{"key_fingerprint":"SHA256:0f1e2d3c","network"`), 1)...)

	got, err := Parse(extended)
	if err != nil {
		t.Fatalf("an identity with a later field was refused: %v", err)
	}
	if got != Message(base) {
		t.Errorf("the fields this build knows changed: %+v", got)
	}
}

// An identity that says nothing is still an identity: it names the link that
// answered, which is the minimum this message is for.
func TestAnEmptyIdentityStillNamesItsLink(t *testing.T) {
	wire := Identity{RepeaterID: 3132912}.Marshal()

	got, err := Parse(wire)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id := got.(Identity).RepeaterID; id != 3132912 {
		t.Errorf("the repeater ID is %d, want 3132912", id)
	}
}

// Input is hostile. None of these may panic, and each must be refused rather
// than half-read.
func TestAMalformedIdentityIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		wire []byte
		want error
	}{
		{"the tag alone", []byte("QSPI"), ErrShort},
		{"a truncated repeater ID", []byte("QSPI\x00\x2f"), ErrShort},
		{"a payload that is not JSON", append([]byte("QSPI\x00\x2f\xcb\x30"), []byte("not json")...), ErrFieldFormat},
		{
			"a payload over the limit",
			append([]byte("QSPI\x00\x2f\xcb\x30"),
				append([]byte(`{"network":"`), append(bytes.Repeat([]byte("x"), identityMaxPayload), []byte(`"}`)...)...)...),
			ErrFieldFormat,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.wire)
			if !errors.Is(err, tc.want) {
				t.Errorf("error is %v, want %v", err, tc.want)
			}
		})
	}
}

// The tag must not collide with anything HBP already carries, or a real message
// would be parsed as an identity or the other way round.
func TestTheIdentityTagIsItsOwn(t *testing.T) {
	for _, tag := range []string{"RPTL", "RPTK", "RPTC", "RPTACK", "RPTPING",
		"MSTPONG", "MSTNAK", "MSTACK", "MSTCL", "RPTCL", "DMRD", "DMRC", "DMRP"} {
		if strings.HasPrefix(tag, identityTag) || strings.HasPrefix(identityTag, tag) {
			t.Errorf("%q and %q share a prefix, so one parses as the other", tag, identityTag)
		}
	}
}
