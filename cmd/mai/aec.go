package main

import "sync"

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
	ratio float64
	pos   float64
	prev  float32
	has   bool
}

func newResampler(inRate, outRate int) *resampler {
	return &resampler{ratio: float64(outRate) / float64(inRate)}
}

func (r *resampler) resample(in []float32) []float32 {
	if len(in) == 0 {
		return nil
	}
	if r.ratio == 1 {
		out := make([]float32, len(in))
		copy(out, in)
		return out
	}
	out := make([]float32, 0, int(float64(len(in))*r.ratio)+2)
	if !r.has {
		r.prev = in[0]
		r.has = true
	}
	for i := 1; i < len(in); i++ {
		r.pos += r.ratio
		for r.pos >= 1 {
			r.pos -= 1
			t := float32(r.pos)
			out = append(out, r.prev*(1-t)+in[i]*t)
		}
		r.prev = in[i]
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
		mu:  0.1,
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
func (e *EchoCanceller) Process(frame []float32) []float32 {
	n := len(frame)
	if n == 0 {
		return frame
	}
	refWin := e.ref.recent(e.L + n)
	out := make([]float32, n)
	L := e.L
	for i := 0; i < n; i++ {
		var y, norm float32
		for k := 0; k < L; k++ {
			xk := refWin[i+k]
			y += e.w[k] * xk
			norm += xk * xk
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

// refBuffer is the shared speaker-reference ring (16 kHz, the mic rate).
var refBuffer = newSpeakerRef(4 * 16000)
