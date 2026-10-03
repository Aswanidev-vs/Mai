package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/malgo"
)

const (
	// audioPeriodMillis is the device period requested on both capture and
	// playback, derived into frames per rate rather than hardcoded.
	//
	// It is set from measured properties of the rest of the front end, and it
	// is the *only* place that period is decided — everything downstream
	// quantises to it.
	//
	//	1. Barge-in. The gate in main.go is evaluated once per frame and holds
	//	   for bargeInSustain in wall-clock. With a P-frame period the decision
	//	   therefore lands at ceil(sustain/P)*P, so P is the quantisation error
	//	   on every interruption. 25 ms divides both tuned constants exactly —
	//	   250/25 = 10 frames, 700/25 = 28 — so neither has to move.
	//	2. Echo canceller. EchoCanceller.Process correlates a frame against the
	//	   newest L+n reference samples, so a frame that waits one period in the
	//	   queue asks the filter to model an echo path one period longer than
	//	   the real one. L is 4096 taps = 256 ms at 16 kHz; at audioQueueDepth 3
	//	   the lag budget is 3P = 75 ms, 29% of the window. Leaving the period
	//	   unset is worse: malgo's DefaultDeviceConfig leaves both period fields
	//	   at 0 with a default performance profile, which miniaudio resolves to
	//	   MA_DEFAULT_PERIOD_SIZE_IN_MILLISECONDS_CONSERVATIVE (100 ms), making
	//	   the lag budget 300 ms — wider than the filter's own tap window, which
	//	   is the condition main.go's queue-lag warning already reports.
	//	3. Speech enhancer. DPDFNet's hop is 160 samples = 10 ms at 16 kHz, and
	//	   the enhancer refreshes its speech verdict once per Run() call, i.e.
	//	   once per frame. At 25 ms that verdict is 2.5 hops old at worst, so
	//	   the gate never lags the waveform by more than a frame on top of a hop.
	//	4. Dropped audio. The pipeline drops the oldest frame when the queue
	//	   overflows, so one drop costs exactly one period. 25 ms is a quarter
	//	   of a 100 ms period and a fifth of a 64 ms one.
	//	5. Playback. The staging cap is playbackBufferSeconds (200 ms), which at
	//	   16 kHz and 44.1 kHz is 8 periods of slack at this setting, so the
	//	   ring is renderer-bound rather than cap-bound and the all-or-nothing
	//	   slice admission in push() never has to park the TTS producer.
	//
	// Total DSP work per second is unchanged by the period: the canceller's
	// O(L*n) filter and the O(L*n/decim) coherence scan are both linear in the
	// frame, so halving the frame size halves the per-callback cost and
	// doubles the callback rate. Only the granularity moves, which is the point.
	audioPeriodMillis = 25

	// minAudioPeriodFrames floors the derived period so a low or degenerate
	// sample rate cannot ask the backend for a period so small it would only
	// ever be clamped up. 32 frames is ~2 ms at 16 kHz.
	minAudioPeriodFrames = 32
)

// audioPeriodFrames converts audioPeriodMillis into frames for a device rate.
//
// Derived rather than hardcoded so capture (16 kHz) and playback (44.1 kHz for
// TTS, 16 kHz for the chime) all get the same wall-clock period: a hardcoded
// 400 would be 9 ms of speaker period at 44.1 kHz, which would ask the backend
// for a buffer eight times smaller than the one the AEC tap window assumes.
func audioPeriodFrames(sampleRate uint32) uint32 {
	if sampleRate == 0 {
		return 0 // let miniaudio pick; there is no rate to derive from
	}
	frames := (uint64(sampleRate)*audioPeriodMillis + 500) / 1000
	if frames < minAudioPeriodFrames {
		frames = minAudioPeriodFrames
	}
	return uint32(frames)
}

// audioCapture manages microphone input via miniaudio.
type audioCapture struct {
	ctx        *malgo.AllocatedContext
	device     *malgo.Device
	onSamples  func([]float32)
	sampleRate uint32
	channels   uint32

	// periodFrames is what this code asked for; observedPeriod is what the
	// backend actually granted. miniaudio treats the request as a hint and
	// silently substitutes on several paths (WASAPI re-derives it from
	// IAudioClient::GetBufferSize / periods), so a config value that is never
	// compared against the callback's frameCount is a claim, not a setting.
	periodFrames   uint32
	observedPeriod atomic.Uint32
}

