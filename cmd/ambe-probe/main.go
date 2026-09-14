// Command ambe-probe is an experiment, not a server.
//
// It talks to an AMBEserver over UDP and prints every byte in both directions,
// so that the AMBE-3000 packet framing can be learned from a dongle on a bench
// rather than assumed from prose.
//
// **The dongle is the oracle.** The control exchange is already known, because
// it has been run: `61 00 01 00 30` in, `61 00 0b 00 30 41 4d 42 45 33 30 30
// 30 46 00` back, which is a product identifier of AMBE3000F. Everything this
// program does beyond that — the mode packet, the speech packet, the channel
// packet — is a reading of a published register map applied to a device nobody
// here has driven yet. If it answers, the reading is right; if it does not,
// the way it fails narrows which field is wrong. Neither answer is available
// by thinking harder.
//
// It is deliberately not part of cmd/qsp. QSP ships no transcoder link until
// one exists that was built from an exchange this project has seen, per
// ADR-0029's standing rule and ADR-0061's boundary: QSP speaks to a vocoder it
// does not own, and the operator's hardware stays the operator's.
//
// # Why an AMBEserver rather than the serial port
//
// The serial port is the operator's device, and one process may hold it.
// AMBEserver already owns it and serves it on a socket, which is how the
// dongle stays available to whatever else the operator runs. QSP talking to a
// socket rather than a tty is also the difference between a daemon that can
// run anywhere and one that must run where the hardware is.
//
// Usage:
//
//	ambe-probe -server 127.0.0.1:2460
//	ambe-probe -server 127.0.0.1:2460 -tone
//
// With -tone it sends one 20 ms frame of a 1 kHz sine as 8 kHz 16-bit PCM and
// prints whatever comes back. That is the exchange the transcoder link is made
// of: audio in, compressed frame out.
package main

import (
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"time"
)

// The AMBE-3000 packet: a start byte, a big-endian length, and a type.
//
// **The length counts the payload and not the type byte**, which is the first
// thing this program got wrong and the first thing the record corrected. The
// observed exchange is `61` `00 01` `00` `30`: one byte of payload, `30`, with
// the type `00` outside the count. The first draft here emitted `61 00 02 00
// 33` and would have been refused by the dongle for a reason that looked like
// a dead device.
//
// The reply is `61` `00 0b` `00` `30` then ten bytes of text, and 0x0b is
// eleven: the field byte plus `AMBE3000F` plus a terminator. Consistent, and
// consistent both ways.
const (
	startByte = 0x61

	typeControl = 0x00
	// **Speech is 0x01 and channel is 0x02**, corrected 2026-09-14 after the
	// first run. They were written the other way round, so 320 bytes of a
	// 1 kHz tone went to the chip labelled as compressed audio to be decoded.
	// AMBEserver forwarded it and the chip said nothing — which is what a
	// wrong type byte looks like, and is indistinguishable from a dead device
	// until the bytes are on the screen.
	typeSpeech  = 0x01 // uncompressed audio, the PCM side
	typeChannel = 0x02 // compressed audio, the AMBE side
)

// Control fields, of which two are confirmed by the exchange already run.
const (
	fieldReset     = 0x33 // confirmed: answers 0x39
	fieldProdID    = 0x30 // confirmed: answers 0AMBE3000F
	fieldVersion   = 0x31 // confirmed: answers V121.E100...
	fieldRateTable = 0x0a // a rate index rather than a full rate word
)

// samplesPerFrame is 20 ms at 8 kHz, which is what the AMBE-3000 takes for one
// compressed frame.
const samplesPerFrame = 160

