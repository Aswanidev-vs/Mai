// Mai - Simplified Offline Voice Assistant
//
// Usage:
//
//	cd e:/Mai
//	go mod tidy
//
// go build -o mai.exe ./cmd/mai
// ./mai
package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/user/mai/internal/agent"
	"github.com/user/mai/internal/cognition"
	"github.com/user/mai/internal/events"
	"github.com/user/mai/internal/llm"
	"github.com/user/mai/internal/memory"
	"github.com/user/mai/internal/perception"
	"github.com/user/mai/internal/personality"
	"github.com/user/mai/internal/server"
	"github.com/user/mai/internal/tools"
	"github.com/user/mai/internal/tools/adapters"
	"github.com/user/mai/internal/tools/mcp"
	pockettts "github.com/user/mai/internal/tts/pocket"
	"github.com/user/mai/pkg/interfaces"
	"github.com/user/mai/pkg/models"
	"gopkg.in/yaml.v3"
)

// bargeInEchoMaxDefault is the echo-coherence ceiling used when the config does
// not set one: a residual whose correlation with Mai's own playback is at least
// this strong is treated as her voice leaking through, not as the user.
//
// Chosen from measurement on a simulated room echo path with a fresh
// (unconverged) canceller — Mai's own leaked voice scores 0.73-0.87 while a
// normal interruption over her voice scores 0.45-0.67 — so 0.7 sits in the
// middle of that gap. See
// TestBargeIn_UncancelledEchoIsRejectedButInterruptionStillFires.
const bargeInEchoMaxDefault = 0.7

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// Load .env file if present
	loadEnvFile(".env")

	var configPath string
	var testCloud bool
	var companionMode bool
	flag.StringVar(&configPath, "config", "config.yaml", "Path to configuration file")
	flag.BoolVar(&testCloud, "test-cloud", false, "Test cloud LLM provider and exit")
	flag.BoolVar(&companionMode, "companion", false, "Enable companion Web UI (overrides config)")
	flag.Parse()

	// Load config
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Failed to read config: %v", err)
	}
	var cfg models.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}

	// Resolve environment variable references in config
	resolveConfigEnv(&cfg)

	// Apply LLM sampling defaults if not configured (reduces hallucination / yapping).
	if cfg.LLM.Sampling.Temperature == 0 {
		cfg.LLM.Sampling.Temperature = 0.55
	}
	if cfg.LLM.Sampling.TopP == 0 {
		cfg.LLM.Sampling.TopP = 0.85
	}
	if cfg.LLM.Sampling.MaxTokens == 0 {
		cfg.LLM.Sampling.MaxTokens = 250
	}

	// Test cloud provider if requested
	if testCloud {
		log.Println("[TEST] Testing cloud LLM provider...")
		factory := llm.NewFactory(cfg)
		if err := factory.TestCloudProvider(); err != nil {
			log.Fatalf("[TEST] Cloud provider test FAILED: %v", err)
		}
		log.Println("[TEST] Cloud provider test PASSED")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var isSpeaking int32 // atomic: 1=speaking, 0=idle
	var lastResponseTime = time.Now()
	var lastResponseMu sync.Mutex
	var lastDetected time.Time = time.Now().Add(-time.Hour)
	var sessionSamples []float32
	var ttsMu sync.Mutex // Mutex for thread-safe TTS

	// atomic: 1=speaking/assistant-audio-muting, 0=idle
	var ttsPlaying int32   // atomic: 1=playing TTS, 0=not
	var stopPlayback int32 // atomic: 1=stop current TTS playback (barge-in), 0=normal
	var lastMicRMS float64
	var lastMicMu sync.Mutex
	var sherpaMu sync.Mutex // Mutex for all other Sherpa-ONNX calls

	// Post-TTS cooldown: suppress mic input briefly after TTS finishes to
	// prevent room reverberation from reaching ASR as false user input.
	// Stored as atomic UnixNano so the audio callback goroutine can read
	// without holding a mutex.
	var lastTTSEndNano atomic.Int64
	const ttsCooldown = 150 * time.Millisecond

	// Post-TTS AEC window: continue echo cancellation for this long after
	// TTS finishes, so room reverberation doesn't pass through to ASR. 2.5s
	// covers typical room reverb + browser playback tail.
	const postTTSAECWindow = 2500 * time.Millisecond

	// Barge-in tuning (while TTS is playing). A real interruption is a loud,
	// sustained voice; a one-frame echo leak or a noise/reverb spike must not
	// cut Mai off mid-sentence. Detection only arms after the AEC warmup
	// window (so the filter has converged) and fires only when the residual
	// stays above BargeInThreshold * bargeInMargin for bargeInSustain straight.
	bargeInWarmup := time.Duration(cfg.Audio.BargeInWarmupMs) * time.Millisecond
	if bargeInWarmup <= 0 {
		bargeInWarmup = 400 * time.Millisecond
	}
	const bargeInMargin = 2.5 // x BargeInThreshold for real speech
	bargeInSustain := time.Duration(cfg.Audio.BargeInSustainMs) * time.Millisecond
	if bargeInSustain <= 0 {
		bargeInSustain = 150 * time.Millisecond
	}
	// Echo-coherence guard: the strongest normalised correlation between the
	// echo-cancelled residual and the speaker reference that still counts as
	// someone interrupting. Mai's own voice leaking through a canceller that has
	// not converged yet tracks the reference almost perfectly (~0.9) and is
	// rejected; another speaker is uncorrelated with it. >= 1 disables the check.
	// bargeInEchoMaxDefault is used when the config does not set one; see its
	// package-level declaration for how 0.7 was chosen.
	bargeInEchoMax := cfg.Audio.BargeInEchoMax
	if bargeInEchoMax <= 0 {
		bargeInEchoMax = bargeInEchoMaxDefault
	}
	var bargeLogNano atomic.Int64 // rate-limits the "why didn't it fire" diagnostics

	// Shared across main scope so greeting / TTS / browser-mic paths can reach the bus.
	var bus interfaces.EventBus
	var companionServer *server.Server
	var browserMicActive int32
	companionHasClient := func() bool { return companionServer != nil && companionServer.ClientCount() > 0 }
	var capture *audioCapture                  // forward-declared; assigned later
	var handleAudioFrame func([]float32, bool) // forward-declared; assigned later

	// Echo cancellation for genuine barge-in: subtracts Mai's own TTS (echoed
	// through the mic) so only a real second speaker survives in the residual.
	echoCanceller := NewEchoCanceller(4096)

	// Background-noise suppression: the mic is streamed through Sherpa-ONNX's
	// DPDFNet speech enhancer so stationary noise (laptop fans, HVAC, mains
	// hum) can no longer reach the wake-word spotter, the VAD or ASR as if it
	// were a user utterance. nil when disabled or the model is unavailable —
	// every call site then falls back to the raw-mic behaviour.
	denoiser := newDenoiseGate(newNoiseSuppressor(&cfg))
	defer denoiser.Close()

	var bargeStartNano atomic.Int64 // when the residual first crossed the barge-in gate (0 = idle)
	var ttsStartedNano atomic.Int64 // UnixNano when current TTS playback started

	// waitForMicSilence blocks until the mic stays quiet (RMS < 0.0015 for 3 consecutive checks)
	// or a 500ms deadline is reached. Returns true if silence was detected, false if timed out.
	waitForMicSilence := func() bool {
		const (
			silenceRMS = 0.0015
			consecN    = 3
			checkEvery = 50 * time.Millisecond
		)
		consec := 0
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			lastMicMu.Lock()
			rms := lastMicRMS
			lastMicMu.Unlock()
			if rms < silenceRMS {
				consec++
				if consec >= consecN {
					return true
				}
			} else {
				consec = 0
			}
			time.Sleep(checkEvery)
		}
		return false
	}

	// thinkingChime plays a short tone to indicate LLM processing has started.
	playingChime := int32(0)
	playThinkingChime := func() {
		if !cfg.Audio.ThinkingChime || !atomic.CompareAndSwapInt32(&playingChime, 0, 1) {
			return
		}
		go func() {
			chime := generateThinkingChime(16000)
			_ = playAudio(ctx, chime, 16000, nil)
			atomic.StoreInt32(&playingChime, 0)
		}()
	}

	// Audio Lookback Buffer (1.5s at 16000Hz = 24000 samples)
	lookbackSize := 24000
	lookbackBuffer := make([]float32, lookbackSize)
	lookbackIdx := 0

	// Safety cap for offline ASR buffer: 30 seconds at 16kHz = 480000 samples.
	// If VAD never finalizes, this prevents unbounded memory growth.
	const offlineASRMaxSamples = 480000
	const offlineASRMaxDuration = 30 * time.Second

	// Start Ollama if needed
	if cfg.LLM.AutoStart && cfg.LLM.Provider == "ollama" {
		stopOllama := startOllama()
		defer stopOllama()
	}

	log.Println("========================================")
	log.Println("  Mai - Offline AI Assistant")
	log.Println("========================================")

	// 1. Initialize KWS (wake word)
	kwsConfig := sherpa.KeywordSpotterConfig{}
	kwsConfig.ModelConfig.Transducer.Encoder = join(cfg.KWS.ModelDir, cfg.KWS.Encoder)
	kwsConfig.ModelConfig.Transducer.Decoder = join(cfg.KWS.ModelDir, cfg.KWS.Decoder)
	kwsConfig.ModelConfig.Transducer.Joiner = join(cfg.KWS.ModelDir, cfg.KWS.Joiner)
	kwsConfig.ModelConfig.Tokens = join(cfg.KWS.ModelDir, cfg.KWS.Tokens)
	kwsConfig.KeywordsBuf = strings.ReplaceAll(cfg.KWS.Keywords, ",", "\n")
	kwsConfig.KeywordsBufSize = len(kwsConfig.KeywordsBuf)
	kwsConfig.KeywordsThreshold = cfg.KWS.Threshold
	kwsConfig.ModelConfig.NumThreads = cfg.KWS.NumThreads
	kwsConfig.ModelConfig.Provider = cfg.KWS.Provider

	spotter := sherpa.NewKeywordSpotter(&kwsConfig)
	if spotter == nil {
		log.Fatal("Failed to create keyword spotter")
	}
	defer sherpa.DeleteKeywordSpotter(spotter)

	kwsStream := sherpa.NewKeywordStreamWithKeywords(spotter, strings.ReplaceAll(cfg.KWS.Keywords, ",", "/"))
	if kwsStream == nil {
		log.Fatal("Failed to create keyword stream")
	}
	defer sherpa.DeleteOnlineStream(kwsStream)

	log.Println("[KWS] Wake word spotter ready")

	// 2. Initialize VAD
	vadConfig := sherpa.VadModelConfig{}
	vadConfig.SileroVad.Model = cfg.VAD.Model
	vadConfig.SileroVad.Threshold = cfg.VAD.Threshold
	vadConfig.SileroVad.MinSilenceDuration = cfg.VAD.MinSilenceDuration
	vadConfig.SileroVad.MinSpeechDuration = cfg.VAD.MinSpeechDuration
	vadConfig.SileroVad.MaxSpeechDuration = cfg.VAD.MaxSpeechDuration
	vadConfig.SileroVad.WindowSize = cfg.VAD.WindowSize
	vadConfig.SampleRate = 16000
	vadConfig.NumThreads = cfg.VAD.NumThreads
	vadConfig.Provider = cfg.VAD.Provider

	vadDetector := sherpa.NewVoiceActivityDetector(&vadConfig, 20)
	if vadDetector == nil {
		log.Fatal("Failed to create VAD")
	}
	defer sherpa.DeleteVoiceActivityDetector(vadDetector)

	vadBuffer := sherpa.NewCircularBuffer(10 * 16000)
	defer sherpa.DeleteCircularBuffer(vadBuffer)

	log.Println("[VAD] Voice activity detector ready")

	// 3. Initialize ASR (supports: qwen3, nemotron, omnilingual, transducer)
	var recognizer *sherpa.OnlineRecognizer
	var offlineRecognizer *sherpa.OfflineRecognizer
	var asrStream *sherpa.OnlineStream
	// Dual-mode fallback
	var fallbackOfflineRecognizer *sherpa.OfflineRecognizer

	switch cfg.ASR.ActiveModel {
	case "qwen3":
		offlineConfig := sherpa.OfflineRecognizerConfig{}
		offlineConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
		offlineConfig.ModelConfig.Qwen3ASR.ConvFrontend = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.ConvFrontend)
		offlineConfig.ModelConfig.Qwen3ASR.Encoder = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.Encoder)
		offlineConfig.ModelConfig.Qwen3ASR.Decoder = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.Decoder)
		offlineConfig.ModelConfig.Qwen3ASR.Tokenizer = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.Tokenizer)
		offlineConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
		offlineConfig.ModelConfig.Provider = cfg.ASR.Provider
		offlineConfig.DecodingMethod = cfg.ASR.Qwen3.DecodingMethod
		if offlineConfig.DecodingMethod == "" {
			offlineConfig.DecodingMethod = "greedy_search"
		}

		offlineRecognizer = sherpa.NewOfflineRecognizer(&offlineConfig)
		if offlineRecognizer == nil {
			log.Fatal("[ASR] Failed to create Qwen3 recognizer")
		}
		defer sherpa.DeleteOfflineRecognizer(offlineRecognizer)
		log.Println("[ASR] Offline Qwen3 recognizer ready")

	case "nemotron":
		asrConfig := sherpa.OnlineRecognizerConfig{}
		asrConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
		asrConfig.ModelConfig.Transducer.Encoder = join(cfg.ASR.Nemotron.ModelDir, cfg.ASR.Nemotron.Encoder)
		asrConfig.ModelConfig.Transducer.Decoder = join(cfg.ASR.Nemotron.ModelDir, cfg.ASR.Nemotron.Decoder)
		asrConfig.ModelConfig.Transducer.Joiner = join(cfg.ASR.Nemotron.ModelDir, cfg.ASR.Nemotron.Joiner)
		asrConfig.ModelConfig.Tokens = join(cfg.ASR.Nemotron.ModelDir, cfg.ASR.Nemotron.Tokens)
		asrConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
		asrConfig.ModelConfig.Provider = cfg.ASR.Provider
		asrConfig.DecodingMethod = cfg.ASR.Nemotron.DecodingMethod
		if asrConfig.DecodingMethod == "" {
			asrConfig.DecodingMethod = "greedy_search"
		}
		asrConfig.MaxActivePaths = cfg.ASR.Nemotron.MaxActivePaths
		asrConfig.EnableEndpoint = cfg.ASR.Nemotron.EnableEndpoint
		asrConfig.Rule1MinTrailingSilence = cfg.ASR.Nemotron.Rule1MinTrailingSilence
		asrConfig.Rule2MinTrailingSilence = cfg.ASR.Nemotron.Rule2MinTrailingSilence
		asrConfig.Rule3MinUtteranceLength = cfg.ASR.Nemotron.Rule3MinUtteranceLength

		recognizer = sherpa.NewOnlineRecognizer(&asrConfig)
		if recognizer == nil {
			log.Fatal("[ASR] Failed to create Nemotron recognizer")
		}
		defer sherpa.DeleteOnlineRecognizer(recognizer)

		asrStream = sherpa.NewOnlineStream(recognizer)
		if asrStream == nil {
			log.Fatal("[ASR] Failed to create Nemotron stream")
		}
		defer sherpa.DeleteOnlineStream(asrStream)
		if lang := cfg.ASR.Nemotron.Language; lang != "" {
			asrStream.SetOption("language", lang)
		}
		log.Println("[ASR] Streaming Nemotron recognizer ready")

	case "omnilingual":
		offlineConfig := sherpa.OfflineRecognizerConfig{}
		offlineConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
		offlineConfig.ModelConfig.ZipformerCtc.Model = join(cfg.ASR.Omnilingual.ModelDir, cfg.ASR.Omnilingual.Model)
		offlineConfig.ModelConfig.Tokens = join(cfg.ASR.Omnilingual.ModelDir, cfg.ASR.Omnilingual.Tokens)
		offlineConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
		offlineConfig.ModelConfig.Provider = cfg.ASR.Provider
		offlineConfig.DecodingMethod = "greedy_search"

		offlineRecognizer = sherpa.NewOfflineRecognizer(&offlineConfig)
		if offlineRecognizer == nil {
			log.Fatal("[ASR] Failed to create Omnilingual recognizer")
		}
		defer sherpa.DeleteOfflineRecognizer(offlineRecognizer)
		log.Println("[ASR] Offline Omnilingual recognizer ready (1600+ languages)")

	case "transducer":
		asrConfig := sherpa.OnlineRecognizerConfig{}
		asrConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
		asrConfig.ModelConfig.Transducer.Encoder = join(cfg.ASR.Transducer.ModelDir, cfg.ASR.Transducer.Encoder)
		asrConfig.ModelConfig.Transducer.Decoder = join(cfg.ASR.Transducer.ModelDir, cfg.ASR.Transducer.Decoder)
		asrConfig.ModelConfig.Transducer.Joiner = join(cfg.ASR.Transducer.ModelDir, cfg.ASR.Transducer.Joiner)
		asrConfig.ModelConfig.Tokens = join(cfg.ASR.Transducer.ModelDir, cfg.ASR.Transducer.Tokens)
		asrConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
		asrConfig.ModelConfig.Provider = cfg.ASR.Provider
		asrConfig.DecodingMethod = cfg.ASR.Transducer.DecodingMethod
		if asrConfig.DecodingMethod == "" {
			asrConfig.DecodingMethod = "greedy_search"
		}
		asrConfig.MaxActivePaths = cfg.ASR.Transducer.MaxActivePaths
		asrConfig.EnableEndpoint = cfg.ASR.Transducer.EnableEndpoint
		asrConfig.Rule1MinTrailingSilence = cfg.ASR.Transducer.Rule1MinTrailingSilence
		asrConfig.Rule2MinTrailingSilence = cfg.ASR.Transducer.Rule2MinTrailingSilence
		asrConfig.Rule3MinUtteranceLength = cfg.ASR.Transducer.Rule3MinUtteranceLength

		recognizer = sherpa.NewOnlineRecognizer(&asrConfig)
		if recognizer == nil {
			log.Fatal("[ASR] Failed to create Transducer recognizer")
		}
		defer sherpa.DeleteOnlineRecognizer(recognizer)

		asrStream = sherpa.NewOnlineStream(recognizer)
		if asrStream == nil {
			log.Fatal("[ASR] Failed to create Transducer stream")
		}
		defer sherpa.DeleteOnlineStream(asrStream)
		log.Println("[ASR] Streaming Transducer recognizer ready")

	default:
		log.Fatalf("[ASR] Unknown active_model: %q (valid: qwen3, nemotron, omnilingual, transducer)", cfg.ASR.ActiveModel)
	}

	// Initialize dual-mode fallback recognizer if enabled
	if cfg.ASR.DualMode.Enabled && cfg.ASR.DualMode.FallbackModel != "" {
		switch cfg.ASR.DualMode.FallbackModel {
		case "qwen3":
			fbConfig := sherpa.OfflineRecognizerConfig{}
			fbConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
			fbConfig.ModelConfig.Qwen3ASR.ConvFrontend = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.ConvFrontend)
			fbConfig.ModelConfig.Qwen3ASR.Encoder = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.Encoder)
			fbConfig.ModelConfig.Qwen3ASR.Decoder = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.Decoder)
			fbConfig.ModelConfig.Qwen3ASR.Tokenizer = join(cfg.ASR.Qwen3.ModelDir, cfg.ASR.Qwen3.Tokenizer)
			fbConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
			fbConfig.ModelConfig.Provider = cfg.ASR.Provider
			fbConfig.DecodingMethod = "greedy_search"
			fallbackOfflineRecognizer = sherpa.NewOfflineRecognizer(&fbConfig)
		case "omnilingual":
			fbConfig := sherpa.OfflineRecognizerConfig{}
			fbConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
			fbConfig.ModelConfig.ZipformerCtc.Model = join(cfg.ASR.Omnilingual.ModelDir, cfg.ASR.Omnilingual.Model)
			fbConfig.ModelConfig.Tokens = join(cfg.ASR.Omnilingual.ModelDir, cfg.ASR.Omnilingual.Tokens)
			fbConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
			fbConfig.ModelConfig.Provider = cfg.ASR.Provider
			fbConfig.DecodingMethod = "greedy_search"
			fallbackOfflineRecognizer = sherpa.NewOfflineRecognizer(&fbConfig)
		}
		if fallbackOfflineRecognizer != nil {
			defer sherpa.DeleteOfflineRecognizer(fallbackOfflineRecognizer)
			log.Printf("[ASR] Dual-mode fallback ready: %s\n", cfg.ASR.DualMode.FallbackModel)
		}
	}

	_ = fallbackOfflineRecognizer // used in dual-mode audio processing below

	// 4. Initialize TTS
	usingCustomPocket := cfg.TTS.ActiveModel == "pocket" && cfg.TTS.Pocket.Bundle != ""
	var tts *sherpa.OfflineTts
	var pocketTTS *pockettts.Engine
	ttsSampleRate := 0

	if usingCustomPocket {
		voice := cfg.TTS.Pocket.Voice
		if cfg.TTS.VoiceCloning.Enabled && cfg.TTS.VoiceCloning.ReferenceAudio != "" {
			voice = cfg.TTS.VoiceCloning.ReferenceAudio
		}
		var err error
		pocketTTS, err = pockettts.New(pockettts.Config{
			ModelDir:        cfg.TTS.Pocket.ModelDir,
			Bundle:          cfg.TTS.Pocket.Bundle,
			Tokenizer:       cfg.TTS.Pocket.Tokenizer,
			BosBeforeVoice:  cfg.TTS.Pocket.BosBeforeVoice,
			LmFlow:          cfg.TTS.Pocket.LmFlow,
			LmMain:          cfg.TTS.Pocket.LmMain,
			Encoder:         cfg.TTS.Pocket.Encoder,
			Decoder:         cfg.TTS.Pocket.Decoder,
			TextConditioner: cfg.TTS.Pocket.TextConditioner,
			Precision:       cfg.TTS.Pocket.Precision,
			Temperature:     cfg.TTS.Pocket.Temperature,
			LSDSteps:        cfg.TTS.Pocket.LSDSteps,
			NumThreads:      cfg.TTS.NumThreads,
			Voice:           voice,
		})
		if err != nil {
			log.Fatalf("Failed to create custom Pocket TTS: %v", err)
		}
		defer pocketTTS.Close()
		ttsSampleRate = pocketTTS.SampleRate()
		log.Printf("[TTS] Custom Pocket bundle ready: %s (voice: %s)", cfg.TTS.Pocket.ModelDir, voice)
	} else {
		ttsConfig := sherpa.OfflineTtsConfig{}
		ttsConfig.Model.NumThreads = cfg.TTS.NumThreads
		ttsConfig.Model.Debug = cfg.TTS.Debug
		ttsConfig.Model.Provider = cfg.TTS.Provider

		switch cfg.TTS.ActiveModel {
		case "supertonic":
			ttsConfig.Model.Supertonic.DurationPredictor = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.DurationPredictor)
			ttsConfig.Model.Supertonic.TextEncoder = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.TextEncoder)
			ttsConfig.Model.Supertonic.VectorEstimator = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.VectorEstimator)
			ttsConfig.Model.Supertonic.Vocoder = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.Vocoder)
			ttsConfig.Model.Supertonic.TtsJson = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.TTSJson)
			ttsConfig.Model.Supertonic.UnicodeIndexer = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.UnicodeIndexer)
			ttsConfig.Model.Supertonic.VoiceStyle = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.VoiceStyle)
		case "kokoro":
			ttsConfig.Model.Kokoro.Model = join(cfg.TTS.Kokoro.ModelDir, cfg.TTS.Kokoro.Model)
			ttsConfig.Model.Kokoro.Voices = join(cfg.TTS.Kokoro.ModelDir, cfg.TTS.Kokoro.Voices)
			ttsConfig.Model.Kokoro.Tokens = join(cfg.TTS.Kokoro.ModelDir, cfg.TTS.Kokoro.Tokens)
			ttsConfig.Model.Kokoro.DataDir = join(cfg.TTS.Kokoro.ModelDir, cfg.TTS.Kokoro.DataDir)
			if cfg.TTS.Kokoro.Lexicon != "" {
				ttsConfig.Model.Kokoro.Lexicon = join(cfg.TTS.Kokoro.ModelDir, cfg.TTS.Kokoro.Lexicon)
			}
			ttsConfig.Model.Kokoro.Lang = cfg.TTS.Kokoro.Lang
			ttsConfig.Model.Kokoro.LengthScale = cfg.TTS.Kokoro.LengthScale
		case "pocket":
			ttsConfig.Model.Pocket.LmFlow = join(cfg.TTS.Pocket.ModelDir, cfg.TTS.Pocket.LmFlow)
			ttsConfig.Model.Pocket.LmMain = join(cfg.TTS.Pocket.ModelDir, cfg.TTS.Pocket.LmMain)
			ttsConfig.Model.Pocket.Encoder = join(cfg.TTS.Pocket.ModelDir, cfg.TTS.Pocket.Encoder)
			ttsConfig.Model.Pocket.Decoder = join(cfg.TTS.Pocket.ModelDir, cfg.TTS.Pocket.Decoder)
			ttsConfig.Model.Pocket.TextConditioner = join(cfg.TTS.Pocket.ModelDir, cfg.TTS.Pocket.TextConditioner)
			ttsConfig.Model.Pocket.VocabJson = join(cfg.TTS.Pocket.ModelDir, cfg.TTS.Pocket.VocabJson)
			ttsConfig.Model.Pocket.TokenScoresJson = join(cfg.TTS.Pocket.ModelDir, cfg.TTS.Pocket.TokenScoresJson)
			if n := cfg.TTS.Pocket.VoiceEmbeddingCacheCapacity; n > 0 {
				ttsConfig.Model.Pocket.VoiceEmbeddingCacheCapacity = n
			}
		case "zipvoice":
			ttsConfig.Model.Zipvoice.Encoder = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Encoder)
			ttsConfig.Model.Zipvoice.Decoder = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Decoder)
			ttsConfig.Model.Zipvoice.DataDir = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.DataDir)
			ttsConfig.Model.Zipvoice.Lexicon = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Lexicon)
			ttsConfig.Model.Zipvoice.Tokens = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Tokens)
			ttsConfig.Model.Zipvoice.Vocoder = cfg.TTS.ZipVoice.Vocoder
		}

		tts = sherpa.NewOfflineTts(&ttsConfig)
		if tts == nil {
			log.Fatal("Failed to create TTS")
		}
		defer sherpa.DeleteOfflineTts(tts)
		ttsSampleRate = tts.SampleRate()
	}

	// Get TTS output sample rate and create resamplers for browser (44.1k)
	// and echo reference (16k).
	log.Printf("[TTS] Engine sample rate: %d Hz", ttsSampleRate)
	if cfg.TTS.ActiveModel == "pocket" {
		mode := "buffered"
		if cfg.TTS.Pocket.Streaming {
			mode = "streaming"
		}
		log.Printf("[TTS] Pocket audio delivery: %s", mode)
	}
	ttsToBrowserResampler := newResampler(ttsSampleRate, 44100)
	ttsToEchoResampler := newResampler(ttsSampleRate, 16000)
	ttsDefaultSpeed := cfg.TTS.BaseSpeed
	if ttsDefaultSpeed == 0 {
		ttsDefaultSpeed = cfg.TTS.Supertonic.Speed
	}
	if ttsDefaultSpeed == 0 {
		ttsDefaultSpeed = 1.0
	}

	// Voice cloning: load reference audio if enabled and active Sherpa model
	// supports it. The custom Pocket runtime loads its reference WAV directly.
	//
	// The official Sherpa Pocket export ships no built-in voice, so a reference
	// WAV is mandatory: without one GetVoiceEmbedding returns null and Generate
	// produces silence. A missing/unreadable file must therefore be a loud
	// failure rather than a silent mute.
	pocketNeedsReference := cfg.TTS.ActiveModel == "pocket" && !usingCustomPocket
	referenceAudioPath := cfg.TTS.VoiceCloning.ReferenceAudio
	if cfg.TTS.VoiceCloning.Enabled && referenceAudioPath == "" && pocketNeedsReference {
		referenceAudioPath = cfg.TTS.Pocket.Voice
	}

	var refAudio []float32
	var refSampleRate int
	voiceCloneEnabled := cfg.TTS.VoiceCloning.Enabled &&
		!usingCustomPocket && (cfg.TTS.ActiveModel == "pocket" || cfg.TTS.ActiveModel == "zipvoice")
	if voiceCloneEnabled && referenceAudioPath != "" {
		wav := sherpa.ReadWaveMultiChannel(referenceAudioPath)
		if wav != nil && wav.SamplesPerChannel > 0 && wav.SampleRate > 0 {
			// Mix down to mono if stereo. ReadWaveMultiChannel returns
			// channel-major (planar) data: channel ch starts at
			// ch*SamplesPerChannel.
			if wav.ChannelCount > 1 {
				mono := make([]float32, wav.SamplesPerChannel)
				for i := 0; i < wav.SamplesPerChannel; i++ {
					sum := float32(0)
					for ch := 0; ch < wav.ChannelCount; ch++ {
						sum += wav.Samples[ch*wav.SamplesPerChannel+i]
					}
					mono[i] = sum / float32(wav.ChannelCount)
				}
				refAudio = mono
			} else {
				refAudio = make([]float32, wav.SamplesPerChannel)
				copy(refAudio, wav.Samples[:wav.SamplesPerChannel])
			}
			refSampleRate = wav.SampleRate
			channels := wav.ChannelCount
			fullLen := len(refAudio)
			if maxSeconds := cfg.TTS.VoiceCloning.MaxReferenceAudioLen; maxSeconds > 0 {
				maxSamples := int(float64(refSampleRate) * float64(maxSeconds))
				if maxSamples > 0 && len(refAudio) > maxSamples {
					refAudio = refAudio[:maxSamples]
				}
			}
			wav.Release()
			log.Printf("[TTS] Voice cloning loaded: %s (%d samples, %d Hz, %d ch)", referenceAudioPath, len(refAudio), refSampleRate, channels)
			if len(refAudio) < fullLen {
				log.Printf("[TTS] Voice cloning reference limited to %.1fs of %.1fs by voice_cloning.max_reference_audio_len — raise it for a stronger clone",
					float64(len(refAudio))/float64(refSampleRate), float64(fullLen)/float64(refSampleRate))
			}
		} else {
			sr := 0
			ns := 0
			if wav != nil {
				sr = wav.SampleRate
				ns = wav.SamplesPerChannel
				wav.Release()
			}
			if pocketNeedsReference {
				log.Fatalf("[TTS] Voice cloning reference %q could not be read (sample_rate=%d, samples=%d). "+
					"The Pocket model has no built-in voice, so it cannot synthesize without a reference WAV. "+
					"Fix voice_cloning.reference_audio (PCM16 WAV) or point pocket.voice at one.",
					referenceAudioPath, sr, ns)
			}
			log.Printf("[TTS] WARNING: Failed to load reference audio %s (sample_rate=%d, samples=%d), falling back to default voice",
				referenceAudioPath, sr, ns)
			voiceCloneEnabled = false
		}
	}
	if pocketNeedsReference && refAudio == nil {
		log.Fatalf("[TTS] Pocket requires a voice-cloning reference WAV: set tts.voice_cloning.enabled=true with " +
			"voice_cloning.reference_audio (PCM16 WAV), or point pocket.voice at one. Without it Pocket generates silence.")
	}

	// Pocket's flow-matching sampler is stochastic: every frame is seeded from
	// Gaussian noise. Left at the engine default (temperature 0.7, random seed)
	// the same sentence comes back with a different timbre and level each time,
	// which makes a cloned voice sound unstable. These knobs pin it down.
	pocketExtra := map[string]any{}
	if t := cfg.TTS.Pocket.Temperature; t > 0 {
		pocketExtra["temperature"] = t
	}
	if cfg.TTS.Pocket.Seed != nil {
		pocketExtra["seed"] = *cfg.TTS.Pocket.Seed
	}
	var pocketExtraJSON json.RawMessage
	if len(pocketExtra) > 0 {
		if encoded, err := json.Marshal(pocketExtra); err == nil {
			pocketExtraJSON = encoded
			log.Printf("[TTS] Pocket sampling pinned: %s", string(encoded))
		}
	}
	if cfg.TTS.ActiveModel == "pocket" && cfg.TTS.Pocket.Seed == nil {
		log.Printf("[TTS] Pocket seed not set — voice timbre and loudness will vary per sentence. " +
			"Set tts.pocket.seed (e.g. 42) for a stable clone.")
	}

	// Makeup gain: style presets attenuate ("calm" = 0.9) and Pocket ignores
	// speed entirely, so a preset can only ever make Pocket quieter. This
	// restores the level without disabling the style's expression.
	ttsOutputGain := cfg.TTS.OutputGain
	if ttsOutputGain <= 0 {
		ttsOutputGain = 1.0
	}
	if ttsOutputGain != 1.0 {
		log.Printf("[TTS] Output gain: %.2fx", ttsOutputGain)
	}

	// Pitch shifting runs on the decoded audio, so it is the one expressive
	// control that works on Pocket (whose ONNX graph ignores speed). It is off
	// unless enabled: per-sentence pitch changes trade vocal consistency for
	// expression, and a clone is recognised by its consistency.
	// The exciter is the fix for the band-limited model's dull top end: Pocket is
	// 24 kHz, so its real content stops near 12 kHz and nothing can put that
	// back. Synthesising harmonics into the air band restores the brightness
	// perceptually. 0 disables it.
	exciterCfg := defaultExciterConfig(ttsSampleRate, float64(cfg.TTS.ExciterAmount))
	if !cfg.TTS.PitchShift {
		log.Printf("[TTS] Pitch shaping off (tts.pitch_shift) — all sentences keep the style's pitch")
	}
	if exciterCfg.amount > 0 {
		log.Printf("[TTS] Brightness exciter on (amount=%.2f, presence>=%.0f Hz, air %.0f-%.0f Hz)",
			exciterCfg.amount, exciterCfg.presence, exciterCfg.bandLo, exciterCfg.bandHi)
	}
	// Loudness normalisation. Measured Pocket output is -25.3 dBFS RMS, about
	// 9 dB below normal program level, and the makeup gain only cancels the
	// style preset's 0.9 — so the engine's own deficit used to reach the speaker
	// untouched, which is what "the volume is at 100% but she sounds quiet" is.
	voiceTargetRMS := 0.0
	if cfg.TTS.NormalizeLoudness == nil || *cfg.TTS.NormalizeLoudness {
		targetDB := cfg.TTS.TargetLoudnessDBFS
		if targetDB >= 0 {
			targetDB = voiceTargetRMSDefault
		}
		voiceTargetRMS = dbfsToLinear(targetDB)
		log.Printf("[TTS] Loudness normalisation on (target %.1f dBFS, peak ceiling %.2f)",
			targetDB, limiterCeiling)
	} else {
		log.Printf("[TTS] Loudness normalisation off — the engine's raw output level reaches the speaker")
	}

	// synthesize picks the right TTS method based on active model and voice
	// cloning config. Pocket audio delivery is controlled by pocket.streaming;
	// other models continue through Sherpa's GenerationConfig API.
	synthesize := func(text string, speed float32, cb func([]float32) bool) {
		// Sherpa exposes Pocket audio through a callback. Keep that callback
		// streaming by default, but allow a config switch to collect the whole
		// utterance before playback. The stop check is still honored while the
		// model is generating so barge-in can interrupt buffered generation.
		streamAudio := cfg.TTS.ActiveModel != "pocket" || cfg.TTS.Pocket.Streaming
		deliver := cb
		var buffered []float32
		if !streamAudio {
			deliver = func(samples []float32) bool {
				if atomic.LoadInt32(&stopPlayback) != 0 {
					return false
				}
				if len(samples) > 0 {
					buffered = append(buffered, samples...)
				}
				return true
			}
		}

		if pocketTTS != nil {
			ttsMu.Lock()
			err := pocketTTS.Generate(text, speed, deliver)
			ttsMu.Unlock()
			if err != nil && !errors.Is(err, pockettts.ErrGenerationStopped) {
				log.Printf("[TTS] Custom Pocket generation failed: %v", err)
			}
		} else {
			// Build the per-request config.
			// Supertonic needs NumSteps + Extra (lang). Kokoro needs Sid + Extra (lang).
			// Pocket/ZipVoice need Speed + ReferenceAudio (+ Pocket's sampling extras).
			genCfg := &sherpa.GenerationConfig{
				Speed: speed,
				Sid:   cfg.TTS.Supertonic.Sid, // Default speaker id; kokoro overrides with its own below
			}
			if cfg.TTS.ActiveModel == "supertonic" {
				genCfg.NumSteps = cfg.TTS.Supertonic.NumSteps
				if cfg.TTS.Supertonic.Extra != "" {
					genCfg.Extra = json.RawMessage(cfg.TTS.Supertonic.Extra)
				}
			} else if cfg.TTS.ActiveModel == "kokoro" {
				genCfg.Sid = cfg.TTS.Kokoro.Sid
				// Kokoro uses Extra for language (e.g., {"lang": "en-us"})
				if cfg.TTS.Kokoro.Lang != "" {
					genCfg.Extra = json.RawMessage(fmt.Sprintf(`{"lang": "%s"}`, cfg.TTS.Kokoro.Lang))
				}
			}
			// Voice cloning reference (Pocket/ZipVoice). The reference must ride
			// on the same request as the sampling extras, otherwise a cloned
			// Pocket request would silently fall back to the engine's random
			// seed/temperature and the voice would drift sentence to sentence.
			if voiceCloneEnabled && refAudio != nil {
				genCfg.ReferenceAudio = refAudio
				genCfg.ReferenceSampleRate = refSampleRate
				genCfg.ReferenceText = cfg.TTS.VoiceCloning.ReferenceText
			}
			// Pocket's sampling extras are Pocket-specific: zipvoice ignores
			// unknown extra keys but should not receive them at all.
			if cfg.TTS.ActiveModel == "pocket" && len(pocketExtraJSON) > 0 {
				genCfg.Extra = pocketExtraJSON
			}
			ttsMu.Lock()
			tts.GenerateWithConfig(text, genCfg, func(samples []float32, _ float32) bool {
				return deliver(samples)
			})
			ttsMu.Unlock()
		}

		if !streamAudio && atomic.LoadInt32(&stopPlayback) == 0 && len(buffered) > 0 {
			cb(buffered)
		}
	}

	// Realtime TTS: streamed sentences are enqueued here and played one at a
	// time by a single player goroutine, so the first audio starts as soon as
	// the first sentence is ready and playback never overlaps. The ttsItem type
	// and drainTTSBatch live in tts_batch.go.
	ttsSentCh := make(chan ttsItem, 64)
	// ttsRenderedCh carries the renderer's output to the player: sentences are
	// synthesized AHEAD of playback (prefetch) so synthesis latency overlaps
	// with the audio of the previous sentence instead of pausing between them.
	// Capped and backpressured: when full, the renderer blocks on synthesis
	// completion, bounding prefetch memory (~4 sentences of 44.1kHz audio).
	ttsRenderedCh := make(chan ttsItem, 4)
	// latestTurn is the newest turn id handed to the queue. The player reads it
	// to drop the tail of an answer the user already interrupted: those
	// superseded sentences sit AHEAD of the new turn in the FIFO queue, so
	// comparing against what is playing right now can never see them.
	var latestTurn atomic.Int64
	// ttsShutdown stops the TTS player; ttsWG tracks every goroutine that can
	// be inside sherpa's GenerateWithConfig. Both are joined during shutdown
	// BEFORE the deferred DeleteOfflineTts frees the engine — destroying it
	// mid-generation is an access violation.
	var ttsWG sync.WaitGroup
	ttsShutdown := make(chan struct{})

	log.Printf("[TTS] Synthesizer ready (%s)", cfg.TTS.ActiveModel)

	// speak enqueues a sentence for the renderer + streaming player below.
	// Keeping a single consumer serializes all TTS so sentences never
	// overlap, and lets barge-in drain the queue.
	speak := func(text string, speed, volume, pitch float32, seq int64) {
		if speed == 0 {
			speed = ttsDefaultSpeed
		}
		if volume == 0 {
			volume = 1.0
		}
		if seq > 0 {
			latestTurn.Store(seq)
		}
		ttsSentCh <- ttsItem{text: text, speed: speed, volume: volume, pitch: pitch, seq: seq}
	}

	// speakTurn says a complete, self-contained utterance: the sentence plus the
	// end-of-turn marker. Used by one-shot callers so the player finalizes the
	// transcript as soon as the sentence has been spoken instead of waiting for
	// a marker that never comes.
	speakTurn := func(text string, speed, volume, pitch float32, seq int64) {
		speak(text, speed, volume, pitch, seq)
		ttsSentCh <- ttsItem{seq: seq, final: true}
	}

	// renderSentence synthesizes ONE sentence ahead of playback (prefetch). It
	// is called by the renderer goroutine while the previous sentence is still
	// playing, so Supertonic's ~0.6-2.2s synthesis latency overlaps with audio
	// instead of appearing as a silent gap between sentences. It returns the
	// fully coloured samples at the engine's native rate, or nil when the
	// sentence's turn was superseded while it was being synthesized.
	renderSentence := func(text string, speed, volume, pitch float32, seq int64) []float32 {
		if speed == 0 {
			speed = ttsDefaultSpeed
		}
		// Clamp volume to a sane range to avoid clipping/distortion, then
		// scale every sample so the emotion-adaptive Volume actually takes
		// effect (louder for happy/excited, softer for sad/stressed/calm).
		if volume == 0 {
			volume = 1.0
		}
		volume = float32(math.Max(0.5, math.Min(1.2, float64(volume))))
		// ttsOutputGain is the master trim. It no longer has to carry the whole
		// level: loudness normalisation below brings the utterance to a fixed
		// reference first, so the style volume is an offset from that reference
		// rather than from the engine's own (very low) output level.
		// Pitch is only honoured when enabled; otherwise every sentence keeps
		// the style's constant pitch, which is what keeps a clone recognisable.
		if !cfg.TTS.PitchShift {
			pitch = 1.0
		}
		if pitch == 0 {
			pitch = 1.0
		}

		// Collect the raw engine output first, then run the colour chain once
		// over the whole sentence: the pitch shifter needs the complete buffer,
		// and the exciter's filters need contiguous state.
		out := make([]float32, 0, 44100*4)
		synthesize(text, speed, func(samples []float32) bool {
			// Abort mid-synthesis only when the turn itself was replaced
			// (barge-in answered by a new reply). Deliberately NOT keyed on
			// stopPlayback: the player clears that flag when the new turn's
			// first item arrives, and the renderer runs ahead of the player —
			// dropping on stopPlayback here could discard a fresh turn's
			// first sentence before the flag is cleared.
			if seq > 0 && seq < latestTurn.Load() {
				return false
			}
			out = append(out, samples...)
			return true
		})
		if seq > 0 && seq < latestTurn.Load() {
			return nil // superseded while synthesizing
		}
		if len(out) == 0 {
			return nil
		}
		rendered := colourVoice(out, colourConfig{
			sampleRate: ttsSampleRate,
			volume:     volume,
			pitch:      pitch,
			outputGain: ttsOutputGain,
			targetRMS:  voiceTargetRMS,
			exciter:    exciterCfg,
		})
		// Level diagnostics: without these, "quiet", "clipping" and "too quiet
		// to hear" are indistinguishable in the logs. The gain is the
		// normaliser's own verdict on this utterance: a value pinned at the
		// +12 dB cap means the engine came back quieter than normalisation can
		// rescue, and "skipped" means it found no speech at all — neither of
		// which the raw/out levels alone can tell apart.
		gainNote := "off"
		if voiceTargetRMS > 0 {
			gainNote = "skipped (no speech in engine output)"
			if gain, ok := loudnessGain(out, ttsSampleRate, voiceTargetRMS); ok {
				gainNote = fmt.Sprintf("%.2fx (%+.1f dB)", gain, 20*math.Log10(gain))
			}
		}
		log.Printf("[TTS-LEVEL] raw rms=%.4f (%.1f dBFS) peak=%.3f -> out rms=%.4f (%.1f dBFS) peak=%.3f | gain %s | %.2fs @ %d Hz",
			rmsLevel(out), levelDBFS(rmsLevel(out)), peakOf(out),
			rmsLevel(rendered), levelDBFS(rmsLevel(rendered)), peakOf(rendered),
			gainNote, float64(len(rendered))/float64(ttsSampleRate), ttsSampleRate)
		return rendered
	}

	// playRendered plays pre-rendered samples, halting immediately if a
	// barge-in sets stopPlayback mid-utterance. It reports whether the audio
	// was rendered on the local speaker (and so mirrored to the browser as a
	// muted chunk), so the player's end-of-turn chunk carries the same flag.
	playRendered := func(samples []float32) bool {
		local := cfg.Audio.TTSPlayLocalAlways || !companionHasClient()
		if len(samples) == 0 {
			return local
		}
		// 200 ms of audio at the ENGINE's rate. This was a hardcoded 8820 samples
		// (200 ms at 44.1 kHz), which is 367 ms of a 24 kHz Pocket buffer, and
		// the duration below divided by 44100 as well — so a Pocket sentence
		// reported 1.84x its real length (a 40 s utterance logged as 21.77 s).
		chunkFrames := ttsSampleRate / 5
		if chunkFrames < 1 {
			chunkFrames = 8820
		}
		log.Printf("[TTS-PLAY] Playing rendered audio (%.2fs @ %d Hz)",
			float64(len(samples))/float64(ttsSampleRate), ttsSampleRate)

		if local {
			// Always render to the local speaker so Mai is audible regardless
			// of the companion tab's autoplay state. The playback callback
			// feeds the echo reference (audio.go), so barge-in AEC still
			// cancels Mai's own voice. The same chunks are mirrored to the
			// companion muted: the tab plays them at zero gain so its
			// lip-sync clock, viseme schedule and speaking state follow the
			// exact audio the user hears instead of the mouth staying frozen.
			// The pre-rendered buffer is sliced into ~200ms pieces so the
			// browser keeps its crossfade/streaming semantics.
			_ = playAudioStreaming(ctx, 44100, &stopPlayback, func(ch chan<- []float32) {
				for len(samples) > 0 {
					if atomic.LoadInt32(&stopPlayback) != 0 {
						return
					}
					n := len(samples)
					if n > chunkFrames {
						n = chunkFrames
					}
					chunk := samples[:n]
					samples = samples[n:]
					resampled := ttsToBrowserResampler.resample(chunk)
					publishTTSAudioChunk(bus, resampled, 44100, false, true)
					ch <- resampled
				}
			})
		} else {
			// Opt-in browser-only path (tts_play_local_always: false): stream
			// chunks to the companion tab. Echo reference is fed manually
			// since the local speaker isn't used here, and publishing is
			// paced at real time so the tab's playback timeline matches.
			for len(samples) > 0 {
				if atomic.LoadInt32(&stopPlayback) != 0 {
					break
				}
				n := len(samples)
				if n > chunkFrames {
					n = chunkFrames
				}
				chunk := samples[:n]
				samples = samples[n:]
				refBuffer.Push(ttsToEchoResampler.resample(chunk))
				publishTTSAudioChunk(bus, ttsToBrowserResampler.resample(chunk), 44100, false, false)
				// Pace: this chunk represents 200ms of audio.
				select {
				case <-ctx.Done():
					return local
				case <-time.After(200 * time.Millisecond):
				}
			}
		}

		lastResponseMu.Lock()
		lastResponseTime = time.Now()
		lastResponseMu.Unlock()
		return local
	}

	// Renderer goroutine: prefetches synthesis. It sits between the sentence
	// queue (fed by the orchestrator) and the player, and synthesizes the next
	// sentence WHILE the player is still speaking the current one. Without it,
	// synthesis and playback were serialized in the player loop, so
	// Supertonic's ~0.6-2.2s per-sentence latency appeared as a silent pause
	// between every sentence. With it, synthesis overlaps playback and a
	// multi-sentence reply plays back-to-back as long as synthesis keeps up
	// (measured ~2.5-3x realtime on this machine).
	//
	// Pocket needs more than prefetch. Its Generate() pays a large FIXED cost
	// per call (a fresh LM state plus the EOS search loop) and only reuses
	// state within one call, so measured realtime factor collapses on short
	// input: 6 chars 1.13x, 24 chars 2.04x, 61 chars 2.23x, 100 chars 2.20x.
	// The LLM streaming handoff emits short sentences (clause flush at 60
	// chars), so one-at-a-time synthesis left Pocket barely ahead of playback
	// and sentences arrived with silent gaps. Merging the sentences already
	// waiting in the queue into a single request amortizes that fixed cost —
	// and because the drain is non-blocking it adds no latency to the first
	// sentence. Supertonic (4.58x, cheap per call) does not need this and keeps
	// its existing one-sentence-per-request behavior.
	pocketBatching := cfg.TTS.ActiveModel == "pocket" && pocketTTS == nil &&
		(cfg.TTS.Pocket.BatchSentences == nil || *cfg.TTS.Pocket.BatchSentences)
	pocketBatchMaxChars := cfg.TTS.Pocket.BatchMaxChars
	if pocketBatchMaxChars <= 0 {
		pocketBatchMaxChars = 160
	}
	if pocketBatching {
		log.Printf("[TTS] Pocket sentence batching on (max %d chars per request)", pocketBatchMaxChars)
	}
	// drainPocketBatch adapts drainTTSBatch (tts_batch.go) to this queue. See
	// tts_batch.go for why Pocket needs it and for the measured numbers.
	drainPocketBatch := func(first ttsItem) (string, *ttsItem, bool) {
		return drainTTSBatch(ttsSentCh, first, pocketBatchMaxChars, &latestTurn)
	}

	ttsWG.Add(1)
	go func() {
		defer ttsWG.Done()
		defer close(ttsRenderedCh)
		var deferred *ttsItem // item absorbed but not consumed by a batch drain
		var batchedSeq int64  // turn whose first request has already been synthesized
		queueClosed := false
		for {
			var item ttsItem
			if deferred != nil {
				item, deferred = *deferred, nil
			} else {
				if queueClosed {
					return
				}
				select {
				case <-ttsShutdown:
					return
				case it, ok := <-ttsSentCh:
					if !ok {
						return
					}
					item = it
				}
			}
			// Superseded turn: drop before wasting synthesis time.
			if item.seq > 0 && item.seq < latestTurn.Load() {
				continue
			}
			if item.final {
				// Markers pass straight through — the player's
				// end-of-turn logic is order-sensitive.
				ttsRenderedCh <- ttsItem{seq: item.seq, final: true}
				continue
			}

			text := item.text
			// The FIRST request of a turn is synthesized alone; only what is
			// already queued behind it is merged. Batching the first request too
			// is what left her silent for seconds before the first word: when the
			// whole reply is already queued (a fast model, or a canned answer) it
			// collapses into one request and nothing plays until all of it has
			// been synthesized. A lone first sentence costs nothing, because the
			// drain is non-blocking. seq == 0 has no turn concept and keeps its
			// previous behaviour.
			if pocketBatching && (item.seq == 0 || item.seq == batchedSeq) {
				merged, next, open := drainPocketBatch(item)
				text = merged
				if next != nil {
					deferred = next
				}
				if !open {
					queueClosed = true
				}
			} else if pocketBatching {
				batchedSeq = item.seq
			}

			samples := renderSentence(text, item.speed, item.volume, item.pitch, item.seq)
			if samples == nil {
				// Superseded while synthesizing.
				continue
			}
			ttsRenderedCh <- ttsItem{
				text: text, seq: item.seq, samples: samples,
			}
		}
	}()

	// Player goroutine: consumes the sentence queue sequentially. Every sentence
	// of one reply shares the orchestrator's turn id, and a turn is closed by an
	// explicit end-of-turn item — never by "the queue looks empty right now".
	// That distinction is the sync fix: an empty queue mid-reply means the LLM is
	// still writing the next sentence, not that the answer is over. When a newer
	// turn arrives we drop the superseded answer's queued tail and close its
	// transcript, while the new reply (the highest turn id) is always preserved.
	ttsWG.Add(1)
	go func() {
		defer ttsWG.Done()
		var playingSeq int64 // turn currently being spoken (0 = none)
		var turnOpen bool    // its transcript was announced and is not yet closed
		var mirrored bool    // the turn is rendered locally; the browser chunk is muted
		// closeTurn releases everything the player holds for one turn, and
		// finalizes the browser transcript and its audio stream together so the
		// text, the viseme schedule and the speaking state all end at once.
		closeTurn := func() {
			if !turnOpen {
				return
			}
			turnOpen = false
			atomic.StoreInt32(&isSpeaking, 0)
			atomic.StoreInt32(&ttsPlaying, 0)
			atomic.StoreInt32(&stopPlayback, 0)
			lastTTSEndNano.Store(time.Now().UnixNano())
			publishTTSAudioChunk(bus, nil, 44100, true, mirrored)
			publishTranscript(bus, "", true)
		}
		for {
			select {
			case <-ttsShutdown:
				closeTurn()
				return
			case item, ok := <-ttsRenderedCh:
				if !ok {
					closeTurn()
					return
				}
				// A newer turn was requested while this one was still queued: the
				// interrupted answer's tail is discarded instead of playing over
				// the reply that replaced it.
				if item.seq > 0 && item.seq < latestTurn.Load() {
					log.Printf("[TTS-PLAYER] Dropping superseded sentence (turn %d < %d): %.60s...",
						item.seq, latestTurn.Load(), item.text)
					continue
				}
				if item.seq > playingSeq {
					// Done with the superseded reply: close its transcript so the
					// new answer opens its own message, and clear any barge-in
					// halt for the fresh turn.
					closeTurn()
					atomic.StoreInt32(&stopPlayback, 0)
					playingSeq = item.seq
				}
				if item.final {
					// Nothing follows for this turn. Finalize only once nothing
					// newer is queued behind the marker.
					if len(ttsSentCh) == 0 && len(ttsRenderedCh) == 0 {
						closeTurn()
					}
					continue
				}
				// A barge-in halted this turn: drop the rest of it instead of
				// resuming mid-answer (stopPlayback is only cleared when a new
				// turn starts).
				if turnOpen && atomic.LoadInt32(&stopPlayback) != 0 {
					log.Printf("[TTS-PLAYER] Turn %d halted, dropping: %.60s...", item.seq, item.text)
					continue
				}
				log.Printf("[TTS-PLAYER] Playing sentence (len=%d, turn=%d): %.80s...", len(item.text), item.seq, item.text)
				// Hold the echo guard and the speaking state for the WHOLE turn,
				// including the gap where the LLM is still producing the next
				// sentence. Releasing them per sentence let Mai's own voice reach
				// the mic in those gaps and produce phantom user turns.
				atomic.StoreInt32(&isSpeaking, 1)
				atomic.StoreInt32(&ttsPlaying, 1)
				if !turnOpen {
					refBuffer.Clear()
					echoCanceller.Reset()
					ttsStartedNano.Store(time.Now().UnixNano())
					bargeStartNano.Store(0)
				}
				// Announce this sentence exactly when its audio starts, so
				// transcript, voice and the browser's viseme schedule advance
				// on the same timeline (TTS/LLM streaming sync).
				publishTranscript(bus, item.text, false)
				turnOpen = true
				mirrored = playRendered(item.samples)
				log.Printf("[TTS-PLAYER] Finished playing sentence, queue_depth=%d", len(ttsRenderedCh))
			}
		}
	}()
	// Test TTS on startup
	ttsWG.Add(1)
	go func() {
		defer ttsWG.Done()
		if voiceCloneEnabled {
			log.Printf("[TTS] Voice cloning active (model: %s)", cfg.TTS.ActiveModel)
		}
		sr := cfg.TTS.OutputSampleRate
		if sr == 0 {
			sr = 44100
		}
		synthesize("System ready.", ttsDefaultSpeed, func(samples []float32) bool {
			playAudio(ctx, ttsToBrowserResampler.resample(samples), sr, nil)
			return true
		})
	}()

	// Initialize automation system
	auto := NewAutomation(cfg.Vision.Model, cfg.Vision.URL, cfg.Vision.Enabled)
	executor := NewActionExecutor(auto)

	// 0. Initialize Agentic Architecture if enabled
	var agentBridge *perception.Bridge
	// Cancels the in-flight LLM stream when a genuine barge-in occurs.
	var interruptCurrent func()
	// The cognitive orchestrator — reached from the audio pipeline for
	// prosody ingestion, so it must live in main() scope, not the agentic block.
	var orch *agent.Orchestrator
	if cfg.Agentic.Enabled {
		log.Println("[BOOT] Initializing Agentic Architecture...")
		bus = events.NewBus()

		// LLM (create first — memory needs it for embeddings)
		llmFactory := llm.NewFactory(cfg)
		llmProvider, err := llmFactory.CreateHybridProvider()
		if err != nil {
			log.Fatalf("[BOOT] Failed to create LLM provider: %v", err)
		}

		// Memory
		workingMem := memory.NewWorkingMemory(10)
		episodicMem, _ := memory.NewEpisodicStore("data/memory/episodic.db")
		semanticMem := memory.NewSemanticStore(llmProvider, "data/vector")
		proceduralMem, _ := memory.NewProceduralStore("data/memory")
		memManager := memory.NewMemoryManager(workingMem, episodicMem, semanticMem, proceduralMem)
		memManager.SetRAGProvider(llmProvider)

		// Tools
		registry := tools.NewRegistry()
		registry.Register(&adapters.ShellTool{})
		registry.Register(&adapters.WebSearchTool{})
		registry.Register(&adapters.OpenAppTool{
			Open: func(name, browser string) error {
				return auto.OpenAppWithBrowser(name, browser)
			},
		})
		registry.Register(&adapters.YouTubeTool{})
		registry.Register(adapters.NewDeepSearchTool())
		registry.Register(adapters.NewWebResearchTool())
		registry.Register(&adapters.FileWriteTool{})
		registry.Register(&adapters.ClockTool{})
		registry.Register(&adapters.WhatsAppTool{
			Send: func(app, contact, text string) error {
				return auto.SendMessage(app, contact, text)
			},
		})
		registry.Register(&adapters.AutomationTool{})

		// MCP — auto-discover tools from configured servers
		if cfg.MCP.Enabled {
			for _, serverURL := range cfg.MCP.Servers {
				mcpClient := mcp.NewClient(serverURL)
				mcpTools, err := mcpClient.DiscoverTools(ctx)
				if err != nil {
					log.Printf("[MCP] Failed to discover tools from %s: %v", serverURL, err)
					continue
				}
				for _, toolMeta := range mcpTools {
					adapter := mcp.NewMCPToolAdapter(toolMeta, mcpClient)
					if err := registry.Register(adapter); err != nil {
						log.Printf("[MCP] Failed to register tool %s: %v", toolMeta.Name, err)
					} else {
						log.Printf("[MCP] Registered external tool: %s", toolMeta.Name)
					}
				}
			}
		}

		// Cognition
		react := cognition.NewReActLoop(llmProvider, registry, workingMem)

		// Orchestrator
		orch = agent.NewOrchestrator(bus, memManager, llmProvider, registry, react,
			interfaces.GenerationOptions{
				Temperature: cfg.LLM.Sampling.Temperature,
				TopP:        cfg.LLM.Sampling.TopP,
				MaxTokens:   cfg.LLM.Sampling.MaxTokens,
			})
		// Pocket can begin TTS as soon as the LLM finishes a safe sentence.
		// Other models keep the existing compatibility behavior.
		llmToTTSStreaming := cfg.TTS.ActiveModel != "pocket" || cfg.TTS.Pocket.Streaming
		orch.SetTTSStreaming(llmToTTSStreaming)
		if cfg.TTS.ActiveModel == "pocket" {
			mode := "buffered"
			if llmToTTSStreaming {
				mode = "streaming"
			}
			log.Printf("[TTS] LLM-to-Pocket handoff: %s", mode)
		}

		// Deliver generated sentences into the serialized TTS queue. The
		// orchestrator decides whether this happens during LLM generation or
		// after the complete response, based on pocket.streaming.
		// The playRendered function handles routing: when a browser client
		// is connected it publishes audio chunks via the event bus (bridge
		// forwards them to the browser); when no client is connected it
		// falls back to local audio playback.  The bus subscription added
		// below covers the local-only fallback path as well.
		orch.TTSFunc = func(text string, params personality.TTSParams, seq int64, final bool) {
			if final {
				// End of this reply's sentences: the player may now finalize the
				// transcript once the queued audio has actually played.
				ttsSentCh <- ttsItem{seq: seq, final: true}
				return
			}
			log.Printf("[TTS-FUNC] Enqueuing sentence (len=%d, speed=%.2f, volume=%.2f, pitch=%.2f, turn=%d): %.80s...", len(text), params.Speed, params.Volume, params.Pitch, seq, text)
			speak(text, params.Speed, params.Volume, params.Pitch, seq)
		}
		interruptCurrent = orch.InterruptCurrent
		orch.DirectAction = executor.ParseAndExecute // Wire up the legacy highly-reliable regex parser

		// Apply TTS voice style from system prompt
		ttsStyle := cfg.TTS.TTSVoiceStyle
		if ttsStyle == "" {
			ttsStyle = personality.ParseVoiceStyle(cfg.LLM.SystemPrompt)
		}
		if ttsStyle != "" && ttsStyle != "neutral" {
			orch.SetTTSVoiceStyle(ttsStyle)
		}
		// Baseline speech rate (warmth) — applied after style so it wins.
		orch.SetTTSBaseSpeed(cfg.TTS.BaseSpeed)
		// Verbatim chat history depth for long-conversation continuity
		// (0 = provider default 10 pairs).
		orch.SetChatHistoryTurns(cfg.LLM.ChatHistoryTurns)

		go orch.Start(ctx)

		// Bridge for Perception
		agentBridge = perception.NewBridge(bus)

		// WebSocket companion server
		if companionMode {
			cfg.Server.Enabled = true
			log.Println("[BOOT] Companion mode enabled via --companion flag")
		}
		if cfg.Server.Enabled {
			srv := server.New(server.ServerConfig{
				Enabled: cfg.Server.Enabled,
				Port:    cfg.Server.Port,
				Token:   cfg.Server.Token,
			}, bus, &isSpeaking, &ttsPlaying, func() string {
				return string(orch.GetStatus())
			})
			if err := srv.Start(); err != nil {
				log.Printf("[SERVER] Failed to start: %v", err)
			}
			companionServer = srv
			// When the browser disconnects, restore the local mic if a browser mic was active.
			srv.OnClientDisconnect = func() {
				if atomic.LoadInt32(&browserMicActive) == 1 && companionServer.ClientCount() == 0 {
					capture.Start()
				}
			}
			srv.SetOnClientGone(func() {
				if srv.OnClientDisconnect != nil {
					srv.OnClientDisconnect()
				}
			})
		}

		// Thinking chime — plays when LLM processing begins for reasoning tasks
		// (not for simple conversational responses).
		bus.Subscribe("perception.audio.transcription", func(event interfaces.Event) {
			// Transcription received - LLM will process, but don't chime yet.
			// The worker goroutine will chime when it actually starts thinking.
		})

		// Agent-level interruption (e.g. a "stop"/"cancel" command routed
		// through HandleInput): halt the sentence currently being spoken.
		// The player keeps the newest turn's audio and drops superseded ones,
		// so this never discards the user's actual reply.
		bus.Subscribe("agent.interrupt", func(event interfaces.Event) {
			atomic.StoreInt32(&stopPlayback, 1)
		})

		// Bridge for TTS — with emotion-adaptive parameters
		bus.Subscribe("action.tts.request", func(event interfaces.Event) {
			text, _ := event.Payload["text"].(string)
			speed, _ := event.Payload["speed"].(float32)
			volume, _ := event.Payload["volume"].(float32)
			pitch, _ := event.Payload["pitch"].(float32)
			seq, _ := event.Payload["seq"].(int64)
			log.Printf("[AGENT] Speaking (speed=%.2f, pitch=%.2f, turn=%d): %s", speed, pitch, seq, text)
			// One-shot utterance on the legacy bus path: include the end-of-turn
			// marker so the player finalizes the transcript as soon as it ends.
			speakTurn(text, speed, volume, pitch, seq)
			log.Printf("[FOLLOW-UP] Listening for follow-up (15s window)...")
		})

		// Browser mic audio frames → same VAD/ASR pipeline as local mic.
		bus.Subscribe("perception.audio.frame", func(event interfaces.Event) {
			samples, _ := event.Payload["samples"].([]float32)
			if len(samples) > 0 {
				handleAudioFrame(samples, true)
			}
		})

		// Pause/resume local system mic when browser mic is active (avoid double input).
		bus.Subscribe("companion.audio.start", func(event interfaces.Event) {
			log.Println("[MIC] Browser mic active — pausing local capture")
			atomic.StoreInt32(&browserMicActive, 1)
			capture.Stop()
		})
		bus.Subscribe("companion.audio.stop", func(event interfaces.Event) {
			atomic.StoreInt32(&browserMicActive, 0)
			if !companionHasClient() {
				log.Println("[MIC] Browser mic stopped — restoring local capture")
				capture.Start()
			}
		})
	}

	log.Println("[AUTO] Automation system ready")

	// 5. Initialize audio capture
	capture = newAudioCapture(16000, 1)
	defer capture.Close()

	log.Println("[AUDIO] Capture initialized")

	// 6. Pipeline Worker (LLM + TTS + Actions)
	type Task struct {
		Text        string
		IsReasoning bool // true if task requires actual reasoning (not just chat)
	}
	workerChan := make(chan Task, 10)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for task := range workerChan {
			log.Printf("[LLM] Thinking about: %s", task.Text)

			// Play thinking chime only for reasoning tasks (not simple chat)
			// and only if enabled in config.
			if task.IsReasoning && cfg.Audio.ThinkingChime {
				playThinkingChime()
			}

			// Try to parse and execute automation action
			executed, feedback, actionErr := executor.ParseAndExecute(task.Text)
			if actionErr != nil {
				log.Printf("[ACTION] Error executing action: %v", actionErr)
			}

			var response string
			var err error

			if executed {
				// Action was executed - ask LLM for natural response with context
				log.Printf("[ACTION] Executed: %s", feedback)
				prompt := fmt.Sprintf("User said: %q. I just did this: %s. Respond very briefly and naturally (1 sentence).", task.Text, feedback)
				response, err = generateOllamaResponse(ctx, cfg, prompt)
				if err != nil {
					log.Printf("[LLM] Error generating contextual response: %v", err)
					response = feedback // Fallback to simple feedback
				}
			} else {
				// No action detected - normal LLM flow
				response, err = generateOllamaResponse(ctx, cfg, task.Text)
				if err != nil {
					log.Printf("[LLM] Error: %v", err)
					atomic.StoreInt32(&isSpeaking, 0)
					continue
				}
			}

			log.Printf("[LLM] Response received. Starting TTS...")

			// Send transcript to browser companion (legacy path).
			if bus != nil && cfg.Server.Enabled && companionHasClient() {
				bus.Publish(interfaces.Event{
					Type:   "chat.response",
					Source: "main",
					Payload: map[string]interface{}{
						"text": response,
						"done": true,
					},
				})
			}

			// Streaming TTS: generate chunks and play them as they arrive.
			atomic.StoreInt32(&isSpeaking, 1) // Block ASR during TTS playback
			atomic.StoreInt32(&ttsPlaying, 1)
			refBuffer.Clear()
			echoCanceller.Reset()
			ttsStartedNano.Store(time.Now().UnixNano())
			bargeStartNano.Store(0)
			atomic.StoreInt32(&stopPlayback, 0)
			playErr := playAudioStreaming(ctx, 44100, &stopPlayback, func(ch chan<- []float32) {
				synthesize(response, ttsDefaultSpeed, func(samples []float32) bool {
					ch <- ttsToBrowserResampler.resample(samples)
					return true
				})
			})
			if playErr != nil {
				log.Printf("[TTS] Play error: %v", playErr)
			}
			atomic.StoreInt32(&ttsPlaying, 0)

			// If barge-in was triggered, skip silence wait
			if atomic.LoadInt32(&stopPlayback) == 0 {
				waitForMicSilence()
			}

			atomic.StoreInt32(&isSpeaking, 0) // Resume ASR
			lastResponseMu.Lock()
			lastResponseTime = time.Now()
			lastResponseMu.Unlock()
			log.Println("[FOLLOW-UP] Listening for follow-up (15s window)...")
		}
	}()

	// 7. State machine
	type State int
	const (
		StateWakeWord State = iota
		StateListening
	)

	state := StateWakeWord
	var lastText string
	var sessionText string

	// Prosody sample buffer: the most recent user utterance (16kHz), capped at
	// ~3s to bound analysis cost. Fed to the orchestrator's prosody→emotion
	// path at segment end so Mai hears *how* the user said something.
	var prosodySamples []float32
	const prosodyMaxSamples = 16000 * 3 // 3 seconds

	// finalizeTurn routes the accumulated utterance to the agent/legacy
	// pipeline and resets all per-turn state. Called by both VAD segment-end
	// and the streaming-ASR endpoint path.
	finalizeTurn := func() {
		// Display-only transcript of what the user said. Never fed back into
		// ASR/turn input anywhere else — routing below is the single consumer.
		if trimmed := strings.TrimSpace(sessionText); trimmed != "" {
			log.Printf("[USER] %s", trimmed)
		}
		if agentBridge != nil {
			log.Println("[AGENT] Routing to cognitive orchestrator...")
			// Feed the voice-derived emotion to Mai *before* the transcription
			// event so HandleInput can merge it with the text keywords
			// ("I'm fine" said flatly vs. stressed).
			if orch != nil && len(prosodySamples) > 0 {
				orch.IngestProsody(orch.AnalyzeProsody(prosodySamples, 16000))
			}
			go agentBridge.PublishTranscription(sessionText)
		} else {
			log.Println("[PIPELINE] Routing to legacy pipeline...")
			// Heuristic: longer inputs (>15 words) are more likely to be
			// reasoning tasks vs. simple conversational turns.
			isReasoning := len(strings.Fields(sessionText)) > 15
			workerChan <- Task{Text: sessionText, IsReasoning: isReasoning}
		}
		state = StateWakeWord
		sessionText = ""
		sessionSamples = nil
		prosodySamples = nil
		if recognizer != nil {
			recognizer.Reset(asrStream)
		}
		lastText = ""
	}

	// updateLiveASR decodes one frame already pushed to asrStream, returns the
	// partial transcript, and logs it. Shared by the listening and wake-word
	// states so the user sees live captions in real time in either state.
	// Uses log (stderr) rather than fmt.Printf("\r") so the line isn't clobbered
	// by the timestamped stderr logs.
	updateLiveASR := func() string {
		if asrStream == nil {
			return ""
		}
		for recognizer.IsReady(asrStream) {
			recognizer.Decode(asrStream)
		}
		text := recognizer.GetResult(asrStream).Text
		if text != "" && text != lastText {
			lastText = text
			log.Printf("[ASR] Live: %s%s", sessionText, text)
		}
		return text
	}

	// Audio callback
	// runListening: the VAD + ASR + segment-end detection pipeline.
	// Shared by local mic (in listening state) and browser mic.
	runListening := func(samples []float32) {
		if atomic.LoadInt32(&ttsPlaying) != 0 {
			return
		}
		// Accumulate the live utterance for prosody analysis (both ASR paths).
		prosodySamples = append(prosodySamples, samples...)
		if len(prosodySamples) > prosodyMaxSamples {
			prosodySamples = prosodySamples[len(prosodySamples)-prosodyMaxSamples:]
		}
		vadBuffer.Push(samples)
		for vadBuffer.Size() >= cfg.VAD.WindowSize {
			head := vadBuffer.Head()
			chunk := vadBuffer.Get(head, cfg.VAD.WindowSize)
			vadBuffer.Pop(cfg.VAD.WindowSize)
			vadDetector.AcceptWaveform(chunk)
		}

		if asrStream != nil && atomic.LoadInt32(&ttsPlaying) == 0 {
			asrStream.AcceptWaveform(16000, samples)
			text := updateLiveASR()
			// Finalize when the recognizer's own endpoint rules fire (e.g.
			// rule1/rule2 trailing silence). Silero VAD alone is unreliable as
			// the sole end-of-turn signal — with streaming ASR live text keeps
			// flowing with no VAD involvement, so a missed VAD segment would
			// leave the turn stuck at "[ASR] Live:" forever.
			if text != "" && recognizer.IsEndpoint(asrStream) {
				log.Printf("\n[ASR] Endpoint detected, finalizing (text=%q)", text)
				sessionText += text + " "
				finalizeTurn()
				for !vadDetector.IsEmpty() {
					vadDetector.Pop()
				}
				return
			}
		} else {
			// Safety cap: force finalization if buffer grows too large
			if len(sessionSamples) >= offlineASRMaxSamples {
				log.Printf("[ASR] WARNING: Offline buffer exceeded %d samples (%.0fs). Forcing finalization.",
					offlineASRMaxSamples, offlineASRMaxDuration.Seconds())
				// Force a VAD end-of-segment to trigger processing
				// by adding a synthetic silence chunk won't help — instead
				// just process what we have
				if offlineRecognizer != nil && len(sessionSamples) > 0 {
					log.Printf("[ASR] Processing oversized segment with %s...\n", cfg.ASR.ActiveModel)
					offlineStream := sherpa.NewOfflineStream(offlineRecognizer)
					// Set language for models that support it (qwen3, omnilingual)
					var lang string
					switch cfg.ASR.ActiveModel {
					case "qwen3":
						lang = cfg.ASR.Qwen3.Language
					case "omnilingual":
						lang = cfg.ASR.Omnilingual.Language
					}
					if lang != "" && lang != "auto" {
						offlineStream.SetOption("language", lang)
					}
					offlineStream.AcceptWaveform(16000, sessionSamples)
					offlineRecognizer.Decode(offlineStream)
					result := offlineStream.GetResult()
					if result != nil && result.Text != "" {
						sessionText = result.Text
						log.Printf("[USER] %s", strings.TrimSpace(sessionText))
						if agentBridge != nil {
							go agentBridge.PublishTranscription(sessionText)
						} else {
							workerChan <- Task{Text: sessionText, IsReasoning: false}
						}
					}
					sherpa.DeleteOfflineStream(offlineStream)
				}
				sessionSamples = nil
				sessionText = ""
				state = StateWakeWord
			} else {
				sessionSamples = append(sessionSamples, samples...)
				fmt.Printf("\r[ASR] Listening... (buffered %d samples)", len(sessionSamples))
			}
		}

		for !vadDetector.IsEmpty() {
			vadDetector.Pop()

			if asrStream != nil {
				// DRAIN: Run any remaining decode cycles before GetResult
				// so trailing words aren't lost when VAD ends slightly early.
				for recognizer.IsReady(asrStream) {
					recognizer.Decode(asrStream)
				}
				text := recognizer.GetResult(asrStream).Text
				if text != "" {
					sessionText += text + " "
				}
				sessionSamples = nil
			} else if offlineRecognizer != nil {
				log.Printf("\n[ASR] Processing segment with %s...\n", cfg.ASR.ActiveModel)
				offlineStream := sherpa.NewOfflineStream(offlineRecognizer)
				// Set language for models that support it (qwen3, omnilingual)
				var lang string
				switch cfg.ASR.ActiveModel {
				case "qwen3":
					lang = cfg.ASR.Qwen3.Language
				case "omnilingual":
					lang = cfg.ASR.Omnilingual.Language
				}
				if lang != "" && lang != "auto" {
					offlineStream.SetOption("language", lang)
				}
				offlineStream.AcceptWaveform(16000, sessionSamples)
				offlineRecognizer.Decode(offlineStream)
				result := offlineStream.GetResult()
				if result != nil {
					sessionText = result.Text
				}
				sherpa.DeleteOfflineStream(offlineStream)
				sessionSamples = nil
			}

			log.Println("\n[VAD] End of segment detected.")
			if sessionText != "" {
				finalizeTurn()
				return
			}
		}
	}

	// handleAudioFrame: entry point for audio frames from any source.
	// fromBrowser=true  → browser mic (skips wake word, goes straight to listening)
	// fromBrowser=false → local system mic (full state machine: wake word + follow-up + listening)
	handleAudioFrame = func(samples []float32, fromBrowser bool) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[AUDIO] Panic recovered in audio callback: %v", r)
			}
		}()
		sherpaMu.Lock()
		defer sherpaMu.Unlock()

		var sum float32
		for _, s := range samples {
			sum += s * s
		}
		rms := math.Sqrt(float64(sum / float32(len(samples))))
		lastMicMu.Lock()
		lastMicRMS = rms
		lastMicMu.Unlock()

		playing := atomic.LoadInt32(&ttsPlaying) != 0
		speaking := atomic.LoadInt32(&isSpeaking) != 0
		postTTS := false
		if nano := lastTTSEndNano.Load(); nano > 0 {
			if time.Since(time.Unix(0, nano)) < postTTSAECWindow {
				postTTS = true
			} else {
				lastTTSEndNano.Store(0)
			}
		}

		// Ask the noise suppressor about this frame. It analyses on its own
		// goroutine (the model costs ~0.6 of a core), so Feed only posts a copy
		// and Speech reads back the latest verdict — the capture callback never
		// waits for it. It is only fed while the frame is actually heading for
		// the spotter/VAD/ASR: during playback and the AEC window that follows
		// it those consumers are gated off anyway, which parks the analysis.
		noiseFrame := false
		if denoiser != nil && !playing && !speaking && !postTTS {
			denoiser.Feed(samples, false)
			noiseFrame = !denoiser.Speech()
		}

		// While Mai is speaking, echo-cancel her own voice against the speaker
		// reference so it does not reach ASR. If a real (different) speaker shows
		// up in the residual, that's a genuine barge-in — stop playback and hand
		// the user's echo-free words to ASR.
		if atomic.LoadInt32(&ttsPlaying) != 0 {
			if cfg.Audio.BargeInEnabled && echoCanceller != nil {
				// Adapt from the very first playback frame: during the warmup
				// window the gate below stays closed, so feeding the filter
				// early is pure training (echo only) and it converges before
				// detection arms.
				clean := echoCanceller.Process(samples)
				crms := rmsOf(clean)
				lastMicMu.Lock()
				lastMicRMS = crms
				lastMicMu.Unlock()

				// Let the enhancer judge the residual: a fan or HVAC drone that
				// survives the echo canceller is noise, not an interruption.
				if denoiser != nil {
					denoiser.Feed(clean, true)
				}

				warm := time.Since(time.Unix(0, ttsStartedNano.Load())) > bargeInWarmup
				loud := crms > cfg.Audio.BargeInThreshold*bargeInMargin
				// The coherence scan costs ~1.6M multiply-adds, so it only runs
				// once the cheap energy gate has already fired.
				var coherence float64
				if warm && loud {
					coherence = echoCanceller.EchoCoherence(clean)
				}
				// Energy alone must not cut Mai off: her own voice is loud in the
				// residual too. Two independent checks have to agree before this
				// is treated as the user speaking.
				//   * notEcho - the residual must not be a delayed copy of what
				//     Mai just played. A 4096-tap canceller needs a couple of
				//     seconds of echo to converge, so at the 400ms warmup her
				//     leaked voice used to trip this gate at warmup+sustain,
				//     every single time.
				//   * voiced - the enhancer must have analysed the residual and
				//     found speech in it, which rejects a stationary drone that
				//     clears the energy gate and carries no words.
				notEcho := coherence < bargeInEchoMax
				voiced := denoiser.HaveVerdict() && denoiser.Speech()
				switch {
				case !warm || !loud:
					bargeStartNano.Store(0)
				case !notEcho:
					bargeStartNano.Store(0)
					if rateLimit(&bargeLogNano, 4*time.Second) {
						log.Printf("[BARGE-IN] Echo suppressed (residual RMS=%.4f, %.0f%% reference correlation).", crms, coherence*100)
					}
				case !voiced:
					bargeStartNano.Store(0)
					if rateLimit(&bargeLogNano, 4*time.Second) {
						log.Printf("[BARGE-IN] Stationary noise ignored in residual (RMS=%.4f).", crms)
					}
				default:
					start := bargeStartNano.Load()
					if start == 0 {
						bargeStartNano.Store(time.Now().UnixNano())
					} else if held := time.Since(time.Unix(0, start)); held >= bargeInSustain {
						log.Printf("[BARGE-IN] Real speech over TTS detected (residual RMS=%.4f, mic RMS=%.4f, echo corr=%.2f, held %v). Stopping playback.", crms, rms, coherence, held)
						bargeStartNano.Store(0)
						atomic.StoreInt32(&stopPlayback, 1)
						if interruptCurrent != nil {
							interruptCurrent()
						}
						echoCanceller.Reset()
						state = StateListening
						sessionText = ""
						lastText = ""
						sessionSamples = nil
						if recognizer != nil {
							recognizer.Reset(asrStream)
						}
						// Feed the user's (echo-free) words to VAD + ASR and keep listening.
						vadBuffer.Push(clean)
						for vadBuffer.Size() >= cfg.VAD.WindowSize {
							head := vadBuffer.Head()
							c := vadBuffer.Get(head, cfg.VAD.WindowSize)
							vadBuffer.Pop(cfg.VAD.WindowSize)
							vadDetector.AcceptWaveform(c)
						}
						if asrStream != nil {
							asrStream.AcceptWaveform(16000, clean)
							for recognizer.IsReady(asrStream) {
								recognizer.Decode(asrStream)
							}
						}
						return
					}
				}
			}
			// No genuine interruption: her echo is not user input, so drop the frame.
			return
		}

		if atomic.LoadInt32(&isSpeaking) != 0 {
			return
		}

		// Post-TTS AEC: after TTS finishes, continue echo cancellation for
		// postTTSAECWindow so room reverberation doesn't reach ASR. During the
		// first ttsCooldown the echo canceller settles; after that, only
		// genuine speech (residual above threshold) passes through.
		if postTTS {
			if echoCanceller != nil {
				clean := echoCanceller.Process(samples)
				crms := rmsOf(clean)
				if denoiser != nil {
					denoiser.Feed(clean, true)
				}
				if crms < cfg.Audio.BargeInThreshold {
					return // just echo, drop it
				}
				// Reverb check: verify residual is not lingering reverberation from Mai's playback.
				if echoCanceller.EchoCoherence(clean) >= bargeInEchoMax {
					return // reverb tail of Mai's playback, drop it
				}
				// Above the threshold, but a stationary drone clears the
				// threshold while carrying no words — so the enhancer must also
				// agree that this is speech before it reaches ASR.
				if denoiser.HaveVerdict() && !denoiser.Speech() {
					return
				}
				// Residual above threshold — genuine speech, fall through
			} else {
				return
			}
		}

		// A frame the enhancer positively identified as stationary noise is not
		// user input. Dropping it here is what stops a laptop fan or HVAC drone
		// from reaching the wake-word spotter, the VAD or ASR — and from
		// opening a turn of its own.
		if noiseFrame {
			return
		}

		if fromBrowser {
			runListening(samples)
			return
		}

		// ── Local mic: existing state machine ──
		switch state {
		case StateWakeWord:
			// Follow-up: if the user speaks within the 15s window, skip wake word.
			if time.Since(lastResponseTime) < 15*time.Second {
				if asrStream != nil && atomic.LoadInt32(&ttsPlaying) == 0 {
					asrStream.AcceptWaveform(16000, samples)
					updateLiveASR()
				}

				vadBuffer.Push(samples)
				var lastChunk []float32
				for vadBuffer.Size() >= cfg.VAD.WindowSize {
					head := vadBuffer.Head()
					lastChunk = vadBuffer.Get(head, cfg.VAD.WindowSize)
					vadBuffer.Pop(cfg.VAD.WindowSize)
					vadDetector.AcceptWaveform(lastChunk)
				}

				if !vadDetector.IsEmpty() {
					var sum float32
					for _, s := range samples {
						sum += s * s
					}
					rms := math.Sqrt(float64(sum / float32(len(samples))))

					if rms > 0.001 {
						log.Printf("[FOLLOW-UP] Speech detected (Level %.4f)! Skipping wake word.", rms)
						state = StateListening

						preBuffer := make([]float32, lookbackSize)
						for i := 0; i < lookbackSize; i++ {
							preBuffer[i] = lookbackBuffer[(lookbackIdx+i)%lookbackSize]
						}
						sessionSamples = append(preBuffer, lastChunk...)

						sessionText = ""
						lastText = ""
						if recognizer != nil {
							recognizer.Reset(asrStream)
						}
						for !vadDetector.IsEmpty() {
							vadDetector.Pop()
						}
						return
					}
				}
			}

			for _, s := range samples {
				lookbackBuffer[lookbackIdx] = s
				lookbackIdx = (lookbackIdx + 1) % lookbackSize
			}

			kwsStream.AcceptWaveform(16000, samples)
			if asrStream != nil && atomic.LoadInt32(&ttsPlaying) == 0 {
				asrStream.AcceptWaveform(16000, samples)
				updateLiveASR()
			}

			fmt.Printf("\r[AUDIO] Level: %.4f ", rms)

			if time.Since(lastDetected) < time.Duration(cfg.KWS.CooldownMs)*time.Millisecond {
				return
			}
			for spotter.IsReady(kwsStream) {
				spotter.Decode(kwsStream)
				fmt.Print("*")
				result := spotter.GetResult(kwsStream)
				if result.Keyword != "" {
					spotter.Reset(kwsStream)
					lastDetected = time.Now()
					log.Println("\n[WAKE] Detected! Listening...")

					// Route greeting through the orchestrator so it shares the
					// turn-sequence space and can be truncated by an interruption.
					go orch.Speak(pickGreeting())

					state = StateListening
					sessionText = ""
					sessionSamples = nil
					sherpa.DeleteCircularBuffer(vadBuffer)
					vadBuffer = sherpa.NewCircularBuffer(10 * 16000)
					if recognizer != nil {
						recognizer.Reset(asrStream)
					}
					lastText = ""
					return
				}
			}

		case StateListening:
			runListening(samples)
		}
	}

	capture.onSamples = func(samples []float32) {
		handleAudioFrame(samples, false)
	}

	// Start capture
	if err := capture.Start(); err != nil {
		log.Fatalf("Failed to start capture: %v", err)
	}

	log.Println("Running. Say wake word to begin. Press Ctrl+C to exit.")

	// Wait for interrupt
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("\n[SYSTEM] Shutting down immediately...")

	cancel() // Cancel the background context (stops Ollama requests, etc.)
	capture.Stop()
	close(workerChan)

	// Stop speech and join every goroutine that may be inside sherpa's
	// GenerateWithConfig BEFORE main returns — the deferred DeleteOfflineTts
	// would otherwise free the engine mid-generation (0xc0000005 crash).
	close(ttsShutdown)
	go func() {
		for range ttsSentCh {
		}
	}() // unblock any blocked senders
	atomic.StoreInt32(&stopPlayback, 1)
	ttsWG.Wait()

	// Wait briefly for cleanup, then force exit
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		log.Println("[SYSTEM] Graceful shutdown complete.")
	case <-time.After(2 * time.Second):
		log.Println("[SYSTEM] Shutdown timeout - forcing exit.")
	}
}

