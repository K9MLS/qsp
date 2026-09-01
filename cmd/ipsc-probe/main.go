// Command ipsc-probe is an experiment, not a server.
//
// It listens on a UDP port and answers a Motorola repeater the way one captured
// master answered one captured peer: replaying the bodies in
// testdata/ipsc/ipsc-phase2-registration.pcap with the sender ID substituted.
// Nine of the eleven body bytes of the registration reply have no known
// meaning, and one of them is known to be a property of the XPR8300 the capture
// came from rather than of the protocol.
//
// So this program cannot be right by construction, and the point is not to be.
// **The repeater is the oracle.** If it registers and holds, those bytes are
// good enough for a master; if it does not, the way it fails narrows which of
// them matters. Neither answer is available by thinking harder about the
// capture, and both are cheap to get with a repeater on the bench.
//
// It is deliberately not part of cmd/qsp. QSP does not ship an IPSC listener
// until one exists that was built rather than replayed, and ipsc reports
// unavailable in the health report until then.
//
// Usage:
//
//	ipsc-probe -listen :50000 -id 3132910
//
// Every datagram in and out is logged with its type, sender and length, so the
// terminal is a running account of what the repeater did. Run a capture
// alongside it: the log says what happened, and the pcap is the evidence.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

func main() {
	listen := flag.String("listen", ":50000", "UDP address to listen on")
	id := flag.Uint("id", 0, "radio ID this master announces as its own (required)")
	verbose := flag.Bool("hex", false, "log the full payload of every datagram")
	flag.Parse()

	if *id == 0 {
		fmt.Fprintln(os.Stderr, "ipsc-probe: -id is required; a master announces a radio ID and 0 is not one")
		os.Exit(2)
	}

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	addr, err := net.ResolveUDPAddr("udp", *listen)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ipsc-probe: cannot resolve %s: %v\n", *listen, err)
		os.Exit(1)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ipsc-probe: cannot listen on %s: %v\n", *listen, err)
		os.Exit(1)
	}
	defer func() { _ = conn.Close() }()

	log.Info("listening", "address", conn.LocalAddr().String(), "master_id", *id,
		"note", "replaying captured master bytes; see internal/protocol/ipsc/responder.go")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		log.Info("stopping")
		_ = conn.Close()
	}()

	responder := ipsc.Responder{MasterID: uint32(*id)}
	buf := make([]byte, 2048)
	var seen int

	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		seen++
		raw := buf[:n]

		msg, perr := ipsc.Parse(raw)
		if perr != nil {
			sender, ok := ipsc.SenderIDOf(raw)
			attrs := []any{"from", from.String(), "bytes", n, "error", perr}
			if ok {
				attrs = append(attrs, "sender_id", sender)
			}
			attrs = append(attrs, "payload", hex.EncodeToString(raw))
			log.Warn("unrecognised datagram", attrs...)
			continue
		}

		attrs := []any{
			"from", from.String(), "type", fmt.Sprintf("%#02x", byte(msg.Kind)),
			"sender_id", msg.SenderID, "bytes", n, "n", seen,
		}
		if *verbose {
			attrs = append(attrs, "payload", hex.EncodeToString(raw))
		}
		log.Info("received", attrs...)

		for _, out := range responder.Reply(msg) {
			wire := out.Marshal()
			// Reply to the address the datagram came from, never to the port
			// it was sent to. A Motorola peer sources from a different port
			// than the one it addresses: the XPR8300 used 50002 against 50000
			// and the second repeater used 50004. An implementation that
			// assumed symmetry would work against itself and fail here.
			if _, werr := conn.WriteToUDP(wire, from); werr != nil {
				log.Error("send failed", "to", from.String(), "error", werr)
				continue
			}
			outAttrs := []any{
				"to", from.String(), "type", fmt.Sprintf("%#02x", byte(out.Kind)),
				"bytes", len(wire),
			}
			if *verbose {
				outAttrs = append(outAttrs, "payload", hex.EncodeToString(wire))
			}
			log.Info("sent", outAttrs...)
		}

	}
}
