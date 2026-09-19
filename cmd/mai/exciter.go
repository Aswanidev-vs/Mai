package main

import "math"

// The harmonic exciter addresses the one thing the Pocket pipeline cannot fix by
// configuration: Pocket is a 24 kHz model, so everything above ~12 kHz is simply
// absent. Measured on this machine, Pocket's output sits at a spectral centroid
// of ~474 Hz against the 712 Hz reference, which is heard as "dulled" or "toned
// down".
//
// No filter, gain or resampler can put back frequencies the model never
// generated. What an exciter does instead is generate *new* harmonics from the
// upper band the model did produce (via soft saturation) and fold them into the
// air band, which restores the perceived brightness. It is a psychoacoustic
// enhancement, not a reconstruction — it will never be the reference's real top
// end, but it removes the muffled quality.

// exciterConfig parameterises the exciter stage. Amount <= 0 disables it.
type exciterConfig struct {
	sampleRate int
	amount     float64 // wet mix; 0 disables the stage entirely
	presence   float64 // source band lower edge (Hz) taken from the voice
	bandLo     float64 // air band lower edge (Hz)
	bandHi     float64 // air band upper edge (Hz)
}

// defaultExciterConfig derives the band edges from the engine's sample rate so
// the stage behaves sensibly for both 24 kHz Pocket and 44.1 kHz Supertonic.
//
// The source band starts at 2.8 kHz: this captures the full consonant and
// sibilant clarity band (fricatives, dental consonants) that listeners read as
// articulation and air. Doubling that lands the synthesised harmonics at 5.6 kHz
// and up, inside the air band the 24 kHz model leaves empty.
func defaultExciterConfig(sampleRate int, amount float64) exciterConfig {
	clampHz := func(hz float64) float64 {
		if limit := 0.45 * float64(sampleRate); hz > limit {
			return limit
		}
		return hz
	}
	return exciterConfig{
		sampleRate: sampleRate,
		amount:     amount,
		presence:   clampHz(2800),
		bandLo:     clampHz(6000),
		bandHi:     clampHz(11000),
	}
}

// biquad is a direct-form-1 second-order IIR section (RBJ cookbook coefficients).
type biquad struct {
	b0, b1, b2, a1, a2 float64
	x1, x2, y1, y2     float64
}

func (f *biquad) process(x float64) float64 {
	y := f.b0*x + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
	f.x2, f.x1 = f.x1, x
	f.y2, f.y1 = f.y1, y
	return y
}

func biquadLowpass(sampleRate int, freq, q float64) *biquad {
	w := 2 * math.Pi * freq / float64(sampleRate)
	cw, alpha := math.Cos(w), math.Sin(w)/(2*q)
	a0 := 1 + alpha
	return &biquad{
		b0: (1 - cw) / 2 / a0,
		b1: (1 - cw) / a0,
		b2: (1 - cw) / 2 / a0,
		a1: -2 * cw / a0,
		a2: (1 - alpha) / a0,
	}
}

func biquadHighpass(sampleRate int, freq, q float64) *biquad {
	w := 2 * math.Pi * freq / float64(sampleRate)
	cw, alpha := math.Cos(w), math.Sin(w)/(2*q)
	a0 := 1 + alpha
	return &biquad{
		b0: (1 + cw) / 2 / a0,
		b1: -(1 + cw) / a0,
		b2: (1 + cw) / 2 / a0,
		a1: -2 * cw / a0,
		a2: (1 - alpha) / a0,
	}
}

func biquadPeaking(sampleRate int, freq, gainDB, q float64) *biquad {
	A := math.Pow(10, gainDB/40)
	w0 := 2 * math.Pi * freq / float64(sampleRate)
	cw := math.Cos(w0)
	sw := math.Sin(w0)
	alpha := sw / (2 * q)
	a0 := 1 + alpha/A
	return &biquad{
		b0: (1 + alpha*A) / a0,
		b1: (-2 * cw) / a0,
		b2: (1 - alpha*A) / a0,
		a1: (-2 * cw) / a0,
		a2: (1 - alpha/A) / a0,
	}
}

