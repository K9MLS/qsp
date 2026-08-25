package events

import (
	"sync"
	"testing"
	"time"
)

func fixedClock() func() time.Time {
	t := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	return func() time.Time { return t }
}

func newTestBus(history, buffer int) *Bus {
	return NewBus(nil, Options{HistorySize: history, SubscriberBuffer: buffer, Clock: fixedClock()})
}

func TestPublishAssignsMonotonicSequence(t *testing.T) {
	b := newTestBus(16, 16)
	defer b.Close()

	var last uint64
	for i := 0; i < 10; i++ {
		ev, ok := b.Publish(TypePeerConnected, nil)
		if !ok {
			t.Fatalf("publish %d refused", i)
		}
		if ev.Seq != last+1 {
			t.Fatalf("sequence jumped: got %d, want %d", ev.Seq, last+1)
		}
		last = ev.Seq
	}
	if b.Seq() != 10 {
		t.Errorf("Seq() = %d, want 10", b.Seq())
	}
}

func TestPublishRefusesUndeclaredType(t *testing.T) {
	b := newTestBus(16, 16)
	defer b.Close()

	if _, ok := b.Publish(Type("peer.exploded"), nil); ok {
		t.Fatal("bus accepted an undeclared event type")
	}
	if b.Seq() != 0 {
		t.Errorf("refused publish consumed a sequence number: Seq() = %d", b.Seq())
	}
}

