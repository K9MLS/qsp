package ambe

import (
	"bufio"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixtures reads the manufacturer's example packets.
//
// They live in a file rather than in this source so that they can be compared
// against a printed page without reading Go.
func fixtures(t *testing.T) map[string][]byte {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "ambe", "manual-examples.hex")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s: %v", path, err)
	}
	defer f.Close()

	out := map[string][]byte{}
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, body, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("%s: %q is not a name and a hex string", path, line)
		}
		b, err := hex.DecodeString(strings.TrimSpace(body))
		if err != nil {
			t.Fatalf("%s: record %s: %v", path, name, err)
		}
		out[name] = b
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s holds no records", path)
	}
	return out
}

// ramp is the 160-sample sequence the manual's speech examples carry, which
// its own prose describes as incrementing from 0 to 159.
func ramp() []int16 {
	out := make([]int16, 160)
	for i := range out {
		out[i] = int16(i)
	}
	return out
}

// TestTheBuilderReproducesTheManufacturersExamplePacketsByteForByte is the
// central test of this package.
//
// Four packets printed by the people who made the chip, rebuilt from their
// semantic parts and compared byte for byte. If the framing rule, the field
// identifiers or the length arithmetic are wrong in any way that matters, one
// of these four will not match — and none of it needs hardware.
//
// This replaces a gate that asserted a constant had a particular value. That
// gate passed while the program could still send the packet that wedged a
// dongle, because the constant was never what was wrong.
func TestTheBuilderReproducesTheManufacturersExamplePacketsByteForByte(t *testing.T) {
	fx := fixtures(t)

	speechd, err := SpeechD(ramp())
	if err != nil {
		t.Fatalf("building SPEECHD: %v", err)
	}
	chand10, err := Chand(80, []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99})
	if err != nil {
		t.Fatalf("building CHAND: %v", err)
	}
	chand7, err := Chand(56, []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66})
	if err != nil {
		t.Fatalf("building CHAND: %v", err)
	}

	for _, tc := range []struct {
		name   string
		table  string
		kind   byte
		fields []FieldValue
	}{
		{"speech-example-1", "Table 111", TypeSpeech, []FieldValue{
			Val(0x40), speechd,
		}},
		{"speech-example-2", "Table 112", TypeSpeech, []FieldValue{
			Val(0x40), speechd, Val(0x02, 0x00, 0x00), Val(0x08, 0x03, 0x00),
		}},
		{"channel-example-1", "Table 113", TypeChannel, []FieldValue{
			chand10,
		}},
		{"channel-example-2", "Table 114", TypeChannel, []FieldValue{
			Val(0x40), chand7, Val(0x03, 0xA1), Val(0x02, 0x00, 0x00),
		}},
	} {
		want, ok := fx[tc.name]
		if !ok {
			t.Errorf("no fixture named %s", tc.name)
			continue
		}
		got, err := Build(tc.kind, tc.fields...)
		if err != nil {
			t.Errorf("%s (%s): %v", tc.name, tc.table, err)
			continue
		}
		if hex.EncodeToString(got) != hex.EncodeToString(want) {
			t.Errorf("%s (%s) is built as\n  %s\nbut the manufacturer prints\n  %s",
				tc.name, tc.table, hex.EncodeToString(got), hex.EncodeToString(want))
		}
	}
}

// TestTheLengthCountsFieldBytesAndExcludesTheHeader reads the rule off the
// fixtures rather than restating it.
//
// Section 6.5.2 says the length is the total packet size minus four. All four
// examples agree. The prose printed under two of them does not — it gives
// 0x0144 for Table 111 and 0x0010 for Table 114, each one more than the table
// beside it — which is why the rule is checked against the tables' own bytes
// and not against the sentences describing them.
func TestTheLengthCountsFieldBytesAndExcludesTheHeader(t *testing.T) {
	for name, pkt := range fixtures(t) {
		if pkt[0] != StartByte {
			t.Errorf("%s starts with %#02x, want %#02x", name, pkt[0], StartByte)
		}
		declared := int(pkt[1])<<8 | int(pkt[2])
		if got := len(pkt) - 4; declared != got {
			t.Errorf("%s declares %d field bytes and carries %d; the length "+
				"counts the fields and the parity bytes and excludes the four "+
				"header bytes", name, declared, got)
		}
	}
}

