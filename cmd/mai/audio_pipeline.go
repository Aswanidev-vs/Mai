package main

import (
	"log"
	"sync"
	"sync/atomic"
	"time"
)

// micFrame is one captured frame on its way to the processing goroutine.
//
// fromBrowser rides along because the browser mic is a different source, not a
// different destination: it skips the wake-word state and goes straight to
// listening. Keeping the flag on the frame rather than on the queue is what lets
// both sources share one stream and therefore one AEC/VAD/ASR — a second queue
// would be a second, unordered view of the same microphone.
type micFrame struct {
	samples     []float32
	fromBrowser bool
}

const (
	// audioQueueDepth bounds how far the processing goroutine may fall behind,
	// counted in frames so the bound follows whatever period size the capture
	// backend picked rather than assuming one.
	//
	// Three frames is a deliberate trade against the echo canceller, not an
	// arbitrary number. EchoCanceller.Process correlates a mic frame against
	// the *newest* L+n samples of the speaker reference, so a frame that sits
	// in the queue for δ while playback keeps advancing asks the adaptive filter
	// to model an echo path δ longer than the real one. With L = 4096 taps
	// (256 ms at 16 kHz) and a capture period in the tens of milliseconds, three
	// frames of lag stays a small fraction of the tap window; a much deeper
	// queue would push the modelled path past the window and stop the filter
	// converging. Jitter rides out on the same budget: a depth of one would turn
	// every scheduling hiccup in the consumer into a dropped frame.
	audioQueueDepth = 3

	// audioDropLogInterval rate-limits the sustained-drop warning. Drops are
	// only interesting while they keep happening — one dropped frame during a
	// 300 ms ASR decode is the design working, a continuous stream of them
	// means the consumer cannot keep up at all.
	audioDropLogInterval = 5 * time.Second

	// audioDropWarnMin is how many frames must be lost within one
	// audioDropLogInterval window before the loss counts as sustained. At a
	// 16 kHz period of ~25 ms a window carries ~200 frames, so this is a ~2.5%
	// loss floor. Anything below it is scheduler jitter — the queue absorbing a
	// single long frame — which is the design working, not a stall. Without a
	// floor, one unlucky decode every few seconds re-arms the warning forever
	// and the log reads as a permanent failure while capture is losing well
	// under 1% of the microphone.
	audioDropWarnMin = 5
)

// audioPipeline is the staging ring between the capture callback and the single
// goroutine that owns the audio front end.
//
// Before it existed, every piece of stateful work ran inline on the miniaudio
// thread: RMS, the noise gate, a 4096-tap NLMS echo canceller, an ~1.6M
// multiply-add coherence scan, the VAD and an ASR decode loop that can take
// 300 ms on its own. Any one of those stalling meant the microphone missed
// audio outright — and playback is exactly when the mic most needs to stay
// alive, because that is when barge-in has to be detected. A 300 ms stall during
// playback is a 300 ms window in which a user cannot cut in.
//
// The queue moves that work off the device thread and keeps two properties the
// stateful components depend on:
//
//   - the capture callback never blocks. It is a bounded channel with
//     drop-oldest on overflow, so backpressure can never reach miniaudio; the
//     cost of falling behind is counted instead (Dropped), which is what makes
//     "input never stalls" measurable rather than assumed.
//   - the processing side is strictly serial. Exactly one goroutine drains the
//     channel, so the AEC weights, the speech enhancer, the VAD and both ASR
//     streams still see one strictly ordered sample timeline. Sharding or
//     parallelising this would silently break all four of them at once.
//
// Neither half of that is negotiable: the callback must not wait, and the
// consumers must not race.
type audioPipeline struct {
	frames  chan micFrame
	process func(micFrame)

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	closed atomic.Bool
	// dropped counts frames discarded because the processing goroutine could
	// not take them in time. It is the keystone's own health metric: at zero,
	// capture has never been stalled by audio processing.
	dropped atomic.Int64
	warned  atomic.Int64 // total as of the last sustained-drop warning
	warnAt  atomic.Int64 // UnixNano of the last warning, for rate limiting
}

