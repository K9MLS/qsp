package zello

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// A session on a Zello channel.
//
// # What this is and what it is not
//
// It speaks the Channels API over one connection: logon, waiting for the
// channel, opening a stream, sending audio, closing it, and reconnecting when
// the link drops. The protocol values it uses come from the specification and
// are asserted in protocol_test.go; the frames come from the WebSocket client
// in ws.go.
//
// **It has never spoken to Zello.** Everything below is tested against a
// server this package writes, which follows the specification — so what is
// proved is that the session does what the specification says, not that Zello
// does. The first real connection is the test that matters and it needs an
// account.
//
// # Three rules the specification sets and this enforces
//
// **A stream cannot start before the channel is online.** The API answers
// `channel is not ready` otherwise, and an operator seeing that would go
// looking at their channel rather than at timing. So Connect waits for
// `on_channel_status` with status `online` before returning.
//
// **A stream must be closed.** A stream left open holds the channel against
// everybody else until the server times it out — the same failure the routing
// core's abandoned-transmission sweep exists for, two boundaries out.
//
// **Bad credentials are not retried.** Retrying `not authorized` forever
// hammers the service with a password that will never work, which is how an
// account gets locked. `Fatal` names that set; everything else is worth
// another attempt.

// Options configures a session.
type Options struct {
	// Endpoint is the wss:// URL. Empty selects the consumer service.
	Endpoint string
	// AuthToken is the JWT for the Friends and Family service.
	AuthToken string
	// Username and Password authenticate a named account.
	Username string
	Password string
	// Channel is the channel to join. Required.
	//
	// **Joined manually in the app first.** The consumer API expects a channel
	// the account is already a member of; a server-side logon does not join
	// one.
	Channel string
	// Timeout bounds a connection attempt and a command's reply.
	Timeout time.Duration
	// Log records what happened. Nil logs nothing.
	Log *slog.Logger
	// Now is the clock, for tests.
	Now func() time.Time
}

// DefaultTimeout bounds a connection attempt and a command's reply.
//
// Generous, because the far side is across the internet and a channel can take
// a moment to come online — and short enough that a wedged connection is
// noticed rather than waited on.
const DefaultTimeout = 20 * time.Second

// Session is a connected channel.
type Session struct {
	opts Options
	conn *Conn

	// mu guards the sequence counter and the pending replies.
	mu      sync.Mutex
	seq     int
	pending map[int]chan Response

	// streamID is the open outgoing stream, or zero.
	streamID uint32

	// events carries what the server said, for a caller that wants incoming
	// audio. Closed when the read loop ends.
	events chan Event
	// audio carries incoming audio packets, already unwrapped.
	audio chan IncomingPacket

	closeOnce sync.Once
	readErr   error
	done      chan struct{}
}

// IncomingPacket is one audio packet from the server.
type IncomingPacket struct {
	StreamID uint32
	PacketID uint32
	Opus     []byte
}

// Connect opens a session and waits for the channel to come online.
func Connect(ctx context.Context, opts Options) (*Session, error) {
	if opts.Channel == "" {
		return nil, errors.New("zello: a session needs a channel")
	}
	if opts.Endpoint == "" {
		opts.Endpoint = EndpointFriendsAndFamily
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	conn, err := Dial(ctx, opts.Endpoint, opts.Timeout)
	if err != nil {
		return nil, err
	}

	s, err := newSession(conn, opts)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return s, nil
}

// newSession runs the handshake over an already-connected socket.
//
// Separate from Connect so the session can be tested over a pipe: the dial and
// the protocol are different problems, and joining them would make the
// protocol untestable without an account.
func newSession(conn *Conn, opts Options) (*Session, error) {
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	s := &Session{
		opts:    opts,
		conn:    conn,
		pending: map[int]chan Response{},
		events:  make(chan Event, 16),
		audio:   make(chan IncomingPacket, 64),
		done:    make(chan struct{}),
	}
	go s.read()

	if err := s.logon(); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.waitForChannel(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// logon authenticates and joins the channel.
func (s *Session) logon() error {
	seq, replies := s.nextSeq()
	cmd, err := NewLogon(seq, s.opts.AuthToken, s.opts.Username, s.opts.Password,
		[]string{s.opts.Channel})
	if err != nil {
		return err
	}
	if err := s.send(cmd); err != nil {
		return err
	}

	res, err := s.await(seq, replies)
	if err != nil {
		return err
	}
	if !res.Success {
		return &CommandError{Command: "logon", Code: res.Error}
	}
	if s.opts.Log != nil {
		// **The refresh token is not logged.** It is a credential, and a log
		// is read, exported and kept far longer than a session.
		s.opts.Log.Info("zello logged on", "channel", s.opts.Channel)
	}
	return nil
}

// waitForChannel blocks until the channel reports itself online.
//
// **Without this a stream fails with `channel is not ready`**, and an operator
// reading that would go looking at their channel rather than at timing. The
// specification is explicit that a client waits for the status.
func (s *Session) waitForChannel() error {
	deadline := time.NewTimer(s.opts.Timeout)
	defer deadline.Stop()

	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				return s.readFailure()
			}
			if ev.Command != EventChannelStatus {
				continue
			}
			switch ev.Status {
			case StatusOnline:
				if s.opts.Log != nil {
					s.opts.Log.Info("zello channel online",
						"channel", ev.Channel, "users_online", ev.UsersOnline)
				}
				return nil
			case StatusOffline:
				return fmt.Errorf(
					"zello: the channel %q reports itself offline%s",
					ev.Channel, reasonSuffix(ev.Error))
			}
		case <-deadline.C:
			return fmt.Errorf(
				"zello: the channel %q did not come online within %s; a stream "+
					"started now would be refused with %q",
				s.opts.Channel, s.opts.Timeout, ErrChannelNotReady)
		case <-s.done:
			return s.readFailure()
		}
	}
}