// TestThePacketThatWedgedTheDongleIsRefused is the incident, as a test.
//
// `61 00 02 00 0a 21` was sent on 2026-09-14: PKT_RATEP with one argument
// byte where the manual gives twelve. The chip waited for the other eleven,
// took them from the head of the next packet, and never recovered its frame.
// A physical unplug was the only thing that cleared it — not a soft reset, not
// an ESXi detach and re-attach.
//
// Nothing here needs a dongle, which is the point. This class of defect costs
// a device rather than a test run, so it belongs where it can fail for free.
func TestThePacketThatWedgedTheDongleIsRefused(t *testing.T) {
	_, err := Build(TypeControl, Val(0x0A, RateIndexDMR))
	if err == nil {
		t.Fatal("PKT_RATEP with one argument byte was accepted; it takes " +
			"twelve, and sending it short leaves the chip mid-field until " +
			"its power is removed")
	}
	if !errors.Is(err, ErrWrongLength) {
		t.Errorf("refused with %v, want a length error", err)
	}

	// And the packet that was meant: the same rate, through the one-byte
	// field.
	got, err := Build(TypeControl, Val(0x09, RateIndexDMR))
	if err != nil {
		t.Fatalf("PKT_RATET with a rate index: %v", err)
	}
	if want := "6100020009" + "21"; hex.EncodeToString(got) != want {
		t.Errorf("the DMR rate packet is %s, want %s", hex.EncodeToString(got), want)
	}
}

// TestPktRatepIsSixRateControlWords pins the number that cost a dongle.
//
// **This test exists because its absence was found by breaking the table.**
// Setting PKT_RATEP back to eleven data bytes left every other test in this
// file passing: the refusal test above only proves that one argument byte is
// rejected, and one is rejected whether the field takes eleven or twelve. A
// test that cannot fail is the ninth recorded instance of that shape in this
// project, so here is the assertion that does fail.
//
// The evidence is Table 44, which gives the field as six rate control words,
// and Table 45, which prints all six for a custom rate of 2800 bps voice with
// no FEC. Six sixteen-bit words is twelve bytes, and the field including its
// identifier is thirteen — which is what Table 44's own caption says.
//
// The eleven comes from a byte string that circulates with other software,
// `0a 01 30 07 63 40 00 00 00 00 00 48`, and it is one byte short of six
// words. A primary source beats a transcript. Neither has been sent to this
// chip and neither should be until the recovery path is in reach, which is why
// PKT_RATET exists and is what the probe uses.
func TestPktRatepIsSixRateControlWords(t *testing.T) {
	f, ok := lookup(TypeControl, 0x0A)
	if !ok {
		t.Fatal("PKT_RATEP is not in the control table")
	}
	if f.Data != 12 {
		t.Errorf("PKT_RATEP takes %d data bytes; Table 44 gives six rate "+
			"control words and Table 45 prints six, which is twelve bytes", f.Data)
	}

	// Table 45, printed page 65: 2800 bps voice, 0 bps FEC.
	words := []uint16{0x0038, 0x0765, 0x0000, 0x0000, 0x0000, 0x0038}
	data := make([]byte, 0, 12)
	for _, w := range words {
		data = append(data, byte(w>>8), byte(w))
	}
	if len(data) != f.Data {
		t.Fatalf("Table 45's six words are %d bytes and the table says %d",
			len(data), f.Data)
	}
	pkt, err := Build(TypeControl, Val(0x0A, data...))
	if err != nil {
		t.Fatalf("Table 45's own rate words were refused: %v", err)
	}
	if want := "61000d00" + "0a" + "003807650000000000000038"; hex.EncodeToString(pkt) != want {
		t.Errorf("Table 45's rate packet is %s, want %s", hex.EncodeToString(pkt), want)
	}

	// The eleven-byte string, refused for being one byte short of six words.
	short := []byte{0x01, 0x30, 0x07, 0x63, 0x40, 0x00, 0x00, 0x00, 0x00, 0x00, 0x48}
	if _, err := Build(TypeControl, Val(0x0A, short...)); err == nil {
		t.Error("the eleven-byte rate string was accepted; the manual gives " +
			"twelve and the difference is a field boundary the chip cannot recover")
	}
}

