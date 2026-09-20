package main

import (
	"math"
	"testing"
)

// spectralCentroid is the energy-weighted mean frequency, the standard proxy for
// perceived "brightness". Lower = darker/duller, which is exactly the quality
// complaint the exciter exists to address.
func spectralCentroid(samples []float32, sampleRate int) float64 {
	const win = 1024
	const hop = 512
	if len(samples) < win {
		return 0
	}
	binHz := float64(sampleRate) / float64(win)
	maxBin := win / 2

	var num, den float64
	for start := 0; start+win <= len(samples); start += hop {
		var energy float64
		for i := 0; i < win; i++ {
			v := float64(samples[start+i])
			energy += v * v
		}
		if energy/float64(win) < 1e-5 {
			continue // skip silence so pauses don't drag the centroid down
		}
		for k := 1; k <= maxBin; k++ {
			var re, im float64
			for i := 0; i < win; i++ {
				h := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(win-1))
				v := float64(samples[start+i]) * h
				ang := 2 * math.Pi * float64(k) * float64(i) / float64(win)
				re += v * math.Cos(ang)
				im -= v * math.Sin(ang)
			}
			mag := re*re + im*im
			num += mag * float64(k) * binHz
			den += mag
		}
	}
	if den == 0 {
		return 0
	}
	return num / den
}

// fundamentalHz estimates the pitch of a near-periodic signal by picking the
// strongest FFT bin below 2 kHz, where speech fundamentals sit.
func fundamentalHz(samples []float32, sampleRate int) float64 {
	const win = 4096
	if len(samples) < win {
		return 0
	}
	start := len(samples)/2 - win/2
	binHz := float64(sampleRate) / float64(win)
	bestBin, bestMag := 1, 0.0
	for k := 1; k < int(2000/binHz); k++ {
		var re, im float64
		for i := 0; i < win; i++ {
			v := float64(samples[start+i])
			ang := 2 * math.Pi * float64(k) * float64(i) / float64(win)
			re += v * math.Cos(ang)
			im -= v * math.Sin(ang)
		}
		if mag := re*re + im*im; mag > bestMag {
			bestMag = mag
			bestBin = k
		}
	}
	return float64(bestBin) * binHz
}

func rmsLevelInTest(s []float32) float64 {
	var acc float64
	for _, v := range s {
		acc += float64(v) * float64(v)
	}
	return math.Sqrt(acc / float64(len(s)))
}

func peakAbs(s []float32) float64 {
	peak := 0.0
	for _, v := range s {
		if a := math.Abs(float64(v)); a > peak {
			peak = a
		}
	}
	return peak
}

// bandEnergyRatio is the share of total energy above cutoffHz. This is the right
// metric for an exciter: spectral centroid is dominated by the fundamental, so
// adding energy at 8 kHz moves it only slightly even though it is clearly
// audible, whereas this ratio moves in direct proportion to what the stage added.
func bandEnergyRatio(samples []float32, sampleRate int, cutoffHz float64) float64 {
	const win = 1024
	if len(samples) < win {
		return 0
	}
	binHz := float64(sampleRate) / float64(win)
	loBin := int(cutoffHz / binHz)
	if loBin < 1 {
		loBin = 1
	}

	total, high := 0.0, 0.0
	for start := 0; start+win <= len(samples); start += win {
		for k := 1; k < win/2; k++ {
			var re, im float64
			for i := 0; i < win; i++ {
				h := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(win-1))
				v := float64(samples[start+i]) * h
				ang := 2 * math.Pi * float64(k) * float64(i) / float64(win)
				re += v * math.Cos(ang)
				im -= v * math.Sin(ang)
			}
			mag := re*re + im*im
			total += mag
			if k >= loBin {
				high += mag
			}
		}
	}
	if total == 0 {
		return 0
	}
	return high / total
}

// sine combines a few harmonics so test signals are closer to speech than a
// single pure tone.
func sine(sr int, seconds float64, partials ...float64) []float32 {
	n := int(float64(sr) * seconds)
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sr)
		var v float64
		for j, f := range partials {
			v += math.Sin(2*math.Pi*f*t) / float64(j+1)
		}
		out[i] = float32(v / float64(len(partials)))
	}
	return out
}

