package main

import (
	"bytes"
	"io"
	"log"
	"math"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/user/mai/pkg/models"
)

// Background-noise suppression for the microphone path.
//
// Everything downstream of the mic (the wake-word spotter, the VAD, ASR, and
// the follow-up energy check) assumes that anything loud enough is speech. A
// laptop fan, HVAC vent or mains hum therefore looks like a user utterance and
// can trigger a turn on its own. The DPDFNet speech enhancer solves exactly
// that: it is a *streaming* model that keeps speech nearly untouched while
// attenuating stationary noise by an order of magnitude.
//
// Two things are derived from it:
//
//  1. The denoised frame, which replaces the raw mic frame on its way to the
//     spotter/VAD/ASR.
//  2. A speech-presence ratio (denoised RMS / raw RMS), used as an extra gate
//     so noise that still clears an energy threshold can be rejected outright.
//     Speech keeps most of its energy (ratio ~0.7-1.0); a fan drone loses most
//     of it (ratio ~0.05-0.2).

// defaultSpeechRatioMin is the denoised/raw energy ratio below which a frame is
// treated as stationary noise rather than speech. The margin between speech
// (~0.7-1.0) and noise (~0.05-0.2) is wide, so this can sit in the middle.
const defaultSpeechRatioMin = 0.3

// speechRatioAlpha is the EMA weight used while *closing* the gate. Opening is
// instantaneous (see update), so this only controls how long a noise verdict
// takes to settle: 0.5 lands it within ~2 frames (200ms), fast enough to keep a
// fan drone from opening a turn.
const speechRatioAlpha = 0.5

// speechGateFloor is the frame energy below which the enhancer's ratio is not
// consulted at all. Frames quieter than the follow-up energy gate (0.001)
// cannot open a turn by themselves, so letting them vote would only allow room
// tone to close the gate for no reason.
const speechGateFloor = 0.001

// streamingProfiles are the DPDFNet exports that the *online* (streaming)
// denoiser accepts. Any other export only works with the offline denoiser,
// which needs the whole utterance up front. Creating an online denoiser from a
// non-streaming export calls exit(-1) inside the C library and kills the
// process with no Go-level error, so the profile is checked before we ask for
// the denoiser. Kept in sync with sherpa-onnx's
// OnlineSpeechDenoiserDpdfNetImpl::Init().
var streamingProfiles = []string{"dpdfnet_16khz", "dpdfnet2_48khz_hr"}

// minOnnxRuntimeMinor is the ONNX Runtime minor version the pinned sherpa-onnx
// C API asks for when it builds the speech enhancer (OrtGetApi(27), i.e. ONNX
// Runtime 1.27+).
//
// A mismatch is not reported as a Go error: the native library takes the whole
// process down with an access violation before any Go code can react. This is
// not hypothetical — Windows ships an older onnxruntime.dll (1.17) in System32,
// and because Windows does not search the working directory for a dependency,
// that stale copy is what gets loaded whenever the sherpa DLLs are not sitting
// next to the executable. So the version is verified before we call into it.
const minOnnxRuntimeMinor = 27

