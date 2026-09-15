package zello

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A WebSocket client, RFC 6455, on the standard library.
//
// # Why this is hand-written
//
// There is no WebSocket in the standard library, and **this project cannot add
// a dependency**: `go.mod` and `go.sum` are never committed, so a library
// would be a build that works on one machine. The protocol needed here is
// small — a handshake, masked client frames, and answering pings — and writing
// it is less risk than a build nobody else can reproduce.
//
// It implements what Zello's Channels API uses and refuses the rest. The
// omissions are deliberate, not pending: extensions, compression and
// server-side operation are absent because nothing here is a server and Zello
// negotiates no extensions.
//
// # The two things that break a WebSocket client in the field
//
// **Client frames must be masked**, and a server must close the connection on
// an unmasked one (§5.1). A client that forgets is not "mostly working" — it
// is dropped at the first frame, which looks like a network fault.
//
// **Pings must be answered.** Zello sends one every 30 seconds and terminates
// the connection if a Pong takes longer than 30 to arrive, so a client that
// ignores them has a link that drops every half minute for no visible reason.
// This one answers from inside Read, so a caller that is reading is a caller
// that is replying.

// The frame opcodes this client handles, RFC 6455 §5.2.
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

// websocketGUID is the constant a server mixes into the key to prove it
// understood the handshake, RFC 6455 §4.2.2.
const websocketGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// MaxFrameBytes bounds a single incoming message.
//
// Generous for this protocol — the largest thing Zello sends is an audio
// packet of about a kilobyte — and finite, because a length field is 63 bits
// wide and a server that claims one is a server that would otherwise be
// allowed to allocate it.
const MaxFrameBytes = 1 << 20

// MessageKind distinguishes a text frame from a binary one, because the
// Channels API uses both: JSON commands as text and audio as binary.
type MessageKind int

// The two kinds a caller sees. Control frames are handled inside and never
// surface.
const (
	KindText MessageKind = iota
	KindBinary
)

// Conn is a WebSocket connection.
//
// **Safe for one reader and one writer**, which is what this protocol needs: a
// session reads events on one goroutine and sends commands and audio from
// another. Two writers would interleave frames, so writes take a mutex; two
// readers would each get half a message, so they are not permitted and there
// is nothing to make it look as though they are.
type Conn struct {
	raw net.Conn
	br  *bufio.Reader

	// writeMu serialises writes. A frame is a header and a payload written
	// separately, so two goroutines writing at once produce a stream neither
	// of them sent.
	writeMu sync.Mutex

	closeOnce sync.Once
	closeErr  error
	// closeSent records that a close frame has gone out, so it goes out once.
	//
	// **Sending two is a protocol violation and a deadlock.** RFC 6455 §5.5.1
	// permits one close frame per direction; a reader that echoes a close and
	// then calls Close would send a second, and against a peer that has
	// stopped reading the second write blocks forever. Found by a test hanging
	// rather than failing.
	closeSent bool
}

// Dial opens a WebSocket connection to a wss:// URL.
//
// **TLS only.** Zello's specification says the protocol supports no other kind
// of connection, and a client that quietly accepted ws:// would send a
// password in the clear on a misconfiguration.
func Dial(ctx interface {
	Done() <-chan struct{}
	Err() error
}, rawURL string, timeout time.Duration) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("zello: %q is not a URL: %w", rawURL, err)
	}
	if u.Scheme != "wss" {
		return nil, fmt.Errorf(
			"zello: %q is not wss://; the Channels API supports no other kind of "+
				"connection and a plain one would send the password in the clear",
			rawURL)
	}
	host := u.Host
	if u.Port() == "" {
		host = net.JoinHostPort(u.Hostname(), "443")
	}

	d := &net.Dialer{Timeout: timeout}
	raw, err := tls.DialWithDialer(d, "tcp", host, &tls.Config{
		ServerName: u.Hostname(),
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return nil, fmt.Errorf("zello: cannot reach %s: %w", host, err)
	}

	c, err := handshake(raw, u, timeout)
	if err != nil {
		raw.Close()
		return nil, err
	}
	return c, nil
}

// NewConn wraps an already-connected socket that has completed a handshake.
//
// It exists so that the framing can be tested over a pipe without TLS or a
// server: the handshake and the framing are separate problems and pretending
// otherwise would make the framing untestable.
func NewConn(raw net.Conn) *Conn {
	return &Conn{raw: raw, br: bufio.NewReader(raw)}
}

