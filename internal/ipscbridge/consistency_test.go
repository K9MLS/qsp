package ipscbridge_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"testing"

	"github.com/k9mls/qsp/internal/dmrfec"
	"github.com/k9mls/qsp/internal/ipscbridge"
	"github.com/k9mls/qsp/internal/protocol/hbp"
	"github.com/k9mls/qsp/internal/protocol/ipsc"
)

// homebrewCapture reads the Homebrew data frames out of a capture, whatever its
// link type. The captures in testdata/hbp were taken with `tcpdump -i any`,
// which writes Linux cooked headers rather than Ethernet.
func homebrewCapture(tb testing.TB, path string) []hbp.Data {
	tb.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("reading %s: %v", path, err)
	}
	ipAt := map[uint32]int{1: 14, 113: 16, 276: 20}[binary.LittleEndian.Uint32(raw[20:24])]
	if ipAt == 0 {
		tb.Fatalf("%s: link type not understood", path)
	}
	var out []hbp.Data
	for off := 24; off+16 <= len(raw); {
		incl := int(binary.LittleEndian.Uint32(raw[off+8 : off+12]))
		off += 16
		if off+incl > len(raw) {
			break
		}
		rec := raw[off : off+incl]
		off += incl
		if len(rec) < ipAt+28 || rec[ipAt+9] != 17 {
			continue
		}
		ip := rec[ipAt:]
		udp := ip[int(ip[0]&0x0f)*4:]
		m, err := hbp.Parse(udp[8:])
		if err != nil {
			continue
		}
		if d, ok := m.(hbp.Data); ok {
			out = append(out, d)
		}
	}
	if len(out) == 0 {
		tb.Fatalf("%s: no Homebrew data frames", path)
	}
	return out
}

// streams splits a capture into its separate transmissions.
//
// **A capture through QSP holds each call more than once**: in from the
// hotspot and out to every other peer, interleaved burst by burst. The first
// version of this test fed them all to one encoder and stitched fragments from
// different copies into one superframe, which decodes as nothing -- and failed
// every row, including ordinary voice, for a reason that was the test's own.
func streams(frames []hbp.Data) [][]hbp.Data {
	// **The target is part of the key.** hbp-voice-live.pcap has both legs
	// of each call under one repeater and one stream ID, one leg on TG 11 and
	// the other rewritten to TG 9 in flight; keyed on the stream alone they
	// merged, and the second version of this test failed ordinary voice for
	// that reason.
	type key struct {
		peer   uint32
		stream hbp.StreamID
		target uint32
	}
	var order []key
	byKey := map[key][]hbp.Data{}
	for _, f := range frames {
		k := key{uint32(f.RepeaterID), f.StreamID, f.TargetID}
		if _, seen := byKey[k]; !seen {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], f)
	}
	out := make([][]hbp.Data, 0, len(order))
	for _, k := range order {
		out = append(out, byKey[k])
	}
	return out
}

// superframes encodes each transmission separately and returns, for every
// superframe sent, the Link Control its four fragments reassemble to and the
// copy attached to the fifth burst.
func superframes(t *testing.T, frames []hbp.Data) (fromFragments, attached [][]byte) {
	t.Helper()
	for _, one := range streams(frames) {
		a, b := encodeOne(one)
		fromFragments = append(fromFragments, a...)
		attached = append(attached, b...)
	}
	return fromFragments, attached
}

func encodeOne(frames []hbp.Data) (fromFragments, attached [][]byte) {
	e := ipscbridge.NewEncoder(3132911, ipscbridge.Config{ColourCode: 11})
	var frags []uint32
	for _, f := range frames {
		msgs, _ := e.Encode(f)
		for _, m := range msgs {
			class, _, trailer, ok := m.Payload()
			if !ok {
				continue
			}
			switch class {
			case ipsc.PayloadSync:
				frags = frags[:0]
			case ipsc.PayloadFragment, ipsc.PayloadFragmentWithLC:
				frags = append(frags, binary.BigEndian.Uint32(trailer[:4]))
				if class != ipsc.PayloadFragmentWithLC || len(frags) < dmrfec.EmbeddedLCBursts {
					continue
				}
				var four [dmrfec.EmbeddedLCBursts]uint32
				copy(four[:], frags[len(frags)-dmrfec.EmbeddedLCBursts:])
				lc, _ := dmrfec.DecodeEmbeddedLC(four)
				fromFragments = append(fromFragments, lc)
				attached = append(attached, append([]byte(nil), trailer[4:4+dmrfec.LinkControlBytes]...))
			}
		}
	}
	return fromFragments, attached
}