// newAudioPipeline starts the single processing goroutine. process is called
// once per frame, in arrival order, never concurrently with itself.
func newAudioPipeline(process func(micFrame)) *audioPipeline {
	p := &audioPipeline{
		frames:  make(chan micFrame, audioQueueDepth),
		process: process,
		stop:    make(chan struct{}),
	}
	p.wg.Add(1)
	go p.run()
	return p
}

// push hands a frame to the processing goroutine. It never blocks and never
// allocates: the caller already owns a slice that nothing else will read, and
// the callback path cannot afford to wait for a slot.
//
// On overflow the *oldest* frame goes, not the newest. A queued mic frame is
// audio the user has already finished speaking; a frame still being captured is
// the one that carries the words that are happening now, and barge-in lives
// entirely in "right now".
func (p *audioPipeline) push(f micFrame) {
	if p == nil || p.closed.Load() {
		return
	}
	select {
	case p.frames <- f:
		return
	default:
	}

	// Full: make room by discarding the oldest, then retry the send.
	select {
	case <-p.frames:
		p.dropped.Add(1)
	default:
		// The consumer drained it between the two selects, so nothing was
		// actually lost and the retry below will fit.
	}
	select {
	case p.frames <- f:
	default:
		// Only reachable if the slot we freed was taken by something else
		// between the two selects. Count the loss rather than spin: the
		// callback must return.
		p.dropped.Add(1)
	}
}

// run drains the queue on the one goroutine that is allowed to touch the audio
// front end.
func (p *audioPipeline) run() {
	defer p.wg.Done()
	for {
		select {
		case <-p.stop:
			return
		case f := <-p.frames:
			p.process(f)
			p.warnIfSustained()
		}
	}
}

// warnIfSustained logs once per interval while frames are being lost at a rate
// that means something. A permanently full queue means the processing goroutine
// cannot keep up, and silently dropping the user's microphone is not an
// acceptable default — the drop counter is the evidence, and someone has to read
// it.
//
// The baseline rolls every interval whether or not anything is logged, so the
// threshold measures the loss *rate* rather than the session total. Counting
// cumulatively would let a slow drip of isolated frames eventually cross any
// fixed line and then report itself as a permanent stall, which is the same
// false alarm the floor exists to remove.
func (p *audioPipeline) warnIfSustained() {
	total := p.dropped.Load()
	if total == p.warned.Load() {
		return
	}
	if !rateLimit(&p.warnAt, audioDropLogInterval) {
		return
	}
	lost := total - p.warned.Swap(total)
	if lost < audioDropWarnMin {
		return
	}
	log.Printf("[AUDIO-PIPELINE] Capture queue overflowing: %d frames dropped (%d in the last %v — about %.1f frames/sec at a 25 ms period) — audio processing is not keeping up with the mic.",
		total, lost, audioDropLogInterval, float64(lost)/audioDropLogInterval.Seconds())
}

// Stop ends the pipeline and waits for the processing goroutine to finish the
// frame it is on. Queued frames are discarded rather than processed: a frame
// captured after the decision to exit cannot influence a turn that will never
// be spoken, and decoding it would only delay the exit.
//
// It must be called after the capture device has stopped (so nothing new
// arrives) and before anything the processing goroutine can reach into is
// closed.
func (p *audioPipeline) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		p.closed.Store(true)
		close(p.stop)
	})
	p.wg.Wait()
}

// Dropped reports how many frames were discarded because the processing
// goroutine fell behind. Zero means capture has never been stalled by audio
// processing, which is the whole point of the queue.
func (p *audioPipeline) Dropped() int64 {
	if p == nil {
		return 0
	}
	return p.dropped.Load()
}
