package v24link

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func readFull(c net.Conn, b []byte) (int, error) { return io.ReadFull(c, b) }

// gatewayFrame is a gateway's voice frame whose audio bytes are all n.
func gatewayFrame(n byte) []byte {
	return append([]byte{0x63}, bytes.Repeat([]byte{n}, 13)...)
}

// asSent is a gateway's frame as a repeater is sent it.
func asSent(frame []byte) []byte {
	return tunnel(append([]byte{0x07, 0x03}, frame...))
}

// What a repeater is sent for a gateway's call, step by step.
//
// Break it: change a byte of the voice, send the start more than once, send
// the end once, or send a header nobody asked for, and a row fails.
func TestAGatewaysCallAsARepeaterIsSentIt(t *testing.T) {
	header := join(tunnel(capturedHeader1), tunnel(capturedHeader2))
	type step struct {
		frame []byte // nil is the end of the call
	}
	voice := func(n byte) step { return step{gatewayFrame(n)} }
	end := step{}
	tests := []struct {
		name   string
		header bool
		steps  []step
		want   []byte // nil is silence
	}{
		{"one frame opens the call", false, []step{voice(1)}, join(callStart, asSent(gatewayFrame(1)))},
		{"the start is sent once", false, []step{voice(1), voice(2), voice(3)},
			join(callStart, asSent(gatewayFrame(1)), asSent(gatewayFrame(2)), asSent(gatewayFrame(3)))},
		{"the end is sent twice", false, []step{voice(1), end},
			join(callStart, asSent(gatewayFrame(1)), callEnd, callEnd)},
		{"an end with no call open says nothing", false, []step{end}, nil},
		{"a second end says nothing more", false, []step{voice(1), end, end},
			join(callStart, asSent(gatewayFrame(1)), callEnd, callEnd)},
		{"a second call gets a start of its own", false, []step{voice(1), end, voice(2), end},
			join(callStart, asSent(gatewayFrame(1)), callEnd, callEnd,
				callStart, asSent(gatewayFrame(2)), callEnd, callEnd)},
		{"with the header asked for, it follows the start", true, []step{voice(1), voice(2), end},
			join(callStart, header, asSent(gatewayFrame(1)), asSent(gatewayFrame(2)), callEnd, callEnd)},
		{"a terminator passed as voice is refused", false,
			[]step{{append([]byte{0x80}, make([]byte, 16)...)}}, nil},
		{"a frame of the wrong length is refused", false, []step{{gatewayFrame(1)[:13]}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, _ := start(t, Config{Keepalive: time.Hour, SendHeader: tc.header})
			c := link(t, l, 1)
			waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })
			for _, s := range tc.steps {
				if s.frame == nil {
					l.EndFromGateway()
				} else {
					l.FromGateway(s.frame)
				}
			}
			if tc.want != nil {
				expect(t, c, tc.want)
			}
			silent(t, c)
		})
	}
}

// The header is the capture's own, byte for byte.
//
// Break it: retype either half wrongly, and a repeater is sent a header no
// repeater ever sent.
func TestTheHeaderSentIsTheOneCaptured(t *testing.T) {
	var halves [][]byte
	for _, payload := range stationFrames(t, voiceFixture) {
		if len(payload) > 2 && payload[1] == controlUI && (payload[2] == recordHeader1 || payload[2] == recordHeader2) {
			halves = append(halves, payload)
		}
		if len(halves) == 2 {
			break
		}
	}
	if len(halves) != 2 {
		t.Fatalf("found %d header halves in the capture", len(halves))
	}
	if !bytes.Equal(capturedHeader1, halves[0]) || !bytes.Equal(capturedHeader2, halves[1]) {
		t.Errorf("the header differs from the capture:\n % x\n % x\n % x\n % x",
			capturedHeader1, halves[0], capturedHeader2, halves[1])
	}
	for _, h := range [][]byte{capturedHeader1, capturedHeader2} {
		if rec, ok := ReadRecord(h); !ok || rec.Kind != RecordHeader {
			t.Errorf("a header half is not read as one: % x", h)
		}
	}
}

