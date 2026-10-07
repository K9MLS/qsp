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

// identityWire is an identity message carrying exactly this JSON.
func identityWire(body string) []byte {
	return append(Identity{RepeaterID: 3132912}.Marshal()[:8], body...)
}

// Where a server says it is, as it arrives. A position is taken only when it
// is whole and on the globe; anything else is a server with no position, and
// what else it said still stands.
//
// Break it: take a latitude without a longitude, drop the range check, or
// refuse the whole identity for a bad position, and a row fails.
func TestAnIdentitysPositionAsItArrives(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		located bool
		lat     float64
		lon     float64
	}{
		{"a position", `{"network":"N","latitude":33.2148,"longitude":-97.1331}`, true, 33.2148, -97.1331},
		{"on the equator", `{"network":"N","latitude":0,"longitude":-78.5}`, true, 0, -78.5},
		{"on the prime meridian", `{"network":"N","latitude":51.48,"longitude":0}`, true, 51.48, 0},
		{"at the extremes", `{"network":"N","latitude":-90,"longitude":180}`, true, -90, 180},
		{"none, from an older server", `{"network":"N"}`, false, 0, 0},
		{"a latitude and no longitude", `{"network":"N","latitude":33.2}`, false, 0, 0},
		{"a longitude and no latitude", `{"network":"N","longitude":-97.1}`, false, 0, 0},
		{"nought and nought, which is nobody's position", `{"network":"N","latitude":0,"longitude":0}`, false, 0, 0},
		{"a latitude off the globe", `{"network":"N","latitude":91,"longitude":10}`, false, 0, 0},
		{"a longitude off the globe", `{"network":"N","latitude":10,"longitude":-180.5}`, false, 0, 0},
		{"null for both", `{"network":"N","latitude":null,"longitude":null}`, false, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Parse(identityWire(tc.body))
			if err != nil {
				t.Fatalf("the identity was refused: %v", err)
			}
			got := m.(Identity)
			if got.Network != "N" {
				t.Errorf("the name was lost with the position: %+v", got)
			}
			if got.Located != tc.located || got.Latitude != tc.lat || got.Longitude != tc.lon {
				t.Errorf("located %v at %v, %v; want %v at %v, %v",
					got.Located, got.Latitude, got.Longitude, tc.located, tc.lat, tc.lon)
			}
		})
	}
}

// What a server sends about where it is. Nothing at all unless it has a
// position, so a server given none does not announce the middle of the ocean.
//
// Break it: write the two numbers whether or not Located is set.
func TestAnIdentityAnnouncesAPositionOnlyWhenItHasOne(t *testing.T) {
	tests := []struct {
		name string
		id   Identity
		sent bool
	}{
		{"located", Identity{Network: "N", Location: "Denton, TX", Latitude: 33.2148, Longitude: -97.1331, Located: true}, true},
		{"numbers set and not located", Identity{Network: "N", Latitude: 33.2148, Longitude: -97.1331}, false},
		{"located at nought and nought", Identity{Network: "N", Located: true}, false},
		{"located off the globe", Identity{Network: "N", Latitude: 120, Longitude: 10, Located: true}, false},
		{"nothing", Identity{Network: "N"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := string(tc.id.Marshal()[8:])
			if has := strings.Contains(body, `"latitude"`) && strings.Contains(body, `"longitude"`); has != tc.sent {
				t.Errorf("sent %s", body)
			}
			if !tc.sent && (strings.Contains(body, "latitude") || strings.Contains(body, "longitude")) {
				t.Errorf("half a position was sent: %s", body)
			}
			back, err := Parse(tc.id.Marshal())
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := back.(Identity); got.Located != tc.sent {
				t.Errorf("read back as located=%v, want %v", got.Located, tc.sent)
			}
		})
	}
}
