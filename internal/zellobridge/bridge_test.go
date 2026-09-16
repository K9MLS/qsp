//go:build zello

package zellobridge

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/opus"
	"github.com/k9mls/qsp/internal/zello"
)

// The assembly: every piece under it is proved on its own, and this is the
// part that puts them in order.

// fakeStream records what a Zello session was asked to do.
type fakeStream struct {
	mu      sync.Mutex
	started int
	stopped int
	packets [][]byte

	startErr error
	sendErr  error
	stopErr  error

	audio  chan zello.IncomingPacket
	events chan zello.Event
}

func newFakeStream() *fakeStream {
	return &fakeStream{
		audio:  make(chan zello.IncomingPacket, 8),
		events: make(chan zello.Event, 8),
	}
}

func (f *fakeStream) StartStream() (uint32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.started++
	return uint32(100 + f.started), nil
}

func (f *fakeStream) SendAudio(p []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return f.sendErr
	}
	f.packets = append(f.packets, append([]byte(nil), p...))
	return nil
}

func (f *fakeStream) StopStream() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stopErr != nil {
		return f.stopErr
	}
	f.stopped++
	return nil
}

func (f *fakeStream) Audio() <-chan zello.IncomingPacket { return f.audio }
func (f *fakeStream) Events() <-chan zello.Event         { return f.events }

func (f *fakeStream) counts() (started, stopped, packets int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.stopped, len(f.packets)
}

// fakeRadio records what the USRP side was asked to do, in order.
type fakeRadio struct {
	mu     sync.Mutex
	events []string
	frames [][]int16

	keyupErr error
	frameErr error
}

func (r *fakeRadio) Keyup() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keyupErr != nil {
		return r.keyupErr
	}
	r.events = append(r.events, "keyup")
	return nil
}

func (r *fakeRadio) SendFrame(pcm []int16) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frameErr != nil {
		return r.frameErr
	}
	r.events = append(r.events, "frame")
	r.frames = append(r.frames, append([]int16(nil), pcm...))
	return nil
}

func (r *fakeRadio) Release() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "release")
	return nil
}

func (r *fakeRadio) order() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.events, ",")
}

func bridge(t *testing.T, s Stream, r Radio) *Bridge {
	t.Helper()
	b, err := New(Options{Stream: s, Radio: r})
	if err != nil {
		t.Fatalf("building a bridge: %v", err)
	}
	t.Cleanup(b.Close)
	return b
}

// speechFrame is 20 ms of speech-shaped 8 kHz audio.
//
// Speech-shaped rather than a tone, because PROJECT_MEMORY §8r records three
// bench runs spent learning that a synthetic tone tests the wrong path of a
// speech codec.
func speechFrame(index int) []int16 {
	out := make([]int16, audio.SamplesPerFrame)
	base := index * audio.SamplesPerFrame
	for i := range out {
		t := float64(base+i) / 8000
		out[i] = int16(3000*math.Sin(2*math.Pi*140*t) +
			2000*math.Sin(2*math.Pi*700*t) +
			1200*math.Sin(2*math.Pi*1220*t))
	}
	return out
}

// TestThreeRadioFramesMakeOneZelloPacket is the ratio the boundary turns on.
func TestThreeRadioFramesMakeOneZelloPacket(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)

	if err := b.RadioKeyup(); err != nil {
		t.Fatalf("keyup: %v", err)
	}
	started, _, _ := s.counts()
	if started != 1 {
		t.Fatalf("%d streams were started on keyup, want 1", started)
	}

	for i := 0; i < 2; i++ {
		if err := b.RadioFrame(speechFrame(i)); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
		if _, _, packets := s.counts(); packets != 0 {
			t.Fatalf("frame %d produced a packet; three make one", i)
		}
	}

	if err := b.RadioFrame(speechFrame(2)); err != nil {
		t.Fatalf("frame 3: %v", err)
	}
	if _, _, packets := s.counts(); packets != 1 {
		t.Fatalf("the third frame produced %d packets, want 1", packets)
	}

	// And the packet is real Opus: it decodes back to a 60 ms block.
	dec, err := opus.NewDecoder()
	if err != nil {
		t.Fatalf("decoder: %v", err)
	}
	defer dec.Close()
	s.mu.Lock()
	first := s.packets[0]
	s.mu.Unlock()
	pcm, err := dec.Decode(first)
	if err != nil {
		t.Fatalf("the packet sent to Zello does not decode: %v", err)
	}
	if len(pcm) != opus.SamplesPerFrame {
		t.Errorf("the packet decodes to %d samples, want %d",
			len(pcm), opus.SamplesPerFrame)
	}
}