// pickGreeting returns a persona-consistent, time-of-day aware opener instead
// of the old JARVIS-style "Yes Sir / At your service" lines. Rotated per
// hour so repeated wake-ups don't sound scripted ("greet by context" — the
// single biggest companionship cue per companion-AI research).
func pickGreeting() string {
	hour := time.Now().Hour()
	var pool []string
	switch {
	case hour >= 5 && hour < 12:
		pool = []string{
			"Good morning. What are we getting into today?",
			"Morning. I'm awake. What do you need?",
			"Good morning, Aswani-kun.",
		}
	case hour >= 12 && hour < 17:
		pool = []string{
			"Hey. What's on your mind?",
			"I'm here. What do we have today?",
			"Good afternoon. What do you need?",
		}
	case hour >= 17 && hour < 22:
		pool = []string{
			"Evening. I'm with you.",
			"Hey, good evening. What are we doing?",
			"Evening, Aswani-kun. I'm here.",
		}
	default:
		pool = []string{
			"Still up? I'm here. What do you need?",
			"Night owl hours. What's on your mind?",
			"Late one, hm? Say the word.",
		}
	}
	return pool[time.Now().UnixNano()%int64(len(pool))]
}

// startOllama starts the ollama serve process and returns a function to kill it.
func startOllama() func() {
	cmd := exec.Command("ollama", "serve")
	if err := cmd.Start(); err != nil {
		log.Printf("[OLLAMA] Warning: Failed to start ollama serve: %v. Assuming it is already running.", err)
		return func() {}
	}
	log.Println("[OLLAMA] Started background server")
	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			log.Println("[OLLAMA] Stopped background server")
		}
	}
}

