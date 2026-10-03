//go:build ignore

// demo_peers logs four invented hotspots in to a QSP server on this machine
// and has them talk, so the Overview has something to show. It exists for
// one purpose: the screenshot in the README, which must not publish any real
// member's callsign, radio ID or address. run.sh beside this file drives it.
//
//	go run scripts/overview-screenshot/demo_peers.go -server 127.0.0.1:62931 -password-file peer.pass
//
// Everything it sends is made up: the callsigns are the owner's own and
// N0CALL, the placeholder amateur software has always used; the radio IDs
// begin 999, which no country is allocated.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/k9mls/qsp/internal/protocol/hbp"
)

type hotspot struct {
	id       hbp.RepeaterID
	callsign string
	location string
	lat, lon string
	software string
}

var hotspots = []hotspot{
	{9990001, "K9MLS", "Home hotspot", "41.5200", "-90.5800", "20260101_Pi-Star"},
	{9990002, "K9MLS", "Mobile hotspot", "41.6600", "-91.5300", "20260101_Pi-Star"},
	{9990003, "N0CALL", "Club repeater", "41.9800", "-91.6700", "MMDVM_MMDVM_HS_Hat"},
	{9990004, "N0CALL", "Field day site", "42.5000", "-90.6600", "20260101_Pi-Star"},
}

// An over is one transmission: who, through which hotspot, where to, and for
// how long. They run one after another, and the last is left running so the
// screenshot catches a call in progress.
var overs = []struct {
	via    int
	source uint32
	tg     uint32
	slot   hbp.Timeslot
	length time.Duration
}{
	{0, 9990101, 2, hbp.Timeslot2, 4 * time.Second},
	{2, 9990103, 2, hbp.Timeslot2, 6 * time.Second},
	{1, 9990102, 9, hbp.Timeslot1, 3 * time.Second},
	{3, 9990104, 2, hbp.Timeslot2, 5 * time.Second},
	{0, 9990101, 2, hbp.Timeslot2, 10 * time.Minute},
}

func main() {
	server := flag.String("server", "127.0.0.1:62031", "the QSP server's DMR address")
	passFile := flag.String("password-file", "", "the file holding the peer password")
	flag.Parse()
	if err := run(*server, *passFile); err != nil {
		fmt.Fprintln(os.Stderr, "demo_peers:", err)
		os.Exit(1)
	}
}

func run(server, passFile string) error {
	raw, err := os.ReadFile(passFile)
	if err != nil {
		return fmt.Errorf("reading the peer password: %w", err)
	}
	password := []byte(strings.TrimSpace(string(raw)))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	conns := make([]*net.UDPConn, len(hotspots))
	for i, h := range hotspots {
		c, err := login(server, h, password)
		if err != nil {
			return fmt.Errorf("hotspot %d: %w", h.id, err)
		}
		defer func() { _ = c.Close() }()
		conns[i] = c
	}
	fmt.Println("four hotspots registered")

	var wg sync.WaitGroup
	for i, h := range hotspots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			keepalive(ctx, conns[i], h.id)
		}()
	}
	for n, o := range overs {
		if err := talk(ctx, conns[o.via], hotspots[o.via].id, o.source, o.tg, o.slot, hbp.StreamID(0x51500000+n), o.length); err != nil {
			return err
		}
		if n == len(overs)-2 {
			fmt.Println("ready: the last over is in progress")
		}
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-time.After(1500 * time.Millisecond):
		}
	}
	wg.Wait()
	return nil
}

func login(server string, h hotspot, password []byte) (*net.UDPConn, error) {
	addr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", server, err)
	}
	c, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return nil, fmt.Errorf("dialling %s: %w", server, err)
	}
	ask := func(m hbp.Message) (hbp.Ack, error) {
		if _, err := c.Write(m.Marshal()); err != nil {
			return hbp.Ack{}, fmt.Errorf("sending %s: %w", m.Kind(), err)
		}
		buf := make([]byte, 512)
		_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
		n, err := c.Read(buf)
		if err != nil {
			return hbp.Ack{}, fmt.Errorf("no answer to %s: %w", m.Kind(), err)
		}
		reply, err := hbp.Parse(buf[:n])
		if err != nil {
			return hbp.Ack{}, fmt.Errorf("the answer to %s: %w", m.Kind(), err)
		}
		ack, ok := reply.(hbp.Ack)
		if !ok {
			return hbp.Ack{}, fmt.Errorf("%s was answered with %s", m.Kind(), reply.Kind())
		}
		return ack, nil
	}
	challenge, err := ask(hbp.Login{RepeaterID: h.id})
	if err != nil {
		return nil, err
	}
	if _, err := ask(hbp.Key{RepeaterID: h.id, Digest: hbp.Digest(challenge.Salt(), password)}); err != nil {
		return nil, err
	}
	_, err = ask(hbp.Config{
		RepeaterID: h.id, Callsign: h.callsign, RXFreq: "438800000", TXFreq: "438800000",
		TXPower: "01", ColorCode: "01", Latitude: h.lat, Longitude: h.lon, Height: "010",
		Location: h.location, Description: "QSP demo", Slots: "3",
		URL: "https://github.com/K9MLS/qsp", SoftwareID: h.software, PackageID: "MMDVM_MMDVM_HS_Hat",
	})
	if err != nil {
		return nil, err
	}
	_ = c.SetReadDeadline(time.Time{})
	// Whatever the server sends back from here on is read and dropped, so
	// the socket's buffer never fills.
	go func() {
		buf := make([]byte, 512)
		for {
			if _, err := c.Read(buf); err != nil {
				return
			}
		}
	}()
	return c, nil
}

func keepalive(ctx context.Context, c *net.UDPConn, id hbp.RepeaterID) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_, _ = c.Write(hbp.Ping{RepeaterID: id}.Marshal())
		}
	}
}

// talk sends one transmission: a voice header, a voice frame every 60 ms, and
// a terminator. The payload is silence; nothing here is ever on the air.
func talk(ctx context.Context, c *net.UDPConn, via hbp.RepeaterID, source, tg uint32, slot hbp.Timeslot, stream hbp.StreamID, length time.Duration) error {
	frame := hbp.Data{
		RepeaterID: via, SourceID: source, TargetID: tg, Timeslot: slot,
		CallType: hbp.CallGroup, StreamID: stream,
		FrameType: hbp.FrameTypeSync, DataType: hbp.DataTypeVoiceLCHeader,
	}
	send := func() error {
		if _, err := c.Write(frame.Marshal()); err != nil {
			return fmt.Errorf("sending a frame: %w", err)
		}
		frame.Sequence++
		return nil
	}
	if err := send(); err != nil {
		return err
	}
	t := time.NewTicker(60 * time.Millisecond)
	defer t.Stop()
	end := time.After(length)
	for n := 0; ; n++ {
		select {
		case <-ctx.Done():
			return nil
		case <-end:
			frame.FrameType, frame.DataType = hbp.FrameTypeSync, hbp.DataTypeTerminator
			return send()
		case <-t.C:
			frame.FrameType, frame.DataType = hbp.FrameTypeVoice, uint8(n%6)
			if n%6 == 0 {
				frame.FrameType, frame.DataType = hbp.FrameTypeVoiceSync, 0
			}
			if err := send(); err != nil {
				return err
			}
		}
	}
}
