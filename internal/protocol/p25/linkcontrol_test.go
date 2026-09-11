package p25

import (
	"errors"
	"sort"
	"testing"
)

// **The finding this test exists to protect**, and the reason a second capture
// was needed: fourteen transmissions across four talkgroups, with one talkgroup
// returned to after another had been used.
//
// The first capture could not answer it. One radio on one talkgroup makes a
// field that never changes indistinguishable from framing that never changes,
// and any claim about which byte meant what would have been a guess — in a
// routing field, where a guess sends a call to the wrong place.
//
// The operator confirmed these four are the talkgroups programmed into the
// radio, so this asserts a decode rather than merely a difference.
func TestTheTalkgroupsInTheCaptureAreTheOnesTransmitted(t *testing.T) {
	want := []uint16{925, 9999, 10888, 31672}

	seen := map[uint16]int{}
	for _, tx := range transmissions(t) {
		for _, p := range tx {
			f, err := Parse(p.Payload)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			id, high, err := f.Talkgroup()
			if err != nil {
				continue
			}
			// The byte above the identifier was zero throughout. A capture
			// showing otherwise is a fact about P25 rather than a bug here, and
			// it must be a visible surprise rather than a number sixty-five
			// thousand too large.
			if high != 0 {
				t.Errorf("the byte above the talkgroup is %#02x; it was zero in every "+
					"captured transmission, so this needs looking at rather than folding in",
					high)
			}
			seen[id]++
			break
		}
	}

	var got []uint16
	for id := range seen {
		got = append(got, id)
	}
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })

	if len(got) != len(want) {
		t.Fatalf("found talkgroups %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("found talkgroups %v, want %v", got, want)
		}
	}
}

// **One talkgroup is returned to after another was used.** Without that, a byte
// that merely drifted over time would look exactly like a talkgroup — which is
// the whole reason the second capture was taken in this order.
func TestATalkgroupIsReturnedToWithinTheCapture(t *testing.T) {
	var order []uint16
	for _, tx := range transmissions(t) {
		for _, p := range tx {
			f, err := Parse(p.Payload)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if id, _, err := f.Talkgroup(); err == nil {
				if len(order) == 0 || order[len(order)-1] != id {
					order = append(order, id)
				}
				break
			}
		}
	}

	if len(order) < 3 {
		t.Fatalf("the capture visits %d talkgroups in sequence: %v — too few to show a "+
			"return, so it cannot rule out a value that drifts with time", len(order), order)
	}
	var returned bool
	for i, id := range order {
		for j := 0; j < i-1; j++ {
			if order[j] == id {
				returned = true
			}
		}
	}
	if !returned {
		t.Errorf("no talkgroup is returned to in %v; a byte drifting with time would look "+
			"the same as a talkgroup", order)
	}
}

// **The source ID is the operator's own radio, exactly**, in every one of the
// fourteen. A 24-bit field holding 3132910 is not a coincidence.
func TestTheSourceIDIsTheTransmittingRadio(t *testing.T) {
	const k9mls = 3132910

	var found int
	for _, tx := range transmissions(t) {
		for _, p := range tx {
			f, err := Parse(p.Payload)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			id, err := f.SourceID()
			if err != nil {
				continue
			}
			found++
			if id != k9mls {
				t.Errorf("a transmission reports radio %d, want %d", id, k9mls)
			}
			break
		}
	}
	if found != 14 {
		t.Fatalf("found a source ID in %d transmissions, want 14", found)
	}
}

// A field is read only from the frame that carries it. A caller asking every
// frame for the talkgroup would otherwise get whatever those bytes happen to be
// in a voice frame — which is audio, and would route a call at random.
func TestAFieldIsOnlyReadFromTheFrameThatCarriesIt(t *testing.T) {
	for _, kind := range []Kind{KindVoice1, KindVoice3, KindVoice6, KindVoice9,
		KindVoice10, KindTerminator} {
		f := Frame{Kind: kind, Payload: make([]byte, frameLength[kind]-1)}

		if kind != talkgroupFrame {
			if _, _, err := f.Talkgroup(); !errors.Is(err, ErrNoLinkControl) {
				t.Errorf("0x%02x answered a talkgroup request: %v", byte(kind), err)
			}
		}
		if kind != sourceFrame {
			if _, err := f.SourceID(); !errors.Is(err, ErrNoLinkControl) {
				t.Errorf("0x%02x answered a source request: %v", byte(kind), err)
			}
		}
	}
}
