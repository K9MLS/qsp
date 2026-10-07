package v24link

import (
	"bytes"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/logging"
	"github.com/k9mls/qsp/internal/p25link"
)

// stepped is a clock a test moves by hand.
type stepped struct {
	base time.Time
	by   atomic.Int64
}

func newStepped() *stepped                 { return &stepped{base: time.Now()} }
func (c *stepped) now() time.Time          { return c.base.Add(time.Duration(c.by.Load())) }
func (c *stepped) advance(d time.Duration) { c.by.Add(int64(d)) }

// drain reads and discards what a repeater is sent, so its tunnel never
// fills.
func drain(c net.Conn) { go func() { _, _ = bytes.NewBuffer(nil).ReadFrom(c) }() }

// audio is the distinguishing byte of each voice frame the gateways were
// sent, in order.
func (g *gateways) audio() []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]byte, 0, len(g.frames))
	for _, f := range g.frames {
		out = append(out, f[1])
	}
	return out
}

// TestOneCallAtATimeThroughAFade is the fault an operator would hear as a
// call turning to noise: a repeater fades for a little over a second,
// another station begins, and the first comes back.
//
// Three repeaters, A and B talking and C listening, and the gateways. What C
// is sent is asserted byte for byte, because that is what goes on the air.
//
// Break it, a row at a time: in relay's "still talking" case call
// `l.floor.Take(st.holder, now)` and ignore the answer (the first two rows
// carry A's later frames); in endRelay send the end to every other repeater
// instead of calling endAtRepeaters, and to the gateways whoever they are
// listening to (the third row ends B's call in the middle).
func TestOneCallAtATimeThroughAFade(t *testing.T) {
	a1, a2, a3 := voiceRecord(0xA1), voiceRecord(0xA2), voiceRecord(0xA3)
	b1, b2 := voiceRecord(0xB1), voiceRecord(0xB2)

	tests := []struct {
		name string
		// request is how often a quiet transmission is looked for; an hour
		// is "the look has not come round yet".
		request time.Duration
		// after B has begun, what A does.
		resumes bool
		// What the gateways and C have been sent by the time B has said
		// its second frame.
		gateways []byte
		ends     int
		toC      []byte
		// held is transmissions that began with somebody else talking.
		held uint64
	}{
		{
			name: "the faded repeater comes back while the other is talking", request: time.Hour, resumes: true,
			gateways: []byte{0xA1, 0xB1, 0xB2}, ends: 1,
			toC: join(callStart, a1, callEnd, callEnd, callStart, b1, b2),
		},
		{
			// Given up on first, so what it says next is a new transmission
			// begun while B is talking: heard, counted, not carried.
			name: "it is given up on, and then comes back", request: 5 * time.Millisecond, resumes: true,
			gateways: []byte{0xA1, 0xB1, 0xB2}, ends: 1,
			toC:  join(callStart, a1, callEnd, callEnd, callStart, b1, b2),
			held: 1,
		},
		{
			name: "the faded repeater never comes back, and is given up on mid-call", request: 5 * time.Millisecond,
			gateways: []byte{0xA1, 0xB1, 0xB2}, ends: 1,
			toC: join(callStart, a1, callEnd, callEnd, callStart, b1, b2),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sink := &gateways{}
			clock := newStepped()
			l, _ := start(t, Config{Keepalive: time.Hour, Request: tc.request,
				Gateways: sink, Floor: &p25link.Floor{}, Now: clock.now})
			a, b, c := link(t, l, 1), link(t, l, 3), link(t, l, 5)
			waitFor(t, "three links up", func() bool { return l.LinksUp() == 3 })
			drain(a)
			drain(b)
			frames := func(n int) func() bool {
				return func() bool { f, _ := sink.counts(); return f == n }
			}

			_, _ = a.Write(join(callStart, a1))
			waitFor(t, "A's first frame at the gateways", frames(1))

			clock.advance(1100 * time.Millisecond) // the fade
			_, _ = b.Write(join(callStart, b1))
			waitFor(t, "B's first frame at the gateways", frames(2))

			clock.advance(100 * time.Millisecond) // 1.2 s: A is back before it is given up on
			if tc.request < time.Hour {
				waitFor(t, "A's transmission given up on", func() bool { return l.Calls() >= 1 })
			}
			if tc.resumes {
				_, _ = a.Write(a2)
			}
			_, _ = b.Write(b2)
			if tc.resumes {
				_, _ = a.Write(a3)
			}
			waitFor(t, "B's second frame at the gateways", func() bool {
				got := sink.audio()
				return len(got) > 0 && got[len(got)-1] == 0xB2
			})
			time.Sleep(50 * time.Millisecond) // anything wrongly on its way has arrived

			if got := sink.audio(); !bytes.Equal(got, tc.gateways) {
				t.Errorf("the gateways were sent % x, want % x", got, tc.gateways)
			}
			if _, ends := sink.counts(); ends != tc.ends {
				t.Errorf("the gateways were told of %d ends while B was talking, want %d", ends, tc.ends)
			}
			expect(t, c, tc.toC)
			silent(t, c)

			// B finishes, and that is an end like any other.
			_, _ = b.Write(join(callEnd, callEnd))
			expect(t, c, join(callEnd, callEnd))
			waitFor(t, "B's end at the gateways", func() bool { _, e := sink.counts(); return e == tc.ends+1 })
			if l.Held() != tc.held {
				t.Errorf("%d transmissions counted as begun while another was talking, want %d",
					l.Held(), tc.held)
			}
		})
	}
}

