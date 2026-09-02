package ipsc_test

import (
	"encoding/binary"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

const twoPeers = "../../../testdata/ipsc/ipsc-two-peers.pcap"

// TestVoiceIsNotAMesh is the finding that decided how the reverse path must be
// built.
//
// **Three repeaters were registered and two transmitted at the same time, and
// every voice frame went to the master.** Not one travelled peer to peer. If
// IP Site Connect exchanged voice directly between peers, the format QSP has to
// send would be the format it already receives, fully decoded, and there would
// be nothing left to find out. It does not, so a master relaying voice is a
// direction nothing has yet captured.
//
// This capture cannot prove the negative on its own — it was taken on the
// master, and traffic between two other hosts would never reach it. What it
// does prove is that both peers addressed the master while transmitting, which
// is what a relayed architecture looks like and what a mesh would not need.
func TestVoiceIsNotAMesh(t *testing.T) {
	sum := readCapture(t, twoPeers)

	const master = "192.168.1.247"
	var toMaster, elsewhere int
	senders := map[uint32]int{}
	for _, p := range sum.UDP {
		if len(p.Payload) == 0 || ipsc.Kind(p.Payload[0]) != ipsc.KindVoice {
			continue
		}
		if p.DstIP == master {
			toMaster++
			senders[binary.BigEndian.Uint32(p.Payload[1:5])]++
			continue
		}
		elsewhere++
		t.Errorf("a voice frame went from %s to %s, which is peer to peer", p.SrcIP, p.DstIP)
	}
	if toMaster == 0 {
		t.Fatal("no voice frames in the capture")
	}
	if len(senders) < 2 {
		t.Fatalf("only %d peer(s) transmitted; the finding needs at least two", len(senders))
	}
	t.Logf("%d voice frames to the master, %d elsewhere, from %d peers: %v",
		toMaster, elsewhere, len(senders), senders)

	// And the master relayed none of it, which is the current behaviour and
	// the reason two Motorola operators cannot hear each other.
	var fromMaster int
	for _, p := range sum.UDP {
		if p.SrcIP == master && len(p.Payload) > 0 && ipsc.Kind(p.Payload[0]) == ipsc.KindVoice {
			fromMaster++
		}
	}
	if fromMaster != 0 {
		t.Errorf("the master sent %d voice frames; QSP does not do that yet, so the "+
			"capture is not what its provenance says", fromMaster)
	}
}

// TestATransmissionIsThreeHeadersASuperframeCycleAndATerminator records the
// shape of a Motorola transmission over IPSC.
//
// A transmission is three header frames, then a repeating six-frame cycle whose
// payload lengths run 52, 57, 57, 57, 66, 57, then one terminator. Six is a DMR
// superframe, and the length varies with the position in it — so these are not
// four unrelated frame types but one voice frame carrying different embedded
// signalling depending on where it sits.
//
// **Motorola sends the header three times, not once.** A QSP master relaying
// voice to a repeater will have to decide whether to do the same, and that is
// exactly the kind of question a capture answers and a guess does not.
func TestATransmissionIsThreeHeadersASuperframeCycleAndATerminator(t *testing.T) {
	sum := readCapture(t, twoPeers)

	type frame struct {
		length int
		flags  uint16
		slot   byte
	}
	streams := map[uint16][]frame{}
	order := []uint16{}
	for _, p := range sum.UDP {
		b := p.Payload
		if len(b) < 20 || ipsc.Kind(b[0]) != ipsc.KindVoice {
			continue
		}
		if binary.BigEndian.Uint32(b[1:5]) != 315544 {
			continue
		}
		id := binary.BigEndian.Uint16(b[15:17])
		if _, seen := streams[id]; !seen {
			order = append(order, id)
		}
		streams[id] = append(streams[id], frame{
			length: len(b),
			flags:  binary.BigEndian.Uint16(b[18:20]),
			slot:   b[17],
		})
	}
	if len(order) == 0 {
		t.Fatal("no transmissions from the remote repeater")
	}

	var complete int
	for _, id := range order {
		f := streams[id]
		if len(f) < 10 {
			continue // a transmission the capture caught only part of
		}
		complete++

		// Three headers, the first flagged as the first frame.
		for i := 0; i < 3; i++ {
			if f[i].length != 54 {
				t.Errorf("stream %#04x frame %d is %d bytes, want a 54-byte header",
					id, i, f[i].length)
			}
		}
		if f[0].flags != 0x80dd {
			t.Errorf("stream %#04x opens with flags %#04x, want the first-frame flag 0x80dd",
				id, f[0].flags)
		}

		// One terminator, carrying the last-frame bit alongside the slot bit.
		last := f[len(f)-1]
		if last.flags != 0x805e {
			t.Errorf("stream %#04x closes with flags %#04x, want 0x805e", id, last.flags)
		}
		if last.slot&ipsc.FlagTerminator == 0 {
			t.Errorf("stream %#04x closes with byte 17 %#02x, want the terminator bit set",
				id, last.slot)
		}

		// The middle is a six-frame cycle by length.
		want := []int{52, 57, 57, 57, 66, 57}
		body := f[3 : len(f)-1]
		for i, fr := range body {
			if fr.length != want[i%6] {
				t.Errorf("stream %#04x body frame %d is %d bytes, want %d at cycle position %d",
					id, i, fr.length, want[i%6], i%6)
			}
		}
	}
	t.Logf("%d complete transmission(s) matched three headers, a 52/57/57/57/66/57 cycle, and a terminator",
		complete)
	if complete == 0 {
		t.Fatal("no complete transmission in the capture")
	}
}
