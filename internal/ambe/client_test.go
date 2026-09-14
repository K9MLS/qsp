package ambe

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeVocoder replays the exchanges captured from the operator's dongle.
//
// **It replays rather than simulates.** Every reply it gives is a recording
// from testdata/ambe/observed-exchanges.hex, keyed by the request that
// produced it, so a client that sends a packet the bench never sent gets
// nothing back rather than a plausible invention. §7's rule about no fake
// anything applies to test doubles too: a double that answers packets the
// hardware was never asked is a double that can hide a defect.
type fakeVocoder struct {
	t     *testing.T
	conn  *net.UDPConn
	reply map[string][]byte

	mu       sync.Mutex
	seen     [][]byte
	drop     map[string]bool
	override map[string][]byte

	// inFlight and overlaps detect a second request arriving before the
	// previous reply went out, which is what an unserialised client would do.
	inFlight atomic.Bool
	overlaps atomic.Uint64
	// hold is how long a reply is delayed, so that an overlap has a window to
	// happen in rather than depending on scheduling luck.
	//
	// Atomic because the read loop is already running when a test sets it —
	// which the race detector said before this comment existed.
	hold atomic.Int64
}

func newFakeVocoder(t *testing.T) *fakeVocoder {
	t.Helper()
	fx := records(t, "observed-exchanges.hex")
	key := func(name string) string { return hex.EncodeToString(fx[name]) }

	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatalf("binding a fake vocoder: %v", err)
	}
	f := &fakeVocoder{
		t:    t,
		conn: conn,
		reply: map[string][]byte{
			key("reset-request"):   fx["reset-reply"],
			key("prodid-request"):  fx["prodid-reply"],
			key("version-request"): fx["version-reply"],
			key("getcfg-request"):  fx["getcfg-reply"],
			key("ratet-request"):   fx["ratet-reply"],
			key("speech-request"):  fx["channel-reply"],
			// PKT_INIT with encoder, decoder and echo canceller: Table 48's
			// 0x07. Not in the capture — the bench run predates Acquire — so
			// this is the acknowledgement shape the manual gives, and it is
			// marked as such here rather than passed off as a recording.
			"610002000b07": {StartByte, 0x00, 0x02, TypeControl, 0x0B, 0x00},
		},
		drop:     map[string]bool{},
		override: map[string][]byte{},
	}
	go f.serve()
	t.Cleanup(func() { _ = conn.Close() })
	return f
}

// serve reads datagrams and answers each on its own goroutine.
//
// **Concurrently on purpose.** A serial read loop cannot detect an
// unserialised client at all: a second request simply waits in the socket
// buffer until the first reply has gone out, so the in-flight window is never
// observed and the test passes whatever the client does. That was the first
// version of this, and it passed with the client's mutex removed entirely.
func (f *fakeVocoder) serve() {
	buf := make([]byte, 2048)
	for {
		n, from, err := f.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		req := make([]byte, n)
		copy(req, buf[:n])
		go f.answer(req, from)
	}
}

// answer replies to one request.
func (f *fakeVocoder) answer(req []byte, from *net.UDPAddr) {
	k := hex.EncodeToString(req)

	f.mu.Lock()
	f.seen = append(f.seen, req)
	dropped := f.drop[k]
	out, overridden := f.override[k]
	f.mu.Unlock()

	if dropped {
		return
	}
	if !overridden {
		out = f.reply[k]
	}
	if out == nil {
		// A packet the bench never sent gets silence, which is what a chip
		// does with a packet it will not accept.
		return
	}

	// The chip processes one packet at a time and answers in order. A second
	// request arriving before this reply leaves is a client with two exchanges
	// in flight, and a reply carries nothing saying which request it answers —
	// so one of the two callers would read the other's frame.
	if !f.inFlight.CompareAndSwap(false, true) {
		f.overlaps.Add(1)
	}
	if d := time.Duration(f.hold.Load()); d > 0 {
		time.Sleep(d)
	}
	_, _ = f.conn.WriteToUDP(out, from)
	f.inFlight.Store(false)
}

func (f *fakeVocoder) address() string { return f.conn.LocalAddr().String() }

func (f *fakeVocoder) dropRequest(hexReq string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.drop[hexReq] = true
}

func (f *fakeVocoder) answerWith(hexReq string, reply []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.override[hexReq] = reply
}

func (f *fakeVocoder) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

