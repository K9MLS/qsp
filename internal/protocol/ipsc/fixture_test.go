package ipsc_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// The two captures differ in exactly one configured setting: the repeater's
// Radio ID. Everything else about the equipment, the network and the procedure
// was held still, which is what makes the difference between them evidence
// rather than coincidence.
const (
	captureA = "../../../testdata/ipsc/ipsc-phase1-a-id100.pcap"
	captureB = "../../../testdata/ipsc/ipsc-phase1-b-id3132910.pcap"

	radioIDA = 100
	radioIDB = 3132910
)

// TestTheCapturedRequestsCarryTheConfiguredRadioID is the test that earns the
// PeerID field.
//
// A single capture cannot establish that bytes 1 to 4 are the peer ID: any
// constant would fit. Two captures taken minutes apart, with one setting
// changed and the new value appearing in those four bytes, is what turns a
// plausible reading into a demonstrated one.
func TestTheCapturedRequestsCarryTheConfiguredRadioID(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want uint32
	}{
		{"capture A, CPS Radio ID 100", captureA, radioIDA},
		{"capture B, CPS Radio ID 3132910", captureB, radioIDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cap := readCapture(t, tc.path)
			for _, pkt := range cap.UDP {
				msg, err := ipsc.Parse(pkt.Payload)
				if err != nil {
					t.Fatalf("packet %d: %v", pkt.Index, err)
				}
				req, ok := msg.(ipsc.RegisterRequest)
				if !ok {
					t.Fatalf("packet %d: parsed as %T, want RegisterRequest", pkt.Index, msg)
				}
				if req.PeerID != tc.want {
					t.Errorf("packet %d: peer ID %d, want %d", pkt.Index, req.PeerID, tc.want)
				}
			}
		})
	}
}

// TestEveryCapturedRequestRoundTrips holds the parser honest about the nine
// bytes it does not understand: whatever they mean, they must survive being
// read and written unchanged.
func TestEveryCapturedRequestRoundTrips(t *testing.T) {
	for _, path := range []string{captureA, captureB} {
		cap := readCapture(t, path)
		for _, pkt := range cap.UDP {
			msg, err := ipsc.Parse(pkt.Payload)
			if err != nil {
				t.Fatalf("%s packet %d: %v", path, pkt.Index, err)
			}
			if got := msg.Marshal(); !bytes.Equal(got, pkt.Payload) {
				t.Errorf("%s packet %d: round trip produced % x, want % x", path, pkt.Index, got, pkt.Payload)
			}
		}
	}
}

// TestTheRequestIsIdenticalOnEveryRetry records that the message carries no
// counter, nonce or timestamp.
//
// The consequence is practical: a recorded request can be replayed against a
// master under test, so developing the reply does not need a repeater in the
// room. If a future capture breaks this, that assumption goes with it.
func TestTheRequestIsIdenticalOnEveryRetry(t *testing.T) {
	for _, path := range []string{captureA, captureB} {
		cap := readCapture(t, path)
		first := cap.UDP[0].Payload
		for _, pkt := range cap.UDP[1:] {
			if !bytes.Equal(pkt.Payload, first) {
				t.Errorf("%s packet %d differs from the first: % x vs % x", path, pkt.Index, pkt.Payload, first)
			}
		}
	}
}

// TestTheRetryIntervalIsTenSecondsWithoutBackoff measures the cadence rather
// than asserting a documented constant, because the cadence is the observation.
//
// Thirty-five requests across two captures, none deviating by more than a few
// milliseconds and none slowing down. A master that assumes a peer will back
// off, or give up, has assumed something this repeater does not do.
func TestTheRetryIntervalIsTenSecondsWithoutBackoff(t *testing.T) {
	const (
		wantMicros  = 10 * 1_000_000
		toleranceUs = 50_000
	)
	for _, path := range []string{captureA, captureB} {
		cap := readCapture(t, path)
		if len(cap.UDP) < 2 {
			t.Fatalf("%s: %d packets, need at least two to measure an interval", path, len(cap.UDP))
		}
		for i := 1; i < len(cap.UDP); i++ {
			gap := int64(cap.UDP[i].Micros) - int64(cap.UDP[i-1].Micros)
			if diff := gap - wantMicros; diff < -toleranceUs || diff > toleranceUs {
				t.Errorf("%s: gap %d to %d was %d us, want %d +/- %d",
					path, i-1, i, gap, wantMicros, toleranceUs)
			}
		}
	}
}

