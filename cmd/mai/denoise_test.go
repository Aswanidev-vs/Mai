package main

import (
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/user/mai/pkg/models"
)

// audioFrame is one capture frame: 100 ms at 16 kHz, matching
// audio.capture_buffer_ms.
const audioFrame = 1600

// toneFrame builds a frame of n samples alternating between +/-level, so its
// RMS is exactly level and the energy is fully deterministic.
func toneFrame(n int, level float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		if i%2 == 0 {
			out[i] = level
		} else {
			out[i] = -level
		}
	}
	return out
}

func TestSpeechRatioGate_DefaultsOpen(t *testing.T) {
	g := newSpeechRatioGate(0) // <= 0 falls back to the default
	assert.Equal(t, defaultSpeechRatioMin, g.min)
	assert.True(t, g.passes(), "an un-primed gate must never block audio")

	var nilGate *speechRatioGate
	assert.True(t, nilGate.passes(), "a nil gate must never block audio")
}

func TestSpeechRatioGate_NoiseClosesGate(t *testing.T) {
	g := newSpeechRatioGate(0.3)
	raw := toneFrame(1600, 0.02)  // fan drone arriving at the mic
	den := toneFrame(1600, 0.002) // enhancer knocked it down ~20 dB

	for i := 0; i < 5; i++ {
		g.update(raw, den)
	}
	assert.False(t, g.passes(), "a heavily attenuated frame is noise, not speech")
}

func TestSpeechRatioGate_SpeechStaysOpen(t *testing.T) {
	g := newSpeechRatioGate(0.3)
	raw := toneFrame(1600, 0.05)
	den := toneFrame(1600, 0.045) // speech survives nearly intact

	for i := 0; i < 20; i++ {
		g.update(raw, den)
	}
	assert.True(t, g.passes(), "speech must never be gated out")
}

func TestSpeechRatioGate_SpeechReopensGate(t *testing.T) {
	g := newSpeechRatioGate(0.3)
	noise := toneFrame(1600, 0.02)
	quiet := toneFrame(1600, 0.001)
	for i := 0; i < 5; i++ {
		g.update(noise, quiet)
	}
	require.False(t, g.passes())

	// The user starts speaking over the fan: a single frame must reopen it,
	// otherwise the first word of every interruption would be lost.
	g.update(toneFrame(1600, 0.06), toneFrame(1600, 0.055))
	assert.True(t, g.passes(), "the user's first word must not be gated out")
}

func TestSpeechRatioGate_SilenceCannotCloseTheGate(t *testing.T) {
	// Silence is below the gate's floor, so it does not vote. Near-silent room
	// tone must not be able to close the gate: anything that quiet cannot open
	// a turn by itself anyway, and speech reopens the gate instantly.
	g := newSpeechRatioGate(0.3)
	silence := make([]float32, 1600) // RMS 0 < speechGateFloor

	for i := 0; i < 20; i++ {
		g.update(silence, silence)
	}
	assert.True(t, g.passes(), "silence carries no verdict")
}

func TestSpeechRatioGate_FloorBoundary(t *testing.T) {
	below := toneFrame(1600, float32(speechGateFloor*0.5))
	above := toneFrame(1600, float32(speechGateFloor*2))
	fullySuppressed := make([]float32, 1600)

	g := newSpeechRatioGate(0.3)
	for i := 0; i < 5; i++ {
		// A zero ratio would close the gate if it were counted.
		g.update(below, below)
	}
	assert.True(t, g.passes(), "frames below the floor must not vote")

	for i := 0; i < 5; i++ {
		g.update(above, fullySuppressed)
	}
	assert.False(t, g.passes(), "frames above the floor must vote")
}

func TestSpeechRatioGate_ResetReopens(t *testing.T) {
	g := newSpeechRatioGate(0.3)
	for i := 0; i < 5; i++ {
		g.update(toneFrame(1600, 0.02), toneFrame(1600, 0.001))
	}
	require.False(t, g.passes())

	g.Reset()
	assert.True(t, g.passes())
}

func TestSpeechRatioGate_EnhancerCannotAmplify(t *testing.T) {
	// If the denoised frame ever reads louder than the input (alignment jitter
	// between the enhancer's output and the frame it came from), the ratio is
	// clamped instead of inflating the estimate.
	g := newSpeechRatioGate(0.3)
	for i := 0; i < 20; i++ {
		g.update(toneFrame(1600, 0.01), toneFrame(1600, 0.5))
	}
	assert.LessOrEqual(t, g.ratio, 1.0)
}

func TestRMSOf(t *testing.T) {
	assert.Equal(t, 0.0, rmsOf(nil))
	assert.Equal(t, 0.0, rmsOf([]float32{}))
	assert.InDelta(t, 0.25, rmsOf(toneFrame(100, 0.25)), 1e-9)
}

