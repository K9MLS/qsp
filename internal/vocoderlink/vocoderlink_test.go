package vocoderlink

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// fakeChip records what it is asked to decode and answers with a frame of
// samples naming the call, so a test can tell whose audio went where.
type fakeChip struct {
	mu        sync.Mutex
	rate      int
	busy      error
	failAfter int // decode fails once this many frames succeeded; 0 never
	held      bool
	acquired  int
	released  int
	decoded   [][]byte
}

func (f *fakeChip) Acquire(ambe.Holder) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.busy != nil {
		return f.busy
	}
	f.held = true
	f.acquired++
	return nil
}

func (f *fakeChip) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held = false
	f.released++
}

func (f *fakeChip) Decode(fr ambe.ChannelFrame) (ambe.SpeechReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.held {
		return ambe.SpeechReply{}, ambe.ErrNotHeld
	}
	if f.failAfter > 0 && len(f.decoded) >= f.failAfter {
		return ambe.SpeechReply{}, errors.New("the vocoder stopped answering")
	}
	f.decoded = append(f.decoded, slices.Clone(fr.Data))
	s := make([]int16, audio.SamplesPerFrame)
	s[0] = int16(len(f.decoded))
	return ambe.SpeechReply{Samples: s}, nil
}

func (f *fakeChip) Rate() int { return f.rate }

// fakeRadio records every USRP frame sent and receives nothing.
type fakeRadio struct {
	mu   sync.Mutex
	sent []audio.Frame
}

func (r *fakeRadio) Send(f audio.Frame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, f)
	return nil
}

func (r *fakeRadio) Receive(ctx context.Context) (audio.Frame, error) {
	<-ctx.Done()
	return audio.Frame{}, ctx.Err()
}

// shape reduces what the radio received to K (keyup), A (audio) and R
// (release), which is the whole contract with the far side.
func (r *fakeRadio) shape() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, 0, len(r.sent))
	for _, f := range r.sent {
		switch {
		case f.PTT && len(f.Samples) == 0:
			out = append(out, 'K')
		case f.PTT:
			out = append(out, 'A')
		default:
			out = append(out, 'R')
		}
	}
	return string(out)
}

// Three vocoder frames with distinct, asymmetric bit patterns.
func knownFrames() [][]byte {
	frames := make([][]byte, dmrfec.FramesPerBurst)
	for i := range frames {
		frames[i] = make([]byte, dmrfec.ProtectedBits)
		for b := range frames[i] {
			frames[i][b] = byte((b*(i+3) + i) % 2)
		}
	}
	return frames
}

// voiceBurst is a real burst layout: the frames either side of a sync pattern.
func voiceBurst(t *testing.T, stream hbp.StreamID) hbp.Data {
	t.Helper()
	burst, ok := dmrfec.AssembleBurst(knownFrames(), dmrfec.VoiceSyncBS)
	if !ok {
		t.Fatal("assembling a burst")
	}
	d := hbp.Data{SourceID: 3100001, TargetID: 2, Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeVoiceSync, StreamID: stream}
	copy(d.Payload[:], burst)
	return d
}

func header(stream hbp.StreamID) hbp.Data {
	return hbp.Data{SourceID: 3100001, TargetID: 2, Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
		FrameType: hbp.FrameTypeSync, DataType: hbp.DataTypeVoiceLCHeader, StreamID: stream}
}

func terminator(stream hbp.StreamID) hbp.Data {
	d := header(stream)
	d.DataType = hbp.DataTypeTerminator
	return d
}

// textBlock is a data burst that is neither a header nor a terminator.
func textBlock(stream hbp.StreamID) hbp.Data {
	d := header(stream)
	d.DataType = 0x7
	return d
}

func newChannel(t *testing.T, chip Chip, radio Radio) *Channel {
	t.Helper()
	ch, err := New(Options{Name: "dvstick", Chip: func() Chip { return chip }, Radio: radio})
	if err != nil {
		t.Fatalf("building a channel: %v", err)
	}
	return ch
}

