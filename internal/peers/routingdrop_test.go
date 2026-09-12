package peers

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/routing"
)

// TestARefusalIsExplainedOncePerTransmission covers the logging that cost an
// evening.
//
// A destination refused by routing was counted and explained only at debug.
// Production runs at info, raising the level needs a restart, and by then the
// transmission is over — so an operator keying up on a talkgroup a peer was not
// attached to saw silence, and the sentence explaining it went somewhere nobody
// reads.
//
// Info is the fix; **once per transmission is what makes info bearable.** A
// refused over is fifty frames a second and a refused text is twenty bursts.
func TestARefusalIsExplainedOncePerTransmission(t *testing.T) {
	l := &Listener{}
	drop := routing.Drop{
		To:     routing.Endpoint{Peer: 3132910, Talkgroup: 11, Timeslot: 1},
		Reason: "peer 3132910 is not attached to talkgroup 11 on TS1",
	}

	if !l.noteRoutingDrop(drop) {
		t.Fatal("the first refusal was not reported; an operator would see silence")
	}
	for i := range 50 {
		if l.noteRoutingDrop(drop) {
			t.Fatalf("frame %d of the same refusal was reported again; a refused "+
				"over would fill the journal", i+2)
		}
	}

	// A different reason for the same destination is a different fact.
	other := drop
	other.Reason = "talkgroup 11 on TS1 is not permitted by dmr.access.talkgroups"
	if !l.noteRoutingDrop(other) {
		t.Error("a different reason for the same destination was suppressed")
	}

	// And the same refusal later is worth saying again: an operator who keys up
	// a second time wants to know it is still refused.
	l.routingDropMu.Lock()
	for k := range l.routingDrops {
		l.routingDrops[k] = time.Now().Add(-2 * routingDropWindow)
	}
	l.routingDropMu.Unlock()
	if !l.noteRoutingDrop(drop) {
		t.Error("a refusal repeated after the window was suppressed; a second " +
			"attempt should be reported")
	}
}

// TestTheRefusalMemoryIsBounded keeps a peer from growing it without limit.
//
// The key includes the destination, so a peer transmitting to endless
// talkgroups would otherwise add an entry per talkgroup for as long as it kept
// going.
func TestTheRefusalMemoryIsBounded(t *testing.T) {
	l := &Listener{}
	for i := range 500 {
		l.noteRoutingDrop(routing.Drop{
			To:     routing.Endpoint{Peer: 3132910, Talkgroup: uint32(i), Timeslot: 1},
			Reason: "not attached",
		})
	}
	l.routingDropMu.Lock()
	n := len(l.routingDrops)
	l.routingDropMu.Unlock()
	if n > 200 {
		t.Errorf("the refusal memory holds %d entries after 500 destinations; "+
			"a peer must not be able to grow it without limit", n)
	}
}

// TestARefusalIsLoggedWhereAnOperatorWillSeeIt asserts the level, not the
// wording.
//
// The deduplication above is only worth having because the line is at info.
// Both halves were written together and either could be undone alone: a
// dropped refusal at debug is invisible on a running server, and that is the
// whole defect this replaced.
func TestARefusalIsLoggedWhereAnOperatorWillSeeIt(t *testing.T) {
	src, err := os.ReadFile("listener.go")
	if err != nil {
		t.Fatalf("reading listener.go: %v", err)
	}
	s := string(src)

	i := strings.Index(s, `for _, drop := range res.Drops {`)
	if i < 0 {
		t.Fatal("the routing drop loop is gone; this test needs rewriting")
	}
	// **The window is the loop, not a character count.** This read the next
	// 1,400 characters, which made the assertions below sensitive to the
	// length of the comments above them: a paragraph added inside the loop on
	// 2026-09-12 pushed `l.log.Info` past the cut-off and failed this test for
	// prose rather than for behaviour. A gate that fires on comment length is
	// a gate an author edits to make green, which is how one stops being a
	// gate.
	body := s[i:]
	if end := strings.Index(body, "\n\tfor _, started := range res.StartedStreams {"); end > 0 {
		body = body[:end]
	} else {
		t.Fatal("the loop after the routing drops is gone; this test needs rewriting")
	}

	if !strings.Contains(body, `l.log.Info("frame not forwarded"`) {
		t.Error("a routing refusal is not logged at info; production runs at " +
			"info, so the reason would be counted and never explainable")
	}
	if strings.Contains(body, `l.log.Debug("frame not forwarded"`) {
		t.Error("a routing refusal is back at debug, which is where the reason " +
			"goes to be unreachable")
	}
	if !strings.Contains(body, "l.noteRoutingDrop(drop)") {
		t.Error("refusals are no longer deduplicated; a refused over is fifty " +
			"frames a second and would fill the journal at info")
	}
}