// TestPitchShiftRaisesPitchAndKeepsDuration checks both halves of the WSOLA
// contract: the fundamental moves by the requested ratio and the length does not
// change. Getting either wrong would make Mai sound like a different person or
// drift out of sync with the transcript.
func TestPitchShiftRaisesPitchAndKeepsDuration(t *testing.T) {
	const sr = 24000
	const f0 = 220.0
	src := sine(sr, 2.0, f0, 2*f0, 3*f0)

	for _, c := range []struct {
		ratio float64
		want  float64
	}{{1.05, f0 * 1.05}, {1.08, f0 * 1.08}, {0.95, f0 * 0.95}} {
		got := pitchShift(src, c.ratio)
		if len(got) == 0 {
			t.Fatalf("ratio %.2f produced no audio", c.ratio)
		}

		gotF0 := fundamentalHz(got, sr)
		if gotF0 == 0 {
			t.Fatalf("ratio %.2f: no fundamental found", c.ratio)
		}
		if math.Abs(gotF0-c.want)/c.want > 0.04 {
			t.Errorf("ratio %.2f: fundamental %.1f Hz, want %.1f Hz (%.1f%% off, tolerance 4%%)",
				c.ratio, gotF0, c.want, 100*math.Abs(gotF0-c.want)/c.want)
		}
		if math.Abs(float64(len(got)-len(src))/float64(len(src))) > 0.05 {
			t.Errorf("ratio %.2f: duration changed by %.1f%% (tolerance 5%%)", c.ratio,
				100*math.Abs(float64(len(got)-len(src))/float64(len(src))))
		}
		t.Logf("[PITCH] ratio %.2f: f0 %.1f -> %.1f Hz (want %.1f), %d -> %d samples",
			c.ratio, f0, gotF0, c.want, len(src), len(got))
	}
}

// TestPitchShiftLeavesUnityAlone guards against paying for (and hearing) a
// shift that was not requested.
func TestPitchShiftLeavesUnityAlone(t *testing.T) {
	src := sine(24000, 1.0, 200)
	got := pitchShift(src, 1.0)
	if len(got) != len(src) {
		t.Fatalf("unity shift changed length: %d -> %d", len(src), len(got))
	}
	for i := range got {
		if got[i] != src[i] {
			t.Fatalf("unity shift modified sample %d: %v -> %v", i, src[i], got[i])
		}
	}
	t.Log("[PITCH] unity shift is a byte-for-byte no-op")
}

// TestPitchShiftClampsExtremeRatios keeps a pathological request from turning
// Mai into a different person: the personality layer allows 0.5-2.0, but that
// far from unity no longer sounds like the reference.
func TestPitchShiftClampsExtremeRatios(t *testing.T) {
	const f0 = 220.0
	src := sine(24000, 1.0, f0)
	gotF0 := fundamentalHz(pitchShift(src, 2.0), 24000)
	if gotF0 > f0*pitchShiftMax*1.05 {
		t.Errorf("ratio 2.0 was not clamped: f0 %.1f Hz exceeds the %.1f Hz ceiling", gotF0, f0*pitchShiftMax)
	}
	t.Logf("[PITCH] ratio 2.0 clamped to f0 %.1f Hz (ceiling %.1f)", gotF0, f0*pitchShiftMax)
}

