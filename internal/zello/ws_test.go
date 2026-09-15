package zello

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// The WebSocket client, against RFC 6455's own field layout.
//
// **Every expectation here is built from the specification rather than from
// this client's encoder.** A test that framed its expected bytes by calling
// the code under test would pass for any consistent mistake, which is the
// shape this project has now caught seventeen times.

// pair returns two ends of an in-memory connection: the client's Conn and the
// raw socket a server would hold.
func pair(t *testing.T) (*Conn, net.Conn) {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })
	return NewConn(client), server
}

// TestAClientFrameIsMasked is the rule that decides whether a connection works
// at all.
//
// RFC 6455 §5.1: a client must mask every frame and a server must close the
// connection on an unmasked one. **A client that forgets is not "mostly
// working"** — it is dropped at the first frame, which presents as a network
// fault.
func TestAClientFrameIsMasked(t *testing.T) {
	c, server := pair(t)

	go func() {
		_ = c.WriteMessage(KindText, []byte("hello"))
	}()

	// **Read the header and the payload separately.** net.Pipe delivers one
	// Write per Read, and this client writes a header and a payload as two —
	// a single Read returns only the header, which made the first version of
	// this test look like a short frame.
	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	header := make([]byte, 2+4)
	if _, err := readFull(server, header); err != nil {
		t.Fatalf("reading the header: %v", err)
	}
	body := make([]byte, 5)
	if _, err := readFull(server, body); err != nil {
		t.Fatalf("reading the payload: %v", err)
	}
	frame := append(append([]byte(nil), header...), body...)

	// Byte 0: FIN set, no reserved bits, opcode 1 for text.
	if frame[0] != 0x81 {
		t.Errorf("byte 0 is %#02x, want 0x81 — FIN set and opcode 1 for text",
			frame[0])
	}
	// Byte 1: mask bit set, then the length in the low seven bits.
	if frame[1]&0x80 == 0 {
		t.Fatalf("byte 1 is %#02x and the mask bit is clear; the server will "+
			"close the connection", frame[1])
	}
	if got := frame[1] & 0x7F; got != 5 {
		t.Errorf("the length is %d, want 5", got)
	}

	// Four bytes of mask, then the payload exclusive-ored with it.
	if len(frame) != 2+4+5 {
		t.Fatalf("the frame is %d bytes; a 5-byte masked message is %d",
			len(frame), 2+4+5)
	}
	mask := frame[2:6]
	unmasked := make([]byte, 5)
	for i := range unmasked {
		unmasked[i] = frame[6+i] ^ mask[i%4]
	}
	if string(unmasked) != "hello" {
		t.Errorf("the payload unmasks to %q", unmasked)
	}
	// And the payload is not sitting there in the clear, which is what an
	// all-zero mask would produce.
	if bytes.Equal(frame[6:], []byte("hello")) {
		t.Error("the payload is unmasked on the wire; the mask key is all zeros")
	}
}

// TestEveryFrameUsesAFreshMask.
//
// RFC 6455 §5.3 requires a fresh unpredictable mask per frame, so that
// identical payloads do not produce identical bytes — which is what lets a
// proxy be confused into treating framed data as a request.
func TestEveryFrameUsesAFreshMask(t *testing.T) {
	c, server := pair(t)

	read := func() []byte {
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
		header := make([]byte, 2+4)
		if _, err := readFull(server, header); err != nil {
			t.Fatalf("reading a header: %v", err)
		}
		body := make([]byte, int(header[1]&0x7F))
		if _, err := readFull(server, body); err != nil {
			t.Fatalf("reading a payload: %v", err)
		}
		return append(append([]byte(nil), header...), body...)
	}

	go func() {
		_ = c.WriteMessage(KindText, []byte("same"))
		_ = c.WriteMessage(KindText, []byte("same"))
	}()

	first, second := read(), read()
	if bytes.Equal(first, second) {
		t.Fatal("the same payload produced identical frames, so the mask is fixed")
	}
	if bytes.Equal(first[2:6], second[2:6]) {
		t.Fatal("two frames share a mask key")
	}
}

