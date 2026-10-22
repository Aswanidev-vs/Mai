package main

import (
	"strings"
	"sync"
	"sync/atomic"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// asrSnapshot is the latest state of the streaming decoder. Generation is the
// reset generation it belongs to; a snapshot from an older generation is
// ignored by the owner. Endpoint and Text are reported together so the turn
// finalizer never combines text from one utterance with an endpoint from the
// next.
type asrSnapshot struct {
	Generation uint64
	Text       string
	Endpoint   bool
}

// asrFrame is one captured block on its way to the decoder worker. The samples
// are copied by Submit: the audio pipeline owns its slice only for the duration
// of the frame callback, while the worker may run a chunk decode behind it.
type asrFrame struct {
	generation uint64
	samples    []float32
}

// streamingASR owns the online recognizer stream on exactly one goroutine.
//
// The old path called AcceptWaveform/IsReady/Decode from the audio pipeline
// goroutine while holding sherpaMu. A 560ms chunk decode can take hundreds of
// milliseconds, which stalls the capture drain and is the direct cause of the
// "capture queue overflowing" warnings. Here the capture side only copies a
// frame into a bounded queue; decoding proceeds behind it in strict arrival
// order. Reset is a generation bump, so a queued frame from the previous turn
// is discarded by the worker rather than being replayed into the next one.
type streamingASR struct {
	rec    *sherpa.OnlineRecognizer
	stream *sherpa.OnlineStream

	in   chan asrFrame
	stop chan struct{}
	wg   sync.WaitGroup

	generation      atomic.Uint64
	endpointEnabled atomic.Bool
	snapshot        atomic.Value // asrSnapshot
	dropped         atomic.Uint64
}

func newStreamingASR(rec *sherpa.OnlineRecognizer, stream *sherpa.OnlineStream, queueDepth int) *streamingASR {
	if rec == nil || stream == nil {
		return nil
	}
	if queueDepth <= 0 {
		queueDepth = 128
	}
	a := &streamingASR{
		rec:    rec,
		stream: stream,
		in:     make(chan asrFrame, queueDepth),
		stop:   make(chan struct{}),
	}
	a.snapshot.Store(asrSnapshot{})
	a.wg.Add(1)
	go a.run()
	return a
}

// Submit hands one capture frame to the decoder. It never blocks the audio
// path. If the decoder is behind, the oldest queued frame is discarded: the
// newest audio is the audio the user is speaking now.
func (a *streamingASR) Submit(samples []float32) {
	if a == nil || len(samples) == 0 {
		return
	}
	f := asrFrame{
		generation: a.generation.Load(),
		samples:    append([]float32(nil), samples...),
	}
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case a.in <- f:
			return
		default:
		}
		select {
		case <-a.in:
			a.dropped.Add(1)
		default:
		}
	}
	// The queue changed under us twice; count the frame rather than spin on
	// the audio path.
	a.dropped.Add(1)
}

// Reset starts a new utterance. The worker resets its recognizer stream when
// it sees the first frame of the new generation.
func (a *streamingASR) Reset() {
	if a == nil {
		return
	}
	gen := a.generation.Add(1)
	// Drop frames queued for the previous utterance. The worker still uses
	// the generation check for a frame it has already taken; this just keeps
	// the new turn from waiting behind a stale backlog.
	for {
		select {
		case <-a.in:
		default:
			a.snapshot.Store(asrSnapshot{Generation: gen})
			return
		}
	}
}

func (a *streamingASR) SetEndpointEnabled(enabled bool) {
	if a == nil {
		return
	}
	a.endpointEnabled.Store(enabled)
}

func (a *streamingASR) Snapshot() asrSnapshot {
	if a == nil {
		return asrSnapshot{}
	}
	s, _ := a.snapshot.Load().(asrSnapshot)
	return s
}

func (a *streamingASR) Generation() uint64 {
	if a == nil {
		return 0
	}
	return a.generation.Load()
}

func (a *streamingASR) Dropped() uint64 {
	if a == nil {
		return 0
	}
	return a.dropped.Load()
}

func (a *streamingASR) Stop() {
	if a == nil {
		return
	}
	select {
	case <-a.stop:
		return
	default:
		close(a.stop)
	}
	a.wg.Wait()
}

func (a *streamingASR) run() {
	defer a.wg.Done()

	var current uint64
	var ended bool
	for {
		select {
		case <-a.stop:
			return
		case f := <-a.in:
			if f.generation != current {
				a.rec.Reset(a.stream)
				current = f.generation
				ended = false
			}
			if ended {
				continue
			}

			a.stream.AcceptWaveform(16000, f.samples)
			for a.rec.IsReady(a.stream) {
				a.rec.Decode(a.stream)
			}
			text := strings.TrimSpace(a.rec.GetResult(a.stream).Text)
			snap := asrSnapshot{
				Generation: current,
				Text:       text,
			}
			// Match the synchronous endpoint guard: an empty stream may not
			// end a turn on rule1's silence alone.
			if text != "" && a.endpointEnabled.Load() && a.rec.IsEndpoint(a.stream) {
				snap.Endpoint = true
				ended = true
			}
			a.snapshot.Store(snap)
		}
	}
}