// TestExciterBrightensWithoutBlowingUp checks that the exciter moves the
// spectral centroid upward (the fix for the toned-down complaint) without adding
// runaway energy or clipping.
func TestExciterBrightensWithoutBlowingUp(t *testing.T) {
	const sr = 24000
	// Band-limited but with real sibilant-band content at 3.5-4.5 kHz, which is
	// exactly the source band the exciter harvests.
	src := sine(sr, 2.0, 300, 900, 1800, 3600, 4500)

	// Normalise the source so the "how much high-band energy was added" number is
	// comparable across amounts, and so the RMS guard measures the stage rather
	// than the input level. The stage itself also normalises its detector, so
	// this mirrors what it does internally.
	peak := 0.0
	for _, v := range src {
		if a := math.Abs(float64(v)); a > peak {
			peak = a
		}
	}
	for i := range src {
		src[i] = src[i] * float32(0.4/peak)
	}

	srcRMS := rmsLevelInTest(src)
	airBefore := bandEnergyRatio(src, sr, 6000)

	for _, amount := range []float64{0, 0.25, 0.5} {
		got := applyExciter(src, sr, defaultExciterConfig(sr, amount))
		if len(got) != len(src) {
			t.Fatalf("amount %.2f changed length: %d -> %d", amount, len(src), len(got))
		}

		// The added harmonics must land in the air band the model leaves empty,
		// without growing the total level noticeably.
		airAfter := bandEnergyRatio(got, sr, 6000)
		rms := rmsLevelInTest(got)

		if rms > srcRMS*1.25 {
			t.Errorf("amount %.2f: RMS %.4f is %.2fx the source %.4f - too much added level",
				amount, rms, rms/srcRMS, srcRMS)
		}
		for _, v := range got {
			if v > 1.0 || v < -1.0 {
				t.Errorf("amount %.2f: sample out of range (%v)", amount, v)
				break
			}
		}

		if amount == 0 {
			if math.Abs(airAfter-airBefore) > 1e-9 {
				t.Errorf("amount 0 should be a no-op: air band %.5f -> %.5f", airBefore, airAfter)
			}
		} else if airAfter < airBefore*1.10 {
			t.Errorf("amount %.2f barely brightened: air band %.5f -> %.5f (%.2fx, want >= 1.10x)",
				amount, airBefore, airAfter, airAfter/airBefore)
		}
		t.Logf("[EXCITER] amount %.2f: air-band share %.4f%% -> %.4f%% (%.2fx), rms %.4f -> %.4f (%.2fx)",
			amount, 100*airBefore, 100*airAfter, airAfter/airBefore, srcRMS, rms, rms/srcRMS)
	}
}

// TestExciterDisabledIsNoOp makes sure the off switch bypasses the stage rather
// than still running filters at zero gain.
func TestExciterDisabledIsNoOp(t *testing.T) {
	src := sine(24000, 1.0, 300)
	got := applyExciter(src, 24000, defaultExciterConfig(24000, 0))
	if len(got) != len(src) {
		t.Fatalf("disabled exciter changed length: %d -> %d", len(src), len(got))
	}
	for i := range got {
		if got[i] != src[i] {
			t.Fatalf("disabled exciter modified sample %d: %v -> %v", i, src[i], got[i])
		}
	}
	t.Log("[EXCITER] amount 0 is a byte-for-byte no-op")
}

