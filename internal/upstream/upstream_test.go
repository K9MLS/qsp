package upstream_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/openbridge"
	"github.com/k9mls/qsp/internal/upstream"
)

const passphrase = "agreed-with-the-far-end"

// received collects frames a link hands back, safely across goroutines.
type received struct {
	mu     sync.Mutex
	frames []hbp.Data
}

func (r *received) add(_ string, f hbp.Data) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, f)
}

func (r *received) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

func (r *received) first() hbp.Data {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frames[0]
}

// waitFor polls until a condition holds, rather than sleeping a fixed time.
//
// A fixed sleep is the flaky-test generator: it passes on a slow machine and
// fails on a fast one, or the reverse. Polling with a deadline fails only when
// the thing genuinely did not happen.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func voice(stream hbp.StreamID, tg uint32) hbp.Data {
	return hbp.Data{
		SourceID:  3132910,
		TargetID:  tg,
		Timeslot:  hbp.Timeslot2,
		CallType:  hbp.CallGroup,
		FrameType: hbp.FrameTypeVoice,
		StreamID:  stream,
	}
}

// pair builds two links pointed at each other on loopback, which is as close to
// a real far end as a test can get without one.
func pair(t *testing.T, clock func() time.Time) (a, b *upstream.Link, ra, rb *received) {
	t.Helper()

	ra, rb = &received{}, &received{}

	// Bind both to port 0 first to learn the addresses, then point each at the
	// other. Two links cannot be configured before either exists.
	first, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "a", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1",
		NetworkID: 3132910, Passphrase: []byte(passphrase),
		Receive: ra.add, Now: clock,
	})
	if err != nil {
		t.Fatalf("New a: %v", err)
	}
	if err := first.Start(context.Background()); err != nil {
		t.Fatalf("Start a: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	second, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "b", ListenAddress: "127.0.0.1:0", TargetAddress: first.Address(),
		NetworkID: 3129100, Passphrase: []byte(passphrase),
		Receive: rb.add, Now: clock,
	})
	if err != nil {
		t.Fatalf("New b: %v", err)
	}
	if err := second.Start(context.Background()); err != nil {
		t.Fatalf("Start b: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	// Now a knows where b is.
	redirected, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "a", ListenAddress: "127.0.0.1:0", TargetAddress: second.Address(),
		NetworkID: 3132910, Passphrase: []byte(passphrase),
		Receive: ra.add, Now: clock,
	})
	if err != nil {
		t.Fatalf("New a redirected: %v", err)
	}
	if err := redirected.Start(context.Background()); err != nil {
		t.Fatalf("Start a redirected: %v", err)
	}
	t.Cleanup(func() { _ = redirected.Close() })
	_ = first.Close()

	return redirected, second, ra, rb
}

// TestAFrameCrossesTheLink over a real socket.
func TestAFrameCrossesTheLink(t *testing.T) {
	a, _, _, rb := pair(t, nil)

	if err := a.Send(voice(0x1234, 3148)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	waitFor(t, "the frame to arrive", func() bool { return rb.count() == 1 })

	got := rb.first()
	if got.TargetID != 3148 {
		t.Errorf("talkgroup %d, want 3148", got.TargetID)
	}
	if got.SourceID != 3132910 {
		t.Errorf("source %d, want 3132910 — the originating radio must survive", got.SourceID)
	}
	if got.Timeslot != hbp.Timeslot1 {
		t.Errorf("arrived on %s; OpenBridge carries everything on TS1", got.Timeslot)
	}
	if got.RepeaterID != 3132910 {
		t.Errorf("repeater ID %d, want the sender's network ID 3132910", got.RepeaterID)
	}
}

// TestADatagramSignedWithTheWrongPassphraseIsRejected.
//
// This is the failure an operator actually hits, and it looks exactly like a
// dead link unless something counts it.
func TestADatagramSignedWithTheWrongPassphraseIsRejected(t *testing.T) {
	_, b, _, rb := pair(t, nil)

	wrong, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "impostor", ListenAddress: "127.0.0.1:0", TargetAddress: b.Address(),
		NetworkID: 9999999, Passphrase: []byte("a-different-secret"),
		Receive: func(string, hbp.Data) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := wrong.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = wrong.Close() }()

	if err := wrong.Send(voice(0x5678, 3148)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	waitFor(t, "the datagram to be rejected", func() bool { return b.Stats().Rejected == 1 })

	if rb.count() != 0 {
		t.Error("a frame signed with the wrong passphrase was delivered")
	}
}

// TestStatusNamesAPassphraseMismatch.
//
// Rejected datagrams and none accepted is the one case QSP can diagnose
// precisely, and it is the case that otherwise costs an evening: both ends are
// configured, both are sending, and neither hears anything.
func TestStatusNamesAPassphraseMismatch(t *testing.T) {
	_, b, _, _ := pair(t, nil)

	wrong, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "impostor", ListenAddress: "127.0.0.1:0", TargetAddress: b.Address(),
		NetworkID: 9999999, Passphrase: []byte("a-different-secret"),
		Receive: func(string, hbp.Data) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := wrong.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = wrong.Close() }()

	_ = wrong.Send(voice(1, 3148))
	waitFor(t, "the rejection", func() bool { return b.Stats().Rejected > 0 })

	st := b.Status()
	if st.EverReceived {
		t.Error("Status claims traffic has been received")
	}
	if !contains(st.Summary, "passphrase") {
		t.Errorf("the summary does not name the likely cause: %q", st.Summary)
	}
}

