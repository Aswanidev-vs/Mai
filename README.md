# Mai - Offline AI Assistant

[![Go Version](https://img.shields.io/badge/go-1.21-blue)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE.md)

> **Acknowledgment:** This project is heavily powered by the incredible [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) speech processing toolkit.

**Mai** is a fully offline, voice-driven AI assistant built in Go. Speak naturally, get intelligent responses, and maintain complete privacy — no cloud required.

> *"Not something you command, but something that understands, remembers, and acts quietly alongside you."*

---

## What Makes Mai Unique

| Feature | Mai | Typical Cloud Assistants |
|---------|-----|-------------------------|
| **Privacy** | 100% offline — your voice never leaves your machine | Audio sent to remote servers for processing |
| **Latency** | Sub-second response pipeline with local inference | Network-dependent, often 1-3s+ |
| **Cost** | Zero ongoing fees — run on your existing hardware | Subscription models or API metering |
| **Customizability** | Swap LLMs, TTS voices, and wake words freely | Locked to vendor's ecosystem |
| **Voice Cloning** | Built-in zero-shot cloning with 3-10s samples | Not available or requires expensive services |
| **Open Source** | Fully open — modify, audit, and extend | Black-box proprietary systems |

Unlike browser-based or cloud-dependent assistants, Mai's entire pipeline — wake word detection, speech recognition, reasoning, and speech synthesis — runs locally using optimized ONNX models.

---

## Quick Start

```bash
# 1. Copy configuration template
cp config.example.yaml config.yaml

# 2. Start Ollama (if not already running)
ollama serve

# 3. Build the assistant
go build -o mai.exe ./cmd/mai

# 4. Run it
./mai.exe
```

Say **"Mai"**, **"Hey Mai"** to wake the assistant. Speak your request naturally.

---

## Features

### ✅ Currently Implemented

| Feature | Status | Description |
|---------|--------|-------------|
| **Wake Word Detection** | ✅ Ready | Continuous listening for "Mai", "Hey Mai" using Zipformer KWS |
| **Voice Activity Detection** | ✅ Ready | Silero VAD automatically segments your speech |
| **Streaming ASR** | ✅ Ready | Real-time speech-to-text with NeMo CTC or Zipformer transducer models |
| **Local LLM Integration** | ✅ Ready | Ollama backend with auto-start support; llama.cpp configurable |
| **Multi-Model TTS** | ✅ Ready | Switch between Supertonic, Pocket, and ZipVoice synthesizers |
| **Follow-Up Queries** | ✅ Ready | 10-second conversation window without repeating the wake word |
| **Interruptible Playback** | ✅ Ready | Speak during TTS to interrupt and redirect |
| **YAML Configuration** | ✅ Ready | Single config file controls all speech and LLM components |
| **Audio I/O** | ✅ Ready | Cross-platform microphone capture and speaker playback via miniaudio |

### 🚧 Planned / Not Yet Implemented

| Feature | Status | Description |
|---------|--------|-------------|
| **Voice Cloning** | 🚧 Config only | TTS model configs prepared; not yet wired into the live pipeline |
| **Structured Action Parser** | 🚧 Planned | Parse commands into JSON actions (open apps, send messages, etc.) |
| **System Automation** | 🚧 Planned | UI automation via RoboGo (app control, typing, messaging) |
| **Memory System** | 🚧 Planned | SQLite short-term memory + Markdown long-term memory |
| **Vision / OCR** | 🚧 Planned | On-demand screen capture and text extraction |
| **Emotion Engine** | 🚧 Planned | Detect user tone and adapt response style |
| **Web Search** | 🚧 Planned | Optional opt-in web knowledge (breaks offline mode) |

> **Current behavior**: Transcribed speech is sent directly to the LLM as raw text. Structured command parsing and action execution are not yet active.


---

## Prerequisites

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | 1.21+ | [Download](https://golang.org/dl/) |
| Ollama | Latest | [Download](https://ollama.com) — for LLM backend |
| ONNX Runtime | Bundled | Included via `sherpa-onnx-go` |

### Optional
- **llama.cpp** — Alternative LLM backend if you prefer it over Ollama
- **Git LFS** — If cloning models from Hugging Face

---

## Setup Instructions

### 1. Clone the Repository

```bash
git clone <repository-url>
cd mai
```

### 2. Verify Models

All required ONNX models are included in the repository:

| Component | Model | Path |
|-----------|-------|------|
| Wake Word | Zipformer Gigaspeech 3.3M | `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01/` |
| VAD | Silero VAD | `silero_vad.onnx` |
| ASR | NeMo Streaming Fast Conformer | `sherpa-onnx-nemo-streaming-fast-conformer-ctc-en-480ms/` |
| TTS | Supertonic / Pocket / ZipVoice | `sherpa-onnx-supertonic-tts-int8-2026-03-06/` etc. |

> **Note**: If models are missing, download them from the [sherpa-onnx releases page](https://github.com/k2-fsa/sherpa-onnx/releases).

### 3. Configure

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` to match your preferences. Key sections:
- `audio`: Sample rate and buffer settings
- `kws`: Wake word sensitivity and cooldown
- `vad`: Speech detection thresholds
- `asr`: Model type and decoding method
- `tts`: Active voice model and speed
- `llm`: Provider, model name, and system prompt

### 4. Prepare LLM

Pull a recommended model via Ollama:

```bash
# Small, fast, capable (recommended for most hardware)
ollama pull gemma2:2b

# Or for higher quality with more RAM
ollama pull qwen2.5:3b

# Or for best multilingual support
ollama pull phi3:mini
```

### 5. Build & Run

```bash
go mod tidy
go build -o mai.exe ./cmd/mai
./mai.exe -config config.yaml
```

---

## Usage

### Wake Words
- **"Mai"** — Primary wake word
- **"Hey Mai"** — Alternative phrase

### Example Interactions

```text
You: "Mai, what's the weather like?"
Mai: "I don't have internet access by default, but I can help you with offline tasks."

You: "Open Chrome"
Mai: "Alright."

You: "Tell me a joke"
Mai: "Why did the Go programmer go broke? Because he used up all his cache!"
```

### Follow-Up Mode
After Mai responds, you have **10 seconds** to ask a follow-up without saying the wake word again.

### Keyboard Controls
- `Ctrl+C` — Graceful shutdown

---

## Configuration Reference

### Audio Settings
```yaml
audio:
  sample_rate: 16000        # Mic sample rate (16kHz required for speech models)
  capture_buffer_ms: 100    # Audio buffer size
  playback_device: ""       # "" = default output device
```

### Wake Word (KWS)
```yaml
kws:
  provider: "cpu"
  num_threads: 2
  model_dir: "./sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01"
  keywords: "▁MA I @mai, ▁MY @mai, ▁HE Y ▁MA I @mai"
  confidence_threshold: 0.02
  cooldown_ms: 1500         # Prevent re-triggering
```

### Voice Activity Detection (VAD)
```yaml
vad:
  provider: "cpu"
  num_threads: 2
  model: "./silero_vad.onnx"
  threshold: 0.6            # Speech detection threshold (0-1)
  min_silence_duration: 0.8 # Seconds of silence to end segment
  min_speech_duration: 0.5  # Minimum speech length
  max_speech_duration: 10.0 # Maximum speech length before forced split
```

### Speech Recognition (ASR)
```yaml
asr:
  type: "nemo"              # "nemo" or "zipformer"
  provider: "cpu"
  num_threads: 2
  model_dir: "./sherpa-onnx-nemo-streaming-fast-conformer-ctc-en-480ms"
  decoding_method: "greedy_search"
  enable_endpoint: 1        # Auto-detect end of utterance
```

### Text-to-Speech (TTS)
```yaml
tts:
  active_model: "supertonic"  # "supertonic" | "pocket" | "zipvoice"
  num_threads: 2
  output_sample_rate: 44100

  supertonic:
    model_dir: "./sherpa-onnx-supertonic-tts-int8-2026-03-06"
    speed: 1.25
    num_steps: 5

  voice_cloning:
    enabled: false
    model: "pocket"           # "pocket" or "zipvoice"
    reference_audio: "./mai_san_v2.wav"
```

### LLM
```yaml
llm:
  provider: "ollama"
  model: "gemma2:2b"
  url: "http://localhost:11434/api/generate"
  auto_start: true            # Start Ollama automatically
  system_prompt: "You are Mai, a helpful and concise offline AI assistant."
```

---

## TTS Models & Voice Cloning

### Model Comparison

| Model | Size | Speed | Quality | Best For |
|-------|------|-------|---------|----------|
| **Supertonic** | ~60 MB | Very Fast | Excellent | Default English female voice |
| **Pocket** | ~30 MB | Fastest | Good | Ultra-low latency, smallest footprint |
| **ZipVoice** | ~100 MB | Fast | Very Good | Expressive voice, cloning support |

### Switching Models
Change `tts.active_model` in `config.yaml`:
```yaml
tts:
  active_model: "supertonic"  # or "pocket" or "zipvoice"
```

### Voice Cloning Setup

1. Record or obtain a **3-10 second clean WAV** of the target voice (16kHz or 24kHz)
2. Place it in the project directory (e.g., `my_voice.wav`)
3. Enable cloning in config:

```yaml
tts:
  voice_cloning:
    enabled: true
    model: "pocket"                    # or "zipvoice"
    reference_audio: "./my_voice.wav"
```

4. Restart Mai. The assistant will now speak in the cloned voice.

> **Tip**: Lower `temperature` (0.5-0.6) creates a closer match to the reference voice. Higher values (0.8-0.9) add more expressiveness.

---

## LLM Setup

### Recommended Models

| Model | Size | RAM Needed | Best For |
|-------|------|------------|----------|
| `gemma2:2b` | ~1.6 GB | 4 GB | Fast responses, general tasks |
| `qwen2.5:3b` | ~2 GB | 4 GB | Multilingual support |
| `phi3:mini` | ~2.3 GB | 6 GB | Strong reasoning, coding help |

### Ollama (Default)
Mai automatically starts Ollama if `llm.auto_start: true`. Ensure your desired model is pulled:

```bash
ollama pull gemma2:2b
```

### Custom System Prompt
Edit `llm.system_prompt` in `config.yaml` to change Mai's personality:

```yaml
llm:
  system_prompt: "You are Mai, a witty and concise AI assistant. Keep answers under 2 sentences."
```

### llama.cpp (Alternative)
Set `llm.provider: "llamacpp"` and point `llm.url` to your local server:

```bash
# Start llama.cpp server
./llama-server -m model.gguf --port 8080
```

```yaml
llm:
  provider: "llamacpp"
  url: "http://localhost:8080/completion"
  auto_start: false
```

---

## Architecture

```
┌─────────────┐     ┌─────────┐     ┌──────────┐     ┌─────────┐
│  Microphone │────▶│   VAD   │────▶│   KWS    │────▶│   ASR   │
└─────────────┘     └─────────┘     └──────────┘     └────┬────┘
                                                          │
                              ┌───────────────────────────┘
                              ▼
                    ┌─────────────────┐
                    │   LLM (Ollama)  │
                    │  Reasoning +    │
                    │  Response Gen   │
                    └────────┬────────┘
                             │
                             ▼
                    ┌─────────────────┐
                    │      TTS        │
                    │  (Supertonic/   │
                    │  Pocket/ZipVoice)│
                    └────────┬────────┘
                             │
                             ▼
                    ┌─────────────────┐
                    │     Speaker     │
                    └─────────────────┘
```

### State Machine

```
IDLE (Wake Word Listen)
    │
    ├───"Mai" detected────▶ LISTENING (VAD + ASR active)
    │                           │
    │                     Speech ends
    │                           │
    │                           ▼
    │                    PROCESSING (LLM inference)
    │                           │
    │                     Response ready
    │                           │
    │                           ▼
    └──────────────────── SPEAKING (TTS playback)
                                │
                          Playback done ──▶ IDLE
```

---

## Performance

Mai is designed for efficient local inference on consumer hardware:

| Metric | Typical Target | Notes |
|--------|---------------|-------|
| **Wake Word Latency** | < 200ms | From speech end to detection |
| **ASR Real-Time Factor** | < 0.3x | Decoding faster than real-time |
| **TTS First Audio** | < 500ms | For short responses |
| **End-to-End Response** | < 2s | Wake word to spoken reply |
| **Idle CPU Usage** | < 5% | While listening for wake word |
| **Memory Footprint** | < 2 GB | Speech pipeline only (excluding LLM) |

> Performance scales with available CPU cores. ONNX Runtime CPU provider is used for all speech components, leaving GPU available for LLM acceleration if desired.

---

## Development

### Project Structure

```
cmd/mai/
├── main.go          # Application entry point and pipeline orchestration
└── audio.go         # Audio capture (malgo) and playback
config.example.yaml  # Configuration template
go.mod / go.sum      # Go module definitions
prd.md              # Product Requirements Document
todo.md             # Implementation roadmap
test/               # Test utilities and web UI for TTS testing
```

### Build Commands

```bash
# Standard build
go build -o mai.exe ./cmd/mai

# With optimizations
go build -ldflags="-s -w" -o mai.exe ./cmd/mai

# Run tests
go test ./...
```



Then open `http://localhost:8080` in your browser to test different TTS voices.

---

## Roadmap

| Phase | Feature | Status | Notes |
|-------|---------|--------|-------|
| 1 | Project Foundation | ✅ Complete | Go module, config system, audio I/O |
| 2 | Wake Word Detection | ✅ Complete | Zipformer KWS with cooldown and confidence thresholds |
| 3 | VAD Integration | ✅ Complete | Silero VAD with circular buffer |
| 4 | Streaming ASR | ✅ Complete | NeMo CTC + Zipformer support, endpoint detection |
| 5 | TTS Integration | ✅ Complete | Supertonic / Pocket / ZipVoice model support |
| 6 | Voice Pipeline Orchestration | ✅ Complete | State machine, follow-up mode, interruptible playback |
| 7 | LLM Integration | ✅ Complete | Ollama client with auto-start; raw text prompts |
| 7b | Command Parser & Action System | 🚧 Planned | Structured JSON action parsing from LLM output |
| 8 | Automation (RoboGo) | 🚧 Planned | Open apps, type text, send messages |
| 9 | Memory System (SQLite + Markdown) | 🚧 Planned | Session history, user profile, persistent notes |
| 10 | Vision (Screen OCR) | 🚧 Planned | On-demand screen capture + Tesseract OCR |
| 11 | Emotion Engine | 🚧 Planned | Tone detection, adaptive TTS speed/pitch |
| 12 | Web Search (Opt-in) | 🚧 Planned | DuckDuckGo/SearXNG integration; disabled by default |
| 13 | Polish & Performance Tuning | 🚧 Planned | Metrics, logging levels, build scripts |
| 14 | Multi-step Task Planning | 🔮 Future | Complex multi-action sequences |

See [`todo.md`](todo.md) for detailed implementation tasks.


---

## Troubleshooting

### "Failed to create keyword spotter"
- Verify model paths in `config.yaml` match actual directories
- Ensure ONNX model files are not corrupted (check file sizes)

### No audio output
- Check `audio.playback_device` in config (leave empty for default)
- Verify Windows audio output is not muted
- Test with `test/` TTS server to isolate issues

### Ollama connection refused
- Ensure Ollama is running: `ollama serve`
- Check `llm.url` matches Ollama's actual port (default: 11434)
- Try disabling `auto_start` and manually starting Ollama

### High CPU usage
- Reduce `num_threads` in KWS, VAD, and TTS configs
- Use a smaller LLM model (e.g., `gemma2:2b` instead of 7B models)
- Ensure `provider: "cpu"` is set for speech models (GPU not needed for these)

### TTS sounds distorted
- Verify `output_sample_rate` matches your model's native rate:
  - Supertonic: 44100 Hz
  - Pocket: 24000 Hz
  - ZipVoice: 24000 Hz

---

## Contributing

We welcome contributions! Please see [`CONTRIBUTING.md`](CONTRIBUTING.md) for guidelines.

## License

This project is licensed under the MIT License — see [`LICENSE.md`](LICENSE.md) for details.

---

## Acknowledgments

- [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) — Next-gen speech processing toolkit
- [k2-fsa/sherpa-onnx-go](https://github.com/k2-fsa/sherpa-onnx-go) — Go bindings for sherpa-onnx
- [gen2brain/malgo](https://github.com/gen2brain/malgo) — Go bindings for miniaudio
- [Ollama](https://ollama.com) — Local LLM serving
- [Supertone](https://supertone.ai) — Supertonic TTS model
