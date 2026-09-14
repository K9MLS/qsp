// Command ambe-probe is an experiment, not a server.
//
// It talks to an AMBEserver over UDP and prints every byte in both directions,
// so that the AMBE-3000 packet framing can be learned from a dongle on a bench
// rather than assumed from prose.
//
// **The dongle is the oracle, and on 2026-09-14 it answered everything.** A
// reset, a product identifier, a version string, a configuration read, a rate
// set and a 20 ms speech frame — the last of which came back as a channel
// packet carrying 72 bits, which is 3600 bps, which is DMR. The whole run is
// in testdata/ambe/observed-exchanges.hex and the tests in internal/ambe
// rebuild every request in it and decode every reply.
//
// So this program is no longer applying a register map to a device nobody has
// driven. What it does now is report what a board says about itself, which is
// worth doing on every run: the configuration pins are read rather than
// assumed, and the rate is confirmed by the size of the frame that comes back
// rather than by the acknowledgement, which only says a field arrived.
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
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"syscall"
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
	repeat := flag.Int("repeat", 1,
		"send the frame this many times; an AMBE decoder carries state and the "+
			"first frame after an init is commonly ramped or muted, so one frame "+
			"cannot tell a silent decoder from a cold one")
	roundtrip := flag.Int("roundtrip", 0,
		"encode this many consecutive 20 ms frames and decode them back in "+
			"order, reporting each frame compactly; a vocoder carries state and "+
			"one isolated frame encodes approximately nothing, so this is the "+
			"only shape of test that says anything about audio")
	decode := flag.String("decode", "",
		"send these channel bits back for decoding, as hex; try 954be6500310b00777, "+
			"the frame this dongle produced on 2026-09-14")
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
	if _, out := ask(conn, *wait, "reset", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldReset))); out == unreachable {
		refused(*server)
	}
	time.Sleep(500 * time.Millisecond)
	if _, out := ask(conn, *wait, "product id", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldProdID))); out == unreachable {
		refused(*server)
	}
	if _, out := ask(conn, *wait, "version", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldVersion))); out == unreachable {
		refused(*server)
	}

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
	if _, out := ask(conn, *wait, "get config", ambe.MustBuild(ambe.TypeControl, ambe.Val(fieldGetCfg))); out == unreachable {
		refused(*server)
	}

	if *roundtrip > 0 {
		roundTrip(conn, *wait, *roundtrip, *rate, *server)
		return
	}

	if !*tone && *decode == "" {
		fmt.Println("\nnothing further attempted; pass -tone to try an audio frame, " +
			"or -decode with a hex channel frame to try the other direction")
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

	// **Ask the decoder to say what it did.** The first decode round trip came
	// back with a peak sample of 3 where the frame had encoded a tone at
	// amplitude 8000. Comfort noise, a frame repeat, a tone frame decoded out
	// of context and a decoder that has not ramped up all look identical in
	// the samples, and Table 16's DCMODE_OUT distinguishes three of them.
	//
	// So this is a reading rather than an argument, which is what §8a says to
	// reach for: the flags are absent by default and PKT_SPCHFMT asks for them
	// in every output speech packet.
	spchfmt, err := ambe.Build(ambe.TypeControl,
		ambe.Val(0x16, ambe.SpchFmtAlwaysDCMode...))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	if _, out := ask(conn, *wait, "ask for the decoder's own flags", spchfmt); out == unreachable {
		refused(*server)
	}
	ratePacket, err := ambe.Build(ambe.TypeControl, ambe.Val(fieldRateIndex, byte(*rate)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	if _, out := ask(conn, *wait, fmt.Sprintf("set rate index %d", *rate), ratePacket); out == unreachable {
		refused(*server)
	}

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
	for i := 0; *tone && i < *repeat; i++ {
		label := "20 ms of 1 kHz tone as a speech packet"
		if *repeat > 1 {
			label = fmt.Sprintf("%s (%d of %d)", label, i+1, *repeat)
		}
		if _, out := ask(conn, *wait, label, framePacket); out == unreachable {
			refused(*server)
		}
	}

	// **The decode direction has never been observed.** The bench run of
	// 2026-09-14 captured speech in and a channel frame out; this is the
	// mirror, built from §6.8 and §6.9, and internal/ambe marks it unproved
	// for exactly that reason. A channel frame in should produce a speech
	// packet out — 160 samples of whatever the dongle makes of those bits.
	//
	// The frame worth sending is the one this dongle produced, so that a
	// success is a round trip rather than a guess about somebody else's bits.
	if *decode != "" {
		bits, err := hex.DecodeString(*decode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ambe-probe: -decode is not hex: %v\n", err)
			os.Exit(1)
		}
		chand, err := ambe.Chand(len(bits)*8, bits)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
			os.Exit(1)
		}
		channelPacket, err := ambe.Build(ambe.TypeChannel, ambe.Val(fieldChannel0), chand)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
			os.Exit(1)
		}
		for i := 0; i < *repeat; i++ {
			label := fmt.Sprintf("%d bits of channel data for decoding", len(bits)*8)
			if *repeat > 1 {
				label = fmt.Sprintf("%s (%d of %d)", label, i+1, *repeat)
			}
			if _, out := ask(conn, *wait, label, channelPacket); out == unreachable {
				refused(*server)
			}
		}
	}
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

// An outcome distinguishes the three things that can happen, because two of
// them used to print the same sentence.
type outcome int

const (
	answered    outcome = iota // the chip replied
	silent                     // the packet reached a listener and got nothing back
	unreachable                // nothing is listening; no packet reached the chip
)

// ask sends one packet and prints what comes back, in full.
//
// **Everything is printed, including the parts that mean nothing yet.** A
// summary is a reading, and a reading is what this program exists to check.
//
// **A refused port is not silence, and conflating them cost two bench runs.**
// On 2026-09-14 this program fired six packets at a host with nothing
// listening on 2460 and reported each one as "the packet was refused or
// misread" — a sentence about the packet format, when the truth was that no
// datagram had left the sending host's stack. ECONNREFUSED on a connected UDP
// socket is an ICMP port-unreachable coming back, and it says nothing about
// AMBE at all. A status that does not name its subject sends the reader to the
// wrong layer, which is the same defect the health report had.
func ask(conn net.Conn, wait time.Duration, label string, out []byte) ([]byte, outcome) {
	fmt.Printf("%-32s sent %d bytes\n", label, len(out))
	fmt.Printf("%s\n", indent(hex.Dump(out)))

	if _, err := conn.Write(out); err != nil {
		if isUnreachable(err) {
			return nil, unreachable
		}
		fmt.Printf("  write failed: %v\n\n", err)
		return nil, silent
	}

	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		if isUnreachable(err) {
			return nil, unreachable
		}
		fmt.Printf("  no reply within %v: %v\n", wait, err)
		fmt.Printf("  (silence is a result: the packet reached a listener and " +
			"was refused or misread)\n\n")
		return nil, silent
	}

	fmt.Printf("%-32s got  %d bytes\n", "", n)
	fmt.Printf("%s", indent(hex.Dump(buf[:n])))
	reply := make([]byte, n)
	copy(reply, buf[:n])
	describe(reply)
	fmt.Println()
	return reply, answered
}

