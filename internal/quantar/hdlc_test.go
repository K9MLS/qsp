package quantar

import (
	"bytes"
	"testing"
)

// Break it: answer with the request's own control byte, or answer every
// two-byte frame, and a row here fails.
func TestOnlyALinkRequestIsAnswered(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want []byte
		kind Kind
	}{
		{"the request as captured", []byte{0xFD, 0x3F}, []byte{0xFD, 0x73}, KindLinkRequest},
		{"a request without the poll bit", []byte{0xFD, 0x2F}, []byte{0xFD, 0x63}, KindLinkRequest},
		{"the station's address is carried back", []byte{0x07, 0x3F}, []byte{0x07, 0x73}, KindLinkRequest},
		{"a receive-ready frame", []byte{0xFD, 0x01}, nil, KindUnknown},
		{"an acceptance sent to us", []byte{0xFD, 0x73}, nil, KindUnknown},
		{"a request with bytes after it", []byte{0xFD, 0x3F, 0x00}, nil, KindUnknown},
		{"one byte", []byte{0xFD}, nil, KindUnknown},
		{"nothing", nil, nil, KindUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, kind := Answer(tc.in)
			if !bytes.Equal(got, tc.want) || kind != tc.kind {
				t.Errorf("got % x (%s), want % x (%s)", got, kind, tc.want, tc.kind)
			}
		})
	}
}
