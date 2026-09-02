package ipscbridge_test

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// twoPeers is the richest IPSC capture held: three repeaters registered to QSP,
// two of them transmitting at once, from two different repeater models.
const twoPeers = "../../testdata/ipsc/ipsc-two-peers.pcap"

// twoPeerFrames returns every IPSC voice datagram in the capture, grouped by the
// address that sent it, in arrival order.
//
// The capture was taken with `tcpdump -i any` on a server with several
// interfaces, so it carries Linux cooked v2 headers rather than Ethernet. A
// reader that assumes Ethernet silently returns nothing here, which would make
// every assertion below vacuous.
func twoPeerFrames(tb testing.TB) map[string][][]byte {
	tb.Helper()
	raw, err := os.ReadFile(twoPeers)
	if err != nil {
		tb.Fatalf("%v", err)
	}
	if len(raw) < 24 {
		tb.Fatal("the capture is too short to hold a header")
	}
	if lt := binary.LittleEndian.Uint32(raw[20:24]); lt != 276 {
		tb.Fatalf("link type %d, want 276 (Linux cooked v2)", lt)
	}

	out := map[string][][]byte{}
	off := 24
	for off+16 <= len(raw) {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl

		// Linux cooked v2: a 20-byte header, then the network layer.
		if len(rec) < 20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[20:]
		if len(ip) < 20 || ip[9] != 17 {
			continue
		}
		src := net4(ip[12:16])
		udp := ip[(ip[0]&0x0f)*4:]
		if len(udp) < 8 {
			continue
		}
		payload := udp[8:]
		if len(payload) == 0 || payload[0] != byte(ipsc.KindVoice) {
			continue
		}
		out[src] = append(out[src], payload)
	}
	if len(out) < 2 {
		tb.Fatalf("the capture yielded %d transmitting peers, want at least 2", len(out))
	}
	return out
}

func net4(b []byte) string {
	const digits = "0123456789"
	out := make([]byte, 0, 15)
	for i, v := range b {
		if i > 0 {
			out = append(out, '.')
		}
		switch {
		case v >= 100:
			out = append(out, digits[v/100], digits[v/10%10], digits[v%10])
		case v >= 10:
			out = append(out, digits[v/10], digits[v%10])
		default:
			out = append(out, digits[v])
		}
	}
	return string(out)
}

// TestEveryFrameARepeaterSendsHasOneOfFourShapes is the measurement the encoder
// is built against, asserted rather than trusted.
//
// A header or terminator is 54 bytes. A voice frame is 52, 57 or 66, and which
// one is decided by the payload class at byte 32 — the shape is a consequence of
// the burst's position in its superframe, not an independent field. Byte 31
// carries the length on a voice frame and the timeslot on a header.
func TestEveryFrameARepeaterSendsHasOneOfFourShapes(t *testing.T) {
	byShape := map[[2]int]int{}
	for _, frames := range twoPeerFrames(t) {
		for _, f := range frames {
			if len(f) < 33 {
				t.Fatalf("a voice datagram is %d bytes", len(f))
			}
			marker, class := int(f[30]), 0
			if marker == 0x8a {
				class = int(f[32])
			}
			byShape[[2]int{marker, class}] = len(f)
		}
	}

	want := map[[2]int]int{
		{0x01, 0}:                               54, // voice header
		{0x02, 0}:                               54, // terminator
		{0x8a, int(ipsc.PayloadSync)}:           52,
		{0x8a, int(ipsc.PayloadFragment)}:       57,
		{0x8a, int(ipsc.PayloadFragmentWithLC)}: 66,
	}
	for k, w := range want {
		got, ok := byShape[k]
		if !ok {
			t.Errorf("the capture holds no frame with marker %#02x class %#02x", k[0], k[1])
			continue
		}
		if got != w {
			t.Errorf("marker %#02x class %#02x is %d bytes, want %d", k[0], k[1], got, w)
		}
	}
	for k, got := range byShape {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected shape: marker %#02x class %#02x, %d bytes", k[0], k[1], got)
		}
	}
}

