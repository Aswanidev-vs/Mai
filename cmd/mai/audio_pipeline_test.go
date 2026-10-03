package main

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The pipeline is the keystone of the full-duplex refactor: the capture thread
// must never block, and the consumers must see one strictly serial stream.
// These tests pin both halves, and the AEC test settles the question the
// refactor has to answer — what losing a mic frame does to echo cancellation.

// blockingPipeline returns a pipeline whose consumer is parked inside process,
// which is the state a slow ASR decode puts it in, plus the channels that
// release it. The caller must push one frame before waiting on entered.
func blockingPipeline() (*audioPipeline, <-chan struct{}, func()) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	p := newAudioPipeline(func(micFrame) {
		once.Do(func() { close(entered) })
		<-release
	})
	return p, entered, func() { close(release); p.Stop() }
}

// TestAudioPipeline_NeverReordersSurvivingFrames is the ordering contract.
// A burst far faster than the mic can produce overflows the queue and sheds
// frames — that is correct — but whatever survives must arrive in strictly
// ascending order, with nothing lost that was not counted: no frame may be
// delivered out of order or twice, because the AEC weights, the speech
// enhancer, the VAD and both ASR streams all depend on seeing one strictly
// ordered timeline.
//
// Ascending rather than contiguous: when the producer runs on past the consumer
// between two dequeues, drop-oldest removes a frame from the middle of what the
// consumer would otherwise have seen next, so a gap is normal. The newest frame
// is always delivered.
func TestAudioPipeline_NeverReordersSurvivingFrames(t *testing.T) {
	const frames = 256

	var mu sync.Mutex
	var got []int32
	p := newAudioPipeline(func(f micFrame) {
		mu.Lock()
		got = append(got, int32(f.samples[0]))
		mu.Unlock()
	})
	defer p.Stop()

	for i := 0; i < frames; i++ {
		p.push(micFrame{samples: []float32{float32(i)}})
	}

	// Wait for the pipeline to settle: no more frames are coming, so the count
	// stops growing.
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if int64(n)+p.Dropped() == int64(frames) || time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	require.NotEmpty(t, got)
	for i := 1; i < len(got); i++ {
		assert.Greater(t, got[i], got[i-1],
			"frame %d was delivered after %d — the stream must stay strictly ordered", got[i], got[i-1])
	}
	assert.Equal(t, int32(frames-1), got[len(got)-1], "the newest frame must always be delivered")
}

// TestAudioPipeline_PacedCaptureLosesNothing is the real operating condition:
// a producer at the mic's own rate never overflows a consumer that keeps up.
// Each frame is acknowledged before the next is offered, exactly as a device
// callback that sleeps for a period between bursts behaves.
func TestAudioPipeline_PacedCaptureLosesNothing(t *testing.T) {
	const frames = 64

	acks := make(chan struct{}, frames)
	var mu sync.Mutex
	var got []int32
	p := newAudioPipeline(func(f micFrame) {
		mu.Lock()
		got = append(got, int32(f.samples[0]))
		mu.Unlock()
		acks <- struct{}{}
	})
	defer p.Stop()

	for i := 0; i < frames; i++ {
		p.push(micFrame{samples: []float32{float32(i)}})
		<-acks
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, frames)
	for i, v := range got {
		assert.Equal(t, int32(i), v, "frame %d arrived out of order", i)
	}
	assert.Equal(t, int64(0), p.Dropped(), "a paced producer must not lose a single frame")
}

// TestAudioPipeline_CallbackNeverBlocks is the property the whole refactor
// exists for: whatever the processing goroutine is doing, push returns.
func TestAudioPipeline_CallbackNeverBlocks(t *testing.T) {
	p, entered, unblock := blockingPipeline()
	defer unblock()

	p.push(micFrame{samples: []float32{1}}) // the frame the consumer parks on
	<-entered

	// Overflow the queue while the consumer is stuck, then time it.
	start := time.Now()
	for i := 0; i < audioQueueDepth*20; i++ {
		p.push(micFrame{samples: []float32{2}})
	}
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 250*time.Millisecond,
		"push must never wait for the consumer; %d overflowing pushes took %v", audioQueueDepth*20, elapsed)
	assert.Greater(t, p.Dropped(), int64(0),
		"a parked consumer must shed frames rather than grow the queue")
}

// TestAudioPipeline_DropsOldestNotNewest pins which end of the queue is
// discarded. Barge-in lives entirely in "right now": the newest frame is the
// one carrying the words being spoken at this instant, so it must be the one
// that always survives.
//
// The consumer stays parked inside process() for the duration, so the queue is
// stable and draining it below cannot race the pipeline.
func TestAudioPipeline_DropsOldestNotNewest(t *testing.T) {
	p, entered, unblock := blockingPipeline()
	defer unblock()

	p.push(micFrame{samples: []float32{0}}) // consumed by the parked consumer
	<-entered

	const total = audioQueueDepth * 10
	for i := 1; i < total; i++ {
		p.push(micFrame{samples: []float32{float32(i)}})
	}

	var survivors []int
	for len(survivors) < audioQueueDepth {
		survivors = append(survivors, int((<-p.frames).samples[0]))
	}
	for i, v := range survivors {
		assert.Equal(t, total-len(survivors)+i, v, "survivors must be a contiguous newest tail")
	}
	assert.Equal(t, int64(total-1-audioQueueDepth), p.Dropped())
}

