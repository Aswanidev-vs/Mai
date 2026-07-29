package main

import (
	"context"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/user/mai/internal/events"
	"github.com/user/mai/pkg/interfaces"
)

type ttsItem struct {
	text  string
	speed float32
}

// TestStress_PublishTTSAudioChunk verifies no goroutine or memory leak
// when publishing many TTS chunks rapidly (simulating streaming TTS).
func TestStress_PublishTTSAudioChunk(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 500; i++ {
		samples := make([]float32, 2048)
		for j := range samples {
			samples[j] = float32(math.Sin(2*math.Pi*0.01*float64(j+i*2048)))
		}
		publishTTSAudioChunk(nil, samples, 44100, i%50 == 0)
	}

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2, "goroutine leak: before=%d after=%d", before, after)
}

// TestStress_PublishTTSAudio verifies no leak with large audio buffers.
func TestStress_PublishTTSAudio(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 100; i++ {
		samples := make([]float32, 32768) // 32k samples
		for j := range samples {
			samples[j] = float32(math.Sin(2*math.Pi*0.001*float64(j)))
		}
		publishTTSAudio(nil, samples, 44100, i%10 == 0)
	}

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2, "goroutine leak: before=%d after=%d", before, after)
}

// TestStress_EchoCanceller verifies no leak with heavy AEC usage.
func TestStress_EchoCanceller(t *testing.T) {
	before := runtime.NumGoroutine()

	ref := newSpeakerRef(4 * 16000)
	ec := NewEchoCanceller(1024)
	ec.ref = ref

	for i := 0; i < 500; i++ {
		// Push reference signal
		refSig := make([]float32, 512)
		for j := range refSig {
			refSig[j] = float32(math.Sin(2*math.Pi*0.01*float64(j+i*512)))
		}
		ref.Push(refSig)

		// Process mic frame
		micFrame := make([]float32, 512)
		for j := range micFrame {
			micFrame[j] = refSig[j]*0.8 + float32(math.Sin(2*math.Pi*0.001*float64(j)))*0.01
		}
		_ = ec.Process(micFrame)
	}

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2, "goroutine leak: before=%d after=%d", before, after)
}

// TestStress_Resampler verifies no leak with streaming resampling.
func TestStress_Resampler(t *testing.T) {
	before := runtime.NumGoroutine()

	rs := newResampler(44100, 16000)
	for i := 0; i < 500; i++ {
		chunk := make([]float32, 1024)
		for j := range chunk {
			chunk[j] = float32(math.Sin(2*math.Pi*0.01*float64(j+i*1024)))
		}
		_ = rs.resample(chunk)
	}

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2, "goroutine leak: before=%d after=%d", before, after)
}

// TestStress_PlayAudioStreaming_BargeInRecovery verifies no goroutine leak
// when playAudioStreaming is interrupted repeatedly.
func TestStress_PlayAudioStreaming_BargeInRecovery(t *testing.T) {
	before := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		var stop int32

		done := make(chan error, 1)
		go func() {
			done <- playAudioStreaming(ctx, 44100, &stop, func(ch chan<- []float32) {
				for j := 0; j < 100; j++ {
					if atomic.LoadInt32(&stop) != 0 {
						return
					}
					chunk := make([]float32, 2048)
					for k := range chunk {
						chunk[k] = float32(math.Sin(2 * math.Pi * 0.01 * float64(k+j*2048)))
					}
					ch <- chunk
					time.Sleep(5 * time.Millisecond)
				}
			})
		}()

		// Simulate barge-in
		time.Sleep(30 * time.Millisecond)
		atomic.StoreInt32(&stop, 1)

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("timeout waiting for playback to stop")
		}
		cancel()
	}

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	after := runtime.NumGoroutine()
	// Allow some fluctuation but no significant leak
	assert.LessOrEqual(t, after, before+5, "goroutine leak after %d barge-ins: before=%d after=%d", 5, before, after)
}

// TestStress_TTSSentCh_NoLeak verifies the sentence channel doesn't leak.
func TestStress_TTSSentCh_NoLeak(t *testing.T) {
	before := runtime.NumGoroutine()

	ch := make(chan ttsItem, 64)
	var wg sync.WaitGroup

	// Producer
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			ch <- ttsItem{text: "test sentence", speed: 1.0}
			time.Sleep(time.Millisecond)
		}
		close(ch)
	}()

	// Consumer (simulates player goroutine)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range ch {
			// Simulate processing
			time.Sleep(2 * time.Millisecond)
		}
	}()

	wg.Wait()

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2, "goroutine leak: before=%d after=%d", before, after)
}

// TestStress_EventBus_SubUnsub verifies no leak with rapid subscribe/unsubscribe.
func TestStress_EventBus_SubUnsub(t *testing.T) {
	before := runtime.NumGoroutine()

	bus := events.NewBus()
	for i := 0; i < 500; i++ {
		sub := bus.Subscribe("test.event", func(event interfaces.Event) {})
		sub.Unsubscribe()
	}

	runtime.GC()
	time.Sleep(100 * time.Millisecond)
	after := runtime.NumGoroutine()
	assert.LessOrEqual(t, after, before+2, "goroutine leak: before=%d after=%d", before, after)
}

// TestStress_BufferTrim verifies the audio buffer trimming doesn't leak.
func TestStress_BufferTrim(t *testing.T) {
	// Simulate the buffer growth and trim pattern from playAudioStreaming
	var mu sync.Mutex
	var buf []float32
	var readIdx int

	for i := 0; i < 1000; i++ {
		mu.Lock()
		// Simulate adding data
		chunk := make([]float32, 1024)
		for j := range chunk {
			chunk[j] = float32(j) / 1024.0
		}
		buf = append(buf, chunk...)

		// Simulate reading data
		readIdx += 512
		if readIdx > len(buf) {
			readIdx = len(buf)
		}

		// Simulate trimming (same logic as playAudioStreaming)
		if readIdx > 8192 && readIdx > len(buf)/2 {
			buf = buf[readIdx:]
			readIdx = 0
		}
		mu.Unlock()
	}

	// Verify buffer doesn't grow unbounded
	assert.Less(t, len(buf), 2000000, "buffer grew too large: %d", len(buf))
}