// newAudioCapture initializes a microphone capture device.
func newAudioCapture(sampleRate uint32, channels uint32) *audioCapture {
	return &audioCapture{
		sampleRate:   sampleRate,
		channels:     channels,
		periodFrames: audioPeriodFrames(sampleRate),
	}
}

// RequestedPeriodFrames is the period passed to the backend.
func (c *audioCapture) RequestedPeriodFrames() uint32 { return c.periodFrames }

// ObservedPeriodFrames is the period the backend actually delivered, read from
// the first data callback's frameCount. Zero until capture has run once.
func (c *audioCapture) ObservedPeriodFrames() uint32 { return c.observedPeriod.Load() }

// PeriodHonoured reports whether the backend granted exactly the period that
// was asked for. Unknown (no callback yet) counts as honoured so a caller
// cannot mistake "not measured" for "rejected".
func (c *audioCapture) PeriodHonoured() bool {
	got := c.observedPeriod.Load()
	return got == 0 || got == c.periodFrames
}

// Start begins capturing audio from the microphone.
func (c *audioCapture) Start() error {
	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, func(message string) {
		fmt.Printf("[malgo] %s\n", message)
	})
	if err != nil {
		return fmt.Errorf("init context: %w", err)
	}
	c.ctx = ctx

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = c.channels
	deviceConfig.SampleRate = c.sampleRate
	deviceConfig.Alsa.NoMMap = 1
	// Explicit period, and explicitly NOT PeriodSizeInMilliseconds: miniaudio
	// gives frames priority (miniaudio.h: periodSizeInFrames "should take
	// priority"), and frames is the unit the queue depth, the tap window and
	// every frame length in this file are already counted in. Periods is left
	// at 0 so miniaudio keeps its default of 3, giving the backend a 75 ms
	// buffer to schedule against rather than a single bare period.
	deviceConfig.PeriodSizeInFrames = c.periodFrames

	callbacks := malgo.DeviceCallbacks{
		Data: c.onRecvFrames,
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, callbacks)
	if err != nil {
		ctx.Free()
		return fmt.Errorf("init device: %w", err)
	}
	c.device = device

	return device.Start()
}

// Stop halts audio capture.
func (c *audioCapture) Stop() error {
	if c.device != nil {
		return c.device.Stop()
	}
	return nil
}

// Close releases audio resources.
func (c *audioCapture) Close() {
	if c.device != nil {
		c.device.Uninit()
		c.device = nil
	}
	if c.ctx != nil {
		_ = c.ctx.Uninit()
		c.ctx.Free()
		c.ctx = nil
	}
}

var rawDataLogged = false

// onRecvFrames converts raw int16 bytes to float32 samples.
func (c *audioCapture) onRecvFrames(_, pSample []byte, frameCount uint32) {
	// frameCount is the backend's internal period, not the one that was
	// requested: miniaudio's own header calls the request "just a hint" and
	// says you "cannot assume you will get exactly what you ask for". With
	// noFixedSizedCallback unset the size is constant, so the first callback
	// settles it. Recording it here — on the device thread, atomically, with no
	// allocation — is what makes the configured period verifiable instead of
	// merely stated.
	c.observedPeriod.CompareAndSwap(0, frameCount)
	if !rawDataLogged && len(pSample) > 0 {
		fmt.Printf("\r[DEBUG] First raw audio packet received: %d bytes\n", len(pSample))
		rawDataLogged = true
	}
	if c.onSamples == nil {
		return
	}
	n := len(pSample) / 2
	samples := make([]float32, n)
	for i := 0; i < n; i++ {
		s16 := int16(pSample[2*i]) | int16(pSample[2*i+1])<<8
		samples[i] = float32(s16) / 32768.0
	}
	c.onSamples(samples)
}