// generateOllamaResponse sends text to Ollama and returns the generated text.
// rateLimit reports whether a diagnostic may be logged now, allowing at most one
// message per interval per stamp. Used for the barge-in "why didn't it fire"
// messages, which would otherwise repeat on every captured frame.
func rateLimit(stamp *atomic.Int64, every time.Duration) bool {
	now := time.Now().UnixNano()
	prev := stamp.Load()
	if now-prev < int64(every) {
		return false
	}
	return stamp.CompareAndSwap(prev, now)
}

func generateOllamaResponse(ctx context.Context, cfg models.Config, prompt string) (string, error) {
	client := &http.Client{}

	body := map[string]interface{}{
		"model":      cfg.LLM.Model,
		"prompt":     prompt,
		"system":     cfg.LLM.SystemPrompt,
		"stream":     false,
		"keep_alive": "30m",
		"options": map[string]interface{}{
			"temperature": cfg.LLM.Sampling.Temperature,
			"top_p":       cfg.LLM.Sampling.TopP,
			"num_predict": cfg.LLM.Sampling.MaxTokens,
		},
	}
	if cfg.LLM.Sampling.MinP > 0 {
		body["options"].(map[string]interface{})["min_p"] = cfg.LLM.Sampling.MinP
	}
	if cfg.LLM.NumCtx > 0 {
		body["options"].(map[string]interface{})["num_ctx"] = cfg.LLM.NumCtx
	}
	if cfg.LLM.Think != nil {
		body["think"] = *cfg.LLM.Think
	}
	requestBody, _ := json.Marshal(body)

	log.Printf("[OLLAMA] Requesting response from %s...", cfg.LLM.Model)
	req, _ := http.NewRequestWithContext(ctx, "POST", cfg.LLM.URL, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama post: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama error status: %d", resp.StatusCode)
	}

	respBody, _ := io.ReadAll(resp.Body)
	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}

	return result.Response, nil
}

