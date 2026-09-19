package main

import (
	"math"
	"sync"
)

// speakerRef is a thread-safe ring of the samples actually sent to the
// speaker. It is the echo reference for acoustic echo cancellation: Mai's own
// TTS voice, replayed through the mic, can be subtracted from the mic signal
// so that only a real (different) speaker trips barge-in.
type speakerRef struct {
	mu    sync.Mutex
	buf   []float32
	write int
}

func newSpeakerRef(capacity int) *speakerRef {
	return &speakerRef{buf: make([]float32, capacity)}
}

// Push appends speaker samples to the echo reference ring.
func (r *speakerRef) Push(samples []float32) {
	if len(samples) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range samples {
		r.buf[r.write] = s
		r.write = (r.write + 1) % len(r.buf)
	}
}

// Clear removes any stale playback history so the next utterance starts from a
// clean echo reference instead of inheriting the previous turn's speaker audio.
func (r *speakerRef) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.buf {
		r.buf[i] = 0
	}
	r.write = 0
}

// recent returns the last n samples (oldest-first).
func (r *speakerRef) recent(n int) []float32 {
	if n > len(r.buf) {
		n = len(r.buf)
	}
	out := make([]float32, n)
	r.mu.Lock()
	for i := 0; i < n; i++ {
		idx := r.write - n + i
		if idx < 0 {
			idx += len(r.buf)
		}
		out[i] = r.buf[idx]
	}
	r.mu.Unlock()
	return out
}

// resampler does linear-interpolation sample-rate conversion. It carries
// fractional state between calls so it can be fed streaming chunks.
type resampler struct {
	// step is the number of input samples represented by one output sample.
	// Keeping the phase in input-sample units avoids extrapolating when the
	// output rate is higher than the input rate.
	step  float64
	phase float64
	prev  float32
	has   bool
}

func newResampler(inRate, outRate int) *resampler {
	return &resampler{step: float64(inRate) / float64(outRate)}
}

func (r *resampler) resample(in []float32) []float32 {
	if len(in) == 0 {
		return nil
	}
	if r.step == 1 {
		out := make([]float32, len(in))
		copy(out, in)
		return out
	}
	out := make([]float32, 0, int(float64(len(in))/r.step)+2)
	start := 0
	if !r.has {
		r.prev = in[0]
		r.has = true
		start = 1
	}
	for i := start; i < len(in); i++ {
		curr := in[i]
		for r.phase < 1 {
			t := float32(r.phase)
			out = append(out, r.prev*(1-t)+curr*t)
			r.phase += r.step
		}
		// One input interval has been consumed. Any remaining phase carries
		// into the next interval, including across streaming calls.
		r.phase -= 1
		r.prev = curr
	}
	return out
}

// EchoCanceller performs adaptive (NLMS) acoustic echo cancellation. It
// estimates the speaker→mic echo path from the reference and subtracts it,
// leaving the residual (near-end / user) speech.
type EchoCanceller struct {
	ref *speakerRef
	L   int
	w   []float32
	mu  float32
	eps float32
}

func NewEchoCanceller(L int) *EchoCanceller {
	if L < 256 {
		L = 1024
	}
	return &EchoCanceller{
		ref: refBuffer,
		L:   L,
		w:   make([]float32, L),
		mu:  0.5, // fast convergence: echo path learned rapidly during initial frames
		eps: 1e-4,
	}
}

// Reset clears the adaptive filter weights so the canceller re-learns
// the echo path from scratch. Call after barge-in or when the acoustic
// environment changes significantly.
func (e *EchoCanceller) Reset() {
	for k := range e.w {
		e.w[k] = 0
	}
}

// Process returns the echo-cancelled residual for a mic frame. The reference
// window for sample i is refWin[i : i+L]; the adaptive filter learns the echo
// delay within L samples, so any room/capture latency below L is absorbed.
//
// This is the per-frame hot path and deliberately does no coherence analysis —
// see EchoCoherence for that.
func (e *EchoCanceller) Process(frame []float32) []float32 {
	n := len(frame)
	if n == 0 {
		return frame
	}
	refWin := e.ref.recent(e.L + n)
	out := make([]float32, n)
	L := e.L
	if len(refWin) < L+n {
		copy(out, frame)
		return out
	}

	var norm float32
	for k := 0; k < L; k++ {
		xk := refWin[k]
		norm += xk * xk
	}

	for i := 0; i < n; i++ {
		if i > 0 {
			if i%128 == 0 {
				norm = 0
				for k := 0; k < L; k++ {
					xk := refWin[i+k]
					norm += xk * xk
				}
			} else {
				prevX := refWin[i-1]
				newX := refWin[i+L-1]
				norm += newX*newX - prevX*prevX
				if norm < 0 {
					norm = 0
				}
			}
		}

		var y float32
		for k := 0; k < L; k++ {
			y += e.w[k] * refWin[i+k]
		}
		res := frame[i] - y
		out[i] = res
		if norm > e.eps {
			step := e.mu / norm
			for k := 0; k < L; k++ {
				e.w[k] += step * res * refWin[i+k]
			}
		}
	}
	return out
}

// coherenceDecim decimates the coherence correlation, cutting its cost 4x. 250us
// of resolution is plenty to tell "her voice" from "not her voice".
const coherenceDecim = 4

// EchoCoherence reports how strongly the residual is explained by the speaker
// reference: the largest normalised cross-correlation between the residual and
// the reference, at any delay the canceller can model.
//
// It answers a question residual energy alone cannot: *is this residual just
// Mai's own voice leaking through?* Her leaked voice is a delayed, quieter copy
// of what she played and scores ~0.85-1.0, while another speaker, a fan drone or
// room noise scores well below 0.5. That makes it usable as a double-talk
// detector even while the canceller is still converging — the window where a
// purely energy-based barge-in gate misfires, because an unconverged canceller
// leaves her voice in the residual at close to full level.
//
// The lag scan is exhaustive rather than coarse-to-fine on purpose: the echo
// path almost never lands on a stride boundary, and being a few dozen samples
// off decorrelates the residual completely. Measured on a true echo, a
// stride-only scan scored 0.17 where the exhaustive scan scores 0.85. Because
// that costs ~1.6M multiply-adds, it is meant to be called only when a cheaper
// energy gate has already fired, not on every frame.
func (e *EchoCanceller) EchoCoherence(residual []float32) float64 {
	n := len(residual)
	if n == 0 {
		return 0
	}
	refWin := e.ref.recent(e.L + n)
	if len(refWin) < e.L+n {
		return 0
	}
	if len(e.w) < e.L {
		return 0
	}

	// Decimate the residual once — it is reused for every lag.
	m := (n + coherenceDecim - 1) / coherenceDecim
	res := make([]float64, m)
	var resEnergy float64
	for k, i := 0, 0; i < n; i, k = i+coherenceDecim, k+1 {
		r := float64(residual[i])
		res[k] = r
		resEnergy += r * r
	}
	if resEnergy <= 0 {
		return 0 // silence is not echo
	}

	best := 0.0
	for d := 0; d < e.L; d++ {
		var num, refEnergy float64
		for k, i := 0, 0; i < n; i, k = i+coherenceDecim, k+1 {
			x := float64(refWin[d+i])
			num += res[k] * x
			refEnergy += x * x
		}
		if refEnergy <= 0 {
			continue
		}
		if c := math.Abs(num) / math.Sqrt(resEnergy*refEnergy); c > best {
			best = c
		}
	}
	return best
}

// refBuffer is the shared speaker-reference ring (16 kHz, the mic rate).
var refBuffer = newSpeakerRef(4 * 16000)
