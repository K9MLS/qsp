// Package zello speaks the Zello Channels API wire protocol.
//
// # Source
//
// The Zello Channel API specification, version 1.0, at
// github.com/zelloptt/zello-channel-api — `API.md`. **Written from the
// specification rather than from recollection**, which matters here for the
// same reason it mattered for the AMBE-3000: three values in this file would
// have been wrong from memory, and each would have failed in a way that looks
// like something else.
//
//   - `packet_id` is **filled with zeroes** when streaming to the server,
//     which ignores it. A counter looks equally plausible and is wrong.
//   - `channels` on logon is an **array**, not a single channel name.
//   - `frames_per_packet` in the codec header is **1 or 2 only**.
//
// # This package is the protocol, not the session
//
// It encodes commands, decodes responses and events, and frames binary audio
// packets. **It opens no connection**, which keeps every line of it testable
// without an account: the session that carries these over a WebSocket is a
// separate thing, and it is the part that cannot be exercised until the
// operator has credentials.
//
// # Two things the transport must do
//
// **TLS only.** The specification says the protocol supports no other kind of
// connection.
//
// **Answer the server's pings.** The API sends a WebSocket Ping every 30
// seconds and terminates the connection if a Pong takes longer than 30 seconds
// to come back. Most WebSocket libraries answer automatically; one that does
// not produces a link that drops every half minute and looks like a network
// fault.
package zello

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// Entry points, from the specification's own table. TLS only.
const (
	// EndpointFriendsAndFamily is the consumer service.
	EndpointFriendsAndFamily = "wss://zello.io/ws"
	// EndpointWorkFormat takes a Zello Work network name.
	EndpointWorkFormat = "wss://zellowork.io/ws/%s"
	// EndpointEnterpriseFormat takes a server domain.
	EndpointEnterpriseFormat = "wss://%s/ws/mesh"
)

// PingInterval is how often the server pings, and the deadline for the reply.
//
// The specification gives both as 30 seconds: a Pong later than that and the
// API terminates the connection.
const PingIntervalSeconds = 30

// PacketTypeAudio is the binary packet type for streamed audio.
//
// The specification also defines 0x02 for images. **Not implemented**: QSP
// carries voice, and a packet type accepted but not understood would be
// audio-shaped data handed to a decoder.
const PacketTypeAudio byte = 0x01

// PacketHeaderBytes is the binary packet header: a type byte and two 32-bit
// fields, in network byte order.
const PacketHeaderBytes = 1 + 4 + 4

// CodecHeader is the four-byte audio parameter block a stream declares.
//
// `{sample_rate_hz(16LE), frames_per_packet(8), frame_size_ms(8)}` — and note
// **the sample rate is little-endian while every other multi-byte field in
// this protocol is network byte order**. That asymmetry is the specification's,
// not a mistake here, and it is the kind of thing that produces a stream
// declaring 32 kHz when 16 was meant.
type CodecHeader struct {
	SampleRate      int
	FramesPerPacket int
	FrameSizeMS     int
}

// ZelloCodecHeader is the header QSP sends: 16 kHz, one frame per packet,
// 60 ms.
//
// It base64-encodes to `gD4BPA==`, which is the specification's own example
// value — so the encoding is checked against a string Zello published rather
// than against this package's own arithmetic.
var ZelloCodecHeader = CodecHeader{SampleRate: 16000, FramesPerPacket: 1, FrameSizeMS: 60}

// Validate reports whether the header is one the specification permits.
func (c CodecHeader) Validate() error {
	if c.SampleRate <= 0 || c.SampleRate > 0xFFFF {
		return fmt.Errorf(
			"zello: a sample rate of %d does not fit the header's 16 bits",
			c.SampleRate)
	}
	// The specification is explicit: 1 or 2.
	if c.FramesPerPacket != 1 && c.FramesPerPacket != 2 {
		return fmt.Errorf(
			"zello: frames per packet is %d and the specification permits 1 or 2",
			c.FramesPerPacket)
	}
	// "Values between 2.5 ms and 60 ms are supported", and the field is one
	// byte, so a fractional duration cannot be expressed in it at all.
	if c.FrameSizeMS < 1 || c.FrameSizeMS > 60 {
		return fmt.Errorf(
			"zello: a frame size of %d ms is outside the 2.5 to 60 the "+
				"specification supports", c.FrameSizeMS)
	}
	return nil
}

// Encode renders the header as the base64 string a command carries.
func (c CodecHeader) Encode() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	raw := []byte{
		byte(c.SampleRate), byte(c.SampleRate >> 8), // little-endian
		byte(c.FramesPerPacket),
		byte(c.FrameSizeMS),
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// DecodeCodecHeader reads a header from the base64 string an event carries.
//
// It is needed for `on_stream_start`: an incoming stream declares its own
// parameters, and **they need not be the ones QSP sends**. A caller that
// assumed 16 kHz and received 8 would play somebody's audio at half speed.
func DecodeCodecHeader(s string) (CodecHeader, error) {
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return CodecHeader{}, fmt.Errorf("zello: codec header %q is not base64: %w", s, err)
	}
	if len(raw) != 4 {
		return CodecHeader{}, fmt.Errorf(
			"zello: a codec header is 4 bytes and %q decodes to %d", s, len(raw))
	}
	c := CodecHeader{
		SampleRate:      int(raw[0]) | int(raw[1])<<8,
		FramesPerPacket: int(raw[2]),
		FrameSizeMS:     int(raw[3]),
	}
	if err := c.Validate(); err != nil {
		return c, err
	}
	return c, nil
}

