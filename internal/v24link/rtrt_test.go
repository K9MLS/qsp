package v24link

import (
	"bytes"
	"context"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// Records from two captures of one Quantar on 2026-10-22, before and after
// RT/RT Configuration was enabled in its codeplug. Frames only: the captures'
// addresses are not kept.
var (
	rtrtOffStart  = mustHex("07030002040c0b0000000000")
	rtrtOffEnd    = mustHex("0703000204250b0000000000")
	rtrtOffHeader = mustHex("07036002040c0b1b151a70000000000000000000000000000008000000000002")
	rtrtOffVoice  = mustHex("07036202040c0b1b151a7080040cfd7bfb7df27b3d9e4402")
	rtrtOnStart   = mustHex("07030002020c0b0000000000")
	rtrtOnHeader  = mustHex("07036002020c0b1b281a5d00000000000000000000000000000800000000002a")
	rtrtOnVoice   = mustHex("07036202020c0b1b2d1a5880040cfd7bfb7df27b3d9e4402")
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestTheRTRTByteIsReadFromACallsStart is the reading, record by record.
//
// The start is the one record read: it opens every call, so a repeater that
// has made one call has said. The header and voice records carry the same
// byte and are not read, so a value is not decided twice from two places.
//
// To see it fail: in RTRTDisabled, read payload[2+rtrtByte+1] (the
// which-byte) and every known case is unknown; drop the RecordStart test and
// the end, header and voice cases are known.
func TestTheRTRTByteIsReadFromACallsStart(t *testing.T) {
	for _, tc := range []struct {
		name     string
		payload  []byte
		disabled bool
		known    bool
	}{
		{"a start marked disabled, as captured", rtrtOffStart, true, true},
		{"a start marked enabled, the same repeater after", rtrtOnStart, false, true},
		{"QSP's own start", startMarker, false, true},
		{"an end is not read", rtrtOffEnd, false, false},
		{"a header is not read", rtrtOffHeader, false, false},
		{"a voice record is not read", rtrtOffVoice, false, false},
		{"a value neither capture held", mustHex("07030002030c0b0000000000"), false, false},
		{"too short to be a start", mustHex("070300"), false, false},
		{"not an information frame", []byte{0xFD, 0x01}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			disabled, known := RTRTDisabled(tc.payload)
			if disabled != tc.disabled || known != tc.known {
				t.Errorf("disabled %v known %v, want %v %v", disabled, known, tc.disabled, tc.known)
			}
		})
	}
}

// TestBothCapturesDifferOnlyInTheRTRTByte holds the finding the reading rests
// on: the records from before and after the codeplug changed are the same
// except at that byte. If a capture is replaced with one where something else
// moved too, this says so rather than the reading going on as though only
// the flag mattered.
func TestBothCapturesDifferOnlyInTheRTRTByte(t *testing.T) {
	for _, tc := range []struct {
		name    string
		off, on []byte
		// same is how many leading bytes must agree apart from the flag:
		// after them, the header and voice carry the call's own fields.
		same int
	}{
		{"start", rtrtOffStart, rtrtOnStart, len(rtrtOnStart)},
		{"header, up to the call's own fields", rtrtOffHeader, rtrtOnHeader, 7},
		{"first voice record, up to the call's own fields", rtrtOffVoice, rtrtOnVoice, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.same {
				if i == 2+rtrtByte {
					if tc.off[i] != rtrtDisabled || tc.on[i] != rtrtEnabled {
						t.Errorf("byte %d is %02x before and %02x after", i, tc.off[i], tc.on[i])
					}
					continue
				}
				if tc.off[i] != tc.on[i] {
					t.Errorf("byte %d also differs: %02x before, %02x after", i, tc.off[i], tc.on[i])
				}
			}
		})
	}
}