// TestStatusDistinguishesNeverFromNotLately.
//
// A link that has never carried traffic is usually misconfigured; one that has
// gone quiet is usually a network fault. Reporting both as "no traffic" would
// send an operator to the wrong place.
func TestStatusDistinguishesNeverFromNotLately(t *testing.T) {
	_, b, _, _ := pair(t, nil)

	st := b.Status()
	if st.EverReceived {
		t.Fatal("a link with no traffic claims to have received some")
	}
	// "has ever arrived" rather than "no traffic recently": the two send an
	// operator to different places. Never means check the far end has this
	// address; lately means check the network between them.
	if !contains(st.Summary, "ever arrived") {
		t.Errorf("the summary does not distinguish never from lately: %q", st.Summary)
	}
	if !contains(st.Summary, "address") {
		t.Errorf("the summary does not say where to look: %q", st.Summary)
	}
	if st.Stale {
		t.Error("a link that has never received anything is reported as stale; " +
			"stale means it stopped, which is a different fault")
	}
}

// TestStalenessUsesTheInjectedClock.
//
// Four hours of silence, tested in microseconds. A test that waited would never
// have been written, and the threshold would have gone unverified.
func TestStalenessUsesTheInjectedClock(t *testing.T) {
	var mu sync.Mutex
	nowVal := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return nowVal
	}
	advance := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		nowVal = nowVal.Add(d)
	}

	ra := &received{}
	sender, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "sender", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1",
		NetworkID: 1, Passphrase: []byte(passphrase), Receive: ra.add, Now: clock,
	})
	if err != nil {
		t.Fatalf("New sender: %v", err)
	}
	if err := sender.Start(context.Background()); err != nil {
		t.Fatalf("Start sender: %v", err)
	}
	defer func() { _ = sender.Close() }()

	rb := &received{}
	receiver, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "receiver", ListenAddress: "127.0.0.1:0", TargetAddress: sender.Address(),
		NetworkID: 2, Passphrase: []byte(passphrase), Receive: rb.add,
		StaleAfter: 4 * time.Hour, Now: clock,
	})
	if err != nil {
		t.Fatalf("New receiver: %v", err)
	}
	if err := receiver.Start(context.Background()); err != nil {
		t.Fatalf("Start receiver: %v", err)
	}
	defer func() { _ = receiver.Close() }()

	// Point the sender at the receiver by rebuilding it, then send one frame.
	toReceiver, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "sender", ListenAddress: "127.0.0.1:0", TargetAddress: receiver.Address(),
		NetworkID: 1, Passphrase: []byte(passphrase), Receive: ra.add, Now: clock,
	})
	if err != nil {
		t.Fatalf("New toReceiver: %v", err)
	}
	if err := toReceiver.Start(context.Background()); err != nil {
		t.Fatalf("Start toReceiver: %v", err)
	}
	defer func() { _ = toReceiver.Close() }()

	if err := toReceiver.Send(voice(0xABCD, 3148)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the frame to arrive", func() bool { return rb.count() == 1 })

	if st := receiver.Status(); st.Stale {
		t.Error("a link that just received a frame is reported stale")
	}

	advance(3 * time.Hour)
	if st := receiver.Status(); st.Stale {
		t.Errorf("stale after 3h with a 4h threshold: %q", st.Summary)
	}

	advance(2 * time.Hour) // now 5h
	st := receiver.Status()
	if !st.Stale {
		t.Fatalf("not stale after 5h with a 4h threshold: %q", st.Summary)
	}
	if !contains(st.Summary, "quiet talkgroup") {
		t.Errorf("the summary claims more than QSP knows: %q", st.Summary)
	}
}