func TestNoiseSuppressor_NilIsSafeAndPermissive(t *testing.T) {
	var n *noiseSuppressor

	den, speech := n.Process(toneFrame(1600, 0.02))
	assert.Nil(t, den)
	assert.True(t, speech, "without a suppressor nothing may be gated out")

	// Must not panic on the "suppression disabled" state.
	n.Reset()
	n.Close()
}

func TestNewDenoiseGate_NilSuppressor(t *testing.T) {
	// Disabled / modelless configurations must yield no worker at all, so
	// nothing starts a goroutine that has no model to run.
	assert.Nil(t, newDenoiseGate(nil))
}

func TestDenoiseGate_NilIsSafeAndPermissive(t *testing.T) {
	var g *denoiseGate

	// The callback calls these on every frame; none may panic or block.
	g.Feed(toneFrame(1600, 0.02), false)
	assert.True(t, g.Speech(), "without an enhancer nothing may be gated out")
	assert.True(t, g.HaveVerdict(), "without an enhancer a verdict is always available")
	assert.Zero(t, g.Dropped())

	g.Reset()
	g.Close()

	var nilGate *speechRatioGate
	assert.True(t, nilGate.passes())
	nilGate.Reset()
	nilGate.update(toneFrame(10, 0.5), toneFrame(10, 0.5))
}

