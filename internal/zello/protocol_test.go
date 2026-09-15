package zello

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

// The Zello Channels API, against the specification's own published values.
//
// Every expectation here is transcribed from API.md — a base64 string, a
// packet layout, an example command, an error code — rather than computed the
// way the code computes it. The three values this package would have got wrong
// from memory are each asserted directly.

// TestTheCodecHeaderEncodesToZellosOwnExampleString is the best check
// available, because Zello published the answer.
//
// The specification says `gD4BPA==` decodes to `{0x80, 0x3e, 0x01, 0x3c}` and
// represents 16000 Hz, one frame per packet, 60 ms. So the encoder can be
// checked against a string this project did not produce.
//
// **The sample rate is little-endian while every other multi-byte field in the
// protocol is network byte order.** That asymmetry is the specification's, and
// getting it backwards yields a header declaring 32 kHz — which is why the
// bytes are asserted and not just the round trip.
func TestTheCodecHeaderEncodesToZellosOwnExampleString(t *testing.T) {
	got, err := ZelloCodecHeader.Encode()
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if want := "gD4BPA=="; got != want {
		t.Errorf("the header encodes to %q and the specification's example is %q",
			got, want)
	}

	back, err := DecodeCodecHeader("gD4BPA==")
	if err != nil {
		t.Fatalf("decoding the specification's example: %v", err)
	}
	if back != ZelloCodecHeader {
		t.Errorf("the example decodes to %+v, want %+v", back, ZelloCodecHeader)
	}
	if back.SampleRate != 16000 || back.FramesPerPacket != 1 || back.FrameSizeMS != 60 {
		t.Errorf("the example decodes to %d Hz, %d frames, %d ms; the "+
			"specification says 16000, 1, 60",
			back.SampleRate, back.FramesPerPacket, back.FrameSizeMS)
	}

	// The raw bytes, so a byte-order mistake cannot hide behind a round trip
	// that is wrong in both directions.
	raw := []byte{byte(back.SampleRate), byte(back.SampleRate >> 8),
		byte(back.FramesPerPacket), byte(back.FrameSizeMS)}
	if got := hex.EncodeToString(raw); got != "803e013c" {
		t.Errorf("the header is %s and the specification says 803e013c", got)
	}
}

// TestFramesPerPacketIsOneOrTwo, which the specification states outright.
func TestFramesPerPacketIsOneOrTwo(t *testing.T) {
	for _, n := range []int{0, 3, 4, 255} {
		h := CodecHeader{SampleRate: 16000, FramesPerPacket: n, FrameSizeMS: 60}
		if err := h.Validate(); err == nil {
			t.Errorf("%d frames per packet was accepted; the specification "+
				"permits 1 or 2", n)
		}
	}
	for _, n := range []int{1, 2} {
		h := CodecHeader{SampleRate: 16000, FramesPerPacket: n, FrameSizeMS: 60}
		if err := h.Validate(); err != nil {
			t.Errorf("%d frames per packet was refused: %v", n, err)
		}
	}

	// And the other two fields have their own limits: the rate is 16 bits and
	// the duration is one byte between 2.5 and 60 ms.
	for _, h := range []CodecHeader{
		{SampleRate: 0, FramesPerPacket: 1, FrameSizeMS: 60},
		{SampleRate: 70000, FramesPerPacket: 1, FrameSizeMS: 60},
		{SampleRate: 16000, FramesPerPacket: 1, FrameSizeMS: 0},
		{SampleRate: 16000, FramesPerPacket: 1, FrameSizeMS: 61},
	} {
		if err := h.Validate(); err == nil {
			t.Errorf("%+v was accepted", h)
		}
	}
}

// TestAnIncomingStreamsHeaderIsReadRatherThanAssumed.
//
// `on_stream_start` carries the sender's own parameters and they need not be
// QSP's. A caller that assumed 16 kHz and received 8 would play somebody's
// audio at half speed, which sounds like a fault in the radio rather than in
// the bridge.
func TestAnIncomingStreamsHeaderIsReadRatherThanAssumed(t *testing.T) {
	// 8 kHz, one frame, 20 ms — a perfectly legal header that is not ours.
	other := CodecHeader{SampleRate: 8000, FramesPerPacket: 1, FrameSizeMS: 20}
	encoded, err := other.Encode()
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if encoded == "gD4BPA==" {
		t.Fatal("a different header encoded to ours")
	}

	back, err := DecodeCodecHeader(encoded)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if back != other {
		t.Errorf("a foreign header decoded to %+v, want %+v", back, other)
	}

	for _, bad := range []string{"", "!!!!", "gD4B", "gD4BPAAA"} {
		if _, err := DecodeCodecHeader(bad); err == nil {
			t.Errorf("%q was accepted as a codec header", bad)
		}
	}
}

