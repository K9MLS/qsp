package peers

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// ipscDataFrames reads every data burst a Motorola repeater sent in a
// capture — preambles, private-call CSBKs, headers and blocks — and codes each
// as the Homebrew frame QSP would hand a hotspot, in capture order.
func ipscDataFrames(t *testing.T, path string) []hbp.Data {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v", err)
	}
	var out []hbp.Data
	for off := 24; off+16 <= len(raw); {
		caplen := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+caplen > len(raw) {
			break
		}
		packet := raw[off : off+caplen]
		off += caplen
		if len(packet) < 20+28 {
			continue
		}
		ip := packet[20:]
		if ip[0]>>4 != 4 || ip[9] != 17 {
			continue
		}
		dg := ip[int(ip[0]&0x0f)*4+8:]
		if len(dg) != 54 || (dg[0] != 0x83 && dg[0] != 0x84) {
			continue
		}
		dt := dg[30] & 0x0f
		if dt != dmrfec.DataTypeCSBK && dt != dmrfec.DataTypeDataHeader && dt != dmrfec.DataTypeRate12 {
			continue
		}
		burst, err := dmrfec.BuildDataBurstFromBlock(11, dt, dg[38:50])
		if err != nil {
			t.Fatalf("%v", err)
		}
		f := hbp.Data{Timeslot: hbp.Timeslot2, FrameType: hbp.FrameTypeSync, DataType: dt}
		copy(f.Payload[:], burst)
		out = append(out, f)
	}
	return out
}

// TestThePreambleGateChangesOnlyWhatMMDVMHostMultiplies replays every data
// burst from the two IPSC text captures through the gate — group texts,
// private texts, and a private exchange of call alerts, radio checks and
// answers — and holds it to one rule: a preamble announcing data is reduced
// to one per header, and every other frame reaches the hotspot at once and
// in order.
//
// The private exchange is what 0443 broke. Each of its requests is a
// preamble with the data bit clear followed by the CSBK itself, and no data
// header ever follows, so 0443 held those preambles and dropped them.
//
// To see it fail: change isDataPreamble back to isPreamble in pass, and every
// no-data preamble goes missing; or drop the DataFollows condition in
// isDataPreamble, with the same result.
func TestThePreambleGateChangesOnlyWhatMMDVMHostMultiplies(t *testing.T) {
	for _, path := range []string{
		"../../testdata/ipsc/ipsc-text.pcap",
		"../../testdata/ipsc/ipsc-text-rate34.pcap",
	} {
		t.Run(path[len("../../testdata/ipsc/"):], func(t *testing.T) {
			in := ipscDataFrames(t, path)
			var noData, headers int
			for _, f := range in {
				c, ok := dmrfec.CSBKOf(f.Payload[:])
				if f.DataType == dmrfec.DataTypeCSBK && ok && c.IsPreamble() && !c.DataFollows {
					noData++
				}
				if f.DataType == dmrfec.DataTypeDataHeader {
					headers++
				}
			}
			if noData == 0 || headers == 0 {
				t.Fatalf("the capture holds %d no-data preambles and %d headers; it should hold both", noData, headers)
			}

			// What the hotspot should receive: every frame except data
			// preambles, and before each header the data preamble most
			// recently held, if one was.
			var want []hbp.Data
			var held *hbp.Data
			for _, f := range in {
				if isDataPreamble(f) {
					f := f
					held = &f
					continue
				}
				if f.DataType == dmrfec.DataTypeDataHeader && held != nil {
					want = append(want, *held)
					held = nil
				}
				want = append(want, f)
			}

			g := newPreambleGate()
			now := time.Unix(0, 0)
			var got []hbp.Data
			for _, f := range in {
				got = append(got, g.pass(3132910, f, now)...)
				now = now.Add(60 * time.Millisecond)
			}
			if len(got) != len(want) {
				t.Fatalf("the hotspot got %d frames, want %d", len(got), len(want))
			}
			for i := range want {
				if got[i].Payload != want[i].Payload {
					t.Fatalf("frame %d differs from what the hotspot should get", i)
				}
			}

			// And the plain count, stated without the model above: every
			// no-data preamble arrives.
			arrived := 0
			for _, f := range got {
				c, ok := dmrfec.CSBKOf(f.Payload[:])
				if f.DataType == dmrfec.DataTypeCSBK && ok && c.IsPreamble() && !c.DataFollows {
					arrived++
				}
			}
			if arrived != noData {
				t.Errorf("%d of %d no-data preambles reached the hotspot", arrived, noData)
			}
		})
	}
}