// TestColourVoiceChain verifies the post-synthesis chain: the exciter brightens
// without clipping, the disabled chain reduces to the limiter alone (byte-for-
// byte below the knee, soft-knee compression above it), and the master trim is
// applied.
//
// targetRMS is 0 here (normalisation off) so these assertions stay exact;
// TestLoudnessNormalisationFixesQuietEngineOutput covers the normaliser. The
// limiter itself is always on — it is the stage that keeps the PCM16 conversion
// from wrapping — so "disabled" means "limitSamples only", not "identity".
func TestColourVoiceChain(t *testing.T) {
	sr := 24000
	plain := func(volume, trim float32, exc exciterConfig) colourConfig {
		return colourConfig{sampleRate: sr, volume: volume, pitch: 1, outputGain: trim, exciter: exc}
	}

	src := sine(sr, 1.0, 300, 1200)

	out := colourVoice(src, plain(1, 1, defaultExciterConfig(sr, 0.25)))
	if len(out) != len(src) {
		t.Fatalf("chain changed length: %d -> %d", len(src), len(out))
	}
	for _, v := range out {
		if v > 1.0 || v < -1.0 {
			t.Fatalf("band-poor input must not be excited, let alone clip (got %v)", v)
		}
	}
	t.Logf("[COLOUR] band-poor input stays in range (peak %.3f)", peakAbs(out))

	// A voice-like signal with sibilant content must still brighten.
	voiced := sine(sr, 1.0, 300, 900, 1800, 3600, 4500)
	brightened := colourVoice(voiced, plain(1, 1, defaultExciterConfig(sr, 0.25)))
	before := bandEnergyRatio(voiced, sr, 6000)
	after := bandEnergyRatio(brightened, sr, 6000)
	if after < before*1.10 {
		t.Errorf("chain did not brighten a voice-like signal: air band %.5f -> %.5f", before, after)
	}
	for _, v := range brightened {
		if v > 1.0 || v < -1.0 {
			t.Fatalf("voice-like input clipped (got %v)", v)
		}
	}
	t.Logf("[COLOUR] voice-like input: air band %.4f%% -> %.4f%%, in range", 100*before, 100*after)

	// With every optional stage off, whatever remains is the always-on limiter.
	// A quiet signal never reaches the knee, so it must come out untouched.
	// (sine's second argument is duration; the partials' combined peak is ~0.75,
	// so halve it to stay under the knee.)
	quiet := sine(sr, 0.5, 300, 1200)
	for i := range quiet {
		quiet[i] *= 0.4 // peak ~0.36, well under the knee
	}
	noop := colourVoice(quiet, plain(1, 1, exciterConfig{sampleRate: sr, amount: 0}))
	for i := range noop {
		if noop[i] != quiet[i] {
			t.Fatalf("fully disabled chain modified below-knee sample %d: %v -> %v", i, quiet[i], noop[i])
		}
	}
	t.Logf("[COLOUR] disabled chain is a byte-for-byte no-op below the knee (peak %.3f)", peakAbs(quiet))

	// A signal over the knee may only be soft-knee compressed — never expanded,
	// never clipped past the ceiling.
	limited := colourVoice(src, plain(1, 1, exciterConfig{sampleRate: sr, amount: 0}))
	for i := range limited {
		if want := float32(softLimit(float64(src[i]))); limited[i] != want {
			t.Fatalf("disabled chain did more than limit at sample %d: %v, want %v", i, limited[i], want)
		}
	}
	t.Logf("[COLOUR] over-knee input (peak %.3f) is only knee-compressed (peak %.3f)",
		peakAbs(src), peakAbs(limited))

	// The trim scales before the limiter, so a halved signal stays under the
	// knee and must survive exactly.
	trimmed := colourVoice(src, plain(1, 0.5, exciterConfig{sampleRate: sr, amount: 0}))
	for i := range trimmed {
		if math.Abs(float64(trimmed[i])-0.5*float64(src[i])) > 1e-4 {
			t.Fatalf("trim not applied at sample %d: %v, want %v", i, trimmed[i], 0.5*src[i])
		}
	}
	t.Log("[COLOUR] master trim applied correctly")
}

// TestLoudnessNormalisationFixesQuietEngineOutput pins the measured defect:
// Pocket emits rms 0.0544 (-25.3 dBFS) with a peak of 0.486, and the chain used
// to pass that through at unity gain — which is the "the volume is at 100% and
// she still sounds quiet" report. Normalisation must bring the speech to the
// target while the limiter keeps the peak bounded.
func TestLoudnessNormalisationFixesQuietEngineOutput(t *testing.T) {
	const sr = 24000
	wantDB := voiceTargetRMSDefault
	target := dbfsToLinear(wantDB)

	scaled := func(rms float64) []float32 {
		s := sine(sr, 1.0, 220, 700, 1600, 3200, 4800)
		scale := rms / rmsLevelInTest(s)
		for i := range s {
			s[i] = float32(float64(s[i]) * scale)
		}
		return s
	}
	offBy := func(rms float64) float64 { return math.Abs(levelDBFS(rms) - wantDB) }
	cfg := colourConfig{sampleRate: sr, volume: 1, pitch: 1, outputGain: 1, targetRMS: target,
		exciter: exciterConfig{sampleRate: sr, amount: 0}}

	quiet := scaled(0.0544) // the level Pocket actually emits
	out := colourVoice(quiet, cfg)
	if err := offBy(rmsLevelInTest(out)); err > 1.0 {
		t.Errorf("quiet output is %.1f dB off the %.0f dBFS target (in rms %.4f -> out rms %.4f)",
			err, wantDB, rmsLevelInTest(quiet), rmsLevelInTest(out))
	}
	if peak := peakAbs(out); peak > limiterCeiling+1e-6 {
		t.Errorf("limiter let a peak of %.3f through (ceiling %.2f)", peak, limiterCeiling)
	}
	t.Logf("[LOUDNESS] quiet engine output rms %.4f (%.1f dBFS) -> %.4f (%.1f dBFS), peak %.3f",
		rmsLevelInTest(quiet), levelDBFS(rmsLevelInTest(quiet)),
		rmsLevelInTest(out), levelDBFS(rmsLevelInTest(out)), peakAbs(out))

	// An utterance already at the target must not be pushed louder.
	loudOut := colourVoice(scaled(target), cfg)
	if err := offBy(rmsLevelInTest(loudOut)); err > 1.0 {
		t.Errorf("already-loud input left the target by %.1f dB", err)
	}
	t.Logf("[LOUDNESS] already-loud input stays put (peak %.3f)", peakAbs(loudOut))

	// Silence must not be amplified: normalising near-silence would lift the
	// room noise floor to the target level.
	silent := make([]float32, sr/4)
	if peak := peakAbs(colourVoice(silent, cfg)); peak > 1e-6 {
		t.Errorf("silence was amplified to a peak of %.6f", peak)
	}
	t.Log("[LOUDNESS] silence is not amplified")
}