// onnxRuntimeSupportsDenoiser reports whether the ONNX Runtime loaded into this
// process is new enough for the speech enhancer. Anything unparseable is
// treated as unsupported, which costs only the denoiser.
func onnxRuntimeSupportsDenoiser() bool {
	parts := strings.SplitN(strings.TrimSpace(sherpa.GetOnnxruntimeVersion()), ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	return major > 1 || (major == 1 && minor >= minOnnxRuntimeMinor)
}

// speechRatioGate smooths the enhancer's suppression ratio over time and turns
// it into a yes/no "was there speech in the recent frames?" verdict.
//
// It is deliberately open on creation and after every Reset: an un-primed gate
// must never block audio, because losing the user's first word is far worse
// than letting one noisy frame through.
type speechRatioGate struct {
	min   float64
	ratio float64
}

func newSpeechRatioGate(min float64) *speechRatioGate {
	if min <= 0 {
		min = defaultSpeechRatioMin
	}
	return &speechRatioGate{min: min, ratio: 1.0}
}

// update folds one frame's suppression ratio into the smoothed estimate.
// raw is the pre-enhancement frame, denoised the enhancer's output for roughly
// the same span of audio.
func (g *speechRatioGate) update(raw, denoised []float32) {
	if g == nil {
		return
	}
	rawRMS := rmsOf(raw)
	if rawRMS < speechGateFloor {
		// Below the gate's own floor: keep the previous verdict rather than let
		// near-silence (or a pause inside a sentence) vote.
		return
	}
	r := math.Min(rmsOf(denoised)/rawRMS, 1) // the enhancer never amplifies
	if r >= g.min {
		// Open instantly. The ratio jumps the moment real speech is present,
		// and any delay here would clip the first word of every turn.
		g.ratio = r
		return
	}
	// Close gradually, so one noisy frame between two words cannot gate the
	// user out.
	g.ratio += speechRatioAlpha * (r - g.ratio)
}

// passes reports whether the recent frames carried speech.
func (g *speechRatioGate) passes() bool {
	if g == nil {
		return true
	}
	return g.ratio >= g.min
}

// Reset reopens the gate.
func (g *speechRatioGate) Reset() {
	if g == nil {
		return
	}
	g.ratio = 1.0
}

// noiseSuppressor wraps the streaming DPDFNet speech enhancer. The model is
// stateful, so every captured frame must be fed through it in order. A nil
// *noiseSuppressor is the "suppression disabled" state, for which every method
// is a safe no-op.
type noiseSuppressor struct {
	sd   *sherpa.OnlineSpeechDenoiser
	rate int
	hop  int
	gate *speechRatioGate
}

// newNoiseSuppressor builds the enhancer described by cfg.Denoise. It returns
// nil (meaning "run without noise suppression", i.e. the previous raw-mic
// behaviour) whenever the feature is off, the model is missing or unusable, or
// it runs at a rate the 16 kHz pipeline cannot consume.
func newNoiseSuppressor(cfg *models.Config) *noiseSuppressor {
	if !cfg.Denoise.Enabled {
		log.Println("[DENOISE] Disabled in config — raw mic feeds the VAD/ASR")
		return nil
	}

	model := strings.TrimSpace(cfg.Denoise.Model)
	if model == "" {
		model = "dpdfnet8.onnx"
	}
	if _, err := os.Stat(model); err != nil {
		log.Printf("[DENOISE] Model %q not found (%v) — running without noise suppression", model, err)
		return nil
	}
	if !hasStreamingProfile(model) {
		log.Printf("[DENOISE] %q is not a streaming DPDFNet export (need profile %s) — running without noise suppression",
			model, strings.Join(streamingProfiles, " or "))
		return nil
	}
	if !onnxRuntimeSupportsDenoiser() {
		log.Printf("[DENOISE] ONNX Runtime %s is older than 1.%d, which the speech enhancer requires — running without noise suppression",
			sherpa.GetOnnxruntimeVersion(), minOnnxRuntimeMinor)
		return nil
	}

	threads := cfg.Denoise.NumThreads
	if threads <= 0 {
		threads = 2
	}
	provider := cfg.Denoise.Provider
	if provider == "" {
		provider = "cpu"
	}

	sd := sherpa.NewOnlineSpeechDenoiser(&sherpa.OnlineSpeechDenoiserConfig{
		Model: sherpa.OfflineSpeechDenoiserModelConfig{
			DpdfNet:    sherpa.OfflineSpeechDenoiserDpdfNetModelConfig{Model: model},
			NumThreads: int32(threads),
			Provider:   provider,
		},
	})
	if sd == nil {
		log.Printf("[DENOISE] Failed to create denoiser for %q — running without noise suppression", model)
		return nil
	}

	rate := sd.SampleRate()
	if rate != 16000 {
		// The mic, VAD, spotter and ASR are all wired to 16 kHz. Feeding a
		// 48 kHz enhancer output into them would corrupt every frame, so
		// decline the model rather than silently degrade recognition.
		log.Printf("[DENOISE] %q runs at %d Hz, pipeline needs 16000 Hz — running without noise suppression", model, rate)
		sherpa.DeleteOnlineSpeechDenoiser(sd)
		return nil
	}

	n := &noiseSuppressor{
		sd:   sd,
		rate: rate,
		hop:  sd.FrameShiftInSamples(),
		gate: newSpeechRatioGate(cfg.Denoise.SpeechRatioMin),
	}
	log.Printf("[DENOISE] DPDFNet speech enhancement ready (%s, %d Hz, hop=%d, threads=%d, ratio_min=%.2f)",
		model, n.rate, n.hop, threads, n.gate.min)
	return n
}

// Process streams one frame of 16 kHz mono mic audio through the enhancer. It
// returns the denoised samples for the hops that completed, plus whether those
// frames looked like speech rather than stationary noise.
//
// A nil or still-warming-up suppressor returns (nil, true): no denoised audio
// is available, and nothing may be gated out.
func (n *noiseSuppressor) Process(frame []float32) ([]float32, bool) {
	if n == nil || n.sd == nil || len(frame) == 0 {
		return nil, true
	}
	den := n.sd.Run(frame, n.rate).Samples
	if len(den) == 0 {
		// The enhancer buffers input until a full hop is available, and emits
		// nothing at all for its very first hop. Keep the previous verdict;
		// the caller falls back to the raw frame.
		return nil, n.gate.passes()
	}
	n.gate.update(frame, den)
	return den, n.gate.passes()
}

// Reset clears the enhancer's streaming state and reopens the speech gate.
// Called when the signal being fed to it changes (mic vs echo-cancelled
// residual), so two different signals are never spliced into one stream.
func (n *noiseSuppressor) Reset() {
	if n == nil || n.sd == nil {
		return
	}
	n.sd.Reset()
	n.gate.Reset()
}

// Close releases the model. Safe to call on a nil suppressor.
func (n *noiseSuppressor) Close() {
	if n == nil || n.sd == nil {
		return
	}
	sherpa.DeleteOnlineSpeechDenoiser(n.sd)
	n.sd = nil
}

// rmsOf returns the root-mean-square level of a frame (0 for an empty frame,
// which also avoids the NaN an inline 0/0 would produce).
func rmsOf(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float32
	for _, s := range samples {
		sum += s * s
	}
	return math.Sqrt(float64(sum / float32(len(samples))))
}

// hasStreamingProfile reports whether the ONNX file carries one of the
// streaming DPDFNet profile markers, without loading the whole model into
// memory.
func hasStreamingProfile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	const chunkSize = 1 << 20
	const overlap = 64 // every marker is shorter than this
	buf := make([]byte, chunkSize+overlap)
	carry := 0
	for {
		n, err := io.ReadFull(f, buf[carry:chunkSize+carry])
		total := carry + n
		if total > 0 {
			for _, p := range streamingProfiles {
				if bytes.Contains(buf[:total], []byte(p)) {
					return true
				}
			}
			// Keep the tail so a marker split across two chunks is still found.
			if total > overlap {
				copy(buf[:overlap], buf[total-overlap:total])
				carry = overlap
			} else {
				carry = total
			}
		}
		if err != nil {
			return false
		}
	}
}