// TestPktChannel0IsABareIdentifierInEveryTable closes a hole the break suite
// found.
//
// **Giving PKT_CHANNEL0 one data byte in the control table left every test in
// this file passing.** The four fixtures all build it as `Val(0x40)` with no
// arguments, and in a speech or channel packet that call would then be
// refused and the fixture comparison would fail — but nothing builds a control
// packet containing 0x40, so the control table's row had no gate on it at all.
// The disputed field this project was most confident about was the one nothing
// checked.
//
// Zero data bytes is what Table 33 says, and it is what the arithmetic
// demands: Table 111 declares 0x0143 for a bare 0x40 plus a 322-byte SPEECHD
// field, and Table 114 declares 0x000F for 0x40 plus nine, two and three. One
// data byte would make both of those one too small. Table 98's "1 byte" and
// the channel prose's "2 bytes" are counting something else.
func TestPktChannel0IsABareIdentifierInEveryTable(t *testing.T) {
	for _, kind := range []byte{TypeControl, TypeSpeech, TypeChannel} {
		f, ok := lookup(kind, 0x40)
		if !ok {
			t.Errorf("PKT_CHANNEL0 is not in the %s table", typeName(kind))
			continue
		}
		if f.Data != 0 {
			t.Errorf("PKT_CHANNEL0 takes %d data byte(s) in the %s table; "+
				"Table 33 gives none and the worked examples' lengths only "+
				"add up if 0x40 contributes exactly one byte",
				f.Data, typeName(kind))
		}
		pkt, err := Build(kind, Val(0x40))
		if err != nil {
			t.Errorf("a bare PKT_CHANNEL0 in a %s packet was refused: %v",
				typeName(kind), err)
			continue
		}
		if got := int(pkt[1])<<8 | int(pkt[2]); got != 1 {
			t.Errorf("a %s packet holding only PKT_CHANNEL0 declares %d field "+
				"byte(s), want 1", typeName(kind), got)
		}
		if _, err := Build(kind, Val(0x40, 0x00)); err == nil {
			t.Errorf("PKT_CHANNEL0 in a %s packet accepted a data byte",
				typeName(kind))
		}
	}
}

// TestAConfigurationResponseIsParsedOrRefused covers the reply that answers
// the parity question.
//
// Tables 77 and 79: the query's identifier followed by CFG0, CFG1 and CFG2.
// The refusals matter as much as the acceptance — a short or mislabelled reply
// read as a configuration would answer "parity is off" for a packet that said
// nothing of the kind, and that is the sort of confident wrong answer this
// project keeps finding.
func TestAConfigurationResponseIsParsedOrRefused(t *testing.T) {
	good := []byte{StartByte, 0x00, 0x04, TypeControl, 0x36, 0xA5, 0x0F, 1 << 4}
	cfg, ok := ConfigFromResponse(good)
	if !ok {
		t.Fatal("a well-formed PKT_GETCFG response was refused")
	}
	if cfg != [3]byte{0xA5, 0x0F, 1 << 4} {
		t.Errorf("configuration read as %#v", cfg)
	}
	if !ParityEnabledIn(cfg) {
		t.Error("CFG2 bit 4 was set and parity read as disabled")
	}

	for name, pkt := range map[string][]byte{
		"empty":            {},
		"truncated":        {StartByte, 0x00, 0x04, TypeControl, 0x36, 0x00},
		"wrong start":      {0x62, 0x00, 0x04, TypeControl, 0x36, 0x00, 0x00, 0x00},
		"wrong type":       {StartByte, 0x00, 0x04, TypeSpeech, 0x36, 0x00, 0x00, 0x00},
		"wrong field":      {StartByte, 0x00, 0x04, TypeControl, 0x30, 0x00, 0x00, 0x00},
		"length disagrees": {StartByte, 0x00, 0x03, TypeControl, 0x36, 0x00, 0x00, 0x00},
	} {
		if _, ok := ConfigFromResponse(pkt); ok {
			t.Errorf("a %s packet was read as a configuration response", name)
		}
	}
}

// TestEveryFixedLengthFieldRefusesEveryOtherLength walks the whole table.
//
// One field at a time, given one byte too few and one too many. A table this
// long is exactly where a single wrong row hides, and a wrong row here is the
// thing that removes power from a device to fix.
func TestEveryFixedLengthFieldRefusesEveryOtherLength(t *testing.T) {
	for _, kind := range []byte{TypeControl, TypeSpeech, TypeChannel} {
		table, _ := FieldsFor(kind)
		for _, f := range table {
			if f.Data == Variable {
				continue
			}
			if _, err := Build(kind, Val(f.ID, make([]byte, f.Data)...)); err != nil {
				t.Errorf("%s (%#02x) in a %s packet refuses its own length of %d: %v",
					f.Name, f.ID, typeName(kind), f.Data, err)
			}
			for _, n := range []int{f.Data - 1, f.Data + 1} {
				if n < 0 {
					continue
				}
				if _, err := Build(kind, Val(f.ID, make([]byte, n)...)); err == nil {
					t.Errorf("%s (%#02x) in a %s packet accepted %d data byte(s) "+
						"where the manual gives %d", f.Name, f.ID, typeName(kind), n, f.Data)
				}
			}
		}
	}
}