// TestAMotorolaRepeaterIsNeverToldTwoThings is the rule every captured
// Motorola superframe follows: the four embedded fragments and the Link
// Control attached to the fifth burst are the same Link Control.
//
// **Measured, not assumed.** In testdata/ipsc, 288 of 290 superframes sent by
// real XPR repeaters and a real master reassemble to exactly the copy they
// attach; the other two are one superframe seen twice that decodes as no Link
// Control at all. Only FLCO 0 and 3 appear -- group and private voice -- and no
// Talker Alias, ever, including from a radio that was sending one.
//
// **QSP broke that rule.** The encoder copied each burst's fragment through
// and attached a Link Control rebuilt from the call, so a superframe whose
// fragments carried a Talker Alias went out saying two different things. Two
// sources did it: a radio's own alias arriving through a hotspot, which
// hbp-talker-alias.pcap shows MMDVMHost forwarding, and from 0.1.264 the
// alias QSP puts on Zello calls.
//
// To see it fail: in Encoder.voice, use the incoming burst's fragment again
// instead of the one derived from the call's Link Control.
func TestAMotorolaRepeaterIsNeverToldTwoThings(t *testing.T) {
	tests := []struct {
		name    string
		capture string
		// wantAlias is whether the input carried a Talker Alias, so the test
		// is known to have exercised the case that went wrong.
		wantAlias bool
	}{
		{"a radio's own Talker Alias, through a Pi-Star", "../../testdata/hbp/hbp-talker-alias.pcap", true},
		{"a login session with an alias in it", "../../testdata/hbp/hbp-login-session.pcap", true},
		{"ordinary voice with no alias", "../../testdata/hbp/hbp-voice-live.pcap", false},
		{"voice on a second colour code", "../../testdata/hbp/hbp-emb-colourcode-4.pcap", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames := homebrewCapture(t, tc.capture)
			if tc.wantAlias && !carriesAlias(frames) {
				t.Fatalf("%s no longer carries a Talker Alias, so this row tests nothing", tc.capture)
			}

			fromFragments, attached := superframes(t, frames)
			if len(fromFragments) == 0 {
				t.Fatal("no superframe was encoded; the test would pass by finding nothing")
			}
			contradictions := 0
			for i := range fromFragments {
				if !bytes.Equal(fromFragments[i], attached[i]) {
					contradictions++
				}
			}
			if contradictions > 0 {
				t.Errorf("%d of %d superframes told the repeater two different things: the "+
					"fragments reassemble to one Link Control and the attached copy is another",
					contradictions, len(fromFragments))
			}
		})
	}
}

// carriesAlias reports whether a stream's embedded signalling holds a Talker
// Alias PDU anywhere, read by burst position as MMDVMHost numbers them.
func carriesAlias(frames []hbp.Data) bool {
	frags := map[hbp.StreamID]*[dmrfec.EmbeddedLCBursts]uint32{}
	for _, f := range frames {
		p := int(f.DataType)
		if f.FrameType != hbp.FrameTypeVoice || p < 1 || p > dmrfec.EmbeddedLCBursts {
			continue
		}
		m, ok := dmrfec.Middle(f.Payload[:])
		if !ok {
			continue
		}
		_, frag := dmrfec.SplitMiddle(m)
		set := frags[f.StreamID]
		if set == nil {
			set = &[dmrfec.EmbeddedLCBursts]uint32{}
			frags[f.StreamID] = set
		}
		set[p-1] = frag
		if p == dmrfec.EmbeddedLCBursts {
			if lc, ok := dmrfec.DecodeEmbeddedLC(*set); ok {
				if flco := lc[0] & 0x3F; flco >= dmrfec.FLCOTalkerAliasHeader && flco <= dmrfec.FLCOTalkerAliasHeader+3 {
					return true
				}
			}
		}
	}
	return false
}

// TestEachCallGetsItsOwnLinkControl runs several calls through ONE encoder,
// one after another, as a timeslot does in production.
//
// **The row above gives each transmission a fresh encoder, and so could not
// see this.** A deliberate break proved it: caching the derived Link Control
// per encoder rather than per call left every row passing, while in production
// every call after the first on a timeslot would have gone out stamped with the
// first call's source and talkgroup -- a worse fault than the one being fixed.
// hbp-voice-live.pcap's two legs differ only in their talkgroup, 11 and 9, which
// is exactly the case a stale Link Control gets wrong.
func TestEachCallGetsItsOwnLinkControl(t *testing.T) {
	frames := homebrewCapture(t, "../../testdata/hbp/hbp-voice-live.pcap")

	var sequential []hbp.Data
	targets := map[uint32]bool{}
	for _, one := range streams(frames) {
		sequential = append(sequential, one...)
		targets[one[0].TargetID] = true
	}
	if len(targets) < 2 {
		t.Fatalf("the capture holds calls to %d talkgroup(s); this needs at least two", len(targets))
	}

	fromFragments, attached := encodeOne(sequential)
	if len(fromFragments) == 0 {
		t.Fatal("no superframe was encoded")
	}
	wrong := 0
	for i := range fromFragments {
		if !bytes.Equal(fromFragments[i], attached[i]) {
			wrong++
		}
	}
	if wrong > 0 {
		t.Errorf("%d of %d superframes across %d calls on one encoder carried another call's Link Control",
			wrong, len(fromFragments), len(targets))
	}
}