// handshake performs the HTTP upgrade and checks the server's answer.
//
// **The accept header is verified rather than assumed.** It proves the server
// understood the WebSocket handshake and is not an HTTP proxy or a cache
// answering 101 for its own reasons — RFC 6455 §4.1 requires the client to
// check it, and skipping it means talking framing to something that is not a
// WebSocket.
func handshake(raw net.Conn, u *url.URL, timeout time.Duration) (*Conn, error) {
	key := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("zello: cannot generate a handshake key: %w", err)
	}
	encodedKey := base64.StdEncoding.EncodeToString(key)

	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + encodedKey + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"

	if timeout > 0 {
		_ = raw.SetDeadline(time.Now().Add(timeout))
	}
	if _, err := raw.Write([]byte(req)); err != nil {
		return nil, fmt.Errorf("zello: cannot send the handshake: %w", err)
	}

	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, fmt.Errorf("zello: cannot read the handshake reply: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSwitchingProtocols {
		return nil, fmt.Errorf(
			"zello: the server answered %s rather than upgrading the connection",
			resp.Status)
	}
	if !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket") {
		return nil, fmt.Errorf("zello: the server upgraded to %q rather than websocket",
			resp.Header.Get("Upgrade"))
	}
	if got, want := resp.Header.Get("Sec-WebSocket-Accept"), acceptFor(encodedKey); got != want {
		return nil, fmt.Errorf(
			"zello: the server's accept header is %q and this handshake requires "+
				"%q; something answered 101 without understanding the handshake",
			got, want)
	}

	_ = raw.SetDeadline(time.Time{})
	return &Conn{raw: raw, br: br}, nil
}

// acceptFor computes the header a server must return, RFC 6455 §4.2.2: the
// base64 of SHA-1 over the client's key concatenated with the protocol GUID.
//
// SHA-1 here is not a security choice and is not being relied on for one — the
// specification names it, and its purpose is to prove the server parsed the
// handshake rather than to authenticate anything.
func acceptFor(encodedKey string) string {
	sum := sha1.Sum([]byte(encodedKey + websocketGUID))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// WriteMessage sends one message, masked as a client must.
func (c *Conn) WriteMessage(kind MessageKind, payload []byte) error {
	op := opText
	if kind == KindBinary {
		op = opBinary
	}
	return c.writeFrame(op, payload)
}

// writeFrame sends a single unfragmented frame.
//
// **Always masked**, because RFC 6455 §5.1 requires a client to mask and a
// server to close the connection on an unmasked frame. There is no option to
// turn it off: an option would be a way to produce a connection that is
// dropped at the first frame and looks like a network fault.
func (c *Conn) writeFrame(op byte, payload []byte) error {
	if len(payload) > MaxFrameBytes {
		return fmt.Errorf("zello: a message of %d bytes exceeds the %d limit",
			len(payload), MaxFrameBytes)
	}

	header := make([]byte, 0, 14)
	header = append(header, 0x80|op) // FIN set: one frame per message

	n := len(payload)
	switch {
	case n < 126:
		header = append(header, 0x80|byte(n)) // mask bit set
	case n < 1<<16:
		header = append(header, 0x80|126)
		header = binary.BigEndian.AppendUint16(header, uint16(n))
	default:
		header = append(header, 0x80|127)
		header = binary.BigEndian.AppendUint64(header, uint64(n))
	}

	var mask [4]byte
	if _, err := io.ReadFull(rand.Reader, mask[:]); err != nil {
		return fmt.Errorf("zello: cannot generate a mask: %w", err)
	}
	header = append(header, mask[:]...)

	masked := make([]byte, n)
	for i := range payload {
		masked[i] = payload[i] ^ mask[i%4]
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := c.raw.Write(header); err != nil {
		return fmt.Errorf("zello: cannot write a frame header: %w", err)
	}
	if n > 0 {
		if _, err := c.raw.Write(masked); err != nil {
			return fmt.Errorf("zello: cannot write a frame payload: %w", err)
		}
	}
	return nil
}

// ErrClosed is returned when the server closed the connection cleanly.
var ErrClosed = errors.New("zello: the server closed the connection")

// ReadMessage returns the next text or binary message.
//
// **Control frames are handled here and never surface.** A ping is answered
// with a pong before the next data frame is looked for, which is what keeps
// the connection alive: Zello terminates it if a pong is later than 30
// seconds, and a caller that had to remember to answer would eventually be a
// caller that forgot.
//
// Fragmented messages are reassembled. Zello does not appear to fragment, but
// a client that treated a continuation frame as a protocol error would break
// the first time anything in the path decided to.
func (c *Conn) ReadMessage() (MessageKind, []byte, error) {
	var (
		assembled []byte
		kind      MessageKind
		started   bool
	)

	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}

		switch op {
		case opPing:
			// Answered immediately, with the ping's own payload as §5.5.3
			// requires.
			if err := c.writeFrame(opPong, payload); err != nil {
				return 0, nil, fmt.Errorf("zello: cannot answer a ping: %w", err)
			}
			continue
		case opPong:
			// Unsolicited or an answer to a ping this client sent; nothing to
			// do, and not an error.
			continue
		case opClose:
			code, reason := closeReason(payload)
			// Echo the close once, then report it. A client that vanishes
			// without echoing leaves the server waiting; one that echoes
			// twice violates §5.5.1 and blocks on the second write.
			c.sendClose(payload)
			_ = c.Close()
			if code == 1000 || code == 0 {
				return 0, nil, ErrClosed
			}
			return 0, nil, fmt.Errorf("%w: %d %s", ErrClosed, code, reason)

		case opText, opBinary:
			if started {
				return 0, nil, errors.New(
					"zello: a new message began before the previous one finished")
			}
			started = true
			kind = KindText
			if op == opBinary {
				kind = KindBinary
			}
			assembled = payload

		case opContinuation:
			if !started {
				return 0, nil, errors.New(
					"zello: a continuation frame arrived with no message to continue")
			}
			if len(assembled)+len(payload) > MaxFrameBytes {
				return 0, nil, fmt.Errorf(
					"zello: a fragmented message exceeds the %d limit", MaxFrameBytes)
			}
			assembled = append(assembled, payload...)

		default:
			return 0, nil, fmt.Errorf("zello: opcode %#x is not one this client reads", op)
		}

		if fin {
			return kind, assembled, nil
		}
	}
}