// TestZeroStaleAfterDisablesTheWarning, for a talkgroup that is legitimately
// silent for days.
func TestZeroStaleAfterDisablesTheWarning(t *testing.T) {
	var mu sync.Mutex
	nowVal := time.Now()
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return nowVal }

	a, b, _, rb := pair(t, clock)

	if err := a.Send(voice(1, 3148)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the frame", func() bool { return rb.count() == 1 })

	mu.Lock()
	nowVal = nowVal.Add(30 * 24 * time.Hour)
	mu.Unlock()

	if st := b.Status(); st.Stale {
		t.Errorf("a link with StaleAfter unset was reported stale after a month: %q", st.Summary)
	}
}

func TestSendCountsAndTracks(t *testing.T) {
	a, _, _, rb := pair(t, nil)

	for i := range 5 {
		if err := a.Send(voice(hbp.StreamID(i+1), 3148)); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}
	waitFor(t, "all five frames", func() bool { return rb.count() == 5 })

	if got := a.Stats().Sent; got != 5 {
		t.Errorf("Sent = %d, want 5", got)
	}
}

func TestNewRefusesAnEmptyPassphrase(t *testing.T) {
	_, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "x", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1",
		NetworkID: 1, Receive: func(string, hbp.Data) {},
	})
	if err == nil {
		t.Fatal("a link with no passphrase was created")
	}
	if !contains(err.Error(), "forge") {
		t.Errorf("the error does not explain why: %v", err)
	}
}

func TestNewRefusesAZeroNetworkID(t *testing.T) {
	_, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "x", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1",
		Passphrase: []byte(passphrase), Receive: func(string, hbp.Data) {},
	})
	if err == nil {
		t.Fatal("a link with network ID 0 was created")
	}
}

func TestSendBeforeStartIsRefused(t *testing.T) {
	l, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "x", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1",
		NetworkID: 1, Passphrase: []byte(passphrase), Receive: func(string, hbp.Data) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Send(voice(1, 3148)); err == nil {
		t.Error("Send on an unopened link succeeded")
	}
}

