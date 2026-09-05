package ipsc_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// The fixtures, and the equipment behind them.
//
// Phase 1 is one repeater talking to a host running nothing: two captures
// differing in exactly one setting, which is what named the sender ID field.
// Phase 2 is two repeaters registered to each other over the internet, which is
// what produced every message type beyond the first.
const (
	phase1A = "../../../testdata/ipsc/ipsc-phase1-a-id100.pcap"
	phase1B = "../../../testdata/ipsc/ipsc-phase1-b-id3132910.pcap"

	rehearsal    = "../../../testdata/ipsc/ipsc-rehearsal-two-peers.pcap"
	notBound     = "../../../testdata/ipsc/ipsc-phase2-master-not-bound.pcap"
	registration = "../../../testdata/ipsc/ipsc-phase2-registration.pcap"
	established  = "../../../testdata/ipsc/ipsc-phase2-established.pcap"

	// The first capture of QSP software answering a Motorola repeater, and of
	// voice crossing IPSC. cmd/ipsc-probe as master, an XPR8300 as peer.
	probeVoice = "../../../testdata/ipsc/ipsc-probe-voice.pcap"

	// Two private calls in opposite directions with group calls either side,
	// which is what named 0x81.

	// K9MLS's XPR8300, firmware R02.30.20. Master in phase 2.
	masterID = 3132910
	// The remote repeater, reached over the internet. Peer in phase 2.
	remotePeerID = 315544
	// The XPR8300's ID during phase 1 capture A, before it was changed.
	phase1AID = 100
)

func allFixtures() []string {
	return []string{phase1A, phase1B, rehearsal, notBound, registration, established,
		probeVoice, privateVoice}
}

// TestEveryCapturedMessageRoundTrips is the property that keeps the parser
// honest about the bytes it does not understand: whatever they mean, they
// survive being read and written unchanged.
func TestEveryCapturedMessageRoundTrips(t *testing.T) {
	for _, path := range allFixtures() {
		cap := readCapture(t, path)
		for _, pkt := range cap.UDP {
			msg, err := ipsc.Parse(pkt.Payload)
			if err != nil {
				t.Fatalf("%s packet %d: %v", path, pkt.Index, err)
			}
			if got := msg.Marshal(); !bytes.Equal(got, pkt.Payload) {
				t.Errorf("%s packet %d: round trip produced % x, want % x",
					path, pkt.Index, got, pkt.Payload)
			}
		}
	}
}

// TestTheSenderIDIsTheSenderNotTheSubject is the finding that a single capture
// could not have produced.
//
// Phase 1 established that bytes 1 to 4 track the repeater's configured Radio
// ID, by changing it. But one repeater talking into silence cannot show whether
// the field means "who sent this" or "who this concerns", because there was
// only ever one party. Phase 2 settles it: in the same exchange, seconds apart,
// the peer's messages carry the peer's ID and the master's carry the master's.
func TestTheSenderIDIsTheSenderNotTheSubject(t *testing.T) {
	const master = "192.168.1.233"
	for _, path := range []string{registration, established} {
		cap := readCapture(t, path)
		var fromMaster, fromPeer int
		for _, pkt := range cap.UDP {
			msg, err := ipsc.Parse(pkt.Payload)
			if err != nil {
				t.Fatalf("%s packet %d: %v", path, pkt.Index, err)
			}
			want := uint32(remotePeerID)
			if pkt.SrcIP == master {
				want = masterID
				fromMaster++
			} else {
				fromPeer++
			}
			if msg.SenderID != want {
				t.Errorf("%s packet %d from %s: sender ID %d, want %d",
					path, pkt.Index, pkt.SrcIP, msg.SenderID, want)
			}
		}
		if fromMaster == 0 || fromPeer == 0 {
			t.Fatalf("%s: %d from master and %d from peer; both directions are the point of this test",
				path, fromMaster, fromPeer)
		}
	}
}

// TestPhase1CarriesTheConfiguredRadioID keeps the differential that named the
// field in the first place.
func TestPhase1CarriesTheConfiguredRadioID(t *testing.T) {
	for _, tc := range []struct {
		path string
		want uint32
	}{
		{phase1A, phase1AID},
		{phase1B, masterID},
	} {
		cap := readCapture(t, tc.path)
		for _, pkt := range cap.UDP {
			msg, err := ipsc.Parse(pkt.Payload)
			if err != nil {
				t.Fatalf("%s packet %d: %v", tc.path, pkt.Index, err)
			}
			if msg.SenderID != tc.want {
				t.Errorf("%s packet %d: sender ID %d, want %d", tc.path, pkt.Index, msg.SenderID, tc.want)
			}
		}
	}
}