func main() {
	server := flag.String("server", "127.0.0.1:2460", "AMBEserver address")
	tone := flag.Bool("tone", false, "send one frame of 1 kHz tone and print the reply")
	rate := flag.Int("rate", 33, "rate index for the mode packet; 33 is DMR/NXDN 2450+1150")
	wait := flag.Duration("wait", 2*time.Second, "how long to wait for each reply")
	// **Overridable, because this is the field that was wrong.** A reading of
	// a register map is a hypothesis; the dongle decides. Trying the other
	// value should cost a flag, not a rebuild and a deploy.
	speechType := flag.Int("speech-type", typeSpeech, "packet type for a PCM frame")
	flag.Parse()

	conn, err := net.Dial("udp", *server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("talking to %s\n\n", *server)

	// The three confirmed exchanges first, so that a run always says whether
	// the dongle is there before it says anything uncertain.
	ask(conn, *wait, "reset", control(fieldReset))
	time.Sleep(500 * time.Millisecond)
	ask(conn, *wait, "product id", control(fieldProdID))
	ask(conn, *wait, "version", control(fieldVersion))

	if !*tone {
		fmt.Println("\nnothing further attempted; pass -tone to try an audio frame")
		return
	}

	// **From here the packets are a reading, not a recording.**
	fmt.Println("\n--- beyond what has been observed ---")
	ask(conn, *wait, fmt.Sprintf("set rate index %d", *rate),
		control(fieldRateTable, byte(*rate)))
	ask(conn, *wait, fmt.Sprintf("20 ms of 1 kHz tone as type %#02x", *speechType),
		packet(byte(*speechType), speechBody(sine(1000))))
}

// control builds a control packet: type 0x00, then a field and its arguments.
func control(field byte, args ...byte) []byte {
	return packet(typeControl, append([]byte{field}, args...))
}

// speechBody is 160 samples of 8 kHz 16-bit PCM, with the field and count
// that precede them.
//
// The field byte and sample count precede the samples, which is the shape the
// register map describes and the part this program exists to test.
func speechBody(samples []int16) []byte {
	body := make([]byte, 0, 2+len(samples)*2)
	body = append(body, 0x00, byte(len(samples)))
	for _, s := range samples {
		body = binary.BigEndian.AppendUint16(body, uint16(s))
	}
	return body
}

// packet wraps a body in the start byte, length and type.
func packet(kind byte, body []byte) []byte {
	out := make([]byte, 0, 4+len(body))
	out = append(out, startByte)
	out = binary.BigEndian.AppendUint16(out, uint16(len(body)))
	out = append(out, kind)
	return append(out, body...)
}

// sine is one frame of a tone, loud enough to be unambiguous and quiet enough
// not to clip.
func sine(hz float64) []int16 {
	out := make([]int16, samplesPerFrame)
	for i := range out {
		out[i] = int16(8000 * math.Sin(2*math.Pi*hz*float64(i)/8000))
	}
	return out
}

// ask sends one packet and prints what comes back, in full.
//
// **Everything is printed, including the parts that mean nothing yet.** A
// summary is a reading, and a reading is what this program exists to check.
func ask(conn net.Conn, wait time.Duration, label string, out []byte) {
	fmt.Printf("%-32s sent %d bytes\n", label, len(out))
	fmt.Printf("%s\n", indent(hex.Dump(out)))

	if _, err := conn.Write(out); err != nil {
		fmt.Printf("  write failed: %v\n\n", err)
		return
	}

	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		fmt.Printf("  no reply within %v: %v\n", wait, err)
		fmt.Printf("  (silence is a result: the packet was refused or misread)\n\n")
		return
	}

	fmt.Printf("%-32s got  %d bytes\n", "", n)
	fmt.Printf("%s", indent(hex.Dump(buf[:n])))
	if text := printable(buf[:n]); text != "" {
		fmt.Printf("  text %q\n", text)
	}
	fmt.Println()
}

func indent(s string) string {
	out := ""
	for _, line := range splitLines(s) {
		if line != "" {
			out += "  " + line + "\n"
		}
	}
	return out
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range s {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// printable pulls the readable characters out, which is how the product
// identifier and version were read in the first place.
func printable(b []byte) string {
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c >= 32 && c < 127 {
			out = append(out, c)
		}
	}
	return string(out)
}