// roundTrip encodes a run of frames and decodes the run back in order.
//
// **One frame says nothing about audio.** On 2026-09-14 a single 20 ms frame
// of a 1 kHz tone was encoded and decoded back, and it came out with a peak
// sample of 3 — and the decoder's own DCMODE_OUT flags said VOICE_ACTIVE with
// DATA_INVALID clear, which is the chip reporting a valid voice frame
// faithfully decoded. So nothing was broken: an AMBE encoder carries state,
// and the first frame out of a freshly reset one encodes approximately
// nothing. The frame was a valid encoding of silence.
//
// A vocoder is tested with a stream, and a stream is also what QSP will send —
// fifty frames a second for the length of a transmission. This does that and
// prints one line per frame, because a hundred hex dumps of 329 bytes is not a
// result anybody reads.
func roundTrip(conn net.Conn, wait time.Duration, frames, rate int, server string) {
	// **This sets the rate and the flags itself.** The first version of this
	// returned before the block that sent them, so a fifty-frame run went out
	// at the board's boot rate of 2400 bps and every reply came back with no
	// decoder flags — after the same program had printed that setting the rate
	// is a precondition for audio. A precondition that a code path can skip is
	// not a precondition, so it lives with the thing that needs it.
	for _, step := range []struct {
		what  string
		field byte
		args  []byte
	}{
		{"decoder flags", 0x16, ambe.SpchFmtAlwaysDCMode},
		{"rate index", 0x09, []byte{byte(rate)}},
	} {
		pkt, err := ambe.Build(ambe.TypeControl, ambe.Val(step.field, step.args...))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
			os.Exit(1)
		}
		reply, out := quiet(conn, wait, pkt)
		if out == unreachable {
			refused(server)
		}
		field, status, ok := ambe.AckedField(reply)
		if out != answered || !ok || field != step.field || status != 0x00 {
			fmt.Fprintf(os.Stderr, "ambe-probe: the %s was not accepted: %x\n",
				step.what, reply)
			os.Exit(1)
		}
	}

	wantBits, ok := ambe.FrameBitsForRate(rate)
	if !ok {
		fmt.Fprintf(os.Stderr, "ambe-probe: rate index %d is not in Table 115\n", rate)
		os.Exit(1)
	}

	fmt.Printf("\n--- %d frames at rate index %d, %d bps, %d bits a frame ---\n\n",
		frames, rate, ambe.TotalRates[rate], wantBits)
	fmt.Printf("  %-6s %-20s %5s %8s %8s  %s\n",
		"frame", "channel data", "bits", "in", "out", "decoder says")

	samples := sine(1000)
	speech, err := ambe.SpeechD(samples)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}
	speechPacket, err := ambe.Build(ambe.TypeSpeech, ambe.Val(fieldChannel0), speech)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ambe-probe: %v\n", err)
		os.Exit(1)
	}

	inPeak := 0
	for _, v := range samples {
		if int(v) > inPeak {
			inPeak = int(v)
		}
	}

	// Encode the whole run first, so the encoder sees a continuous stream
	// rather than an encode and a decode alternating on one chip.
	encoded := make([]ambe.ChannelFrame, 0, frames)
	for i := 0; i < frames; i++ {
		reply, out := quiet(conn, wait, speechPacket)
		if out == unreachable {
			refused(server)
		}
		if out != answered {
			fmt.Printf("  %-6d no reply to a speech packet\n", i+1)
			return
		}
		frame, ok := ambe.ChannelFrameFromResponse(reply)
		if !ok {
			fmt.Printf("  %-6d a speech packet was answered by %x\n", i+1, reply)
			return
		}
		// **The frame width is the only evidence of the rate in effect.** The
		// acknowledgement above says a field arrived; 48 bits where 72 was
		// asked for says the rate did not take, and that is what a run at the
		// boot rate looked like before anything checked.
		if frame.Bits != wantBits {
			fmt.Printf("  %-6d %-20x %5d  the rate did not take: %d bps where "+
				"index %d is %d\n", i+1, frame.Data, frame.Bits,
				frame.Rate(), rate, ambe.TotalRates[rate])
			return
		}
		encoded = append(encoded, frame)
	}

	// Then decode the run in the order it was produced, which is the order a
	// receiver would get it in.
	for i, frame := range encoded {
		chand, err := ambe.Chand(frame.Bits, frame.Data)
		if err != nil {
			fmt.Printf("  %-6d %v\n", i+1, err)
			return
		}
		pkt, err := ambe.Build(ambe.TypeChannel, ambe.Val(fieldChannel0), chand)
		if err != nil {
			fmt.Printf("  %-6d %v\n", i+1, err)
			return
		}
		reply, out := quiet(conn, wait, pkt)
		if out == unreachable {
			refused(server)
		}
		if out != answered {
			fmt.Printf("  %-6d %-20x %5d %8d %8s\n",
				i+1, frame.Data, frame.Bits, inPeak, "no reply")
			continue
		}
		speechReply, ok := ambe.SpeechReplyFromResponse(reply)
		if !ok {
			fmt.Printf("  %-6d %-20x %5d %8d  a channel packet was answered by "+
				"%d bytes this build does not decode\n",
				i+1, frame.Data, frame.Bits, inPeak, len(reply))
			continue
		}
		says := "no flags requested"
		if speechReply.Reported {
			says = speechReply.Flags.String()
		}
		fmt.Printf("  %-6d %-20x %5d %8d %8d  %s\n",
			i+1, frame.Data, frame.Bits, inPeak, speechReply.Peak(), says)
	}

	fmt.Printf("\n  **Read the out column across the run, not any one row.** If it " +
		"climbs toward the in column the vocoder is warming up and the round " +
		"trip works; if it stays near zero with the decoder reporting valid " +
		"voice frames, the encoder is producing silence and the input is the " +
		"thing to change.\n")
}