// TestAudioPipeline_OneGoroutineAtATime is the other half of the contract.
// Concurrent producers are the real shape (capture thread + browser socket),
// and the stateful consumers behind process depend on never overlapping.
func TestAudioPipeline_OneGoroutineAtATime(t *testing.T) {
	var (
		inFlight atomic.Int32
		overlaps atomic.Int32
		seen     atomic.Int64
	)
	p := newAudioPipeline(func(micFrame) {
		if inFlight.Add(1) != 1 {
			overlaps.Add(1)
		}
		seen.Add(1)
		time.Sleep(50 * time.Microsecond) // widen any window there might be
		inFlight.Add(-1)
	})
	defer p.Stop()

	const producers, each = 4, 64
	var wg sync.WaitGroup
	for w := 0; w < producers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				p.push(micFrame{samples: make([]float32, 1)})
			}
		}()
	}
	wg.Wait()

	deadline := time.Now().Add(10 * time.Second)
	for seen.Load() < int64(producers*each) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	assert.Zero(t, overlaps.Load(), "process must never run concurrently with itself")
	assert.Equal(t, int64(producers*each), seen.Load()+p.Dropped(),
		"every pushed frame must be either delivered or counted as dropped")
}

// TestAudioPipeline_StopJoinsWithoutLeak is the shutdown contract: Stop
// returns only once the processing goroutine is gone, and the frame it was
// holding is finished rather than abandoned.
func TestAudioPipeline_StopJoinsWithoutLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	entered := make(chan struct{})
	var once sync.Once
	var ran atomic.Int32
	p := newAudioPipeline(func(micFrame) {
		ran.Add(1)
		once.Do(func() { close(entered) })
		time.Sleep(2 * time.Millisecond) // in flight when Stop arrives
	})
	defer p.Stop()

	for i := 0; i < audioQueueDepth*4; i++ {
		p.push(micFrame{samples: make([]float32, 1)})
	}
	<-entered
	p.Stop()

	assert.GreaterOrEqual(t, ran.Load(), int32(1))
	assert.True(t, p.closed.Load(), "Stop must close the pipeline so late pushes are inert")

	// Post-Stop pushes are inert rather than panicking or blocking: the
	// capture device may fire once more while it is being stopped.
	done := make(chan struct{})
	go func() { defer close(done); p.push(micFrame{samples: make([]float32, 1)}) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("push after Stop must return immediately")
	}

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	assert.LessOrEqual(t, runtime.NumGoroutine(), before+1, "pipeline goroutine leaked")
}

func TestAudioPipeline_StopIsIdempotent(t *testing.T) {
	p := newAudioPipeline(func(micFrame) {})
	p.Stop()
	p.Stop() // shutdown runs through defers; must not panic on double close
}

// TestAudioPipeline_ZeroDropUnderJitter models the real load: a consumer whose
// per-frame cost occasionally spikes (an ASR decode) but whose average cost is
// well inside the frame period. That must not cost a single frame — it is the
// difference between "the mic never stalls" and "the mic drops audio".
func TestAudioPipeline_ZeroDropUnderJitter(t *testing.T) {
	var ticks, processed atomic.Int64
	p := newAudioPipeline(func(micFrame) {
		n := ticks.Add(1)
		processed.Add(1)
		if n%16 == 0 {
			time.Sleep(3 * time.Millisecond) // the occasional slow frame
		}
	})
	defer p.Stop()

	// ~500 frames/sec, which is where a 16 kHz mic at a 20 ms period sits.
	// The consumer averages well under the 2 ms budget.
	const frames = 400
	for i := 0; i < frames; i++ {
		p.push(micFrame{samples: make([]float32, 1)})
		time.Sleep(2 * time.Millisecond)
	}

	deadline := time.Now().Add(10 * time.Second)
	for processed.Load() < frames && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	assert.Equal(t, int64(0), p.Dropped(), "jittery but nominally-paced processing must not drop frames")
}

// TestAudioPipeline_DropsUnderOverload is the other direction: when the
// consumer is genuinely slower than capture, frames must be shed and *counted*
// rather than queued without bound. An uncounted drop is the failure mode the
// counter exists to rule out.
func TestAudioPipeline_DropsUnderOverload(t *testing.T) {
	p := newAudioPipeline(func(micFrame) { time.Sleep(20 * time.Millisecond) })
	defer p.Stop()

	for i := 0; i < audioQueueDepth*8; i++ {
		p.push(micFrame{samples: make([]float32, 1)})
	}
	assert.Positive(t, p.Dropped(), "an overloaded consumer must shed and count frames")
}

