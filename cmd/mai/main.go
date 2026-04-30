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
	"syscall"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/user/mai/internal/agent"
	"github.com/user/mai/internal/cognition"
	"github.com/user/mai/internal/events"
	"github.com/user/mai/internal/llm"
	"github.com/user/mai/internal/memory"
	"github.com/user/mai/internal/perception"
	"github.com/user/mai/internal/tools"
	"github.com/user/mai/internal/tools/adapters"
	"github.com/user/mai/pkg/interfaces"
	"github.com/user/mai/pkg/models"
	"gopkg.in/yaml.v3"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	var configPath string
	flag.StringVar(&configPath, "config", "config.yaml", "Path to configuration file")
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var isSpeaking bool
	var lastResponseTime time.Time
	var lastDetected time.Time = time.Now().Add(-time.Hour)
	var sessionSamples []float32
	var ttsMu sync.Mutex    // Mutex for thread-safe TTS
	var sherpaMu sync.Mutex // Mutex for all other Sherpa-ONNX calls

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

	log.Printf("[TTS] Synthesizer ready (%s)", cfg.TTS.ActiveModel)
	// Test TTS on startup
	go func() {
		testAudio := tts.Generate("System ready.", cfg.TTS.Supertonic.Sid, cfg.TTS.Supertonic.Speed)
		if testAudio != nil {
			playAudio(testAudio.Samples, testAudio.SampleRate)
		}
	}()

	// Initialize automation system
	auto := NewAutomation(cfg.Vision.Model, cfg.Vision.URL, cfg.Vision.Enabled)
	executor := NewActionExecutor(auto)

	// 0. Initialize Agentic Architecture if enabled
	var agentBridge *perception.Bridge
	if cfg.Agentic.Enabled {
		log.Println("[BOOT] Initializing Agentic Architecture...")
		bus := events.NewBus()

		// Memory
		workingMem := memory.NewWorkingMemory(10)
		episodicMem, _ := memory.NewEpisodicStore("data/memory/episodic.db")
		memManager := memory.NewMemoryManager(workingMem, episodicMem, nil, nil) // Stubs for others

		// LLM
		llmFactory := llm.NewFactory(cfg)
		llmProvider, err := llmFactory.CreateHybridProvider()
		if err != nil {
			log.Fatalf("[BOOT] Failed to create LLM provider: %v", err)
		}

		// Tools
		registry := tools.NewRegistry()
		registry.Register(&adapters.ShellTool{})
		registry.Register(&adapters.WebSearchTool{})
		registry.Register(&adapters.OpenAppTool{})
		registry.Register(&adapters.YouTubeTool{})
		registry.Register(adapters.NewDeepSearchTool())
		registry.Register(&adapters.FileWriteTool{})
		registry.Register(&adapters.ClockTool{})
		registry.Register(&adapters.WhatsAppTool{})
		registry.Register(&adapters.AutomationTool{})

		// Cognition
		react := cognition.NewReActLoop(llmProvider, registry, workingMem)

		// Orchestrator
		orch := agent.NewOrchestrator(bus, memManager, llmProvider, registry, react)
		orch.DirectAction = executor.ParseAndExecute // Wire up the legacy highly-reliable regex parser
		go orch.Start(ctx)

		// Bridge for Perception
		agentBridge = perception.NewBridge(bus)

		// Bridge for TTS
		bus.Subscribe("action.tts.request", func(event interfaces.Event) {
			text, _ := event.Payload["text"].(string)
			log.Printf("[AGENT] Speaking: %s", text)
			isSpeaking = true
			ttsMu.Lock()
			audio := tts.Generate(text, cfg.TTS.Supertonic.Sid, cfg.TTS.Supertonic.Speed)
			ttsMu.Unlock()
			if audio != nil {
				playAudio(audio.Samples, audio.SampleRate)
			}
			time.Sleep(400 * time.Millisecond) // Wait for room echo to die down
			isSpeaking = false
			lastResponseTime = time.Now()
			log.Printf("[FOLLOW-UP] Listening for follow-up (15s window)...")
		})
	}
	log.Println("[AUTO] Automation system ready")

	// 5. Initialize audio capture

	// 5. Initialize audio capture
	capture := newAudioCapture(16000, 1)
	defer capture.Close()

	log.Println("[AUDIO] Capture initialized")

	// 6. Pipeline Worker (LLM + TTS + Actions)
	type Task struct {
		Text string
	}
	workerChan := make(chan Task, 10)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for task := range workerChan {
			isSpeaking = true // Pause ASR while thinking and talking
			log.Printf("[LLM] Thinking about: %s", task.Text)

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
					isSpeaking = false
					continue
				}
			}

			log.Printf("[LLM] Response received. Starting TTS...")

			// TTS
			ttsMu.Lock()
			audio := tts.Generate(response, cfg.TTS.Supertonic.Sid, cfg.TTS.Supertonic.Speed)
			ttsMu.Unlock()
			if audio != nil {
				log.Println("[TTS] Playing response...")
				playAudio(audio.Samples, audio.SampleRate)
			}

			time.Sleep(400 * time.Millisecond) // Wait for room echo to die down
			isSpeaking = false // Resume ASR
			lastResponseTime = time.Now()
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
	capture.onSamples = func(samples []float32) {
		sherpaMu.Lock()
		defer sherpaMu.Unlock()

		if isSpeaking {
			return // Ignore samples while Mai is talking
		}
		switch state {
		case StateWakeWord:
			// If in follow-up window, allow VAD to trigger listening
			if time.Since(lastResponseTime) < 15*time.Second {
				// Feed to ASR continuously if streaming
				if asrStream != nil {
					asrStream.AcceptWaveform(16000, samples)
				}

				// Feed to VAD buffer for speech detection
				vadBuffer.Push(samples)
				var lastChunk []float32
				for vadBuffer.Size() >= cfg.VAD.WindowSize {
					head := vadBuffer.Head()
					lastChunk = vadBuffer.Get(head, cfg.VAD.WindowSize)
					vadBuffer.Pop(cfg.VAD.WindowSize)
					vadDetector.AcceptWaveform(lastChunk)
				}

				// Check if VAD has detected speech segments
				if !vadDetector.IsEmpty() {
					// Check RMS level to ensure it's not just silence/noise
					var sum float32
					for _, s := range samples {
						sum += s * s
					}
					rms := math.Sqrt(float64(sum / float32(len(samples))))

					if rms > 0.001 { // Much more sensitive for follow-up
						log.Printf("[FOLLOW-UP] Speech detected (Level %.4f)! Skipping wake word.", rms)
						state = StateListening
						
						// Prepend the lookback buffer to catch the start of the sentence
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
						// Clear VAD segments
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

			// Feed to KWS/VAD buffer
			kwsStream.AcceptWaveform(16000, samples)

			if asrStream != nil {
				asrStream.AcceptWaveform(16000, samples)
			} else {
				// For offline models, we still want to keep track of the audio
				// if we might be in a follow-up window.
				// However, we don't buffer HERE yet, because we haven't switched to StateListening.
			}

			// Volume check (RMS)
			var sum float32
			for _, s := range samples {
				sum += s * s
			}
			rms := math.Sqrt(float64(sum / float32(len(samples))))
			fmt.Printf("\r[AUDIO] Level: %.4f ", rms)

			if time.Since(lastDetected) < time.Duration(cfg.KWS.CooldownMs)*time.Millisecond {
				return
			}
			for spotter.IsReady(kwsStream) {
				spotter.Decode(kwsStream)
				fmt.Print("*") // Small star for every decode attempt
				result := spotter.GetResult(kwsStream)
				if result.Keyword != "" {
					spotter.Reset(kwsStream)
					lastDetected = time.Now()
					log.Println("\n[WAKE] Detected! Listening...")

					// JARVIS-style acknowledgement
					go func() {
						// JARVIS-style acknowledgment (more sophisticated)
						greetings := []string{
							"Yes Sir. How can I assist you?",
							"At your service. What is the objective?",
							"I'm here. What do you need?",
						}
						greet := greetings[time.Now().UnixNano()%int64(len(greetings))]
						ttsMu.Lock()
						audio := tts.Generate(greet, cfg.TTS.Supertonic.Sid, cfg.TTS.Supertonic.Speed)
						ttsMu.Unlock()
						if audio != nil {
							playAudio(audio.Samples, audio.SampleRate)
						}
					}()

					state = StateListening
					sessionText = "" // Reset session text
					sessionSamples = nil
					vadBuffer = sherpa.NewCircularBuffer(10 * 16000)
					if recognizer != nil {
						recognizer.Reset(asrStream)
					}
					lastText = ""
					return
				}
			}

		case StateListening:
			// Feed to VAD
			vadBuffer.Push(samples)
			for vadBuffer.Size() >= cfg.VAD.WindowSize {
				head := vadBuffer.Head()
				chunk := vadBuffer.Get(head, cfg.VAD.WindowSize)
				vadBuffer.Pop(cfg.VAD.WindowSize)
				vadDetector.AcceptWaveform(chunk)
			}

			// Feed to ASR
			if asrStream != nil {
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
				// Offline ASR - buffer audio samples
				sessionSamples = append(sessionSamples, samples...)
				fmt.Printf("\r[ASR] Listening... (buffered %d samples)", len(sessionSamples))
			}

			// Check VAD for speech end
			for !vadDetector.IsEmpty() {
				vadDetector.Pop()

				// When a segment ends, add it to our session buffer
				if asrStream != nil {
					text := recognizer.GetResult(asrStream).Text
					if text != "" {
						sessionText += text + " "
					}
				} else if offlineRecognizer != nil {
					// Process full buffer with Offline ASR
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
					sessionSamples = nil // Clear buffer
				}

				log.Println("\n[VAD] End of segment detected.")

				if sessionText != "" {
					if agentBridge != nil {
						log.Println("[AGENT] Routing to cognitive orchestrator...")
						// Use a goroutine to avoid blocking the audio thread
						go agentBridge.PublishTranscription(sessionText)
					} else {
						log.Println("[PIPELINE] Routing to legacy pipeline...")
						workerChan <- Task{Text: sessionText}
					}
					state = StateWakeWord
					sessionText = ""
					if recognizer != nil {
						recognizer.Reset(asrStream)
					}
					lastText = ""
					return
				}

			}
		}
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

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":  cfg.LLM.Model,
		"prompt": prompt,
		"system": cfg.LLM.SystemPrompt,
		"stream": false,
	})

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

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
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
