//go:build zello

// Package zellobridge joins the pieces into a working path.
//
// # What it assembles
//
// Toward Zello: USRP frames of 8 kHz PCM, three at a time, resampled to 16 kHz
// and repacketised into one 60 ms block, encoded as Opus, sent on a stream.
//
// Toward the radio: an Opus packet decoded to 60 ms of 16 kHz, resampled down
// and split into three 20 ms frames, each sent as USRP.
//
// Every one of those pieces is proved on its own — `internal/audio` for the
// rate conversion and the packetisation, `internal/opus` against libopus,
// `internal/zello` for the wire protocol and the session. **This is the part
// that was missing: nothing put them in order.**
//
// # Why it is behind a build tag
//
// It reaches `internal/opus`, which is cgo, and [ADR-0062] keeps cgo out of
// QSP: `CGO_ENABLED=0`, the single static binary, the ARM cross-build, an
// install needing no development headers. A tagless file here would break
// `go build ./...` for a server that does not run a connector.
//
// # Where the 60 ms goes
//
// Two frames are held while the third arrives, so audio toward Zello is 60 ms
// late. That is unavoidable at a fixed packet size and it dominates the path —
// the resampler's filter adds under two milliseconds. It is not a defect for
// anybody to go hunting.
package zellobridge

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/opus"
	"github.com/k9mls/qsp/internal/zello"
)

// Stream is what the bridge needs from a Zello session.
//
// **An interface for the reason every other seam in this project is one**: the
// assembly has to be testable without an account, and the session it wraps
// cannot be. The Session type in `internal/zello` satisfies it.
type Stream interface {
	StartStream() (uint32, error)
	SendAudio(opus []byte) error
	StopStream() error
	Audio() <-chan zello.IncomingPacket
	Events() <-chan zello.Event
}

// Radio is what the bridge needs from the USRP side.
//
// Two methods rather than one, because a keyup is a datagram in its own right
// and the far side opens a channel on it — before any audio arrives.
type Radio interface {
	// Keyup opens a transmission toward the radio side.
	Keyup() error
	// SendFrame sends one 20 ms frame of 8 kHz audio.
	SendFrame(pcm []int16) error
	// Release closes it.
	Release() error
}

// Bridge carries audio between a radio side and a Zello session.
type Bridge struct {
	stream Stream
	radio  Radio
	log    *slog.Logger

	// bitrate is what the Opus encoder is asked for.
	bitrate int

	// toZello owns the encoder and the packer, which are one direction's
	// state and must be reset together.
	toZello struct {
		sync.Mutex
		enc     *opus.Encoder
		packer  *audio.Packer
		keyed   bool
		frames  int
		packets int
	}

	// toRadio owns the decoder and the unpacker.
	toRadio struct {
		sync.Mutex
		dec      *opus.Decoder
		unpacker *audio.Unpacker
		keyed    bool
		stream   uint32
		frames   int
	}
}

// Options configures a bridge.
type Options struct {
	// Stream is the Zello session. Required.
	Stream Stream
	// Radio is the USRP side. Required.
	Radio Radio
	// Bitrate is the Opus bitrate in bits per second. Zero selects 16000.
	//
	// **It has to be set to something.** Left to the encoder's default, the
	// investigation in ADR-0062 measured 69 kbit/s for 8 kHz mono — five
	// times what the audio needs, on a link somebody may be paying for by
	// the megabyte.
	Bitrate int
	// Log records what happened. Nil logs nothing.
	Log *slog.Logger
}

// DefaultBitrate is what a voice channel needs at 16 kHz mono.
const DefaultBitrate = 16000