// TestTheLastPartialBlockIsSentBeforeTheStreamCloses.
//
// **A transmission is rarely a multiple of three frames**, and dropping the
// remainder clips the last word of every single call — audible on every
// transmission and hard to attribute to a buffer.
func TestTheLastPartialBlockIsSentBeforeTheStreamCloses(t *testing.T) {
	for _, frames := range []int{1, 2, 4, 5, 7} {
		s, r := newFakeStream(), &fakeRadio{}
		b := bridge(t, s, r)

		if err := b.RadioKeyup(); err != nil {
			t.Fatalf("%d frames: keyup: %v", frames, err)
		}
		for i := 0; i < frames; i++ {
			if err := b.RadioFrame(speechFrame(i)); err != nil {
				t.Fatalf("%d frames: frame %d: %v", frames, i, err)
			}
		}
		if err := b.RadioRelease(); err != nil {
			t.Fatalf("%d frames: release: %v", frames, err)
		}

		_, stopped, packets := s.counts()
		want := (frames + audio.FramesPerPacket - 1) / audio.FramesPerPacket
		if packets != want {
			t.Errorf("%d frames produced %d packets, want %d — the remainder is "+
				"the end of somebody's sentence", frames, packets, want)
		}
		if stopped != 1 {
			t.Errorf("%d frames: the stream was stopped %d times", frames, stopped)
		}
	}

	// An exact multiple sends nothing extra, so no packet of pure silence
	// arrives at the end of every clean transmission.
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)
	if err := b.RadioKeyup(); err != nil {
		t.Fatalf("keyup: %v", err)
	}
	for i := 0; i < audio.FramesPerPacket; i++ {
		if err := b.RadioFrame(speechFrame(i)); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if err := b.RadioRelease(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, _, packets := s.counts(); packets != 1 {
		t.Errorf("three frames produced %d packets, want 1 and no silent tail",
			packets)
	}
}

// TestTheStreamIsClosedEvenIfTheLastBlockCannotBeSent.
//
// **A stream left open holds the channel** against everybody else until the
// server times it out, which on a voice channel is a channel nobody can use.
// So a failure to send the tail must not skip the close.
func TestTheStreamIsClosedEvenIfTheLastBlockCannotBeSent(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)

	if err := b.RadioKeyup(); err != nil {
		t.Fatalf("keyup: %v", err)
	}
	if err := b.RadioFrame(speechFrame(0)); err != nil {
		t.Fatalf("frame: %v", err)
	}

	s.mu.Lock()
	s.sendErr = errors.New("the link went away")
	s.mu.Unlock()

	err := b.RadioRelease()
	if err == nil {
		t.Error("a release reported success although the last block was lost")
	}
	if _, stopped, _ := s.counts(); stopped != 1 {
		t.Errorf("the stream was stopped %d times; one left open holds the "+
			"channel until the server times it out", stopped)
	}
}

