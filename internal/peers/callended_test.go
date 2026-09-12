package peers

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/routing"
)

// journal returns a listener that logs everything into a buffer.
//
// Debug is included deliberately: two of the checks below are about a line
// being at debug rather than absent, and a handler that filtered it could not
// tell those apart.
func journal() (*Listener, *bytes.Buffer) {
	var buf bytes.Buffer
	l := &Listener{log: slog.New(slog.NewTextHandler(&buf,
		&slog.HandlerOptions{Level: slog.LevelDebug}))}
	return l, &buf
}

// TestTheLoopRuleIsNotCountedAsACollision is the defect an operator found by
// asking what a number meant.
//
// The test server read 20 collisions, then 88 after two keyups on the far end
// of its link. Thirty-four frames apiece: `repeat` offers a group call to
// every QSP link including the one it arrived on, the loop rule refuses that
// one, and every refusal was counted and rendered amber on Traffic. A counter
// that climbs at the frame rate while a link works perfectly is worse than no
// counter, because the first real collision arrives as a number that was
// already rising.
//
// routing.Drop.NotAJudgement already classified these correctly and had one
// reader; this was the second one it needed.
func TestTheLoopRuleIsNotCountedAsACollision(t *testing.T) {
	l, buf := journal()

	loop := routing.Drop{
		To:            routing.Endpoint{Upstream: "production", Talkgroup: 2, Timeslot: hbp.Timeslot2},
		Reason:        "arrived from production, which is where it came from",
		NotAJudgement: true,
	}
	for range 34 {
		l.deliver(0, routing.Result{Drops: []routing.Drop{loop}})
	}

	if got := l.Stats().Collisions; got != 0 {
		t.Errorf("a link carrying one transmission counted %d collisions; the "+
			"loop rule is a rule about links, not a verdict on a transmission", got)
	}

	// **Still logged.** The refusal line is how an operator sees a link is
	// carrying at all, and it is what diagnosed this. It is the counting that
	// was wrong, not the saying.
	if !strings.Contains(buf.String(), "frame not forwarded") {
		t.Error("the refusal was not logged; the line that diagnosed this must survive")
	}
}

// TestARealRefusalIsStillACollision is the other side of the same fix.
//
// Excluding the loop rule must not turn the counter off. A destination that
// judged the frame — an access list, an unattached peer, a held timeslot — is
// what the number exists to report.
func TestARealRefusalIsStillACollision(t *testing.T) {
	l, _ := journal()

	l.deliver(0, routing.Result{Drops: []routing.Drop{{
		To:     routing.Endpoint{Peer: 3132910, Talkgroup: 11, Timeslot: hbp.Timeslot1},
		Reason: "peer 3132910 is not attached to talkgroup 11 on TS1",
	}}})

	if got := l.Stats().Collisions; got != 1 {
		t.Errorf("a refused destination counted %d collisions, want 1; excluding "+
			"the loop rule must not silence the counter", got)
	}
}

// TestATransmissionEndingReachesTheJournal is the gap that cost an exchange on
// 2026-09-12.
//
// `observe` computed the end of every call, published it to the console and
// wrote it to the history, and said nothing to the journal: "call ended"
// existed in internal/ipsclink and nowhere else, so the Motorola side reported
// both halves of a transmission and the Homebrew side reported only the first.
//
// That breaks the diagnostic this project relies on most — log the same fact
// at two layers and read the gap — in the direction that matters, because a
// missing end line meant nothing and so could not mean something. A whole
// timeslot of lost audio was found by exactly that comparison
// (internal/protocol/ipsc/voice.go).
func TestATransmissionEndingReachesTheJournal(t *testing.T) {
	l, buf := journal()
	l.cfg.Calls = calls.NewTracker(calls.Options{})

	const stream = hbp.StreamID(0x14c5)
	l.observe(0, voiceFrame(stream, hbp.FrameTypeSync, hbp.DataTypeVoiceLCHeader))
	for range 32 {
		l.observe(0, voiceFrame(stream, hbp.FrameTypeVoice, 0))
	}
	l.observe(0, voiceFrame(stream, hbp.FrameTypeSync, hbp.DataTypeTerminator))

	out := buf.String()
	if !strings.Contains(out, "call started") {
		t.Fatal("no call started line, so this test is not exercising the tracker")
	}
	if !strings.Contains(out, "call ended") {
		t.Fatalf("a completed transmission was never reported as ending.\n%s", out)
	}
	// The counts are what make the two layers comparable: the IPSC line
	// carries frames and a duration, and a line without them cannot be read
	// against it.
	for _, want := range []string{"level=INFO", "frames=34", "duration=", "talkgroup=2", "timeslot=2"} {
		if !strings.Contains(out, want) {
			t.Errorf("the end-of-call line does not carry %q.\n%s", want, out)
		}
	}
}

// TestADataBurstEndingIsNotAnInfoLine keeps the fix from reintroducing a
// defect this project has already shipped twice.
//
// A text converted from a Motorola repeater is a run of data bursts sharing
// one stream ID, so each ends here. Info for every one of them is the
// seventeen-lines-per-text defect that dropped `call started` to debug for a
// data run in the first place.
func TestADataBurstEndingIsNotAnInfoLine(t *testing.T) {
	l, buf := journal()
	l.cfg.Calls = calls.NewTracker(calls.Options{})

	const stream = hbp.StreamID(0x39ba)
	l.observe(0, voiceFrame(stream, hbp.FrameTypeSync, dataTypeMessage))
	l.observe(0, voiceFrame(stream, hbp.FrameTypeSync, dataTypeMessage))
	l.observe(0, voiceFrame(stream, hbp.FrameTypeSync, hbp.DataTypeTerminator))

	out := buf.String()
	if !strings.Contains(out, "call ended") {
		t.Fatalf("a data run was never reported as ending, at any level.\n%s", out)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.Contains(line, "call ended") && strings.Contains(line, "level=INFO") {
			t.Errorf("a data burst ending is an info line; one text message would "+
				"write a run of them.\n%s", line)
		}
	}
}

// dataTypeMessage is a data burst that is neither a voice LC header nor a
// terminator, which is what makes it part of a run rather than the end of one.
// Deliberately not the CSBK data type: a preamble is skipped before the
// tracker ever sees it, and a fixture that was skipped would pass this test
// for the wrong reason.
const dataTypeMessage uint8 = 0x7

// voiceFrame builds one frame of a transmission on the network this project
// runs: talkgroup 2, timeslot 2.
//
// **A data sync frame is not a terminator on its own**, so the data type is
// passed rather than derived: internal/calls' own fixtures made every data
// burst a terminator once, and a helper that does lets a defect pass.
func voiceFrame(stream hbp.StreamID, ft hbp.FrameType, dt uint8) hbp.Data {
	return hbp.Data{
		RepeaterID: 3132910,
		SourceID:   3132910,
		TargetID:   2,
		Timeslot:   hbp.Timeslot2,
		CallType:   hbp.CallGroup,
		FrameType:  ft,
		DataType:   dt,
		StreamID:   stream,
	}
}