// Logon is the first command a client sends.
//
// `platform_name` is worth setting deliberately: the specification says a name
// containing "Gateway" or "Kiosk", case-insensitively, makes Zello's Alarms
// service track this client's online status. QSP is a gateway and an operator
// wanting that notification should get it.
type Logon struct {
	Command      string   `json:"command"`
	Seq          int      `json:"seq"`
	AuthToken    string   `json:"auth_token,omitempty"`
	RefreshToken string   `json:"refresh_token,omitempty"`
	Username     string   `json:"username,omitempty"`
	Password     string   `json:"password,omitempty"`
	Channels     []string `json:"channels"`
	ListenOnly   bool     `json:"listen_only,omitempty"`
	Version      string   `json:"version,omitempty"`
	PlatformType string   `json:"platform_type,omitempty"`
	PlatformName string   `json:"platform_name,omitempty"`
}

// NewLogon builds a logon command for a named account on one channel.
//
// **A password is required when a username is given**, and the specification's
// `not enough params` error is what a server returns otherwise — an error that
// reads like a malformed request rather than a missing field.
func NewLogon(seq int, authToken, username, password string, channels []string) (Logon, error) {
	if len(channels) == 0 {
		return Logon{}, fmt.Errorf("zello: a logon names no channel")
	}
	if username != "" && password == "" {
		return Logon{}, fmt.Errorf(
			"zello: a username was given without a password; the server answers "+
				"%q, which reads like a malformed request", ErrNotEnoughParams)
	}
	if authToken == "" && username == "" {
		return Logon{}, fmt.Errorf(
			"zello: a logon needs an auth token, a username, or both")
	}
	return Logon{
		Command:   "logon",
		Seq:       seq,
		AuthToken: authToken,
		Username:  username,
		Password:  password,
		Channels:  append([]string(nil), channels...),
		// Named so that Zello's Alarms service tracks this client, per the
		// specification's note about "Gateway".
		PlatformType: "qsp",
		PlatformName: "QSP DMR Gateway",
	}, nil
}

// StartStream opens an outgoing voice message.
type StartStream struct {
	Command        string `json:"command"`
	Seq            int    `json:"seq"`
	Channel        string `json:"channel"`
	Type           string `json:"type"`
	Codec          string `json:"codec"`
	CodecHeader    string `json:"codec_header"`
	PacketDuration int    `json:"packet_duration"`
	For            string `json:"for,omitempty"`
}

// NewStartStream builds a start_stream command.
//
// The type is always `audio` and the codec always `opus`: the specification
// permits nothing else, which is the fact ADR-0062 rests on.
func NewStartStream(seq int, channel string, header CodecHeader) (StartStream, error) {
	if channel == "" {
		return StartStream{}, fmt.Errorf("zello: a stream names no channel")
	}
	encoded, err := header.Encode()
	if err != nil {
		return StartStream{}, err
	}
	return StartStream{
		Command:     "start_stream",
		Seq:         seq,
		Channel:     channel,
		Type:        "audio",
		Codec:       "opus",
		CodecHeader: encoded,
		// The duration must agree with the header, or the server is told two
		// different things about the same audio.
		PacketDuration: header.FrameSizeMS * header.FramesPerPacket,
	}, nil
}

// StopStream closes an outgoing voice message.
type StopStream struct {
	Command  string `json:"command"`
	Seq      int    `json:"seq"`
	StreamID uint32 `json:"stream_id"`
	Channel  string `json:"channel"`
}

// NewStopStream builds a stop_stream command.
//
// **It must be sent after the last data packet.** A stream left open holds the
// channel against everybody else until the server times it out, which is the
// same failure the routing core's abandoned-transmission sweep exists for, two
// boundaries out.
func NewStopStream(seq int, channel string, streamID uint32) StopStream {
	return StopStream{
		Command:  "stop_stream",
		Seq:      seq,
		StreamID: streamID,
		Channel:  channel,
	}
}

// Response is a reply to a command, identified by its sequence number.
type Response struct {
	Seq          int    `json:"seq"`
	Success      bool   `json:"success"`
	Error        string `json:"error"`
	StreamID     uint32 `json:"stream_id"`
	RefreshToken string `json:"refresh_token"`
}