// TestACallBecomesKeyupAudioAndRelease drives the channel frame by frame.
//
// Each row is a sequence of frames and the exact USRP shape it must produce.
// To see rows fail, break the implementation deliberately:
//   - delete the IsUserData test in handle: "a text message with no call
//     open" keys the far side up for a burst of data it cannot play
//   - delete the stream comparison: "a new stream" never sends the first R
//   - send the release after Release() in finish: nothing here catches the
//     order, which is why TestTheReleaseGoesOutBeforeTheChipIsFreed exists
//   - drop the rate check in start: "the wrong rate" keys up
func TestACallBecomesKeyupAudioAndRelease(t *testing.T) {
	v := func(s hbp.StreamID) hbp.Data { return voiceBurst(t, s) }
	tests := []struct {
		name       string
		chip       *fakeChip
		noChip     bool
		frames     []hbp.Data
		wantShape  string
		wantCalls  uint64
		wantRefuse uint64
		wantFailed uint64
		wantAband  uint64
		wantHeld   bool
	}{
		{name: "a whole call", chip: &fakeChip{rate: ambe.RateIndexDMR},
			frames:    []hbp.Data{header(1), v(1), v(1), terminator(1)},
			wantShape: "KAAAAAAR", wantCalls: 1},
		{name: "late entry with no header", chip: &fakeChip{rate: ambe.RateIndexDMR},
			frames:    []hbp.Data{v(1), terminator(1)},
			wantShape: "KAAAR", wantCalls: 1},
		{name: "a text block inside a call is not decoded", chip: &fakeChip{rate: ambe.RateIndexDMR},
			frames:    []hbp.Data{header(1), textBlock(1), v(1), terminator(1)},
			wantShape: "KAAAR", wantCalls: 1},
		{name: "a text message with no call open keys nothing", chip: &fakeChip{rate: ambe.RateIndexDMR},
			frames:    []hbp.Data{textBlock(5), textBlock(5), textBlock(5)},
			wantShape: ""},
		{name: "a new stream closes the one whose terminator was lost", chip: &fakeChip{rate: ambe.RateIndexDMR},
			frames:    []hbp.Data{header(1), v(1), header(2), v(2), terminator(2)},
			wantShape: "KAAARKAAAR", wantCalls: 2, wantAband: 1},
		{name: "a call still open holds the chip", chip: &fakeChip{rate: ambe.RateIndexDMR},
			frames:    []hbp.Data{header(1), v(1)},
			wantShape: "KAAA", wantCalls: 1, wantHeld: true},
		{name: "no vocoder reachable", noChip: true,
			frames:    []hbp.Data{header(1), v(1), terminator(1)},
			wantShape: "", wantRefuse: 1},
		{name: "the chip is already held", chip: &fakeChip{rate: ambe.RateIndexDMR, busy: ambe.ErrBusy},
			frames:    []hbp.Data{header(1), v(1), terminator(1)},
			wantShape: "", wantRefuse: 1},
		{name: "the wrong rate", chip: &fakeChip{rate: 0},
			frames:    []hbp.Data{header(1), v(1), terminator(1)},
			wantShape: "", wantRefuse: 1},
		{name: "a decode failure unkeys and absorbs the rest", chip: &fakeChip{rate: ambe.RateIndexDMR, failAfter: 4},
			frames:    []hbp.Data{header(1), v(1), v(1), v(1), terminator(1)},
			wantShape: "KAAAAR", wantCalls: 1, wantFailed: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			radio := &fakeRadio{}
			var chip Chip = tc.chip
			if tc.noChip {
				chip = nil
			}
			ch := newChannel(t, chip, radio)

			var cur *call
			now := time.Now()
			for i, f := range tc.frames {
				cur = ch.handle(cur, f, now.Add(time.Duration(i)*60*time.Millisecond))
			}

			if got := radio.shape(); got != tc.wantShape {
				t.Errorf("USRP shape %q, want %q", got, tc.wantShape)
			}
			if got := ch.calls.Load(); got != tc.wantCalls {
				t.Errorf("calls %d, want %d", got, tc.wantCalls)
			}
			if got := ch.refused.Load(); got != tc.wantRefuse {
				t.Errorf("refused %d, want %d", got, tc.wantRefuse)
			}
			if got := ch.failed.Load(); got != tc.wantFailed {
				t.Errorf("failed %d, want %d", got, tc.wantFailed)
			}
			if got := ch.abandoned.Load(); got != tc.wantAband {
				t.Errorf("abandoned %d, want %d", got, tc.wantAband)
			}
			if tc.chip != nil && tc.chip.held != tc.wantHeld {
				t.Errorf("chip held = %v at the end, want %v; a chip left held refuses "+
					"every later call", tc.chip.held, tc.wantHeld)
			}
		})
	}
}