// readFrame reads one frame.
//
// **A masked frame from the server is refused.** RFC 6455 §5.1 says a server
// must not mask, and unmasking one anyway would mean accepting a stream from
// something that is not following the protocol — which is exactly when a
// client should stop rather than guess.
func (c *Conn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var head [2]byte
	if _, err := io.ReadFull(c.br, head[:]); err != nil {
		return false, 0, nil, err
	}

	fin = head[0]&0x80 != 0
	if head[0]&0x70 != 0 {
		return false, 0, nil, errors.New(
			"zello: a reserved bit is set, so an extension was negotiated that " +
				"this client did not agree to")
	}
	op = head[0] & 0x0F

	masked := head[1]&0x80 != 0
	if masked {
		return false, 0, nil, errors.New(
			"zello: the server masked a frame, which RFC 6455 forbids")
	}

	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}

	// **Checked before allocating.** The field is 63 bits wide, so a server
	// claiming a large frame is a server asking this process to allocate it.
	if length > MaxFrameBytes {
		return false, 0, nil, fmt.Errorf(
			"zello: the server announced a frame of %d bytes and the limit is %d",
			length, MaxFrameBytes)
	}

	if length > 0 {
		payload = make([]byte, length)
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return false, 0, nil, err
		}
	}
	return fin, op, payload, nil
}

// closeReason reads a close frame's status code and text, §5.5.1.
func closeReason(payload []byte) (uint16, string) {
	if len(payload) < 2 {
		return 0, ""
	}
	return binary.BigEndian.Uint16(payload[:2]), string(payload[2:])
}

// Close shuts the connection down, sending a close frame first.
//
// Idempotent, because both a reader seeing a close frame and a caller giving
// up will reach for it.
func (c *Conn) Close() error {
	c.closeOnce.Do(func() {
		var payload [2]byte
		binary.BigEndian.PutUint16(payload[:], 1000) // normal closure
		c.sendClose(payload[:])
		c.closeErr = c.raw.Close()
	})
	return c.closeErr
}

// sendClose writes a close frame at most once for the life of the connection.
func (c *Conn) sendClose(payload []byte) {
	c.writeMu.Lock()
	already := c.closeSent
	c.closeSent = true
	c.writeMu.Unlock()
	if already {
		return
	}
	_ = c.writeFrame(opClose, payload)
}

// SetReadDeadline bounds a read, so a session can notice a server that has
// stopped speaking without waiting forever.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.raw.SetReadDeadline(t) }