// New builds a bridge.
func New(opts Options) (*Bridge, error) {
	if opts.Stream == nil {
		return nil, errors.New("zellobridge: no Zello session")
	}
	if opts.Radio == nil {
		return nil, errors.New("zellobridge: no radio side")
	}
	if opts.Bitrate <= 0 {
		opts.Bitrate = DefaultBitrate
	}

	b := &Bridge{
		stream:  opts.Stream,
		radio:   opts.Radio,
		log:     opts.Log,
		bitrate: opts.Bitrate,
	}

	enc, err := opus.NewEncoder(opts.Bitrate)
	if err != nil {
		return nil, err
	}
	dec, err := opus.NewDecoder()
	if err != nil {
		enc.Close()
		return nil, err
	}
	b.toZello.enc = enc
	b.toZello.packer = audio.NewPacker()
	b.toRadio.dec = dec
	b.toRadio.unpacker = audio.NewUnpacker()

	if opts.Log != nil {
		opts.Log.Info("zello bridge ready",
			"libopus", opus.Version(),
			"bitrate", enc.Bitrate(),
			"latency_ms", audio.ZelloFrameMS)
	}
	return b, nil
}

// Close releases the codecs.
func (b *Bridge) Close() {
	b.toZello.Lock()
	if b.toZello.enc != nil {
		b.toZello.enc.Close()
		b.toZello.enc = nil
	}
	b.toZello.Unlock()

	b.toRadio.Lock()
	if b.toRadio.dec != nil {
		b.toRadio.dec.Close()
		b.toRadio.dec = nil
	}
	b.toRadio.Unlock()
}

// RadioKeyup begins a transmission from the radio side.
//
// **The stream is opened here, not on the first frame.** Zello gives a stream
// an identifier and every packet names it, so the identifier has to exist
// before there is audio to send — and opening on the first frame would spend
// that frame's 20 ms on a round trip to the far side.
func (b *Bridge) RadioKeyup() error {
	b.toZello.Lock()
	defer b.toZello.Unlock()

	if b.toZello.keyed {
		return nil // already transmitting; a repeated keyup is not an error
	}

	// A new transmission gets a clear filter and an empty block. Resetting
	// one and not the other leaves the previous caller's tail at the front of
	// this caller's audio.
	b.toZello.packer.Reset()
	b.toZello.frames = 0
	b.toZello.packets = 0

	if _, err := b.stream.StartStream(); err != nil {
		return fmt.Errorf("zellobridge: cannot open a Zello stream: %w", err)
	}
	b.toZello.keyed = true
	return nil
}

// RadioFrame takes one 20 ms frame of 8 kHz audio from the radio side.
//
// It sends a packet only when three have arrived, which is where the 60 ms
// comes from.
func (b *Bridge) RadioFrame(pcm []int16) error {
	b.toZello.Lock()
	defer b.toZello.Unlock()

	if !b.toZello.keyed {
		return errors.New(
			"zellobridge: no transmission is open, so this frame has nowhere to go")
	}
	b.toZello.frames++

	block, err := b.toZello.packer.Add(pcm)
	if err != nil {
		return err
	}
	if block == nil {
		return nil
	}
	return b.sendBlock(block)
}

// sendBlock encodes and sends one 60 ms block. The caller holds the lock.
func (b *Bridge) sendBlock(block []int16) error {
	if b.toZello.enc == nil {
		return errors.New("zellobridge: the encoder is closed")
	}
	packet, err := b.toZello.enc.Encode(block)
	if err != nil {
		return fmt.Errorf("zellobridge: cannot encode a block: %w", err)
	}
	if err := b.stream.SendAudio(packet); err != nil {
		return fmt.Errorf("zellobridge: cannot send a packet: %w", err)
	}
	b.toZello.packets++
	return nil
}

// RadioRelease ends a transmission from the radio side.
//
// **The partial block is flushed before the stream closes.** A transmission is
// rarely a multiple of three frames, and dropping the remainder clips the last
// word of every single call — audible on every transmission and hard to
// attribute to a buffer.
//
// The stream is closed even if the flush fails, because a stream left open
// holds the channel against everybody else until the server times it out.
func (b *Bridge) RadioRelease() error {
	b.toZello.Lock()
	defer b.toZello.Unlock()

	if !b.toZello.keyed {
		return nil
	}
	b.toZello.keyed = false

	var flushErr error
	if tail := b.toZello.packer.Flush(); tail != nil {
		flushErr = b.sendBlock(tail)
	}

	if b.log != nil {
		b.log.Info("transmission to Zello ended",
			"frames", b.toZello.frames, "packets", b.toZello.packets)
	}

	if err := b.stream.StopStream(); err != nil {
		if flushErr != nil {
			return fmt.Errorf("zellobridge: the last block was not sent (%v) and "+
				"the stream was not closed: %w", flushErr, err)
		}
		return fmt.Errorf("zellobridge: cannot close the Zello stream: %w", err)
	}
	return flushErr
}

