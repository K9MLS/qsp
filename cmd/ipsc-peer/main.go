// Command ipsc-peer registers to a real IP Site Connect master and writes down
// everything that master sends.
//
// # What this is for
//
// Every capture in testdata/ipsc is the same half of the conversation: a
// repeater talking to a master. There is not one byte of a master talking to a
// repeater. Everything QSP knows about being a master it inferred from watching
// peers, and that shows what a master must *answer* — never what a master
// *initiates*. A message a real master sends unprompted is one QSP has never
// seen, does not send, and cannot discover by studying its own captures.
//
// This program is the instrument that closes that gap. It plays a repeater so
// that a real master will talk to it.
//
// # What this is not
//
// **It is not QSP and it is not a route to peer support.** ADR-0043 settles that
// QSP is the master and is never a peer in production: a club points every
// Pi-Star and every Motorola repeater at QSP and runs nothing else. That
// decision removes the peer role from the server permanently. This is a bench
// instrument in the same category as cmd/ipsc-probe and testdata — it never
// ships in cmd/qsp, and an operator putting a repeater into master role for an
// afternoon is not a club running a second master.
//
// # It cannot be right by construction, and that is the point
//
// The registration bytes are replayed from one SLR5700 in
// ipsc-phase2-registration.pcap with the sender ID substituted. Most have no
// known meaning and the first body byte is known to be device-specific. So this
// is a recording of one repeater's requests, not an implementation of IPSC.
//
// **The master is the oracle.** If it accepts the registration and keeps
// answering, the bytes were good enough; if it does not, the failure says which
// of them matters. Neither answer is available by reasoning about them.
//
// # Usage
//
//	ipsc-peer -master 192.168.1.233:50000 -listen :50004 -id 3132911
//
// -master is the address the real master listens on. -listen is this program's
// own local port, which is a different thing: a Motorola repeater has two
// separate port fields for exactly this reason and conflating them produces ICMP
// unreachables from a configuration that looks correct. In the capture the peer
// sent to the master's 50000 and received on its own 50004.
//
// -id must not be the master's radio ID. A repeater refuses to register with a
// master announcing the repeater's own ID and retries silently forever with no
// indication of why; this program checks for that and says so.
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

func main() {
	var (
		master    = flag.String("master", "", "address of the real IPSC master, host:port")
		listen    = flag.String("listen", ":50004", "this program's own UDP port")
		id        = flag.Uint("id", 0, "radio ID to register as; must differ from the master's")
		retry     = flag.Duration("retry", 10*time.Second, "how often to re-send the registration until accepted")
		keepalive = flag.Duration("keepalive", 15*time.Second, "how often to send a keepalive once registered")
	)
	flag.Parse()

	if *master == "" || *id == 0 {
		fmt.Fprintln(os.Stderr, "ipsc-peer: -master and -id are both required")
		flag.Usage()
		os.Exit(2)
	}

	remote, err := net.ResolveUDPAddr("udp4", *master)
	if err != nil {
		log.Fatalf("ipsc-peer: -master %q: %v", *master, err)
	}
	local, err := net.ResolveUDPAddr("udp4", *listen)
	if err != nil {
		log.Fatalf("ipsc-peer: -listen %q: %v", *listen, err)
	}
	conn, err := net.ListenUDP("udp4", local)
	if err != nil {
		log.Fatalf("ipsc-peer: binding %s: %v", *listen, err)
	}
	defer conn.Close()

	log.Printf("registering to %s from %s as radio ID %d", remote, conn.LocalAddr(), *id)
	log.Printf("every datagram in both directions is printed; redirect stdout to keep it")

	send := func(kind ipsc.Kind) {
		msg, ok := ipsc.PeerMessageFor(kind, uint32(*id))
		if !ok {
			log.Printf("no captured body for kind %#02x; not sending", byte(kind))
			return
		}
		raw := msg.Marshal()
		if _, err := conn.WriteToUDP(raw, remote); err != nil {
			log.Printf("send %#02x failed: %v", byte(kind), err)
			return
		}
		log.Printf("OUT %#02x %3d bytes  %s", byte(kind), len(raw), hex.EncodeToString(raw))
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	registered := make(chan struct{})
	go pump(conn, uint32(*id), registered, send)

	send(ipsc.KindRegisterRequest)
	retries := time.NewTicker(*retry)
	defer retries.Stop()
	beats := time.NewTicker(*keepalive)
	defer beats.Stop()

	accepted := false
	for {
		select {
		case <-stop:
			log.Printf("stopping")
			return
		case <-registered:
			if accepted {
				continue
			}
			accepted = true
			retries.Stop()
			log.Printf("registered; sending keepalives every %s", *keepalive)
			// The capture shows the peer sending this once, immediately after
			// the reply, and the master answering with KindF1.
			send(ipsc.KindF0)
		case <-retries.C:
			// Silence is the only refusal vocabulary IPSC has: an unregistered
			// peer is never told no, it simply gets no answer.
			log.Printf("no reply yet; re-sending the registration")
			send(ipsc.KindRegisterRequest)
		case <-beats.C:
			if accepted {
				send(ipsc.KindKeepaliveRequest)
			}
		}
	}
}

// pump reads and prints every datagram the master sends.
//
// It prints rather than interprets. The purpose of this program is to find out
// what a master sends, so a message this code does not recognise is the most
// interesting thing that can arrive and must not be dropped quietly.
func pump(conn *net.UDPConn, id uint32, registered chan<- struct{}, send func(ipsc.Kind)) {
	buf := make([]byte, 2048)
	seen := map[byte]int{}
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		raw := append([]byte(nil), buf[:n]...)
		kind := byte(0)
		if n > 0 {
			kind = raw[0]
		}
		seen[kind]++

		note := ""
		if msg, err := ipsc.Parse(raw); err == nil {
			if msg.SenderID == id {
				note = "  <-- THE MASTER IS ANNOUNCING THIS PROGRAM'S OWN RADIO ID." +
					" A repeater refuses to register in this case and retries" +
					" silently. Restart with a different -id."
			}
			switch msg.Kind {
			case ipsc.KindRegisterReply:
				select {
				case registered <- struct{}{}:
				default:
				}
			case ipsc.KindKeepaliveRequest, ipsc.Kind85:
				// A master that sends these unprompted is doing something no
				// capture in the repository shows. Answer nothing; just record.
			}
		} else {
			note = fmt.Sprintf("  <-- does not parse as IPSC (%v). Recorded verbatim.", err)
		}

		if seen[kind] == 1 {
			log.Printf("IN  %#02x %3d bytes from %s  %s   [first of this kind]%s",
				kind, n, from, hex.EncodeToString(raw), note)
			continue
		}
		log.Printf("IN  %#02x %3d bytes from %s  %s%s",
			kind, n, from, hex.EncodeToString(raw), note)
	}
}