// TestTheChipReceivesTheBurstsFramesAndNotItsSyncField is the bit layout.
//
// The sync pattern sits in the middle of a burst, so slicing the payload as
// one run puts 48 bits of sync into the audio. The chip decodes that without
// complaint and the result sounds like a bad radio.
//
// To see it bite: in handle, replace VocoderFrames with three plain 72-bit
// slices of BurstBitsFrom(payload) and this fails on the second frame.
func TestTheChipReceivesTheBurstsFramesAndNotItsSyncField(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	radio := &fakeRadio{}
	ch := newChannel(t, chip, radio)

	cur := ch.handle(nil, header(1), time.Now())
	ch.handle(cur, voiceBurst(t, 1), time.Now())

	want := knownFrames()
	if len(chip.decoded) != len(want) {
		t.Fatalf("the chip decoded %d frames, want %d", len(chip.decoded), len(want))
	}
	for i := range want {
		if !slices.Equal(chip.decoded[i], packBits(want[i])) {
			t.Errorf("frame %d reached the chip as %x, want %x", i+1, chip.decoded[i], packBits(want[i]))
		}
		if len(chip.decoded[i]) != 9 {
			t.Errorf("frame %d is %d bytes; a 72-bit frame is 9", i+1, len(chip.decoded[i]))
		}
	}
	// And the audio leaves in the order it was decoded.
	for i, f := range radio.sent[1:] {
		if f.Samples[0] != int16(i+1) || f.Talkgroup != 2 {
			t.Errorf("USRP frame %d carries decode %d on talkgroup %d", i+1, f.Samples[0], f.Talkgroup)
		}
	}
}

// orderRadio fails the test if it is sent a release after the chip is free.
type orderRadio struct {
	fakeRadio
	chip *fakeChip
	t    *testing.T
}

func (r *orderRadio) Send(f audio.Frame) error {
	if !f.PTT {
		r.chip.mu.Lock()
		held := r.chip.held
		r.chip.mu.Unlock()
		if !held {
			r.t.Error("the release went out after the chip was freed; the next call's " +
				"audio can reach a far side still keyed for this one")
		}
	}
	return r.fakeRadio.Send(f)
}

// TestTheReleaseGoesOutBeforeTheChipIsFreed covers the ordering in finish.
//
// To see it bite: swap the Send and Release calls in finish.
func TestTheReleaseGoesOutBeforeTheChipIsFreed(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	radio := &orderRadio{chip: chip, t: t}
	ch := newChannel(t, chip, radio)
	cur := ch.handle(nil, header(1), time.Now())
	cur = ch.handle(cur, voiceBurst(t, 1), time.Now())
	ch.handle(cur, terminator(1), time.Now())
	if radio.shape() != "KAAAR" {
		t.Fatalf("shape %q", radio.shape())
	}
}