// TestARepeaterTakingTheFloorIsSentTheEndOfWhatItWasHearing. B was being
// sent A's call when A faded. B then talks, and is owed the end of A's call
// when it does, not a second and a half into its own.
//
// Break it: delete the block in relay that sends st the end marker once it
// has the floor.
func TestARepeaterTakingTheFloorIsSentTheEndOfWhatItWasHearing(t *testing.T) {
	clock := newStepped()
	l, _ := start(t, Config{Keepalive: time.Hour, Floor: &p25link.Floor{}, Now: clock.now})
	a, b := link(t, l, 1), link(t, l, 3)
	waitFor(t, "both links up", func() bool { return l.LinksUp() == 2 })
	drain(a)

	_, _ = a.Write(join(callStart, voiceRecord(0xA1)))
	expect(t, b, join(callStart, voiceRecord(0xA1)))
	clock.advance(1100 * time.Millisecond)
	_, _ = b.Write(join(callStart, voiceRecord(0xB1)))
	expect(t, b, join(callEnd, callEnd))
	silent(t, b)
}

// TestAGatewaysCallGivenUpOnDoesNotEndARepeaters. A gateway's call loses its
// terminator, a repeater begins, and a second later the gateway's call is
// given up on. Its end used to go to every repeater that had been sent any
// of it, by then in the middle of the repeater's call.
//
// Break it: in watchInbound, send the end to every linked repeater.
func TestAGatewaysCallGivenUpOnDoesNotEndARepeaters(t *testing.T) {
	clock := newStepped()
	l, _ := start(t, Config{Keepalive: time.Hour, Floor: &p25link.Floor{}, Now: clock.now})
	a, c := link(t, l, 1), link(t, l, 5)
	waitFor(t, "both links up", func() bool { return l.LinksUp() == 2 })
	drain(a)

	l.FromGateway(gatewayFrame(1))
	expect(t, c, join(callStart, asSent(gatewayFrame(1))))

	_, _ = a.Write(join(callStart, voiceRecord(0xA1)))
	// The gateway's call is closed at C as A's replaces it.
	expect(t, c, join(callEnd, callEnd, callStart, voiceRecord(0xA1)))

	clock.advance(2 * CallTimeout)
	time.Sleep(CallTimeout/4 + 150*time.Millisecond) // the look for a gateway's call gone quiet
	silent(t, c)
}

