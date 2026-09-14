package ambe

import (
	"encoding/hex"
	"math"
	"strings"
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

// TestEveryObservedPacketObeysTheLengthRule reads the rule off the capture.
//
// Section 6.5.2 in both directions, and this file is where the reply direction
// is evidenced. It also keeps the fixture honest: every record here is a whole
// datagram, so a partial packet added for illustration would fail this rather
// than sit waiting for something to misread it.
func TestEveryObservedPacketObeysTheLengthRule(t *testing.T) {
	for name, pkt := range observed(t) {
		if len(pkt) < 5 {
			t.Errorf("%s is %d bytes, which is not a whole packet", name, len(pkt))
			continue
		}
		if pkt[0] != StartByte {
			t.Errorf("%s starts with %#02x, want %#02x", name, pkt[0], StartByte)
		}
		if declared, got := int(pkt[1])<<8|int(pkt[2]), len(pkt)-4; declared != got {
			t.Errorf("%s declares %d field bytes and carries %d", name, declared, got)
		}
	}
}

// TestTheDecodeRequestIsTheFrameTheDongleMade covers the packet that proved
// the decode direction.
//
// `ambe-probe -decode 954be6500310b00777` sent the dongle's own channel frame
// back, and a 326-byte speech packet came out. Sending back a frame this chip
// produced is what makes a success a round trip rather than a guess about
// somebody else's bits.
func TestTheDecodeRequestIsTheFrameTheDongleMade(t *testing.T) {
	fx := observed(t)
	frame, ok := ChannelFrameFromResponse(fx["channel-reply"])
	if !ok {
		t.Fatal("the observed channel reply was not decoded")
	}
	chand, err := Chand(frame.Bits, frame.Data)
	if err != nil {
		t.Fatalf("building CHAND: %v", err)
	}
	got, err := Build(TypeChannel, Val(0x40), chand)
	if err != nil {
		t.Fatalf("building the channel packet: %v", err)
	}
	if want := fx["decode-request"]; hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("the decode request is built as %s and was sent as %s",
			hex.EncodeToString(got), hex.EncodeToString(want))
	}
}