// firstExisting returns the first candidate path that exists, so the real-model
// tests run both from the package directory (go test ./cmd/mai) and from the
// repository root (a compiled test binary placed next to the sherpa DLLs).
func firstExisting(paths ...string) string {
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// denoiseModelPath locates the enhancer model.
func denoiseModelPath() string {
	return firstExisting(filepath.Join("..", "..", "dpdfnet8.onnx"), "dpdfnet8.onnx")
}

// newTestGate builds the real gate (real model, real worker goroutine), or skips
// when this machine cannot run the enhancer.
func newTestGate(t *testing.T) *denoiseGate {
	t.Helper()
	if !onnxRuntimeSupportsDenoiser() {
		t.Skipf("ONNX Runtime %s is older than 1.%d, which the enhancer requires; "+
			"place the sherpa-onnx DLLs next to the test binary to exercise this",
			sherpa.GetOnnxruntimeVersion(), minOnnxRuntimeMinor)
	}
	model := denoiseModelPath()
	if model == "" {
		t.Skip("DPDFNet model (dpdfnet8.onnx) not available")
	}

	var cfg models.Config
	cfg.Denoise.Enabled = true
	cfg.Denoise.Model = model
	cfg.Denoise.NumThreads = 2

	gate := newDenoiseGate(newNoiseSuppressor(&cfg))
	require.NotNil(t, gate, "the enhancer must load %s", model)
	t.Cleanup(gate.Close)
	return gate
}

// fanDrone synthesizes the broadband whoosh of a laptop fan (low-passed noise
// plus 120 Hz mains hum) at the requested RMS level, deterministically.
func fanDrone(n int, level float32) []float32 {
	rnd := rand.New(rand.NewSource(42))
	out := make([]float32, n)
	lp := 0.0
	for i := range out {
		lp += 0.15 * (rnd.NormFloat64() - lp)
		out[i] = float32(lp*3 + 0.15*math.Sin(2*math.Pi*120*float64(i)/16000))
	}
	if r := rmsOf(out); r > 1e-12 {
		g := float32(float64(level) / r)
		for i := range out {
			out[i] *= g
		}
	}
	return out
}

// TestDenoiseGate_RealModel_GatesOutFanDrone is the regression test for the
// reported bug: a laptop fan was being transcribed as a user utterance and
// opening turns. The enhancer must positively identify the drone as noise so
// the frame is dropped before it reaches the spotter, VAD or ASR.
func TestDenoiseGate_RealModel_GatesOutFanDrone(t *testing.T) {
	gate := newTestGate(t)
	drone := fanDrone(2*16000, 0.02) // 2 s of fan noise at a realistic mic level

	// Pace frames roughly in real time so the worker keeps up (the model needs
	// ~0.6 of a core), and assert the drone closes the gate well within 2 s.
	const cadence = 70 * time.Millisecond
	closedAfter := 0
	for off := 0; off+audioFrame <= len(drone); off += audioFrame {
		gate.Feed(drone[off:off+audioFrame], false)
		time.Sleep(cadence)
		closedAfter++
		if !gate.Speech() {
			break
		}
	}

	require.NotZero(t, closedAfter, "at least one frame must be analysed")
	assert.True(t, gate.HaveVerdict(), "the enhancer must have formed a verdict")
	assert.False(t, gate.Speech(), "a fan drone must close the noise gate")
	t.Logf("fan drone gated out after %d frame(s) (~%dms)", closedAfter, closedAfter*100)
	assert.LessOrEqual(t, closedAfter, 5, "the drone must be rejected promptly")
}

// TestDenoiseGate_RealModel_KeepsSpeechOverDroneOpen is the other half of the
// contract: while the same drone is running, real speech must still get through,
// and quickly — a gate that stays shut for longer than the VAD's minimum speech
// duration would swallow the user's turn.
func TestDenoiseGate_RealModel_KeepsSpeechOverDroneOpen(t *testing.T) {
	wav := firstExisting(
		filepath.Join("..", "..", "sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01", "test_wavs", "0.wav"),
		filepath.Join("sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01", "test_wavs", "0.wav"),
	)
	if wav == "" {
		t.Skip("16 kHz speech sample not available")
	}
	w := sherpa.ReadWave(wav)
	if w == nil || w.SampleRate != 16000 || len(w.Samples) < audioFrame {
		t.Skipf("16 kHz speech sample not usable at %s", wav)
	}

	const speechLevel = 0.06 // typical speech level arriving at the mic

	// Speech alone, at a realistic level...
	speech := append([]float32(nil), w.Samples...)
	if r := rmsOf(speech); r > 1e-12 {
		g := float32(speechLevel / r)
		for i := range speech {
			speech[i] *= g
		}
	}
	// ...plus the fan drone 10 dB below it, which is the user's real situation.
	mixed := append([]float32(nil), speech...)
	drone := fanDrone(len(speech), float32(speechLevel/math.Pow(10, 10.0/20)))
	for i := range mixed {
		mixed[i] += drone[i]
	}

	gate := newTestGate(t)

	// Frames are paced roughly in real time so the worker keeps up. The verdict
	// is paired with the *previous* frame, because the worker can be a frame or
	// two behind and that is the conservative comparison.
	const cadence = 70 * time.Millisecond
	const speechFloor = speechLevel / 2 // quieter than this = a pause, not speech
	checked, runs, worstRun := 0, 0, 0
	for off := 0; off+audioFrame <= len(mixed); off += audioFrame {
		gate.Feed(mixed[off:off+audioFrame], false)
		time.Sleep(cadence)

		prev := off - audioFrame
		if prev < 0 || rmsOf(speech[prev:prev+audioFrame]) < speechFloor {
			continue // leading silence or a pause: the drone alone is present
		}
		checked++
		if gate.Speech() {
			runs = 0
			continue
		}
		runs++
		if runs > worstRun {
			worstRun = runs
		}
	}

	require.NotZero(t, checked, "the sample must contain speech frames")
	assert.LessOrEqual(t, worstRun, 3,
		"speech over a fan drone must not be gated out for more than ~300ms")
	t.Logf("checked %d speech frames, longest gated run %d frame(s)", checked, worstRun)
}

func TestHasStreamingProfile(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, data, 0o600))
		return p
	}

	t.Run("streaming 16k export", func(t *testing.T) {
		p := write("dpdfnet8.onnx", []byte("junk\x00dpdfnet_16khz\x00sample_rate\x00n_fft"))
		assert.True(t, hasStreamingProfile(p))
	})

	t.Run("streaming 48k export", func(t *testing.T) {
		assert.True(t, hasStreamingProfile(write("hr.onnx", []byte("dpdfnet2_48khz_hr"))))
	})

	t.Run("marker split across the scan chunk boundary", func(t *testing.T) {
		// The scanner reads 1 MiB at a time. A profile marker straddling that
		// boundary must still be found, otherwise a perfectly good model would
		// be silently declined.
		const chunkSize = 1 << 20
		marker := []byte("dpdfnet_16khz")
		pad := chunkSize - 5 // start the marker 5 bytes before the boundary
		buf := make([]byte, pad+len(marker)+16)
		copy(buf[pad:], marker)

		assert.True(t, hasStreamingProfile(write("split.onnx", buf)))
	})

	t.Run("non-streaming export is rejected", func(t *testing.T) {
		// A non-streaming DPDFNet export would make the C library call exit(-1)
		// and kill the process, so it must be refused up front.
		assert.False(t, hasStreamingProfile(write("offline.onnx", []byte("profile\x00dpdfnet_8khz\x00n_fft"))))
	})

	t.Run("no profile metadata", func(t *testing.T) {
		assert.False(t, hasStreamingProfile(write("plain.onnx", []byte("onnx\x00random bytes"))))
	})

	t.Run("missing file", func(t *testing.T) {
		assert.False(t, hasStreamingProfile(filepath.Join(dir, "absent.onnx")))
	})
}

func TestNewNoiseSuppressor_DegradesWithoutModel(t *testing.T) {
	var cfg models.Config

	cfg.Denoise.Enabled = false
	assert.Nil(t, newNoiseSuppressor(&cfg), "disabled must yield no suppressor")

	cfg.Denoise.Enabled = true
	cfg.Denoise.Model = filepath.Join(t.TempDir(), "absent.onnx")
	assert.Nil(t, newNoiseSuppressor(&cfg), "a missing model must degrade, not crash")

	// A non-streaming export is refused before the C library can exit(-1).
	offline := filepath.Join(t.TempDir(), "offline.onnx")
	require.NoError(t, os.WriteFile(offline, []byte("profile\x00dpdfnet_8khz"), 0o600))
	cfg.Denoise.Model = offline
	assert.Nil(t, newNoiseSuppressor(&cfg))
}