// TestAFrameWithNoTransmissionIsRefused, and a repeated keyup is not an error.
//
// The second matters because a radio side may re-announce a transmission it is
// already sending, and treating that as a failure would drop a call that was
// working.
func TestAFrameWithNoTransmissionIsRefused(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)

	if err := b.RadioFrame(speechFrame(0)); err == nil {
		t.Error("a frame was accepted with no transmission open")
	} else if !strings.Contains(err.Error(), "nowhere to go") {
		t.Errorf("the refusal does not say why: %v", err)
	}

	if err := b.RadioKeyup(); err != nil {
		t.Fatalf("keyup: %v", err)
	}
	if err := b.RadioKeyup(); err != nil {
		t.Errorf("a repeated keyup was refused: %v", err)
	}
	if started, _, _ := s.counts(); started != 1 {
		t.Errorf("%d streams were opened by two keyups", started)
	}

	// A release with nothing open is not an error either, because the caller
	// that must always reach it is a deferred one on an error path.
	if err := b.RadioRelease(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if err := b.RadioRelease(); err != nil {
		t.Errorf("a second release was refused: %v", err)
	}
}

// TestOnePacketFromZelloBecomesThreeRadioFramesAfterAKeyup.
//
// **The keyup comes first.** The radio side opens a channel on it, and audio
// arriving before it has nowhere to be played.
func TestOnePacketFromZelloBecomesThreeRadioFramesAfterAKeyup(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)

	// Encode a real 60 ms block so the decode is real too.
	enc, err := opus.NewEncoder(16000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	defer enc.Close()
	block := make([]int16, opus.SamplesPerFrame)
	for i := range block {
		block[i] = int16(3000 * math.Sin(2*math.Pi*700*float64(i)/opus.SampleRate))
	}
	packet, err := enc.Encode(block)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	if err := b.ZelloPacket(zello.IncomingPacket{StreamID: 77, Opus: packet}); err != nil {
		t.Fatalf("handling a packet: %v", err)
	}

	if got := r.order(); got != "keyup,frame,frame,frame" {
		t.Errorf("the radio saw %q, want a keyup then three frames", got)
	}
	r.mu.Lock()
	for i, f := range r.frames {
		if len(f) != audio.SamplesPerFrame {
			t.Errorf("frame %d is %d samples, want %d",
				i, len(f), audio.SamplesPerFrame)
		}
	}
	r.mu.Unlock()

	// A second packet on the same stream does not key up again.
	if err := b.ZelloPacket(zello.IncomingPacket{StreamID: 77, Opus: packet}); err != nil {
		t.Fatalf("handling a second packet: %v", err)
	}
	if got := r.order(); strings.Count(got, "keyup") != 1 {
		t.Errorf("the radio saw %d keyups for one stream", strings.Count(got, "keyup"))
	}

	// And the stop releases.
	if err := b.ZelloStreamStopped(77); err != nil {
		t.Fatalf("stopping: %v", err)
	}
	if !strings.HasSuffix(r.order(), "release") {
		t.Errorf("the radio was not released: %q", r.order())
	}
}

// TestANewStreamIdEndsThePreviousTransmission.
//
// **Detected from the packets rather than only from on_stream_start**, because
// a bridge that missed that event — a dropped message, a reconnection
// mid-transmission — would otherwise feed one caller's audio into another's
// open transmission.
func TestANewStreamIdEndsThePreviousTransmission(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)

	enc, err := opus.NewEncoder(16000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	defer enc.Close()
	packet, err := enc.Encode(make([]int16, opus.SamplesPerFrame))
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	if err := b.ZelloPacket(zello.IncomingPacket{StreamID: 1, Opus: packet}); err != nil {
		t.Fatalf("first stream: %v", err)
	}
	if err := b.ZelloPacket(zello.IncomingPacket{StreamID: 2, Opus: packet}); err != nil {
		t.Fatalf("second stream: %v", err)
	}

	order := r.order()
	if strings.Count(order, "keyup") != 2 {
		t.Errorf("two streams produced %d keyups: %q",
			strings.Count(order, "keyup"), order)
	}
	if strings.Count(order, "release") != 1 {
		t.Errorf("the first transmission was not released before the second "+
			"began: %q", order)
	}
	// The release must come before the second keyup, or the two callers'
	// audio was joined.
	firstRelease := strings.Index(order, "release")
	secondKeyup := strings.LastIndex(order, "keyup")
	if firstRelease > secondKeyup {
		t.Errorf("the second transmission began before the first ended: %q", order)
	}
}

