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
	"github.com/user/mai/pkg/interfaces"
	"github.com/user/mai/pkg/models"
	"gopkg.in/yaml.v3"
)

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
	ttsConfig := sherpa.OfflineTtsConfig{}
	ttsConfig.Model.NumThreads = cfg.TTS.NumThreads
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
	case "zipvoice":
		ttsConfig.Model.Zipvoice.Encoder = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Encoder)
		ttsConfig.Model.Zipvoice.Decoder = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Decoder)
		ttsConfig.Model.Zipvoice.DataDir = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.DataDir)
		ttsConfig.Model.Zipvoice.Lexicon = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Lexicon)
		ttsConfig.Model.Zipvoice.Tokens = join(cfg.TTS.ZipVoice.ModelDir, cfg.TTS.ZipVoice.Tokens)
		ttsConfig.Model.Zipvoice.Vocoder = cfg.TTS.ZipVoice.Vocoder
	}

	tts := sherpa.NewOfflineTts(&ttsConfig)
	if tts == nil {
		log.Fatal("Failed to create TTS")
	}
	defer sherpa.DeleteOfflineTts(tts)

	// Get TTS output sample rate and create resamplers for browser (44.1k)
	// and echo reference (16k).
	ttsSampleRate := tts.SampleRate()
	log.Printf("[TTS] Engine sample rate: %d Hz", ttsSampleRate)
	ttsToBrowserResampler := newResampler(ttsSampleRate, 44100)
	ttsToEchoResampler := newResampler(ttsSampleRate, 16000)

	// Voice cloning: load reference audio if enabled and active model supports it.
	var refAudio []float32
	var refSampleRate int
	voiceCloneEnabled := cfg.TTS.VoiceCloning.Enabled &&
		(cfg.TTS.ActiveModel == "pocket" || cfg.TTS.ActiveModel == "zipvoice")
	if voiceCloneEnabled && cfg.TTS.VoiceCloning.ReferenceAudio != "" {
		wav := sherpa.ReadWaveMultiChannel(cfg.TTS.VoiceCloning.ReferenceAudio)
		if wav != nil && wav.SamplesPerChannel > 0 && wav.SampleRate > 0 {
			// Mix down to mono if stereo
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
			wav.Release()
			log.Printf("[TTS] Voice cloning loaded: %s (%d samples, %d Hz, %d ch)",
				cfg.TTS.VoiceCloning.ReferenceAudio, len(refAudio), refSampleRate, wav.ChannelCount)
		} else {
			sr := 0
			ns := 0
			if wav != nil {
				sr = wav.SampleRate
				ns = wav.SamplesPerChannel
			}
			log.Printf("[TTS] WARNING: Failed to load reference audio %s (sample_rate=%d, samples=%d), falling back to default voice",
				cfg.TTS.VoiceCloning.ReferenceAudio, sr, ns)
			if wav != nil {
				wav.Release()
			}
			voiceCloneEnabled = false
		}
	}

	// synthesize picks the right TTS method based on active model and voice cloning config.
	// For supertonic or when cloning is off: uses GenerateWithCallback (fast, default voice).
	// For pocket/zipvoice with cloning on: uses GenerateWithConfig with reference audio.
	// For kokoro: uses GenerateWithConfig with speaker ID and language.
	synthesize := func(text string, speed float32, cb func([]float32) bool) {
		if voiceCloneEnabled && refAudio != nil {
			ttsMu.Lock()
			tts.GenerateWithConfig(text, &sherpa.GenerationConfig{
				Speed:               speed,
				ReferenceAudio:      refAudio,
				ReferenceSampleRate: refSampleRate,
				ReferenceText:       cfg.TTS.VoiceCloning.ReferenceText,
			}, func(samples []float32, _ float32) bool {
				return cb(samples)
			})
			ttsMu.Unlock()
		} else {
			// Build the per-request config.
			// Supertonic needs NumSteps + Extra (lang). Kokoro needs Sid + Extra (lang).
			// Pocket/ZipVoice need Speed + Sid.
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
			ttsMu.Lock()
			tts.GenerateWithConfig(text, genCfg, func(samples []float32, _ float32) bool {
				return cb(samples)
			})
			ttsMu.Unlock()
		}
	}

	// Realtime TTS: streamed sentences are enqueued here and played one at a
	// time by a single player goroutine, so the first audio starts as soon as
	// the first sentence is ready and playback never overlaps.
	type ttsItem struct {
		text  string
		speed float32
		seq   int64 // turn sequence from the orchestrator; used to drop stale audio
	}
	ttsSentCh := make(chan ttsItem, 64)

	log.Printf("[TTS] Synthesizer ready (%s)", cfg.TTS.ActiveModel)

	// speak enqueues a sentence for the streaming player (see playSentence +
	// the player goroutine below). Keeping a single consumer serializes all
	// TTS so sentences never overlap, and lets barge-in drain the queue.
	speak := func(text string, speed float32, seq int64) {
		if speed == 0 {
			speed = cfg.TTS.Supertonic.Speed
		}
		ttsSentCh <- ttsItem{text: text, speed: speed, seq: seq}
	}

	// playSentence synthesizes and plays ONE sentence, halting immediately if
	// a barge-in sets stopPlayback mid-utterance.
	playSentence := func(text string, speed float32) {
		if speed == 0 {
			speed = cfg.TTS.Supertonic.Speed
		}
		atomic.StoreInt32(&isSpeaking, 1)
		// Mark TTS as playing so handleAudioFrame's echo-cancel/drop guard runs
		// for the browser mic too. Without this, the companion path never sets
		// ttsPlaying and Mai's own voice (echoed through the browser mic) reaches
		// ASR and gets transcribed as a user turn — the classic feedback loop.
		atomic.StoreInt32(&ttsPlaying, 1)
		ttsStartedNano.Store(time.Now().UnixNano())
		bargeStartNano.Store(0)
		atomic.StoreInt32(&stopPlayback, 0)
		defer atomic.StoreInt32(&ttsPlaying, 0)
		log.Printf("[TTS-PLAY] Starting synthesis: speed=%.2f, text=%.80s...", speed, text)

		if cfg.Audio.TTSPlayLocalAlways || !companionHasClient() {
			// Always render to the local speaker so Mai is audible regardless
			// of the companion tab's autoplay state. The playback callback
			// feeds the echo reference (audio.go), so barge-in AEC still
			// cancels Mai's own voice. Browser audio publishing is skipped to
			// avoid doubled output; the tab keeps getting text/visual events.
			_ = playAudioStreaming(ctx, 44100, &stopPlayback, func(ch chan<- []float32) {
				synthesize(text, speed, func(samples []float32) bool {
					if atomic.LoadInt32(&stopPlayback) != 0 {
						return false
					}
					ch <- ttsToBrowserResampler.resample(samples)
					return true
				})
			})
		} else {
			// Opt-in browser-only path (tts_play_local_always: false): stream
			// chunks to the companion tab. Echo reference is fed manually
			// since the local speaker isn't used here.
			synthStart := time.Now()
			var publishedSamples int64
			synthesize(text, speed, func(samples []float32) bool {
				if atomic.LoadInt32(&stopPlayback) != 0 {
					return false
				}
				if len(samples) > 0 {
					publishedSamples += int64(len(samples))
					refBuffer.Push(ttsToEchoResampler.resample(samples))
					publishTTSAudioChunk(bus, ttsToBrowserResampler.resample(samples), 44100, false)
				}
				return true
			})
			publishTTSAudioChunk(bus, nil, 44100, true)

			if ttsSampleRate > 0 {
				estEnd := synthStart.Add(time.Duration(float64(publishedSamples)/float64(ttsSampleRate)*float64(time.Second)) + 400*time.Millisecond)
				for time.Now().Before(estEnd) {
					if atomic.LoadInt32(&stopPlayback) != 0 {
						break
					}
					select {
					case <-ctx.Done():
						return
					case <-time.After(20 * time.Millisecond):
					}
				}
			}
		}

		lastResponseMu.Lock()
		lastResponseTime = time.Now()
		lastResponseMu.Unlock()
	}

	// Player goroutine: consumes the sentence queue sequentially. Each
	// sentence carries a turn sequence (orchestrator.ttsSeq). When a newer
	// turn arrives we (a) halt any sentence currently being spoken and
	// (b) drop every queued sentence belonging to an older, now-superseded
	// turn. This is what makes interruption production-grade: the previous
	// answer's tail is discarded instead of playing after the user takes
	// over, while the NEW reply (the highest seq) is always preserved.
	go func() {
		var playingSeq int64
		for item := range ttsSentCh {
			// Stale audio from a turn the user already interrupted: drop it.
			if item.seq > 0 && item.seq <= playingSeq {
				log.Printf("[TTS-PLAYER] Dropping stale sentence (turn %d <= %d): %.60s...", item.seq, playingSeq, item.text)
				if len(ttsSentCh) == 0 {
					atomic.StoreInt32(&isSpeaking, 0)
					atomic.StoreInt32(&stopPlayback, 0)
					lastTTSEndNano.Store(time.Now().UnixNano())
				}
				continue
			}
			// A newer turn started: halt the sentence currently being spoken
			// so the new reply begins promptly. playSentence resets
			// stopPlayback at its own start, so this never blocks the new turn.
			if item.seq > playingSeq {
				atomic.StoreInt32(&stopPlayback, 1)
				playingSeq = item.seq
			}
			log.Printf("[TTS-PLAYER] Playing sentence (len=%d, turn=%d): %.80s...", len(item.text), item.seq, item.text)
			playSentence(item.text, item.speed)
			log.Printf("[TTS-PLAYER] Finished playing sentence, queue_depth=%d", len(ttsSentCh))
			if len(ttsSentCh) == 0 {
				atomic.StoreInt32(&isSpeaking, 0)
				atomic.StoreInt32(&stopPlayback, 0) // ensure clean state for next response
				lastTTSEndNano.Store(time.Now().UnixNano())
			}
		}
		atomic.StoreInt32(&isSpeaking, 0)
		atomic.StoreInt32(&stopPlayback, 0)
		lastTTSEndNano.Store(time.Now().UnixNano())
	}()
	// Test TTS on startup
	go func() {
		if voiceCloneEnabled {
			log.Printf("[TTS] Voice cloning active (model: %s)", cfg.TTS.ActiveModel)
		}
		sr := cfg.TTS.OutputSampleRate
		if sr == 0 {
			sr = 44100
		}
		synthesize("System ready.", cfg.TTS.Supertonic.Speed, func(samples []float32) bool {
			playAudio(ctx, samples, sr, nil)
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

		// Stream generated sentences into the serialized TTS queue.
		// The playSentence function handles routing: when a browser client
		// is connected it publishes audio chunks via the event bus (bridge
		// forwards them to the browser); when no client is connected it
		// falls back to local audio playback.  The bus subscription added
		// below covers the local-only fallback path as well.
		orch.TTSFunc = func(text string, speed float32, seq int64) {
			log.Printf("[TTS-FUNC] Enqueuing sentence (len=%d, speed=%.2f, turn=%d): %.80s...", len(text), speed, seq, text)
			ttsSentCh <- ttsItem{text: text, speed: speed, seq: seq}
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
			seq, _ := event.Payload["seq"].(int64)
			log.Printf("[AGENT] Speaking (speed=%.2f, turn=%d): %s", speed, seq, text)
			speak(text, speed, seq)
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
			ttsStartedNano.Store(time.Now().UnixNano())
			bargeStartNano.Store(0)
			atomic.StoreInt32(&stopPlayback, 0)
			playErr := playAudioStreaming(ctx, 44100, &stopPlayback, func(ch chan<- []float32) {
				synthesize(response, cfg.TTS.Supertonic.Speed, func(samples []float32) bool {
					ch <- samples
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
				var csum float32
				for _, s := range clean {
					csum += s * s
				}
				crms := math.Sqrt(float64(csum / float32(len(clean))))
				lastMicMu.Lock()
				lastMicRMS = crms
				lastMicMu.Unlock()

				warm := time.Since(time.Unix(0, ttsStartedNano.Load())) > bargeInWarmup
				loud := crms > cfg.Audio.BargeInThreshold*bargeInMargin
				// Require sustained, clearly-louder-than-echo residual (not a
				// one-frame echo leak or reverb spike), so Mai's own voice
				// echoing through the mic doesn't get misread as a user
				// interruption and fed back to ASR (which makes her "say back"
				// what was said).
				if warm && loud {
					start := bargeStartNano.Load()
					if start == 0 {
						bargeStartNano.Store(time.Now().UnixNano())
					} else if held := time.Since(time.Unix(0, start)); held >= bargeInSustain {
						log.Printf("[BARGE-IN] Real speech over TTS detected (residual RMS=%.4f, held %v). Stopping playback.", crms, held)
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
				} else {
					bargeStartNano.Store(0)
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
		if nano := lastTTSEndNano.Load(); nano > 0 {
			elapsed := time.Since(time.Unix(0, nano))
			if elapsed < postTTSAECWindow {
				if echoCanceller != nil {
					clean := echoCanceller.Process(samples)
					var csum float32
					for _, s := range clean {
						csum += s * s
					}
					crms := math.Sqrt(float64(csum / float32(len(clean))))
					if crms < cfg.Audio.BargeInThreshold {
						return // just echo, drop it
					}
					// Residual above threshold — genuine speech, fall through
				} else {
					return
				}
			} else {
				lastTTSEndNano.Store(0)
			}
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

// publishTTSAudioChunk encodes a single callback chunk and pushes it onto the
// event bus so the bridge can forward it to the browser in real time.
func publishTTSAudioChunk(bus interfaces.EventBus, samples []float32, sampleRate int, done bool) {
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
