package ipsclink_test

import (
	"encoding/binary"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// aliasKeyUps returns the voice message bodies of
// testdata/ipsc/ipsc-talker-alias.pcap, one slice per key-up.
func aliasKeyUps(tb testing.TB) [][][]byte {
	tb.Helper()
	raw, err := os.ReadFile("../../testdata/ipsc/ipsc-talker-alias.pcap")
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out [][][]byte
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 42 {
			continue
		}
		msg, err := ipsc.Parse(rec[42:])
		if err != nil || !msg.Kind.IsVoice() {
			continue
		}
		if v, _ := msg.AsVoice(); v.IsFirstFrame() || len(out) == 0 {
			out = append(out, nil)
		}
		out[len(out)-1] = append(out[len(out)-1], append([]byte(nil), msg.Body...))
	}
	if len(out) != 2 {
		tb.Fatalf("the capture holds %d key-ups, want 2", len(out))
	}
	return out
}

// delivered collects what the listener hands to the DMR side.
type delivered struct {
	mu     sync.Mutex
	frames []hbp.Data
}

func (d *delivered) add(_ hbp.RepeaterID, f hbp.Data) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.frames = append(d.frames, f)
}

func (d *delivered) streams() map[hbp.StreamID]uint32 {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[hbp.StreamID]uint32{}
	for _, f := range d.frames {
		out[f.StreamID] = f.SourceID
	}
	return out
}

// TestAnOverWithATalkerAliasIsRecordedOnce is the listener's half of the fix in
// ipscbridge: the journal and the peer's record describe one call, from the
// radio, for a key-up whose repeater changed its stream ID twice on the way.
//
// **Before 0.1.311 the journal held three `call started` lines for it**, one of
// them from source 5002016, and two `call ended without a terminator`.
//
// To see it fail: remove the conv.Resolve call from recordVoice. No call then
// holds all of a key-up's frames, and each case stops there saying so.
func TestAnOverWithATalkerAliasIsRecordedOnce(t *testing.T) {
	keyUps := aliasKeyUps(t)
	tests := []struct {
		name  string
		keyUp int
	}{
		{"the longer over", 0},
		{"the shorter over", 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			journal := &syncBuffer{}
			out := &delivered{}
			l, conn := startLogging(t, ipsclink.Config{
				Bridge:  ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true},
				Deliver: out.add,
			}, logging.New(journal, logging.Options{Level: slog.LevelInfo, Format: logging.FormatJSON}))

			send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
			expectReply(t, conn, ipsc.KindRegisterReply)
			waitForPeers(t, l, 1)

			bodies := keyUps[tc.keyUp]
			for _, body := range bodies {
				send(t, conn, ipsc.KindVoice, peerID, body)
			}
			call := waitForCall(t, l, uint64(len(bodies)))
			waitForJournal(t, journal, `"msg":"call ended"`)

			if call.Source != 3132910 || call.Destination != 2 {
				t.Errorf("recorded %d calling %d, want 3132910 calling TG 2",
					call.Source, call.Destination)
			}
			got := journal.String()
			if n := strings.Count(got, `"msg":"call started"`); n != 1 {
				t.Errorf("%d calls started for one key-up, want 1", n)
			}
			if strings.Contains(got, "without a terminator") {
				t.Error("a call was closed as lost in the middle of an over that ended properly")
			}
			if strings.Contains(got, "5002016") {
				t.Error("the journal names 5002016, which is the letters of an alias and not a radio")
			}
			if s := out.streams(); len(s) != 1 {
				t.Errorf("the DMR side was given %d streams for one key-up, want 1: %v", len(s), s)
			}
		})
	}
}

// TestAQuietSlotIsNotContinuedByTheNextCall is the timeout the converter cannot
// keep for itself, because it has no clock: an over whose end was lost, a pause
// long enough for the listener to close it, then an over whose headers were
// lost too. Nothing in the second marks a beginning, so only the pause can.
//
// To see it fail: remove the conv.Forget call from ExpireCallsAt. The second
// over is then delivered under the first one's stream, and one stream is
// counted where two are wanted.
func TestAQuietSlotIsNotContinuedByTheNextCall(t *testing.T) {
	keyUps := aliasKeyUps(t)
	out := &delivered{}
	l, conn := start(t, ipsclink.Config{
		Bridge:  ipscbridge.Config{ColourCode: 11, SlotBitIsTimeslot2: true},
		Deliver: out.add,
	})
	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)
	waitForPeers(t, l, 1)

	first := keyUps[0][:len(keyUps[0])-1] // the terminator is lost
	for _, body := range first {
		send(t, conn, ipsc.KindVoice, peerID, body)
	}
	waitForCall(t, l, uint64(len(first)))
	if closed := l.ExpireCallsAt(time.Now().Add(2 * ipsclink.CallTimeout)); closed != 1 {
		t.Fatalf("closed %d transmissions, want 1", closed)
	}

	var second [][]byte
	for _, body := range keyUps[1] {
		if ipsc.FrameKindOf(body[25]) != ipsc.FrameHeader {
			second = append(second, body)
		}
	}
	for _, body := range second {
		send(t, conn, ipsc.KindVoice, peerID, body)
	}
	waitForCall(t, l, uint64(len(second)))

	if s := out.streams(); len(s) != 2 {
		t.Errorf("the DMR side was given %d streams for two overs a pause apart, want 2: %v",
			len(s), s)
	}
}
