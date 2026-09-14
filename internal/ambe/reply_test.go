package ambe

import (
	"encoding/hex"
	"math"
	"testing"
)

// observed reads the exchanges captured from the operator's dongle.
func observed(t *testing.T) map[string][]byte {
	t.Helper()
	return records(t, "observed-exchanges.hex")
}

// TestTheBenchExchangeIsReproducedAndDecoded is the capture, as a test.
//
// On 2026-09-14 a DVstick 30 answered six packets in one run. Every request in
// that run is rebuilt here from its semantic parts and compared byte for byte
// against what was sent, and every reply is decoded. **This is the first AMBE
// exchange this project owns rather than reads about**, and it settles by
// observation several things that were previously settled by a contents page.
func TestTheBenchExchangeIsReproducedAndDecoded(t *testing.T) {
	fx := observed(t)

	// The four zero-argument queries and the rate packet, rebuilt.
	for _, tc := range []struct {
		name  string
		field byte
		args  []byte
	}{
		{"reset-request", 0x33, nil},
		{"prodid-request", 0x30, nil},
		{"version-request", 0x31, nil},
		{"getcfg-request", 0x36, nil},
		{"ratet-request", 0x09, []byte{RateIndexDMR}},
	} {
		got, err := Build(TypeControl, Val(tc.field, tc.args...))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if want, ok := fx[tc.name]; !ok {
			t.Errorf("no fixture named %s", tc.name)
		} else if hex.EncodeToString(got) != hex.EncodeToString(want) {
			t.Errorf("%s is built as %s and was sent as %s",
				tc.name, hex.EncodeToString(got), hex.EncodeToString(want))
		}
	}

	// The speech frame, rebuilt from the same sine the probe generates.
	samples := make([]int16, 160)
	for i := range samples {
		samples[i] = int16(8000 * math.Sin(2*math.Pi*1000*float64(i)/8000))
	}
	speech, err := SpeechD(samples)
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	got, err := Build(TypeSpeech, Val(0x40), speech)
	if err != nil {
		t.Fatalf("building the speech packet: %v", err)
	}
	if want := fx["speech-request"]; hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("the speech packet is built as\n  %s\nand was sent as\n  %s",
			hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

// TestTheReplyToASpeechPacketIsAThirtySixHundredBitPerSecondChannelFrame is
// the exchange the transcoder link is made of.
//
// Audio in, compressed frame out. `61 00 0b 01 01 48` and nine bytes: a
// channel packet carrying 72 bits, which in a 20 ms frame is 3600 bps — the
// rate Table 115 index 33 gives as interoperable with DMR and APCO P25 half
// rate.
//
// **Two claims are settled here that were previously taken from a contents
// page.** Speech in as type 0x02 produces a channel packet out as type 0x01,
// which patch 0351 swapped on a hypothesis and 0352 swapped back. And the
// length rule holds in the reply direction as well as the send direction.
//
// The frame size is also the only evidence that the rate took effect. The
// acknowledgement says a field was received; 72 bits says 3600 bps. CFG1 reads
// 0x00, so the board did not boot at this rate.
func TestTheReplyToASpeechPacketIsAThirtySixHundredBitPerSecondChannelFrame(t *testing.T) {
	pkt := observed(t)["channel-reply"]

	if pkt[3] != TypeChannel {
		t.Fatalf("the reply to a speech packet is type %#02x, want %#02x; "+
			"0351 swapped these on a hypothesis and the wire says otherwise",
			pkt[3], TypeChannel)
	}

	frame, ok := ChannelFrameFromResponse(pkt)
	if !ok {
		t.Fatal("the observed channel reply was not decoded")
	}
	if frame.Bits != 72 {
		t.Errorf("the frame carries %d bits, want 72", frame.Bits)
	}
	if frame.Rate() != 3600 {
		t.Errorf("the frame is %d bps, want 3600", frame.Rate())
	}
	if got, want := len(frame.Data), 9; got != want {
		t.Errorf("72 bits arrived in %d byte(s), want %d", got, want)
	}
	if got, want := hex.EncodeToString(frame.Data), "954be6500310b00777"; got != want {
		t.Errorf("the channel data is %s, want %s", got, want)
	}
}

// TestAChannelFrameWhoseBitCountDisagreesWithItsDataIsRefused is the
// reply-direction form of the defect that wedged the chip.
//
// A count that does not match its payload is not a frame, and reporting it as
// one would put a wrong number of bits into whatever consumed it. It is
// reported rather than trusted.
func TestAChannelFrameWhoseBitCountDisagreesWithItsDataIsRefused(t *testing.T) {
	for name, pkt := range map[string][]byte{
		"one byte short":  {StartByte, 0x00, 0x0A, TypeChannel, 0x01, 0x48, 1, 2, 3, 4, 5, 6, 7, 8},
		"one byte over":   {StartByte, 0x00, 0x0C, TypeChannel, 0x01, 0x48, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10},
		"too few bits":    {StartByte, 0x00, 0x07, TypeChannel, 0x01, 0x20, 1, 2, 3, 4, 5},
		"a speech packet": {StartByte, 0x00, 0x0B, TypeSpeech, 0x01, 0x48, 1, 2, 3, 4, 5, 6, 7, 8, 9},
		"length disagrees": {StartByte, 0x00, 0x0A, TypeChannel, 0x01, 0x48, 1, 2, 3, 4,
			5, 6, 7, 8, 9},
	} {
		if _, ok := ChannelFrameFromResponse(pkt); ok {
			t.Errorf("a reply with %s was decoded as a channel frame", name)
		}
	}
}

// TestAnAcknowledgementIsAFieldAndAStatusByte reads the rate reply.
//
// `61 00 02 00 09 00`: PKT_RATET echoed with a zero, which Table 43 gives as
// success. Anything other than zero indicates an error, so the status byte is
// returned rather than swallowed.
func TestAnAcknowledgementIsAFieldAndAStatusByte(t *testing.T) {
	field, status, ok := AckedField(observed(t)["ratet-reply"])
	if !ok {
		t.Fatal("the observed rate acknowledgement was not decoded")
	}
	if field != 0x09 {
		t.Errorf("the acknowledged field is %#02x, want 0x09", field)
	}
	if status != 0x00 {
		t.Errorf("the status byte is %#02x, want 0x00", status)
	}

	// A reset is answered by PKT_READY, which is a field on its own and not an
	// acknowledgement of anything.
	if _, _, ok := AckedField(observed(t)["reset-reply"]); ok {
		t.Error("the reset reply was read as an acknowledgement; it is a " +
			"PKT_READY field with no status byte")
	}
}

// TestTheTextRepliesDecodeWithoutTheirFramingBytes fixes what the probe used
// to print.
//
// The old output read "a0AMBE3000F" and "a11V121.E100..." because it sieved
// the printable characters out of the whole datagram: the start byte 0x61 as
// 'a', the field identifier 0x30 as '0', and in the version reply the length
// byte 0x31 as a stray '1' inside the text. Framing bytes read as payload is
// the same class of error as a byte read by eye, and this project has been
// wrong that way nine times.
func TestTheTextRepliesDecodeWithoutTheirFramingBytes(t *testing.T) {
	fx := observed(t)

	field, text, ok := TextFromResponse(fx["prodid-reply"])
	if !ok {
		t.Fatal("the observed product identifier reply was not decoded")
	}
	if field != 0x30 {
		t.Errorf("the product reply is field %#02x, want 0x30", field)
	}
	if text != "AMBE3000F" {
		t.Errorf("the product identifier is %q, want %q", text, "AMBE3000F")
	}

	field, text, ok = TextFromResponse(fx["version-reply"])
	if !ok {
		t.Fatal("the observed version reply was not decoded")
	}
	if field != 0x31 {
		t.Errorf("the version reply is field %#02x, want 0x31", field)
	}
	const want = "V121.E100.XXXX.C110.G514.R014.A0030608.C0020208"
	if text != want {
		t.Errorf("the version is %q, want %q", text, want)
	}
	// The variant matters: the R manual differs from the F where this work
	// touches, and the handover pointed at the R one for a while.
	if text[:4] != "V121" {
		t.Errorf("the version begins %q; this board is an AMBE3000F", text[:4])
	}
}

// TestTheConfigurationBytesFromTheBenchDecode reads the pins the board
// actually reported.
//
// `61 00 04 00 36 05 00 ec`. Three findings, each of which had been an
// assumption:
//
//   - CFG0 0x05 is packet mode over the UART, and CP_ENABLE is low, so speech
//     samples are 16-bit linear — which is what the probe sends.
//   - CFG1 0x00 means every RATE pin is low, so the board does not boot at the
//     DMR rate and setting it is a precondition for audio.
//   - CFG2 0xec has PARITY_ENABLE low, so parity is off. That pin carries an
//     internal pullup, so a floating pin would read high and parity would be
//     on; reading zero means the board ties it low deliberately.
func TestTheConfigurationBytesFromTheBenchDecode(t *testing.T) {
	cfg, ok := ConfigFromResponse(observed(t)["getcfg-reply"])
	if !ok {
		t.Fatal("the observed configuration reply was not decoded")
	}
	if cfg != [3]byte{0x05, 0x00, 0xec} {
		t.Fatalf("the configuration decoded as %#v", cfg)
	}
	if got, want := Mode(cfg), "packet mode over the UART"; got != want {
		t.Errorf("the mode reads %q, want %q", got, want)
	}
	if CompandingEnabledIn(cfg) {
		t.Error("companding read as enabled; CFG0 0x05 has CP_ENABLE low, and " +
			"the probe sends 16-bit linear samples")
	}
	if got := RateControlWordIn(cfg); got != 0 {
		t.Errorf("the boot rate control word reads %d, want 0", got)
	}
	if ParityEnabledIn(cfg) {
		t.Error("parity read as enabled; CFG2 0xec has bit 4 low")
	}
}