// TestContextCancellationClosesTheLink, so a shutdown does not leak a socket.
func TestContextCancellationClosesTheLink(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	l, err := upstream.New(logging.Discard(), upstream.Config{
		Name: "x", ListenAddress: "127.0.0.1:0", TargetAddress: "127.0.0.1:1",
		NetworkID: 1, Passphrase: []byte(passphrase), Receive: func(string, hbp.Data) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	cancel()
	waitFor(t, "the link to close", func() bool { return !l.Status().Open })
}

// TestOversizedDatagramIsRejected: a sender that disagrees about the format is
// not guessed at.
func TestOversizedDatagramIsRejected(t *testing.T) {
	_, b, _, rb := pair(t, nil)

	conn, err := dial(b.Address())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.Write(make([]byte, openbridge.PacketSize+10)); err != nil {
		t.Fatalf("write: %v", err)
	}

	waitFor(t, "the rejection", func() bool { return b.Stats().Rejected == 1 })
	if rb.count() != 0 {
		t.Error("an oversized datagram was delivered")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

// dial opens a UDP socket to an address, for tests that send raw bytes.
func dial(addr string) (*net.UDPConn, error) {
	target, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	return net.DialUDP("udp", nil, target)
}

// TestSetRoutesByName.
func TestSetRoutesByName(t *testing.T) {
	a, _, _, rb := pair(t, nil)

	set := upstream.NewSet(logging.Discard())
	if err := set.Add(a); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := set.Send("a", voice(1, 3148)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	waitFor(t, "the frame", func() bool { return rb.count() == 1 })
}

// TestSetRefusesAnUnknownLink.
//
// A bridge naming a link that does not exist would otherwise appear to work
// while carrying nothing — the hardest fault to notice, because everything
// looks configured.
func TestSetRefusesAnUnknownLink(t *testing.T) {
	set := upstream.NewSet(logging.Discard())

	err := set.Send("not-configured", voice(1, 3148))
	if err == nil {
		t.Fatal("sending to an unknown link succeeded")
	}
	if !contains(err.Error(), "not-configured") {
		t.Errorf("the error does not name the missing link: %v", err)
	}
}

// TestSetRefusesDuplicateNames, because Send would otherwise reach whichever
// happened to be registered second.
func TestSetRefusesDuplicateNames(t *testing.T) {
	a, b, _, _ := pair(t, nil)

	set := upstream.NewSet(logging.Discard())
	if err := set.Add(a); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	// b is named "b", so rename by building a second set entry with a's name.
	if err := set.Add(a); err == nil {
		t.Error("two links with the same name were accepted")
	}
	_ = b
}

func TestSetReportsEveryStatus(t *testing.T) {
	a, b, _, _ := pair(t, nil)

	set := upstream.NewSet(logging.Discard())
	if err := set.Add(a); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := set.Add(b); err != nil {
		t.Fatalf("Add b: %v", err)
	}

	statuses := set.Statuses()
	if len(statuses) != 2 {
		t.Fatalf("%d statuses, want 2", len(statuses))
	}
	if statuses[0].Name != "a" || statuses[1].Name != "b" {
		t.Errorf("statuses are not in name order: %s, %s", statuses[0].Name, statuses[1].Name)
	}
}

// TestClosingTwiceIsNotAnError is why systemd recorded a failure for every
// ordinary stop.
//
// Two things close an OpenBridge link: the goroutine serve starts on the
// context, and the Set closing at shutdown. Whichever lost the race got
// "use of closed network connection", the Set returned it as its own error,
// and cmd/qsp turned that into "shutdown was not clean" and exit 1. The
// journal then carried `Failed with result 'exit-code'` for a stop that was
// entirely correct — which is the line somebody chases for an hour during a
// real fault.
//
// It was intermittent, and it is a race between two goroutines rather than on
// memory, so the race detector was never going to see it.
func TestClosingTwiceIsNotAnError(t *testing.T) {
	l, err := upstream.New(logging.Discard(), upstream.Config{
		Name:          "far",
		ListenAddress: "127.0.0.1:0",
		TargetAddress: "127.0.0.1:62045",
		NetworkID:     3132910,
		Passphrase:    []byte("a-passphrase-long-enough-to-be-accepted"),
		Receive:       func(string, hbp.Data) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := l.Close(); err != nil {
		t.Fatalf("the first close reported %v", err)
	}
	// Cancelling makes serve's goroutine close it too, exactly as a shutdown
	// does. Neither order may produce an error.
	cancel()
	for i := range 5 {
		if err := l.Close(); err != nil {
			t.Fatalf("close %d reported %v; a clean stop would exit 1", i+2, err)
		}
	}
}

// TestConcurrentClosesAgreeOnOneAnswer, because the two closers are goroutines
// and the fault only ever appeared when they overlapped.
func TestConcurrentClosesAgreeOnOneAnswer(t *testing.T) {
	l, err := upstream.New(logging.Discard(), upstream.Config{
		Name:          "far",
		ListenAddress: "127.0.0.1:0",
		TargetAddress: "127.0.0.1:62045",
		NetworkID:     3132910,
		Passphrase:    []byte("a-passphrase-long-enough-to-be-accepted"),
		Receive:       func(string, hbp.Data) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := l.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = l.Close()
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("concurrent close %d reported %v", i, err)
		}
	}
}
