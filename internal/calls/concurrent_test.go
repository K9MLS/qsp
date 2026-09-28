package calls_test

import (
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestTheTrackerIsSafeFromEveryGoroutineThatUsesIt is the guard for the race
// that could end the process: the serve loop, the IPSC listener, links and
// the text sender all record calls, and the console reads them. Run under
// -race, which check.sh does; without the lock this fails within the first
// few hundred operations, usually as a fatal concurrent map write.
//
// To see it fail: delete the t.mu.Lock() at the top of Update.
func TestTheTrackerIsSafeFromEveryGoroutineThatUsesIt(t *testing.T) {
	tr := calls.NewTracker(calls.Options{Timeout: time.Millisecond})
	var wg sync.WaitGroup
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				tr.Update(hbp.RepeaterID(g+1), hbp.Data{
					SourceID: 3132910, TargetID: 2, Timeslot: hbp.Timeslot2,
					FrameType: hbp.FrameTypeVoice, StreamID: hbp.StreamID(i),
				}, time.Now())
			}
		}()
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 500 {
			tr.Expire(time.Now().Add(time.Second))
		}
	}()
	go func() {
		defer wg.Done()
		for range 500 {
			active, recent := tr.Snapshot()
			_ = tr.ActiveCount()
			_, _ = active, recent
		}
	}()
	wg.Wait()
	if n := len(tr.History()); n == 0 {
		t.Error("nothing reached the history")
	}
}