// TestACallAfterALinkDropStartsFromAStart, for a call from a gateway and for
// one from another repeater. A repeater whose link dropped part-way through
// a call went on being marked as part-way through it, and the next call
// reached it with no start: a transmitter sent voice it had not been told
// was coming.
//
// Break it: delete `st.receiving = ""` from countUp, and the rows where the
// call is still going when the link returns fail.
func TestACallAfterALinkDropStartsFromAStart(t *testing.T) {
	type source struct {
		name string
		// call sends one voice frame numbered n toward c and returns what c
		// should be sent for it after a start.
		call func(l *Listener, talker net.Conn, n byte) []byte
		end  func(l *Listener, talker net.Conn)
	}
	tests := []source{
		{"from a gateway",
			func(l *Listener, _ net.Conn, n byte) []byte {
				l.FromGateway(gatewayFrame(n))
				return asSent(gatewayFrame(n))
			},
			func(l *Listener, _ net.Conn) { l.EndFromGateway() }},
		{"from another repeater",
			func(_ *Listener, talker net.Conn, n byte) []byte {
				_, _ = talker.Write(voiceRecord(n))
				return voiceRecord(n)
			},
			func(_ *Listener, talker net.Conn) { _, _ = talker.Write(join(callEnd, callEnd)) }},
	}
	type row struct {
		source
		// endsMeanwhile is whether the call ends while the link is down.
		endsMeanwhile bool
	}
	var rows []row
	for _, s := range tests {
		rows = append(rows, row{s, true}, row{s, false})
	}
	for _, tc := range rows {
		name := tc.name + ", and the call is still going when the link returns"
		if tc.endsMeanwhile {
			name = tc.name + ", and the call ends while the link is down"
		}
		t.Run(name, func(t *testing.T) {
			l, _ := start(t, Config{Keepalive: time.Hour})
			talker := link(t, l, 3)
			drain(talker)
			c := link(t, l, 1)
			waitFor(t, "both links up", func() bool { return l.LinksUp() == 2 })

			expect(t, c, join(callStart, tc.call(l, talker, 1)))

			// The station restarts its link in the middle of the call.
			_, _ = c.Write(linkRequest)
			expect(t, c, join(linkAnswer, ourRequest))
			waitFor(t, "the link down", func() bool { return l.LinksUp() == 1 })
			if tc.endsMeanwhile {
				tc.end(l, talker)
				waitFor(t, "the call over", func() bool {
					return tc.source.name == "from a gateway" || l.Calls() >= 1
				})
			}

			// It comes back, and there is a new call.
			_, _ = c.Write(join(theirAcceptance, introduction(1), tunnel([]byte{0xFD, 0x01})))
			expect(t, c, ourIntroduction)
			waitFor(t, "the link up again", func() bool { return l.LinksUp() == 2 })

			expect(t, c, join(callStart, tc.call(l, talker, 2)))
		})
	}
}

