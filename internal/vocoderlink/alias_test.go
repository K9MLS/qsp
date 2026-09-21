package vocoderlink

import (
	"strings"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// aliasChannel is outboundChannel with an alias configured.
func aliasChannel(t *testing.T, chip Chip, radio Radio, out *delivered, alias string) *Channel {
	t.Helper()
	ch, err := New(Options{Name: "dvstick", Chip: func() Chip { return chip }, Radio: radio,
		RadioID: gatewayID, Talkgroup: 2, Timeslot: hbp.Timeslot2, Deliver: out.add,
		Idle: time.Second, Alias: alias})
	if err != nil {
		t.Fatalf("building a channel with alias %q: %v", alias, err)
	}
	return ch
}

// embeddedPDUs reassembles every complete embedded-signalling PDU from a
// delivered burst stream, in order.
//
// **Decoded from the bursts, not read from the channel's own fields.** What
// matters is what a radio receives, and the fragments have been through EMB
// construction, the encode matrix and burst assembly by the time they are
// here. A test reading c.aliasMiddles would pass on a stream no radio could
// decode.
func embeddedPDUs(t *testing.T, frames []hbp.Data) [][]byte {
	t.Helper()
	var out [][]byte
	var fragments [dmrfec.EmbeddedLCBursts]uint32
	held := 0
	for _, f := range frames {
		if f.FrameType != hbp.FrameTypeVoice && f.FrameType != hbp.FrameTypeVoiceSync {
			continue
		}
		position := int(f.DataType)
		if position < 1 || position > dmrfec.EmbeddedLCBursts {
			continue
		}
		middle, ok := dmrfec.Middle(f.Payload[:])
		if !ok {
			t.Fatalf("burst at position %d has no middle", position)
		}
		emb, fragment := dmrfec.SplitMiddle(middle)
		if !dmrfec.ValidEMB(emb) {
			t.Errorf("burst at position %d has an EMB that fails its own parity", position)
			continue
		}
		want, _ := dmrfec.LCSSForPosition(position)
		if got := dmrfec.LCSSOf(emb); got != want {
			t.Errorf("burst at position %d carries LCSS %d, want %d; a radio would mis-frame the fragment",
				position, got, want)
		}
		fragments[position-1] = fragment
		held++
		if position == dmrfec.EmbeddedLCBursts && held >= dmrfec.EmbeddedLCBursts {
			pdu, ok := dmrfec.DecodeEmbeddedLC(fragments)
			if !ok {
				t.Error("a complete set of four fragments failed its checksum")
			} else {
				out = append(out, pdu)
			}
			held = 0
		}
	}
	return out
}

// talk runs a transmission of n superframes' worth of audio and returns what
// was delivered, driving handleUSRP as the other tests in this package do.
func talk(t *testing.T, alias string, superframes int) []hbp.Data {
	t.Helper()
	chip := &fakeChip{rate: ambe.RateIndexDMR}
	out := &delivered{}
	ch := aliasChannel(t, chip, &fakeRadio{}, out, alias)

	var tx *outbound
	tx = ch.handleUSRP(tx, nil, usrpKeyup, time.Now())
	// Three frames to a burst, six bursts to a superframe.
	for i := 0; i < superframes*dmrfec.SuperframeBursts*dmrfec.FramesPerBurst; i++ {
		tx = ch.handleUSRP(tx, nil, pcm(int16(i+1)), time.Now())
	}
	ch.handleUSRP(tx, nil, usrpRelease, time.Now())
	// Paced output is released by the run loop; drain it here.
	ch.flushPaced()
	return out.snapshot()
}

// TestTheConfiguredAliasReachesTheBursts checks what a radio would decode from
// a transmission built out of USRP audio.
//
// **The alias is configuration and only configuration (ADR-0064 §3).** A Zello
// display name is set by its user, so an alias taken from one would let a Zello
// user appear on somebody's repeater under that operator's callsign. The rows
// below check the text arrives, that the Link Control still arrives with it,
// and that it arrives again for a radio that joined late.
//
// To see rows fail: return the alias middles from middlesFor unconditionally,
// so the Link Control is never sent; drop the modulo so the alias plays once
// and never repeats; or hand encodeAlias a Zello display name.
func TestTheConfiguredAliasReachesTheBursts(t *testing.T) {
	tests := []struct {
		name        string
		alias       string
		superframes int
		wantAlias   string
		wantLCFirst bool
	}{
		{
			name:        "no alias configured sends the Link Control every superframe",
			alias:       "",
			superframes: 4,
			wantAlias:   "",
			wantLCFirst: true,
		},
		{
			name:        "a callsign arrives, with the Link Control first",
			alias:       "K9MLS",
			superframes: 4,
			wantAlias:   "K9MLS",
			wantLCFirst: true,
		},
		{
			name:        "a longer alias spans more than one PDU",
			alias:       "K9MLS Zello Gateway",
			superframes: 6,
			wantAlias:   "K9MLS Zello Gateway",
			wantLCFirst: true,
		},
		{
			name:        "the alias repeats, so a late joiner still gets it",
			alias:       "K9MLS",
			superframes: 8,
			wantAlias:   "K9MLS",
			wantLCFirst: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames := talk(t, tc.alias, tc.superframes)
			pdus := embeddedPDUs(t, frames)
			if len(pdus) == 0 {
				t.Fatal("no complete embedded PDU was sent at all")
			}

			aliasPDUs := aliasOnly(pdus)
			var lcs, everyAliasPDU [][]byte
			for _, pdu := range pdus {
				if isAliasPDU(pdu) {
					everyAliasPDU = append(everyAliasPDU, pdu)
					continue
				}
				lcs = append(lcs, pdu)
			}

			if tc.wantLCFirst {
				if len(lcs) == 0 {
					t.Error("no Link Control was sent; a radio has no source or talkgroup for the call")
				} else if flco := pdus[0][0] & 0x3F; flco == dmrfec.FLCOTalkerAliasHeader {
					t.Error("the first superframe carried the alias rather than the Link Control")
				}
			}

			if tc.wantAlias == "" {
				if len(everyAliasPDU) != 0 {
					t.Errorf("no alias is configured and %d alias PDU(s) were sent", len(everyAliasPDU))
				}
				return
			}

			got, ok := dmrfec.TalkerAliasFrom(aliasPDUs)
			if !ok {
				t.Fatalf("the %d alias PDU(s) sent do not decode as a talker alias", len(aliasPDUs))
			}
			if got != tc.wantAlias {
				t.Errorf("a radio would show %q, want %q", got, tc.wantAlias)
			}
			// Repeating matters as much as sending: a radio that joined after
			// the first cycle, or lost a burst to a fade, gets nothing from a
			// transmitter that sent the alias once.
			if tc.superframes >= 8 && len(lcs) < 2 {
				t.Errorf("the Link Control was sent %d time(s) in %d superframes; it has to come back for a late joiner",
					len(lcs), tc.superframes)
			}
		})
	}
}

