package zello

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// The session, against a server this package writes from the specification.
//
// **What is proved is that the session does what the specification says, not
// that Zello does.** The first real connection is the test that matters and it
// needs an account — so this file is built to make that connection a
// confirmation rather than a debugging session.

// fakeZello is the server side, speaking the Channels API.
//
// It frames replies with serverFrame, which is built by hand from RFC 6455's
// layout rather than from this package's encoder — so a framing mistake cannot
// be consistent across both sides and pass.
type fakeZello struct {
	t    *testing.T
	conn net.Conn

	// received records the commands the client sent, for assertions about
	// order and content.
	received []map[string]any
}

// newFakeZello returns a client connection and the server side of it.
//
// **A loopback socket rather than net.Pipe.** A pipe is unbuffered, so the
// courtesy close frame a session sends on shutdown has nowhere to go and waits
// out its deadline — which added two seconds to every test in this file. A
// real socket has kernel buffers, like the connection this code will actually
// run over, so a small frame nobody reads does not block.
func newFakeZello(t *testing.T) (*Conn, *fakeZello) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()

	clientSide, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dialling: %v", err)
	}
	t.Cleanup(func() { clientSide.Close() })

	serverSide, ok := <-accepted
	if !ok {
		t.Fatal("the server side was never accepted")
	}
	t.Cleanup(func() { serverSide.Close() })

	return NewConn(clientSide), &fakeZello{t: t, conn: serverSide}
}

// readCommand reads one masked client frame and decodes its JSON.
func (f *fakeZello) readCommand() map[string]any {
	f.t.Helper()
	_ = f.conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	head := make([]byte, 2)
	if _, err := readFull(f.conn, head); err != nil {
		f.t.Fatalf("reading a frame header: %v", err)
	}
	length := int(head[1] & 0x7F)
	switch length {
	case 126:
		ext := make([]byte, 2)
		if _, err := readFull(f.conn, ext); err != nil {
			f.t.Fatalf("reading an extended length: %v", err)
		}
		length = int(binary.BigEndian.Uint16(ext))
	case 127:
		f.t.Fatal("a command larger than 64 KiB")
	}
	mask := make([]byte, 4)
	if _, err := readFull(f.conn, mask); err != nil {
		f.t.Fatalf("reading a mask: %v", err)
	}
	body := make([]byte, length)
	if length > 0 {
		if _, err := readFull(f.conn, body); err != nil {
			f.t.Fatalf("reading a payload: %v", err)
		}
	}
	for i := range body {
		body[i] ^= mask[i%4]
	}

	if head[0]&0x0F == opBinary {
		// An audio packet, recorded as such.
		f.received = append(f.received, map[string]any{
			"binary": base64.StdEncoding.EncodeToString(body),
		})
		return f.received[len(f.received)-1]
	}

	var cmd map[string]any
	if err := json.Unmarshal(body, &cmd); err != nil {
		f.t.Fatalf("a command is not JSON: %v (%q)", err, body)
	}
	f.received = append(f.received, cmd)
	return cmd
}

// reply sends a response to a sequence number.
func (f *fakeZello) reply(seq int, body map[string]any) {
	f.t.Helper()
	body["seq"] = seq
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Fatalf("marshalling a reply: %v", err)
	}
	if _, err := f.conn.Write(serverFrame(true, opText, raw)); err != nil {
		f.t.Fatalf("writing a reply: %v", err)
	}
}

// event sends an unprompted message.
func (f *fakeZello) event(body map[string]any) {
	f.t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		f.t.Fatalf("marshalling an event: %v", err)
	}
	if _, err := f.conn.Write(serverFrame(true, opText, raw)); err != nil {
		f.t.Fatalf("writing an event: %v", err)
	}
}

// serveLogon answers a logon and brings the channel online.
func (f *fakeZello) serveLogon() {
	cmd := f.readCommand()
	if cmd["command"] != "logon" {
		f.t.Errorf("the first command is %v, want logon", cmd["command"])
	}
	seq, _ := cmd["seq"].(float64)
	f.reply(int(seq), map[string]any{"success": true, "refresh_token": "a-token"})
	f.event(map[string]any{
		"command": "on_channel_status", "channel": "test",
		"status": "online", "users_online": 3,
	})
}