// TestAudioPipeline_AECSurvivesDroppedMicFrames is the answer to the question
// this refactor has to settle: does losing a mic frame break echo
// cancellation?
//
// It does not, and the reason is structural. EchoCanceller.Process reads the
// *newest* L+n reference samples, so its alignment is "the speaker reference as
// of now", not "the reference as of when this frame was captured". Shed frames
// therefore do not shift the filter's view of the room: alignment is restored
// on the very next frame, and the only lasting effect is that one frame's worth
// of NLMS adaptation never happened. The weights stay a valid, slightly
// less-trained estimate of the echo path.
//
// That is the whole difference between an isolated drop and sustained overload.
// Isolated drops — the queue shedding a frame because one ASR decode ran long
// while capture kept its period — cost adaptation throughput and nothing else.
// A *steady* drop rate means the consumer is permanently slower than capture,
// the reference runs away from the canceller, and cancellation degrades; that
// is a different failure, and it is the one the drop counter exists to make
// visible rather than hide.
//
// The three assertions are the properties bargein_test.go pins for the inline
// path, re-checked with a third of the frames shed.
func TestAudioPipeline_AECSurvivesDroppedMicFrames(t *testing.T) {
	// A frame small enough that audioQueueDepth of them plus the one in flight
	// fit well inside the 4096-tap window — i.e. what a sane capture period
	// looks like, as opposed to the 100ms frames config.capture_buffer_ms
	// assumes.
	const n, L = 512, 4096
	require.Less(t, (audioQueueDepth+1)*n, L, "precondition: the queue lag must fit the canceller window")

	// run drives frames through the real queue with a real canceller, in order,
	// shedding every dropEvery'th one. Playback advances whether or not the
	// mic frame survives — that is what makes the shed frame matter, because
	// the reference keeps moving while adaptation does not.
	run := func(frames, dropEvery int, userLevel float32) (coherence, residualRMS float64, loud bool) {
		span := frames * n
		ref := randomSignal(L+span+n, 21, 0.3)
		user := randomSignal(span+n, 22, userLevel)

		// Mai's voice as captured, at the level from the original false
		// barge-in report.
		echo := make([]float32, span)
		for t0 := 0; t0 < span; t0 += n {
			copy(echo[t0:], syntheticEcho(ref, t0, n))
		}
		if r := rmsOf(echo); r > 1e-12 {
			g := float32(0.0844 / r)
			for i := range echo {
				echo[i] *= g
			}
		}

		refBuffer.Clear()
		refBuffer.Push(make([]float32, L)) // align the window with the room path
		ec := NewEchoCanceller(L)
		ec.ref = refBuffer

		ack := make(chan struct{}, 1)
		p := newAudioPipeline(func(f micFrame) {
			out := ec.Process(f.samples)
			residualRMS = rmsOf(out)
			loud = residualRMS > bargeInGateRMS
			if loud {
				coherence = ec.EchoCoherence(out) // only once the cheap gate fires
			}
			ack <- struct{}{}
		})
		defer p.Stop()

		for k := 0; k < frames; k++ {
			t0 := k * n
			refBuffer.Push(ref[t0 : t0+n]) // the speaker is unaffected by a mic drop
			if dropEvery > 0 && k%dropEvery == 0 {
				continue // captured, then shed: never reaches the canceller
			}
			mic := make([]float32, n)
			for i := range mic {
				mic[i] = echo[t0+i] + user[t0+i]
			}
			p.push(micFrame{samples: mic})
			<-ack // one device period per frame: the consumer keeps up
		}
		return coherence, residualRMS, loud
	}

	t.Run("unconverged her voice is still echo", func(t *testing.T) {
		_, _, loud := run(6, 0, 0)
		require.True(t, loud, "precondition: uncancelled echo must clear the energy gate")

		coherence, residual, loud := run(6, 3, 0)
		assert.True(t, loud, "residual %.4f", residual)
		assert.GreaterOrEqual(t, coherence, bargeInEchoMaxDefault,
			"coherence must still identify her voice as echo with a third of frames missing")
	})

	t.Run("canceller still converges", func(t *testing.T) {
		const frames = 150 // ~4.8s of audio, matching the inline path's window

		_, _, loud := run(frames, 0, 0)
		require.False(t, loud, "precondition: a converged canceller must cancel her below the gate")

		_, residual, loud := run(frames, 3, 0)
		assert.False(t, loud,
			"a converged canceller must still cancel her voice with frames missing (residual %.4f)", residual)
	})

	t.Run("a real interruption still passes", func(t *testing.T) {
		coherence, _, _ := run(20, 3, 0.06)
		assert.Less(t, coherence, bargeInEchoMaxDefault,
			"a second speaker must not be dismissed as echo when frames are being dropped")
	})
}