// reasonSuffix appends a server-supplied reason when there is one.
func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// CommandError is a command the server refused.
//
// It carries the code so a caller can ask whether to retry rather than
// matching strings — `Retryable` and `Fatal` are the two questions worth
// asking.
type CommandError struct {
	Command string
	Code    string
}

func (e *CommandError) Error() string {
	return fmt.Sprintf("zello: the server refused %s: %s", e.Command, e.Code)
}

// Retryable reports whether another attempt could succeed.
func (e *CommandError) Retryable() bool { return Retryable(e.Code) }

// Fatal reports whether this configuration can never work.
func (e *CommandError) Fatal() bool { return Fatal(e.Code) }

// StartStream opens an outgoing voice message and returns its stream ID.
//
// **One at a time.** The API gives a stream an ID and expects its packets and
// its stop to name it, so a second stream opened while one is live would leave
// two IDs and one caller — and audio would be attributed to whichever was
// remembered.
func (s *Session) StartStream() (uint32, error) {
	s.mu.Lock()
	open := s.streamID
	s.mu.Unlock()
	if open != 0 {
		return 0, fmt.Errorf(
			"zello: stream %d is still open; close it before starting another, or "+
				"its audio and this one's cannot be told apart", open)
	}

	seq, replies := s.nextSeq()
	cmd, err := NewStartStream(seq, s.opts.Channel, ZelloCodecHeader)
	if err != nil {
		return 0, err
	}
	if err := s.send(cmd); err != nil {
		return 0, err
	}
	res, err := s.await(seq, replies)
	if err != nil {
		return 0, err
	}
	if !res.Success {
		return 0, &CommandError{Command: "start_stream", Code: res.Error}
	}
	if res.StreamID == 0 {
		return 0, errors.New(
			"zello: the server accepted a stream and gave it no identifier, so " +
				"nothing could name its packets")
	}

	s.mu.Lock()
	s.streamID = res.StreamID
	s.mu.Unlock()
	return res.StreamID, nil
}

// SendAudio sends one Opus packet on the open stream.
func (s *Session) SendAudio(opus []byte) error {
	s.mu.Lock()
	id := s.streamID
	s.mu.Unlock()
	if id == 0 {
		return errors.New("zello: no stream is open, so this audio has nowhere to go")
	}

	packet, err := AudioPacket(id, opus)
	if err != nil {
		return err
	}
	return s.conn.WriteMessage(KindBinary, packet)
}

// StopStream closes the open stream.
//
// **Safe to call when nothing is open**, because the caller that must always
// reach it is a deferred one on an error path, and an error there about there
// being nothing to close would hide the error that mattered.
func (s *Session) StopStream() error {
	s.mu.Lock()
	id := s.streamID
	s.streamID = 0
	s.mu.Unlock()
	if id == 0 {
		return nil
	}

	seq, replies := s.nextSeq()
	if err := s.send(NewStopStream(seq, s.opts.Channel, id)); err != nil {
		return err
	}
	res, err := s.await(seq, replies)
	if err != nil {
		return err
	}
	if !res.Success {
		return &CommandError{Command: "stop_stream", Code: res.Error}
	}
	return nil
}

