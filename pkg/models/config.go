package models

// Config holds all settings.
type Config struct {
	Audio struct {
		SampleRate       int     `yaml:"sample_rate"`
		CaptureBufferMs  int     `yaml:"capture_buffer_ms"`
		BargeInEnabled   bool    `yaml:"barge_in_enabled"`
		BargeInThreshold float64 `yaml:"barge_in_threshold"`
		BargeInWarmupMs  int     `yaml:"barge_in_warmup_ms"`  // AEC convergence wait before barge-in arms; <=0 = 400ms
		BargeInSustainMs int     `yaml:"barge_in_sustain_ms"` // Sustained speech required to trigger; <=0 = 150ms
		ThinkingChime    bool    `yaml:"thinking_chime"`
	} `yaml:"audio"`
	KWS struct {
		Provider   string  `yaml:"provider"` // "cpu", "cuda", "coreml", "opencl"
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
		Provider           string  `yaml:"provider"` // "cpu", "cuda", "coreml", "opencl"
		Model              string  `yaml:"model"`
		WindowSize         int     `yaml:"window_size"`
		Threshold          float32 `yaml:"threshold"`
		MinSilenceDuration float32 `yaml:"min_silence_duration"`
		MinSpeechDuration  float32 `yaml:"min_speech_duration"`
		MaxSpeechDuration  float32 `yaml:"max_speech_duration"`
		NumThreads         int     `yaml:"num_threads"`
	} `yaml:"vad"`
	ASR struct {
		// ActiveModel selects which model to use: "qwen3" | "nemotron" | "omnilingual" | "transducer"
		ActiveModel string `yaml:"active_model"`
		Provider    string `yaml:"provider"` // "cpu", "cuda", "coreml", "opencl"
		NumThreads  int    `yaml:"num_threads"`
		Debug       int    `yaml:"debug"`

		// Dual-model: use a secondary offline model for unsupported languages
		DualMode struct {
			Enabled           bool     `yaml:"enabled"`
			FallbackModel     string   `yaml:"fallback_model"`     // "qwen3" | "omnilingual"
			FallbackLanguages []string `yaml:"fallback_languages"` // e.g. ["ml"] for Malayalam
		} `yaml:"dual_mode"`

		// Qwen3-ASR (offline, LLM-based, 30+ languages)
		Qwen3 struct {
			ModelDir       string `yaml:"model_dir"`
			ConvFrontend   string `yaml:"conv_frontend"`
			Encoder        string `yaml:"encoder"`
			Decoder        string `yaml:"decoder"`
			Tokenizer      string `yaml:"tokenizer"`
			Language       string `yaml:"language"`
			DecodingMethod string `yaml:"decoding_method"`
		} `yaml:"qwen3"`

		// Nemotron 3.5 (streaming transducer, 40 languages).
		// model_dir selects the latency variant (e.g. ...-560ms-... vs ...-1120ms-...);
		// both share this same streaming-transducer path.
		Nemotron struct {
			ModelDir                string  `yaml:"model_dir"`
			Encoder                 string  `yaml:"encoder"`
			Decoder                 string  `yaml:"decoder"`
			Joiner                  string  `yaml:"joiner"`
			Tokens                  string  `yaml:"tokens"`
			Language                string  `yaml:"language"` // "en", "auto", etc.
			DecodingMethod          string  `yaml:"decoding_method"`
			MaxActivePaths          int     `yaml:"max_active_paths"`
			EnableEndpoint          int     `yaml:"enable_endpoint"`
			Rule1MinTrailingSilence float32 `yaml:"rule1_min_trailing_silence"`
			Rule2MinTrailingSilence float32 `yaml:"rule2_min_trailing_silence"`
			Rule3MinUtteranceLength float32 `yaml:"rule3_min_utterance_length"`
		} `yaml:"nemotron"`

		// Omnilingual ASR (offline CTC, 1600+ languages incl. Malayalam)
		Omnilingual struct {
			ModelDir string `yaml:"model_dir"`
			Model    string `yaml:"model"`
			Tokens   string `yaml:"tokens"`
			Language string `yaml:"language"` // "auto" or specific lang
		} `yaml:"omnilingual"`

		// Transducer (Zipformer, Conformer, etc.)
		Transducer struct {
			ModelDir                string  `yaml:"model_dir"`
			Encoder                 string  `yaml:"encoder"`
			Decoder                 string  `yaml:"decoder"`
			Joiner                  string  `yaml:"joiner"`
			Tokens                  string  `yaml:"tokens"`
			Language                string  `yaml:"language"`
			DecodingMethod          string  `yaml:"decoding_method"`
			MaxActivePaths          int     `yaml:"max_active_paths"`
			EnableEndpoint          int     `yaml:"enable_endpoint"`
			Rule1MinTrailingSilence float32 `yaml:"rule1_min_trailing_silence"`
			Rule2MinTrailingSilence float32 `yaml:"rule2_min_trailing_silence"`
			Rule3MinUtteranceLength float32 `yaml:"rule3_min_utterance_length"`
		} `yaml:"transducer"`
	} `yaml:"asr"`
	TTS struct {
		Provider         string  `yaml:"provider"` // "cpu", "cuda", "coreml", "opencl"
		ActiveModel      string  `yaml:"active_model"`
		NumThreads       int     `yaml:"num_threads"`
		OutputSampleRate int     `yaml:"output_sample_rate"`
		TTSVoiceStyle    string  `yaml:"voice_style"` // Optional: "calm", "warm", "energetic", "serious", "soft"
		BaseSpeed        float32 `yaml:"base_speed"`  // Baseline speech rate; lower = warmer/calmer (supertonic only honors speed)
		Supertonic       struct {
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
			Extra             string  `yaml:"extra"` // Opaque JSON passed to supertonic (e.g. '{"lang": "en"}')
		} `yaml:"supertonic"`
		Kokoro struct {
			ModelDir    string  `yaml:"model_dir"`
			Model       string  `yaml:"model"`
			Voices      string  `yaml:"voices"`
			Tokens      string  `yaml:"tokens"`
			DataDir     string  `yaml:"data_dir"`
			Lexicon     string  `yaml:"lexicon"`
			Lang        string  `yaml:"lang"`
			Sid         int     `yaml:"sid"` // Kokoro speaker/voice id (0 = af_heart, the most natural US female)
			LengthScale float32 `yaml:"length_scale"`
		} `yaml:"kokoro"`
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
			ReferenceText  string `yaml:"reference_text"`
		} `yaml:"voice_cloning"`
	} `yaml:"tts"`
	LLM struct {
		Provider     string `yaml:"provider"` // Default provider: "ollama", "openai", "gemini", "claude", "openrouter", "llamacpp"
		Model        string `yaml:"model"`
		URL          string `yaml:"url"`
		APIKey       string `yaml:"api_key"`
		AutoStart    bool   `yaml:"auto_start"`
		SystemPrompt string `yaml:"system_prompt"`
		HybridMode   bool   `yaml:"hybrid_mode"`
		LocalModel   string `yaml:"local_model"` // Model for local provider (Ollama)
		Think        *bool  `yaml:"think"`       // Ollama think mode: false disables reasoning, nil = use model default

		// Sampling controls generation behavior (temperature, top_p, max_tokens).
		Sampling struct {
			Temperature float64 `yaml:"temperature"`
			TopP        float64 `yaml:"top_p"`
			MinP        float64 `yaml:"min_p"` // Ollama min_p — rejects low-probability tokens; 0 disables
			MaxTokens   int     `yaml:"max_tokens"`
		} `yaml:"sampling"`

		// Cloud provider config — used when hybrid_mode is true.
		// If Cloud.Provider is set, it overrides Provider for cloud routing.
		Cloud struct {
			Provider string `yaml:"provider"` // "openai", "gemini", "claude", "openrouter", "nvidia"
			Model    string `yaml:"model"`    // Cloud model name (e.g., "gpt-4o", "gemini-2.0-flash", "claude-sonnet-4-20250514")
			URL      string `yaml:"url"`      // API endpoint (provider-specific defaults if empty)
			APIKey   string `yaml:"api_key"`  // API key for cloud provider
		} `yaml:"cloud"`
	} `yaml:"llm"`
	Vision struct {
		Provider string `yaml:"provider"` // "ollama", "openai", "gemini"
		Enabled  bool   `yaml:"enabled"`
		Model    string `yaml:"model"`
		URL      string `yaml:"url"`
		APIKey   string `yaml:"api_key"`
	} `yaml:"vision"`
	Privacy   Privacy `yaml:"privacy"`
	SpeakerID struct {
		Enabled        bool    `yaml:"enabled"`
		ReferenceAudio string  `yaml:"reference_audio"` // WAV file to enroll as "user"
		Threshold      float64 `yaml:"threshold"`       // 0.5=lenient, 0.7=balanced, 0.85=strict
	} `yaml:"speaker_id"`
	Agentic struct {
		Enabled bool `yaml:"enabled"`
	} `yaml:"agentic"`
	MCP struct {
		Enabled bool     `yaml:"enabled"`
		Servers []string `yaml:"servers"`
	} `yaml:"mcp"`
	Server struct {
		Enabled             bool   `yaml:"enabled"`
		Port                int    `yaml:"port"`
		Token               string `yaml:"token"`
		RouteAudioToBrowser bool   `yaml:"route_audio_to_browser"`
	} `yaml:"server"`
}

type Privacy struct {
	DetectionEnabled bool     `yaml:"detection_enabled"`
	SensitiveWords   []string `yaml:"sensitive_words"`
	BlockCloud       bool     `yaml:"block_cloud_on_sensitivity"`
}