// TestEveryObservedKindAppearsInAFixture is the guard against the constant list
// drifting away from the evidence.
//
// A kind this package recognises but no capture contains is a guess wearing a
// constant's clothes, which is the exact failure ADR-0029 exists to prevent.
func TestEveryObservedKindAppearsInAFixture(t *testing.T) {
	seen := map[ipsc.Kind]int{}
	for _, path := range allFixtures() {
		cap := readCapture(t, path)
		for _, pkt := range cap.UDP {
			msg, err := ipsc.Parse(pkt.Payload)
			if err != nil {
				t.Fatalf("%s packet %d: %v", path, pkt.Index, err)
			}
			seen[msg.Kind]++
		}
	}
	for _, k := range []ipsc.Kind{
		ipsc.KindRegisterRequest, ipsc.KindRegisterReply,
		ipsc.KindKeepaliveRequest, ipsc.KindKeepaliveReply,
		ipsc.Kind85, ipsc.KindF0, ipsc.KindF1, ipsc.KindVoice,
	} {
		if seen[k] == 0 {
			t.Errorf("kind %#02x is recognised by this package but appears in no fixture", byte(k))
		}
	}
}

// TestEachKindWasCapturedAtItsObservedLength records the lengths without
// enforcing them.
//
// The parser deliberately accepts any length past the five-byte header:
// KindF1 was captured with one peer registered, and a master that knows three
// repeaters may well send a longer one. Rejecting that would break QSP on the
// day IPSC starts being worth having.
func TestEachKindWasCapturedAtItsObservedLength(t *testing.T) {
	for _, path := range allFixtures() {
		cap := readCapture(t, path)
		for _, pkt := range cap.UDP {
			msg, err := ipsc.Parse(pkt.Payload)
			if err != nil {
				t.Fatalf("%s packet %d: %v", path, pkt.Index, err)
			}
			want, ok := ipsc.ObservedLen(msg.Kind)
			if !ok {
				t.Fatalf("%s packet %d: kind %#02x parsed but has no observed length",
					path, pkt.Index, byte(msg.Kind))
			}
			// A zero observed length means the kind has no single one. Voice
			// frames were seen at 52, 54, 57 and 66 bytes across one
			// superframe: the burst payload varies with the burst.
			if want == 0 {
				continue
			}
			if len(pkt.Payload) != want {
				t.Errorf("%s packet %d: kind %#02x is %d bytes, observed length is %d",
					path, pkt.Index, byte(msg.Kind), len(pkt.Payload), want)
			}
		}
	}
}

// TestALongerMessageIsStillParsed is the other half of the rule above: the
// observed lengths are recorded, not enforced.
func TestALongerMessageIsStillParsed(t *testing.T) {
	long := append([]byte{byte(ipsc.KindF1), 0x00, 0x2f, 0xcd, 0xee}, bytes.Repeat([]byte{0x5a}, 80)...)
	msg, err := ipsc.Parse(long)
	if err != nil {
		t.Fatalf("a longer KindF1 was rejected: %v", err)
	}
	if msg.SenderID != masterID {
		t.Errorf("sender ID %d, want %d", msg.SenderID, masterID)
	}
	if got := msg.Marshal(); !bytes.Equal(got, long) {
		t.Errorf("round trip produced % x, want % x", got, long)
	}
}

// TestRegisteredAndUnregisteredRunOnDifferentClocks is the timing finding that
// two captures of one state could not have produced.
//
// An unregistered peer retries at ten seconds. A registered one sends a
// keepalive at fifteen. An implementation that used one interval for both would
// be wrong in whichever state it was not written for, and would look correct in
// testing against itself.
func TestRegisteredAndUnregisteredRunOnDifferentClocks(t *testing.T) {
	unregistered := gapsFor(t, phase1A, ipsc.KindRegisterRequest)
	assertCadence(t, "unregistered retry", unregistered, 10_000_000, 50_000)

	registered := gapsFor(t, established, ipsc.KindKeepaliveRequest)
	assertCadence(t, "registered keepalive", registered, 15_000_000, 50_000)
}

// TestTheMasterAnswersKeepalivesPromptly checks that every keepalive in the
// twenty-minute capture drew a reply, which is what "the link stayed up" means
// in bytes.
func TestTheMasterAnswersKeepalivesPromptly(t *testing.T) {
	cap := readCapture(t, established)
	var requests, replies int
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		switch msg.Kind {
		case ipsc.KindKeepaliveRequest:
			requests++
		case ipsc.KindKeepaliveReply:
			replies++
		}
	}
	if requests == 0 {
		t.Fatal("no keepalive requests in the established capture")
	}
	if requests != replies {
		t.Errorf("%d keepalive requests against %d replies", requests, replies)
	}
}

