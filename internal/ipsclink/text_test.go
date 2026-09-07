package ipsclink_test

import (
	"encoding/binary"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// textBodies returns the bodies of real text bursts, ready to re-send at a
// listener as if the repeater had.
func textBodies(tb testing.TB, want ipsc.Kind, n int) [][]byte {
	tb.Helper()
	raw, err := os.ReadFile("../../testdata/ipsc/ipsc-text.pcap")
	if err != nil {
		tb.Fatalf("%v", err)
	}
	var out [][]byte
	for off := 24; off+16 <= len(raw) && len(out) < n; {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		// 54-byte datagrams only. This once said "a Rate 3/4 burst is refused\n		// by design", which stopped being true in ADR-0047; the other fixture\n		// carries those and TestATextIsRecordedAsOneEvent reads it.
		if len(udp) < 9 || ipsc.Kind(udp[8]) != want {
			continue
		}
		pl := udp[8:]
		if len(pl) != 54 {
			continue
		}
		out = append(out, append([]byte(nil), pl[ipsc.HeaderLen:]...))
	}
	if len(out) < n {
		tb.Fatalf("the fixture yielded %d bursts of kind %#02x, want %d",
			len(out), byte(want), n)
	}
	return out
}

// TestATextFromARepeaterReachesRouting is the wiring test.
//
// **Every other test here exercises the decoder and the converter**, and both
// passed for weeks while the listener dropped these datagrams as unrecognised.
// This one drives a real socket and asserts that a text handed to the listener
// comes out the other side as something routing can carry.
func TestATextFromARepeaterReachesRouting(t *testing.T) {
	var mu sync.Mutex
	var got []hbp.Data

	l, conn := start(t, ipsclink.Config{
		Bridge: ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: true},
		Deliver: func(_ hbp.RepeaterID, f hbp.Data) {
			mu.Lock()
			got = append(got, f)
			mu.Unlock()
		},
	})

	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	for _, body := range textBodies(t, ipsc.KindTextPrivate, 4) {
		send(t, conn, ipsc.KindTextPrivate, peerID, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(got) < 4 {
		t.Fatalf("%d of 4 text bursts reached routing; they used to be counted "+
			"as unrecognised and dropped", len(got))
	}
	for _, f := range got {
		if f.FrameType != hbp.FrameTypeSync {
			t.Errorf("a text arrived as frame type %v, want the data sync type",
				f.FrameType)
		}
		if f.CallType != hbp.CallPrivate {
			t.Error("a private text reached routing as a group call")
		}
	}

	// Nothing must still be counting these as unparsed, which is how they were
	// discarded before ADR-0045.
	if peers := l.Peers(); len(peers) != 1 {
		t.Fatalf("%d peers registered, want 1", len(peers))
	}
	if ignored, unparsed := l.Counters(); unparsed != 0 {
		t.Errorf("%d datagrams were still counted as unparsed (ignored %d); "+
			"text is recognised now", unparsed, ignored)
	}
}
