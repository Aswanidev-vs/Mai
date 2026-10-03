package main

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover sampleRing, the staging buffer that playAudioStreaming
// feeds from the TTS generator and drains on the miniaudio device thread. The
// type holds no device handle on purpose, so the flush and the backlog bound
// are exercised without a sound card.

func tone(n int, amp float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = amp
	}
	return out
}

// poisoned returns a device-sized output buffer pre-filled with a non-zero
// pattern, so a test can tell "render wrote silence" apart from "render wrote
// nothing and left whatever was already there" — the caller is the one that
// owns silence (see fillSilence).
func poisoned(frames int) []byte {
	buf := make([]byte, frames*2)
	for i := range buf {
		buf[i] = 0xFF
	}
	return buf
}

func TestAudioBuffer_BacklogCapIsTwoHundredMilliseconds(t *testing.T) {
	for _, tc := range []struct {
		sampleRate int
		want       int
	}{
		{16000, 3200}, // 200 ms
		{44100, 8820}, // 200 ms
		{48000, 9600}, // 200 ms
		{8000, 2048},  // below the floor: clamped to one sane device window
	} {
		ring := newSampleRing(tc.sampleRate)
		assert.Equal(t, tc.want, ring.limit, "sample rate %d", tc.sampleRate)
	}
}

func TestAudioBuffer_FlushDiscardsQueuedAudio(t *testing.T) {
	ring := newSampleRing(16000)
	require.True(t, ring.push(tone(1000, 0.8)))

	// Nothing has been played, so all 1000 samples are queued audio.
	assert.Equal(t, 1000, ring.abort(nil))
	assert.Equal(t, 0, ring.unread())
}

func TestAudioBuffer_FlushCountsOnlyUnplayedSamples(t *testing.T) {
	ring := newSampleRing(16000)

	// Already-consumed samples are not counted as discarded: they reached the
	// speaker, so they are history, not a queue.
	require.True(t, ring.push(tone(1000, 0.8)))
	dst := make([]byte, 400*2)
	require.Equal(t, 400, ring.render(dst, 400, nil))

	assert.Equal(t, 600, ring.abort(nil))
	assert.Equal(t, 0, ring.unread())
}

func TestAudioBuffer_FlushedAudioNeverReachesTheSpeaker(t *testing.T) {
	ring := newSampleRing(16000)
	const period = 256
	require.True(t, ring.push(tone(4*period, 0.9)))

	// A barge-in mid-stream: one period plays, then the queue is thrown away.
	dst := make([]byte, period*2)
	require.Equal(t, period, ring.render(dst, period, nil))
	assert.Equal(t, 3*period, ring.abort(nil))

	// The device still plays this period, and every frame of it must now be
	// silence: the discarded audio is gone, and the frames the renderer found
	// no audio for belong to the caller to silence (see fillSilence).
	dst = poisoned(period)
	assert.Equal(t, 0, ring.render(dst, period, nil))
	fillSilence(dst, 0, period)
	for i := 0; i < period*2; i++ {
		require.Zero(t, dst[i], "frame %d leaked discarded audio", i/2)
	}
}

func TestAudioBuffer_RenderReportsExactlyWhatItPlayed(t *testing.T) {
	ring := newSampleRing(16000)
	audio := []float32{0.5, -0.5, 0.25, -0.25, 1, -1}
	require.True(t, ring.push(audio))

	var played []float32
	dst := make([]byte, len(audio)*2)
	require.Equal(t, len(audio), ring.render(dst, len(audio), func(s []float32) {
		played = append([]float32(nil), s...)
	}))

	// The echo reference must describe the speaker's output exactly: anything
	// else and the AEC is cancelling a signal that never existed.
	assert.Equal(t, audio, played)
}

func TestAudioBuffer_FastProducerCannotOutrunRenderer(t *testing.T) {
	const (
		sampleRate = 16000
		frames     = 512
		chunkLen   = 1600
		chunks     = 200 // 20 s of audio offered into a 200 ms queue
	)
	ring := newSampleRing(sampleRate)
	defer ring.abort(nil) // guarantees the producer can never outlive the test

	produced := make(chan bool, 1)
	go func() {
		audio := tone(chunkLen, 0.3)
		ok := true
		for i := 0; i < chunks && ok; i++ {
			ok = ring.push(audio)
		}
		produced <- ok
	}()

	dst := make([]byte, frames*2)
	drained := make(chan int, 1)
	go func() {
		total := 0
		for total < chunks*chunkLen {
			ring.render(dst, frames, nil)
			total += frames
		}
		drained <- total
	}()

	select {
	case total := <-drained:
		assert.GreaterOrEqual(t, total, chunks*chunkLen)
	case <-time.After(20 * time.Second):
		t.Fatal("bounded queue deadlocked the renderer against the producer")
	}
	select {
	case ok := <-produced:
		assert.True(t, ok, "push reported the ring was aborted")
	case <-time.After(20 * time.Second):
		t.Fatal("bounded queue deadlocked the producer against the renderer")
	}

	// The whole point of the bound: 20 s of audio never sat in a 200 ms queue.
	assert.LessOrEqual(t, ring.highWaterMark(), ring.limit)
	assert.Greater(t, ring.highWaterMark(), 0)
}

func TestAudioBuffer_AbortUnblocksParkedProducer(t *testing.T) {
	ring := newSampleRing(16000)

	// Fill the queue completely: the next push has to park.
	require.True(t, ring.push(tone(ring.limit, 0.5)))
	parked := make(chan bool, 1)
	go func() { parked <- ring.push(tone(500, 0.5)) }()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, ring.limit, ring.unread(), "producer should still be parked")

	marked := -1
	dropped := ring.abort(func(d int) { marked = d })
	assert.Equal(t, ring.limit, dropped)
	assert.Equal(t, ring.limit, marked, "the flush hook sees the dropped count")

	select {
	case ok := <-parked:
		assert.False(t, ok, "push must report failure once aborted")
	case <-time.After(2 * time.Second):
		t.Fatal("abort left the producer parked on the condition variable")
	}
	assert.Equal(t, 0, ring.unread())
}

