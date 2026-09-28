package tms_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"sort"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/tms"
)

// Group text messages, from the calibration capture of 2026-09-27.
//
// Six messages were sent one character apart on purpose, to give a CRC search
// same-length pairs. They arrived as something this project had not seen: a
// **group** text goes out as Rate 1/2 **unconfirmed** blocks, twelve octets
// each with no serial number and no CRC-9, addressed to 225.0.0.\<talkgroup\>
// rather than to Motorola's radio-IP prefix, with the TMS header's third octet
// reading 0xa0 where a private message reads 0xe0, and a time to live of one.
//
// None of that was guessed. It is why `internal/tms` after patch 0433 would
// have refused every group message on this network, which nothing noticed
// because no fixture held one.
//
// The texts below are exactly what the operator typed. An earlier version of
// this comment claimed the radio had autocapitalised "AAAA" into "Aaaa" and
// that text entry was therefore not literal — **that was wrong**, corrected on
// the operator's word: he typed "Aaaa", the first letter capitalised because
// that is how he typed it, and did not bother shifting the rest. The radio
// transmitted what it was given.
//
// The leading space on " Aaaa" is recorded as observed and unexplained. It may
// have been typed. Nothing here attributes it to the radio, because nothing
// here knows.
const captureGroup = "../../testdata/ipsc/ipsc-text-group-cal.pcap"

const (
	dataTypeRate12 = 7
	rate12Block    = 12
)

// groupTransmission is one Rate 1/2 message: its header and its blocks in
// arrival order, because an unconfirmed block carries no serial to sort by.
type groupTransmission struct {
	header []byte
	blocks [][]byte
	sid    string
}

func readGroupTransmissions(tb testing.TB, path string) []groupTransmission {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading %s: %v", path, err)
	}
	if link := binary.LittleEndian.Uint32(raw[20:24]); link != 276 {
		tb.Fatalf("%s: link type %d, want 276 (LINUX_SLL2)", path, link)
	}
	type acc struct {
		header []byte
		blocks [][]byte
	}
	byStream := map[string]*acc{}
	order := []string{}
	for off := 24; off+16 <= len(raw); {
		caplen := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+caplen > len(raw) {
			break
		}
		packet := raw[off : off+caplen]
		off += caplen
		if len(packet) < sll2HeaderBytes+28 {
			continue
		}
		ip := packet[sll2HeaderBytes:]
		udp := ip[int(ip[0]&0x0f)*4:]
		ulen := int(binary.BigEndian.Uint16(udp[4:6]))
		if ulen < 8 || ulen > len(udp) {
			continue
		}
		dg := udp[8:ulen]
		if len(dg) != bptcDatagramBytes {
			continue
		}
		sid := string(dg[12:16])
		a, ok := byStream[sid]
		if !ok {
			a = &acc{}
			byStream[sid] = a
			order = append(order, sid)
		}
		switch dg[dataTypeAt] & 0x0f {
		case dataTypeHeader:
			a.header = append([]byte(nil), dg[blockAt:blockAt+headerBlockBytes]...)
		case dataTypeRate12:
			a.blocks = append(a.blocks, append([]byte(nil), dg[blockAt:blockAt+rate12Block]...))
		}
	}
	out := []groupTransmission{}
	for _, sid := range order {
		a := byStream[sid]
		if a.header == nil {
			continue
		}
		h, err := dmrfec.ParseDataHeader(a.header)
		if err != nil || h.Confirmed || h.SAP != dmrfec.SAPIPPacketData {
			continue
		}
		// The header states the block count, and a stream missing one of
		// them cannot be detected any other way: an unconfirmed block has no
		// serial and no CRC of its own.
		if len(a.blocks) != int(h.Blocks) {
			continue
		}
		out = append(out, groupTransmission{header: a.header, blocks: a.blocks, sid: sid})
	}
	return out
}