// pcm16 converts one float sample to the 16-bit PCM the device plays.
//
// The clamping is not cosmetic: Go's int16 conversion WRAPS on overflow, so a
// sample above full scale used to come out as the opposite sign at full
// amplitude. A single such sample is a full-scale click, which is exactly what
// intermittent crackle in the output sounds like.
func pcm16(s float32) int16 {
	v := float64(s) * 32767.0
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

// writePCMFrame writes one sample into a stereo-interleaved-free mono frame.
func writePCMFrame(dst []byte, frame int, s float32) {
	v := pcm16(s)
	dst[frame*2] = byte(v & 0xFF)
	dst[frame*2+1] = byte(v >> 8) // arithmetic shift = correct two's complement high byte
}

// fillSilence writes zeros for frames [from, to). Every frame of the device
// buffer must be written on every callback: miniaudio hands back its internal
// buffer as-is, so leaving frames untouched re-plays a slice of the previous
// callback — heard as a click or a short burst of stale audio.
func fillSilence(dst []byte, from, to int) {
	for i := from; i < to; i++ {
		dst[i*2] = 0
		dst[i*2+1] = 0
	}
}

const (
	// playbackBufferSeconds caps how much rendered TTS may sit queued between
	// the generator and the speaker. Queued audio is pure latency: it is audio
	// the user hears *after* they have already started talking, which is the
	// exact failure barge-in exists to prevent. 200 ms rides out jitter between
	// rendering and the device while staying short enough that a cut-in is
	// heard immediately.
	playbackBufferSeconds = 0.20

	// minPlaybackBufferSamples keeps the cap above a whole device period, so
	// the renderer can always be served even if the producer has not run. With
	// the period now explicit (audioPeriodMillis) this floor only binds below
	// ~10 kHz: at 16 kHz the cap is 3200 samples over a 400-frame period, and
	// at 44.1 kHz it is 8820 over 1103 — eight periods of slack either way.
	minPlaybackBufferSamples = 2048
)

// sampleRing is the staging buffer between the TTS generator and the miniaudio
// playback callback: a drain goroutine pushes into it, the device thread renders
// out of it. It holds no device handle, so the flush and bounding rules below
// are exercisable without a sound card.
//
// The progress rules are all here, in one place, because they are what stops
// the two sides from deadlocking each other:
//
//   - push only ever parks with a full ring (unread >= limit >= 1), so a waiting
//     producer always implies the renderer has a sample available to consume.
//   - render signals after consuming, since consuming is the only thing that
//     frees space.
//   - both sides re-check their condition under the mutex, so a Signal landing
//     between the check and the Wait cannot be lost.
//
// abort is the escape hatch for the teardown paths: it wakes both sides at
// once, so the device is never left with a callback parked on the condition
// variable while it is being stopped.
type sampleRing struct {
	mu        sync.Mutex
	cond      *sync.Cond
	buf       []float32
	readIdx   int
	limit     int
	highWater int
	scratch   []float32 // reused per-callback copy of what was played
	closed    bool      // producer finished; a renderer must stop waiting for data
	aborted   bool      // playback abandoned; every waiter unblocks at once
}

// newSampleRing builds a ring capped at playbackBufferSeconds of audio,
// converted from the device rate rather than hardcoded so a 16 kHz and a
// 44.1 kHz device are both bounded at the same wall-clock latency.
func newSampleRing(sampleRate int) *sampleRing {
	limit := int(playbackBufferSeconds * float64(sampleRate))
	if limit < minPlaybackBufferSamples {
		limit = minPlaybackBufferSamples
	}
	r := &sampleRing{limit: limit}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// unread reports how many queued samples have not been played yet.
func (r *sampleRing) unread() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.buf) - r.readIdx
}

// highWaterMark reports the deepest the queue has ever been, in samples. Used
// for logging and tests: a value well under limit means the renderer is the
// bottleneck, one pinned at limit means the cap is throttling the generator.
func (r *sampleRing) highWaterMark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.highWater
}