func TestSubscribeDeliversSubsequentEvents(t *testing.T) {
	b := newTestBus(16, 16)
	defer b.Close()

	sub, since := b.Subscribe()
	defer sub.Close()
	if since != 0 {
		t.Fatalf("expected sequence 0 on a fresh bus, got %d", since)
	}

	b.Publish(TypeCallStarted, map[string]any{"talkgroup": 3100})

	select {
	case ev := <-sub.C():
		if ev.Type != TypeCallStarted {
			t.Errorf("got type %q, want %q", ev.Type, TypeCallStarted)
		}
		if ev.Seq != 1 {
			t.Errorf("got seq %d, want 1", ev.Seq)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestSubscribeReturnsCurrentSequenceForSnapshotSync(t *testing.T) {
	// The documented snapshot protocol: subscribe, note the sequence, take a
	// snapshot, then discard delivered events at or below that sequence.
	b := newTestBus(16, 16)
	defer b.Close()

	b.Publish(TypePeerConnected, nil)
	b.Publish(TypePeerConnected, nil)

	sub, since := b.Subscribe()
	defer sub.Close()

	if since != 2 {
		t.Fatalf("Subscribe reported sequence %d, want 2", since)
	}
	select {
	case ev := <-sub.C():
		t.Fatalf("subscriber received pre-subscription event seq %d", ev.Seq)
	default:
	}
}

func TestSlowSubscriberIsDroppedAndCounted(t *testing.T) {
	// Constitution: publishing must never block on a slow consumer, and loss
	// must be visible rather than silent.
	b := newTestBus(64, 2)
	defer b.Close()

	sub, _ := b.Subscribe()
	defer sub.Close()

	const n = 50
	done := make(chan struct{})
	go func() {
		for i := 0; i < n; i++ {
			b.Publish(TypeCallStarted, i)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}

	if sub.Lagged() == 0 {
		t.Error("expected dropped events to be counted, got Lagged() = 0")
	}
	if got := b.Seq(); got != n {
		t.Errorf("Seq() = %d, want %d: drops must not affect sequencing", got, n)
	}
}

func TestReplayReturnsEventsAfterSequence(t *testing.T) {
	b := newTestBus(16, 16)
	defer b.Close()

	for i := 0; i < 5; i++ {
		b.Publish(TypePeerConnected, i)
	}

	evs, complete := b.Replay(2)
	if !complete {
		t.Error("Replay reported an incomplete range that history covers")
	}
	if len(evs) != 3 {
		t.Fatalf("got %d events, want 3", len(evs))
	}
	for i, ev := range evs {
		if want := uint64(i + 3); ev.Seq != want {
			t.Errorf("event %d has seq %d, want %d", i, ev.Seq, want)
		}
	}
}

func TestReplayReportsIncompleteWhenHistoryEvicted(t *testing.T) {
	// The critical honesty property: a consumer must never be told it has a
	// complete stream when events were evicted.
	b := newTestBus(4, 16)
	defer b.Close()

	for i := 0; i < 20; i++ {
		b.Publish(TypePeerConnected, i)
	}

	if _, complete := b.Replay(0); complete {
		t.Error("Replay claimed completeness after eviction")
	}
	if _, complete := b.Replay(19); !complete {
		t.Error("Replay reported incomplete for an already-current consumer")
	}
}

func TestReplayOnEmptyBus(t *testing.T) {
	b := newTestBus(4, 16)
	defer b.Close()

	evs, complete := b.Replay(0)
	if len(evs) != 0 {
		t.Errorf("got %d events from an empty bus", len(evs))
	}
	if !complete {
		t.Error("an empty bus with a current consumer should report complete")
	}
}

func TestCloseClosesSubscriberChannels(t *testing.T) {
	b := newTestBus(16, 16)
	sub, _ := b.Subscribe()

	b.Close()

	select {
	case _, open := <-sub.C():
		if open {
			t.Error("expected a closed channel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber channel was not closed by Bus.Close")
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	b := newTestBus(16, 16)
	sub, _ := b.Subscribe()
	b.Close()
	b.Close()
	sub.Close()
	sub.Close()
}

func TestPublishAfterCloseIsRefused(t *testing.T) {
	b := newTestBus(16, 16)
	b.Close()
	if _, ok := b.Publish(TypePeerConnected, nil); ok {
		t.Error("bus accepted a publish after Close")
	}
}

func TestSubscribeAfterCloseYieldsClosedChannel(t *testing.T) {
	b := newTestBus(16, 16)
	b.Close()

	sub, _ := b.Subscribe()
	select {
	case _, open := <-sub.C():
		if open {
			t.Error("expected a closed channel from a closed bus")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Subscribe on a closed bus returned an open channel")
	}
}

func TestSubscriberCount(t *testing.T) {
	b := newTestBus(16, 16)
	defer b.Close()

	if b.SubscriberCount() != 0 {
		t.Fatalf("fresh bus reports %d subscribers", b.SubscriberCount())
	}
	s1, _ := b.Subscribe()
	s2, _ := b.Subscribe()
	if b.SubscriberCount() != 2 {
		t.Errorf("SubscriberCount() = %d, want 2", b.SubscriberCount())
	}
	s1.Close()
	if b.SubscriberCount() != 1 {
		t.Errorf("SubscriberCount() = %d after one close, want 1", b.SubscriberCount())
	}
	s2.Close()
}

func TestConcurrentPublishAndSubscribe(t *testing.T) {
	// Exercised under -race in CI.
	b := newTestBus(128, 32)
	defer b.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Publish(TypeCallStarted, j)
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, _ := b.Subscribe()
			defer sub.Close()
			deadline := time.After(2 * time.Second)
			for {
				select {
				case _, open := <-sub.C():
					if !open {
						return
					}
				case <-deadline:
					return
				}
			}
		}()
	}
	wg.Wait()

	if got := b.Seq(); got != 800 {
		t.Errorf("Seq() = %d, want 800", got)
	}
}

func TestKnownTypes(t *testing.T) {
	types := KnownTypes()
	if len(types) != 7 {
		t.Errorf("KnownTypes() returned %d types, want 7", len(types))
	}
	for _, ty := range types {
		if !IsKnown(ty) {
			t.Errorf("KnownTypes() returned %q but IsKnown says otherwise", ty)
		}
	}
	if IsKnown(Type("nope")) {
		t.Error("IsKnown returned true for an undeclared type")
	}
}
