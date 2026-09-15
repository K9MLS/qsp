package audio

import "math"

// Rate conversion between the radio side and the Zello side.
//
// # Why there is any
//
// DMR is 8 kHz. Zello's Channels API fixes its codec at **16 kHz mono with
// 60 ms frames** — `gD4BPA==` in their own example decodes to exactly that
// (ADR-0062). So every sample crossing the boundary is resampled, in both
// directions, and the factor is exactly two.
//
// # Why not the obvious thing
//
// Doubling a sample rate by repeating each sample, or halving it by dropping
// every other one, both work and both sound wrong. Dropping samples folds
// everything above 4 kHz back down into the voice band — a 6 kHz component
// arrives as 2 kHz, right in the middle of speech, and it cannot be removed
// afterwards because by then it is indistinguishable from signal. Repeating
// samples leaves images above 4 kHz that the Opus encoder then spends bits on.
//
// So both directions filter at the band edge. The cost is a fixed delay of
// half the filter length, which is 15 samples — under two milliseconds, and
// far less than the 60 ms the repacketiser already adds.
//
// # The filter
//
// A windowed-sinc low-pass, **computed rather than tabulated** so that the
// cutoff and length are visible as numbers instead of a block of constants
// nobody can check. Its response is measured in the tests: the passband has to
// survive and a 6 kHz tone has to not appear at 2 kHz.

// Voice band limits, in hertz, at the 16 kHz rate where the filter runs.
const (
	// filterCutoff is 3.4 kHz, the top of the telephony voice band and safely
	// under the 4 kHz Nyquist limit of the 8 kHz side.
	filterCutoff = 3400.0
	// filterRate is the rate the filter operates at: the higher of the two.
	filterRate = 16000.0
	// filterTaps is odd, so the filter has an exact centre and therefore
	// linear phase — a delay rather than a smear.
	filterTaps = 31
)

// lowPass holds the filter coefficients, built once.
var lowPass = buildLowPass()

// buildLowPass returns a windowed-sinc low-pass.
//
// A Hamming window rather than a rectangular one: truncating a sinc abruptly
// leaves ripple in the passband and poor rejection in the stopband, which is
// the difference between a filter and a shape that looks like one.
func buildLowPass() []float64 {
	taps := make([]float64, filterTaps)
	mid := (filterTaps - 1) / 2
	// Normalised cutoff in cycles per sample.
	fc := filterCutoff / filterRate

	var sum float64
	for i := range taps {
		n := i - mid
		var h float64
		if n == 0 {
			h = 2 * fc
		} else {
			x := 2 * math.Pi * fc * float64(n)
			h = math.Sin(x) / (math.Pi * float64(n))
		}
		// Hamming.
		w := 0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(filterTaps-1))
		taps[i] = h * w
		sum += taps[i]
	}
	// Unit gain at DC, so the filter changes the band and not the level.
	for i := range taps {
		taps[i] /= sum
	}
	return taps
}

// Upsampler converts 8 kHz to 16 kHz, keeping its filter state between calls.
//
// **State matters.** A filter restarted on every frame produces a transient at
// every frame boundary, fifty times a second, which is heard as a buzz at the
// frame rate rather than as distortion. One per stream, reset between
// transmissions.
type Upsampler struct{ history []float64 }

// NewUpsampler returns an upsampler ready for the first frame of a stream.
func NewUpsampler() *Upsampler {
	return &Upsampler{history: make([]float64, filterTaps)}
}

// Reset clears the filter between transmissions, so one caller's tail does not
// appear at the start of the next caller's audio.
func (u *Upsampler) Reset() {
	for i := range u.history {
		u.history[i] = 0
	}
}

// Up converts a run of 8 kHz samples to twice as many at 16 kHz.
//
// Zero-stuffing then filtering, which is the textbook interpolator: inserting
// a zero between samples creates the 16 kHz stream with an image above 4 kHz,
// and the low-pass removes it. The gain of two compensates for the energy the
// zeros removed.
func (u *Upsampler) Up(in []int16) []int16 {
	out := make([]int16, 0, len(in)*2)
	for _, s := range in {
		for _, v := range [2]float64{float64(s) * 2, 0} {
			copy(u.history, u.history[1:])
			u.history[len(u.history)-1] = v
			out = append(out, clamp(convolve(u.history, lowPass)))
		}
	}
	return out
}

// Downsampler converts 16 kHz to 8 kHz, keeping its filter state.
type Downsampler struct {
	history []float64
	phase   int
}

// NewDownsampler returns a downsampler ready for the first frame of a stream.
func NewDownsampler() *Downsampler {
	return &Downsampler{history: make([]float64, filterTaps)}
}

// Reset clears the filter and the phase between transmissions.
func (d *Downsampler) Reset() {
	for i := range d.history {
		d.history[i] = 0
	}
	d.phase = 0
}

// Down converts a run of 16 kHz samples to half as many at 8 kHz.
//
// **Filter first, then drop.** Dropping first is the mistake: everything above
// 4 kHz folds into the voice band and nothing downstream can separate it from
// speech again.
//
// The phase is kept across calls so that an odd-length input does not shift
// which samples are taken — a shift of one sample per frame is a slow drift in
// the output rate, which shows up as a click when a buffer catches up.
func (d *Downsampler) Down(in []int16) []int16 {
	out := make([]int16, 0, (len(in)+1)/2)
	for _, s := range in {
		copy(d.history, d.history[1:])
		d.history[len(d.history)-1] = float64(s)
		if d.phase == 0 {
			out = append(out, clamp(convolve(d.history, lowPass)))
		}
		d.phase ^= 1
	}
	return out
}

// convolve applies the filter to a history buffer, oldest sample first.
func convolve(history, taps []float64) float64 {
	var acc float64
	for i, t := range taps {
		acc += history[i] * t
	}
	return acc
}

// clamp converts to a sample, saturating rather than wrapping.
//
// **Wrapping is the loud failure.** A value one above full scale becomes full
// scale negative, which is a click at maximum amplitude in somebody's ear; the
// interpolator's gain of two makes an overshoot on loud audio entirely
// possible.
func clamp(v float64) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(math.Round(v))
}