// denoiseGate runs the enhancer on its own goroutine and exposes only its
// verdict, so the capture callback never waits for it.
//
// Measured on the target machine the model needs ~57ms per 100ms of audio
// (RTF ~0.57, and ~5.7ms per 160-sample hop) — the cost is that library's
// naive O(n^2) DFT STFT, which does not scale with threads. Running that inline
// on the miniaudio callback would stall every frame and drop capture, so the
// callback posts a copy of the frame and reads back the latest verdict instead.
//
// A frame or two of lag is harmless for a noise gate, and because a speech
// verdict is applied instantly the user's first word is never delayed by it.
type denoiseGate struct {
	ns      *noiseSuppressor
	frames  chan denoiseJob
	done    chan struct{}
	verdict atomic.Bool // true = the most recent frames looked like speech
	have    atomic.Bool // true once a frame of the current stream has been analysed
	closed  atomic.Bool
	dropped atomic.Int64
	wg      sync.WaitGroup
}

// denoiseJob is one frame tagged with the signal it came from, so the worker can
// restart the enhancer when the source changes instead of splicing two
// different signals into one stream.
type denoiseJob struct {
	samples    []float32
	onResidual bool // true = echo-cancelled residual, false = raw mic
}

const (
	// denoiseQueueDepth bounds how far the enhancer may fall behind. Two frames
	// (~200ms) keeps the verdict fresh; on overflow the *oldest* frame is
	// dropped, because a stale verdict is worse than a missing one.
	denoiseQueueDepth = 2
	// denoiseStreamGap resets the enhancer when the stream was interrupted for
	// longer than this, so two non-adjacent stretches of audio are never
	// spliced into one stream.
	denoiseStreamGap = 500 * time.Millisecond
)