// TestThePeerDoesNotSourceFromTheMasterPort guards a mistake that loopback
// testing cannot catch: the peer addresses 50000 and sends from 50002.
//
// An implementation that replies to the port it was addressed on rather than
// the port the datagram came from will work perfectly against itself.
func TestThePeerDoesNotSourceFromTheMasterPort(t *testing.T) {
	for _, path := range []string{captureA, captureB} {
		cap := readCapture(t, path)
		for _, pkt := range cap.UDP {
			if pkt.DstPort != 50000 {
				t.Errorf("%s packet %d: destination port %d, want 50000", path, pkt.Index, pkt.DstPort)
			}
			if pkt.SrcPort == pkt.DstPort {
				t.Errorf("%s packet %d: source port equals destination port %d", path, pkt.Index, pkt.SrcPort)
			}
			if pkt.SrcPort != 50002 {
				t.Errorf("%s packet %d: source port %d, want 50002", path, pkt.Index, pkt.SrcPort)
			}
		}
	}
}

// TestICMPUnreachableDoesNotStopThePeer records the reason a master cannot
// refuse anybody by staying silent.
//
// The host's kernel answered every request with a port unreachable, and the
// repeater's cadence did not change. Refusal therefore has to be an IPSC-level
// message — and no capture contains one, so QSP cannot yet refuse a peer at
// all. Naming that here is how it stays visible.
func TestICMPUnreachableDoesNotStopThePeer(t *testing.T) {
	for _, path := range []string{captureA, captureB} {
		cap := readCapture(t, path)
		if cap.ICMP != len(cap.UDP) {
			t.Errorf("%s: %d ICMP replies against %d requests, expected one each",
				path, cap.ICMP, len(cap.UDP))
		}
	}
}

// TestTheTrailerIsWhatWasObserved asserts the nine unexplained bytes so that a
// capture disagreeing with them is a visible event.
//
// This is not a claim that the trailer is constant. It came from one repeater
// on one firmware with one codeplug, and the parser accepts any value.
func TestTheTrailerIsWhatWasObserved(t *testing.T) {
	for _, path := range []string{captureA, captureB} {
		cap := readCapture(t, path)
		for _, pkt := range cap.UDP {
			msg, err := ipsc.Parse(pkt.Payload)
			if err != nil {
				t.Fatalf("%s packet %d: %v", path, pkt.Index, err)
			}
			req := msg.(ipsc.RegisterRequest)
			if req.Trailer != ipsc.ObservedTrailer {
				t.Errorf("%s packet %d: trailer % x, observed % x",
					path, pkt.Index, req.Trailer, ipsc.ObservedTrailer)
			}
		}
	}
}

// TestAnUncapturedTrailerIsStillParsed is the other half of the rule above: the
// trailer is recorded, not enforced. A repeater sending different bytes should
// be understood, not rejected on the strength of one XPR8300.
func TestAnUncapturedTrailerIsStillParsed(t *testing.T) {
	raw := append([]byte{0x90, 0x00, 0x2f, 0xcd, 0xee}, bytes.Repeat([]byte{0xaa}, 9)...)
	msg, err := ipsc.Parse(raw)
	if err != nil {
		t.Fatalf("a request with an unfamiliar trailer was rejected: %v", err)
	}
	req := msg.(ipsc.RegisterRequest)
	if req.PeerID != radioIDB {
		t.Errorf("peer ID %d, want %d", req.PeerID, radioIDB)
	}
	if got := msg.Marshal(); !bytes.Equal(got, raw) {
		t.Errorf("round trip produced % x, want % x", got, raw)
	}
}

// TestUncapturedMessageTypesAreRefused is the ADR-0029 boundary expressed as a
// test. Other implementations are known to use other leading bytes; this one
// has never seen them, so it says so rather than guessing.
func TestUncapturedMessageTypesAreRefused(t *testing.T) {
	for _, lead := range []byte{0x00, 0x80, 0x91, 0x92, 0x96, 0xff} {
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
		want error
	}{
		{"empty", nil, ipsc.ErrShort},
		{"type byte only", []byte{0x90}, ipsc.ErrShort},
		{"one byte short", bytes.Repeat([]byte{0x90}, 13), ipsc.ErrShort},
		{"one byte long", bytes.Repeat([]byte{0x90}, 15), ipsc.ErrTrailingBytes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ipsc.Parse(tc.in); !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// FuzzParse checks that no input panics and that anything accepted round-trips.
func FuzzParse(f *testing.F) {
	for _, path := range []string{captureA, captureB} {
		cap, err := readCaptureRaw(path)
		if err != nil {
			f.Fatalf("%v", err)
		}
		f.Add(cap.UDP[0].Payload)
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
