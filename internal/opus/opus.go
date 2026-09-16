//go:build zello

// Package opus wraps libopus for the Zello companion.
//
// # Why this is behind a build tag
//
// It is cgo, and [ADR-0062] keeps cgo out of QSP: `CGO_ENABLED=0`, the single
// static binary, cross-compilation to ARM with no C toolchain, and an install
// needing no development headers. All four survive only if nothing in the
// default build reaches this package.
//
// **So `go build ./...` and the whole gate chain must not see it.** A cgo
// package in the tree without a tag breaks `CGO_ENABLED=0 go build ./...`,
// which breaks `gofmt`, `go vet`, `staticcheck` and `go test ./...` for
// everything — a server that does not need Opus would stop compiling because
// of a connector it does not run.
//
// Build the companion with `-tags zello` and libopus present. QSP itself never
// sets that tag.
//
// # Why libopus rather than pure Go
//
// Measured, not assumed, and the measurement is in [ADR-0062]'s revisit.
// Pure-Go Opus implementations now exist and their **decoders** are sound: one
// passes all twelve RFC 8251 vectors and decoded a libopus file at the right
// level. Their **encoders** are not: driven at 8 kHz VoIP, one produced
// packets its own decoder rendered as −32768 on every sample, and repeated at
// 48 kHz its output came back from libopus at peak 32761 against an input of
// 6149. The others are decoder-only or CELT-only, which is the music mode.
//
// The decoder half could be pure Go today. Splitting one codec across two
// processes to save cgo in one direction is worse than keeping it whole, so
// both live here.
package opus

