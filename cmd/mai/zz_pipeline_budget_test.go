package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/user/mai/pkg/models"
	"gopkg.in/yaml.v3"
)

// Diagnostic harness that measures the per-frame cost of the audioPipeline
// consumer path with the *shipped* models, so the queue-overflow diagnosis is a
// number rather than an inference.
//
// The capture queue is only audioQueueDepth (3) frames deep = 75 ms, so what
// matters is not the mean frame cost but the TAIL: one frame whose consumer work
// exceeds the remaining queue slack overflows it. This harness therefore reports
// percentiles, and how many frames would have overflowed a 3-deep queue.
//
// It times, for one 400-sample capture frame (25 ms at 16 kHz):
//
//	denoise   DPDFNet enhancer, one 400-sample frame
//	kws       KeywordStream.AcceptWaveform + spotter.IsReady/Decode
//	asr       OnlineStream.AcceptWaveform + recognizer.IsReady/Decode
//	vad       Silero AcceptWaveform, fed in WindowSize (512) chunks
//
// Run with:
//
//	MAI_PIPELINE_PROBE=1 go test -c -o pipelineprobe.exe ./cmd/mai && cd <repo root> && ./pipelineprobe.exe -test.run TestPipelineBudget -test.v
//
// NOTE: build the test binary into the repository root. A stale onnxruntime.dll
// in C:\Windows\System32 shadows the 1.27.1 copy the project ships and every
// recognizer then dies inside ORT with an API-version fault.

func pipelineProbeRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func pipelineProbeConfig(t *testing.T) models.Config {
	t.Helper()
	path := filepath.Join(pipelineProbeRoot(t), "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no config.yaml: %v", err)
	}
	var cfg models.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cfg
}

// percentiles summarises a set of per-frame durations.
type percentiles struct {
	p50, p90, p99, max time.Duration
	mean               time.Duration
	over               int // frames exceeding a 3-deep queue's slack
	total              time.Duration
}

func probeMinInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func summarise(d []time.Duration, slack time.Duration) percentiles {
	if len(d) == 0 {
		return percentiles{}
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var total time.Duration
	p := percentiles{
		p50: s[len(s)*50/100],
		p90: s[len(s)*90/100],
		p99: s[probeMinInt(len(s)-1, len(s)*99/100)],
		max: s[len(s)-1],
	}
	for _, x := range s {
		total += x
		if x > slack {
			p.over++
		}
	}
	p.mean = total / time.Duration(len(s))
	p.total = total
	return p
}

func (p percentiles) line(name string, frameMS float64) string {
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }
	return fmt.Sprintf("  %-9s mean %6.2f  p50 %6.2f  p90 %6.2f  p99 %6.2f  max %7.2f ms   |  >25ms: %d/%d  (max = %.1f frame budgets)",
		name, ms(p.mean), ms(p.p50), ms(p.p90), ms(p.p99), ms(p.max), p.over, 1, ms(p.max)/frameMS)
}

