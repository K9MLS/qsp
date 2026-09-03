package ipsclink_test

import (
	"context"
	"encoding/binary"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/parrot"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// recordingSink captures what a player delivered and to whom.
type recordingSink struct {
	frames map[hbp.RepeaterID]int
}

func (s *recordingSink) Deliver(peer hbp.RepeaterID, _ hbp.Data) error {
	if s.frames == nil {
		s.frames = map[hbp.RepeaterID]int{}
	}
	s.frames[peer]++
	return nil
}

func parrotFor(t *testing.T, tg uint32) *parrot.Recorder {
	t.Helper()
	r, err := parrot.New(parrot.Config{
		Talkgroup:   tg,
		Timeslot:    hbp.Timeslot2,
		MaxDuration: 30 * time.Second,
		Gap:         time.Millisecond,
	})
	if err != nil {
		t.Fatalf("parrot.New: %v", err)
	}
	return r
}

func voiceOn(tg uint32, peer uint32, stream hbp.StreamID) hbp.Data {
	return hbp.Data{
		RepeaterID: hbp.RepeaterID(peer),
		SourceID:   peer,
		TargetID:   tg,
		Timeslot:   hbp.Timeslot2,
		StreamID:   stream,
		FrameType:  hbp.FrameTypeVoiceSync,
	}
}

// TestAReplayGoesToTheMemberWhoRecordedItAndNobodyElse is the property the
// whole design turns on.
//
// A parrot recording is a private answer. Sending it anywhere else would put
// one operator's echo test on every repeater on the network, which is exactly
// what BrandMeister avoids by using a private call and what QSP avoids by
// consuming the frame before it reaches routing.
func TestAReplayGoesToTheMemberWhoRecordedItAndNobodyElse(t *testing.T) {
	const tg, alice, bob = 9998, 3132910, 3155413

	rec := parrotFor(t, tg)
	sink := &recordingSink{}
	player := parrot.NewPlayer(logging.Discard(), sink)

	// Alice transmits on the parrot talkgroup.
	var finished *parrot.Recording
	for i := range 5 {
		if r := rec.Observe(hbp.RepeaterID(alice), voiceOn(tg, alice, hbp.StreamID(1))); r != nil {
			finished = r
		}
		_ = i
	}
	if finished == nil {
		for _, r := range rec.Expire(time.Now().Add(time.Minute)) {
			c := r
			finished = &c
		}
	}
	if finished == nil {
		t.Fatal("five frames on the parrot talkgroup produced no recording")
	}
	// Expire was called with a future time so the recording would complete, so
	// its PlayAt is that far out too. The gap before a replay is tested in
	// internal/peers; here the question is who receives it.
	finished.PlayAt = time.Now()

	player.Start(context.Background(), *finished)
	deadline := time.Now().Add(3 * time.Second)
	for player.Active() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if got := sink.frames[hbp.RepeaterID(alice)]; got == 0 {
		t.Error("the recording was never replayed to the member who made it")
	}
	if got := sink.frames[hbp.RepeaterID(bob)]; got != 0 {
		t.Errorf("another peer received %d frames of somebody else's echo test", got)
	}
}

// TestTwoRecordersDoNotShareAKeyspace is why the IPSC listener has its own
// parrot.Recorder rather than sharing the Homebrew listener's.
//
// **Both key recordings by radio ID and the two protocols share the DMR ID
// space.** This network had 3132910 registered on both listeners at once on
// 2026-09-02: a Pi-Star and an XPR8300. With one shared recorder the hotspot's
// transmission and the repeater's would be the same recording, and one
// operator's audio would be replayed into the other's radio with nothing logged
// to say it had happened.
func TestTwoRecordersDoNotShareAKeyspace(t *testing.T) {
	const tg, shared = 9998, 3132910

	homebrew := parrotFor(t, tg)
	ipsc := parrotFor(t, tg)

	// The same radio ID transmits on both listeners at once, which is the
	// situation that actually occurred.
	homebrew.Observe(hbp.RepeaterID(shared), voiceOn(tg, shared, hbp.StreamID(1)))
	ipsc.Observe(hbp.RepeaterID(shared), voiceOn(tg, shared, hbp.StreamID(2)))

	if homebrew.Active() != 1 {
		t.Errorf("the Homebrew recorder holds %d recordings, want 1", homebrew.Active())
	}
	if ipsc.Active() != 1 {
		t.Errorf("the IPSC recorder holds %d recordings, want 1", ipsc.Active())
	}

	// Cancelling one must not touch the other: a member who keys something
	// else on one protocol has not abandoned a recording on the other.
	homebrew.Cancel(hbp.RepeaterID(shared))
	if homebrew.Active() != 0 {
		t.Errorf("cancelling left %d Homebrew recordings", homebrew.Active())
	}
	if ipsc.Active() != 1 {
		t.Error("cancelling a Homebrew recording destroyed the IPSC one for the same radio ID")
	}
}

// TestAFrameOnAnotherTalkgroupIsNotParrots checks the consume decision.
//
// Parrot consumes what it handles, so handling too much would silently delete
// traffic: a transmission would vanish and the journal would say it had been
// dealt with.
func TestAFrameOnAnotherTalkgroupIsNotParrots(t *testing.T) {
	rec := parrotFor(t, 9998)
	if rec.Handles(voiceOn(2, 3132910, hbp.StreamID(1))) {
		t.Error("parrot claimed a frame on talkgroup 2; it is configured for 9998")
	}
	if !rec.Handles(voiceOn(9998, 3132910, hbp.StreamID(1))) {
		t.Error("parrot did not claim a frame on its own talkgroup")
	}
}

// voiceBodyOn returns the body of a real captured voice datagram with its
// destination rewritten.
//
// Real bytes rather than invented ones: the listener decodes the vocoder
// payload before it produces a burst, and a frame of zeroes is discarded before
// parrot ever sees it — which would make this test pass for the wrong reason.
func voiceBodyOn(tb testing.TB, tg uint32) []byte {
	tb.Helper()
	raw, err := os.ReadFile("../../testdata/ipsc/ipsc-master-voice.pcap")
	if err != nil {
		tb.Fatalf("%v", err)
	}
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 34 || binary.BigEndian.Uint16(rec[12:14]) != 0x0800 {
			continue
		}
		ip := rec[14:]
		if ip[9] != 17 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 9+33 || udp[8] != byte(ipsc.KindVoice) {
			continue
		}
		pl := udp[8:]
		if pl[30] != 0x8a || pl[32] != byte(ipsc.PayloadSync) {
			continue
		}
		body := append([]byte(nil), pl[5:]...)
		// Bytes 9 to 11 of the datagram are the destination; body starts at 5.
		body[4] = byte(tg >> 16)
		body[5] = byte(tg >> 8)
		body[6] = byte(tg)
		return body
	}
	tb.Fatal("the fixture holds no synchronisation voice frame")
	return nil
}

