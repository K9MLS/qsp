package v24link

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"
)

const fixture = "../../testdata/quantar/stun-link-request.bin"

// The capture is the authority: one opening message, then the station's link
// request over and over, and written back out it is the same bytes.
//
// Break it: read a six-byte header, or take the length from the wrong two
// bytes, and the second frame's marker is wrong.
func TestTheCaptureReadsAsFramesAndWritesBackUnchanged(t *testing.T) {
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	r := bytes.NewReader(raw)
	var out []byte
	var frames []Frame
	for {
		f, err := ReadFrame(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("frame %d: %v", len(frames), err)
		}
		frames = append(frames, f)
		out = f.Append(out)
	}
	if len(frames) != 733 {
		t.Fatalf("read %d frames, and the capture holds 733", len(frames))
	}
	if f := frames[0]; f.Op != OpOpen || len(f.Payload) != 30 || f.Group != 1 {
		t.Errorf("the opening message read as type %04x, %d bytes, group %d",
			uint16(f.Op), len(f.Payload), f.Group)
	}
	for i, f := range frames[1:] {
		if f.Op != OpData || f.Group != 1 || !bytes.Equal(f.Payload, []byte{0xFD, 0x3F}) {
			t.Fatalf("frame %d read as type %04x group %d payload % x",
				i+1, uint16(f.Op), f.Group, f.Payload)
		}
	}
	if !bytes.Equal(out, raw) {
		t.Error("the frames written back are not the bytes captured")
	}
}

// Break it: drop the marker check, or the length limit, and a stream that has
// lost its place is read as frames.
func TestWhatIsNotAFrameIsRefused(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want error
	}{
		{"nothing at all", nil, io.EOF},
		{"a header cut short", []byte{0x08, 0x31, 0x00}, io.ErrUnexpectedEOF},
		{"the wrong marker", []byte{0x08, 0x32, 0, 0, 0, 2, 1, 0xFD, 0x3F}, ErrNotTunnel},
		{"a payload cut short", []byte{0x08, 0x31, 0, 0, 0, 2, 1, 0xFD}, io.ErrUnexpectedEOF},
		{"a length no serial frame has", []byte{0x08, 0x31, 0, 0, 0x08, 0x39, 1}, ErrTooLong},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadFrame(bytes.NewReader(tc.in))
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// The longest frame the serial line can carry is still a frame.
func TestTheLongestFrameIsRead(t *testing.T) {
	in := Frame{Op: OpData, Group: 1, Payload: make([]byte, MaxPayload)}
	f, err := ReadFrame(bytes.NewReader(in.Append(nil)))
	if err != nil || len(f.Payload) != MaxPayload {
		t.Fatalf("got %d bytes and %v", len(f.Payload), err)
	}
}