func TestPipelineBudget(t *testing.T) {
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

	const frameN = 400 // 25 ms at 16 kHz — the capture period in use
	const frameMS = 25.0
	// The queue holds audioQueueDepth frames; the consumer is allowed to be one
	// frame behind before the oldest is discarded, so a frame costing more than
	// (depth-1) frames' worth of time is what actually overflows it.
	slack := time.Duration(audioQueueDepth-1) * 25 * time.Millisecond

	// ── signals to measure ──
	silence := make([]float32, 200*frameN)

	noise := make([]float32, 200*frameN)
	rng := rand.New(rand.NewSource(7))
	for i := range noise {
		noise[i] = float32(rng.NormFloat64() * 0.002)
	}

	// Real speech: the KWS test wav is 16 kHz mono.
	var speech []float32
	wavPath := filepath.Join(root, "sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01", "test_wavs", "0.wav")
	if audio, rate, ok := asrProbeReadWAV(wavPath); ok && rate == 16000 {
		need := 200 * frameN
		speech = make([]float32, need)
		for i := range speech {
			speech[i] = audio[i%len(audio)]
		}
	} else {
		t.Logf("no speech wav at %s (ok=%v rate=%v) — skipping the speech case", wavPath, ok, rate)
	}

	// ── models, built exactly as main.go builds them ──
	kwsConfig := sherpa.KeywordSpotterConfig{}
	kwsConfig.ModelConfig.Transducer.Encoder = resolve(join(cfg.KWS.ModelDir, cfg.KWS.Encoder))
	kwsConfig.ModelConfig.Transducer.Decoder = resolve(join(cfg.KWS.ModelDir, cfg.KWS.Decoder))
	kwsConfig.ModelConfig.Transducer.Joiner = resolve(join(cfg.KWS.ModelDir, cfg.KWS.Joiner))
	kwsConfig.ModelConfig.Tokens = resolve(join(cfg.KWS.ModelDir, cfg.KWS.Tokens))
	kwsConfig.KeywordsBuf = splitKeywords(cfg.KWS.Keywords, "\n")
	kwsConfig.KeywordsBufSize = len(kwsConfig.KeywordsBuf)
	kwsConfig.KeywordsThreshold = cfg.KWS.Threshold
	kwsConfig.ModelConfig.NumThreads = cfg.KWS.NumThreads
	kwsConfig.ModelConfig.Provider = cfg.KWS.Provider
	spotter := sherpa.NewKeywordSpotter(&kwsConfig)
	if spotter == nil {
		t.Fatal("NewKeywordSpotter returned nil")
	}
	defer sherpa.DeleteKeywordSpotter(spotter)

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

	vadConfig := sherpa.VadModelConfig{}
	vadConfig.SileroVad.Model = resolve(cfg.VAD.Model)
	vadConfig.SileroVad.Threshold = cfg.VAD.Threshold
	vadConfig.SileroVad.MinSilenceDuration = cfg.VAD.MinSilenceDuration
	vadConfig.SileroVad.MinSpeechDuration = cfg.VAD.MinSpeechDuration
	vadConfig.SileroVad.MaxSpeechDuration = cfg.VAD.MaxSpeechDuration
	vadConfig.SileroVad.WindowSize = cfg.VAD.WindowSize
	vadConfig.SampleRate = 16000
	vadConfig.NumThreads = cfg.VAD.NumThreads
	vadConfig.Provider = cfg.VAD.Provider

	ns := newNoiseSuppressor(&cfg)
	if ns == nil {
		t.Log("denoiser unavailable — timing the rest only")
	} else {
		defer ns.Close()
	}

	// ── run each signal through a fresh set of streams ──
	type result struct {
		name    string
		kws     []time.Duration
		asr     []time.Duration
		vad     []time.Duration
		deno    []time.Duration
		frames  int
		slowIdx []int
	}

	run := func(label string, signal []float32) result {
		frames := len(signal) / frameN
		res := result{name: label, frames: frames}
		slowIdx := []int{}

		kwsStream := sherpa.NewKeywordStreamWithKeywords(spotter, splitKeywords(cfg.KWS.Keywords, "/"))
		defer sherpa.DeleteOnlineStream(kwsStream)
		asrStream := sherpa.NewOnlineStream(recognizer)
		defer sherpa.DeleteOnlineStream(asrStream)
		if lang := n.Language; lang != "" {
			asrStream.SetOption("language", lang)
		}
		vadDetector := sherpa.NewVoiceActivityDetector(&vadConfig, 20)
		defer sherpa.DeleteVoiceActivityDetector(vadDetector)
		vadBuf := sherpa.NewCircularBuffer(10 * 16000)
		defer sherpa.DeleteCircularBuffer(vadBuf)

		if ns != nil {
			ns.Process(signal[:frameN]) // warm up
		}
		for i := 0; i < frames; i++ {
			f := signal[i*frameN : (i+1)*frameN]

			if ns != nil {
				st := time.Now()
				ns.Process(f)
				res.deno = append(res.deno, time.Since(st))
			}

			st := time.Now()
			kwsStream.AcceptWaveform(16000, f)
			for spotter.IsReady(kwsStream) {
				spotter.Decode(kwsStream)
			}
			res.kws = append(res.kws, time.Since(st))

			st = time.Now()
			asrStream.AcceptWaveform(16000, f)
			for recognizer.IsReady(asrStream) {
				recognizer.Decode(asrStream)
			}
			d := time.Since(st)
			res.asr = append(res.asr, d)
			if d > 25*time.Millisecond {
				slowIdx = append(slowIdx, i)
			}

			vadBuf.Push(f)
			for vadBuf.Size() >= cfg.VAD.WindowSize {
				head := vadBuf.Head()
				chunk := vadBuf.Get(head, cfg.VAD.WindowSize)
				vadBuf.Pop(cfg.VAD.WindowSize)
				st = time.Now()
				vadDetector.AcceptWaveform(chunk)
				res.vad = append(res.vad, time.Since(st))
			}
		}
		res.slowIdx = slowIdx
		return res
	}

	results := []result{run("silence", silence), run("room noise", noise)}
	if speech != nil {
		results = append(results, run("speech", speech))
	}

	fmt.Printf("\n=== audioPipeline consumer: per-frame cost (400 samples = 25 ms, queue depth %d = %d ms) ===\n",
		audioQueueDepth, audioQueueDepth*25)
	for _, r := range results {
		fmt.Printf("\n--- signal: %s (%d frames = %.1f s) ---\n", r.name, r.frames, float64(r.frames)*25.0/1000.0)
		fmt.Println(summarise(r.kws, slack).line("kws", frameMS))
		fmt.Println(summarise(r.asr, slack).line("asr", frameMS))
		fmt.Println(summarise(r.vad, slack).line("vad", frameMS))
		if len(r.deno) > 0 {
			fmt.Println(summarise(r.deno, slack).line("denoise", frameMS))
		}
		// The consumer's critical path per frame is kws + asr + vad.
		var combined []time.Duration
		for i := range r.kws {
			if i < len(r.asr) {
				combined = append(combined, r.kws[i]+r.asr[i])
			}
		}
		fmt.Println(summarise(combined, slack).line("kws+asr", frameMS))
		if len(r.slowIdx) > 0 {
			fmt.Printf("  slow ASR frames (index, elapsed ms):")
			for _, i := range r.slowIdx {
				fmt.Printf(" %d=%.0fms", i, float64(r.asr[i].Microseconds())/1000.0)
			}
			fmt.Println()
			if len(r.slowIdx) > 1 {
				fmt.Printf("  spacing between slow frames (frames -> ms):")
				for k := 1; k < len(r.slowIdx); k++ {
					fmt.Printf(" %d", (r.slowIdx[k]-r.slowIdx[k-1])*25)
				}
				fmt.Println("   <- expect ~560 ms if these are the model's chunk boundaries")
			}
		}
	}
	fmt.Println("\n  '>25ms' counts frames whose stage cost alone exceeds one capture period —")
	fmt.Println("  the queue cannot absorb those, so each one is a guaranteed drop.")
}

func splitKeywords(s, sep string) string {
	out := ""
	for i, k := range splitOnComma(s) {
		if i > 0 {
			out += sep
		}
		out += k
	}
	return out
}

func splitOnComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}