// push queues chunk and returns false once the ring has been aborted or closed.
//
// The chunk is admitted in slices rather than all-or-nothing. Upstream TTS
// chunks are ~200 ms themselves, so refusing anything that does not fit the
// cap whole would park the producer whenever anything at all was queued, and
// the device would underrun on nearly every period. Slicing keeps the backlog
// hard-bounded at the cap while guaranteeing the producer can always make
// progress — which is also what keeps render from ever waiting on a producer
// that is unable to run.
func (r *sampleRing) push(chunk []float32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for off := 0; off < len(chunk); {
		if r.aborted || r.closed {
			return false
		}
		free := r.limit - (len(r.buf) - r.readIdx)
		if free <= 0 {
			r.cond.Wait()
			continue
		}
		take := min(free, len(chunk)-off)
		r.buf = append(r.buf, chunk[off:off+take]...)
		off += take
		if unread := len(r.buf) - r.readIdx; unread > r.highWater {
			r.highWater = unread
		}
		r.cond.Signal() // Wake a renderer parked on an empty ring.
	}
	return !r.aborted
}

// render copies the next frames of queued audio into dst as 16-bit PCM,
// blocking on the condition variable while the ring is empty but the producer
// is still running. onPlayed, when non-nil, receives exactly the samples that
// were written, under the lock. It returns the number of frames written; the
// caller owns the rest of dst and must fill it with silence.
func (r *sampleRing) render(dst []byte, frames int, onPlayed func([]float32)) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	if onPlayed != nil && cap(r.scratch) < frames {
		r.scratch = make([]float32, frames) // grown once per device period
	}
	written := 0
	for written < frames {
		if r.readIdx >= len(r.buf) {
			if r.closed || r.aborted {
				break
			}
			r.cond.Wait()
			continue
		}
		s := r.buf[r.readIdx]
		writePCMFrame(dst, written, s)
		if onPlayed != nil {
			// Copied as it is written rather than sliced out of the ring
			// afterwards: this callback can be parked on the condition
			// variable holding frames the device already has, and a barge-in
			// flush reclaims the ring underneath it. Those frames were really
			// played, so the echo reference still has to see them.
			r.scratch[written] = s
		}
		r.readIdx++
		written++
	}
	if written > 0 {
		if onPlayed != nil {
			onPlayed(r.scratch[:written])
		}
		// Consuming is what frees backlog, so the producer has to be woken by
		// the read rather than by the write it is waiting on.
		r.cond.Signal()
	}
	r.compact()
	return written
}

// compact drops consumed samples so the backing array does not creep forward
// over a long reply.
func (r *sampleRing) compact() {
	switch {
	case r.readIdx == len(r.buf):
		r.buf = r.buf[:0]
		r.readIdx = 0
	case r.readIdx >= 4096:
		r.buf = append(r.buf[:0], r.buf[r.readIdx:]...)
		r.readIdx = 0
	}
}

// abort discards every queued-but-unplayed sample and tears the ring down,
// returning how many were dropped. This is the barge-in path: audio that has
// not reached the speaker must never reach it, however much of it is queued,
// because the queue is exactly what makes the assistant keep talking after the
// user cut in.
//
// onFlushed, when non-nil, runs with the ring lock still held and receives that
// count, so a caller whose echo-reference pipeline is fed from render can mark
// the discontinuity without racing the device thread.
func (r *sampleRing) abort(onFlushed func(dropped int)) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	dropped := r.discardLocked()
	r.aborted = true
	r.cond.Broadcast()
	if onFlushed != nil && dropped > 0 {
		onFlushed(dropped)
	}
	return dropped
}

func (r *sampleRing) discardLocked() int {
	dropped := len(r.buf) - r.readIdx
	r.buf = r.buf[:0]
	r.readIdx = 0
	return dropped
}

// finish marks the producer side complete, releasing a renderer parked on an
// empty ring so it can silence the rest of its period and return instead of
// waiting for audio that will never come.
func (r *sampleRing) finish() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	r.cond.Broadcast()
}