// TestLoudnessLeavesUnspeakableEngineOutputAlone pins the other half of the
// contract: an utterance the engine did not really speak must not have the
// +12 dB cap spent on it. Measured Pocket failures — a lone "..." fragment, or
// an early EOS that leaves a sub-second lead-in blip — come back at rms 0.0018
// (-55 dBFS) with a peak near 0.011, and normalising that just raises the
// model's noise floor, which makes the [TTS-LEVEL] line read as a level problem
// when it is really a failed generation.
func TestLoudnessLeavesUnspeakableEngineOutputAlone(t *testing.T) {
	const sr = 24000
	target := dbfsToLinear(voiceTargetRMSDefault)

	// 0.56 s with a single short blip: the shape the logging captured.
	failed := make([]float32, sr*56/100)
	for i := 4000; i < 5000; i++ {
		failed[i] = float32(0.011 * math.Sin(2*math.Pi*300*float64(i)/float64(sr)))
	}
	if gain, ok := loudnessGain(failed, sr, target); ok {
		t.Errorf("failed generation was normalised at %.2fx (%+.1f dB); want it left alone",
			gain, 20*math.Log10(gain))
	}
	out := normalizeLoudness(failed, sr, target)
	for i := range failed {
		if out[i] != failed[i] {
			t.Fatalf("sample %d changed (%v -> %v); a failed generation must pass through untouched",
				i, failed[i], out[i])
		}
	}
	t.Logf("[LOUDNESS] failed generation (peak %.3f) passes through unamplified", peakAbs(failed))

	// The guard must not swallow genuinely quiet speech: 20 dB below the target
	// still normalises, capped as always at the +12 dB maximum.
	quiet := sine(sr, 1.0, 220, 700, 1600)
	scale := 0.0158 / rmsLevelInTest(quiet) // ~20 dB below the -16 dBFS target
	for i := range quiet {
		quiet[i] = float32(float64(quiet[i]) * scale)
	}
	gain, ok := loudnessGain(quiet, sr, target)
	if !ok {
		t.Fatalf("genuinely quiet speech (rms %.4f) was treated as a failed generation", rmsLevelInTest(quiet))
	}
	if gain != voiceMaxGain {
		t.Errorf("quiet speech gained %.2fx (%+.1f dB), want the %.1fx cap",
			gain, 20*math.Log10(gain), voiceMaxGain)
	}
	t.Logf("[LOUDNESS] quiet-but-real speech (rms %.4f) still normalises at the %+.1f dB cap",
		rmsLevelInTest(quiet), 20*math.Log10(gain))
}