// Event is a message the server sends unprompted.
//
// One type carrying every event's fields, because the alternative is a
// discriminated union built before anything needs it. `Command` says which
// fields mean anything.
type Event struct {
	Command string `json:"command"`

	// on_channel_status
	Channel     string `json:"channel"`
	Status      string `json:"status"`
	UsersOnline int    `json:"users_online"`
	ErrorType   string `json:"error_type"`

	// on_stream_start and on_stream_stop
	StreamID       uint32 `json:"stream_id"`
	Type           string `json:"type"`
	Codec          string `json:"codec"`
	CodecHeader    string `json:"codec_header"`
	PacketDuration int    `json:"packet_duration"`
	From           string `json:"from"`

	// on_error, and on_channel_status when a channel drops
	Error string `json:"error"`
}

// The event and status names this package acts on.
const (
	EventChannelStatus = "on_channel_status"
	EventStreamStart   = "on_stream_start"
	EventStreamStop    = "on_stream_stop"
	EventError         = "on_error"

	StatusOnline  = "online"
	StatusOffline = "offline"
)

// The error codes the specification lists. Named so that a caller can decide
// what to do rather than matching strings at the call site.
const (
	ErrUnknownCommand    = "unknown command"
	ErrInternalServer    = "internal server error"
	ErrInvalidJSON       = "invalid json"
	ErrInvalidRequest    = "invalid request"
	ErrNotAuthorized     = "not authorized"
	ErrNotLoggedIn       = "not logged in"
	ErrNotEnoughParams   = "not enough params"
	ErrServerClosed      = "server closed connection"
	ErrChannelNotReady   = "channel is not ready"
	ErrListenOnly        = "listen only connection"
	ErrFailedStartStream = "failed to start stream"
	ErrFailedStopStream  = "failed to stop stream"
	ErrFailedSendData    = "failed to send data"
	ErrInvalidAudio      = "invalid audio packet"
	ErrChannelsLimit     = "channels limit exceeded"
)

// Retryable reports whether an error code is worth reconnecting or retrying
// after.
//
// **Credentials are not retryable and everything else mostly is.** A client
// that retries `not authorized` forever hammers the service with a password
// that will never work, which is how an account gets locked; one that gives up
// on `server closed connection` stays down after a network blip the
// specification explicitly says to reconnect from.
func Retryable(code string) bool {
	switch code {
	case ErrNotAuthorized, ErrListenOnly, ErrNotEnoughParams,
		ErrUnknownCommand, ErrInvalidJSON, ErrInvalidRequest, ErrChannelsLimit:
		return false
	}
	return true
}

// Fatal reports whether an error means this configuration can never work, so
// that a health check can say so rather than reporting a permanent retry.
func Fatal(code string) bool {
	switch code {
	case ErrNotAuthorized, ErrListenOnly, ErrChannelsLimit:
		return true
	}
	return false
}

// Decode reads a text message from the server.
//
// A message with a `seq` is a response to a command; one with a `command` is
// an event. Both are returned so the caller can dispatch on whichever is set,
// and a message that is neither is refused rather than silently ignored.
func Decode(b []byte) (*Response, *Event, error) {
	var probe struct {
		Seq     *int    `json:"seq"`
		Command *string `json:"command"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return nil, nil, fmt.Errorf("zello: message is not JSON: %w", err)
	}

	switch {
	case probe.Seq != nil:
		var r Response
		if err := json.Unmarshal(b, &r); err != nil {
			return nil, nil, fmt.Errorf("zello: response: %w", err)
		}
		return &r, nil, nil
	case probe.Command != nil:
		var e Event
		if err := json.Unmarshal(b, &e); err != nil {
			return nil, nil, fmt.Errorf("zello: event: %w", err)
		}
		return nil, &e, nil
	}
	return nil, nil, fmt.Errorf(
		"zello: a message carries neither a sequence number nor a command: %s", b)
}

// AudioPacket renders one Opus packet for the wire.
//
// `{type(8) = 0x01, stream_id(32), packet_id(32), data[]}` in network byte
// order, and **the packet ID is zero**. The specification is explicit: when
// streaming to the server the value is ignored and should be filled with
// zeroes. It carries a packet number only in the other direction.
func AudioPacket(streamID uint32, opus []byte) ([]byte, error) {
	if len(opus) == 0 {
		return nil, fmt.Errorf("zello: an audio packet carries no audio")
	}
	out := make([]byte, PacketHeaderBytes, PacketHeaderBytes+len(opus))
	out[0] = PacketTypeAudio
	binary.BigEndian.PutUint32(out[1:5], streamID)
	binary.BigEndian.PutUint32(out[5:9], 0) // see above
	return append(out, opus...), nil
}

// IncomingAudio reads an audio packet from the server.
//
// It refuses anything that is not an audio packet rather than interpreting it:
// the specification's type 0x02 is an image, and handing image bytes to an
// Opus decoder is the loudest available failure.
func IncomingAudio(b []byte) (streamID, packetID uint32, opus []byte, ok bool) {
	if len(b) <= PacketHeaderBytes || b[0] != PacketTypeAudio {
		return 0, 0, nil, false
	}
	return binary.BigEndian.Uint32(b[1:5]),
		binary.BigEndian.Uint32(b[5:9]),
		b[PacketHeaderBytes:],
		true
}