// markFlushedSilence advances the echo reference past audio the speaker never
// emitted, so a barge-in flush cannot leave the canceller chasing a queue that
// was just thrown away.
//
// The discarded samples themselves must NOT be pushed: they never reached the
// speaker, so feeding them in would ask the AEC to cancel a signal that does not
// exist in the room. What belongs in the reference is the silence that really
// did come out, because the reference's job is to be a sample-accurate record of
// the speaker's output. Advancing it by exactly the skipped count keeps the
// timeline aligned with the room; leaving it stale would put the next TTS frame
// that many samples out of phase with what the microphone is hearing. Routing
// the silence through the same resampler both converts the count to the 16 kHz
// reference rate and clears the interpolation history, so the first sample after
// a flush is not shaped against waveform the speaker no longer holds.
//
// Callers must hold the renderer lock (sampleRing.abort does) so this cannot
// race the device thread's use of the same resampler.
func markFlushedSilence(ref *speakerRef, rs *resampler, dropped int) {
	if dropped <= 0 {
		return
	}
	ref.Push(rs.resample(make([]float32, dropped)))
}

// playAudioStreaming plays TTS chunks as they arrive on a channel.
// The generator function is called in a goroutine and should send
// float32 sample slices into the returned channel, then close it when done.
// Returns once the channel is closed and all buffered audio has been played —
// or, on barge-in, as soon as the queued audio has been flushed away.
func playAudioStreaming(ctx context.Context, sampleRate int, stop *int32, generate func(ch chan<- []float32)) error {
	if ctx == nil {
		ctx = context.Background() // some callers pass nil to mean "never cancel"
	}
	ch := make(chan []float32, 64)

	// Run the TTS generator in a goroutine.
	go func() {
		defer close(ch)
		generate(ch)
	}()

	devCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	defer devCtx.Free()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(sampleRate)
	deviceConfig.PeriodSizeInFrames = audioPeriodFrames(uint32(sampleRate))

	ring := newSampleRing(sampleRate)
	rs := newResampler(sampleRate, 16000) // Reference ring must match the 16k mic rate.

	stopped := func() bool { return stop != nil && atomic.LoadInt32(stop) != 0 }

	// Every exit path aborts first. Uninit stops the device, and the device
	// cannot stop while its callback is parked waiting for audio that no longer
	// has a producer.
	defer ring.abort(nil)

	// Drain channel into the ring. This starts before the device so the very
	// first callback can never block against a ring with nobody feeding it.
	// After barge-in it keeps consuming instead of returning, because leaving
	// the channel full would park the generator goroutine on ch <- chunk
	// forever; its chunks are simply dropped.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for chunk := range ch {
			if stopped() || !ring.push(chunk) {
				continue
			}
		}
		ring.finish()
	}()

	onSamples := func(pOutputSample, _ []byte, frameCount uint32) {
		n := int(frameCount)
		if stopped() {
			fillSilence(pOutputSample, 0, n)
			return
		}
		// Feed exactly the samples sent to the speaker into the echo reference.
		written := ring.render(pOutputSample, n, func(played []float32) {
			refBuffer.Push(rs.resample(played))
		})
		// Underrun, barge-in or end of stream: the frames this callback found no
		// audio for are still played by the device, so they must be silence
		// rather than whatever the previous callback left in the buffer.
		fillSilence(pOutputSample, written, n)
	}

	device, err := malgo.InitDevice(devCtx.Context, deviceConfig, malgo.DeviceCallbacks{Data: onSamples})
	if err != nil {
		return err
	}
	// Ordering matters and defers run LIFO, so these two are registered in
	// reverse of the order they must execute: the ring is torn down first so a
	// device callback parked on its condition variable is released, and only
	// then is the device uninited. Uninit waits on that callback to return, so
	// uniniting first would block until TTS happened to produce a chunk or
	// close the channel. abort is idempotent and takes a nil callback here, so
	// the earlier defer above running last costs nothing.
	defer device.Uninit()
	defer ring.abort(nil)

	if err := device.Start(); err != nil {
		return err
	}

	var bargeInOnce sync.Once
	flushBargeIn := func() {
		bargeInOnce.Do(func() {
			// Abort first: the reference is marked and the device is stopped
			// only once nothing can still hand more queued audio to the speaker.
			dropped := ring.abort(func(dropped int) {
				markFlushedSilence(refBuffer, rs, dropped)
			})
			log.Printf("[BARGE-IN] Playback flushed: dropped %d queued samples (%.1f ms), peak backlog %d samples.",
				dropped, float64(dropped)/float64(sampleRate)*1000, ring.highWaterMark())
			// From here the callback emits silence on its own, but stopping the
			// device as well means no already-committed backend buffer can still
			// reach the speakers after the queue is gone. Reopening is not
			// needed: this call owns the context and device for its whole
			// lifetime and the next utterance opens a fresh pair, so there is
			// no session for a flush to resume into.
			_ = device.Stop()
		})
	}

	// Watch the barge-in flag so the flush lands the moment the user cuts in,
	// rather than whenever the generator happens to notice next. The flag is a
	// plain atomic set by the caller, so this has to poll; a few milliseconds
	// here is inaudible because the device callback already bails to silence on
	// the same flag.
	watchStop := make(chan struct{})
	watchDone := make(chan struct{})
	defer func() {
		close(watchStop)
		<-watchDone // never Uninit the device from under a running watcher
	}()
	go func() {
		defer close(watchDone)
		ticker := time.NewTicker(5 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watchStop:
				return
			case <-ticker.C:
				if stopped() {
					flushBargeIn()
					return
				}
			}
		}
	}()

	// Wait for generation to finish.
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}

	// Play out what is still queued. Barge-in needs no special path here: the
	// flush empties the ring, so this loop simply stops. The stopped() check
	// covers the case where the generator finished before the watcher ticked.
	for ring.unread() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if stopped() {
			flushBargeIn()
			continue
		}
		time.Sleep(5 * time.Millisecond)
	}

	return nil
}

