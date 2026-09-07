package ipsclink_test

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/ipsclink"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// rate34TextBodies returns the bodies of one whole text transmission — the
// preamble CSBKs, the data header and the Rate 3/4 content blocks — from the
// capture that holds them.
//
// It reads a single stream ID, because a stream is a transmission: 154
// datagrams in that fixture group into 16 transmissions by stream ID alone,
// exactly as voice does.
func rate34TextBodies(tb testing.TB) (bodies [][]byte, stream uint16) {
	tb.Helper()
	raw, err := os.ReadFile("../../testdata/ipsc/ipsc-text-rate34.pcap")
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
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		// Only the repeater's own datagrams, not QSP's relayed copies.
		if binary.BigEndian.Uint32(ip[12:16]) != 0xc0a801e9 {
			continue
		}
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 9 {
			continue
		}
		pl := udp[8:]
		if ipsc.Kind(pl[0]) != ipsc.KindTextPrivate || len(pl) < 19 {
			continue
		}
		id := binary.BigEndian.Uint16(pl[15:17])
		if stream == 0 {
			stream = id
		}
		if id != stream {
			if len(bodies) > 0 {
				break
			}
			continue
		}
		bodies = append(bodies, append([]byte(nil), pl[ipsc.HeaderLen:]...))
	}
	if len(bodies) < 4 {
		tb.Fatalf("the fixture yielded %d datagrams for stream %#04x, want at least 4",
			len(bodies), stream)
	}
	return bodies, stream
}

// TestATextIsRecordedAsOneEvent is what was missing.
//
// A text from a repeater produced **no journal line, no counter and no colour
// code**: the branch converted the burst and returned. An operator watching the
// console could not tell a working text path from a broken one, which is the
// condition that hid ADR-0047's defect for eighteen patches — the network could
// not send a text for that whole time and nothing said so.
//
// Twenty-three datagrams of one transmission must produce one record, not
// twenty-three. A text is many datagrams and one event.
func TestATextIsRecordedAsOneEvent(t *testing.T) {
	l, conn := start(t, ipsclink.Config{
		Bridge: ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: true},
	})

	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	bodies, _ := rate34TextBodies(t)
	for _, body := range bodies {
		send(t, conn, ipsc.KindTextPrivate, peerID, body)
	}

	var peer ipsclink.Peer
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range l.Peers() {
			if p.RadioID == peerID {
				peer = p
			}
		}
		if peer.TextFrames >= uint64(len(bodies)) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	if peer.TextFrames != uint64(len(bodies)) {
		t.Errorf("%d of %d text datagrams were counted", peer.TextFrames, len(bodies))
	}
	// **Separate from VoiceFrames rather than added into it.** That counter is
	// documented as audio and the console draws it as "voice frames", so a
	// network whose text worked and whose audio did not would have read as
	// healthy.
	if peer.VoiceFrames != 0 {
		t.Errorf("%d text datagrams were counted as voice", peer.VoiceFrames)
	}

	if peer.LastCall == nil {
		t.Fatal("a whole text transmission left no record at all")
	}
	// Recorded as an instant: started and ended together. No reliable
	// end-of-transmission marker exists for a text — the flags bit that looks
	// like one holds for nine of the sixteen transmissions in this fixture —
	// and a record left open would sit in the console as an active call for
	// minutes after a message that took a second.
	if peer.LastCall.Ended.IsZero() {
		t.Error("the text record was left open; it would show as an active call")
	}
	if peer.LastCall.Frames != uint64(len(bodies)) {
		t.Errorf("the record counted %d datagrams, want %d",
			peer.LastCall.Frames, len(bodies))
	}
}

// TestATextTeachesTheColourCode is the one part of this an operator sees.
//
// The colour code was learned in recordVoice and nowhere else, so a repeater
// that had only ever sent text showed "not heard yet" in the peer table for as
// long as it stayed connected. Both Motorola peers on the live network read
// that way on 2026-09-07 while the text path was working perfectly.
func TestATextTeachesTheColourCode(t *testing.T) {
	l, conn := start(t, ipsclink.Config{
		Bridge: ipscbridge.Config{ColourCode: 4, SlotBitIsTimeslot2: true},
	})

	send(t, conn, ipsc.KindRegisterRequest, peerID, registerBody())
	expectReply(t, conn, ipsc.KindRegisterReply)

	bodies, _ := rate34TextBodies(t)
	for _, body := range bodies {
		send(t, conn, ipsc.KindTextPrivate, peerID, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, p := range l.Peers() {
			if p.RadioID == peerID && p.ColourCodeKnown {
				if p.ColourCode != 4 {
					t.Errorf("learned colour code %d, want 4", p.ColourCode)
				}
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("a whole text transmission taught the listener nothing about the colour code")
}
