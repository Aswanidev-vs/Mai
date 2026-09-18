package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// roomTaps models Mai's voice arriving back at the mic: a direct path 94ms after
// it leaves the speaker, plus three reflections. Every delay is inside the
// canceller's 4096-tap window, so a converged filter can model it.
var roomTaps = map[int]float32{1500: 0.5, 1660: 0.25, 1980: 0.12, 2700: 0.06}

// syntheticEcho convolves the reference with roomTaps over one frame starting at
// reference sample t.
func syntheticEcho(ref []float32, t, n int) []float32 {
	out := make([]float32, n)
	for k, g := range roomTaps {
		for i := 0; i < n; i++ {
			if idx := t + i - k; idx >= 0 && idx < len(ref) {
				out[i] += g * ref[idx]
			}
		}
	}
	return out
}

// bargeInGateRMS mirrors barge_in_threshold * bargeInMargin from the config.
const bargeInGateRMS = 0.008 * 2.5

// measureBargeIn runs `frames` frames of playback through a *fresh* canceller
// and returns the last frame's echo coherence, residual level and whether the
// energy gate fired. micEcho is the captured level of Mai's own voice — the
// reported false trigger had a residual of 0.0844 — and userLevel adds an
// uncorrelated speaker on top, standing in for the user talking over her (a real
// voice is uncorrelated with her playback, which is the property that matters).
func measureBargeIn(frames int, micEcho, userLevel float32) (coherence, residualRMS float64, loud bool) {
	const n, L = 1600, 4096
	span := frames * n
	ref := randomSignal(L+span+n, 21, 0.3)
	user := randomSignal(span+n, 22, userLevel)

	// Mai's voice as captured, scaled to the requested level.
	echo := make([]float32, span)
	for t0 := 0; t0 < span; t0 += n {
		copy(echo[t0:], syntheticEcho(ref, t0, n))
	}
	if r := rmsOf(echo); r > 1e-12 {
		g := float32(float64(micEcho) / r)
		for i := range echo {
			echo[i] *= g
		}
	}

	// Prime with L samples of silence so the reference window lines up exactly
	// with the echo path from the first frame on.
	refBuffer.Push(make([]float32, L))

	ec := NewEchoCanceller(L)
	for t0 := 0; t0 < span; t0 += n {
		refBuffer.Push(ref[t0 : t0+n])
		mic := make([]float32, n)
		for i := range mic {
			mic[i] = echo[t0+i] + user[t0+i]
		}
		out := ec.Process(mic)
		residualRMS = rmsOf(out)
		loud = residualRMS > bargeInGateRMS
		if loud {
			coherence = ec.EchoCoherence(out) // only once the cheap gate fires
		}
	}
	return coherence, residualRMS, loud
}

// TestBargeIn_UncancelledEchoIsRejectedButInterruptionStillFires is the
// regression test for the reported false barge-in ("I didn't say anything but
// still this triggered", residual RMS 0.0844, held 150ms).
//
// Two properties have to hold at once, and they are what the 0.7 default is
// chosen from:
//
//	Mai's own leaked voice   0.73 - 0.87  -> rejected
//	a real interruption      0.45 - 0.67  -> allowed
//
// A canceller this size needs a couple of seconds of echo to converge, so the
// loud-leak window is real: at 400ms it leaves her voice at 0.075 RMS, far above
// the 0.02 energy gate.
func TestBargeIn_UncancelledEchoIsRejectedButInterruptionStillFires(t *testing.T) {
	const herVoice = 0.0844 // the level from the report

	// 400ms of playback (the warmup window), user silent: loud but her own voice.
	coherence, residual, loud := measureBargeIn(4, herVoice, 0)
	require.True(t, loud,
		"uncancelled echo must clear the energy gate — that was the bug (residual %.4f)", residual)
	assert.GreaterOrEqual(t, coherence, bargeInEchoMaxDefault,
		"Mai's own leaked voice must be rejected as echo (residual %.4f)", residual)

	// The user interrupts at a normal level once the canceller has had a second.
	coherence, _, _ = measureBargeIn(10, herVoice, 0.06)
	assert.Less(t, coherence, bargeInEchoMaxDefault,
		"a real interruption must not be dismissed as echo")

	// After the canceller converges her voice drops below the energy gate, so
	// barge-in cannot misfire there even without the coherence guard.
	_, residual, loud = measureBargeIn(30, herVoice, 0)
	assert.False(t, loud, "a converged canceller must leave her voice below the gate (residual %.4f)", residual)
}
