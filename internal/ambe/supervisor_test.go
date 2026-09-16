package ambe

import (
	"context"
	"encoding/hex"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/health"
)

// waitFor polls until cond holds or the deadline passes.
//
// The supervisor opens its channels on goroutines, so a test has to wait for
// something rather than assume it. A poll with a deadline says what it is
// waiting for when it gives up, which a fixed sleep cannot.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestASupervisedChannelOpensAndReportsItselfReadyAndCarryingNothing is the
// state this patch leaves the subsystem in, asserted rather than described.
//
// **A reachable vocoder is degraded, not healthy.** ADR-0063 built the
// talkgroup-to-channel mapping and nothing delivers frames to it yet. A green
// line against a subsystem that cannot pass audio is the stub that claims
// success, which §7 forbids, and it is also the specific way an operator
// spends an afternoon: everything reports fine and no audio crosses.
func TestASupervisedChannelOpensAndReportsItselfReadyAndCarryingNothing(t *testing.T) {
	f := newFakeVocoder(t)
	s := NewSupervisor(nil, []Vocoder{{Name: "dvstick", Address: f.address(), Rate: RateIndexDMR}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("the supervisor did not stop when its context was cancelled")
		}
	})

	waitFor(t, "the channel to open", func() bool { return s.ClientFor("dvstick") != nil })

	res := s.CheckFor("dvstick").Check(context.Background())
	if res.Status != health.StatusDegraded {
		t.Errorf("a reachable vocoder reports %q; it carries no audio yet and "+
			"must not report healthy", res.Status)
	}
	// "reachable" and "since it opened": right after a restart this line is
	// amber for a working vocoder, and it must not read like a fault.
	for _, want := range []string{"AMBE3000F", "reachable", "carried nothing", "since it opened at"} {
		if !strings.Contains(res.Summary, want) {
			t.Errorf("the summary %q does not mention %q", res.Summary, want)
		}
	}
	if res.Fix == "" {
		t.Error("a degraded result carries no fix; an operator reading it needs " +
			"to know whether to do something")
	}
	if got := res.Detail["product"]; got != "AMBE3000F" {
		t.Errorf("the detail reports product %q", got)
	}
	if got := res.Detail["address"]; got != f.address() {
		t.Errorf("the detail reports address %q, want %q", got, f.address())
	}
}