// quiet sends one packet and returns its reply without printing anything.
//
// The full dump is what ask does and what a protocol question needs. A run of
// a hundred frames needs a table instead, so this is the same exchange with
// the transcript left out.
func quiet(conn net.Conn, wait time.Duration, out []byte) ([]byte, outcome) {
	if _, err := conn.Write(out); err != nil {
		if isUnreachable(err) {
			return nil, unreachable
		}
		return nil, silent
	}
	_ = conn.SetReadDeadline(time.Now().Add(wait))
	buf := make([]byte, 2048)
	n, err := conn.Read(buf)
	if err != nil {
		if isUnreachable(err) {
			return nil, unreachable
		}
		return nil, silent
	}
	reply := make([]byte, n)
	copy(reply, buf[:n])
	return reply, answered
}

// isUnreachable reports whether an error is a port-unreachable rather than a
// timeout.
//
// On a connected UDP socket an ICMP port-unreachable surfaces as ECONNREFUSED,
// wrapped in a *net.OpError, on either the write or the following read
// depending on timing — so the classification is by errors.Is and not by which
// call returned it.
func isUnreachable(err error) bool {
	return errors.Is(err, syscall.ECONNREFUSED)
}

// refused reports that nothing is listening and stops.
//
// It stops rather than continuing because the remaining packets would each
// print the same thing, and because one of them is an audio frame: sending 327
// bytes at a closed port is noise in a transcript that somebody will later try
// to read as a protocol result.
func refused(server string) {
	fmt.Printf("  nothing is listening on %s\n", server)
	fmt.Printf("  (ICMP port unreachable — no packet reached the dongle, and " +
		"this says nothing about the packet format)\n\n")
	fmt.Printf("start AMBEserver on the host holding the dongle and run this " +
		"again; -x is its debug flag and -v only prints a version\n")
	os.Exit(1)
}