// TestARepeaterWhoseCallsAreMarkedRTRTDisabledIsShown: the console learns it
// from the first call, and unlearns it when the codeplug is fixed.
//
// To see it fail: remove the `if known` block in serve, and the first step
// never shows it; set it without the `known` test and an end clears it.
func TestARepeaterWhoseCallsAreMarkedRTRTDisabledIsShown(t *testing.T) {
	l, _ := start(t, Config{Keepalive: time.Hour})
	talker := link(t, l, 1)
	listener := link(t, l, 3)
	go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(listener) }()
	waitFor(t, "both links up", func() bool { return l.LinksUp() == 2 })

	shown := func(site uint8) bool {
		for _, r := range l.Repeaters() {
			if r.Site == site {
				return r.RTRTDisabled
			}
		}
		t.Fatalf("no repeater at site %d", site)
		return false
	}
	if shown(1) {
		t.Fatal("a repeater that has made no call is shown as RT/RT disabled")
	}

	for _, step := range []struct {
		name string
		send []byte
		want bool
	}{
		{"a call marked disabled", join(tunnel(rtrtOffStart), voiceRecord(1), tunnel(rtrtOffEnd), tunnel(rtrtOffEnd)), true},
		{"the end does not clear it", tunnel(rtrtOffEnd), true},
		{"a call marked enabled clears it", join(tunnel(rtrtOnStart), voiceRecord(2), callEnd, callEnd), false},
	} {
		_, _ = talker.Write(step.send)
		waitFor(t, step.name, func() bool { return shown(1) == step.want })
	}
	if shown(3) {
		t.Error("the repeater that only listened is shown as RT/RT disabled")
	}
}

// TestACallRelayedToAnotherRepeaterIsSentToIt: "sent to it" counts what one
// repeater was sent of another's call, as it counts a gateway's.
//
// On 2026-10-22 two repeaters carried each other's calls all afternoon and
// both rows said "0 sent to it".
//
// To see it fail: pass nil for wrote in relay's call to toRepeaters.
func TestACallRelayedToAnotherRepeaterIsSentToIt(t *testing.T) {
	l, _ := start(t, Config{Keepalive: time.Hour})
	talker := link(t, l, 1)
	listener := link(t, l, 3)
	waitFor(t, "both links up", func() bool { return l.LinksUp() == 2 })

	_, _ = talker.Write(join(callStart, voiceRecord(1), voiceRecord(2), voiceRecord(3), callEnd, callEnd))
	expect(t, listener, join(callStart, voiceRecord(1), voiceRecord(2), voiceRecord(3), callEnd, callEnd))

	sent := func(site uint8) uint64 {
		for _, r := range l.Repeaters() {
			if r.Site == site {
				return r.Sent
			}
		}
		return 0
	}
	waitFor(t, "three voice records counted at the listener", func() bool { return sent(3) == 3 })
	for _, tc := range []struct {
		what string
		got  uint64
		want uint64
	}{
		{"sent to the listener", sent(3), 3},
		{"sent to the talker, which is not sent its own call", sent(1), 0},
		{"sent in all", l.Sent(), 3},
	} {
		if tc.got != tc.want {
			t.Errorf("%s: %d, want %d", tc.what, tc.got, tc.want)
		}
	}
}

// lockedLog is a log the tunnel's goroutines can write while the test reads.
type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedLog) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedLog) count(s string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Count(b.buf.String(), s)
}

// TestTheRTRTWarningIsLoggedOncePerLink: a repeater set up this way says so
// on every call, and the log says so once, with what to change.
//
// To see it fail: drop `once("rtrt disabled")` from the condition and three
// calls log it three times; drop the warning and it is never logged.
func TestTheRTRTWarningIsLoggedOncePerLink(t *testing.T) {
	const warning = "marks its calls RT/RT disabled"
	for _, tc := range []struct {
		name  string
		calls [][]byte
		want  int
	}{
		{"three calls marked disabled", [][]byte{rtrtOffStart, rtrtOffStart, rtrtOffStart}, 1},
		{"calls marked enabled", [][]byte{rtrtOnStart, rtrtOnStart}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &lockedLog{}
			l, err := New(slog.New(slog.NewTextHandler(log, nil)), Config{
				ListenAddress: "127.0.0.1:0", Request: time.Hour, Hold: holdInTests, Keepalive: time.Hour,
			})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if err := l.Start(ctx); err != nil {
				cancel()
				t.Fatalf("Start: %v", err)
			}
			t.Cleanup(func() { cancel(); l.Wait() })

			c := link(t, l, 1)
			waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })
			for i, startRecord := range tc.calls {
				_, _ = c.Write(join(tunnel(startRecord), voiceRecord(byte(i)), callEnd, callEnd))
			}
			waitFor(t, "every call heard", func() bool {
				r := l.Repeaters()
				return len(r) == 1 && r[0].Calls == uint64(len(tc.calls))
			})
			if got := log.count(warning); got != tc.want {
				t.Errorf("the warning was logged %d times, want %d", got, tc.want)
			}
			if tc.want > 0 && log.count("ENABLED in its codeplug") == 0 {
				t.Error("the warning does not say what to change")
			}
		})
	}
}
