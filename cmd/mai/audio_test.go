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
)

func TestGenerateThinkingChime(t *testing.T) {
	chime := generateThinkingChime(44100)

	// Duration ~80ms at 44.1kHz = ~3528 samples
	expectedSamples := int(float64(44100) * 0.08)
	assert.InDelta(t, expectedSamples, len(chime), 100)

	// All samples should be in [-1, 1]
	for i, s := range chime {
		assert.True(t, s >= -1.0 && s <= 1.0, "sample %d out of range: %f", i, s)
	}

	// First few samples should have non-zero energy (sine wave starts)
	// Note: sin(0)=0, so sample 0 is 0, but nearby samples should be non-zero
	hasEnergy := false
	for i := 1; i < 100 && i < len(chime); i++ {
		if chime[i] != 0 {
			hasEnergy = true
			break
		}
	}
	assert.True(t, hasEnergy, "chime has no energy in first 100 samples")

	// Last sample should be near zero (envelope fades out)
	assert.True(t, math.Abs(float64(chime[len(chime)-1])) < 0.05, "last sample too loud: %f", chime[len(chime)-1])
}

func TestPlayAudioStreaming_BufferDrain(t *testing.T) {
	// Test that playAudioStreaming properly drains the buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stop int32
	var generated int32

	err := playAudioStreaming(ctx, 44100, &stop, func(ch chan<- []float32) {
		// Send a few chunks
		for i := 0; i < 5; i++ {
			chunk := make([]float32, 1024)
			for j := range chunk {
				chunk[j] = float32(math.Sin(2*math.Pi*0.01*float64(j+i*1024)))
			}
			ch <- chunk
			atomic.AddInt32(&generated, 1)
		}
	})

	assert.NoError(t, err)
	assert.Equal(t, int32(5), atomic.LoadInt32(&generated))
}

func TestPlayAudioStreaming_BargeIn(t *testing.T) {
	// Test that barge-in stops playback
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var stop int32
	playDone := make(chan error, 1)

	go func() {
		playDone <- playAudioStreaming(ctx, 44100, &stop, func(ch chan<- []float32) {
			// Send many chunks to simulate long TTS
			for i := 0; i < 100; i++ {
				chunk := make([]float32, 4096)
				for j := range chunk {
					chunk[j] = float32(math.Sin(2*math.Pi*0.01*float64(j+i*4096)))
				}
				ch <- chunk
				time.Sleep(10 * time.Millisecond)
			}
		})
	}()

	// Simulate barge-in after a short delay
	time.Sleep(50 * time.Millisecond)
	atomic.StoreInt32(&stop, 1)

	err := <-playDone
	assert.NoError(t, err)
}

func TestPlayAudioStreaming_EmptyGenerate(t *testing.T) {
	// Test that empty generator doesn't hang
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var stop int32

	err := playAudioStreaming(ctx, 44100, &stop, func(ch chan<- []float32) {
		// Send nothing
	})

	assert.NoError(t, err)
}

func TestPlayAudioStreaming_ContextCancel(t *testing.T) {
	// Test that context cancellation stops playback
	ctx, cancel := context.WithCancel(context.Background())
	var stop int32

	playDone := make(chan error, 1)
	go func() {
		playDone <- playAudioStreaming(ctx, 44100, &stop, func(ch chan<- []float32) {
			for i := 0; i < 1000; i++ {
				chunk := make([]float32, 4096)
				ch <- chunk
			}
		})
	}()

	// Cancel after a short delay
	time.Sleep(50 * time.Millisecond)
	cancel()

	err := <-playDone
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPublishTTSAudioChunk_NilBus(t *testing.T) {
	// Should not panic with nil bus
	assert.NotPanics(t, func() {
		publishTTSAudioChunk(nil, []float32{0.5}, 44100, false)
	})
}

func TestPublishTTSAudioChunk_EmptySamples(t *testing.T) {
	// Should not panic with empty samples
	assert.NotPanics(t, func() {
		publishTTSAudioChunk(nil, nil, 44100, false)
	})
}

func TestPublishTTSAudioChunk_Clipping(t *testing.T) {
	// Samples outside [-1, 1] should be clipped
	// We can't easily test the bus output, but we can verify no panic
	assert.NotPanics(t, func() {
		publishTTSAudioChunk(nil, []float32{-2.0, 0.0, 2.0}, 44100, false)
	})
}

func TestPublishTTSAudio_NilBus(t *testing.T) {
	assert.NotPanics(t, func() {
		publishTTSAudio(nil, []float32{0.5}, 44100, false)
	})
}

func TestPublishTTSAudio_EmptySamples(t *testing.T) {
	assert.NotPanics(t, func() {
		publishTTSAudio(nil, nil, 44100, false)
	})
}

func TestPublishTTSAudio_Chunking(t *testing.T) {
	// Verify chunking logic by checking the function doesn't panic
	// with large sample arrays
	samples := make([]float32, 32768) // 32k samples
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * 0.01 * float64(i)))
	}
	assert.NotPanics(t, func() {
		publishTTSAudio(nil, samples, 44100, true)
	})
}

// Memory leak tests using goroutine tracking
func TestPlayAudioStreaming_NoGoroutineLeak(t *testing.T) {
	// Track goroutines before
	before := runtime.NumGoroutine()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var stop int32

	err := playAudioStreaming(ctx, 44100, &stop, func(ch chan<- []float32) {
		chunk := make([]float32, 1024)
		ch <- chunk
	})
	assert.NoError(t, err)

	// Wait for goroutines to settle
	time.Sleep(100 * time.Millisecond)

	after := runtime.NumGoroutine()
	// Allow small fluctuation but no significant leak
	assert.LessOrEqual(t, after, before+2, "possible goroutine leak: before=%d after=%d", before, after)
}

// Suppress unused import warning
var _ = sync.Mutex{}
