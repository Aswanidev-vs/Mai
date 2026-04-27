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
	"gopkg.in/yaml.v3"
)

// Config holds all settings.
type Config struct {
	Audio struct {
		SampleRate      int `yaml:"sample_rate"`
		CaptureBufferMs int `yaml:"capture_buffer_ms"`
	} `yaml:"audio"`
	KWS struct {
		ModelDir   string  `yaml:"model_dir"`
		Encoder    string  `yaml:"encoder"`
		Decoder    string  `yaml:"decoder"`
		Joiner     string  `yaml:"joiner"`
		Tokens     string  `yaml:"tokens"`
		Keywords   string  `yaml:"keywords"`
		NumThreads int     `yaml:"num_threads"`
		CooldownMs int     `yaml:"cooldown_ms"`
		Threshold  float32 `yaml:"confidence_threshold"`
	} `yaml:"kws"`
	VAD struct {
		Model              string  `yaml:"model"`
		WindowSize         int     `yaml:"window_size"`
		Threshold          float32 `yaml:"threshold"`
		MinSilenceDuration float32 `yaml:"min_silence_duration"`
		MinSpeechDuration  float32 `yaml:"min_speech_duration"`
		MaxSpeechDuration  float32 `yaml:"max_speech_duration"`
		NumThreads         int     `yaml:"num_threads"`
	} `yaml:"vad"`
	ASR struct {
		Type                    string  `yaml:"type"`
		ModelDir                string  `yaml:"model_dir"`
		Encoder                 string  `yaml:"encoder"`
		Decoder                 string  `yaml:"decoder"`
		Joiner                  string  `yaml:"joiner"`
		Tokens                  string  `yaml:"tokens"`
		ConvFrontend            string  `yaml:"conv_frontend"`
		Tokenizer               string  `yaml:"tokenizer"`
		Language                string  `yaml:"language"`
		DecodingMethod          string  `yaml:"decoding_method"`
		MaxActivePaths          int     `yaml:"max_active_paths"`
		EnableEndpoint          int     `yaml:"enable_endpoint"`
		Rule1MinTrailingSilence float32 `yaml:"rule1_min_trailing_silence"`
		Rule2MinTrailingSilence float32 `yaml:"rule2_min_trailing_silence"`
		Rule3MinUtteranceLength float32 `yaml:"rule3_min_utterance_length"`
		NumThreads              int     `yaml:"num_threads"`
	} `yaml:"asr"`
	TTS struct {
		ActiveModel string `yaml:"active_model"`
		NumThreads  int    `yaml:"num_threads"`
		Supertonic  struct {
			ModelDir          string  `yaml:"model_dir"`
			DurationPredictor string  `yaml:"duration_predictor"`
			TextEncoder       string  `yaml:"text_encoder"`
			VectorEstimator   string  `yaml:"vector_estimator"`
			Vocoder           string  `yaml:"vocoder"`
			TTSJson           string  `yaml:"tts_json"`
			UnicodeIndexer    string  `yaml:"unicode_indexer"`
			VoiceStyle        string  `yaml:"voice_style"`
			Sid               int     `yaml:"sid"`
			NumSteps          int     `yaml:"num_steps"`
			Speed             float32 `yaml:"speed"`
		} `yaml:"supertonic"`
		Pocket struct {
			ModelDir        string `yaml:"model_dir"`
			LmFlow          string `yaml:"lm_flow"`
			LmMain          string `yaml:"lm_main"`
			Encoder         string `yaml:"encoder"`
			Decoder         string `yaml:"decoder"`
			TextConditioner string `yaml:"text_conditioner"`
			VocabJson       string `yaml:"vocab_json"`
			TokenScoresJson string `yaml:"token_scores_json"`
		} `yaml:"pocket"`
		ZipVoice struct {
			ModelDir string `yaml:"model_dir"`
			Encoder  string `yaml:"encoder"`
			Decoder  string `yaml:"decoder"`
			DataDir  string `yaml:"data_dir"`
			Lexicon  string `yaml:"lexicon"`
			Tokens   string `yaml:"tokens"`
			Vocoder  string `yaml:"vocoder"`
		} `yaml:"zipvoice"`
		VoiceCloning struct {
			Enabled        bool   `yaml:"enabled"`
			Model          string `yaml:"model"`
			ReferenceAudio string `yaml:"reference_audio"`
		} `yaml:"voice_cloning"`
	} `yaml:"tts"`
	LLM struct {
		Provider     string `yaml:"provider"`
		Model        string `yaml:"model"`
		URL          string `yaml:"url"`
		AutoStart    bool   `yaml:"auto_start"`
		SystemPrompt string `yaml:"system_prompt"`
	} `yaml:"llm"`
}

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
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		log.Fatalf("Failed to parse config: %v", err)
	}

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
	auto := NewAutomation()
	executor := NewActionExecutor(auto)
	log.Println("[AUTO] Automation system ready")

	// 5. Initialize audio capture

	var isSpeaking bool
	var lastResponseTime time.Time
	var sessionSamples []float32
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
				response, err = generateOllamaResponse(cfg, prompt)
				if err != nil {
					log.Printf("[LLM] Error generating contextual response: %v", err)
					response = feedback // Fallback to simple feedback
				}
			} else {
				// No action detected - normal LLM flow
				response, err = generateOllamaResponse(cfg, task.Text)
				if err != nil {
					log.Printf("[LLM] Error: %v", err)
					isSpeaking = false
					continue
				}
			}

			log.Printf("[LLM] Response received. Starting TTS...")

			// TTS
			audio := tts.Generate(response, cfg.TTS.Supertonic.Sid, cfg.TTS.Supertonic.Speed)
			if audio != nil {
				log.Println("[TTS] Playing response...")
				playAudio(audio.Samples, audio.SampleRate)
			}

			isSpeaking = false // Resume ASR
			lastResponseTime = time.Now()
			log.Println("[FOLLOW-UP] Listening for follow-up (10s window)...")
		}
	}()

	// 7. State machine
	type State int
	const (
		StateWakeWord State = iota
		StateListening
	)

	state := StateWakeWord
	lastDetected := time.Now().Add(-time.Hour)
	var lastText string
	var sessionText string

	// Audio callback
	capture.onSamples = func(samples []float32) {
		if isSpeaking {
			return // Ignore samples while Mai is talking
		}
		switch state {
		case StateWakeWord:
			// If in follow-up window, allow VAD to trigger listening
			if time.Since(lastResponseTime) < 10*time.Second {
				// Feed to ASR continuously if streaming
				if asrStream != nil {
					asrStream.AcceptWaveform(16000, samples)
				}

				// Feed to VAD buffer for speech detection
				vadBuffer.Push(samples)
				for vadBuffer.Size() >= cfg.VAD.WindowSize {
					head := vadBuffer.Head()
					chunk := vadBuffer.Get(head, cfg.VAD.WindowSize)
					vadBuffer.Pop(cfg.VAD.WindowSize)
					vadDetector.AcceptWaveform(chunk)
				}

				// Check if VAD has detected speech segments
				if !vadDetector.IsEmpty() {
					log.Println("[FOLLOW-UP] Speech detected! Skipping wake word.")
					state = StateListening
					// Clear VAD segments - they were just for start detection
					for !vadDetector.IsEmpty() {
						vadDetector.Pop()
					}
					sessionText = ""
					lastText = ""
					sessionSamples = nil
					if recognizer != nil {
						recognizer.Reset(asrStream)
					}
					return
				}
			}

			// Feed to Keyword Spotter
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
					sessionText = result.Text
					sherpa.DeleteOfflineStream(offlineStream)
					sessionSamples = nil // Clear buffer
				}

				log.Println("\n[VAD] End of segment detected.")

				if sessionText != "" {
					log.Println("[PIPELINE] Processing full sentence...")
					workerChan <- Task{Text: sessionText}
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
	log.Println("\nShutting down...")
	capture.Stop()
	close(workerChan)
	wg.Wait()
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
func generateOllamaResponse(cfg Config, prompt string) (string, error) {
	client := &http.Client{Timeout: 5 * time.Minute} // Increased to 5 minutes

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":  cfg.LLM.Model,
		"prompt": prompt,
		"system": cfg.LLM.SystemPrompt,
		"stream": false,
	})

	log.Printf("[OLLAMA] Requesting response from %s...", cfg.LLM.Model)
	resp, err := client.Post(cfg.LLM.URL, "application/json", bytes.NewBuffer(requestBody))
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