func openAgainst(t *testing.T, f *fakeVocoder) *Client {
	t.Helper()
	c, err := Open(context.Background(), nil, f.address(), Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("opening a vocoder: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func tone() []int16 {
	out := make([]int16, 160)
	for i := range out {
		out[i] = int16(8000 * math.Sin(2*math.Pi*1000*float64(i)/8000))
	}
	return out
}

// TestOpenBringsTheChipToAKnownStateAndReportsIt walks the startup sequence
// against the recorded replies.
//
// Reset, product, version, configuration, rate — five exchanges, every one of
// them a packet the bench actually sent and a reply the dongle actually gave.
// What the client must end up holding is what the chip said about itself,
// because a health report that reported a configuration nobody asked for would
// be the same defect as a console describing a build that no longer existed.
func TestOpenBringsTheChipToAKnownStateAndReportsIt(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)

	if got, want := c.Product(), "AMBE3000F"; got != want {
		t.Errorf("the product is %q, want %q", got, want)
	}
	if !strings.HasPrefix(c.Version(), "V121.") {
		t.Errorf("the version is %q, want it to begin V121.", c.Version())
	}
	if got, want := c.Config(), [3]byte{0x05, 0x00, 0xec}; got != want {
		t.Errorf("the configuration is %#v, want %#v", got, want)
	}
	if c.CompandingEnabled() {
		t.Error("companding reported as enabled; CFG0 0x05 has CP_ENABLE low " +
			"and this client sends 16-bit linear samples")
	}
	if got := f.requests(); got != 5 {
		t.Errorf("the handshake sent %d packets, want 5", got)
	}
}

// TestOpenRefusesAChipWithParityEnabled is a failure that would otherwise look
// like a dead dongle.
//
// Parity is enabled by default (§6.5.5) and a chip with it enabled silently
// discards every packet lacking a valid parity field. This client sends none,
// so nothing would work and every exchange would time out. **Saying so at
// startup is the difference between a message and an afternoon** — the same
// reasoning as a refused port not printing as a refused packet.
func TestOpenRefusesAChipWithParityEnabled(t *testing.T) {
	f := newFakeVocoder(t)
	// The same configuration reply with CFG2 bit 4 set.
	f.answerWith("6100010036", []byte{StartByte, 0x00, 0x04, TypeControl, 0x36, 0x05, 0x00, 0xec | 1<<4})

	_, err := Open(context.Background(), nil, f.address(), Options{Timeout: time.Second})
	if !errors.Is(err, ErrParityEnabled) {
		t.Fatalf("opening against a parity-enabled chip gave %v, want a parity refusal", err)
	}
	if !strings.Contains(err.Error(), "dead device") {
		t.Errorf("the refusal does not say what the failure would look like: %v", err)
	}
}

// TestOpenRefusesSomethingThatIsNotAnAmbe3000 covers the wrong part answering.
//
// The manual's own advice for proving a link is to send two packets with known
// replies (§6.6.1). This is that advice as a gate: a USB-3000 or a different
// chip on the same port would answer, and its frames would not be the frames
// QSP asked for.
func TestOpenRefusesSomethingThatIsNotAnAmbe3000(t *testing.T) {
	f := newFakeVocoder(t)
	f.answerWith("6100010030", []byte{StartByte, 0x00, 0x08, TypeControl, 0x30,
		'U', 'S', 'B', '3', '0', '0', 0x00})

	_, err := Open(context.Background(), nil, f.address(), Options{Timeout: time.Second})
	if !errors.Is(err, ErrNotAnAMBE3000F) {
		t.Fatalf("opening against a different part gave %v, want a product refusal", err)
	}
	if !strings.Contains(err.Error(), "USB300") {
		t.Errorf("the refusal does not name what answered: %v", err)
	}
}

// TestOpenFailsWhenTheChipDoesNotAnswerAReset is the dead-dongle case.
//
// A timeout has to name which step failed, because "the vocoder did not
// answer" and "the vocoder answered the wrong thing" send an operator to
// different places.
func TestOpenFailsWhenTheChipDoesNotAnswerAReset(t *testing.T) {
	f := newFakeVocoder(t)
	f.dropRequest("6100010033")

	_, err := Open(context.Background(), nil, f.address(), Options{Timeout: 100 * time.Millisecond})
	if err == nil {
		t.Fatal("opening against a silent chip succeeded")
	}
	if !strings.Contains(err.Error(), "reset") {
		t.Errorf("the failure does not name the step that failed: %v", err)
	}
}

// TestEncodingAFrameReproducesTheBenchExchange is the capture, through the
// client.
//
// 160 linear samples of a 1 kHz tone in, and the channel frame the dongle
// returned back out: 72 bits, 3600 bps, nine bytes. The rate is checked from
// the frame rather than from the acknowledgement, because the acknowledgement
// only says a field arrived.
func TestEncodingAFrameReproducesTheBenchExchange(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)

	if err := c.Acquire(Holder{Talkgroup: 2, Source: 3132910, Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}
	frame, err := c.Encode(tone())
	if err != nil {
		t.Fatalf("encoding a frame: %v", err)
	}
	if frame.Bits != 72 {
		t.Errorf("the frame carries %d bits, want 72", frame.Bits)
	}
	if frame.Rate() != 3600 {
		t.Errorf("the frame is %d bps, want 3600", frame.Rate())
	}
	if got, want := hex.EncodeToString(frame.Data), "954be6500310b00777"; got != want {
		t.Errorf("the channel data is %s, want %s", got, want)
	}

	encoded, _, _, failed := c.Counters()
	if encoded != 1 || failed != 0 {
		t.Errorf("counters read encoded=%d failed=%d, want 1 and 0", encoded, failed)
	}
}

// TestAFrameWithoutTheChannelIsRefused keeps capacity honest at the entry
// point.
//
// A caller that has not acquired the channel has not been told whether anybody
// else holds it, so a frame from one is audio that may be about to interleave.
func TestAFrameWithoutTheChannelIsRefused(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)

	if _, err := c.Encode(tone()); !errors.Is(err, ErrNotHeld) {
		t.Errorf("encoding without the channel gave %v, want a not-held error", err)
	}
	if _, err := c.Decode(ChannelFrame{Bits: 72, Data: make([]byte, 9)}); !errors.Is(err, ErrNotHeld) {
		t.Errorf("decoding without the channel gave %v, want a not-held error", err)
	}
}