func sessionOptions() Options {
	return Options{
		AuthToken: "a.jwt.token",
		Channel:   "test",
		Timeout:   3 * time.Second,
	}
}

// TestASessionLogsOnAndWaitsForTheChannel is the handshake in order.
//
// **The wait is not optional.** Without it a stream fails with `channel is not
// ready`, and an operator reading that would go looking at their channel
// rather than at timing. The specification is explicit that a client waits for
// the status.
func TestASessionLogsOnAndWaitsForTheChannel(t *testing.T) {
	conn, fake := newFakeZello(t)

	done := make(chan *Session, 1)
	errs := make(chan error, 1)
	go func() {
		s, err := newSession(conn, sessionOptions())
		if err != nil {
			errs <- err
			return
		}
		done <- s
	}()

	fake.serveLogon()

	select {
	case err := <-errs:
		t.Fatalf("connecting: %v", err)
	case s := <-done:
		defer s.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("the session did not come up")
	}

	// The logon carried the channel as an array and a gateway platform name.
	logon := fake.received[0]
	channels, ok := logon["channels"].([]any)
	if !ok || len(channels) != 1 || channels[0] != "test" {
		t.Errorf("the logon named channels %v", logon["channels"])
	}
	name, _ := logon["platform_name"].(string)
	if !strings.Contains(strings.ToLower(name), "gateway") {
		t.Errorf("the platform name is %q; including \"Gateway\" is what makes "+
			"Zello track a gateway's online status", name)
	}
}