// playAudio plays float32 samples through the default output device.
// It can be interrupted midway via ctx cancellation or the stop flag.
func playAudio(ctx context.Context, samples []float32, sampleRate int, stop *int32) error {
	devCtx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return err
	}
	defer devCtx.Free()

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Playback)
	deviceConfig.Playback.Format = malgo.FormatS16
	deviceConfig.Playback.Channels = 1
	deviceConfig.SampleRate = uint32(sampleRate)
	deviceConfig.PeriodSizeInFrames = audioPeriodFrames(uint32(sampleRate))

	var rs *resampler
	if sampleRate != 16000 {
		rs = newResampler(sampleRate, 16000)
	}

	var playbackIndex int
	onSamples := func(pOutputSample, _ []byte, frameCount uint32) {
		n := int(frameCount)
		if stop != nil && atomic.LoadInt32(stop) != 0 {
			fillSilence(pOutputSample, 0, n)
			return // Stop playback immediately on barge-in
		}
		start := playbackIndex
		written := 0
		for written < n {
			if playbackIndex >= len(samples) {
				break
			}
			writePCMFrame(pOutputSample, written, samples[playbackIndex])
			playbackIndex++
			written++
		}
		// End of buffer: the remaining frames must be silence, not the previous
		// callback's contents.
		fillSilence(pOutputSample, written, n)
		// Chime also leaves the speaker, so include it in the echo reference.
		if playbackIndex > start {
			played := samples[start:playbackIndex]
			if rs != nil {
				refBuffer.Push(rs.resample(played))
			} else {
				refBuffer.Push(played)
			}
		}
	}

	device, err := malgo.InitDevice(devCtx.Context, deviceConfig, malgo.DeviceCallbacks{Data: onSamples})
	if err != nil {
		return err
	}
	defer device.Uninit()

	if err := device.Start(); err != nil {
		return err
	}

	// Poll loop with context awareness and stop flag support
	for playbackIndex < len(samples) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if stop != nil && atomic.LoadInt32(stop) != 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}

	return nil
}

// generateThinkingChime creates a short 440Hz sine wave tone for the thinking indicator.
// Duration ~80ms, amplitude ~-12dB (0.25) to be subtle.
func generateThinkingChime(sampleRate int) []float32 {
	duration := 0.08 // 80ms
	numSamples := int(float64(sampleRate) * duration)
	samples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		// Sine wave at 440Hz with a quick fade-out envelope
		envelope := 1.0 - float64(i)/float64(numSamples)
		samples[i] = float32(0.25 * math.Sin(2*math.Pi*440*t) * envelope)
	}
	return samples
}