// join concatenates directory and filename with forward slash.
func join(dir, file string) string {
	if dir == "" {
		return file
	}
	if file == "" {
		return dir
	}
	last := dir[len(dir)-1]
	if last == '/' || last == '\\' {
		return dir + file
	}
	return dir + "/" + file
}

// loadEnvFile loads KEY=VALUE pairs from a .env file into environment variables.
func loadEnvFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return // .env file is optional
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove surrounding quotes
		if len(value) >= 2 && (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}

		os.Setenv(key, value)
		log.Printf("[ENV] Loaded: %s", key)
	}
}

// resolveConfigEnv replaces config values that match environment variable names
// with the actual env var values. This lets users put "OPENROUTER_API" in config.yaml
// and have it resolved from the .env file or system environment.
func resolveConfigEnv(cfg *models.Config) {
	cfg.LLM.APIKey = resolveEnv(cfg.LLM.APIKey)
	cfg.LLM.Cloud.APIKey = resolveEnv(cfg.LLM.Cloud.APIKey)
	cfg.LLM.URL = resolveEnv(cfg.LLM.URL)
	cfg.LLM.Cloud.URL = resolveEnv(cfg.LLM.Cloud.URL)
	cfg.Vision.APIKey = resolveEnv(cfg.Vision.APIKey)
}

