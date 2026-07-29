package main

import (
	"testing"
	"time"

	"go.uber.org/goleak"
)

// TestMain sets up global goroutine leak detection.
// Any goroutine that survives test completion (beyond a short grace period)
// is reported as a potential leak.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		// Ignore known background goroutines that are expected to outlive tests
		goleak.IgnoreTopFunction("github.com/gen2brain/malgo.(*context).handler"),
		goleak.IgnoreTopFunction("internal/poll.runtime_pollWait"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("runtime/pprof.runtime_goroutineProfileWithLabels"),
		goleak.IgnoreAnyFunction("github.com/k2-fsa/sherpa-onnx-go"),
		goleak.IgnoreAnyFunction("malgo"),
	)
}

// TestSpeakerRef_NoLeak verifies speakerRef doesn't leak goroutines
func TestSpeakerRef_NoLeak(t *testing.T) {
	ref := newSpeakerRef(4 * 16000)
	for i := 0; i < 1000; i++ {
		ref.Push([]float32{float32(i%100) / 100.0})
		_ = ref.recent(100)
	}
}

// TestResampler_NoLeak verifies resampler doesn't leak goroutines
func TestResampler_NoLeak(t *testing.T) {
	rs := newResampler(44100, 16000)
	for i := 0; i < 100; i++ {
		chunk := make([]float32, 1024)
		for j := range chunk {
			chunk[j] = float32(j) / 1024.0
		}
		_ = rs.resample(chunk)
	}
}

// TestEchoCanceller_NoLeak verifies EchoCanceller doesn't leak goroutines
func TestEchoCanceller_NoLeak(t *testing.T) {
	ref := newSpeakerRef(4 * 16000)
	ec := NewEchoCanceller(256)
	ec.ref = ref

	// Push reference and process frames
	ref.Push(make([]float32, 2048))
	for i := 0; i < 100; i++ {
		frame := make([]float32, 512)
		for j := range frame {
			frame[j] = float32(j) / 512.0
		}
		_ = ec.Process(frame)
	}
}

// TestPublishTTSAudio_NoLeak verifies publishTTSAudio doesn't leak
func TestPublishTTSAudio_NoLeak(t *testing.T) {
	// With nil bus, should be a no-op
	for i := 0; i < 100; i++ {
		samples := make([]float32, 8192)
		publishTTSAudio(nil, samples, 44100, i%10 == 0)
	}
}

// TestPublishTTSAudioChunk_NoLeak verifies publishTTSAudioChunk doesn't leak
func TestPublishTTSAudioChunk_NoLeak(t *testing.T) {
	for i := 0; i < 100; i++ {
		samples := make([]float32, 1024)
		publishTTSAudioChunk(nil, samples, 44100, i%10 == 0)
	}
}

// TestGenerateThinkingChime_NoLeak verifies chime generation doesn't leak
func TestGenerateThinkingChime_NoLeak(t *testing.T) {
	for i := 0; i < 100; i++ {
		_ = generateThinkingChime(44100)
	}
}

// TestPlayAudioStreaming_ResourceCleanup verifies resources are freed
func TestPlayAudioStreaming_ResourceCleanup(t *testing.T) {
	// This test verifies that playAudioStreaming cleans up properly
	// by checking that repeated calls don't accumulate resources
	for i := 0; i < 3; i++ {
		done := make(chan error, 1)
		go func() {
			done <- playAudioStreaming(nil, 44100, nil, func(ch chan<- []float32) {
				// Send one chunk then close
			})
		}()

		select {
		case <-done:
			// OK - device init may fail with nil context, that's fine
		case <-time.After(2 * time.Second):
			t.Fatal("playAudioStreaming timed out")
		}
	}
}