// TestTheLengthEncodingFollowsTheThreeCases, §5.2.
//
// Under 126 the length is in the byte; 126 means a 16-bit length follows; 127
// means 64-bit. Getting the boundary wrong sends a frame the server reads as a
// different length, which desynchronises the stream and looks like corruption.
func TestTheLengthEncodingFollowsTheThreeCases(t *testing.T) {
	for _, tc := range []struct {
		size      int
		indicator byte
		extra     int
	}{
		{0, 0, 0},
		{1, 1, 0},
		{125, 125, 0},
		{126, 126, 2},
		{1000, 126, 2},
		{1 << 16, 127, 8},
	} {
		c, server := pair(t)
		go func() { _ = c.WriteMessage(KindBinary, make([]byte, tc.size)) }()

		header := make([]byte, 2+8+4)
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := readFull(server, header[:2+tc.extra+4]); err != nil {
			t.Fatalf("%d bytes: reading the header: %v", tc.size, err)
		}

		if got := header[1] & 0x7F; got != tc.indicator {
			t.Errorf("%d bytes: the length indicator is %d, want %d",
				tc.size, got, tc.indicator)
		}
		switch tc.extra {
		case 2:
			if got := binary.BigEndian.Uint16(header[2:4]); int(got) != tc.size {
				t.Errorf("%d bytes: the 16-bit length reads %d", tc.size, got)
			}
		case 8:
			if got := binary.BigEndian.Uint64(header[2:10]); int(got) != tc.size {
				t.Errorf("%d bytes: the 64-bit length reads %d", tc.size, got)
			}
		}
		if header[0]&0x0F != 0x2 {
			t.Errorf("%d bytes: the opcode is %#x, want 2 for binary",
				tc.size, header[0]&0x0F)
		}
	}
}

// readFull is io.ReadFull without importing io for one call.
func readFull(c net.Conn, into []byte) (int, error) {
	read := 0
	for read < len(into) {
		n, err := c.Read(into[read:])
		read += n
		if err != nil {
			return read, err
		}
	}
	return read, nil
}

// serverFrame builds an unmasked server frame from RFC 6455's layout, by hand.
//
// **Not by calling the client's encoder**, which would make every test pass
// for any consistent mistake.
func serverFrame(fin bool, op byte, payload []byte) []byte {
	var first byte = op
	if fin {
		first |= 0x80
	}
	out := []byte{first}
	switch n := len(payload); {
	case n < 126:
		out = append(out, byte(n))
	case n < 1<<16:
		out = append(out, 126)
		out = binary.BigEndian.AppendUint16(out, uint16(n))
	default:
		out = append(out, 127)
		out = binary.BigEndian.AppendUint64(out, uint64(n))
	}
	return append(out, payload...)
}

// TestAPingIsAnsweredFromInsideRead is what keeps the connection alive.
//
// Zello sends a ping every 30 seconds and terminates the connection if a pong
// is later than 30. **A client that ignored them would drop every half minute
// for no visible reason**, so the answer happens inside Read rather than being
// a caller's responsibility — a caller that had to remember would eventually
// be one that forgot.
//
// §5.5.3 also requires the pong to carry the ping's own payload.
func TestAPingIsAnsweredFromInsideRead(t *testing.T) {
	c, server := pair(t)

	go func() {
		_, _ = server.Write(serverFrame(true, opPing, []byte("are you there")))
		_, _ = server.Write(serverFrame(true, opText, []byte(`{"seq":1}`)))
	}()

	// The read returns the data frame, not the ping.
	done := make(chan struct{})
	var kind MessageKind
	var payload []byte
	var readErr error
	go func() {
		kind, payload, readErr = c.ReadMessage()
		close(done)
	}()

	// The pong comes back first.
	_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
	pongHeader := make([]byte, 2+4)
	if _, err := readFull(server, pongHeader); err != nil {
		t.Fatalf("reading the pong header: %v", err)
	}
	pongBody := make([]byte, int(pongHeader[1]&0x7F))
	if _, err := readFull(server, pongBody); err != nil {
		t.Fatalf("reading the pong payload: %v", err)
	}
	pong := append(append([]byte(nil), pongHeader...), pongBody...)
	if pong[0]&0x0F != opPong {
		t.Errorf("the reply has opcode %#x, want %#x for pong", pong[0]&0x0F, opPong)
	}
	if pong[1]&0x80 == 0 {
		t.Error("the pong is unmasked; a client must mask every frame")
	}
	mask := pong[2:6]
	body := make([]byte, len(pong)-6)
	for i := range body {
		body[i] = pong[6+i] ^ mask[i%4]
	}
	if string(body) != "are you there" {
		t.Errorf("the pong carries %q; §5.5.3 requires the ping's own payload", body)
	}

	<-done
	if readErr != nil {
		t.Fatalf("reading: %v", readErr)
	}
	if kind != KindText || string(payload) != `{"seq":1}` {
		t.Errorf("the read returned kind %d and %q; a ping must not surface",
			kind, payload)
	}
}

