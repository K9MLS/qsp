package vocoderlink

import (
	"math"
	"testing"
	"time"

	"github.com/k9mls/qsp/internal/ambe"
	"github.com/k9mls/qsp/internal/audio"
	"github.com/k9mls/qsp/internal/protocol/hbp"
)

// speechLike is a quiet, peaky test signal: mostly low-level tone with short
// loud bursts, about the crest factor of speech. It stands in for the capture
// of 2026-09-16, which measured DMR audio to Zello at -35.5 dBFS and is not in
// the repository because it holds other people's voices.
func speechLike(n int, rmsDBFS float64) []int16 {
	raw := make([]float64, n)
	for i := range raw {
		t := float64(i) / 8000
		v := math.Sin(2*math.Pi*220*t) + 0.5*math.Sin(2*math.Pi*660*t)
		if (i/800)%5 == 0 { // a loud syllable every half second
			v *= 6
		}
		raw[i] = v
	}
	var sum float64
	for _, v := range raw {
		sum += v * v
	}
	scale := math.Pow(10, rmsDBFS/20) * 32768 / math.Sqrt(sum/float64(n))
	out := make([]int16, n)
	for i, v := range raw {
		out[i] = int16(math.Round(v * scale))
	}
	return out
}

func rmsDBFS(s []int16) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return 20 * math.Log10(math.Sqrt(sum/float64(len(s)))/32768)
}

// TestGainRaisesTheLevelAndNeverWraps.
//
// To see rows fail, break Gain deliberately:
//   - replace the limiter with a plain int16 conversion: "a +20 dB boost of a
//     loud signal" wraps, and a positive peak reads negative
//   - clamp at ±32767 instead of limiting: "the loud syllables keep their
//     shape" fails, the peaks squared off
//   - drop the unity fast path's exactness: "0 dB" changes samples
func TestGainRaisesTheLevelAndNeverWraps(t *testing.T) {
	quiet := speechLike(16000, -35.5)

	t.Run("0 dB is exactly no change", func(t *testing.T) {
		out := NewGain(0).Apply(quiet)
		for i := range quiet {
			if out[i] != quiet[i] {
				t.Fatalf("sample %d changed from %d to %d", i, quiet[i], out[i])
			}
		}
	})

	t.Run("+13 dB brings the measured DMR level to the Zello level", func(t *testing.T) {
		got := rmsDBFS(NewGain(13).Apply(quiet))
		if math.Abs(got-(-22.5)) > 1.0 {
			t.Errorf("level %.1f dBFS after +13 dB, want about -22.5 (Zello audio measured -22)", got)
		}
	})

	t.Run("a quiet sample is scaled exactly", func(t *testing.T) {
		out := NewGain(6).Apply([]int16{1000, -1000})
		want := int16(math.Round(1000 * math.Pow(10, 6.0/20)))
		if out[0] != want || out[1] != -want {
			t.Errorf("got %v, want ±%d", out, want)
		}
	})

	t.Run("a +20 dB boost of a loud signal never wraps or reaches full scale", func(t *testing.T) {
		loud := speechLike(16000, -12)
		out := NewGain(MaxGainDB).Apply(loud)
		for i := range loud {
			if (loud[i] > 0 && out[i] < 0) || (loud[i] < 0 && out[i] > 0) {
				t.Fatalf("sample %d wrapped: %d became %d", i, loud[i], out[i])
			}
			if out[i] == math.MaxInt16 || out[i] == math.MinInt16 || out[i] == -math.MaxInt16 {
				t.Fatalf("sample %d reached full scale (%d); the limiter must approach it, not hit it", i, out[i])
			}
		}
	})

	t.Run("the loud syllables keep their shape", func(t *testing.T) {
		// Hard clipping flattens a run of neighbouring samples to one value;
		// a soft limiter keeps them distinct.
		out := NewGain(MaxGainDB).Apply(speechLike(16000, -12))
		run, longest := 1, 1
		for i := 1; i < len(out); i++ {
			if out[i] == out[i-1] && (out[i] > 30000 || out[i] < -30000) {
				run++
				longest = max(longest, run)
			} else {
				run = 1
			}
		}
		if longest > 3 {
			t.Errorf("%d consecutive identical samples near full scale: the peaks were squared off", longest)
		}
	})

	t.Run("the input buffer is not modified", func(t *testing.T) {
		in := []int16{1000, 2000}
		_ = NewGain(10).Apply(in)
		if in[0] != 1000 || in[1] != 2000 {
			t.Error("Apply wrote into its input, which may be the vocoder's buffer")
		}
	})
}

// TestAChannelAppliesItsGainInEachDirection: a correct Gain nobody calls
// passes every test above.
//
// To see a row fail: send reply.Samples in handle, or encode samples in
// encodeInto, without Apply.
func TestAChannelAppliesItsGainInEachDirection(t *testing.T) {
	want := func(v int16, db float64) int16 { return int16(math.Round(float64(v) * math.Pow(10, db/20))) }

	t.Run("DMR toward USRP", func(t *testing.T) {
		chip := &fakeChip{rate: ambe.RateIndexDMR}
		radio := &fakeRadio{}
		ch, err := New(Options{Name: "zello", Chip: func() Chip { return chip }, Radio: radio, GainToUSRPDB: 6})
		if err != nil {
			t.Fatal(err)
		}
		cur := ch.handle(nil, header(1), time.Now())
		ch.handle(cur, voiceBurst(t, 1), time.Now())
		// The fake chip answers the n-th decode with a first sample of n.
		for i, f := range radio.sent[1:] {
			if got, w := f.Samples[0], want(int16(i+1), 6); got != w {
				t.Errorf("frame %d reached USRP with first sample %d, want %d after +6 dB", i+1, got, w)
			}
		}
	})

	t.Run("USRP toward DMR", func(t *testing.T) {
		chip := &fakeChip{rate: ambe.RateIndexDMR}
		out := &delivered{}
		ch, err := New(Options{Name: "zello", Chip: func() Chip { return chip }, Radio: &fakeRadio{},
			RadioID: gatewayID, Talkgroup: 2, Timeslot: hbp.Timeslot2, Deliver: out.add, GainToDMRDB: -6})
		if err != nil {
			t.Fatal(err)
		}
		var tx *outbound
		for _, f := range []audio.Frame{usrpKeyup, pcm(1000), pcm(2000), pcm(3000)} {
			tx = ch.handleUSRP(tx, nil, f, time.Now())
		}
		for i, v := range []int16{1000, 2000, 3000} {
			if got, w := chip.encoded[i], want(v, -6); got != w {
				t.Errorf("frame %d reached the vocoder with first sample %d, want %d after -6 dB", i+1, got, w)
			}
		}
	})
}