// TestADecoderFlagWordSaysWhatTheDecoderDid is the reading that settles a
// near-silent reply.
//
// The first decode round trip came back with a peak sample of 3 where the
// frame had encoded a tone at amplitude 8000. Comfort noise, a frame repeat, a
// tone frame out of context and a decoder that has not ramped up all look the
// same in the samples, and Table 16 distinguishes three of them — so the chip
// is asked rather than reasoned about. **Not yet observed**: the flags are
// absent unless PKT_SPCHFMT requests them, and nothing has requested them on
// hardware yet.
func TestADecoderFlagWordSaysWhatTheDecoderDid(t *testing.T) {
	for _, tc := range []struct {
		flags DecoderFlags
		want  string
	}{
		{VoiceActive, "voice or tone synthesised"},
		{0, "comfort noise synthesised"},
		{DataInvalid, "data invalid"},
		{VoiceActive | ToneFrame, "tone frame"},
	} {
		if got := tc.flags.String(); !strings.Contains(got, tc.want) {
			t.Errorf("flags %#04x describe as %q, want it to mention %q",
				uint16(tc.flags), got, tc.want)
		}
	}

	// **The bit positions are literals here on purpose.** Writing
	// byte(DataInvalid) into the fixture and then asserting DataInvalid is a
	// test that encodes the same assumption as the code it tests: moving the
	// constant to the wrong bit left it passing. Table 16 gives DATA_INVALID
	// as bit 5, which is 0x20, and TONE_FRAME as bit 15, and those are the
	// numbers written below.
	if uint16(DataInvalid) != 0x0020 {
		t.Errorf("DATA_INVALID is %#04x, want 0x0020 — Table 16 bit 5",
			uint16(DataInvalid))
	}
	if uint16(VoiceActive) != 0x0002 {
		t.Errorf("VOICE_ACTIVE is %#04x, want 0x0002 — Table 16 bit 1",
			uint16(VoiceActive))
	}
	if uint16(ToneFrame) != 0x8000 {
		t.Errorf("TONE_FRAME is %#04x, want 0x8000 — Table 16 bit 15",
			uint16(ToneFrame))
	}

	// Table 65: bits 1 and 0 are the dcmode setting and 01 is "always contain
	// dcmode field"; every reserved bit must be zero or the manual warns of
	// unexpected results.
	if got, want := hex.EncodeToString(SpchFmtAlwaysDCMode), "0001"; got != want {
		t.Errorf("PKT_SPCHFMT asks for %s, want %s; %s would ask for no flags "+
			"at all and the reply would look the same as one that had none",
			got, want, got)
	}

	// A speech reply with a CMODE field carrying the flags, per Tables 16
	// and 65.
	samples := make([]int16, 160)
	speech, err := SpeechD(samples)
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	pkt, err := Build(TypeSpeech, speech, Val(0x02, 0x00, 0x20))
	if err != nil {
		t.Fatalf("building a speech reply with flags: %v", err)
	}
	reply, ok := SpeechReplyFromResponse(pkt)
	if !ok {
		t.Fatal("a speech reply carrying a CMODE field was not decoded")
	}
	if !reply.Reported {
		t.Error("the reply carried a CMODE field and the flags read as absent")
	}
	if reply.Flags&DataInvalid == 0 {
		t.Errorf("the flags read %#04x, want DATA_INVALID set", uint16(reply.Flags))
	}
	if len(reply.Samples) != 160 {
		t.Errorf("the reply carries %d samples, want 160", len(reply.Samples))
	}
	if reply.Peak() != 0 {
		t.Errorf("a silent reply has peak %d, want 0", reply.Peak())
	}

	// And the shape actually observed: samples, no flags.
	plain, err := Build(TypeSpeech, speech)
	if err != nil {
		t.Fatalf("building a plain speech reply: %v", err)
	}
	got, ok := SpeechReplyFromResponse(plain)
	if !ok {
		t.Fatal("a speech reply without a CMODE field was not decoded")
	}
	if got.Reported {
		t.Error("flags read as present in a reply that carried none; absent and " +
			"zero mean different things here, and zero means comfort noise")
	}

	// A field this build does not decode must be refused rather than stepped
	// over, and **this packet is built by hand because the shape is the whole
	// point.** Its fields are a three-byte TONE field whose data happens to be
	// 0x00 0xA0, followed by 320 bytes of samples — so a parser that stepped
	// over the unrecognised identifier one byte at a time would land exactly
	// on what looks like a complete SPEECHD field and return 160 samples that
	// were never sent.
	//
	// A reply built from any other shape does not distinguish the two: the
	// first version of this used TONE with 0x03 0x00 and the skip fell off the
	// end, so the broken parser refused it for a different reason and the test
	// passed either way.
	body := append([]byte{0x08, 0x00, 0xA0}, make([]byte, 320)...)
	hand := append([]byte{StartByte, byte(len(body) >> 8), byte(len(body)), TypeSpeech}, body...)
	if _, ok := SpeechReplyFromResponse(hand); ok {
		t.Error("a speech reply carrying a field this build does not decode was " +
			"accepted; an unrecognised field has to be refused, because " +
			"stepping over it by a guessed length reads the next field from " +
			"the middle of this one and returns samples nobody sent")
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

// TestAFrameWidthIdentifiesTheRateInEffect is the check that would have caught
// a probe run at the wrong rate.
//
// **A rate acknowledgement says a field arrived and nothing more.** On
// 2026-09-14 fifty frames were encoded at 48 bits — 2400 bps, index 0, the
// board's boot rate — because the code path taken had skipped PKT_RATET, and
// the only evidence was the width of the frames. Table 115 makes that width a
// derivable number rather than something to notice by eye.
func TestAFrameWidthIdentifiesTheRateInEffect(t *testing.T) {
	// The two rates this project has now seen on the wire.
	for _, tc := range []struct {
		index int
		bits  int
		why   string
	}{
		{RateIndexDMR, 72, "3600 bps, the DMR and P25 half-rate index"},
		{0, 48, "2400 bps, this board's boot rate with every RATE pin low"},
	} {
		got, ok := FrameBitsForRate(tc.index)
		if !ok {
			t.Errorf("rate index %d is not in Table 115", tc.index)
			continue
		}
		if got != tc.bits {
			t.Errorf("rate index %d gives %d bits per frame, want %d — %s",
				tc.index, got, tc.bits, tc.why)
		}
	}

	// The observed channel frame at index 33 is 72 bits, and that is the
	// evidence tying the table to the hardware.
	frame, ok := ChannelFrameFromResponse(observed(t)["channel-reply"])
	if !ok {
		t.Fatal("the observed channel reply was not decoded")
	}
	want, _ := FrameBitsForRate(RateIndexDMR)
	if frame.Bits != want {
		t.Errorf("the dongle returned %d bits at rate index %d and Table 115 "+
			"gives %d", frame.Bits, RateIndexDMR, want)
	}
	if frame.Rate() != TotalRates[RateIndexDMR] {
		t.Errorf("the frame is %d bps and Table 115 gives %d for index %d",
			frame.Rate(), TotalRates[RateIndexDMR], RateIndexDMR)
	}

	// Every rate in the table divides into whole bits per 20 ms frame. A rate
	// that did not would mean the table had been mistyped.
	for i, total := range TotalRates {
		if total%50 != 0 {
			t.Errorf("rate index %d is %d bps, which is not a whole number of "+
				"bits in a 20 ms frame", i, total)
		}
		if total < 2000 || total > 9600 {
			t.Errorf("rate index %d is %d bps, outside the 2000 to 9600 the "+
				"part supports", i, total)
		}
	}

	if _, ok := FrameBitsForRate(62); ok {
		t.Error("rate index 62 was accepted; Table 115 stops at 61")
	}
	if _, ok := FrameBitsForRate(-1); ok {
		t.Error("a negative rate index was accepted")
	}
}

// TestTheEncoderControlWordNamesTheBitsTableThirteenGives is the word that
// explains fifty identical frames.
//
// On 2026-09-14 a run of fifty frames of a 1 kHz sine came back byte-identical
// from frame 2 onward, the decoder reporting a tone frame each time. Tone
// detection is initialised to 1 at reset whatever the pins say (Table 13), so
// the encoder was emitting a tone descriptor rather than coding speech — the
// round trip was proved and the voice path was not.
//
// The bit positions are literals from Table 13 rather than expressions over
// the constants, because a test that builds its fixture from the value it
// asserts is the shape this project has now found fourteen times.
func TestTheEncoderControlWordNamesTheBitsTableThirteenGives(t *testing.T) {
	for _, tc := range []struct {
		bit  ECMode
		want uint16
		name string
	}{
		{ECNoiseSuppressor, 0x0040, "NS_ENABLE, bit 6"},
		{ECCompandALaw, 0x0080, "CP_SELECT, bit 7"},
		{ECCompand, 0x0100, "CP_ENABLE, bit 8"},
		{ECEchoSuppressor, 0x0200, "ES_ENABLE, bit 9"},
		{ECDiscontinuous, 0x0800, "DTX_ENABLE, bit 11"},
		{ECToneDetect, 0x1000, "TD_ENABLE, bit 12"},
		{ECEchoCanceller, 0x2000, "EC_ENABLE, bit 13"},
		{ECToneSend, 0x4000, "TS_ENABLE, bit 14"},
	} {
		if uint16(tc.bit) != tc.want {
			t.Errorf("%s is %#04x, want %#04x", tc.name, uint16(tc.bit), tc.want)
		}
	}

	// The reserved bits must stay clear, or the manual warns of unexpected
	// results.
	const reserved = 0x843F
	for _, m := range []ECMode{ECModeAtReset, ECToneDetect | ECNoiseSuppressor} {
		if uint16(m)&reserved != 0 {
			t.Errorf("control word %#04x sets a reserved bit", uint16(m))
		}
	}

	// This board's reset state: every pin-derived bit low, tone detection on.
	if ECModeAtReset != ECToneDetect {
		t.Errorf("the reset control word is %#04x, want tone detection alone; "+
			"CFG0 0x05 and CFG1 0x00 leave every pin-derived bit clear",
			uint16(ECModeAtReset))
	}

	// The field on the wire: identifier then the word, most significant byte
	// first, which is the convention for every 16-bit value in a packet.
	pkt, err := Build(TypeControl, ECModeField(ECToneDetect))
	if err != nil {
		t.Fatalf("building PKT_ECMODE: %v", err)
	}
	if got, want := hex.EncodeToString(pkt), "6100030005"+"1000"; got != want {
		t.Errorf("a tone-detection control packet is %s, want %s", got, want)
	}
	off, err := Build(TypeControl, ECModeField(0))
	if err != nil {
		t.Fatalf("building PKT_ECMODE: %v", err)
	}
	if got, want := hex.EncodeToString(off), "6100030005"+"0000"; got != want {
		t.Errorf("a control packet with nothing enabled is %s, want %s", got, want)
	}

	if got := ECModeAtReset.String(); !strings.Contains(got, "tone detection") {
		t.Errorf("the reset word describes as %q", got)
	}
	if got := ECMode(0).String(); got != "nothing enabled" {
		t.Errorf("an empty word describes as %q", got)
	}
}