// TestAMaskedServerFrameIsRefused.
//
// §5.1 says a server must not mask. Unmasking one anyway would mean accepting a
// stream from something that is not following the protocol, which is exactly
// when a client should stop rather than guess.
func TestAMaskedServerFrameIsRefused(t *testing.T) {
	c, server := pair(t)

	go func() {
		// A server frame with the mask bit set, built by hand.
		frame := []byte{0x81, 0x80 | 4, 1, 2, 3, 4}
		for i, b := range []byte("mask") {
			frame = append(frame, b^[]byte{1, 2, 3, 4}[i%4])
		}
		_, _ = server.Write(frame)
	}()

	_, _, err := c.ReadMessage()
	if err == nil {
		t.Fatal("a masked server frame was accepted")
	}
	if !strings.Contains(err.Error(), "forbids") {
		t.Errorf("the refusal does not say the specification forbids it: %v", err)
	}
}

// TestAnAnnouncedFrameLargerThanTheLimitIsRefusedBeforeAllocating.
//
// The length field is 63 bits wide, so a server claiming an enormous frame is
// a server asking this process to allocate it. The check comes before the
// allocation, which is the only place it helps.
func TestAnAnnouncedFrameLargerThanTheLimitIsRefusedBeforeAllocating(t *testing.T) {
	c, server := pair(t)

	go func() {
		// A 64-bit length announcing far more than the limit, and no payload
		// to follow: if the client allocates first it will block or die
		// rather than refuse.
		head := []byte{0x82, 127}
		head = binary.BigEndian.AppendUint64(head, 1<<40)
		_, _ = server.Write(head)
	}()

	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := c.ReadMessage()
	if err == nil {
		t.Fatal("a frame larger than the limit was accepted")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("the refusal does not mention the limit: %v", err)
	}
}

// TestAFragmentedMessageIsReassembled.
//
// Zello does not appear to fragment, but a client treating a continuation
// frame as an error would break the first time anything in the path decided
// to — and the failure would be intermittent and blamed on the network.
func TestAFragmentedMessageIsReassembled(t *testing.T) {
	c, server := pair(t)

	go func() {
		_, _ = server.Write(serverFrame(false, opText, []byte(`{"seq"`)))
		_, _ = server.Write(serverFrame(false, opContinuation, []byte(`:1,"su`)))
		_, _ = server.Write(serverFrame(true, opContinuation, []byte(`ccess":true}`)))
	}()

	kind, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if kind != KindText {
		t.Errorf("the reassembled message is kind %d, want text", kind)
	}
	if string(payload) != `{"seq":1,"success":true}` {
		t.Errorf("the message reassembled as %q", payload)
	}

	// A continuation with nothing to continue is a protocol error rather than
	// a message of its own.
	c2, server2 := pair(t)
	go func() { _, _ = server2.Write(serverFrame(true, opContinuation, []byte("orphan"))) }()
	if _, _, err := c2.ReadMessage(); err == nil {
		t.Error("an orphan continuation frame was accepted as a message")
	}
}