// Break it: send to a repeater whose link is not open, skip the start for a
// repeater that joins part-way, or count a frame for a repeater it did not
// reach, and this fails.
func TestWhichRepeatersAreSentAGatewaysCall(t *testing.T) {
	l, _ := start(t, Config{Keepalive: time.Hour})
	first := link(t, l, 1)
	half := dial(t, l)
	_, _ = half.Write(linkRequest)
	expect(t, half, join(linkAnswer, ourRequest))
	waitFor(t, "one link up", func() bool { return l.LinksUp() == 1 })

	if n := l.FromGateway(gatewayFrame(1)); n != 1 {
		t.Fatalf("the frame reached %d repeaters, want 1", n)
	}
	expect(t, first, join(callStart, asSent(gatewayFrame(1))))
	silent(t, half)

	// A second repeater links while the call is in progress.
	late := link(t, l, 3)
	waitFor(t, "two links up", func() bool { return l.LinksUp() == 2 })
	if n := l.FromGateway(gatewayFrame(2)); n != 2 {
		t.Fatalf("the frame reached %d repeaters, want 2", n)
	}
	expect(t, first, asSent(gatewayFrame(2)))
	expect(t, late, join(callStart, asSent(gatewayFrame(2))))

	if n := l.EndFromGateway(); n != 2 {
		t.Errorf("the end reached %d repeaters, want 2", n)
	}
	expect(t, first, join(callEnd, callEnd))
	expect(t, late, join(callEnd, callEnd))
	silent(t, half)

	if l.Sent() != 3 {
		t.Errorf("%d frames counted as sent, want 3", l.Sent())
	}
	for _, r := range l.Repeaters() {
		switch {
		case r.Site == 1 && r.Sent != 2, r.Site == 3 && r.Sent != 1:
			t.Errorf("site %d was sent %d frames", r.Site, r.Sent)
		}
	}
}

// Break it: never look for a gateway's call that has stopped, and a repeater
// sent a start and no end stays keyed.
func TestAGatewaysCallThatStopsIsEndedAtTheRepeaters(t *testing.T) {
	l, _ := start(t, Config{Keepalive: time.Hour})
	c := link(t, l, 1)
	waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })

	l.FromGateway(gatewayFrame(1))
	expect(t, c, join(callStart, asSent(gatewayFrame(1))))

	// Nothing more arrives. The end comes by itself, once, after CallTimeout.
	got := make([]byte, len(callEnd)*2)
	_ = c.SetReadDeadline(time.Now().Add(CallTimeout + 2*time.Second))
	if _, err := readFull(c, got); err != nil {
		t.Fatalf("no end was sent: %v", err)
	}
	if !bytes.Equal(got, join(callEnd, callEnd)) {
		t.Fatalf("got % x", got)
	}
	silent(t, c)
}

// A repeater that keys while a gateway's call is being sent to the repeaters
// is heard and not carried, and the gateway's call is not disturbed.
//
// Break it: let the repeater take the floor from the gateways, and its voice
// goes back out to the gateways in the middle of their own call.
func TestARepeaterKeyingOverAGatewayIsHeld(t *testing.T) {
	sink := &gateways{}
	l, _ := start(t, Config{Keepalive: time.Hour, Gateways: sink})
	c := link(t, l, 1)
	waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })

	// The gateway listener takes the floor for its caller, as it does.
	l.floor.Take("gateways", time.Now())
	l.FromGateway(gatewayFrame(1))
	expect(t, c, join(callStart, asSent(gatewayFrame(1))))

	_, _ = c.Write(join(callStart, voiceRecord(9), callEnd, callEnd))
	waitFor(t, "the repeater's call being held", func() bool { return l.Held() == 1 })
	if f, e := sink.counts(); f != 0 || e != 0 {
		t.Errorf("the repeater's call reached the gateways: %d frames, %d ends", f, e)
	}
}