// applyExciter mixes synthesised harmonics of the voice's presence band back in
// around the air band.
//
// x*|x| is deliberately used rather than a saturating curve: second-order
// distortion generates the 2f component of every partial, which is what actually
// lands new energy above the band, whereas tanh is close to linear at speech
// level and produces almost none. The result is high-passed back to the air band
// so it cannot thicken the vowels.
//
// Because x*|x| scales with the square of the input, a quiet sentence would
// produce almost no harmonics. The presence band is therefore normalised to a
// fixed peak first (it runs on a complete sentence, so it can look ahead), which
// makes the stage's effect independent of how loud the input was.
func applyExciter(samples []float32, sampleRate int, cfg exciterConfig) []float32 {
	if cfg.amount <= 0 || len(samples) == 0 || sampleRate <= 0 {
		return samples
	}
	if cfg.presence >= cfg.bandLo || cfg.bandLo >= cfg.bandHi {
		return samples // degenerate band for this rate; leave the audio alone
	}

	presenceHP := biquadHighpass(sampleRate, cfg.presence, 0.707)
	airHP := biquadHighpass(sampleRate, cfg.bandLo, 0.707)
	airLP := biquadLowpass(sampleRate, cfg.bandHi, 0.707)

	// Pass 1: measure the presence band so the harmonic generator gets a
	// predictable level.
	presence := make([]float32, len(samples))
	peak := 0.0
	for i, s := range samples {
		x := float64(s)
		if math.IsNaN(x) || math.IsInf(x, 0) {
			presence[i] = 0
			continue
		}
		p := presenceHP.process(x)
		presence[i] = float32(p)
		if a := math.Abs(p); a > peak {
			peak = a
		}
	}
	if peak < 1e-9 {
		return samples // nothing in the presence band; nothing to excite
	}

	// A band with no real energy must not be excited: scaling a near-empty band
	// up produces only filter ringing, which is how a "subtle brightness" knob
	// ends up spitting out a full-scale impulse. Real speech carries far more
	// than this in the sibilant band; a low-passed voice carries almost none.
	rawRMS := rmsLevel(presence)
	dryRMS := rmsLevel(samples)
	if rawRMS < 0.02*dryRMS {
		return samples
	}

	const targetPeak = 0.5
	scale := targetPeak / peak

	air := make([]float32, len(samples))
	for i := range samples {
		p := float64(presence[i]) * scale
		h := p * math.Abs(p) // second-order: every partial gains a 2f component
		air[i] = float32(airLP.process(airHP.process(h)))
	}

	// Calibrate so `amount` has a predictable meaning: the air band is scaled to
	// `amount` times the dry signal's RMS. 0.25 is therefore "air at -12 dB
	// relative to the voice", which is audible, and every step of the knob is
	// proportionate. Without this the stage's output would depend on the square
	// of the input level and the knob would mean nothing measurable.
	airRMS := rmsLevel(air)
	if airRMS < 1e-9 || dryRMS < 1e-9 {
		return samples
	}
	gain := cfg.amount * dryRMS / airRMS

	// RMS calibration alone does not bound the peak: an impulsive air band can
	// have a peak far above its RMS and would clip. Cap the added amplitude too.
	peakAir := 0.0
	for _, v := range air {
		if a := math.Abs(float64(v)); a > peakAir {
			peakAir = a
		}
	}
	const maxPeakAdd = 0.30
	if maxGain := maxPeakAdd / peakAir; gain > maxGain {
		gain = maxGain
	}

	out := make([]float32, len(samples))
	for i, s := range samples {
		out[i] = float32(float64(s) + gain*float64(air[i]))
	}
	return out
}

func rmsLevel(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var acc float64
	for _, v := range samples {
		acc += float64(v) * float64(v)
	}
	return math.Sqrt(acc / float64(len(samples)))
}

// colourConfig is the post-synthesis voice chain's configuration.
type colourConfig struct {
	sampleRate int
	// volume is the emotion/style loudness offset. It is applied AFTER loudness
	// normalisation so a preset changes loudness relative to a fixed reference
	// instead of relative to the engine's own (very low) output level.
	volume float32
	pitch  float32
	// outputGain is the master trim (config tts.output_gain).
	outputGain float32
	// targetRMS is the loudness normalisation target; <= 0 disables it.
	targetRMS float64
	exciter   exciterConfig
}

// colourVoice applies the post-synthesis chain in the order it must run. Pitch
// runs before the exciter so the exciter's synthesised harmonics are not
// resampled, which would alias them. Loudness normalisation runs after the
// exciter but before the emotion offset, and the limiter runs last.
func colourVoice(samples []float32, cfg colourConfig) []float32 {
	if len(samples) == 0 {
		return samples
	}
	out := pitchShift(samples, float64(cfg.pitch))
	out = applyExciter(out, cfg.exciter.sampleRate, cfg.exciter)

	// Presence & clarity enhancement (3.8 kHz +2.2 dB bell EQ):
	// active when exciter is enabled to elevate consonant crispness and articulation.
	if cfg.exciter.amount > 0 && cfg.sampleRate >= 16000 {
		eq := biquadPeaking(cfg.sampleRate, 3800, 2.2, 1.2)
		clarity := make([]float32, len(out))
		for i, s := range out {
			clarity[i] = float32(eq.process(float64(s)))
		}
		out = clarity
	}

	// The engine's own output level carries no information (measured
	// -25.3 dBFS), so the voice is first brought to a fixed reference and only
	// then offset by the emotion volume.
	if cfg.targetRMS > 0 {
		out = normalizeLoudness(out, cfg.sampleRate, cfg.targetRMS)
	}
	if trim := cfg.volume * cfg.outputGain; trim != 1 {
		scaled := make([]float32, len(out))
		for i, s := range out {
			scaled[i] = s * trim
		}
		out = scaled
	}
	return limitSamples(out)
}