// newDenoiseGate starts the enhancer worker. Returns nil when ns is nil, i.e.
// when suppression is disabled or the model is unavailable.
func newDenoiseGate(ns *noiseSuppressor) *denoiseGate {
	if ns == nil {
		return nil
	}
	g := &denoiseGate{ns: ns, frames: make(chan denoiseJob, denoiseQueueDepth), done: make(chan struct{})}
	g.verdict.Store(true) // an un-primed gate must never block audio
	g.wg.Add(1)
	go g.run()
	return g
}

func (g *denoiseGate) run() {
	defer g.wg.Done()
	var last time.Time
	var haveSource, lastResidual bool
	for {
		select {
		case <-g.done:
			return
		case job := <-g.frames:
			// A new signal, or a stream that was interrupted: start clean.
			if !haveSource || job.onResidual != lastResidual ||
				(!last.IsZero() && time.Since(last) > denoiseStreamGap) {
				g.ns.Reset()
				g.verdict.Store(true)
				g.have.Store(false)
			}
			haveSource, lastResidual, last = true, job.onResidual, time.Now()
			_, speech := g.ns.Process(job.samples)
			g.verdict.Store(speech)
			g.have.Store(true)
		}
	}
}

// Feed hands the enhancer a copy of the frame for background analysis. It never
// blocks and never allocates per call beyond the copy: if the enhancer has
// fallen behind, the oldest queued frame is discarded to make room.
//
// onResidual says which signal the frame belongs to (the echo-cancelled
// residual while Mai is speaking, the raw mic otherwise). Switching between the
// two restarts the enhancer, so a verdict is never formed from a spliced
// stream. Frames from different sources must be fed in the order they arrive —
// the enhancer is stateful.
func (g *denoiseGate) Feed(frame []float32, onResidual bool) {
	if g == nil || len(frame) == 0 || g.closed.Load() {
		return
	}
	job := denoiseJob{samples: append([]float32(nil), frame...), onResidual: onResidual}
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case g.frames <- job:
			return
		default:
			select {
			case <-g.frames:
				g.dropped.Add(1)
			default:
				// The worker drained it first; the next send will fit.
			}
		}
	}
}

// Speech reports whether the enhancer's most recent frames looked like speech
// rather than stationary noise. While it has no opinion yet, the gate is open,
// so a caller that must not lose audio can use it directly.
func (g *denoiseGate) Speech() bool {
	if g == nil {
		return true
	}
	return g.verdict.Load()
}

// HaveVerdict reports whether the enhancer has actually analysed a frame of the
// current stream. Callers that must not act on a guess — barge-in, where a
// wrong answer cuts Mai off mid-sentence — pair it with Speech(); callers that
// must not lose audio just use Speech().
func (g *denoiseGate) HaveVerdict() bool {
	if g == nil {
		return true
	}
	return g.have.Load()
}

// Dropped reports how many frames the enhancer fell behind and discarded.
func (g *denoiseGate) Dropped() int64 {
	if g == nil {
		return 0
	}
	return g.dropped.Load()
}

// Reset reopens the gate (used by tests and on explicit stream restarts).
func (g *denoiseGate) Reset() {
	if g == nil {
		return
	}
	g.verdict.Store(true)
	g.have.Store(false)
}

// Close stops the worker and only then frees the model, so the enhancer is
// never deleted while a frame is still inside it.
func (g *denoiseGate) Close() {
	if g == nil {
		return
	}
	if g.closed.CompareAndSwap(false, true) {
		close(g.done)
	}
	g.wg.Wait()
	g.ns.Close()
}