// TestThePacketIdIsZeroWhenStreamingToTheServer is one of the three values
// this package would have got wrong from memory.
//
// The specification: "When streaming data to the server the `packet_id` value
// is ignored and should be filled with zeroes." A counter is the plausible
// wrong answer, and it would have worked — the server ignores it — right up
// until something else depended on it.
func TestThePacketIdIsZeroWhenStreamingToTheServer(t *testing.T) {
	opus := []byte{0x78, 0x9a, 0xbc}
	packet, err := AudioPacket(22695, opus)
	if err != nil {
		t.Fatalf("building a packet: %v", err)
	}

	// {type(8) = 0x01, stream_id(32), packet_id(32), data[]}, network byte
	// order. Stream 22695 is the specification's own example value: 0x58A7.
	if want := "01" + "000058a7" + "00000000" + "789abc"; hex.EncodeToString(packet) != want {
		t.Errorf("the packet is %s, want %s", hex.EncodeToString(packet), want)
	}
	if packet[0] != PacketTypeAudio {
		t.Errorf("the type byte is %#02x, want %#02x", packet[0], PacketTypeAudio)
	}
	if got := hex.EncodeToString(packet[5:9]); got != "00000000" {
		t.Errorf("the packet ID is %s and the specification says zeroes", got)
	}
	if PacketHeaderBytes != 9 {
		t.Errorf("the header is %d bytes; a type byte and two 32-bit fields is 9",
			PacketHeaderBytes)
	}

	if _, err := AudioPacket(1, nil); err == nil {
		t.Error("a packet with no audio was built")
	}
}

// TestAnIncomingAudioPacketIsReadAndAnImageIsRefused.
//
// The specification defines type 0x02 for images, with the same header shape.
// **Handing image bytes to an Opus decoder is the loudest available failure**,
// so anything that is not type 0x01 is refused rather than interpreted.
func TestAnIncomingAudioPacketIsReadAndAnImageIsRefused(t *testing.T) {
	audio := append([]byte{0x01, 0, 0, 0x58, 0xa7, 0, 0, 0x01, 0x2c}, 0xde, 0xad)
	streamID, packetID, opus, ok := IncomingAudio(audio)
	if !ok {
		t.Fatal("an audio packet was refused")
	}
	if streamID != 22695 {
		t.Errorf("the stream ID reads %d, want 22695", streamID)
	}
	// Incoming packets do carry a packet number, unlike outgoing ones.
	if packetID != 300 {
		t.Errorf("the packet ID reads %d, want 300", packetID)
	}
	if got := hex.EncodeToString(opus); got != "dead" {
		t.Errorf("the audio is %s, want dead", got)
	}

	for name, b := range map[string][]byte{
		"an image packet":          {0x02, 0, 0, 0x58, 0xa7, 0, 0, 0, 0x01, 0xde},
		"a header with no payload": {0x01, 0, 0, 0, 1, 0, 0, 0, 0},
		"a truncated header":       {0x01, 0, 0},
		"nothing":                  nil,
	} {
		if _, _, _, ok := IncomingAudio(b); ok {
			t.Errorf("%s was read as audio", name)
		}
	}
}