// TestAFieldFromTheWrongTableIsRefused keeps the three packet types apart.
//
// The identifiers overlap: 0x02 is PKT_CHANNEL0's neighbour CMODE in a speech
// packet and also a whole packet type; 0x30 is PKT_PRODID in a control packet
// and, according to one of the manual's two answers, SAMPLES in a channel
// packet. A field is only meaningful inside its own type.
func TestAFieldFromTheWrongTableIsRefused(t *testing.T) {
	// PKT_RESET is a control field and means nothing in a speech packet.
	if _, err := Build(TypeSpeech, Val(0x33)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("a control field in a speech packet gave %v, want an unknown-field error", err)
	}
	// SPEECHD's identifier is not a control field.
	if _, err := Build(TypeControl, Val(0x00, 0x01)); !errors.Is(err, ErrUnknownField) {
		t.Errorf("a speech field in a control packet gave %v, want an unknown-field error", err)
	}
	if _, err := Build(0x07, Val(0x40)); !errors.Is(err, ErrUnknownType) {
		t.Errorf("packet type 0x07 gave %v, want an unknown-type error", err)
	}
}

// TestTheConfirmedControlPacketsAreUnchanged is the exchange that actually
// happened, kept as the definition.
//
// On 2026-09-14 an AMBE3000F on the operator's bench answered
// `61 00 01 00 30` with `61 00 0b 00 30 41 4d 42 45 33 30 30 30 46 00`, and
// `61 00 01 00 33` with a packet containing 0x39. The first draft of the probe
// emitted the reset as `61 00 02 00 33`, because the length was written to
// include the type byte. It does not.
//
// **A dongle refusing a malformed packet looks exactly like a dead dongle**,
// so this is the difference between an hour and a week.
func TestTheConfirmedControlPacketsAreUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field byte
		want  string
	}{
		{"reset", 0x33, "6100010033"},
		{"product id", 0x30, "6100010030"},
		{"version", 0x31, "6100010031"},
		{"get config", 0x36, "6100010036"},
	} {
		got, err := Build(TypeControl, Val(tc.field))
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if hex.EncodeToString(got) != tc.want {
			t.Errorf("%s is %s, want %s", tc.name, hex.EncodeToString(got), tc.want)
		}
	}
}

// TestSamplesIsTheIdentifierTheManufacturersExampleUses records which of the
// manual's two answers was taken, and why.
//
// Table 106 gives SAMPLES as 0x30 and Table 109 gives 0x03. Channel Packet
// Example 2 prints `03 A1` for a decoder asked to produce 161 samples, so the
// example decides it. This test exists so that a future reader who finds
// Table 106 first cannot change it back without a failure.
func TestSamplesIsTheIdentifierTheManufacturersExampleUses(t *testing.T) {
	f, ok := lookup(TypeChannel, 0x03)
	if !ok || f.Name != "SAMPLES" {
		t.Fatal("SAMPLES is not 0x03 in the channel table; Table 109 and " +
			"Channel Packet Example 2 both say it is, against Table 106's 0x30")
	}
	if _, ok := lookup(TypeChannel, 0x30); ok {
		t.Error("0x30 is also a channel field; Table 106's reading was taken " +
			"as well as Table 109's, and only one of them can be right")
	}
}

// TestADisputedFieldSaysSo guards the ambiguities.
//
// Three fields have more than one answer in the manual. Each carries the
// dispute in its table entry, because the failure mode here is not a wrong
// guess — it is a right guess that looks arbitrary to the next reader, who
// tidies it away. Where a length is wrong the chip needs its power removed,
// so the reasoning travels with the value.
func TestADisputedFieldSaysSo(t *testing.T) {
	want := map[string][]byte{
		"PKT_CHANNEL0":  {TypeControl, TypeSpeech, TypeChannel},
		"PKT_RTSTHRESH": {TypeControl},
		"SAMPLES":       {TypeChannel},
	}
	for name, kinds := range want {
		for _, kind := range kinds {
			table, _ := FieldsFor(kind)
			found := false
			for _, f := range table {
				if f.Name != name {
					continue
				}
				found = true
				if f.Disputed == "" {
					t.Errorf("%s in the %s table records no dispute; the manual "+
						"gives more than one answer for it", name, typeName(kind))
				}
			}
			if !found {
				t.Errorf("%s is not in the %s table", name, typeName(kind))
			}
		}
	}
}