// TestACloseFrameIsEchoedAndReported.
//
// A client that vanishes without echoing leaves the server waiting, and a
// caller that cannot tell a clean close from a network error will reconnect
// when it should stop.
func TestACloseFrameIsEchoedAndReported(t *testing.T) {
	c, server := pair(t)

	go func() {
		var payload [2]byte
		binary.BigEndian.PutUint16(payload[:], 1000)
		_, _ = server.Write(serverFrame(true, opClose, payload[:]))
		// **Drain one echo and only one.** An earlier version of this client
		// echoed the close and then sent a second from Close, which is a
		// §5.5.1 violation and, over an unbuffered pipe, a deadlock — the
		// test hung rather than failed.
		_ = server.SetReadDeadline(time.Now().Add(2 * time.Second))
		echo := make([]byte, 2+4+2)
		_, _ = readFull(server, echo)
		// Anything further would be a second close frame. Reading with a
		// short deadline proves there is none.
		_ = server.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		extra := make([]byte, 1)
		if n, _ := server.Read(extra); n > 0 {
			t.Error("a second frame followed the close echo; §5.5.1 permits one " +
				"per direction and the second write deadlocks")
		}
	}()

	_, _, err := c.ReadMessage()
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("a clean close gave %v, want ErrClosed", err)
	}
}

// TestTheAcceptHeaderIsComputedFromTheSpecificationsOwnExample.
//
// RFC 6455 §1.3 works the handshake through with a key of
// `dGhlIHNhbXBsZSBub25jZQ==` and an accept of
// `s3pPLMBiTxaQ9kYGzzhZRbK+xOo=`. **Checked against the specification's
// numbers**, because the alternative is checking a SHA-1 against itself.
func TestTheAcceptHeaderIsComputedFromTheSpecificationsOwnExample(t *testing.T) {
	const (
		key  = "dGhlIHNhbXBsZSBub25jZQ=="
		want = "s3pPLMBiTxaQ9kYGzzhZRbK+xOo="
	)
	if got := acceptFor(key); got != want {
		t.Errorf("the accept for %q is %q and RFC 6455 §1.3 gives %q", key, got, want)
	}
	// And the GUID is the specification's, since the whole computation rests
	// on it.
	if websocketGUID != "258EAFA5-E914-47DA-95CA-C5AB0DC85B11" {
		t.Errorf("the protocol GUID is %q", websocketGUID)
	}
	// A key of the right length base64-decodes to 16 bytes, which is what
	// §4.1 requires the client to send.
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 16 {
		t.Errorf("the specification's example key decodes to %d bytes", len(raw))
	}
}

// TestAPlainConnectionIsRefused.
//
// Zello's specification says the protocol supports no connection other than
// TLS, and a client that quietly accepted ws:// would send a password in the
// clear on a misconfiguration.
func TestAPlainConnectionIsRefused(t *testing.T) {
	ctx := neverDone{}
	for _, raw := range []string{
		"ws://zello.io/ws",
		"http://zello.io/ws",
		"zello.io/ws",
		"",
	} {
		if _, err := Dial(ctx, raw, time.Second); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

// neverDone is a context that never cancels, for a dial that must fail before
// it reaches the network.
type neverDone struct{}

func (neverDone) Done() <-chan struct{} { return nil }
func (neverDone) Err() error            { return nil }

// TestAReservedBitIsRefused.
//
// The reserved bits mean an extension was negotiated. This client negotiates
// none, so a frame setting one is a frame it cannot interpret — and guessing
// would mean handing a caller compressed bytes as if they were JSON.
func TestAReservedBitIsRefused(t *testing.T) {
	c, server := pair(t)
	go func() {
		// RSV1 set on a text frame.
		_, _ = server.Write([]byte{0x81 | 0x40, 2, 'h', 'i'})
	}()
	_, _, err := c.ReadMessage()
	if err == nil {
		t.Fatal("a frame with a reserved bit set was accepted")
	}
	if !strings.Contains(err.Error(), "extension") {
		t.Errorf("the refusal does not say why: %v", err)
	}
}
