package peers

import (
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/tms"
)

// aComposedText is a real text: sixteen preambles, a header and blocks.
func aComposedText(t *testing.T, slot hbp.Timeslot) []hbp.Data {
	t.Helper()
	var n hbp.StreamID
	frames, err := tms.Frames(tms.Message{From: 9990, To: 2, Group: true, Reference: 0x80, Text: "QSP 0443"},
		slot, 11, func() hbp.StreamID { n++; return n })
	if err != nil {
		t.Fatalf("%v", err)
	}
	return frames
}

func blocksToFollow(t *testing.T, f hbp.Data) uint8 {
	t.Helper()
	payload, _, ok := dmrfec.DecodeBPTC(f.Payload[:])
	if !ok {
		t.Fatal("a preamble did not decode")
	}
	p, err := dmrfec.ParsePreamble(dmrfec.BurstBytesFrom(payload)[:dmrfec.CSBKBytes])
	if err != nil {
		t.Fatalf("%v", err)
	}
	return p.BlocksToFollow
}

// TestAHotspotIsSentOnePreamblePerText is the fix for 2026-09-28: MMDVMHost
// turns every network preamble into fifteen on the air, so sixteen became
// 240 and no hotspot radio showed a text. It must receive exactly one — the
// last, immediately before the header — and every data block after it.
//
// To see it fail: return []hbp.Data{frame} for a preamble in pass, and the
// hotspot is sent all sixteen again; or keep the first preamble rather than
// replacing it, and the count is 21 where the header says 6.
func TestAHotspotIsSentOnePreamblePerText(t *testing.T) {
	g := newPreambleGate()
	in := aComposedText(t, hbp.Timeslot2)
	now := time.Unix(0, 0)

	var out []hbp.Data
	for _, f := range in {
		out = append(out, g.pass(3132910, f, now)...)
		now = now.Add(60 * time.Millisecond)
	}

	blocks := len(in) - tms.Preambles - 1
	if want := 1 + 1 + blocks; len(out) != want {
		t.Fatalf("the hotspot was sent %d frames, want %d: one preamble, the header and %d blocks",
			len(out), want, blocks)
	}
	if out[0].DataType != dmrfec.DataTypeCSBK || out[1].DataType != dmrfec.DataTypeDataHeader {
		t.Fatalf("sent %#x then %#x, want a preamble then the header", out[0].DataType, out[1].DataType)
	}
	if got, want := blocksToFollow(t, out[0]), uint8(1+blocks); got != want {
		t.Errorf("the preamble sent counts %d bursts to come, want %d: the header and its blocks", got, want)
	}
	for i, f := range out[2:] {
		if f.DataType != dmrfec.DataTypeRate12 {
			t.Errorf("frame %d after the header is %#x, want a data block", i, f.DataType)
		}
	}
}

// TestThePreambleGateKeepsEverythingElseApart covers what it must not touch
// and what it must keep separate.
func TestThePreambleGateKeepsEverythingElseApart(t *testing.T) {
	t0 := time.Unix(0, 0)
	voice := hbp.Data{FrameType: hbp.FrameTypeVoiceSync, Timeslot: hbp.Timeslot2, StreamID: 9}
	header := func(slot hbp.Timeslot) hbp.Data { return aComposedText(t, slot)[tms.Preambles] }
	pre := func(slot hbp.Timeslot) hbp.Data { return aComposedText(t, slot)[tms.Preambles-1] }

	tests := []struct {
		name  string
		steps []struct {
			peer  hbp.RepeaterID
			frame hbp.Data
			after time.Duration
		}
		want []int // how many frames each step releases
	}{
		{
			name: "voice passes at once",
			steps: []struct {
				peer  hbp.RepeaterID
				frame hbp.Data
				after time.Duration
			}{{1, voice, 0}},
			want: []int{1},
		},
		{
			name: "a header with no preamble goes alone",
			steps: []struct {
				peer  hbp.RepeaterID
				frame hbp.Data
				after time.Duration
			}{{1, header(hbp.Timeslot2), 0}},
			want: []int{1},
		},
		{
			name: "a preamble whose header is too late is dropped",
			steps: []struct {
				peer  hbp.RepeaterID
				frame hbp.Data
				after time.Duration
			}{{1, pre(hbp.Timeslot2), 0}, {1, header(hbp.Timeslot2), preambleHold + time.Second}},
			want: []int{0, 1},
		},
		{
			name: "another hotspot's header does not release it",
			steps: []struct {
				peer  hbp.RepeaterID
				frame hbp.Data
				after time.Duration
			}{{1, pre(hbp.Timeslot2), 0}, {2, header(hbp.Timeslot2), 0}, {1, header(hbp.Timeslot2), 0}},
			want: []int{0, 1, 2},
		},
		{
			name: "the other timeslot's header does not release it",
			steps: []struct {
				peer  hbp.RepeaterID
				frame hbp.Data
				after time.Duration
			}{{1, pre(hbp.Timeslot2), 0}, {1, header(hbp.Timeslot1), 0}, {1, header(hbp.Timeslot2), 0}},
			want: []int{0, 1, 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := newPreambleGate()
			now := t0
			for i, s := range tc.steps {
				now = now.Add(s.after)
				if got := len(g.pass(s.peer, s.frame, now)); got != tc.want[i] {
					t.Errorf("step %d released %d frames, want %d", i, got, tc.want[i])
				}
			}
		})
	}

	// A CSBK that is not a preamble — a radio check, a call alert — goes out
	// the moment it arrives.
	notPreamble := pre(hbp.Timeslot2)
	block, err := dmrfec.BuildPreamble(dmrfec.Preamble{To: 2, From: 9990, Group: true})
	if err != nil {
		t.Fatalf("%v", err)
	}
	block[0] = 0x80 | 4
	crc, _ := dmrfec.CSBKCRC(block)
	block[10], block[11] = byte(crc>>8), byte(crc)
	burst, err := dmrfec.BuildDataBurstFromBlock(11, dmrfec.DataTypeCSBK, block)
	if err != nil {
		t.Fatalf("%v", err)
	}
	copy(notPreamble.Payload[:], burst)
	if got := len(newPreambleGate().pass(1, notPreamble, t0)); got != 1 {
		t.Errorf("a CSBK that is not a preamble released %d frames, want 1", got)
	}
}