// TestASessionDoesNotComeUpUntilTheChannelIsOnline.
//
// A logon that succeeds is not a channel that is ready, and returning a
// session at that point would hand a caller something whose first stream is
// refused.
func TestASessionDoesNotComeUpUntilTheChannelIsOnline(t *testing.T) {
	conn, fake := newFakeZello(t)
	opts := sessionOptions()
	opts.Timeout = 300 * time.Millisecond

	errs := make(chan error, 1)
	go func() {
		s, err := newSession(conn, opts)
		if s != nil {
			s.Close()
		}
		errs <- err
	}()

	// Logon succeeds and the channel never reports itself.
	cmd := fake.readCommand()
	seq, _ := cmd["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true})

	select {
	case err := <-errs:
		if err == nil {
			t.Fatal("a session came up with the channel silent")
		}
		if !strings.Contains(err.Error(), ErrChannelNotReady) {
			t.Errorf("the failure does not say what a stream would be refused "+
				"with: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the session neither came up nor gave up")
	}
}

// TestBadCredentialsAreReportedAsFatal.
//
// Retrying `not authorized` forever hammers the service with a password that
// will never work, which is how an account gets locked — so the error a caller
// receives has to be able to say so.
func TestBadCredentialsAreReportedAsFatal(t *testing.T) {
	conn, fake := newFakeZello(t)

	errs := make(chan error, 1)
	go func() {
		s, err := newSession(conn, sessionOptions())
		if s != nil {
			s.Close()
		}
		errs <- err
	}()

	cmd := fake.readCommand()
	seq, _ := cmd["seq"].(float64)
	fake.reply(int(seq), map[string]any{"error": ErrNotAuthorized})

	err := <-errs
	if err == nil {
		t.Fatal("a refused logon produced a session")
	}
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("the error is %T, want a CommandError a caller can classify", err)
	}
	if !cmdErr.Fatal() {
		t.Errorf("%q is not reported as fatal; retrying it can never succeed",
			cmdErr.Code)
	}
	if cmdErr.Retryable() {
		t.Errorf("%q is reported as retryable", cmdErr.Code)
	}
}

// TestATransmissionIsStartStreamPacketsStopStream.
//
// And the packets carry the stream ID the server gave, because nothing else
// identifies them — a packet naming the wrong stream is audio attributed to
// somebody else's transmission.
func TestATransmissionIsStartStreamPacketsStopStream(t *testing.T) {
	conn, fake := newFakeZello(t)

	sessions := make(chan *Session, 1)
	go func() {
		s, err := newSession(conn, sessionOptions())
		if err != nil {
			t.Errorf("connecting: %v", err)
			close(sessions)
			return
		}
		sessions <- s
	}()
	fake.serveLogon()
	s := <-sessions
	if s == nil {
		t.Fatal("no session")
	}
	defer s.Close()

	const assigned = 22695

	streams := make(chan uint32, 1)
	go func() {
		id, err := s.StartStream()
		if err != nil {
			t.Errorf("starting a stream: %v", err)
			close(streams)
			return
		}
		streams <- id
	}()

	cmd := fake.readCommand()
	if cmd["command"] != "start_stream" {
		t.Fatalf("the command is %v, want start_stream", cmd["command"])
	}
	if cmd["codec"] != "opus" || cmd["type"] != "audio" {
		t.Errorf("the stream declares codec %v and type %v", cmd["codec"], cmd["type"])
	}
	if cmd["codec_header"] != "gD4BPA==" {
		t.Errorf("the codec header is %v, want the specification's gD4BPA==",
			cmd["codec_header"])
	}
	seq, _ := cmd["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true, "stream_id": assigned})

	id := <-streams
	if id != assigned {
		t.Fatalf("the stream ID is %d, want %d", id, assigned)
	}

	// Audio, carrying that ID and a zeroed packet ID.
	sent := make(chan error, 1)
	go func() { sent <- s.SendAudio([]byte{0x78, 0x9a}) }()
	packet := fake.readCommand()
	if err := <-sent; err != nil {
		t.Fatalf("sending audio: %v", err)
	}
	raw, err := base64.StdEncoding.DecodeString(packet["binary"].(string))
	if err != nil {
		t.Fatalf("decoding the audio packet: %v", err)
	}
	if raw[0] != PacketTypeAudio {
		t.Errorf("the packet type is %#02x, want %#02x", raw[0], PacketTypeAudio)
	}
	if got := binary.BigEndian.Uint32(raw[1:5]); got != assigned {
		t.Errorf("the packet names stream %d, want %d", got, assigned)
	}
	if got := binary.BigEndian.Uint32(raw[5:9]); got != 0 {
		t.Errorf("the packet ID is %d and the specification says zeroes", got)
	}

	// And the stop names the stream. **Waited for rather than left running**:
	// a goroutine still in await when the test returns reads a socket the
	// cleanup has closed, and reports that as the failure instead of whatever
	// was actually wrong.
	stopped := make(chan error, 1)
	go func() { stopped <- s.StopStream() }()

	stop := fake.readCommand()
	if stop["command"] != "stop_stream" {
		t.Fatalf("the command is %v, want stop_stream", stop["command"])
	}
	if got, _ := stop["stream_id"].(float64); uint32(got) != assigned {
		t.Errorf("the stop names stream %v", stop["stream_id"])
	}
	seq, _ = stop["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true})

	if err := <-stopped; err != nil {
		t.Fatalf("stopping: %v", err)
	}
}

// TestAudioWithNoStreamIsRefused, and a second stream while one is open.
//
// **Two open streams would leave two IDs and one caller**, and audio would be
// attributed to whichever was remembered. And audio with no stream has nowhere
// to go, which is better said than sent.
func TestAudioWithNoStreamIsRefused(t *testing.T) {
	conn, fake := newFakeZello(t)

	sessions := make(chan *Session, 1)
	go func() {
		s, err := newSession(conn, sessionOptions())
		if err != nil {
			t.Errorf("connecting: %v", err)
			close(sessions)
			return
		}
		sessions <- s
	}()
	fake.serveLogon()
	s := <-sessions
	if s == nil {
		t.Fatal("no session")
	}
	defer s.Close()

	if err := s.SendAudio([]byte{1, 2}); err == nil {
		t.Error("audio was sent with no stream open")
	} else if !strings.Contains(err.Error(), "nowhere to go") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	// Stopping nothing is not an error, because the caller that must always
	// reach it is a deferred one on an error path.
	if err := s.StopStream(); err != nil {
		t.Errorf("stopping with no stream open: %v", err)
	}

	// **And a second stream while one is open is refused.** Two open streams
	// leave two identifiers and one caller, and audio would be attributed to
	// whichever was remembered. Nothing tested this until removing the guard
	// passed the whole suite.
	streams := make(chan uint32, 1)
	go func() {
		id, err := s.StartStream()
		if err != nil {
			t.Errorf("starting the first stream: %v", err)
			close(streams)
			return
		}
		streams <- id
	}()
	start := fake.readCommand()
	seq, _ := start["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true, "stream_id": 101})
	if id := <-streams; id != 101 {
		t.Fatalf("the first stream is %d", id)
	}

	if _, err := s.StartStream(); err == nil {
		t.Error("a second stream was opened while the first was live; its audio " +
			"and the first one's could not be told apart")
	} else if !strings.Contains(err.Error(), "still open") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	// Closing it lets another begin, so the guard is a guard and not a
	// one-stream-per-session limit.
	stopped := make(chan error, 1)
	go func() { stopped <- s.StopStream() }()
	stop := fake.readCommand()
	seq, _ = stop["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true})
	if err := <-stopped; err != nil {
		t.Fatalf("stopping: %v", err)
	}

	again := make(chan error, 1)
	go func() {
		_, err := s.StartStream()
		again <- err
	}()
	next := fake.readCommand()
	seq, _ = next["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true, "stream_id": 102})
	if err := <-again; err != nil {
		t.Errorf("a stream after a clean stop was refused: %v", err)
	}
	stopped2 := make(chan error, 1)
	go func() { stopped2 <- s.StopStream() }()
	last := fake.readCommand()
	seq, _ = last["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true})
	<-stopped2
}

// TestIncomingAudioReachesTheCaller.
func TestIncomingAudioReachesTheCaller(t *testing.T) {
	conn, fake := newFakeZello(t)

	sessions := make(chan *Session, 1)
	go func() {
		s, err := newSession(conn, sessionOptions())
		if err != nil {
			t.Errorf("connecting: %v", err)
			close(sessions)
			return
		}
		sessions <- s
	}()
	fake.serveLogon()
	s := <-sessions
	if s == nil {
		t.Fatal("no session")
	}
	defer s.Close()

	// A stream start, then a packet, as the specification describes.
	fake.event(map[string]any{
		"command": "on_stream_start", "type": "audio", "codec": "opus",
		"codec_header": "gD4BPA==", "packet_duration": 60,
		"stream_id": 4242, "channel": "test", "from": "someone",
	})
	packet := append([]byte{PacketTypeAudio}, 0, 0, 0x10, 0x92, 0, 0, 0, 7)
	packet = append(packet, 0xde, 0xad)
	if _, err := fake.conn.Write(serverFrame(true, opBinary, packet)); err != nil {
		t.Fatalf("writing audio: %v", err)
	}

	select {
	case got := <-s.Audio():
		if got.StreamID != 4242 {
			t.Errorf("the packet names stream %d, want 4242", got.StreamID)
		}
		if got.PacketID != 7 {
			t.Errorf("the packet ID is %d, want 7 — incoming packets do carry one",
				got.PacketID)
		}
		if string(got.Opus) != "\xde\xad" {
			t.Errorf("the audio is %x", got.Opus)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no incoming audio reached the caller")
	}
}

// TestAnImageDoesNotKillTheSession.
//
// The specification has a packet type for images. **A session that died on
// somebody sending a photograph would be a session that died on a
// photograph** — so an unreadable binary message is skipped and the link
// survives.
func TestAnImageDoesNotKillTheSession(t *testing.T) {
	conn, fake := newFakeZello(t)

	sessions := make(chan *Session, 1)
	go func() {
		s, err := newSession(conn, sessionOptions())
		if err != nil {
			t.Errorf("connecting: %v", err)
			close(sessions)
			return
		}
		sessions <- s
	}()
	fake.serveLogon()
	s := <-sessions
	if s == nil {
		t.Fatal("no session")
	}
	defer s.Close()

	// An image packet, then real audio behind it.
	image := append([]byte{0x02}, 0, 0, 0, 1, 0, 0, 0, 1, 0xff)
	if _, err := fake.conn.Write(serverFrame(true, opBinary, image)); err != nil {
		t.Fatalf("writing an image: %v", err)
	}
	audio := append([]byte{PacketTypeAudio}, 0, 0, 0, 9, 0, 0, 0, 1)
	audio = append(audio, 0xbe, 0xef)
	if _, err := fake.conn.Write(serverFrame(true, opBinary, audio)); err != nil {
		t.Fatalf("writing audio: %v", err)
	}

	select {
	case got := <-s.Audio():
		if got.StreamID != 9 {
			t.Errorf("the audio after an image names stream %d", got.StreamID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("an image ended the session; audio behind it never arrived")
	}
}

// TestACommandThatIsNeverAnsweredTimesOutAndLeaksNothing.
//
// A pending reply left in the map would leak one entry per attempt, and a
// session reconnecting on a flapping link attempts a great many.
func TestACommandThatIsNeverAnsweredTimesOutAndLeaksNothing(t *testing.T) {
	conn, fake := newFakeZello(t)
	opts := sessionOptions()
	opts.Timeout = 200 * time.Millisecond

	sessions := make(chan *Session, 1)
	go func() {
		s, err := newSession(conn, opts)
		if err != nil {
			t.Errorf("connecting: %v", err)
			close(sessions)
			return
		}
		sessions <- s
	}()
	fake.serveLogon()
	s := <-sessions
	if s == nil {
		t.Fatal("no session")
	}
	defer s.Close()

	go func() { fake.readCommand() }() // read it and never reply
	if _, err := s.StartStream(); err == nil {
		t.Fatal("an unanswered command succeeded")
	}

	s.mu.Lock()
	pending := len(s.pending)
	s.mu.Unlock()
	if pending != 0 {
		t.Errorf("%d pending replies survived a timeout; a flapping link would "+
			"accumulate one per attempt", pending)
	}
}

// TestClosingDoesNotHangWhenThePeerHasStoppedReading is the regression test
// for a defect that appeared as a hung test rather than a failing one.
//
// The courtesy close frame had no deadline, so a peer that had stopped reading
// — a dead far end, a full window, a process being killed — left the write
// with nowhere to go. **A shutdown path that blocks forever is a daemon that
// will not stop**, and that is worse than a close frame nobody receives.
func TestClosingDoesNotHangWhenThePeerHasStoppedReading(t *testing.T) {
	// A pipe whose far end is never read from at all.
	clientSide, serverSide := net.Pipe()
	defer serverSide.Close()
	c := NewConn(clientSide)

	closed := make(chan struct{})
	go func() {
		_ = c.Close()
		close(closed)
	}()

	select {
	case <-closed:
	case <-time.After(CloseWriteTimeout + 3*time.Second):
		t.Fatal("Close did not return; a shutdown that can block forever is a " +
			"daemon that will not stop")
	}

	// And the bound is what makes it return, so it has to be finite and
	// short enough that nobody waits on a broken connection.
	if CloseWriteTimeout <= 0 || CloseWriteTimeout > 10*time.Second {
		t.Errorf("the close write timeout is %s", CloseWriteTimeout)
	}
}

// TestClosingStopsTheStreamBeforeTheConnection.
//
// **Dropping the socket with a stream open holds the channel** against
// everybody else until the server times it out, which on a voice channel is a
// channel nobody can use.
func TestClosingStopsTheStreamBeforeTheConnection(t *testing.T) {
	conn, fake := newFakeZello(t)

	sessions := make(chan *Session, 1)
	go func() {
		s, err := newSession(conn, sessionOptions())
		if err != nil {
			t.Errorf("connecting: %v", err)
			close(sessions)
			return
		}
		sessions <- s
	}()
	fake.serveLogon()
	s := <-sessions
	if s == nil {
		t.Fatal("no session")
	}

	streams := make(chan uint32, 1)
	go func() {
		id, err := s.StartStream()
		if err != nil {
			t.Errorf("starting: %v", err)
			close(streams)
			return
		}
		streams <- id
	}()
	start := fake.readCommand()
	seq, _ := start["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true, "stream_id": 55})
	<-streams

	// Close, and the stop must arrive before the socket goes.
	closed := make(chan struct{})
	go func() { _ = s.Close(); close(closed) }()
	stop := fake.readCommand()
	if stop["command"] != "stop_stream" {
		t.Fatalf("closing sent %v rather than stop_stream; the channel would be "+
			"held until the server timed it out", stop["command"])
	}
	if got, _ := stop["stream_id"].(float64); uint32(got) != 55 {
		t.Errorf("the stop names stream %v", stop["stream_id"])
	}

	// Close waits for the stop's reply, so answer it and let Close finish
	// rather than leaving a goroutine in await.
	seq, _ = stop["seq"].(float64)
	fake.reply(int(seq), map[string]any{"success": true})
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Error("Close did not return after the stop was answered")
	}
}