// TestNothingAZelloUserControlsCanBecomeAnAlias is the ADR-0064 row: the only
// input to the alias is configuration.
func TestNothingAZelloUserControlsCanBecomeAnAlias(t *testing.T) {
	// A USRP frame has no field for a display name, and that is the point: if
	// audio.Frame ever gains one, this test is where the temptation to use it
	// should be caught.
	pdus := embeddedPDUs(t, talk(t, "K9MLS", 4))
	got, ok := dmrfec.TalkerAliasFrom(aliasOnly(pdus))
	if !ok {
		t.Fatal("the alias did not decode")
	}
	if got != "K9MLS" {
		t.Errorf("the alias on air is %q; only the configured value may appear", got)
	}
}

// isAliasPDU reports whether a PDU is a Talker Alias header or block.
func isAliasPDU(pdu []byte) bool {
	flco := pdu[0] & 0x3F
	return flco == dmrfec.FLCOTalkerAliasHeader ||
		(flco >= dmrfec.FLCOTalkerAliasBlock1 && flco <= dmrfec.FLCOTalkerAliasBlock1+2)
}

// aliasOnly keeps the Talker Alias PDUs of ONE cycle: from the first header up
// to the next one.
//
// **Not every alias PDU in the stream.** The alias repeats, so a long
// transmission carries the header more than once, and TalkerAliasFrom is right
// to refuse a sequence with two headers in it -- that is not an alias, it is
// two. Handing it the lot is what failed here first, and it looked like a
// decoding bug in the transmitter when the transmitter was correct.
func aliasOnly(pdus [][]byte) [][]byte {
	var out [][]byte
	for _, pdu := range pdus {
		if !isAliasPDU(pdu) {
			continue
		}
		if pdu[0]&0x3F == dmrfec.FLCOTalkerAliasHeader && len(out) > 0 {
			break // the cycle came round
		}
		out = append(out, pdu)
	}
	return out
}

// TestAnAliasThatCannotBeSentFailsAtStartup keeps a bad alias out of a call.
//
// **A transmission that fails mid-call has already gone on the air**: the
// header is out, a radio is listening, and the operator hears a truncated
// over. So the encoding happens when the channel is built, where the failure
// is a configuration error nobody has heard.
func TestAnAliasThatCannotBeSentFailsAtStartup(t *testing.T) {
	tests := []struct {
		name    string
		alias   string
		wantErr bool
	}{
		{name: "empty is no alias, not an error", alias: "", wantErr: false},
		{name: "whitespace only is no alias", alias: "   ", wantErr: false},
		{name: "a callsign", alias: "K9MLS", wantErr: false},
		{name: "the longest the length element can state", alias: strings.Repeat("A", dmrfec.TalkerAliasMaxLength), wantErr: false},
		{name: "one character too many", alias: strings.Repeat("A", dmrfec.TalkerAliasMaxLength+1), wantErr: true},
		// The multi-byte formats are 8 bits per character and dmrfec refuses
		// non-ASCII there, because the standard states the length element in
		// bytes in its prose and in characters in its table. A startup error
		// is readable; a mangled alias on somebody else's radio is not.
		{name: "non-ASCII is refused, with a reason", alias: "K9MLS café", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := encodeAlias(tc.alias)
			if tc.wantErr && err == nil {
				t.Errorf("encodeAlias(%q) succeeded; a call would fail on it with a radio already listening", tc.alias)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("encodeAlias(%q): %v", tc.alias, err)
			}
		})
	}
}