// TestTheMasterNotBoundCaptureContainsNoReply keeps the failure that cost an
// afternoon.
//
// The XPR8300's CPS has two port fields: the master port it dials and the port
// it binds. They were 50000 and 50001, so a master that looked correctly
// configured served a port nobody was calling. Nothing but ICMP came back, and
// this fixture is what that looks like — worth keeping so the next person
// recognises it in minutes rather than hours.
func TestTheMasterNotBoundCaptureContainsNoReply(t *testing.T) {
	cap := readCapture(t, notBound)
	for _, pkt := range cap.UDP {
		if pkt.SrcIP == "192.168.1.233" {
			t.Errorf("packet %d: the master sent UDP, but this fixture records it never binding", pkt.Index)
		}
	}
	if cap.ICMP == 0 {
		t.Error("no ICMP in a fixture whose whole content is a port being refused")
	}
}

// TestTwoRepeatersDisagreeOnTheUnexplainedBytes is why the parser does not
// enforce a body it has only seen from one repeater.
//
// The rehearsal capture holds registration requests from both an XPR8300 and a
// different remote repeater. Their bodies differ. Patch 0167 recorded one
// repeater's trailer and deliberately did not enforce it; four hours later a
// second repeater disagreed with it.
func TestTwoRepeatersDisagreeOnTheUnexplainedBytes(t *testing.T) {
	cap := readCapture(t, rehearsal)
	bodies := map[uint32]string{}
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			t.Fatalf("packet %d: %v", pkt.Index, err)
		}
		if msg.Kind != ipsc.KindRegisterRequest {
			continue
		}
		bodies[msg.SenderID] = string(msg.Body)
	}
	if len(bodies) < 2 {
		t.Fatalf("expected registration requests from two repeaters, got %d", len(bodies))
	}
	distinct := map[string]bool{}
	for _, b := range bodies {
		distinct[b] = true
	}
	if len(distinct) < 2 {
		t.Error("both repeaters sent identical bodies; the parser could have enforced one after all")
	}
}

// TestUncapturedMessageTypesAreRefused is the ADR-0029 boundary as a test.
func TestUncapturedMessageTypesAreRefused(t *testing.T) {
	for _, lead := range []byte{0x00, 0x82, 0x92, 0x93, 0x9a, 0xff} {
		_, err := ipsc.Parse(append([]byte{lead}, bytes.Repeat([]byte{0}, 13)...))
		if !errors.Is(err, ipsc.ErrNotCaptured) {
			t.Errorf("leading byte %#02x: %v, want ErrNotCaptured", lead, err)
		}
	}
}

// TestMalformedInputIsRejected covers the lengths a socket can deliver.
func TestMalformedInputIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"type byte only", []byte{0x90}},
		{"one byte short of a header", bytes.Repeat([]byte{0x90}, 4)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ipsc.Parse(tc.in); !errors.Is(err, ipsc.ErrShort) {
				t.Errorf("got %v, want ErrShort", err)
			}
		})
	}
}

// FuzzParse checks that no input panics and that anything accepted round-trips.
func FuzzParse(f *testing.F) {
	for _, path := range allFixtures() {
		cap, err := readCaptureRaw(path)
		if err != nil {
			f.Fatalf("%v", err)
		}
		for _, pkt := range cap.UDP {
			f.Add(pkt.Payload)
		}
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		msg, err := ipsc.Parse(in)
		if err != nil {
			return
		}
		if got := msg.Marshal(); !bytes.Equal(got, in) {
			t.Fatalf("round trip produced % x, want % x", got, in)
		}
	})
}

func gapsFor(tb testing.TB, path string, kind ipsc.Kind) []int64 {
	tb.Helper()
	cap := readCapture(tb, path)
	var last uint64
	var gaps []int64
	for _, pkt := range cap.UDP {
		msg, err := ipsc.Parse(pkt.Payload)
		if err != nil {
			tb.Fatalf("%s packet %d: %v", path, pkt.Index, err)
		}
		if msg.Kind != kind {
			continue
		}
		if last != 0 {
			gaps = append(gaps, int64(pkt.Micros)-int64(last))
		}
		last = pkt.Micros
	}
	return gaps
}

func assertCadence(tb testing.TB, what string, gaps []int64, want, tolerance int64) {
	tb.Helper()
	if len(gaps) < 5 {
		tb.Fatalf("%s: %d intervals, too few to call it a cadence", what, len(gaps))
	}
	for i, g := range gaps {
		if d := g - want; d < -tolerance || d > tolerance {
			tb.Errorf("%s: interval %d was %d us, want %d +/- %d", what, i, g, want, tolerance)
		}
	}
}