/*
#cgo pkg-config: opus
#include <opus.h>
#include <stdlib.h>

// opus_encoder_ctl is variadic and cgo cannot call a variadic C function, so
// each control needs a wrapper with a fixed signature. One per control rather
// than a generic one taking an int request: the request constants are macros
// that expand to a number and a type, and passing the wrong type through a
// generic wrapper is undefined behaviour the compiler cannot catch.
static int qsp_opus_set_bitrate(OpusEncoder *st, opus_int32 bps) {
	return opus_encoder_ctl(st, OPUS_SET_BITRATE(bps));
}

static int qsp_opus_get_bitrate(OpusEncoder *st, opus_int32 *bps) {
	return opus_encoder_ctl(st, OPUS_GET_BITRATE(bps));
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"
)

// Zello's codec parameters, from ADR-0062: the Channels API requires `opus`
// and fixes 16 kHz mono with 60 ms frames. `gD4BPA==` in their own example
// decodes to exactly that.
const (
	// SampleRate is the rate Zello declares.
	SampleRate = 16000
	// FrameMS is the packet length Zello declares.
	FrameMS = 60
	// SamplesPerFrame is one Zello packet's worth of audio, as QSP sends it.
	SamplesPerFrame = SampleRate * FrameMS / 1000
	// MaxPacketMS is the longest audio one Opus packet may carry (RFC 6716
	// §3.2.5). **What QSP receives is whatever the far side chose**, not
	// what QSP sends: the Zello app sent packets longer than one 60 ms frame
	// on the first real connection, 2026-09-16, and a decoder sized for our
	// own packets refused every one as "buffer too small".
	MaxPacketMS = 120
	// MaxSamplesPerPacket is MaxPacketMS at SampleRate.
	MaxSamplesPerPacket = SampleRate * MaxPacketMS / 1000
	// Channels is mono.
	Channels = 1
)

// maxPacket bounds an encoded packet.
//
// Opus never exceeds 1275 bytes for a single frame at any bitrate, so this is
// the codec's own limit rather than a guess. A buffer smaller than that turns
// a legitimate loud frame into a truncated packet.
const maxPacket = 1275

// Encoder turns 16 kHz mono PCM into Opus packets.
type Encoder struct {
	st *C.OpusEncoder
	// buf is reused so that a frame every 60 ms does not allocate.
	buf []byte
	// bitrate is what libopus applied, read back rather than assumed.
	bitrate int
}

// NewEncoder returns an encoder configured for voice at a bitrate in bits per
// second.
//
// **The VoIP hint is right and not load-bearing.** It biases toward SILK, the
// speech model, rather than CELT, which is for music — and DMR audio is speech
// that has already been through a speech codec once. But the hint cannot
// decide it: RFC 6716 §2 gives CELT frame sizes of 2.5 to 20 ms and SILK 10 to
// 60 ms, so **Zello's 60 ms frame can only be SILK** whatever anybody asks
// for. An earlier version of this comment claimed the hint was what mattered,
// and setting it to audio changed nothing measurable.
//
// It sharpens ADR-0062's rejection of the CELT-only pure-Go encoders: one
// cannot produce a 60 ms frame at all, so it could never serve Zello whatever
// its quality.
func NewEncoder(bitrate int) (*Encoder, error) {
	return newEncoderWithHint(bitrate, voipHint)
}

// The application hints, named so that a test can show they make no difference
// at 60 ms.
const (
	voipHint  = int(C.OPUS_APPLICATION_VOIP)
	audioHint = int(C.OPUS_APPLICATION_AUDIO)
)

func newEncoderWithHint(bitrate, hint int) (*Encoder, error) {
	var err C.int
	st := C.opus_encoder_create(C.opus_int32(SampleRate), C.int(Channels),
		C.int(hint), &err)
	if err != C.OPUS_OK || st == nil {
		return nil, fmt.Errorf("opus: cannot create an encoder: %s", errString(err))
	}

	e := &Encoder{st: st, buf: make([]byte, maxPacket)}
	// A finaliser is not a substitute for Close, and this is not one: it is
	// the backstop for a caller that drops an encoder on an error path, which
	// would otherwise leak the C allocation for the life of the process.
	runtime.SetFinalizer(e, func(e *Encoder) { e.Close() })

	if bitrate > 0 {
		if err := e.setBitrate(bitrate); err != nil {
			e.Close()
			return nil, err
		}
	}
	return e, nil
}

// setBitrate applies a bitrate.
//
// **It has to be set.** Left at its default the encoder chose 69 kbit/s for
// 8 kHz mono during the pure-Go investigation — five times what the audio
// needs, on a link somebody may be paying for by the megabyte.
func (e *Encoder) setBitrate(bps int) error {
	if r := C.qsp_opus_set_bitrate(e.st, C.opus_int32(bps)); r != C.OPUS_OK {
		return fmt.Errorf("opus: cannot set the bitrate to %d: %s", bps, errString(r))
	}
	// **Read it back.** A control that returns OPUS_OK has been accepted, not
	// necessarily applied as asked: libopus clamps a bitrate to what the mode
	// can carry, and a silently clamped rate is a stream costing more or
	// sounding worse than the configuration says.
	var got C.opus_int32
	if r := C.qsp_opus_get_bitrate(e.st, &got); r != C.OPUS_OK {
		return fmt.Errorf("opus: cannot read the bitrate back: %s", errString(r))
	}
	e.bitrate = int(got)
	return nil
}

// Bitrate reports the bitrate libopus actually applied, which may not be the
// one requested.
func (e *Encoder) Bitrate() int { return e.bitrate }

// Encode turns exactly one Zello frame of PCM into a packet.
//
// The returned slice is only valid until the next call: a frame every 60 ms
// for the length of a transmission is a lot of garbage otherwise, and the
// caller is a socket write that copies.
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if e.st == nil {
		return nil, errors.New("opus: the encoder is closed")
	}
	if len(pcm) != SamplesPerFrame {
		return nil, fmt.Errorf(
			"opus: a Zello frame is %d samples at %d Hz, got %d",
			SamplesPerFrame, SampleRate, len(pcm))
	}

	n := C.opus_encode(e.st,
		(*C.opus_int16)(unsafe.Pointer(&pcm[0])), C.int(len(pcm)),
		(*C.uchar)(unsafe.Pointer(&e.buf[0])), C.opus_int32(len(e.buf)))
	if n < 0 {
		return nil, fmt.Errorf("opus: encoding a frame: %s", errString(C.int(n)))
	}
	// A one-byte packet is Opus's "this frame is silence" encoding and is
	// legitimate, so it is not an error — but zero would be.
	if n == 0 {
		return nil, errors.New("opus: the encoder produced an empty packet")
	}
	return e.buf[:n], nil
}

// Close releases the encoder.
func (e *Encoder) Close() {
	if e.st != nil {
		C.opus_encoder_destroy(e.st)
		e.st = nil
	}
}

// Decoder turns Opus packets into 16 kHz mono PCM.
type Decoder struct {
	st  *C.OpusDecoder
	pcm []int16
}

// NewDecoder returns a decoder for Zello's parameters.
func NewDecoder() (*Decoder, error) {
	var err C.int
	st := C.opus_decoder_create(C.opus_int32(SampleRate), C.int(Channels), &err)
	if err != C.OPUS_OK || st == nil {
		return nil, fmt.Errorf("opus: cannot create a decoder: %s", errString(err))
	}
	// Sized for the longest legal packet, not for the packets QSP makes.
	d := &Decoder{st: st, pcm: make([]int16, MaxSamplesPerPacket)}
	runtime.SetFinalizer(d, func(d *Decoder) { d.Close() })
	return d, nil
}

// Decode turns one packet into PCM.
//
// **A lost packet is a real case, not an error.** Pass nil and the decoder
// conceals the gap from its own history, which is what packet-loss concealment
// is for: on a voice channel a concealed 60 ms is far less noticeable than a
// hole, and a hole is what a caller gets if it treats a loss as a failure and
// sends nothing.
func (d *Decoder) Decode(packet []byte) ([]int16, error) {
	if d.st == nil {
		return nil, errors.New("opus: the decoder is closed")
	}

	var data *C.uchar
	var length C.opus_int32
	// **For a lost packet the size passed is how much audio to invent**, not
	// room: libopus conceals exactly that many samples. So concealment asks
	// for one frame, and only a real packet is offered the whole buffer —
	// sized for the longest legal packet, because that is what the far side
	// may send. Offering loss the whole buffer turned every dropped packet
	// into 120 ms of made-up audio; TestALostPacketIsConcealedRatherThanFailing
	// caught it when the buffer grew.
	want := SamplesPerFrame
	if len(packet) > 0 {
		data = (*C.uchar)(unsafe.Pointer(&packet[0]))
		length = C.opus_int32(len(packet))
		want = len(d.pcm)
	}

	n := C.opus_decode(d.st, data, length,
		(*C.opus_int16)(unsafe.Pointer(&d.pcm[0])), C.int(want), 0)
	if n < 0 {
		return nil, fmt.Errorf("opus: decoding a packet: %s", errString(C.int(n)))
	}
	return d.pcm[:n], nil
}

// Close releases the decoder.
func (d *Decoder) Close() {
	if d.st != nil {
		C.opus_decoder_destroy(d.st)
		d.st = nil
	}
}

// Version reports the libopus build in use, for a log line at startup.
//
// Worth logging: this package's behaviour depends on a library the operator
// installed rather than one QSP shipped, and "which libopus" is the first
// question about any codec problem.
func Version() string { return C.GoString(C.opus_get_version_string()) }

// errString renders a libopus error code.
func errString(code C.int) string {
	return C.GoString(C.opus_strerror(code))
}