// distortion path: colourVoice used to hard-clip each sample, and any sample
// that escaped above 1.0 wrapped sign in the PCM16 conversion — a full-scale
// click rather than a loud sample.
//
// The limiter is tested directly rather than through the chain, because
// loudness normalisation already attenuates a uniformly over-scale signal; the
// limiter's real job is bounding a high-crest-factor transient that the
// RMS-based gain could not foresee.
func TestLimiterCompressesInsteadOfWrapping(t *testing.T) {
	const sr = 24000
	hot := sine(sr, 0.5, 300, 900, 1800)
	for i := range hot {
		hot[i] *= 3.0 // well past full scale
	}

	out := limitSamples(hot)
	inPeak := peakAbs(hot)
	if inPeak <= limiterKnee {
		t.Fatalf("test signal is not over the knee (peak %.3f)", inPeak)
	}
	for i, v := range out {
		if v > limiterCeiling+1e-6 || v < -limiterCeiling-1e-6 {
			t.Fatalf("sample %d exceeds the ceiling %v (got %v)", i, limiterCeiling, v)
		}
	}
	outPeak := peakAbs(out)
	if outPeak <= limiterKnee {
		t.Errorf("peak reduced to %.3f, below the knee %.2f — attenuating rather than limiting",
			outPeak, limiterKnee)
	}
	if outPeak >= inPeak {
		t.Errorf("over-scale input was not reduced (%.3f -> %.3f)", inPeak, outPeak)
	}
	t.Logf("[LIMITER] input peak %.3f -> output peak %.3f (ceiling %.2f), all samples in range",
		inPeak, outPeak, limiterCeiling)

	// Through the whole chain the peak must stay bounded for every emotion
	// volume, including the loudest one (excited = 1.2) that is applied after
	// normalisation.
	for _, vol := range []float32{0.5, 1.0, 1.2} {
		got := colourVoice(hot, colourConfig{sampleRate: sr, volume: vol, pitch: 1, outputGain: 1,
			targetRMS: dbfsToLinear(voiceTargetRMSDefault), exciter: exciterConfig{sampleRate: sr, amount: 0}})
		if peak := peakAbs(got); peak > limiterCeiling+1e-6 {
			t.Errorf("chain at volume %.1f produced a peak of %.3f (ceiling %.2f)", vol, peak, limiterCeiling)
		}
	}
	t.Log("[LIMITER] chain stays bounded at volumes 0.5 / 1.0 / 1.2")

	// Below the knee the limiter must be transparent, or it would colour every
	// quiet sentence.
	soft := sine(sr, 0.25, 220, 660)
	for i, v := range soft {
		if got := softLimit(float64(v)); got != float64(v) {
			t.Fatalf("softLimit altered below-knee sample %d (%v -> %v)", i, v, got)
		}
	}
	t.Log("[LIMITER] transparent below the knee")
}

// TestPCM16DoesNotWrap covers the playback conversion. int16(s*32767) wraps on
// overflow, so an over-full-scale sample used to come out as the opposite sign
// at full amplitude: a full-scale click, which is what intermittent crackle in
// the output is.
func TestPCM16DoesNotWrap(t *testing.T) {
	cases := []struct {
		in   float32
		want int16
	}{
		{0, 0}, {1, 32767}, {-1, -32767}, {1.5, 32767}, {3.0, 32767}, {-1.5, -32768}, {-3.0, -32768},
	}
	for _, c := range cases {
		if got := pcm16(c.in); got != c.want {
			t.Errorf("pcm16(%v) = %d, want %d", c.in, got, c.want)
		}
	}
	// Documenting what the clamp prevents. This has to go through a variable:
	// Go rejects the overflowing conversion at compile time when the operand is
	// a constant, but performs (and silently wraps) it when the operand is a
	// runtime value — which is exactly the case in the playback callback.
	over := float64(1.5)
	if naive := int16(over * 32767.0); naive >= 0 {
		t.Errorf("expected the runtime conversion to wrap negative, got %d", naive)
	} else {
		t.Logf("[PCM] runtime int16(1.5*32767) = %d (a sign-flipped full-scale click)", naive)
	}

	// Every frame of the device buffer must be written: an untouched frame
	// replays the previous callback's audio.
	buf := make([]byte, 2*8)
	for i := range buf {
		buf[i] = 0xAB
	}
	writePCMFrame(buf, 0, 0.5)
	fillSilence(buf, 1, 8)
	if buf[0] != 0xFF || buf[1] != 0x3F {
		t.Errorf("frame 0 = %#x %#x, want 0x3FFF (0.5 full scale)", buf[0], buf[1])
	}
	for i := 2; i < len(buf); i++ {
		if buf[i] != 0 {
			t.Fatalf("byte %d left stale (%#x); unfilled frames replay old audio", i, buf[i])
		}
	}
	t.Log("[PCM] 0.5 -> 0x3FFF, remaining frames silenced")
}
