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
// that did not ask, or leave the station's own site number in the
// introduction, and a row here fails.
func TestWhatEachFrameIsOwed(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		site uint8
		want []byte
		kind Kind
	}{
		{"the request as captured", []byte{0xFD, 0x3F}, 2, []byte{0xFD, 0x73}, KindLinkRequest},
		{"a request without the poll bit", []byte{0xFD, 0x2F}, 2, []byte{0xFD, 0x63}, KindLinkRequest},
		{"the station's address is carried back", []byte{0x07, 0x3F}, 2, []byte{0x07, 0x73}, KindLinkRequest},
		{"the introduction as captured, answered as site 2", stationIntroduction, 2,
			[]byte{0xFD, 0xBF, 0x01, 0x05, 0xC2, 0x00, 0x00, 0x00, 0x00, 0xFF}, KindIntroduction},
		{"answered as site 13", stationIntroduction, 13,
			[]byte{0xFD, 0xBF, 0x01, 0x1B, 0xC2, 0x00, 0x00, 0x00, 0x00, 0xFF}, KindIntroduction},
		{"answered as the largest site", stationIntroduction, MaxSite,
			[]byte{0xFD, 0xBF, 0x01, 0xFF, 0xC2, 0x00, 0x00, 0x00, 0x00, 0xFF}, KindIntroduction},
		{"an introduction cut short", stationIntroduction[:9], 2, nil, KindUnknown},
		{"an introduction of another message type",
			[]byte{0xFD, 0xBF, 0x02, 0x03, 0xC2, 0, 0, 0, 0, 0xFF}, 2, nil, KindUnknown},
		{"a keepalive that asks nothing", []byte{0xFD, 0x01}, 2, nil, KindReceiveReady},
		{"a keepalive that demands an answer", []byte{0xFD, 0x11}, 2, []byte{0xFD, 0x11}, KindReceiveReady},
		{"a keepalive carrying a sequence number", []byte{0xFD, 0x41}, 2, nil, KindReceiveReady},
		{"an acceptance sent to us", []byte{0xFD, 0x73}, 2, nil, KindUnknown},
		{"voice", []byte{0x07, 0x03, 0x60, 0x02, 0x04, 0x0C}, 2, nil, KindUnknown},
		{"a request with bytes after it", []byte{0xFD, 0x3F, 0x00}, 2, nil, KindUnknown},
		{"one byte", []byte{0xFD}, 2, nil, KindUnknown},
		{"nothing", nil, 2, nil, KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := bytes.Clone(tc.in)
			got, kind := Answer(tc.in, tc.site)
			if !bytes.Equal(got, tc.want) || kind != tc.kind {
				t.Errorf("got % x (%s), want % x (%s)", got, kind, tc.want, tc.kind)
			}
			if !bytes.Equal(tc.in, in) {
				t.Errorf("the station's frame was changed to % x", tc.in)
			}
		})
	}
}

// What the station did when its introduction went unanswered, as captured:
// one link request, three introductions, and round again.
//
// Break it: classify the introduction by its length alone, or by its control
// byte alone, and the capture still passes while the table above does not —
// which is why both exist.
func TestTheCapturedLoopIsRequestsAndIntroductions(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/quantar/stun-introduction-unanswered.bin")
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