// ZelloPacket takes one incoming Opus packet and sends the audio to the radio.
//
// **A keyup is sent before the first frame**, because the radio side opens a
// channel on it and audio arriving first has nowhere to be played.
func (b *Bridge) ZelloPacket(p zello.IncomingPacket) error {
	b.toRadio.Lock()
	defer b.toRadio.Unlock()

	if b.toRadio.dec == nil {
		return errors.New("zellobridge: the decoder is closed")
	}

	// A new stream identifier is a new transmission. **Detected here rather
	// than relying on on_stream_start**, because a bridge that missed that
	// event — a dropped message, a reconnection mid-transmission — would
	// otherwise feed one caller's audio into another's open transmission.
	if p.StreamID != b.toRadio.stream {
		if b.toRadio.keyed {
			if err := b.finishToRadio(); err != nil && b.log != nil {
				b.log.Warn("cannot end the previous transmission", "error", err)
			}
		}
		b.toRadio.stream = p.StreamID
		b.toRadio.unpacker.Reset()
		b.toRadio.frames = 0
		if err := b.radio.Keyup(); err != nil {
			return fmt.Errorf("zellobridge: cannot key the radio side: %w", err)
		}
		b.toRadio.keyed = true
	}

	pcm, err := b.toRadio.dec.Decode(p.Opus)
	if err != nil {
		return fmt.Errorf("zellobridge: cannot decode a packet: %w", err)
	}

	for _, frame := range framesOf(b.toRadio.unpacker.Add(pcm)) {
		if err := b.radio.SendFrame(frame); err != nil {
			return fmt.Errorf("zellobridge: cannot send a frame: %w", err)
		}
		b.toRadio.frames++
	}
	return nil
}

// ZelloStreamStopped ends a transmission toward the radio.
func (b *Bridge) ZelloStreamStopped(streamID uint32) error {
	b.toRadio.Lock()
	defer b.toRadio.Unlock()

	if !b.toRadio.keyed {
		return nil
	}
	// A stop for a stream that is not the open one is somebody else's, and
	// acting on it would cut a transmission in progress.
	if streamID != 0 && streamID != b.toRadio.stream {
		return nil
	}
	return b.finishToRadio()
}

// finishToRadio flushes and releases. The caller holds the lock.
func (b *Bridge) finishToRadio() error {
	var flushErr error
	if tail := b.toRadio.unpacker.Flush(); tail != nil {
		if err := b.radio.SendFrame(tail); err != nil {
			flushErr = err
		} else {
			b.toRadio.frames++
		}
	}

	if b.log != nil {
		b.log.Info("transmission from Zello ended",
			"stream", b.toRadio.stream, "frames", b.toRadio.frames)
	}

	b.toRadio.keyed = false
	b.toRadio.stream = 0

	if err := b.radio.Release(); err != nil {
		return fmt.Errorf("zellobridge: cannot release the radio side: %w", err)
	}
	return flushErr
}

// framesOf splits a run of samples into 20 ms frames.
//
// The unpacker returns whole frames only, so this is a slicing rather than a
// decision — and it is a function so that the caller cannot accidentally send
// a partial one.
func framesOf(samples []int16) [][]int16 {
	if len(samples) == 0 {
		return nil
	}
	out := make([][]int16, 0, len(samples)/audio.SamplesPerFrame)
	for i := 0; i+audio.SamplesPerFrame <= len(samples); i += audio.SamplesPerFrame {
		out = append(out, samples[i:i+audio.SamplesPerFrame])
	}
	return out
}

// Counters reports what has crossed, for a health check.
func (b *Bridge) Counters() (toZelloFrames, toZelloPackets, toRadioFrames int) {
	b.toZello.Lock()
	toZelloFrames, toZelloPackets = b.toZello.frames, b.toZello.packets
	b.toZello.Unlock()

	b.toRadio.Lock()
	toRadioFrames = b.toRadio.frames
	b.toRadio.Unlock()
	return
}
