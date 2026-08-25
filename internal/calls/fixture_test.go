package calls_test

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/calls"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// TestAgainstTheCapturedVoiceSession replays real traffic through the tracker.
//
// The fixture contains seven transmissions, each traversing two links, and each
// carrying exactly two sync frames. A tracker that reassembles them correctly
// must find fourteen calls and time none of them out: every one has a
// terminator.
//
// This is the difference between a tracker that works on frames I invented and
// one that works on frames a radio produced.
func TestAgainstTheCapturedVoiceSession(t *testing.T) {
	packets := readFixture(t, "../../testdata/hbp/hbp-voice-session.pcap")

	tr := calls.NewTracker(calls.Options{History: 100})
	var started, ended int
	now := time.Date(2026, 8, 23, 20, 0, 0, 0, time.UTC)

	for _, p := range packets {
		msg, err := hbp.Parse(p.payload)
		if err != nil {
			continue
		}
		d, ok := msg.(hbp.Data)
		if !ok {
			continue
		}
		// The capture's own timestamps preserve real inter-frame timing.
		at := now.Add(time.Duration(p.micros-packets[0].micros) * time.Microsecond)

		// Peer identity differs per link, exactly as a relay produces; using it
		// keeps the two traversals of each stream separate.
		s, e := tr.Update(hbp.RepeaterID(p.flow), d, at)
		if s != nil {
			started++
		}
		if e != nil {
			ended++
		}
	}

	if started != 14 {
		t.Errorf("started %d calls, want 14 (7 streams over 2 links)", started)
	}
	if ended != 14 {
		t.Errorf("ended %d calls, want 14", ended)
	}
	if tr.ActiveCount() != 0 {
		t.Errorf("%d calls left open; every captured stream has a terminator", tr.ActiveCount())
	}

	for _, c := range tr.History() {
		if c.EndReason != calls.EndTerminated {
			t.Errorf("call %s ended as %q, want terminated", c.Key, c.EndReason)
		}
		if c.Source != 3132910 {
			t.Errorf("call source = %d, want 3132910", c.Source)
		}
		if c.Target != 9999 {
			t.Errorf("call target = %d, want 9999", c.Target)
		}
		if c.Frames < 2 {
			t.Errorf("call %s has %d frames", c.Key, c.Frames)
		}
	}
}

type fixturePacket struct {
	payload []byte
	micros  uint64
	flow    uint16
}

func readFixture(t *testing.T, path string) []fixturePacket {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	if len(raw) < 24 || binary.LittleEndian.Uint32(raw[20:24]) != 276 {
		t.Fatalf("%s is not a LINUX_SLL2 capture", path)
	}

	var out []fixturePacket
	off := 24
	for off+16 <= len(raw) {
		sec := uint64(binary.LittleEndian.Uint32(raw[off : off+4]))
		usec := uint64(binary.LittleEndian.Uint32(raw[off+4 : off+8]))
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl

		const sll2 = 20
		if len(rec) < sll2+20 || binary.BigEndian.Uint16(rec[0:2]) != 0x0800 {
			continue
		}
		ip := rec[sll2:]
		ihl := int(ip[0]&0x0F) * 4
		if ihl < 20 || len(ip) < ihl+8 || ip[9] != 17 {
			continue
		}
		udp := ip[ihl:]
		length := int(binary.BigEndian.Uint16(udp[4:6]))
		if length < 8 || length > len(udp) {
			continue
		}
		out = append(out, fixturePacket{
			payload: udp[8:length],
			micros:  sec*1_000_000 + usec,
			flow:    binary.BigEndian.Uint16(udp[0:2]),
		})
	}
	if len(out) == 0 {
		t.Fatalf("%s yielded no packets", path)
	}
	return out
}
