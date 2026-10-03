package parrot

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

type countingSink struct {
	mu sync.Mutex
	n  int
}

func (s *countingSink) Deliver(hbp.RepeaterID, hbp.Data) error {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return nil
}

// A replay replaced by a newer one must not take the newer one's entry with
// it on the way out. It did, and the player then said nothing was playing to
// that peer while something was: a key-up could not stop it, and a text was
// sent over the top of it.
//
// To see it fail: make play's deferred delete unconditional.
func TestAReplacedReplayDoesNotForgetItsSuccessor(t *testing.T) {
	p := NewPlayer(slog.New(slog.NewTextHandler(io.Discard, nil)), &countingSink{})
	long := Recording{Peer: 7, Frames: make([]hbp.Data, 500)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p.Start(ctx, long)
	p.Start(ctx, long) // cancels the first, which exits and cleans up
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !p.Busy(7) {
			t.Fatal("the player says nothing is playing to the peer while the second replay runs")
		}
		time.Sleep(5 * time.Millisecond)
		if time.Until(deadline) < 1500*time.Millisecond {
			break
		}
	}
	p.Stop(7)
	if p.Busy(7) {
		t.Error("still busy after Stop")
	}
}
