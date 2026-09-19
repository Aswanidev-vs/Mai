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

// TestColourVoiceChain verifies the post-synthesis chain: gain is applied, the
// disabled chain is a byte-for-byte pass-through, and the output stays in range.
func TestColourVoiceChain(t *testing.T) {
	src := sine(24000, 1.0, 300, 1200)

	out := colourVoice(src, 1.0, 1.0, defaultExciterConfig(24000, 0.25))
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
	voiced := sine(24000, 1.0, 300, 900, 1800, 3600, 4500)
	brightened := colourVoice(voiced, 1.0, 1.0, defaultExciterConfig(24000, 0.25))
	before := bandEnergyRatio(voiced, 24000, 6000)
	after := bandEnergyRatio(brightened, 24000, 6000)
	if after < before*1.10 {
		t.Errorf("chain did not brighten a voice-like signal: air band %.5f -> %.5f", before, after)
	}
	for _, v := range brightened {
		if v > 1.0 || v < -1.0 {
			t.Fatalf("voice-like input clipped (got %v)", v)
		}
	}
	t.Logf("[COLOUR] voice-like input: air band %.4f%% -> %.4f%%, in range", 100*before, 100*after)

	plain := colourVoice(src, 1.0, 1.0, exciterConfig{sampleRate: 24000, amount: 0})
	for i := range plain {
		if plain[i] != src[i] {
			t.Fatalf("fully disabled chain modified sample %d: %v -> %v", i, src[i], plain[i])
		}
	}
	t.Log("[COLOUR] fully disabled chain is a byte-for-byte no-op")

	quiet := colourVoice(src, 0.5, 1.0, exciterConfig{sampleRate: 24000, amount: 0})
	for i := range quiet {
		if math.Abs(float64(quiet[i])-0.5*float64(src[i])) > 1e-4 {
			t.Fatalf("gain not applied at sample %d: %v, want %v", i, quiet[i], 0.5*src[i])
		}
	}
	t.Log("[COLOUR] gain applied correctly")
}