// TestParityIsTheExclusiveOrOfEverythingButTheStartAndParityBytes checks the
// reading, and says that it is one.
//
// Section 6.5.5: the parity byte is the exclusive-or of every byte in the
// packet except the start byte and the parity byte itself, the field
// identifier 0x2F is included in that sum, and the two parity bytes count
// toward the length. The manufacturer prints no worked example with parity, so
// unlike everything above this is derived from prose and has never been seen
// on the wire — the operator's board has parity disabled.
func TestParityIsTheExclusiveOrOfEverythingButTheStartAndParityBytes(t *testing.T) {
	bare, err := Build(TypeControl, Val(0x30))
	if err != nil {
		t.Fatalf("building PKT_PRODID: %v", err)
	}
	withP := WithParity(bare)

	if got, want := len(withP), len(bare)+2; got != want {
		t.Fatalf("a parity field added %d bytes, want 2", got-len(bare))
	}
	if declared := int(withP[1])<<8 | int(withP[2]); declared != len(withP)-4 {
		t.Errorf("the length reads %d with parity on and the packet carries %d "+
			"field bytes; the parity bytes count toward the length", declared, len(withP)-4)
	}
	if withP[len(withP)-2] != ParityFieldID {
		t.Errorf("the parity field identifier is %#02x, want 0x2f", withP[len(withP)-2])
	}

	// The sum by hand: everything but the start byte and the parity byte.
	var want byte
	for _, b := range withP[1 : len(withP)-1] {
		want ^= b
	}
	if got := withP[len(withP)-1]; got != want {
		t.Errorf("the parity byte is %#02x, want %#02x", got, want)
	}
}

// TestParityEnabledIsReadFromTheRightBit locates PARITY_ENABLE in a
// configuration response.
//
// Table 74: CFG2, bit 4. This is the measurement that replaces an inference —
// parity is enabled by default, a chip with it enabled discards every packet
// without it, and the operator's board answered three packets that carried
// none. The conclusion was sound and PKT_GETCFG makes it checkable.
func TestParityEnabledIsReadFromTheRightBit(t *testing.T) {
	if ParityEnabledIn([3]byte{0xFF, 0xFF, 0x00}) {
		t.Error("parity read as enabled with CFG2 clear")
	}
	if !ParityEnabledIn([3]byte{0x00, 0x00, 1 << 4}) {
		t.Error("parity read as disabled with CFG2 bit 4 set")
	}
	// Every other bit of CFG2 belongs to something else, so none of them may
	// answer this question.
	for bit := 0; bit < 8; bit++ {
		if bit == 4 {
			continue
		}
		if ParityEnabledIn([3]byte{0xFF, 0xFF, byte(1) << bit}) {
			t.Errorf("CFG2 bit %d read as PARITY_ENABLE, which is bit 4", bit)
		}
	}
}

// TestASpeechFieldIsBoundedBySkewControlsRange keeps the sample count inside
// what the part accepts.
//
// Section 4.5.5 and Table 99: 156 to 164 samples, the nominal being 160, which
// is 20 ms at 8 kHz.
func TestASpeechFieldIsBoundedBySkewControlsRange(t *testing.T) {
	for _, n := range []int{0, 155, 165, 320} {
		s := make([]int16, n)
		if _, err := SpeechD(s); err == nil {
			t.Errorf("SPEECHD accepted %d samples; the manual gives 156 to 164", n)
		}
	}
	for _, n := range []int{156, 160, 164} {
		s := make([]int16, n)
		fv, err := SpeechD(s)
		if err != nil {
			t.Errorf("SPEECHD refused %d samples: %v", n, err)
			continue
		}
		if got, want := len(fv.Data), 1+n*2; got != want {
			t.Errorf("SPEECHD for %d samples is %d data bytes, want %d", n, got, want)
		}
	}
}

// TestChandCountsBitsAndNotBytes is the other variable-length field, where the
// count byte means something different.
//
// Table 107: the count is a number of bits from 40 to 192, and the data that
// follows is those bits packed eight to a byte. Channel Packet Example 1 sends
// 0x50 — eighty bits — followed by ten bytes.
func TestChandCountsBitsAndNotBytes(t *testing.T) {
	if _, err := Chand(80, make([]byte, 9)); err == nil {
		t.Error("CHAND accepted eighty bits with nine bytes behind them")
	}
	if _, err := Chand(39, make([]byte, 5)); err == nil {
		t.Error("CHAND accepted 39 bits; the manual gives 40 to 192")
	}
	if _, err := Chand(193, make([]byte, 25)); err == nil {
		t.Error("CHAND accepted 193 bits; the manual gives 40 to 192")
	}
	// A rate that is not a multiple of 400 bps: DMR's 72 bits is nine whole
	// bytes, but 49 bits of speech alone would be seven with the last one
	// padded.
	if _, err := Chand(49, make([]byte, 7)); err != nil {
		t.Errorf("CHAND refused 49 bits in seven padded bytes: %v", err)
	}
}