func TestAudioBuffer_FinishUnblocksParkedRenderer(t *testing.T) {
	ring := newSampleRing(16000)
	const period = 256
	dst := poisoned(period)

	written := make(chan int, 1)
	go func() { written <- ring.render(dst, period, nil) }()
	time.Sleep(50 * time.Millisecond)

	// The generator finished with nothing left to give, so a renderer waiting
	// for audio must be released instead of blocking the device thread forever.
	ring.finish()
	select {
	case n := <-written:
		assert.Equal(t, 0, n, "renderer should have written no frames")
	case <-time.After(2 * time.Second):
		t.Fatal("finish left the renderer parked on the condition variable")
	}
	for i := range dst {
		require.Equal(t, byte(0xFF), dst[i], "render must leave untouched frames to the caller")
	}
}

func TestAudioBuffer_AbortedRendererReturnsSilenceImmediately(t *testing.T) {
	ring := newSampleRing(16000)
	require.True(t, ring.push(tone(2000, 0.9)))
	ring.abort(nil)

	const period = 256
	dst := poisoned(period)
	done := make(chan int, 1)
	go func() { done <- ring.render(dst, period, nil) }()

	select {
	case n := <-done:
		assert.Equal(t, 0, n)
	case <-time.After(2 * time.Second):
		t.Fatal("an aborted ring must never block the device thread")
	}
}

func TestAudioBuffer_ConcurrentPushRenderFlushIsRaceFree(t *testing.T) {
	ring := newSampleRing(44100)
	defer ring.abort(nil)

	audio := tone(1024, 0.4)
	var wg sync.WaitGroup

	wg.Add(2)
	go func() { // producer, bounded by the cap
		defer wg.Done()
		for i := 0; i < 500; i++ {
			if !ring.push(audio) {
				return
			}
		}
	}()
	go func() { // device thread stand-in
		defer wg.Done()
		dst := make([]byte, 480*2)
		for i := 0; i < 1000; i++ {
			written := ring.render(dst, 480, func(played []float32) {
				_ = played
			})
			fillSilence(dst, written, 480)
		}
		ring.finish()
	}()

	time.Sleep(20 * time.Millisecond)
	ring.abort(nil) // barge-in lands in the middle of both sides
	wg.Wait()
}

// TestMarkFlushedSilence covers the echo-reference half of the flush, in
// production shape.
//
// The resampler here has already streamed the whole utterance, so its
// interpolation history is full of real speaker samples — which is what a
// barge-in always finds, since the flush happens mid-playback. This test used
// to hand the flush a brand-new resampler instead, whose history was empty, and
// so passed while the flushed window opened with a Catmull-Rom tail of the
// audio that had just been discarded.
func TestAudioBuffer_MarkFlushedSilence_MarksSilenceNotDiscardedAudio(t *testing.T) {
	const (
		sampleRate = 44100
		refRate    = 16000
		queued     = 8820 // exactly one 200 ms queue at 44.1 kHz
	)

	ref := newSpeakerRef(4 * refRate)
	ref.Clear()
	rs := newResampler(sampleRate, refRate)

	// What the speaker really emitted before the cut-in, streamed through the
	// same resampler the flush will use.
	spoken := rs.resample(tone(sampleRate, 0.7))
	require.NotEmpty(t, spoken)
	ref.Push(spoken)

	markFlushedSilence(ref, rs, queued)

	// Ask for more than the ring holds since Clear, so the window certainly
	// covers the whole turn; recent() hands back the newest samples first, so
	// whatever ref.Clear left behind sits harmlessly in front.
	ring := ref.recent(len(spoken) + queued)

	// The speaker's audio is a constant 0.7 and the flush is exact zeros, so the
	// boundary between them is unambiguous.
	start := 0
	for start < len(ring) && ring[start] == 0 {
		start++
	}
	require.Less(t, start+len(spoken), len(ring), "the speaker audio is missing from the reference")
	flushed := ring[start+len(spoken):]

	// The reference advanced by the skipped duration, expressed in reference
	// rate samples. (Measured rather than assumed: the resampler's exact output
	// length is aec.go's business, not this test's.)
	assert.InDelta(t, queued*refRate/sampleRate, len(flushed), 4,
		"the flush must advance the reference by the skipped duration")

	// Everything the flush skipped reads as silence — including its very first
	// sample, which is the one that used to carry a spline tail of the audio
	// before it. The discarded queue was never spoken, so neither that nor any
	// remnant of the last thing that was may be there for the AEC to cancel.
	for i, s := range flushed {
		require.Zero(t, s, "flushed reference sample %d should be silence, not discarded audio", i)
	}

	// The audio the speaker really did emit survives, still in phase.
	assert.Equal(t, spoken, ring[start:start+len(spoken)],
		"the pre-flush speaker audio must stay in the reference, in phase")
}

func TestAudioBuffer_MarkFlushedSilence_IgnoresEmptyFlush(t *testing.T) {
	ref := newSpeakerRef(4 * 16000)
	ref.Clear()
	spoken := tone(64, 0.7)
	ref.Push(spoken)

	rs := newResampler(44100, 16000)
	markFlushedSilence(ref, rs, 0)
	markFlushedSilence(ref, rs, -5)

	assert.Equal(t, spoken, ref.recent(len(spoken)), "nothing dropped means no timeline shift")
}
