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
		cfg.LLM.Sampling.MaxTokens = 400
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

	// Shared across main scope so greeting / TTS / browser-mic paths can reach the bus.
	var bus interfaces.EventBus
	var companionServer *server.Server
	var browserMicActive int32
	companionHasClient := func() bool { return companionServer != nil && companionServer.ClientCount() > 0 }
	var capture *audioCapture                  // forward-declared; assigned later
	var handleAudioFrame func([]float32, bool) // forward-declared; assigned later

	// Echo cancellation for genuine barge-in: subtracts Mai's own TTS (echoed
	// through the mic) so only a real second speaker survives in the residual.
	echoCanceller := NewEchoCanceller(1024)
	var bargeCount int
	var ttsStartedAt time.Time

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
	kwsConfig.ModelConfig.Provider = "cpu"

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
	vadConfig.Provider = "cpu"

	vadDetector := sherpa.NewVoiceActivityDetector(&vadConfig, 20)
	if vadDetector == nil {
		log.Fatal("Failed to create VAD")
	}
	defer sherpa.DeleteVoiceActivityDetector(vadDetector)

	vadBuffer := sherpa.NewCircularBuffer(10 * 16000)
	defer sherpa.DeleteCircularBuffer(vadBuffer)

	log.Println("[VAD] Voice activity detector ready")

	// 3. Initialize ASR
	var recognizer *sherpa.OnlineRecognizer
	var offlineRecognizer *sherpa.OfflineRecognizer
	var asrStream *sherpa.OnlineStream

	if cfg.ASR.Type == "qwen3" {
		offlineConfig := sherpa.OfflineRecognizerConfig{}
		offlineConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
		offlineConfig.ModelConfig.Qwen3ASR.ConvFrontend = join(cfg.ASR.ModelDir, cfg.ASR.ConvFrontend)
		offlineConfig.ModelConfig.Qwen3ASR.Encoder = join(cfg.ASR.ModelDir, cfg.ASR.Encoder)
		offlineConfig.ModelConfig.Qwen3ASR.Decoder = join(cfg.ASR.ModelDir, cfg.ASR.Decoder)
		offlineConfig.ModelConfig.Qwen3ASR.Tokenizer = join(cfg.ASR.ModelDir, cfg.ASR.Tokenizer)
		offlineConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
		offlineConfig.ModelConfig.Provider = "cpu"
		offlineConfig.DecodingMethod = "greedy_search"

		offlineRecognizer = sherpa.NewOfflineRecognizer(&offlineConfig)
		if offlineRecognizer == nil {
			log.Fatal("Failed to create Offline ASR recognizer")
		}
		defer sherpa.DeleteOfflineRecognizer(offlineRecognizer)
		log.Println("[ASR] Offline Qwen3 recognizer ready")
	} else {
		asrConfig := sherpa.OnlineRecognizerConfig{}
		asrConfig.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}

		if cfg.ASR.Type == "nemo" {
			asrConfig.ModelConfig.NemoCtc.Model = join(cfg.ASR.ModelDir, cfg.ASR.Encoder)
			asrConfig.ModelConfig.Tokens = join(cfg.ASR.ModelDir, cfg.ASR.Tokens)
		} else {
			// Default to Transducer (Zipformer)
			asrConfig.ModelConfig.Transducer.Encoder = join(cfg.ASR.ModelDir, cfg.ASR.Encoder)
			asrConfig.ModelConfig.Transducer.Decoder = join(cfg.ASR.ModelDir, cfg.ASR.Decoder)
			asrConfig.ModelConfig.Transducer.Joiner = join(cfg.ASR.ModelDir, cfg.ASR.Joiner)
			asrConfig.ModelConfig.Tokens = join(cfg.ASR.ModelDir, cfg.ASR.Tokens)
		}

		asrConfig.ModelConfig.NumThreads = cfg.ASR.NumThreads
		asrConfig.ModelConfig.Provider = "cpu"
		asrConfig.DecodingMethod = cfg.ASR.DecodingMethod
		asrConfig.MaxActivePaths = cfg.ASR.MaxActivePaths
		asrConfig.EnableEndpoint = cfg.ASR.EnableEndpoint
		asrConfig.Rule1MinTrailingSilence = cfg.ASR.Rule1MinTrailingSilence
		asrConfig.Rule2MinTrailingSilence = cfg.ASR.Rule2MinTrailingSilence
		asrConfig.Rule3MinUtteranceLength = cfg.ASR.Rule3MinUtteranceLength

		recognizer = sherpa.NewOnlineRecognizer(&asrConfig)
		if recognizer == nil {
			log.Fatal("Failed to create ASR recognizer")
		}
		defer sherpa.DeleteOnlineRecognizer(recognizer)

		asrStream = sherpa.NewOnlineStream(recognizer)
		if asrStream == nil {
			log.Fatal("Failed to create ASR stream")
		}
		defer sherpa.DeleteOnlineStream(asrStream)
		log.Println("[ASR] Streaming recognizer ready")
	}

	// 4. Initialize TTS
	ttsConfig := sherpa.OfflineTtsConfig{}
	ttsConfig.Model.NumThreads = cfg.TTS.NumThreads
	ttsConfig.Model.Provider = "cpu"

	switch cfg.TTS.ActiveModel {
	case "supertonic":
		ttsConfig.Model.Supertonic.DurationPredictor = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.DurationPredictor)
		ttsConfig.Model.Supertonic.TextEncoder = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.TextEncoder)
		ttsConfig.Model.Supertonic.VectorEstimator = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.VectorEstimator)
		ttsConfig.Model.Supertonic.Vocoder = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.Vocoder)
		ttsConfig.Model.Supertonic.TtsJson = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.TTSJson)
		ttsConfig.Model.Supertonic.UnicodeIndexer = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.UnicodeIndexer)
		ttsConfig.Model.Supertonic.VoiceStyle = join(cfg.TTS.Supertonic.ModelDir, cfg.TTS.Supertonic.VoiceStyle)
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
			// Build the per-request config so supertonic gets NumSteps and the
			// Extra JSON (e.g. {"lang": "en"}). GenerateWithCallback would drop
			// these, leaving NumSteps=0 and no language hint — which makes
			// supertonic synthesize silence.
			genCfg := &sherpa.GenerationConfig{
				Speed:    speed,
				Sid:      cfg.TTS.Supertonic.Sid,
				NumSteps: cfg.TTS.Supertonic.NumSteps,
			}
			if cfg.TTS.Supertonic.Extra != "" {
				genCfg.Extra = json.RawMessage(cfg.TTS.Supertonic.Extra)
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
	}
	ttsSentCh := make(chan ttsItem, 64)

	log.Printf("[TTS] Synthesizer ready (%s)", cfg.TTS.ActiveModel)

	// speak enqueues a sentence for the streaming player (see playSentence +
	// the player goroutine below). Keeping a single consumer serializes all
	// TTS so sentences never overlap, and lets barge-in drain the queue.
	speak := func(text string, speed float32) {
		if speed == 0 {
			speed = cfg.TTS.Supertonic.Speed
		}
		ttsSentCh <- ttsItem{text: text, speed: speed}
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
		ttsStartedAt = time.Now()
		atomic.StoreInt32(&stopPlayback, 0)
		defer atomic.StoreInt32(&ttsPlaying, 0)
		log.Printf("[TTS-PLAY] Starting synthesis: speed=%.2f, text=%.80s...", speed, text)

		if bus != nil && cfg.Server.Enabled && companionHasClient() {
			// ── Browser companion: stream chunks, stop on barge-in ──
			synthesize(text, speed, func(samples []float32) bool {
				if atomic.LoadInt32(&stopPlayback) != 0 {
					return false
				}
				// Feed the synthesized samples into the echo reference so the
				// AEC can subtract Mai's own voice (echoed through the browser
				// mic) — otherwise barge-in detection would misread her echo as
				// a user interruption and cut her off.
				if len(samples) > 0 {
					resampledForEcho := ttsToEchoResampler.resample(samples)
					refBuffer.Push(resampledForEcho)
					// Resample to 44.1k for browser playback.
					resampledForBrowser := ttsToBrowserResampler.resample(samples)
					publishTTSAudioChunk(bus, resampledForBrowser, 44100, false)
				}
				return true
			})
			publishTTSAudioChunk(bus, nil, 44100, true)
		} else {
			// ── Local-only path: streaming playback ──
			atomic.StoreInt32(&ttsPlaying, 1)
			atomic.StoreInt32(&stopPlayback, 0)
			_ = playAudioStreaming(ctx, 44100, &stopPlayback, func(ch chan<- []float32) {
				synthesize(text, speed, func(samples []float32) bool {
					if atomic.LoadInt32(&stopPlayback) != 0 {
						return false
					}
					// Resample TTS native rate to 44.1k for local playback.
					resampled := ttsToBrowserResampler.resample(samples)
					ch <- resampled
					return true
				})
			})
			atomic.StoreInt32(&ttsPlaying, 0)
		}

		lastResponseMu.Lock()
		lastResponseTime = time.Now()
		lastResponseMu.Unlock()
	}

	// Player goroutine: consumes the sentence queue sequentially. Skips any
	// sentence left queued after a barge-in (stopPlayback). isSpeaking stays
	// set across sentences and is only cleared once the queue drains.
	go func() {
		for item := range ttsSentCh {
			if atomic.LoadInt32(&stopPlayback) != 0 {
				log.Printf("[TTS-PLAYER] Skipping sentence due to barge-in: %.60s...", item.text)
				// If we've skipped the last item in the queue, clear stopPlayback
				// so a fresh TTS response can start playing.
				if len(ttsSentCh) == 0 {
					atomic.StoreInt32(&stopPlayback, 0)
				}
				continue // dropped due to an interruption
			}
			log.Printf("[TTS-PLAYER] Playing sentence (len=%d): %.80s...", len(item.text), item.text)
			playSentence(item.text, item.speed)
			log.Printf("[TTS-PLAYER] Finished playing sentence, queue_depth=%d", len(ttsSentCh))
			if len(ttsSentCh) == 0 {
				atomic.StoreInt32(&isSpeaking, 0)
				atomic.StoreInt32(&stopPlayback, 0) // ensure clean state for next response
			}
		}
		atomic.StoreInt32(&isSpeaking, 0)
		atomic.StoreInt32(&stopPlayback, 0)
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
		orch := agent.NewOrchestrator(bus, memManager, llmProvider, registry, react,
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
		orch.TTSFunc = func(text string, speed float32) {
			log.Printf("[TTS-FUNC] Enqueuing sentence (len=%d, speed=%.2f): %.80s...", len(text), speed, text)
			ttsSentCh <- ttsItem{text: text, speed: speed}
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

		// Bridge for TTS — with emotion-adaptive parameters
		bus.Subscribe("action.tts.request", func(event interfaces.Event) {
			text, _ := event.Payload["text"].(string)
			speed, _ := event.Payload["speed"].(float32)
			log.Printf("[AGENT] Speaking (speed=%.2f): %s", speed, text)
			speak(text, speed)
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
		Text          string
		IsReasoning   bool // true if task requires actual reasoning (not just chat)
	}
	workerChan := make(chan Task, 10)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for task := range workerChan {
			atomic.StoreInt32(&isSpeaking, 1) // Pause ASR while thinking and talking
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
			atomic.StoreInt32(&ttsPlaying, 1)
			ttsStartedAt = time.Now()
			bargeCount = 0
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

	// Audio callback
	// runListening: the VAD + ASR + segment-end detection pipeline.
	// Shared by local mic (in listening state) and browser mic.
	runListening := func(samples []float32) {
		if atomic.LoadInt32(&ttsPlaying) != 0 {
			return
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
			for recognizer.IsReady(asrStream) {
				recognizer.Decode(asrStream)
			}
			text := recognizer.GetResult(asrStream).Text
			if text != "" && text != lastText {
				lastText = text
				fmt.Printf("\r[ASR] Live: %s%s", sessionText, text)
			}
		} else {
			sessionSamples = append(sessionSamples, samples...)
			fmt.Printf("\r[ASR] Listening... (buffered %d samples)", len(sessionSamples))
		}

		for !vadDetector.IsEmpty() {
			vadDetector.Pop()

			if asrStream != nil {
				text := recognizer.GetResult(asrStream).Text
				if text != "" {
					sessionText += text + " "
				}
				sessionSamples = nil
			} else if offlineRecognizer != nil {
				log.Println("\n[ASR] Processing segment with Offline Qwen3...")
				offlineStream := sherpa.NewOfflineStream(offlineRecognizer)
				if cfg.ASR.Language != "" {
					offlineStream.SetOption("language", cfg.ASR.Language)
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
				if agentBridge != nil {
					log.Println("[AGENT] Routing to cognitive orchestrator...")
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
				if recognizer != nil {
					recognizer.Reset(asrStream)
				}
				lastText = ""
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
			if cfg.Audio.BargeInEnabled && echoCanceller != nil && time.Since(ttsStartedAt) > 400*time.Millisecond {
				clean := echoCanceller.Process(samples)
				var csum float32
				for _, s := range clean {
					csum += s * s
				}
				crms := math.Sqrt(float64(csum / float32(len(clean))))
				lastMicMu.Lock()
				lastMicRMS = crms
				lastMicMu.Unlock()

				if crms > cfg.Audio.BargeInThreshold {
					bargeCount++
					// Require sustained residual (not a single-frame echo leak)
					// and a margin above threshold, so Mai's own voice echoing
					// through the mic doesn't get misread as a user interruption
					// and fed back to ASR (which makes her "say back" what was said).
					if bargeCount >= 4 && crms > cfg.Audio.BargeInThreshold*2 {
						log.Printf("[BARGE-IN] Real speech over TTS detected (residual RMS=%.4f). Stopping playback.", crms)
						atomic.StoreInt32(&stopPlayback, 1)
						if interruptCurrent != nil {
							interruptCurrent()
						}
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
						bargeCount = 0
						return
					}
				} else {
					bargeCount = 0
				}
			}
			// No genuine interruption: her echo is not user input, so drop the frame.
			return
		}

		if atomic.LoadInt32(&isSpeaking) != 0 {
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

					// Route greeting through the same speak() helper so it streams
					// to the browser companion (lips sync) when a client is connected.
					go func() {
						greetings := []string{
							"Yes Sir. How can I assist you?",
							"At your service. What is the objective?",
							"I'm here. What do you need?",
						}
						greet := greetings[time.Now().UnixNano()%int64(len(greetings))]
						speak(greet, cfg.TTS.Supertonic.Speed)
					}()

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
		"model":  cfg.LLM.Model,
		"prompt": prompt,
		"system": cfg.LLM.SystemPrompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": cfg.LLM.Sampling.Temperature,
			"top_p":       cfg.LLM.Sampling.TopP,
			"num_predict": cfg.LLM.Sampling.MaxTokens,
		},
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