// TestAnUnreachableChannelFailsAndSaysHowToFixIt covers the ordinary case.
//
// The operator's dongle is passed through ESXi, AMBEserver has no unit and is
// started by hand, and the recovery path for a wedged chip is removing its
// power. So absent is the normal state, and the check has to be useful about
// it rather than merely red.
func TestAnUnreachableChannelFailsAndSaysHowToFixIt(t *testing.T) {
	// A port nothing is listening on. Loopback, so the refusal is immediate
	// rather than a timeout.
	s := NewSupervisor(nil, []Vocoder{{Name: "dvstick", Address: "127.0.0.1:1", Rate: RateIndexDMR}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "an attempt to be recorded", func() bool {
		return s.CheckFor("dvstick").Check(context.Background()).Detail["attempts"] != "0"
	})

	res := s.CheckFor("dvstick").Check(context.Background())
	if res.Status != health.StatusFailing {
		t.Errorf("an unreachable vocoder reports %q, want failing", res.Status)
	}
	if !strings.Contains(res.Summary, "127.0.0.1:1") {
		t.Errorf("the summary %q does not name the address it could not reach", res.Summary)
	}
	// The two things that have actually gone wrong on this bench, in the fix:
	// AMBEserver not running, and a wedged chip needing its power removed.
	for _, want := range []string{"AMBEserver", "-x", "unplug"} {
		if !strings.Contains(res.Fix, want) {
			t.Errorf("the fix does not mention %q: %s", want, res.Fix)
		}
	}
	if s.ClientFor("dvstick") != nil {
		t.Error("an unreachable channel reports a client")
	}
}

// TestASupervisorSurvivesAnAbsentVocoder is the property that decides this is
// a supervisor rather than a startup step.
//
// **A server that refused to start without a dongle would be a server that
// refused to start.** The thing at the other end is unplugged for ten seconds
// at a time as its documented recovery path.
func TestASupervisorSurvivesAnAbsentVocoder(t *testing.T) {
	s := NewSupervisor(nil, []Vocoder{
		{Name: "absent", Address: "127.0.0.1:1"},
		{Name: "alsoabsent", Address: "127.0.0.1:2"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	finished := make(chan struct{})
	go func() { s.Run(ctx); close(finished) }()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("the supervisor did not return after its context expired")
	}

	// Both are still reported, and reported as failing rather than omitted.
	for _, name := range s.Names() {
		if got := s.CheckFor(name).Check(context.Background()).Status; got != health.StatusFailing {
			t.Errorf("channel %q reports %q after failing to open", name, got)
		}
	}
}

// TestANamedChannelThatIsNotConfiguredReportsUnavailable.
//
// A bridge naming a transcoder that does not exist is refused by
// configuration, so this should be unreachable — which is exactly why it must
// not report healthy if it ever is reached. Unavailable says there is nothing
// to check, which is the truth.
func TestANamedChannelThatIsNotConfiguredReportsUnavailable(t *testing.T) {
	s := NewSupervisor(nil, nil)
	res := s.CheckFor("ghost").Check(context.Background())
	if res.Status != health.StatusUnavailable {
		t.Errorf("an unconfigured channel reports %q, want unavailable", res.Status)
	}
	if !strings.Contains(res.Summary, "ghost") {
		t.Errorf("the summary %q does not name the channel", res.Summary)
	}
	if got := s.CheckFor("ghost").Name(); got != "transcoder:ghost" {
		t.Errorf("the check is named %q, want transcoder:ghost", got)
	}
}

// TestChannelsKeepTheirConfigurationOrder, because an operator reading the
// console compares it against the document they wrote.
func TestChannelsKeepTheirConfigurationOrder(t *testing.T) {
	s := NewSupervisor(nil, []Vocoder{
		{Name: "first", Address: "127.0.0.1:1"},
		{Name: "second", Address: "127.0.0.1:2"},
		{Name: "third", Address: "127.0.0.1:3"},
	})
	got := strings.Join(s.Names(), ",")
	if want := "first,second,third"; got != want {
		t.Errorf("the channels are ordered %q, want %q", got, want)
	}
}

// TestCancellingTheContextClosesTheSocket checks the half that is easy to
// forget.
//
// Anything that takes a resource must give it back, and a supervisor that held
// its sockets past shutdown would leave AMBEserver believing a client was
// still there.
func TestCancellingTheContextClosesTheSocket(t *testing.T) {
	f := newFakeVocoder(t)
	s := NewSupervisor(nil, []Vocoder{{Name: "dvstick", Address: f.address()}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	waitFor(t, "the channel to open", func() bool { return s.ClientFor("dvstick") != nil })
	cancel()
	<-done

	if s.ClientFor("dvstick") != nil {
		t.Error("the channel still reports a client after shutdown")
	}
	res := s.CheckFor("dvstick").Check(context.Background())
	if res.Status != health.StatusFailing {
		t.Errorf("a closed channel reports %q; it is no longer reachable", res.Status)
	}
}

// TestOpeningIsNotRetriedWhileAChannelIsAlreadyOpen keeps the retry from
// becoming a reconnect storm.
//
// The chip answers one packet at a time and a handshake is five exchanges. A
// supervisor that re-handshaked every interval would spend a fifth of its
// vocoder's capacity proving it was still there, and would reset the chip each
// time — which discards its configuration and its rate.
func TestOpeningIsNotRetriedWhileAChannelIsAlreadyOpen(t *testing.T) {
	f := newFakeVocoder(t)
	s := NewSupervisor(nil, []Vocoder{{Name: "dvstick", Address: f.address()}})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := s.channels[0]

	if err := s.openOnce(ctx, ch); err != nil {
		t.Fatalf("opening a channel: %v", err)
	}
	after := f.requests()

	var extra atomic.Int64
	for i := 0; i < 5; i++ {
		if err := s.openOnce(ctx, ch); err != nil {
			t.Fatalf("a second open attempt returned %v", err)
		}
	}
	extra.Store(int64(f.requests() - after))
	if extra.Load() != 0 {
		t.Errorf("five further attempts sent %d packets to an already open "+
			"channel; each handshake resets the chip and discards its rate",
			extra.Load())
	}
	ch.close()
}

// TestAnAMBEserverRestartedUnderneathIsReopened is production on 2026-09-16.
//
// AMBEserver was stopped and started again while QSP ran. The supervisor had
// opened the channel once and never checked it again, so the new AMBEserver
// never received the DMR rate and every call decoded into garbled audio until
// QSP was restarted by hand.
//
// To see it bite: delete the broken-client block in keepOpen, and no reset
// ever reaches the second vocoder.
func TestAnAMBEserverRestartedUnderneathIsReopened(t *testing.T) {
	first := newFakeVocoder(t)
	addr := first.address()

	s := NewSupervisor(nil, []Vocoder{{Name: "zello", Address: addr, Rate: RateIndexDMR}})
	s.checkInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	deadline := time.Now().Add(3 * time.Second)
	for s.ClientFor("zello") == nil {
		if time.Now().After(deadline) {
			t.Fatal("the channel never opened")
		}
		time.Sleep(5 * time.Millisecond)
	}
	opened := s.ClientFor("zello")

	// AMBEserver stops. The next call's first exchange fails.
	_ = first.conn.Close()
	opened.timeout = 100 * time.Millisecond
	if err := opened.Acquire(Holder{Reason: "a call during the outage"}); err == nil {
		t.Fatal("a call started against a vocoder that is not there")
	}
	if s.ClientFor("zello") != nil {
		t.Error("a client that stopped answering was still handed out")
	}

	// AMBEserver comes back on the same address.
	second := fakeVocoderAt(t, addr)
	deadline = time.Now().Add(3 * time.Second)
	for {
		c := s.ClientFor("zello")
		if c != nil && c != opened {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the channel was not reopened against the restarted AMBEserver")
		}
		time.Sleep(5 * time.Millisecond)
	}
	second.mu.Lock()
	var sawReset, sawRate bool
	for _, req := range second.seen {
		switch hex.EncodeToString(req) {
		case "6100010033":
			sawReset = true
		case "610002000921":
			sawRate = true
		}
	}
	second.mu.Unlock()
	if !sawReset || !sawRate {
		t.Errorf("the restarted vocoder saw reset %v and the DMR rate %v; the handshake must be whole", sawReset, sawRate)
	}
}