// TestGivingUpOnACallDoesNotStopTheNextOne. The timer that closes a quiet
// transmission looked, and then closed, with nothing held in between; a
// record arriving in the gap began a new transmission which the close then
// stopped carrying, though it had the floor.
//
// Break it: release callMu in expire between the check and endRelay. It
// fails in some rounds and not all, which is why there are sixty.
func TestGivingUpOnACallDoesNotStopTheNextOne(t *testing.T) {
	for round := range 60 {
		func() {
			sink := &gateways{}
			clock := newStepped()
			l, err := New(logging.Discard(), Config{ListenAddress: "127.0.0.1:0", Keepalive: time.Hour,
				Request: time.Millisecond, Gateways: sink, Now: clock.now, Hold: holdInTests})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if err := l.Start(ctx); err != nil {
				cancel()
				t.Fatalf("Start: %v", err)
			}
			defer func() { cancel(); l.Wait() }()
			c, err := net.Dial("tcp", l.Addr().String())
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer c.Close()
			drain(c)
			_, _ = c.Write(join(linkRequest, theirAcceptance, introduction(1), tunnel([]byte{0xFD, 0x01})))
			waitFor(t, "the link up", func() bool { return l.LinksUp() == 1 })

			_, _ = c.Write(voiceRecord(1))
			waitFor(t, "the first frame", func() bool { f, _ := sink.counts(); return f == 1 })
			// The transmission goes stale as another record arrives.
			_, _ = c.Write(voiceRecord(2))
			clock.advance(2 * time.Second)
			waitFor(t, "the old transmission closed or the record carried", func() bool {
				f, _ := sink.counts()
				return l.Calls() >= 1 || f >= 2
			})
			time.Sleep(10 * time.Millisecond)
			before, _ := sink.counts()
			_, _ = c.Write(voiceRecord(3))
			waitFor(t, "the next record carried", func() bool {
				after, _ := sink.counts()
				if after > before || l.Held() > 0 {
					return true
				}
				return false
			})
			if l.Held() != 0 {
				t.Fatalf("round %d: a transmission with nobody else talking was not carried", round)
			}
		}()
	}
}

// TestStoppingDoesNotWaitForATunnelThatWasJustAccepted. A tunnel accepted as
// QSP was told to stop was never closed, and stopping waited half a minute
// for it to go quiet.
//
// Break it: delete the `stopping` lines from accept. It hangs in a few
// rounds in a hundred, which is why there are a hundred and fifty.
func TestStoppingDoesNotWaitForATunnelThatWasJustAccepted(t *testing.T) {
	stuck := 0
	for range 150 {
		l, err := New(logging.Discard(), Config{ListenAddress: "127.0.0.1:0",
			Keepalive: time.Hour, Request: time.Hour, Hold: holdInTests})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		if err := l.Start(ctx); err != nil {
			cancel()
			t.Fatalf("Start: %v", err)
		}
		addr := l.Addr().String()
		var (
			mu    sync.Mutex
			conns []net.Conn
			wg    sync.WaitGroup
		)
		for range 4 {
			wg.Go(func() {
				if c, err := net.Dial("tcp", addr); err == nil {
					mu.Lock()
					conns = append(conns, c)
					mu.Unlock()
				}
			})
		}
		cancel()
		waited := make(chan struct{})
		go func() { l.Wait(); close(waited) }()
		select {
		case <-waited:
		case <-time.After(2 * time.Second):
			stuck++
		}
		wg.Wait()
		for _, c := range conns {
			_ = c.Close()
		}
		<-waited
	}
	if stuck > 0 {
		t.Errorf("stopping was still waiting two seconds later in %d of 150 rounds", stuck)
	}
}

// TestSentIsWhatWasWritten. "Sent to it" on the console counted frames
// queued for a repeater, so a tunnel taking nothing counted up all the same.
//
// Break it: call q.wrote before p.write in pacer.run.
func TestSentIsWhatWasWritten(t *testing.T) {
	tests := []struct {
		name  string
		fails bool
		want  int32
	}{
		{"a tunnel that takes it", false, 3},
		{"a tunnel that does not", true, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var wrote, attempts atomic.Int32
			p := newPacer(logging.Discard(), time.Now, func([]byte) error {
				attempts.Add(1)
				if tc.fails {
					return net.ErrClosed
				}
				return nil
			})
			stop := make(chan struct{})
			done := make(chan struct{})
			go func() { p.run(context.Background(), stop, 0); close(done) }()
			for n := range byte(3) {
				p.send(asSent(gatewayFrame(n)), func() { wrote.Add(1) })
			}
			waitFor(t, "the queue emptied", func() bool {
				p.mu.Lock()
				defer p.mu.Unlock()
				return len(p.queue) == 0 && attempts.Load() >= 1
			})
			time.Sleep(20 * time.Millisecond)
			close(stop)
			<-done
			if got := wrote.Load(); got != tc.want {
				t.Errorf("%d frames counted as sent, want %d", got, tc.want)
			}
		})
	}
}