// TestParrotConsumesTheFrameOnTheIPSCPath is the wiring test, and it exists
// because breaking the wiring did not fail anything else.
//
// **Parrot must consume what it handles.** A frame that reaches both parrot and
// routing puts one member's echo test on every repeater and hotspot on the
// network — which is precisely what the group-call design avoids by never
// letting the frame reach the core.
//
// The complementary half matters as much: a frame on any other talkgroup must
// still be delivered. Parrot that consumed too much would silently delete
// traffic, and the journal would say it had been handled.
func TestParrotConsumesTheFrameOnTheIPSCPath(t *testing.T) {
	const parrotTG, ordinaryTG = 9998, 2

	var mu sync.Mutex
	var delivered []uint32
	l, conn := start(t, ipsclink.Config{
		Parrot: parrotFor(t, parrotTG),
		// **The parrot timeslot and the slot bit polarity have to agree.**
		// The captured frames carry the slot bit set; with the default
		// polarity they convert to timeslot 1, and a parrot configured for
		// timeslot 2 then never claims them. That is a live trap for an
		// operator — dmr.parrot.timeslot and ipsc.slot_bit_is_timeslot2 are
		// set in different sections and nothing relates them — and it is why
		// this test sets the polarity explicitly rather than taking a default.
		Bridge: ipscbridge.Config{SlotBitIsTimeslot2: true},
		Deliver: func(_ hbp.RepeaterID, f hbp.Data) {
			mu.Lock()
			delivered = append(delivered, f.TargetID)
			mu.Unlock()
		},
	})
	_ = l

	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	for range 4 {
		send(t, conn, ipsc.KindVoice, peerID, voiceBodyOn(t, parrotTG))
	}
	for range 4 {
		send(t, conn, ipsc.KindVoice, peerID, voiceBodyOn(t, ordinaryTG))
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(delivered)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	var parrotFrames, ordinaryFrames int
	for _, tg := range delivered {
		switch tg {
		case parrotTG:
			parrotFrames++
		case ordinaryTG:
			ordinaryFrames++
		}
	}
	if parrotFrames != 0 {
		t.Errorf("%d frames on the parrot talkgroup reached routing; parrot must consume them",
			parrotFrames)
	}
	if ordinaryFrames == 0 {
		t.Error("no frame on the ordinary talkgroup was delivered; parrot is consuming too much")
	}
}