// TestTheSecondCallIsRefusedAndToldWhatHoldsTheChannel is BLUEPRINT §7's
// requirement.
//
// **One AMBE-3000 is one channel.** A club bridge wanting four simultaneous
// transcoded talkgroups needs four chips, and the second call has to be
// refused rather than mangled. The refusal names the holder, because a count
// cannot answer the question an operator has — which is what COLLISIONS taught
// this project on 2026-09-12.
func TestTheSecondCallIsRefusedAndToldWhatHoldsTheChannel(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)

	first := Holder{Talkgroup: 2, Source: 3132910, Reason: "zello"}
	if err := c.Acquire(first); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	err := c.Acquire(Holder{Talkgroup: 11, Source: 315544, Reason: "dmr bridge"})
	if !errors.Is(err, ErrBusy) {
		t.Fatalf("a second call gave %v, want a busy error", err)
	}
	for _, want := range []string{"zello", "3132910", "talkgroup 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not mention %q: %v", want, err)
		}
	}

	if _, _, refused, _ := c.Counters(); refused != 1 {
		t.Errorf("refusals read %d, want 1", refused)
	}

	// And the channel comes back.
	c.Release()
	if c.Holder() != nil {
		t.Error("the channel still reports a holder after release")
	}
	if err := c.Acquire(Holder{Talkgroup: 11, Reason: "dmr bridge"}); err != nil {
		t.Errorf("acquiring a released channel: %v", err)
	}
}

// TestAcquireClearsVocoderStateBetweenCalls is §4.4, which exists precisely
// for this case.
//
// The manual says to send a PKT_INIT between unrelated audio streams so that a
// new stream does not decode with state left by the last one. One chip serving
// consecutive transmissions from different radios is exactly that case, so the
// init belongs in Acquire rather than in a caller that might forget.
func TestAcquireClearsVocoderStateBetweenCalls(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)

	before := f.requests()
	if err := c.Acquire(Holder{Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}
	if got := f.requests() - before; got != 1 {
		t.Fatalf("acquiring sent %d packets, want 1 (a PKT_INIT)", got)
	}

	f.mu.Lock()
	last := f.seen[len(f.seen)-1]
	f.mu.Unlock()
	if got, want := hex.EncodeToString(last), "610002000b07"; got != want {
		t.Errorf("the init packet is %s, want %s — field 0x0b with Table 48's "+
			"0x07, encoder, decoder and echo canceller", got, want)
	}
}

// TestAFailedInitDoesNotLeaveTheChannelHeld is the leak that would take the
// vocoder out of service.
//
// If the init exchange fails the call never starts, and a channel left held by
// a call that does not exist would refuse every later call for the life of the
// process. **Anything that takes a resource must give it back on the path
// where it fails**, which is the same rule as anything a page creates it must
// be able to remove.
func TestAFailedInitDoesNotLeaveTheChannelHeld(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	f.dropRequest("610002000b07")

	err := c.Acquire(Holder{Reason: "zello"})
	if err == nil {
		t.Fatal("acquiring succeeded with the init dropped")
	}
	if c.Holder() != nil {
		t.Fatal("the channel is still held after a failed acquire; every later " +
			"call would be refused for the life of the process")
	}
	if _, _, _, failed := c.Counters(); failed != 1 {
		t.Errorf("failures read %d, want 1", failed)
	}
}