func resolveEnv(val string) string {
	if val == "" {
		return val
	}
	// If the value looks like an env var name (uppercase, underscores, no spaces)
	// and the env var exists, use the env var value
	if envVal := os.Getenv(val); envVal != "" {
		return envVal
	}
	return val
}

// publishTranscript pushes one streamed transcript segment to the companion
// UI. The TTS player (not the orchestrator) owns this so the text advances in
// lock-step with the voice: a sentence is announced when its playback starts,
// and the turn is finalized once the queue drains.
func publishTranscript(bus interfaces.EventBus, text string, done bool) {
	if bus == nil {
		return
	}
	bus.Publish(interfaces.Event{
		Type:   "chat.response",
		Source: "main.tts",
		Payload: map[string]interface{}{
			"text": text,
			"done": done,
		},
	})
}

// publishTTSAudioChunk encodes a single callback chunk and pushes it onto the
// event bus so the bridge can forward it to the browser in real time. Muted
// marks audio the browser plays at zero gain: the sound comes from the local
// speakers and the tab only needs the same timeline for lip sync and the
// speaking state.
func publishTTSAudioChunk(bus interfaces.EventBus, samples []float32, sampleRate int, done bool, muted bool) {
	if bus == nil {
		return
	}
	var encoded string
	if len(samples) > 0 {
		buf := make([]byte, len(samples)*2)
		for j, s := range samples {
			if s > 1.0 {
				s = 1.0
			} else if s < -1.0 {
				s = -1.0
			}
			s16 := int16(s * 32767.0)
			buf[j*2] = byte(s16 & 0xFF)
			buf[j*2+1] = byte(s16 >> 8)
		}
		encoded = base64.StdEncoding.EncodeToString(buf)
	}
	bus.Publish(interfaces.Event{
		Type:   "tts.audio.chunk",
		Source: "main",
		Payload: map[string]interface{}{
			"audio":       encoded,
			"sample_rate": sampleRate,
			"done":        done,
			"muted":       muted,
		},
	})
}

func publishTTSAudio(bus interfaces.EventBus, samples []float32, sampleRate int, done bool) {
	if bus == nil || len(samples) == 0 {
		return
	}

	const chunkSize = 8192
	totalSamples := len(samples)

	for i := 0; i < totalSamples; i += chunkSize {
		end := i + chunkSize
		isLastChunk := false
		if end >= totalSamples {
			end = totalSamples
			isLastChunk = true
		}

		chunk := samples[i:end]
		buf := make([]byte, len(chunk)*2)
		for j, s := range chunk {
			if s > 1.0 {
				s = 1.0
			} else if s < -1.0 {
				s = -1.0
			}
			s16 := int16(s * 32767.0)
			buf[j*2] = byte(s16 & 0xFF)
			buf[j*2+1] = byte(s16 >> 8)
		}

		encoded := base64.StdEncoding.EncodeToString(buf)
		bus.Publish(interfaces.Event{
			Type:   "tts.audio.chunk",
			Source: "main",
			Payload: map[string]interface{}{
				"audio":       encoded,
				"sample_rate": sampleRate,
				"done":        done && isLastChunk,
			},
		})
	}
}
