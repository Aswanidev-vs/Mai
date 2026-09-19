package main

import "math"

// Pitch shifting is applied to the decoded audio, so unlike `speed` it works on
// every TTS engine — including Pocket, whose ONNX graph ignores speed entirely.
//
// The personality layer clamps pitch to 0.5-2.0, which is far enough from unity
// to sound like a different person. These bounds keep the clone recognisable.
const (
	pitchShiftMin = 0.85
	pitchShiftMax = 1.20
	// pitchShiftDeadZone skips the DSP for negligible changes, so an unshifted
	// sentence is never touched.
	pitchShiftDeadZone = 0.02
)

// pitchShift re-voices samples by pitchRatio (1.0 = unchanged) without changing
// the duration, using WSOLA:
//
//  1. time-stretch by pitchRatio with overlap-add, searching each frame for the
//     alignment that best continues the previous frame (WSOLA's similarity
//     search) — this changes the length while preserving the pitch;
//  2. resample the stretched signal by pitchRatio, which restores the original
//     duration and moves the pitch by pitchRatio.
//
// Returns samples unchanged when the ratio is ~1, out of range, or the input is
// too short to stretch.
func pitchShift(samples []float32, pitchRatio float64) []float32 {
	if len(samples) == 0 || pitchRatio <= 0 {
		return samples
	}
	if math.Abs(pitchRatio-1) < pitchShiftDeadZone {
		return samples
	}
	ratio := math.Max(pitchShiftMin, math.Min(pitchShiftMax, pitchRatio))

	stretched := wsolaStretch(samples, ratio)
	if len(stretched) < 2 {
		return samples
	}
	// Reading the stretched signal `ratio` times faster undoes the stretch and
	// raises the pitch by `ratio`. Reuses the streaming resampler from aec.go.
	return (&resampler{step: ratio}).resample(stretched)
}

// wsolaStretch changes the length of in by `stretch` (>1 makes it longer) while
// preserving pitch, using WSOLA: overlap-add with a similarity search so the
// frame boundaries land on waveform-similar positions instead of cutting
// through periodic content, which is what makes naive OLA sound rough.
func wsolaStretch(in []float32, stretch float64) []float32 {
	const (
		win     = 1024 // ~43 ms at 24 kHz
		hopS    = 256  // synthesis hop; win/4 gives 4x overlap
		corrLen = 256  // similarity-search window
		search  = 96   // max alignment offset, ~4 ms at 24 kHz
	)
	if len(in) < win+corrLen {
		return in
	}

	hopA := float64(hopS) / stretch

	hann := make([]float32, win)
	for i := range hann {
		hann[i] = float32(0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(win-1)))
	}

	est := int(float64(len(in))*stretch) + win
	out := make([]float32, est)
	wsum := make([]float32, est)

	// prevSeg is the natural continuation of the previously copied segment; the
	// next frame searches for the input position that best continues it.
	prevSeg := in[:corrLen]
	readPos := 0.0
	outPos := 0
	last := len(in) - win

	for {
		nominal := int(readPos)
		if nominal > last {
			break
		}
		idx := nominal
		if nominal > 0 {
			bestScore := math.Inf(-1)
			for d := -search; d <= search; d++ {
				cand := nominal + d
				if cand < 0 || cand > last {
					continue
				}
				var num, den float64
				for i := 0; i < corrLen; i++ {
					a := float64(in[cand+i])
					num += a * float64(prevSeg[i])
					den += a * a
				}
				if den <= 0 {
					continue
				}
				// Normalise by candidate energy so a loud region does not win
				// on amplitude alone.
				if score := num / math.Sqrt(den); score > bestScore {
					bestScore = score
					idx = cand
				}
			}
		}

		for i := 0; i < win; i++ {
			out[outPos+i] += in[idx+i] * hann[i]
			wsum[outPos+i] += hann[i]
		}

		next := idx + hopS
		if next+corrLen > len(in) {
			break
		}
		prevSeg = in[next : next+corrLen]

		outPos += hopS
		readPos += hopA
	}

	// Divide out the overlap-add envelope. Hann at win/4 overlap is nearly flat,
	// but the stretched seams are not, and leaving the ripple in would modulate
	// the output amplitude.
	end := outPos + win
	if end > len(out) {
		end = len(out)
	}
	res := make([]float32, end)
	for i := 0; i < end; i++ {
		if wsum[i] > 1e-3 {
			res[i] = out[i] / wsum[i]
		} else {
			res[i] = out[i]
		}
	}
	return res
}