// TestRunReleasesACallThatStopsWithoutATerminator is the chip welded open.
//
// A repeater that loses power mid-over sends no terminator. Without the idle
// check the chip stays held, and every later call is refused as busy until
// the process restarts.
//
// To see it bite: delete the tick case in Run and this times out.
func TestRunReleasesACallThatStopsWithoutATerminator(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	radio := &fakeRadio{}
	ch, err := New(Options{Name: "dvstick", Chip: func() Chip { return chip }, Radio: radio,
		Idle: 40 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)

	for _, f := range []hbp.Data{header(1), voiceBurst(t, 1)} {
		if err := ch.Send(f); err != nil {
			t.Fatalf("queueing: %v", err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for radio.shape() != "KAAAR" {
		if time.Now().After(deadline) {
			t.Fatalf("shape %q after two seconds; the abandoned call was never released", radio.shape())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if ch.abandoned.Load() != 1 {
		t.Errorf("abandoned %d, want 1", ch.abandoned.Load())
	}
}

// TestShutdownUnkeysACallInProgress: a far side left keyed at shutdown holds
// its Zello channel until Zello times it out.
//
// To see it bite: make the ctx.Done case in Run return without finish.
func TestShutdownUnkeysACallInProgress(t *testing.T) {
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	radio := &fakeRadio{}
	ch := newChannel(t, chip, radio)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { ch.Run(ctx); close(done) }()

	_ = ch.Send(header(1))
	_ = ch.Send(voiceBurst(t, 1))
	deadline := time.Now().Add(2 * time.Second)
	for radio.shape() != "KAAA" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	if radio.shape() != "KAAAR" {
		t.Errorf("shape %q at shutdown, want KAAAR", radio.shape())
	}
}

// TestSendNeverBlocks: Send runs on the goroutine that reads every peer.
//
// To see it bite: make Send a plain `c.queue <- frame` and this times out
// with the listener, in effect, stalled.
func TestSendNeverBlocks(t *testing.T) {
	ch := newChannel(t, &fakeChip{rate: ambe.RateIndexDMR}, &fakeRadio{})
	done := make(chan error, 1)
	go func() {
		var last error
		for range QueueDepth + 5 {
			last = ch.Send(header(1))
		}
		done <- last
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrQueueFull) {
			t.Errorf("the last Send returned %v, want ErrQueueFull", err)
		}
		if ch.dropped.Load() != 5 {
			t.Errorf("dropped %d, want 5", ch.dropped.Load())
		}
	case <-time.After(time.Second):
		t.Fatal("Send blocked on a full queue")
	}
}

// TestASupervisorWithNoOpenChannelGivesANilChip is the nil-interface trap.
//
// To see it bite: return s.ClientFor(name) directly from SupervisedChip; the
// result is a non-nil Chip holding a nil pointer and this fails.
func TestASupervisorWithNoOpenChannelGivesANilChip(t *testing.T) {
	s := ambe.NewSupervisor(nil, []ambe.Vocoder{{Name: "dvstick", Address: "127.0.0.1:1", Rate: ambe.RateIndexDMR}})
	if chip := SupervisedChip(s, "dvstick")(); chip != nil {
		t.Errorf("a supervisor that has opened nothing gave a non-nil chip %#v; the channel "+
			"would call Acquire on a nil client", chip)
	}
}

// TestASetMatchesNamesTheWayConfigurationDoes.
func TestASetMatchesNamesTheWayConfigurationDoes(t *testing.T) {
	ch := newChannel(t, &fakeChip{rate: ambe.RateIndexDMR}, &fakeRadio{})
	set := NewSet(ch)
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"dvstick", false},
		{" DVstick ", false},
		{"second", true},
	}
	for _, tc := range tests {
		if err := set.Send(tc.name, header(1)); (err != nil) != tc.wantErr {
			t.Errorf("Send(%q) error = %v, want error %v", tc.name, err, tc.wantErr)
		}
	}
}
