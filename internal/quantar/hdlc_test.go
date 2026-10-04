package quantar

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

// The station's introduction is the one captured on 2026-10-04.
var stationIntroduction = []byte{0xFD, 0xBF, 0x01, 0x03, 0xC2, 0x00, 0x00, 0x00, 0x00, 0xFF}

// Break it: answer with the request's own control byte, answer a keepalive
// that did not ask, or answer an introduction without knowing the link's
// state, and a row here fails.
func TestWhatEachFrameIsOwedWhateverTheState(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
		kind Kind
	}{
		{"the request as captured", []byte{0xFD, 0x3F}, []byte{0xFD, 0x73}, KindLinkRequest},
		{"a request without the poll bit", []byte{0xFD, 0x2F}, []byte{0xFD, 0x63}, KindLinkRequest},
		{"the station's address is carried back", []byte{0x07, 0x3F}, []byte{0x07, 0x73}, KindLinkRequest},
		{"the introduction as captured", stationIntroduction, nil, KindIntroduction},
		{"an introduction cut short", stationIntroduction[:9], nil, KindUnknown},
		{"an introduction of another message type",
			[]byte{0xFD, 0xBF, 0x02, 0x03, 0xC2, 0, 0, 0, 0, 0xFF}, nil, KindUnknown},
		{"a keepalive that asks nothing", []byte{0xFD, 0x01}, nil, KindReceiveReady},
		{"a keepalive that demands an answer", []byte{0xFD, 0x11}, []byte{0xFD, 0x11}, KindReceiveReady},
		{"a keepalive carrying a sequence number", []byte{0xFD, 0x41}, nil, KindReceiveReady},
		{"an acceptance of our request", []byte{0xFD, 0x73}, nil, KindAcceptance},
		{"an acceptance without the final bit", []byte{0xFD, 0x63}, nil, KindAcceptance},
		{"voice", []byte{0x07, 0x03, 0x60, 0x02, 0x04, 0x0C}, nil, KindUnknown},
		{"a request with bytes after it", []byte{0xFD, 0x3F, 0x00}, nil, KindUnknown},
		{"one byte", []byte{0xFD}, nil, KindUnknown},
		{"nothing", nil, nil, KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := bytes.Clone(tc.in)
			got, kind := Answer(tc.in)
			if !bytes.Equal(got, tc.want) || kind != tc.kind {
				t.Errorf("got % x (%s), want % x (%s)", got, kind, tc.want, tc.kind)
			}
			if !bytes.Equal(tc.in, in) {
				t.Errorf("the station's frame was changed to % x", tc.in)
			}
		})
	}
}

// What QSP originates, in each form, byte for byte. The repeater form is the
// station's own frames with another site; the console form is the published
// reply a Quantar accepted.
//
// Break it: give the console the repeater's address or type, or drop the
// doubling of the site number, and a row fails.
func TestWhatQSPSaysInEachForm(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want []byte
	}{
		{"repeater asks", Repeater.LinkRequest(), []byte{0xFD, 0x3F}},
		{"repeater introduces site 2", Repeater.Introduction(2),
			[]byte{0xFD, 0xBF, 0x01, 0x05, 0xC2, 0, 0, 0, 0, 0xFF}},
		{"repeater introduces the largest site", Repeater.Introduction(MaxSite),
			[]byte{0xFD, 0xBF, 0x01, 0xFF, 0xC2, 0, 0, 0, 0, 0xFF}},
		{"repeater keeps alive", Repeater.Keepalive(), []byte{0xFD, 0x01}},
		{"console asks", Console.LinkRequest(), []byte{0x0B, 0x3F}},
		{"console introduces site 13, as published", Console.Introduction(13),
			[]byte{0x0B, 0xBF, 0x01, 0x1B, 0x00, 0, 0, 0, 0, 0xFF}},
		{"console keeps alive", Console.Keepalive(), []byte{0x0B, 0x01}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if !bytes.Equal(tc.got, tc.want) {
				t.Errorf("got % x, want % x", tc.got, tc.want)
			}
		})
	}
	for name, want := range map[string]string{"": "repeater", "repeater": "repeater", "console": "console"} {
		if id, ok := IdentityNamed(name); !ok || id.Name != want {
			t.Errorf("%q named %q, %v", name, id.Name, ok)
		}
	}
	if _, ok := IdentityNamed("Repeater "); ok {
		t.Error("a name that is neither form was taken")
	}
}

