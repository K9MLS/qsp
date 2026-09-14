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
	"encoding/hex"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
)

// The packet framing, the field table and the builder now live in
// internal/ambe, built from the AMBE-3000F users manual version 3.7 and
// proved against the four worked example packets the manufacturer prints in
// its section 6.11.
//
// **Nothing here constructs a packet by hand any more, and that is the fix.**
// The gate that existed after the dongle was wedged checked that the field
// constants held the right values. They did. The call site passed the wrong
// one, every constant stayed correct, and the gate passed. A builder that
// consults the table is the only version of that check which cannot be walked
// past — see internal/ambe and its tests, which also record the incident.
//
// samplesPerFrame is 20 ms at 8 kHz, which is what the part takes for one
// compressed frame.
const samplesPerFrame = 160

func main() {
	server := flag.String("server", "127.0.0.1:2460", "AMBEserver address")
	tone := flag.Bool("tone", false, "send one frame of 1 kHz tone and print the reply")
	rate := flag.Int("rate", ambe.RateIndexDMR,
		"built-in rate index for PKT_RATET; 33 is 3600/2450/1150, the DMR and P25 half-rate one")
	wait := flag.Duration("wait", 2*time.Second, "how long to wait for each reply")
	flag.Parse()

	conn, err := net.Dial("udp", *server)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()
	fmt.Printf("talking to %s\n\n", *server)

	// The queries that carry no arguments, so that a run always says whether
	// the dongle is there before it says anything uncertain. Three of these
	// are exchanges this project has already seen answered.
	ask(conn, *wait, "reset", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldReset)))
	time.Sleep(500 * time.Millisecond)
	ask(conn, *wait, "product id", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldProdID)))
	ask(conn, *wait, "version", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldVersion)))

	// PKT_GETCFG reports the configuration pins as they were latched at boot,
	// and **CFG2 bit 4 is PARITY_ENABLE**. It runs with the confirmed group
	// rather than behind -tone because it takes no arguments and changes
	// nothing: it is the safest packet in the table, and it replaces an
	// inference this project has been carrying.
	//
	// The inference was sound — parity is enabled by default, a chip with it
	// enabled discards every packet that lacks a valid parity field, and this
	// board answered three packets that carried none, so parity must be off.
	// Sound reasoning has been wrong here before while the data was one query
	// away, which is the whole of section 8p's last entry.
	if reply, got := ask(conn, *wait, "get config", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldGetCfg))); got {
		if cfg, ok := ambe.ConfigFromResponse(reply); ok {
			fmt.Printf("  %-30s cfg0=%#02x cfg1=%#02x cfg2=%#02x\n", "configuration pins at boot",
				cfg[0], cfg[1], cfg[2])
			fmt.Printf("  %-30s %v (CFG2 bit 4)\n\n", "parity enabled",
				ambe.ParityEnabledIn(cfg))
		} else {
			fmt.Printf("  not a configuration response; the bytes above are the result\n\n")
		}
	}

	if !*tone {
		fmt.Println("\nnothing further attempted; pass -tone to try an audio frame")
		return
	}

	// **From here the packets are a reading, not a recording.**
	//
	// The rate goes through PKT_RATET, the one-byte index, and not through
	// PKT_RATEP. Sending a one-byte index to PKT_RATEP is what wedged this
	// operator's dongle on 2026-09-14, and PKT_RATEP's own length is the one
	// number in the table the manual and the software that circulates around
	// it disagree about — twelve against eleven. The index avoids the question
	// entirely and rate 33 is the rate that was wanted all along.
	fmt.Println("\n--- beyond what has been observed ---")
	ratePacket, err := ambe.Build(ambe.TypeControl, ambe.Val(fieldRateIndex, byte(*rate)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	ask(conn, *wait, fmt.Sprintf("set rate index %d", *rate), ratePacket)

	speech, err := ambe.SpeechD(sine(1000))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	framePacket, err := ambe.Build(ambe.TypeSpeech, ambe.Val(fieldChannel0), speech)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	ask(conn, *wait, "20 ms of 1 kHz tone as a speech packet", framePacket)
}

// The field identifiers this program sends, named here so that a call site
// reads as the manual does. Their lengths are not repeated — internal/ambe
// holds those, and repeating them is how the two got out of step in the first
// place.
const (
	fieldReset     = 0x33 // PKT_RESET, confirmed on the bench: answers 0x39
	fieldProdID    = 0x30 // PKT_PRODID, confirmed: answers AMBE3000F
	fieldVersion   = 0x31 // PKT_VERSTRING, confirmed: answers V121.E100...
	fieldGetCfg    = 0x36 // PKT_GETCFG, three bytes of configuration pins
	fieldRateIndex = 0x09 // PKT_RATET, one byte: an index into Table 115
	fieldChannel0  = 0x40 // PKT_CHANNEL0, a bare identifier
)

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
func ask(conn net.Conn, wait time.Duration, label string, out []byte) ([]byte, bool) {
	fmt.Printf("%-32s sent %d bytes\n", label, len(out))
	fmt.Printf("%s\n", indent(hex.Dump(out)))

	if _, err := conn.Write(out); err != nil {
		fmt.Printf("  write failed: %v\n\n", err)
		return nil, false
	}

	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		fmt.Printf("  no reply within %v: %v\n", wait, err)
		fmt.Printf("  (silence is a result: the packet was refused or misread)\n\n")
		return nil, false
	}

	fmt.Printf("%-32s got  %d bytes\n", "", n)
	fmt.Printf("%s", indent(hex.Dump(buf[:n])))
	if text := printable(buf[:n]); text != "" {
		fmt.Printf("  text %q\n", text)
	}
	fmt.Println()
	reply := make([]byte, n)
	copy(reply, buf[:n])
	return reply, true
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