// TestAnUnexpectedReplyIsReportedRatherThanDecoded keeps a wrong answer from
// becoming audio.
//
// A speech packet answered by anything that is not a channel frame is a
// protocol surprise, and passing it on as a frame would put whatever it was
// into somebody's audio path. It is refused and the bytes are in the message.
func TestAnUnexpectedReplyIsReportedRatherThanDecoded(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	if err := c.Acquire(Holder{Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	fx := records(t, "observed-exchanges.hex")
	// Answer the speech packet with a ready packet instead.
	f.answerWith(hex.EncodeToString(fx["speech-request"]), fx["reset-reply"])

	if _, err := c.Encode(tone()); err == nil {
		t.Fatal("a ready packet was accepted as a channel frame")
	} else if !strings.Contains(err.Error(), "not a channel frame") {
		t.Errorf("the failure does not say what was wrong: %v", err)
	}
	if _, _, _, failed := c.Counters(); failed != 1 {
		t.Errorf("failures read %d, want 1", failed)
	}
}

// TestTheDecodeDirectionRoundTripsItsFraming is the mirror exchange, and it is
// marked for what it is.
//
// **The decode direction has not been proved on hardware.** The bench run
// captured speech in and channel out; this is channel in and speech out, built
// from §6.8 and §6.9. What this test proves is that the client builds a valid
// channel packet and reads a valid speech reply — not that the chip agrees,
// which needs `ambe-probe -decode` and thirty seconds at the bench.
func TestTheDecodeDirectionRoundTripsItsFraming(t *testing.T) {
	f := newFakeVocoder(t)
	c := openAgainst(t, f)
	if err := c.Acquire(Holder{Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	// The channel frame the dongle produced, sent back for decoding.
	frame := ChannelFrame{Bits: 72, Data: mustHex(t, "954be6500310b00777")}
	chand, err := Chand(frame.Bits, frame.Data)
	if err != nil {
		t.Fatalf("building CHAND: %v", err)
	}
	request, err := Build(TypeChannel, Val(0x40), chand)
	if err != nil {
		t.Fatalf("building the channel packet: %v", err)
	}

	// A speech reply carrying the tone, per Table 99.
	speech, err := SpeechD(tone())
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	reply, err := Build(TypeSpeech, speech)
	if err != nil {
		t.Fatalf("building the speech reply: %v", err)
	}
	f.answerWith(hex.EncodeToString(request), reply)

	samples, err := c.Decode(frame)
	if err != nil {
		t.Fatalf("decoding a frame: %v", err)
	}
	if len(samples) != 160 {
		t.Fatalf("decoded %d samples, want 160", len(samples))
	}
	want := tone()
	for i := range samples {
		if samples[i] != want[i] {
			t.Fatalf("sample %d decoded as %d, want %d", i, samples[i], want[i])
		}
	}
	if _, decoded, _, _ := c.Counters(); decoded != 1 {
		t.Errorf("decoded reads %d, want 1", decoded)
	}
}

// TestConcurrentEncodesDoNotInterleaveOnTheWire is why exchanges are
// serialised.
//
// Packet mode replies in the order requests arrived, and a reply carries
// nothing identifying which request it answers. So two callers in flight at
// once would each be able to read the other's frame — which is the audio
// interleaving that capacity exists to prevent, one layer down.
//
// **The first version of this test could not fail.** It ran eight goroutines
// that all sent the same tone and all expected the same frame, so a reply
// delivered to the wrong caller was invisible: removing the mutex entirely
// left it passing, and the race detector found nothing either because there is
// no shared memory involved — only a shared socket. That is the shape §8a
// keeps catching, and it was found here the same way as always, by breaking
// the code and watching the test not notice.
//
// What it does now is watch the server side. The fake marks a request in
// flight until its reply leaves and counts anything that arrives during that
// window, which is the thing an unserialised client actually does wrong.
func TestConcurrentEncodesDoNotInterleaveOnTheWire(t *testing.T) {
	f := newFakeVocoder(t)
	f.hold.Store(int64(2 * time.Millisecond))
	c := openAgainst(t, f)
	if err := c.Acquire(Holder{Reason: "zello"}); err != nil {
		t.Fatalf("acquiring the channel: %v", err)
	}

	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			frame, err := c.Encode(tone())
			if err != nil {
				errs <- err
				return
			}
			if frame.Bits != 72 {
				errs <- errors.New("a frame came back with the wrong bit count")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("a concurrent encode failed: %v", err)
	}
	if encoded, _, _, _ := c.Counters(); encoded != callers {
		t.Errorf("encoded reads %d, want %d", encoded, callers)
	}
	if got := f.overlaps.Load(); got != 0 {
		t.Errorf("%d request(s) reached the vocoder while another exchange was "+
			"in flight; replies carry nothing saying which request they answer, "+
			"so a caller would read somebody else's frame", got)
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("decoding %q: %v", s, err)
	}
	return b
}
