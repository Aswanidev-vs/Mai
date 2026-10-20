package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

// End-to-end validation of the queue-overflow diagnosis, using the real
// audioPipeline type and the real ASR recognizer.
//
// It replays 25 ms capture frames at the real 40 frames/sec cadence into a
// pipeline whose consumer does the real streaming-ASR work, and counts how many
// frames the queue had to discard. Then it repeats with a deeper queue, which
// is the proposed fix.
//
// Run with:
//
//	MAI_PIPELINE_PROBE=1 go test -c -o pipelineprobe.exe ./cmd/mai && cd <repo root> && ./pipelineprobe.exe -test.run TestQueueOverflow -test.v
func TestQueueOverflow(t *testing.T) {
	if os.Getenv("MAI_PIPELINE_PROBE") == "" {
		t.Skip("model-loading probe; set MAI_PIPELINE_PROBE=1 to run")
	}
	root := pipelineProbeRoot(t)
	cfg := pipelineProbeConfig(t)
	resolve := func(rel string) string {
		if filepath.IsAbs(rel) {
			return rel
		}
		return filepath.Join(root, rel)
	}

	n := cfg.ASR.Nemotron
	asrConfig := sherpa.OnlineRecognizerConfig{}
	asrConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
	asrConfig.ModelConfig.Transducer.Encoder = resolve(join(n.ModelDir, n.Encoder))
	asrConfig.ModelConfig.Transducer.Decoder = resolve(join(n.ModelDir, n.Decoder))
	asrConfig.ModelConfig.Transducer.Joiner = resolve(join(n.ModelDir, n.Joiner))
	asrConfig.ModelConfig.Tokens = resolve(join(n.ModelDir, n.Tokens))
	asrConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
	asrConfig.ModelConfig.Provider = cfg.ASR.Provider
	asrConfig.DecodingMethod = n.DecodingMethod
	if asrConfig.DecodingMethod == "" {
		asrConfig.DecodingMethod = "greedy_search"
	}
	asrConfig.MaxActivePaths = n.MaxActivePaths
	asrConfig.EnableEndpoint = n.EnableEndpoint
	asrConfig.Rule1MinTrailingSilence = n.Rule1MinTrailingSilence
	asrConfig.Rule2MinTrailingSilence = n.Rule2MinTrailingSilence
	asrConfig.Rule3MinUtteranceLength = n.Rule3MinUtteranceLength

	recognizer := sherpa.NewOnlineRecognizer(&asrConfig)
	if recognizer == nil {
		t.Fatal("NewOnlineRecognizer returned nil")
	}
	defer sherpa.DeleteOnlineRecognizer(recognizer)

	// A consumer that does the same work handleAudioFrame does on the ASR path.
	newConsumer := func() func(micFrame) {
		stream := sherpa.NewOnlineStream(recognizer)
		return func(f micFrame) {
			stream.AcceptWaveform(16000, f.samples)
			for recognizer.IsReady(stream) {
				recognizer.Decode(stream)
			}
		}
	}

	// run replays `seconds` of capture at the real cadence into a pipeline of
	// the given depth and returns how many frames were discarded.
	run := func(depth int, seconds float64) (dropped int64, pushed int) {
		p := &audioPipeline{
			frames:  make(chan micFrame, depth),
			process: newConsumer(),
			stop:    make(chan struct{}),
		}
		p.wg.Add(1)
		go p.run()

		frameN := 400
		samples := make([]float32, frameN) // silence is enough to drive the decoder
		period := 25 * time.Millisecond
		total := int(seconds * 1000 / 25)
		start := time.Now()
		for i := 0; i < total; i++ {
			p.push(micFrame{samples: samples})
			pushed++
			// hold the capture cadence
			if d := time.Until(start.Add(time.Duration(i+1) * period)); d > 0 {
				time.Sleep(d)
			}
		}
		p.closed.Store(true)
		close(p.stop)
		p.wg.Wait()
		return p.Dropped(), pushed
	}

	// runAdaptive exercises the shipped behaviour: the pipeline runs on the wide
	// idle budget, and the budget is narrowed to the echo canceller's window
	// when playback starts.
	runAdaptive := func(seconds float64) (dropped int64, pushed int) {
		p := &audioPipeline{
			frames:  make(chan micFrame, audioQueueCapacity),
			process: newConsumer(),
			stop:    make(chan struct{}),
		}
		p.maxLag.Store(audioQueueDepth)
		p.wg.Add(1)
		go p.run()
		p.SetMaxLag(audioIdleLagFrames) // idle: nothing is playing

		frameN := 400
		samples := make([]float32, frameN)
		period := 25 * time.Millisecond
		total := int(seconds * 1000 / 25)
		start := time.Now()
		for i := 0; i < total; i++ {
			p.push(micFrame{samples: samples})
			pushed++
			if d := time.Until(start.Add(time.Duration(i+1) * period)); d > 0 {
				time.Sleep(d)
			}
		}
		p.closed.Store(true)
		close(p.stop)
		p.wg.Wait()
		return p.Dropped(), pushed
	}

	fmt.Printf("\n=== queue overflow: 25 ms frames at 40/sec, consumer = real streaming ASR ===\n")
	fmt.Println("--- before the fix: a fixed 3-frame budget (75 ms) ---")
	for _, depth := range []int{3, 6, 12, 24} {
		dropped, pushed := run(depth, 10)
		perSec := float64(dropped) / 10.0
		fmt.Printf("  fixed depth %2d (%3d ms): %4d/%d frames dropped over 10 s  = %.1f drops/sec\n",
			depth, depth*25, dropped, pushed, perSec)
	}
	fmt.Println("--- after the fix: the shipped adaptive budget ---")
	dropped, pushed := runAdaptive(10)
	fmt.Printf("  adaptive (idle %d frames = %d ms): %4d/%d frames dropped over 10 s  = %.1f drops/sec\n",
		audioIdleLagFrames, audioIdleLagFrames*25, dropped, pushed, float64(dropped)/10.0)
}