// TestTheOutboundVoiceShapeMatchesARepeater takes a real repeater's audio,
// converts it to Homebrew bursts and back, and requires the datagram lengths QSP
// produces to be the ones the repeater itself produced.
//
// **This is the assertion the transmit path never had.** Until it existed QSP
// sent 66 bytes for every voice frame and 33 for every header, and nothing in a
// green test suite said otherwise.
func TestTheOutboundVoiceShapeMatchesARepeater(t *testing.T) {
	for host, frames := range twoPeerFrames(t) {
		t.Run(host, func(t *testing.T) {
			c, err := ipscbridge.New(ipscbridge.Config{ColourCode: 1})
			if err != nil {
				t.Fatalf("%v", err)
			}
			e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 1})

			var got []int
			for _, raw := range frames {
				m, err := ipsc.Parse(raw)
				if err != nil {
					continue
				}
				for _, burst := range c.Convert(m, hbp.RepeaterID(3132910)) {
					if burst.FrameType == hbp.FrameTypeSync {
						continue // signalling this package built, not audio
					}
					for _, out := range e.Encode(burst) {
						if _, _, _, ok := out.Payload(); ok {
							got = append(got, len(out.Marshal()))
						}
					}
				}
			}
			if len(got) < 12 {
				t.Fatalf("only %d voice frames produced; the fixture path is broken", len(got))
			}

			// The cycle, from the first synchronisation frame onward.
			start := -1
			for i, n := range got {
				if n == 52 {
					start = i
					break
				}
			}
			if start < 0 {
				t.Fatal("no 52-byte synchronisation frame was ever produced")
			}
			cycle := []int{52, 57, 57, 57, 66, 57}
			for i := start; i < len(got); i++ {
				if w := cycle[(i-start)%len(cycle)]; got[i] != w {
					t.Fatalf("frame %d of the cycle is %d bytes, want %d\nproduced: %v",
						i-start, got[i], w, got[start:min(len(got), start+18)])
				}
			}
		})
	}
}

// TestAVoiceHeaderIsTheBytesARepeaterSends rebuilds a header from its Link
// Control alone and requires it back byte for byte against one a real SLR5700
// sent, with only the two bytes this project cannot derive excluded.
//
// The captured header, bytes 30 to 53 of a 54-byte datagram from 198.51.100.2
// in ipsc-two-peers.pcap, source 0x3025ad, destination 2, colour code 1:
//
//	01 c0 00 0a 80 0a 00 60 00 00 00 00 00 02 30 25 ad ea d1 50 00 11 1e 7b
//	         |constants     | |Link Control block                | |ST| |tail|
//
// The Reed-Solomon parity `ea d1 50` is not copied from the capture: it is
// computed from the Link Control and masked for a voice header, and it comes
// out equal. Five headers and terminators from two models on two talkgroups
// agree.
func TestAVoiceHeaderIsTheBytesARepeaterSends(t *testing.T) {
	const (
		source      = 0x3025ad
		destination = 2
		colourCode  = 1
	)
	captured := []byte{
		0x01, 0xc0, 0x00, 0x0a, 0x80, 0x0a, 0x00, 0x60,
		0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0x30, 0x25, 0xad, 0xea, 0xd1, 0x50,
		0x00, 0x11,
	}

	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{
		ColourCode:         colourCode,
		SlotBitIsTimeslot2: true,
	})

	msgs := e.Encode(hbp.Data{
		SourceID:  source,
		TargetID:  destination,
		Timeslot:  hbp.Timeslot2, // the captured frame has the slot bit set
		FrameType: hbp.FrameTypeVoiceSync,
		StreamID:  0x5b4b,
		Payload:   syntheticBurst(),
	})
	if len(msgs) == 0 {
		t.Fatal("the first frame of a transmission produced nothing")
	}

	head := msgs[0].Marshal()
	if len(head) != ipsc.HeaderLenTotal {
		t.Fatalf("a voice header is %d bytes, want %d", len(head), ipsc.HeaderLenTotal)
	}
	for i, w := range captured {
		if got := head[30+i]; got != w {
			t.Errorf("byte %d is %#02x, want %#02x\n got %x\nwant %x",
				30+i, got, w, head[30:52], captured)
			break
		}
	}
	// The two bytes this project cannot derive are written as zero and are
	// deliberately not asserted against the capture.
	if head[52] != 0 || head[53] != 0 {
		t.Errorf("the unresolved tail is %02x %02x, want zero", head[52], head[53])
	}
}

// syntheticBurst is a 33-byte burst whose vocoder payload decodes, so that the
// encoder reaches the point of emitting a header. The audio is irrelevant here;
// the header is what is under test.
func syntheticBurst() [33]byte {
	var b [33]byte
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}
