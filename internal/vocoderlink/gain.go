package vocoderlink

import "math"

// MaxGainDB bounds a configured gain either way. Twenty decibels is ten times
// the amplitude — past that a quiet source is a problem at its end, and a gain
// that large mostly amplifies its noise.
const MaxGainDB = 20

// limitKnee is where the limiter starts: below it a sample is only scaled.
const limitKnee = 0.8 * 32767

// Gain scales 16-bit PCM by a fixed number of decibels.
//
// **Linear below the knee, compressed smoothly above it, and never past full
// scale.** A plain multiply wraps a loud sample around to the other extreme —
// a crack on air — and clamping instead squares off every peak, which is heard
// as distortion on exactly the loud syllables a boost exists for. Normal speech
// raised by a modest gain stays under the knee and is untouched but for its
// level.
type Gain struct {
	factor float64
	unity  bool
}

// NewGain returns a gain of db decibels. Zero is exactly no change.
func NewGain(db float64) Gain {
	if db == 0 {
		return Gain{factor: 1, unity: true}
	}
	return Gain{factor: math.Pow(10, db/20)}
}

// Apply returns samples scaled and limited. The input is not modified: it may
// be a buffer the caller or the vocoder still owns.
func (g Gain) Apply(samples []int16) []int16 {
	if g.unity {
		return samples
	}
	out := make([]int16, len(samples))
	span := 32767 - limitKnee
	for i, s := range samples {
		v := float64(s) * g.factor
		a := math.Abs(v)
		if a > limitKnee {
			// **A rational curve, not tanh.** Both approach full scale
			// without reaching it in theory; tanh gets there exponentially,
			// so a sample driven well past full scale rounds to exactly 1.0
			// and a loud syllable becomes a flat run at full scale — hard
			// clipping by another name, which the first version of this did.
			// excess/(excess+span) approaches 1 as 1/excess, so neighbouring
			// loud samples stay distinct and the output stays below full
			// scale even at MaxGainDB.
			excess := a - limitKnee
			a = limitKnee + span*excess/(excess+span)
		}
		if v < 0 {
			a = -a
		}
		out[i] = int16(math.Round(a))
	}
	return out
}