// The link's state for every order the station's frames can arrive in.
//
// Break it: introduce on a link open from one end only, or count a link up
// before both ends have introduced themselves, and a row fails.
func TestTheLinkOpensFromBothEnds(t *testing.T) {
	const (
		req = KindLinkRequest
		acc = KindAcceptance
		xid = KindIntroduction
		rr  = KindReceiveReady
	)
	tests := []struct {
		name      string
		frames    []Kind
		introduce []bool // whether QSP introduces itself after each frame
		up        bool
	}{
		{"the expected order", []Kind{req, acc, xid, rr}, []bool{false, false, true, false}, true},
		{"its introduction before its acceptance", []Kind{req, xid, acc, rr}, []bool{false, false, true, false}, true},
		{"an introduction on a link only it has opened", []Kind{req, xid, xid, xid}, []bool{false, false, false, false}, false},
		{"a repeated introduction is answered again", []Kind{req, acc, xid, xid}, []bool{false, false, true, true}, false},
		{"a second acceptance changes nothing", []Kind{req, xid, acc, acc}, []bool{false, false, true, false}, false},
		{"a keepalive before any introduction", []Kind{req, acc, rr}, []bool{false, false, false}, false},
		{"an acceptance with no request of its own", []Kind{acc, xid}, []bool{false, false}, false},
		{"starting again takes the link down", []Kind{req, acc, xid, rr, req}, []bool{false, false, true, false, false}, false},
		{"and it comes back", []Kind{req, acc, xid, rr, req, acc, xid, rr}, []bool{false, false, true, false, false, false, true, false}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var s linkState
			for i, k := range tc.frames {
				var introduce bool
				s, introduce = s.after(k)
				if introduce != tc.introduce[i] {
					t.Errorf("after frame %d (%s) introduce is %v", i, k, introduce)
				}
			}
			if s.up != tc.up {
				t.Errorf("up is %v", s.up)
			}
		})
	}
}

// What the station did when its introduction went unanswered, and when it was
// answered on a link open from one end only, as captured: one link request,
// three introductions, and round again. And never an acceptance.
//
// Break it: classify the introduction by its length alone, or by its control
// byte alone, and the capture still passes while the table above does not —
// which is why both exist.
func TestTheCapturedLoopIsRequestsAndIntroductions(t *testing.T) {
	for _, name := range []string{"stun-introduction-unanswered.bin", "stun-introduction-ignored.bin"} {
		t.Run(name, func(t *testing.T) { capturedLoop(t, name) })
	}
}

func capturedLoop(t *testing.T, name string) {
	raw, err := os.ReadFile("../../testdata/quantar/" + name)
	if err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	r := bytes.NewReader(raw)
	var kinds []Kind
	for {
		f, err := ReadFrame(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("frame %d: %v", len(kinds), err)
		}
		if f.Op != OpData {
			continue
		}
		kinds = append(kinds, Classify(f.Payload))
		if kinds[len(kinds)-1] == KindIntroduction && !bytes.Equal(f.Payload, stationIntroduction) {
			t.Errorf("an introduction read % x", f.Payload)
		}
	}
	want := []Kind{KindLinkRequest, KindIntroduction, KindIntroduction, KindIntroduction}
	if len(kinds) < 2*len(want) {
		t.Fatalf("only %d frames in the capture", len(kinds))
	}
	for i, k := range kinds {
		if k != want[i%len(want)] {
			t.Fatalf("frame %d is a %s, want a %s", i, k, want[i%len(want)])
		}
	}
}
