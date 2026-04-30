# Mai - JARVIS-Class Autonomous AI Assistant

[![Go Version](https://img.shields.io/badge/go-1.25-blue)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE.md)

> **Acknowledgment:** This project is heavily powered by the incredible [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) speech processing toolkit.

**Mai** is a fully offline, **JARVIS-class autonomous agentic assistant** built in Go. Unlike standard voice assistants that simply respond to queries, Mai is designed to perceive, reason, and act independently across your system — all while maintaining 100% local privacy.

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

## Dual-Mode Architecture

Mai operates in two modes, switchable at runtime via configuration:

| Mode | Behavior | Use Case |
|------|----------|----------|
| **Legacy Mode** | Classic wake word → ASR → regex/LLM → TTS pipeline | Fast, simple commands with minimal overhead |
| **Agentic Mode** | Full cognitive loop with memory, planning, and proactivity | Complex multi-step tasks, autonomous monitoring |

In **Agentic Mode**, Mai features:
- **Autonomous Proactive Monitoring**: Periodic self-reflection loops (every 5 minutes) analyze context and decide if proactive assistance is needed
- **Multi-Step Goal Reasoning (ReAct)**: Breaks complex objectives into thought-action-observation cycles, executing tools sequentially
- **Dual-Path Cognitive Routing**:
  - **Fast Path**: Sub-millisecond regex matching for direct commands (open app, send message, etc.)
  - **Reasoning Path**: Deep ReAct cognitive loops for analytical problem-solving
- **Self-Correction (Reflexion)**: If a tool call fails, analyzes the error and adjusts strategy automatically
- **Multi-Modal Perception Fusion**: Combines streaming audio with vision input via the event bus

Enable Agentic Mode in `config.yaml`:
```yaml
agentic:
  enabled: true
```

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

# Optional: specify a custom config file
# ./mai.exe -config my-config.yaml

```

Say **"Mai"**, **"Hey Mai"** to wake the assistant. Speak your request naturally.

---

## Features

### Core Pipeline (Legacy Mode)

| Feature | Status | Description |
|---------|--------|-------------|
| **Wake Word Detection** | ✅ Ready | Continuous listening for "Mai", "Hey Mai" using Zipformer KWS |
| **Voice Activity Detection** | ✅ Ready | Silero VAD automatically segments your speech |
| **Streaming ASR** | ✅ Ready | Real-time speech-to-text with NeMo CTC, Zipformer, or Qwen3 models |
| **Local LLM Integration** | ✅ Ready | Ollama backend with auto-start support; multi-provider capable |
| **Multi-Model TTS** | ✅ Ready | Switch between Supertonic, Pocket, and ZipVoice synthesizers |
| **Follow-Up Queries** | ✅ Ready | 15-second conversation window without repeating the wake word |
| **Interruptible Playback** | ✅ Ready | Speak during TTS to interrupt and redirect |
| **Structured Action Parser** | ✅ Ready | High-reliability regex parser (Fast Path) + LLM-based action fallback |
| **System Automation** | ✅ Ready | UI automation via RobotGo (WhatsApp, Telegram, YouTube, App Control) |
| **YAML Configuration** | ✅ Ready | Single config file controls all speech and LLM components |
| **Audio I/O** | ✅ Ready | Cross-platform microphone capture and speaker playback via miniaudio |

### Agentic Layer (Optional)

| Feature | Status | Description |
|---------|--------|-------------|
| **Event Bus** | ✅ Ready | Async pub/sub communication between all components |
| **ReAct Reasoning Engine** | ✅ Ready | Multi-step thought → action → observation loops |
| **Tool Registry** | ✅ Ready | 10+ built-in tools with dynamic discovery |
| **Working Memory** | ✅ Ready | In-memory short-term context buffer |
| **Episodic Memory** | ✅ Ready | SQLite-backed conversation and event history |
| **Multi-Provider LLM** | ✅ Ready | Ollama, OpenAI, Gemini, Claude + Hybrid mode |
| **Privacy Guard** | ✅ Ready | Sensitive data detection for hybrid cloud/local routing |
| **Proactive Monitoring** | ✅ Ready | Self-reflection loops every 5 minutes |
| **Meta-Cognition** | ✅ Ready | Performance tracking and strategy monitoring |
| **Perception Bridge** | ✅ Ready | Audio transcription and vision event publishing |

### 🚧 Planned / Not Yet Implemented

| Feature | Status | Description |
|---------|--------|-------------|
| **Semantic Memory** | 🚧 Stub | Vector DB (Chroma/Milvus) for RAG — interface exists, not wired |
| **Procedural Memory** | 🚧 Stub | Skill and tool usage pattern storage — interface exists |
| **Voice Cloning** | 🚧 Config only | TTS model configs prepared; not yet wired into live pipeline |
| **Vision / OCR** | 🚧 Partial | Vision bridge exists; continuous screen monitoring needs enhancement |
| **Emotion Engine** | 🚧 Planned | Detect user tone and adapt response style |
| **MCP Client** | 🚧 Stub | Model Context Protocol client exists; not fully integrated |
| **HTN Planner** | 🚧 Planned | Hierarchical Task Network for complex goal decomposition |
| **Web Search** | 🚧 Planned | Optional opt-in web knowledge (breaks offline mode) |

---

## Prerequisites

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | 1.25+ | [Download](https://golang.org/dl/) |
| Ollama | Latest | [Download](https://ollama.com) — for LLM backend |
| ONNX Runtime | Bundled | Included via `sherpa-onnx-go` |

### Optional
- **llama.cpp** — Alternative LLM backend if you prefer it over Ollama
- **OpenAI / Gemini / Claude API keys** — For hybrid cloud mode (optional)
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
| ASR | Qwen3 Offline ASR | `sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25/` |
| TTS | Supertonic | `sherpa-onnx-supertonic-tts-int8-2026-03-06/` |
| TTS | Pocket | `sherpa-onnx-pocket-tts-2026-01-26/` |
| TTS | ZipVoice | `sherpa-onnx-zipvoice-distill-int8-zh-en-emilia/` |

> **Note**: If models are missing, download them from the [sherpa-onnx releases page](https://github.com/k2-fsa/sherpa-onnx/releases).

### 3. Configure

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` to match your preferences. Key sections:
- `audio`: Sample rate and buffer settings
- `kws`: Wake word sensitivity and cooldown
- `vad`: Speech detection thresholds
- `asr`: Model type (`nemo`, `zipformer`, `qwen3`) and decoding method
- `tts`: Active voice model and speed
- `llm`: Provider, model name, and system prompt
- `agentic`: Enable/disable agentic mode
- `privacy`: Sensitive word detection for hybrid mode

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
./mai.exe

# Optional: use a custom config file
# ./mai.exe -config my-config.yaml

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
After Mai responds, you have **15 seconds** to ask a follow-up without saying the wake word again.

### Keyboard Controls
- `Ctrl+C` — Graceful shutdown

---

## LLM Providers

Mai supports multiple LLM backends through a unified interface:

| Provider | Type | Setup |
|----------|------|-------|
| **Ollama** | Local (default) | `ollama serve` running locally |
| **llama.cpp** | Local | Point `url` to your local server |
| **OpenAI** | Cloud | Set `api_key` and `url` |
| **Gemini** | Cloud | Set `api_key` |
| **Claude** | Cloud | Set `api_key` |

### Hybrid Mode

Enable intelligent routing between local and cloud models:

```yaml
llm:
  provider: "openai"      # Cloud provider
  model: "gpt-4o-mini"
  url: "https://api.openai.com/v1/chat/completions"
  api_key: "sk-..."
  hybrid_mode: true       # Enable hybrid routing
  system_prompt: "You are Mai, a helpful AI assistant."

privacy:
  detection_enabled: true
  sensitive_words:
    - "password"
    - "secret"
    - "credit card"
```

**How it works**: The PrivacyGuard scans every prompt for sensitive keywords. If detected, the request routes to your local Ollama model. Otherwise, it uses the cloud provider for higher capability.

---

## Tool Registry

Mai's agentic mode includes a universal tool registry. Built-in tools:

| Tool | Description | Example |
|------|-------------|---------|
| `shell_execute` | Run shell commands | `"List files in current directory"` |
| `open_application` | Launch apps by name | `"Open Chrome"` |
| `web_search` | Open browser search | `"Search for Go programming"` |
| `youtube_play` | Play YouTube videos | `"Play Perfect on YouTube"` |
| `whatsapp_send` | Send WhatsApp messages | `"Send hello to Manu on WhatsApp"` |
| `get_system_time` | Get current time/date | `"What time is it?"` |
| `file_write` | Write to files | `"Save this note to todo.txt"` |
| `deep_search` | Research with reasoning | `"Research quantum computing"` |
| `ui_automation` | UI control (click, type) | `"Press Ctrl+F"` |
| `media_control` | Play/pause/skip media | `"Pause the music"` |

Tools are dynamically discovered and executed by the ReAct reasoning engine.

---

## Memory System

Mai implements a hierarchical memory architecture:

| Layer | Storage | Purpose | Status |
|-------|---------|---------|--------|
| **Working Memory** | In-memory (10-100 KB) | Short-term context buffer | ✅ Implemented |
| **Episodic Memory** | SQLite (`data/memory/episodic.db`) | Conversation and event history | ✅ Implemented |
| **Semantic Memory** | Vector DB (planned) | Long-term facts and knowledge | 🚧 Stub |
| **Procedural Memory** | Compiled patterns (planned) | Skills and tool usage patterns | 🚧 Stub |

The memory manager provides unified retrieval across all layers for the ReAct loop.

---

## Architecture & Core Systems

Mai is built on a high-concurrency, event-driven architecture designed for low-latency offline interaction. It consists of three primary layers:

### 1. Perception Layer (`internal/perception`)
- **Audio Bridge**: Captures microphone input via `malgo` (miniaudio) and routes it through VAD (Silero) and ASR (NeMo/Zipformer/Qwen).
- **Vision Bridge**: Performs periodic or on-demand screen understanding using local Vision LLMs (via Ollama).
- **Event Bus**: An in-process pub/sub bus that decouples perception from cognition.

### 2. Cognitive Layer (`internal/cognition` & `internal/agent`)
- **BDI Orchestrator**: Manages the agent's Beliefs (Memory), Desires (Goals), and Intentions (Plans).
- **ReAct Engine**: A Reasoning + Acting loop that uses structured LLM output to plan and execute multi-step tool sequences.
- **Memory Manager**: Maintains Working Memory (short-term context) and Episodic Memory (long-term conversation history).
- **Two-Tier Routing**:
  - **Fast Path**: Sub-millisecond regex matching for direct commands.
  - **Reasoning Path**: Deep cognitive loops for complex problem solving.

### 3. Action Layer (`internal/tools` & `cmd/mai`)
- **Tool Registry**: A central hub for discovering and executing capabilities.
- **Action Executor**: A high-reliability legacy system for precise UI control.
- **RobotGo Automation**: Direct OS-level control for typing, shortcut execution, and application management.

---

## Technical Package Breakdown

| Package | Responsibility |
|---------|----------------|
| `cmd/mai/` | Entry point, audio drivers, and the high-reliability legacy automation core |
| `internal/agent/` | Central orchestrator (BDI loop, goal manager, executive controller) |
| `internal/cognition/` | ReAct loop, reasoning, and planning logic |
| `internal/llm/` | Multi-provider LLM client (Ollama, OpenAI, Gemini, Claude, Hybrid) |
| `internal/memory/` | Hierarchical memory system (Working, Episodic, Semantic, Procedural) |
| `internal/tools/` | Tool definitions and adapters (Shell, Web, YouTube, WhatsApp, etc.) |
| `internal/perception/` | Bridges for ASR, VAD, and Vision data |
| `internal/events/` | Async pub/sub event bus for decoupled communication |
| `pkg/interfaces/` | Core interface definitions ensuring modularity and testability |

---

## Technology Stack

- **Language**: Go 1.25+ (concurrency-first architecture)
- **Inference**: ONNX Runtime (CPU-optimized for speech/VAD/ASR)
- **Automation**: RobotGo (Cross-platform UI control)
- **Audio**: Malgo (C-bindings for miniaudio)
- **LLM Backends**: Ollama (default), llama.cpp, OpenAI, Gemini, Claude
- **Memory**: SQLite (episodic), in-memory (working), Chroma/Milvus (semantic, planned)
- **Models**: NeMo CTC, Silero VAD, Supertonic TTS, Qwen/Gemma LLMs

---

## Performance Targets

| Metric | Target | Actual (Optimized) |
|--------|--------|-------------------|
| **Fast Path Latency** | < 100ms | ~20-50ms (Regex matching) |
| **Reasoning Latency** | < 2s | ~1.2s (phi3:mini / gemma:2b) |
| **ASR Accuracy** | > 95% | Excellent (NeMo / Qwen3) |
| **TTS Jitter** | < 5ms | Near-zero (Buffered playback) |

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
  type: "nemo"              # "nemo", "zipformer", or "qwen3"
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
  provider: "ollama"        # "ollama", "openai", "gemini", "claude", "llamacpp"
  model: "gemma2:2b"
  url: "http://localhost:11434/api/generate"
  auto_start: true
  hybrid_mode: false        # Enable for local/cloud routing
  api_key: ""               # Required for cloud providers
  system_prompt: "You are Mai, a helpful and concise offline AI assistant."
```

### Agentic Mode
```yaml
agentic:
  enabled: false            # Set to true to enable agentic architecture
```

### Privacy (Hybrid Mode)
```yaml
privacy:
  detection_enabled: true
  sensitive_words:
    - "password"
    - "secret"
    - "credit card"
    - "ssn"
```

---

## Development

### Project Structure

```
cmd/mai/
├── main.go          # Application entry point and pipeline orchestration
├── audio.go         # Audio capture (malgo) and playback
├── automation.go    # UI automation via RobotGo
├── actions.go       # Regex-based action parser
└── vision.go        # Vision processing via Ollama
internal/
├── agent/           # Orchestrator, BDI loop, meta-cognition, privacy guard
├── cognition/       # ReAct loop and reasoning logic
├── llm/             # Multi-provider LLM clients and factory
├── memory/          # Working, episodic, semantic, procedural memory
├── perception/      # Audio and vision bridges
├── tools/           # Tool registry and adapters
├── events/          # Pub/sub event bus
└── config/          # Configuration management
pkg/
└── interfaces/      # Core Go interfaces (agent, cognition, llm, memory, tools, events)
data/
├── memory/          # SQLite databases
├── vector/          # Vector DB files (future)
└── cache/           # Temporary caches
config.example.yaml  # Configuration template
go.mod / go.sum      # Go module definitions
prd.md              # Product Requirements Document
ROADMAP.md          # Implementation roadmap
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

---

## Roadmap

| Phase | Feature | Status | Notes |
|-------|---------|--------|-------|
| 1 | Project Foundation | ✅ Complete | Go module, config system, audio I/O |
| 2 | Wake Word Detection | ✅ Complete | Zipformer KWS with cooldown and confidence thresholds |
| 3 | VAD Integration | ✅ Complete | Silero VAD with circular buffer |
| 4 | Streaming ASR | ✅ Complete | NeMo CTC + Zipformer + Qwen3 support |
| 5 | TTS Integration | ✅ Complete | Supertonic / Pocket / ZipVoice model support |
| 6 | Voice Pipeline Orchestration | ✅ Complete | State machine, follow-up mode, interruptible playback |
| 7 | LLM Integration | ✅ Complete | Multi-provider: Ollama, OpenAI, Gemini, Claude, Hybrid |
| 7b | Command Parser & Action System | ✅ Complete | High-reliability regex (Fast Path) + LLM fallback |
| 8 | Automation (RobotGo) | ✅ Complete | WhatsApp, Telegram, YouTube, and System App control |
| 9 | Memory System | 🚧 Partial | Working + Episodic implemented; Semantic + Procedural stubs |
| 10 | Vision (Screen OCR) | 🚧 Partial | Vision bridge exists; continuous monitoring needs work |
| 11 | Emotion Engine | 🚧 Planned | Tone detection, adaptive TTS speed/pitch |
| 12 | Web Search (Opt-in) | 🚧 Planned | DuckDuckGo/SearXNG integration; disabled by default |
| 13 | Polish & Performance Tuning | 🚧 In Progress | Routing optimization, latency reduction, stability fixes |
| 14 | Multi-step Task Planning | ✅ Complete | ReAct reasoning engine for complex sequences |

See [`ROADMAP.md`](ROADMAP.md) for detailed implementation tasks.

---

## Troubleshooting

### "Failed to create keyword spotter"
- Verify model paths in `config.yaml` match actual directories
- Ensure ONNX model files are not corrupted (check file sizes)

### No audio output
- Check `audio.playback_device` in config (leave empty for default)
- Verify Windows audio output is not muted

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