// TestLogonCarriesAnArrayOfChannels is the second value memory would have got
// wrong.
//
// `channels` is an array. A single string would be rejected as `not enough
// params`, which reads like a missing credential rather than a type error.
func TestLogonCarriesAnArrayOfChannels(t *testing.T) {
	l, err := NewLogon(1, "a.jwt.token", "sherlock", "secret",
		[]string{"Baker Street 221B", "Reichenbach Falls"})
	if err != nil {
		t.Fatalf("building a logon: %v", err)
	}

	raw, err := json.Marshal(l)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	var probe struct {
		Command  string   `json:"command"`
		Channels []string `json:"channels"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		t.Fatalf("the logon does not round trip: %v", err)
	}
	if probe.Command != "logon" {
		t.Errorf("the command is %q, want logon", probe.Command)
	}
	if len(probe.Channels) != 2 {
		t.Errorf("channels marshalled as %v, want an array of two", probe.Channels)
	}

	// The platform name is deliberate: the specification says a name
	// containing "Gateway" makes Zello's Alarms service track this client's
	// online status, and QSP is a gateway.
	if !strings.Contains(strings.ToLower(l.PlatformName), "gateway") {
		t.Errorf("the platform name is %q; including \"Gateway\" is what makes "+
			"Zello track the gateway's online status", l.PlatformName)
	}

	// A caller's slice must not be shared with the command, or a later edit
	// changes what was sent.
	channels := []string{"test"}
	l2, err := NewLogon(1, "t", "", "", channels)
	if err != nil {
		t.Fatalf("building a logon: %v", err)
	}
	channels[0] = "somewhere else"
	if l2.Channels[0] != "test" {
		t.Error("the logon shares the caller's slice; editing it afterwards " +
			"changed which channel would be joined")
	}
}

// TestALogonMissingItsCredentialsIsRefusedHere.
//
// The server's answer is `not enough params`, which reads like a malformed
// request rather than a missing password — so it is caught before the
// connection, where the message can name the field.
func TestALogonMissingItsCredentialsIsRefusedHere(t *testing.T) {
	for name, build := range map[string]func() (Logon, error){
		"no channels": func() (Logon, error) {
			return NewLogon(1, "token", "user", "pass", nil)
		},
		"a username with no password": func() (Logon, error) {
			return NewLogon(1, "token", "user", "", []string{"test"})
		},
		"neither a token nor a username": func() (Logon, error) {
			return NewLogon(1, "", "", "", []string{"test"})
		},
	} {
		if _, err := build(); err == nil {
			t.Errorf("a logon with %s was accepted", name)
		}
	}
}

// TestStartStreamSaysAudioAndOpusAndAgreesWithItsHeader.
//
// The specification permits no other type and no other codec, which is the
// fact ADR-0062 rests on. And the duration must agree with the header, or the
// server is told two different things about the same audio.
func TestStartStreamSaysAudioAndOpusAndAgreesWithItsHeader(t *testing.T) {
	s, err := NewStartStream(2, "Baker Street 221B", ZelloCodecHeader)
	if err != nil {
		t.Fatalf("building a start_stream: %v", err)
	}

	if s.Command != "start_stream" || s.Type != "audio" || s.Codec != "opus" {
		t.Errorf("the command is %+v; the specification permits only audio and opus", s)
	}
	if s.CodecHeader != "gD4BPA==" {
		t.Errorf("the header is %q, want the specification's gD4BPA==", s.CodecHeader)
	}
	if s.PacketDuration != ZelloCodecHeader.FrameSizeMS*ZelloCodecHeader.FramesPerPacket {
		t.Errorf("the duration is %d and the header declares %d ms times %d frames",
			s.PacketDuration, ZelloCodecHeader.FrameSizeMS,
			ZelloCodecHeader.FramesPerPacket)
	}

	// Two frames per packet doubles the duration, which is the case where a
	// fixed 60 would be wrong.
	two := CodecHeader{SampleRate: 16000, FramesPerPacket: 2, FrameSizeMS: 20}
	s2, err := NewStartStream(3, "test", two)
	if err != nil {
		t.Fatalf("building a start_stream: %v", err)
	}
	if s2.PacketDuration != 40 {
		t.Errorf("two 20 ms frames give a duration of %d, want 40", s2.PacketDuration)
	}

	if _, err := NewStartStream(4, "", ZelloCodecHeader); err == nil {
		t.Error("a stream with no channel was accepted")
	}
	if _, err := NewStartStream(5, "test", CodecHeader{}); err == nil {
		t.Error("a stream with an invalid header was accepted")
	}
}

// TestAResponseAndAnEventAreToldApart.
//
// A message with a `seq` answers a command; one with a `command` is an event.
// A message that is neither is refused rather than silently ignored, because a
// protocol change that QSP does not understand should be visible.
func TestAResponseAndAnEventAreToldApart(t *testing.T) {
	// The specification's own examples.
	res, ev, err := Decode([]byte(`{"seq":2,"success":true,"stream_id":22695}`))
	if err != nil {
		t.Fatalf("decoding a response: %v", err)
	}
	if ev != nil || res == nil {
		t.Fatal("a response was read as an event")
	}
	if res.Seq != 2 || !res.Success || res.StreamID != 22695 {
		t.Errorf("the response read as %+v", res)
	}

	res, ev, err = Decode([]byte(`{"command":"on_stream_start","type":"audio",` +
		`"codec":"opus","codec_header":"gD4BPA==","packet_duration":20,` +
		`"stream_id":22695,"channel":"test","from":"alex"}`))
	if err != nil {
		t.Fatalf("decoding an event: %v", err)
	}
	if res != nil || ev == nil {
		t.Fatal("an event was read as a response")
	}
	if ev.Command != EventStreamStart || ev.From != "alex" || ev.StreamID != 22695 {
		t.Errorf("the event read as %+v", ev)
	}
	if ev.CodecHeader != "gD4BPA==" {
		t.Errorf("the event's codec header is %q", ev.CodecHeader)
	}

	// A channel status, which is what must be online before a stream starts.
	_, ev, err = Decode([]byte(`{"command":"on_channel_status","channel":"test",` +
		`"status":"online","users_online":2}`))
	if err != nil {
		t.Fatalf("decoding a channel status: %v", err)
	}
	if ev.Status != StatusOnline || ev.UsersOnline != 2 {
		t.Errorf("the status read as %+v", ev)
	}

	// An error response carries a code rather than success.
	res, _, err = Decode([]byte(`{"seq":1,"error":"not authorized"}`))
	if err != nil {
		t.Fatalf("decoding an error: %v", err)
	}
	if res.Success || res.Error != ErrNotAuthorized {
		t.Errorf("the error response read as %+v", res)
	}

	for name, b := range map[string]string{
		"not JSON":                `{`,
		"neither seq nor command": `{"success":true}`,
	} {
		if _, _, err := Decode([]byte(b)); err == nil {
			t.Errorf("a message that is %s was accepted", name)
		}
	}
}

// TestBadCredentialsAreNotRetried is the classification that keeps an account
// from being locked.
//
// A client that retries `not authorized` forever hammers the service with a
// password that will never work. One that gives up on `server closed
// connection` stays down after a blip the specification explicitly says to
// reconnect from.
func TestBadCredentialsAreNotRetried(t *testing.T) {
	for _, code := range []string{
		ErrNotAuthorized, ErrListenOnly, ErrNotEnoughParams,
		ErrUnknownCommand, ErrInvalidJSON, ErrInvalidRequest, ErrChannelsLimit,
	} {
		if Retryable(code) {
			t.Errorf("%q is retryable; retrying it cannot ever succeed", code)
		}
	}
	for _, code := range []string{
		ErrServerClosed, ErrInternalServer, ErrChannelNotReady,
		ErrFailedStartStream, ErrFailedSendData, ErrInvalidAudio,
	} {
		if !Retryable(code) {
			t.Errorf("%q is not retryable; the specification says to try again "+
				"or wait for the channel", code)
		}
	}

	// Fatal is the narrower set: a configuration that can never work, so a
	// health check says so rather than reporting a permanent retry.
	for _, code := range []string{ErrNotAuthorized, ErrListenOnly, ErrChannelsLimit} {
		if !Fatal(code) {
			t.Errorf("%q is not fatal; no amount of retrying fixes it", code)
		}
	}
	for _, code := range []string{ErrServerClosed, ErrChannelNotReady, ErrInternalServer} {
		if Fatal(code) {
			t.Errorf("%q is fatal; it is a transient condition", code)
		}
	}
}

// TestTheKeepaliveDeadlineIsRecorded.
//
// The server pings every 30 seconds and terminates the connection if a Pong
// takes longer than 30 to arrive. **A library that does not answer
// automatically produces a link dropping every half minute**, which looks like
// a network fault rather than a missing Pong — so the number is in the code
// where whoever writes the session will see it.
func TestTheKeepaliveDeadlineIsRecorded(t *testing.T) {
	if PingIntervalSeconds != 30 {
		t.Errorf("the ping interval is %d and the specification says 30",
			PingIntervalSeconds)
	}
	if EndpointFriendsAndFamily != "wss://zello.io/ws" {
		t.Errorf("the consumer endpoint is %q", EndpointFriendsAndFamily)
	}
	// TLS only: the specification says the protocol supports nothing else, so
	// an endpoint that is not wss is a configuration that cannot connect.
	for _, e := range []string{EndpointFriendsAndFamily, EndpointWorkFormat, EndpointEnterpriseFormat} {
		if !strings.HasPrefix(e, "wss://") {
			t.Errorf("%q is not a TLS endpoint and the protocol supports no other", e)
		}
	}
}
