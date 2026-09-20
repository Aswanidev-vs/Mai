package main

import "math"

// Measured on this machine: Pocket with the cloned reference emits
// rms 0.0544 (-25.3 dBFS) with a peak of 0.486, against a reference WAV at
// about -18 dBFS. Normal program material is roughly -16 dBFS RMS, so the engine
// is ~9 dB quiet on its own, and the chain's makeup gain (1.11) only cancels the
// "calm" preset's 0.9 — the net gain was unity, so the model's deficit reached
// the speaker untouched. That is exactly what "the volume bar is at 100% and she
// still sounds low" means.
//
// Peak normalisation alone cannot fix it: the measured crest factor is ~19 dB,
// so scaling the peak to full scale would leave the speech still quiet and would
// clip on the next sentence that happened to have a sharper transient.
const (
	voiceTargetRMSDefault = -16.0 // dBFS
	voiceMinGain          = 0.5   // never attenuate by more than 6 dB
	voiceMaxGain          = 4.0   // +12 dB, enough for the measured 9.3 dB deficit
	// An utterance that needs more gain than this is not speech. Measured Pocket
	// failures (a lone "..." fragment, or an early EOS that leaves a sub-second
	// lead-in blip) come back 30 dB or more below the target with the loudest
	// sample in the whole buffer around 0.01. Normalising that spends the full
	// +12 dB cap on the model's noise floor, which dresses a failed generation up
	// as a merely quiet utterance — in the audio and in the [TTS-LEVEL] log
	// alike. Real sentences from the same engine measure 1.3-3.7x, so 16x
	// (+24 dB) cannot catch speech.
	voiceUnreachableGain = voiceMaxGain * 4
	limiterKnee          = 0.70
	limiterCeiling       = 0.95
	// Frames quieter than this fraction of the loudest frame are silence and are
	// excluded from the loudness measurement. Pocket pads utterances with
	// silence, and averaging over that padding would over-boost the speech.
	loudnessActiveFloor = 0.1
)

// dbfsToLinear converts a dBFS level to a linear amplitude.
func dbfsToLinear(db float64) float64 {
	return math.Pow(10, db/20)
}

// normalizeLoudness scales the whole utterance so its active speech sits at
// targetRMS, bounding the peak on the way out. It runs on a complete sentence,
// so it can look ahead.
func normalizeLoudness(samples []float32, sampleRate int, targetRMS float64) []float32 {
	if len(samples) == 0 || sampleRate <= 0 || targetRMS <= 0 {
		return samples
	}
	gain, ok := loudnessGain(samples, sampleRate, targetRMS)
	if !ok {
		return samples
	}
	out := make([]float32, len(samples))
	for i, s := range samples {
		out[i] = float32(softLimit(float64(s) * gain))
	}
	return out
}

// loudnessGain reports the scale factor that brings the utterance's active
// speech to targetRMS. It returns false for an utterance that must not be
// normalised: digital silence, or a buffer sitting so far below the target that
// amplifying it would only raise the engine's noise floor (voiceUnreachableGain)
// — which is what a failed generation looks like coming out of the engine.
func loudnessGain(samples []float32, sampleRate int, targetRMS float64) (float64, bool) {
	frame := sampleRate / 100 // 10 ms
	if frame < 1 {
		frame = 1
	}
	frames := make([]float64, 0, len(samples)/frame+1)
	maxFrame := 0.0
	for start := 0; start+frame <= len(samples); start += frame {
		var acc float64
		for _, s := range samples[start : start+frame] {
			acc += float64(s) * float64(s)
		}
		rms := math.Sqrt(acc / float64(frame))
		frames = append(frames, rms)
		if rms > maxFrame {
			maxFrame = rms
		}
	}
	if maxFrame < 1e-6 {
		return 0, false
	}

	floor := loudnessActiveFloor * maxFrame
	var acc float64
	active := 0
	for _, rms := range frames {
		if rms >= floor {
			acc += rms * rms
			active++
		}
	}
	if active == 0 {
		return 0, false
	}
	activeRMS := math.Sqrt(acc / float64(active))
	if activeRMS < 1e-6 {
		return 0, false
	}
	needed := targetRMS / activeRMS
	if needed > voiceUnreachableGain {
		return 0, false
	}
	return math.Max(voiceMinGain, math.Min(voiceMaxGain, needed)), true
}

// softLimit bounds a sample to limiterCeiling through a C1-continuous knee, so
// peaks are compressed rather than clipped. The previous per-sample hard clamp
// was audible harshness on loud consonants, and any sample that escaped above
// 1.0 wrapped sign in the int16 conversion — a full-scale click, not a slightly
// loud sample.
func softLimit(v float64) float64 {
	a := math.Abs(v)
	if a <= limiterKnee {
		return v
	}
	width := limiterCeiling - limiterKnee
	y := limiterKnee + width*math.Tanh((a-limiterKnee)/width)
	if v < 0 {
		return -y
	}
	return y
}

// limitSamples applies softLimit to every sample. It always returns a bound
// buffer: this is the one stage that clips, and it is what keeps a sample above
// full scale from wrapping in the PCM16 conversion.
func limitSamples(samples []float32) []float32 {
	out := make([]float32, len(samples))
	for i, s := range samples {
		out[i] = float32(softLimit(float64(s)))
	}
	return out
}

// peakOf returns the largest absolute sample value.
func peakOf(samples []float32) float64 {
	peak := 0.0
	for _, s := range samples {
		if a := math.Abs(float64(s)); a > peak {
			peak = a
		}
	}
	return peak
}

// levelDBFS converts a linear level to dBFS for logging. A zero level is
// reported as -inf, which is what a silent utterance actually is.
func levelDBFS(level float64) float64 {
	if level <= 0 {
		return math.Inf(-1)
	}
	return 20 * math.Log10(level)
}
