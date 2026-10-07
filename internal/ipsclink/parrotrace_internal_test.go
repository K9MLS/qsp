package ipsclink

import (
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestTheParrotIsSharedByTheReadLoopAndTheTicker. The read loop offers every
// burst to the parrot and the half-second ticker ends recordings that have
// gone quiet. They are two goroutines with one recorder, whose map had no
// lock: on a machine with more than one core that stopped the server.
//
// The health report asks how many recordings are running from a third.
//
// To see it fail: in parrot.Recorder.Expire, delete the two lines that take
// and release r.mu. The race detector names this test, and without the
// detector the run ends in "fatal error: concurrent map".
func TestTheParrotIsSharedByTheReadLoopAndTheTicker(t *testing.T) {
	for _, tc := range []struct {
		name      string
		talkgroup uint32
	}{
		{"members keying the parrot", 9998},
		{"members keying something else, which cancels", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := parrot.New(parrot.Config{Talkgroup: 9998, Timeslot: hbp.Timeslot2,
				MaxDuration: 30 * time.Second, Gap: time.Millisecond, Silence: time.Nanosecond})
			if err != nil {
				t.Fatalf("parrot.New: %v", err)
			}
			l, err := New(logging.Discard(), Config{ListenAddress: "127.0.0.1:0", MasterID: 1, Parrot: rec})
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			var wg sync.WaitGroup
			wg.Go(func() { // the read loop
				for i := range 20000 {
					peer := hbp.RepeaterID(3132910 + i%4)
					tg := tc.talkgroup
					if i%7 == 0 {
						tg = 9998
					}
					l.parrotHandles(peer, hbp.Data{RepeaterID: peer, SourceID: uint32(peer),
						TargetID: tg, Timeslot: hbp.Timeslot2, CallType: hbp.CallGroup,
						StreamID: hbp.StreamID(i / 100), FrameType: hbp.FrameTypeVoiceSync})
				}
			})
			wg.Go(func() { // the ticker
				for range 20000 {
					l.expireParrot()
				}
			})
			wg.Go(func() { // the health report
				for range 20000 {
					_ = rec.Active()
				}
			})
			wg.Wait()
		})
	}
}