// TestAStopForSomebodyElsesStreamIsIgnored.
//
// Acting on it would cut a transmission in progress, which on a channel with
// several people talking in turn is a call ended by somebody else's.
func TestAStopForSomebodyElsesStreamIsIgnored(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)

	enc, err := opus.NewEncoder(16000)
	if err != nil {
		t.Fatalf("encoder: %v", err)
	}
	defer enc.Close()
	packet, err := enc.Encode(make([]int16, opus.SamplesPerFrame))
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	if err := b.ZelloPacket(zello.IncomingPacket{StreamID: 5, Opus: packet}); err != nil {
		t.Fatalf("handling a packet: %v", err)
	}
	if err := b.ZelloStreamStopped(6); err != nil {
		t.Fatalf("stopping another stream: %v", err)
	}
	if strings.Contains(r.order(), "release") {
		t.Errorf("a stop for another stream released the radio: %q", r.order())
	}

	if err := b.ZelloStreamStopped(5); err != nil {
		t.Fatalf("stopping: %v", err)
	}
	if !strings.Contains(r.order(), "release") {
		t.Errorf("the right stop did not release: %q", r.order())
	}
}

// TestABridgeRefusesWhatItCannotBuildOn, so a misconfiguration is a startup
// error rather than a nil dereference on the first frame of audio.
func TestABridgeRefusesWhatItCannotBuildOn(t *testing.T) {
	if _, err := New(Options{Radio: &fakeRadio{}}); err == nil {
		t.Error("a bridge with no Zello session was built")
	}
	if _, err := New(Options{Stream: newFakeStream()}); err == nil {
		t.Error("a bridge with no radio side was built")
	}

	// The bitrate defaults rather than being left to the encoder's own
	// choice, which measured 69 kbit/s for 8 kHz mono in ADR-0062's
	// investigation.
	if DefaultBitrate != 16000 {
		t.Errorf("the default bitrate is %d", DefaultBitrate)
	}
	b, err := New(Options{Stream: newFakeStream(), Radio: &fakeRadio{}})
	if err != nil {
		t.Fatalf("building: %v", err)
	}
	defer b.Close()
	if b.bitrate != DefaultBitrate {
		t.Errorf("the bitrate defaulted to %d", b.bitrate)
	}
}

// TestAKeyupThatFailsDoesNotLeaveTheBridgeThinkingItIsTransmitting.
//
// A bridge that believed a transmission was open would accept frames and send
// them on a stream that does not exist, and the failure would appear on every
// frame rather than at the keyup.
func TestAKeyupThatFailsDoesNotLeaveTheBridgeThinkingItIsTransmitting(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	s.startErr = errors.New("channel is not ready")
	b := bridge(t, s, r)

	if err := b.RadioKeyup(); err == nil {
		t.Fatal("a keyup succeeded although the stream could not be opened")
	}
	if err := b.RadioFrame(speechFrame(0)); err == nil {
		t.Error("a frame was accepted after a failed keyup; it would be sent on " +
			"a stream that does not exist")
	}
}

// TestCountersReportWhatCrossed, for a health check that has to say whether
// audio is moving rather than whether a connection exists.
func TestCountersReportWhatCrossed(t *testing.T) {
	s, r := newFakeStream(), &fakeRadio{}
	b := bridge(t, s, r)

	if err := b.RadioKeyup(); err != nil {
		t.Fatalf("keyup: %v", err)
	}
	for i := 0; i < 4; i++ {
		if err := b.RadioFrame(speechFrame(i)); err != nil {
			t.Fatalf("frame %d: %v", i, err)
		}
	}
	if err := b.RadioRelease(); err != nil {
		t.Fatalf("release: %v", err)
	}

	frames, packets, _ := b.Counters()
	if frames != 4 {
		t.Errorf("%d frames counted, want 4", frames)
	}
	if packets != 2 {
		t.Errorf("%d packets counted, want 2 — one full block and one flushed",
			packets)
	}
}