// TestEveryCapturedGroupTextRoundTrips is the same assertion as the private
// case and over a different block format: reassemble, split, parse, rebuild,
// rejoin, re-cut, and compare every octet but the four of the packet CRC.
func TestEveryCapturedGroupTextRoundTrips(t *testing.T) {
	texts := []string{}
	for _, tr := range readGroupTransmissions(t, captureGroup) {
		h, err := dmrfec.ParseDataHeader(tr.header)
		if err != nil {
			t.Fatalf("%s: header: %v", tr.sid, err)
		}

		userData, err := dmrfec.Rate12UserData(tr.blocks)
		if err != nil {
			t.Fatalf("%s: reassembling: %v", tr.sid, err)
		}
		total := int(binary.BigEndian.Uint16(userData[2:4]))
		payload, pad, crc, err := dmrfec.SplitPacket(userData, total)
		if err != nil {
			t.Fatalf("%s: splitting: %v", tr.sid, err)
		}
		if len(pad) != int(h.Pad) {
			t.Errorf("%s: %d pad octets against a header stating %d", tr.sid, len(pad), h.Pad)
		}
		if derived := dmrfec.PadOctets(total, int(h.Blocks), dmrfec.Rate12DataBytes); derived != int(h.Pad) {
			t.Errorf("%s: PadOctets derives %d and the header says %d", tr.sid, derived, h.Pad)
		}

		msg, err := tms.Parse(payload)
		if err != nil {
			t.Fatalf("%s: parsing: %v", tr.sid, err)
		}
		if !msg.Group {
			t.Errorf("%s: parsed as a private message", tr.sid)
		}
		if msg.To != h.To {
			t.Errorf("%s: payload addresses talkgroup %d and the header says %d", tr.sid, msg.To, h.To)
		}
		texts = append(texts, msg.Text)

		rebuilt, err := tms.Build(msg)
		if err != nil {
			t.Fatalf("%s: building: %v", tr.sid, err)
		}
		if !bytes.Equal(rebuilt, payload) {
			t.Errorf("%s: rebuilt datagram differs\n have %x\n want %x", tr.sid, rebuilt, payload)
			continue
		}
		joined, err := dmrfec.JoinPacket(rebuilt, len(tr.blocks), dmrfec.Rate12DataBytes, crc)
		if err != nil {
			t.Fatalf("%s: joining: %v", tr.sid, err)
		}
		if !bytes.Equal(joined, userData) {
			t.Errorf("%s: rejoined user data differs\n have %x\n want %x", tr.sid, joined, userData)
			continue
		}
		again, err := dmrfec.Rate12Blocks(joined)
		if err != nil {
			t.Fatalf("%s: segmenting: %v", tr.sid, err)
		}
		if len(again) != len(tr.blocks) {
			t.Fatalf("%s: rebuilt %d blocks, captured %d", tr.sid, len(again), len(tr.blocks))
		}
		for i := range tr.blocks {
			if !bytes.Equal(again[i], tr.blocks[i]) {
				t.Errorf("%s: block %d differs\n have %x\n want %x", tr.sid, i, again[i], tr.blocks[i])
			}
		}
	}

	// The six texts the operator sent, exactly as typed and exactly as
	// transmitted. They are recorded unaltered — mixed case, leading space
	// and all — because a fixture has to say what was on the wire rather than
	// what would have been tidier.
	sort.Strings(texts)
	want := []string{" Aaaa", "A", "Aa", "Aaa ", "Aaab", "Baaa"}
	if len(texts) != len(want) {
		t.Fatalf("decoded %d group messages, want %d: %q", len(texts), len(want), texts)
	}
	for i := range want {
		if texts[i] != want[i] {
			t.Errorf("message %d decoded to %q, want %q", i, texts[i], want[i])
		}
	}
}

// TestAGroupMessageIsNotAPrivateOne guards the thing 0433 got wrong: the
// address prefix and the TMS call-type octet both carry the distinction, and a
// message where they disagree is refused rather than guessed at.
func TestAGroupMessageIsNotAPrivateOne(t *testing.T) {
	group, err := tms.Build(tms.Message{From: 3132910, To: 2, Group: true, Reference: 0x8a, Text: "Aaab"})
	if err != nil {
		t.Fatalf("building a group message: %v", err)
	}
	private, err := tms.Build(tms.Message{From: 3132910, To: 3155373, Reference: 0x84, Text: "Aaab"})
	if err != nil {
		t.Fatalf("building a private message: %v", err)
	}
	if bytes.Equal(group, private) {
		t.Fatal("a group and a private message with the same text built identical datagrams")
	}
	if group[16] == private[16] {
		t.Errorf("both address prefixes read %#02x", group[16])
	}
	if group[30] == private[30] {
		t.Errorf("both call-type octets read %#02x", group[30])
	}

	// A datagram whose address says group and whose header says private must
	// not parse. Before this patch nothing looked at the pair.
	mixed := append([]byte(nil), group...)
	mixed[30] = private[30]
	// The UDP checksum covers the payload, so repair it the way a forger
	// would have to, leaving only the disagreement itself to be caught.
	mixed[26], mixed[27] = 0, 0
	if _, err := tms.Parse(mixed); err == nil {
		t.Error("parsed a group address carrying a private call type")
	}
}