// describe decodes a reply into the fields it carries.
//
// **It decodes rather than sieves.** This used to print the printable
// characters of the whole datagram, which rendered the product reply as
// "a0AMBE3000F" — the start byte as 'a' and the field identifier 0x30 as '0' —
// and put the version reply's length byte in the middle of its text as a stray
// '1'. Framing bytes read as payload is the same class of error as a byte read
// by eye, and this project has been wrong that way nine times.
//
// Only the four reply shapes this project has captured are decoded. Anything
// else says so rather than being guessed at; the bytes are above it either
// way.
func describe(reply []byte) {
	if field, text, ok := ambe.TextFromResponse(reply); ok {
		fmt.Printf("  %-30s %q (field %#02x)\n", "text", text, field)
		return
	}
	if cfg, ok := ambe.ConfigFromResponse(reply); ok {
		fmt.Printf("  %-30s cfg0=%#02x cfg1=%#02x cfg2=%#02x\n",
			"configuration pins at boot", cfg[0], cfg[1], cfg[2])
		fmt.Printf("  %-30s %s\n", "mode", ambe.Mode(cfg))
		fmt.Printf("  %-30s %v\n", "companding enabled", ambe.CompandingEnabledIn(cfg))
		fmt.Printf("  %-30s %v (CFG2 bit 4)\n", "parity enabled", ambe.ParityEnabledIn(cfg))
		fmt.Printf("  %-30s %d\n", "boot rate control word", ambe.RateControlWordIn(cfg))
		if ambe.RateControlWordIn(cfg) == 0 {
			fmt.Printf("  %-30s the RATE pins are all low, so this board does "+
				"not boot at the DMR rate\n", "")
			fmt.Printf("  %-30s setting it with PKT_RATET is a precondition "+
				"for audio, not a refinement\n", "")
		}
		return
	}
	if frame, ok := ambe.ChannelFrameFromResponse(reply); ok {
		fmt.Printf("  %-30s %d bits in 20 ms, so %d bps\n", "channel frame",
			frame.Bits, frame.Rate())
		fmt.Printf("  %-30s %x\n", "channel data", frame.Data)
		if frame.Rate() != 3600 {
			fmt.Printf("  %-30s 3600 bps was expected; the rate did not take\n", "")
		}
		return
	}
	if speech, ok := ambe.SpeechReplyFromResponse(reply); ok {
		fmt.Printf("  %-30s %d samples, peak %d\n", "speech frame",
			len(speech.Samples), speech.Peak())
		if speech.Reported {
			fmt.Printf("  %-30s %s\n", "decoder says", speech.Flags)
		} else {
			fmt.Printf("  %-30s no flags in this reply; PKT_SPCHFMT asks for "+
				"them and absent is not the same as zero\n", "decoder says")
		}
		if speech.Peak() < 100 {
			fmt.Printf("  %-30s near silence — read the decoder's flags above "+
				"rather than the samples\n", "")
		}
		return
	}
	if field, status, ok := ambe.AckedField(reply); ok {
		if status == 0x00 {
			fmt.Printf("  %-30s field %#02x accepted\n", "acknowledged", field)
		} else {
			fmt.Printf("  %-30s field %#02x answered %#02x, which is an error\n",
				"acknowledged", field, status)
		}
		return
	}
	if len(reply) > 4 && reply[3] == ambe.TypeControl && reply[4] == 0x39 {
		fmt.Printf("  %-30s PKT_READY; the chip has reset and re-read its "+
			"configuration pins\n", "ready")
		return
	}
	fmt.Printf("  %-30s no decoder for this reply shape; the bytes above are "+
		"the result\n", "undecoded")
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