// Audio returns the channel incoming packets arrive on.
func (s *Session) Audio() <-chan IncomingPacket { return s.audio }

// Events returns the channel server events arrive on.
func (s *Session) Events() <-chan Event { return s.events }

// nextSeq allocates a sequence number and the channel its reply will arrive
// on.
func (s *Session) nextSeq() (int, chan Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	ch := make(chan Response, 1)
	s.pending[s.seq] = ch
	return s.seq, ch
}

// send marshals and writes a command.
func (s *Session) send(cmd any) error {
	body, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("zello: cannot encode a command: %w", err)
	}
	return s.conn.WriteMessage(KindText, body)
}

// await waits for the reply to one command.
//
// **The pending entry is removed on every path.** A command that timed out and
// left its channel in the map would leak one entry per attempt, and a session
// that reconnects on a flapping link attempts a great many.
func (s *Session) await(seq int, replies chan Response) (Response, error) {
	defer func() {
		s.mu.Lock()
		delete(s.pending, seq)
		s.mu.Unlock()
	}()

	timer := time.NewTimer(s.opts.Timeout)
	defer timer.Stop()

	select {
	case res := <-replies:
		return res, nil
	case <-timer.C:
		return Response{}, fmt.Errorf(
			"zello: no reply to command %d within %s", seq, s.opts.Timeout)
	case <-s.done:
		return Response{}, s.readFailure()
	}
}

// read is the one goroutine that reads the connection.
//
// One reader, because a WebSocket message is not divisible: two readers would
// each get half of some messages. Responses are matched by sequence number and
// everything else is published.
func (s *Session) read() {
	defer close(s.done)
	defer close(s.events)
	defer close(s.audio)

	for {
		kind, payload, err := s.conn.ReadMessage()
		if err != nil {
			s.mu.Lock()
			s.readErr = err
			s.mu.Unlock()
			return
		}

		if kind == KindBinary {
			streamID, packetID, opus, ok := IncomingAudio(payload)
			if !ok {
				// A binary message this build does not read. Not fatal: the
				// specification has an image type, and a session that died on
				// somebody sending a photograph would be a session that died
				// on a photograph.
				if s.opts.Log != nil {
					s.opts.Log.Debug("zello sent a binary message this build does not read",
						"bytes", len(payload))
				}
				continue
			}
			select {
			case s.audio <- IncomingPacket{StreamID: streamID, PacketID: packetID, Opus: opus}:
			default:
				// **Dropped rather than blocking the reader.** A full audio
				// buffer means the consumer is behind, and blocking here
				// would stop pings being answered — which drops the
				// connection thirty seconds later for a reason that looks
				// nothing like a slow consumer.
				if s.opts.Log != nil {
					s.opts.Log.Warn("zello audio dropped; the consumer is behind",
						"stream", streamID)
				}
			}
			continue
		}

		res, ev, err := Decode(payload)
		if err != nil {
			if s.opts.Log != nil {
				s.opts.Log.Warn("zello sent a message this build could not read",
					"error", err)
			}
			continue
		}

		switch {
		case res != nil:
			s.mu.Lock()
			ch, waiting := s.pending[res.Seq]
			s.mu.Unlock()
			if !waiting {
				// A reply to a command nobody is waiting for — a timed-out
				// one, most likely. Logged rather than dropped silently,
				// because it means a timeout was too short.
				if s.opts.Log != nil {
					s.opts.Log.Warn("zello replied to a command that had already timed out",
						"seq", res.Seq)
				}
				continue
			}
			ch <- *res
		case ev != nil:
			if ev.Command == EventError && s.opts.Log != nil {
				s.opts.Log.Warn("zello reported an error", "error", ev.Error)
			}
			select {
			case s.events <- *ev:
			default:
				if s.opts.Log != nil {
					s.opts.Log.Warn("zello event dropped; the consumer is behind",
						"command", ev.Command)
				}
			}
		}
	}
}

// readFailure reports why the read loop stopped.
func (s *Session) readFailure() error {
	s.mu.Lock()
	err := s.readErr
	s.mu.Unlock()
	if err == nil {
		return ErrClosed
	}
	return err
}

// Close stops the session, closing an open stream first.
//
// **The stream before the connection.** Dropping the socket with a stream open
// leaves the channel held against everybody else until the server times it
// out, which on a voice channel is a channel nobody else can use.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		_ = s.StopStream()
		_ = s.conn.Close()
	})
	return nil
}

// Done returns a channel closed when the session's reader stops.
func (s *Session) Done() <-chan struct{} { return s.done }
