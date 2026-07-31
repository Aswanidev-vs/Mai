# Mai - JARVIS-Class Autonomous AI Assistant

[![Go Version](https://img.shields.io/badge/go-1.25-blue)](https://golang.org)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE.md)

> **Acknowledgment:** This project is heavily powered by the incredible [sherpa-onnx](https://github.com/k2-fsa/sherpa-onnx) speech processing toolkit.

**Mai** is a fully offline, **JARVIS-class autonomous agentic assistant** built in Go with a real-time 3D companion web UI. Unlike standard voice assistants that simply respond to queries, Mai is designed to perceive, reason, and act independently across your system — all while maintaining 100% local privacy and presenting a living, breathing 3D character companion.

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
| **Emotional Intelligence** | Prosody-aware STT + emotion-adaptive TTS | No emotional awareness |
| **Proactive Intelligence** | Pattern learning, anticipatory actions, idle reminders | Purely reactive |
| **User Modeling** | Learns preferences, habits, frequent apps, topics | Generic interactions |
| **Function Calling** | Structured JSON tool invocation from LLM | Raw text parsing only |
| **3D Companion** | Real-time VRM character with alive-ness system | Static text UI |
| **Open Source** | Fully open — modify, audit, and extend | Black-box proprietary systems |

---

## Companion Mode (Web UI)

Mai includes a browser-based companion interface with a real-time 3D character, voice interaction, and streaming responses.

### Features
- **VRM 3D Character** — Real-time rendered with Three.js + @pixiv/three-vrm
- **Alive-ness System** — CPT eye saccades, spring-damper head physics, auto-blink, mouse tracking, breathing, idle behaviors, micro-expressions, spontaneous smiling
- **Streaming TTS with Lip-Sync** — Audio chunks streamed to browser with word-level viseme scheduling and winner+runner mouth blending
- **Voice Input (Mic)** — Browser Web Audio API captures speech → PCM 16kHz → WebSocket → Sherpa-ONNX VAD/ASR pipeline
- **WebGPU/WebGL Rendering** — Automatic backend selection with fallback
- **PixiJS 2D Background** — Cozy warm-toned background with floating dust particles
- **Motion3 Animation** — VRoid .motion3.json parameter animations for idle movement
- **Emotion Display** — Real-time emotion badge showing current character state
- **WebSocket Real-time** — Bi-directional communication for instant responses

### Quick Start (Companion Mode)

```bash
# 1. Build with companion support
go build -o mai.exe ./cmd/mai

# 2. Run with companion flag
./mai.exe --companion

# 3. Open browser
# Navigate to http://localhost:8080
```

### Architecture

```
Browser (Three.js + Web Audio) ←→ WebSocket ←→ Go Server
    ↓                                      ↓
VRM Character Renderer              LLM / TTS / ASR
Motion3 Animations                  Sherpa-ONNX Pipeline
Live Lip-Sync                       Emotion Detection
Voice Input (Mic)                   Streaming Audio Chunks
```

### Voice Interaction Flow

```
1. User clicks mic → Web Audio API captures PCM 16kHz
2. PCM chunks sent via WebSocket (audio.input frames)
3. Server: Sherpa-ONNX VAD detects speech segments
4. Server: ASR transcribes speech → text
5. Server: LLM generates response → streaming text tokens
6. Server: TTS synthesizes audio → streaming chunks
7. Browser: Receives audio chunks → sequential playback
8. Browser: Viseme schedule drives mouth animation
9. Browser: RMS energy drives real-time mouth amplitude
```

---

## Dual-Mode Architecture

Mai operates in two modes, switchable at runtime via configuration:

| Mode | Behavior | Use Case |
|------|----------|----------|
| **Legacy Mode** | Classic wake word → ASR → regex/LLM → TTS pipeline | Fast, simple commands with minimal overhead |
| **Agentic Mode** | Full cognitive loop with memory, planning, and proactivity | Complex multi-step tasks, autonomous monitoring |
| **Companion Mode** | Web-based 3D character with voice interaction | Real-time conversational companion |

In **Agentic Mode**, Mai features:
- **Unified Prompt Engine**: Single prompt template with JARVIS personality — consistent across all task types
- **Natural Language ReAct**: Thinks through problems naturally, not in rigid JSON steps
- **Simplified Routing**: Regex fast path → LLM handles everything else (2 paths, not 5)
- **Emotion-Aware Pipeline**: Prosody analysis from audio → emotion detection → adapted TTS speed/pitch/volume
- **Proactive Intelligence**: Pattern learning (time-of-day, frequency), anticipatory suggestions, idle reminders
- **User Modeling**: Learns preferences, tracks habits, extracts topics, persists to `data/user_profile.json`
- **Interrupt Hierarchy**: 4-level priority system (critical > high > normal > low) with queue management
- **Hierarchical Memory**: Working (ring buffer) + Episodic (SQLite) + Semantic (vector search with lazy loading) + Procedural (skill patterns) + RAG pipeline
- **GPU Offloading**: Configurable CPU/CUDA/CoreML/OpenCL for KWS, VAD, ASR, TTS

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

# Optional: run with companion web UI
./mai.exe --companion

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
| **GPU Offloading** | ✅ Ready | CUDA/CoreML/OpenCL support for KWS, VAD, ASR, TTS |

### Companion UI Features

| Feature | Status | Description |
|---------|--------|-------------|
| **VRM 3D Character** | ✅ Ready | Real-time rendered with Three.js + @pixiv/three-vrm |
| **Streaming TTS Lip-Sync** | ✅ Ready | Word-level viseme scheduling + real-time RMS amplitude |
| **Voice Input (Mic)** | ✅ Ready | Web Audio API → PCM 16kHz → WebSocket → Sherpa-ONNX ASR |
| **Eye Saccades (CPT)** | ✅ Ready | Probability-weighted natural eye movement (800-4400ms intervals) |
| **Spring-Damper Head** | ✅ Ready | Stiffness=120, Damping=16 physics for mouse-tracking head movement |
| **Auto-Blink** | ✅ Ready | State machine: idle→closing(75ms)→opening(150-300ms), 3-8s delay |
| **Mouse Eye Tracking** | ✅ Ready | Raycast screen→3D plane projection for gaze following |
| **Breathing Animation** | ✅ Ready | Multi-layered sine waves (0.75Hz base rate) |
| **Idle Behaviors** | ✅ Ready | Stretch, glance, adjust, breatheDeep, headNod, shoulderShrug |
| **Micro-Expressions** | ✅ Ready | Subtle expression flickers every 3-8 seconds |
| **Spontaneous Smiling** | ✅ Ready | 15% chance every 8-20 seconds, sine curve fade |
| **Emotion Postures** | ✅ Ready | Head tilt varies by emotion (happy/sad/surprised/think) |
| **Motion3 Animations** | ✅ Ready | VRoid .motion3.json parameter animation playback (8 clips) |
| **WebGPU Rendering** | ✅ Ready | Automatic WebGPU→WebGL fallback |
| **PixiJS Background** | ✅ Ready | Cozy warm-toned 2D background with floating dust particles |
| **Emotion Display** | ✅ Ready | Real-time emotion badge showing current character state |
| **Voice Activity Gate** | ✅ Ready | Lip-sync pauses during silence, smooth release on speech end |

### Agentic Layer — Reasoning & Cognition

| Feature | Status | Description |
|---------|--------|-------------|
| **Unified Prompt Engine** | ✅ Ready | Single prompt template with JARVIS personality — consistent across all task types |
| **Natural Language ReAct** | ✅ Ready | Thinks through problems naturally, not rigid JSON steps. Max 3 tool calls per request. |
| **Smart Routing** | ✅ Ready | Regex fast path → LLM handles everything else (2 paths, not 5) |
| **Fact Verifier** | ✅ Ready | Claim verification and tool call result validation |

### Agentic Layer — Memory & Knowledge

| Feature | Status | Description |
|---------|--------|-------------|
| **Working Memory** | ✅ Ready | Lock-free ring buffer with O(1) add/get operations |
| **Episodic Memory** | ✅ Ready | SQLite-backed conversation and event history |
| **Semantic Memory** | ✅ Ready | Lazy-loaded vector store with JSONL append-only persistence and approximate search for large sets |
| **Procedural Memory** | ✅ Ready | Skill and tool usage pattern storage with success/failure tracking |
| **RAG Pipeline** | ✅ Ready | Semantic + episodic retrieval → LLM answer generation with confidence scoring |
| **Session Continuity** | ✅ Ready | Non-blocking session restore on startup |

### Agentic Layer — Emotional Intelligence

| Feature | Status | Description |
|---------|--------|-------------|
| **Text Emotion Detection** | ✅ Ready | Keyword-based emotion scoring (happy, sad, stressed, excited, frustrated, calm) |
| **Prosody Analyzer** | ✅ Ready | Audio feature extraction: RMS energy, zero-crossing rate, spectral centroid, pitch, volume variance, pause ratio |
| **Emotion-Aware TTS** | ✅ Ready | Adapts speed, pitch, volume, emphasis, and pause scale per detected emotion |
| **Response Adaptation** | ✅ Ready | Shortens responses for stressed users, prefixes empathy for frustrated users |
| **Character Emotion Display** | ✅ Ready | Real-time VRM expression changes based on detected emotion |

### Agentic Layer — Proactive Intelligence

| Feature | Status | Description |
|---------|--------|-------------|
| **Pattern Learning** | ✅ Ready | Tracks time-of-day and frequency patterns in user actions |
| **Predictive Actions** | ✅ Ready | Suggests actions based on learned patterns and current context |
| **Idle Reminders** | ✅ Ready | Notifies user of pending goals after 15+ minutes of silence |
| **Performance Monitoring** | ✅ Ready | Tracks action success rate, warns if below 50% |
| **Self-Improvement Loop** | ✅ Ready | Analyzes strategy performance every 10 minutes, adjusts approach |

### Agentic Layer — User Modeling

| Feature | Status | Description |
|---------|--------|-------------|
| **User Profile** | ✅ Ready | Persists name, preferences, frequent apps, topics to `data/user_profile.json` |
| **Preference Learning** | ✅ Ready | Extracts and remembers user preferences from conversations |
| **Habit Tracking** | ✅ Ready | Records interaction patterns by time of day and action type |
| **Topic Extraction** | ✅ Ready | Identifies user interests (music, coding, work, food, etc.) from conversation |
| **Context Injection** | ✅ Ready | User profile context injected into prompts for personalized responses |

### Agentic Layer — Infrastructure

| Feature | Status | Description |
|---------|--------|-------------|
| **Event Bus** | ✅ Ready | Snapshot-based pub/sub with zero-allocation dispatch (~18ns/publish) |
| **Tool Registry** | ✅ Ready | Dynamic tool discovery with categories, semantic search, runtime registration |
| **Interrupt Hierarchy** | ✅ Ready | 4-level priority system (critical > high > normal > low) with queue management |
| **Multi-Provider LLM** | ✅ Ready | Ollama, OpenAI, Gemini, Claude, OpenRouter, NVIDIA + Hybrid mode |
| **Privacy Guard** | ✅ Ready | Sensitive data detection for hybrid cloud/local routing |
| **Perception Bridge** | ✅ Ready | Audio transcription and vision event publishing |
| **Meta-Cognition** | ✅ Ready | Performance tracking, strategy analysis, and self-improvement |
| **MCP Client** | ✅ Ready | Model Context Protocol for external tool discovery |
| **Companion Skills** | ✅ Ready | Skill routing via `data/skills.json` (trigger phrases → skill execution) |

---

## GPU Offloading

Mai supports GPU acceleration for speech processing components. Configure in `config.yaml`:

```yaml
kws:
  provider: "cuda"   # "cpu" | "cuda" (NVIDIA) | "coreml" (macOS) | "opencl"

vad:
  provider: "cuda"

asr:
  provider: "cuda"

tts:
  provider: "cuda"
```

**Requirements:**
- NVIDIA GPU with CUDA support for `"cuda"`
- sherpa-onnx built with the corresponding backend

**Performance impact:**
- ASR: 2-5x faster transcription
- TTS: 3-10x faster synthesis
- VAD/KWS: Marginal improvement (already fast on CPU)

---

## Performance

### Benchmark Results (AMD Ryzen 5 5600H)

| Component | Latency | Allocs | Notes |
|-----------|---------|--------|-------|
| Event bus publish | **18 ns** | 0 | Zero allocation |
| Event bus (10 handlers) | **128 ns** | 0 | Zero allocation |
| Echo detection | **310 ns** | 2 | Stack-allocated set |
| Working memory add | **13 ns** | 0 | Ring buffer |
| Working memory get | **8 ns** | 0 | Direct index |
| Task classification | **434 ns** | 2 | String matching |
| Prompt construction | **1.3 μs** | 10 | String builder |
| Cosine similarity (8-d) | **12 ns** | 0 | Math only |
| Cosine similarity (384-d) | **323 ns** | 0 | Math only |
| Semantic search (100 vectors) | **1.9 μs** | 9 | Brute force |
| Semantic search (1000 vectors) | **18 μs** | 22 | Approximate search |

### End-to-End Latency Breakdown

```
User stops speaking
    ↓
ASR transcription:     50-500ms  (ONNX — hardware-bound)
    ↓
Echo detection:        310ns     (stack-allocated)
Emotion detection:     ~200ns    (keyword match)
Task classification:   434ns     (string match)
Memory storage:        13ns      (ring buffer)
Prompt construction:   1.3μs     (string builder)
    ↓
LLM inference:         500ms+    (Ollama — network + model)
```

**Total Go pipeline overhead (excluding ML): ~2.3μs**

---

## Emotion-Adaptive Pipeline

Mai detects user emotion from both text and audio prosody, then adapts its voice, responses, and character expression:

```
User speaks → Audio samples → Prosody Analyzer → Emotion Detection
                                     ↓
Text transcript → Text Emotion Detection → Combined Emotion State
                                     ↓
                    ┌────────────────┼────────────────┐
                    ↓                ↓                ↓
            Prompt Engine      TTS Adapter      Character
         (tone directives)  (speed/pitch/vol)  (VRM expression)
```

**TTS Adaptation by Emotion:**

| Emotion | Speed | Pitch | Volume | Pauses | Character Expression |
|---------|-------|-------|--------|--------|---------------------|
| **Stressed** | -15% | -5% | -10% | +30% | Head droop, furrowed brows |
| **Frustrated** | -10% | -2% | -5% | +20% | Head tilt, tight mouth |
| **Sad** | -20% | -8% | -15% | +50% | Head droop, sad eyes |
| **Excited** | +15% | +5% | +10% | -20% | Head lift, wide eyes, smile |
| **Happy** | +5% | +3% | +5% | -10% | Head tilt right, smile, squint |
| **Calm** | -5% | -2% | -5% | +10% | Neutral posture, relaxed |

---

## Character Alive-ness System

The VRM character uses AIRI-inspired techniques to feel alive:

### Eye Movement (CPT-Distributed Saccades)
- Probability-weighted intervals: 7.5% at 800ms, 11% at 1200ms, ... up to 4400ms
- Feels natural — short saccades more frequent than long ones
- Mouse tracking via screen→3D plane raycasting

### Auto-Blink (State Machine)
- Three phases: idle → closing (75ms ease-out) → opening (150-300ms ease-in)
- 3-8 second delay between blinks
- Skips when eyes already near-closed (threshold 0.15)
- Double-blink chance: 22%

### Spring-Damper Head Physics
- Stiffness=120, Damping=16, Mass=1 (matching AIRI)
- Semi-implicit Euler integration with snap-to-target
- Applied to yaw/pitch/roll for mouse tracking

### Lip Sync (Winner+Runner Blending)
- Top 2 visemes blended (not winner-take-all)
- Mouth-integrated emotions (happy→aa, angry→ee, etc.)
- Smoothstep release (200ms crossfade) when speech ends

### Breathing & Idle
- Multi-layered sine waves at 0.75Hz base rate
- Idle behaviors: stretch, glance, adjust, breatheDeep, headNod, shoulderShrug
- Micro-expressions: subtle flickers every 3-8 seconds
- Spontaneous smiling: 15% chance every 8-20 seconds

---

## Interrupt Hierarchy

Mai uses a priority-based interrupt system to handle concurrent requests:

| Level | Examples | Behavior |
|-------|----------|----------|
| **CRITICAL** | "emergency", "help me", "danger" | Always interrupts — stops current task immediately |
| **HIGH** | "important", "asap", "stop", "cancel" | Interrupts speaking or processing |
| **NORMAL** | Regular requests | Queued if currently busy |
| **LOW** | Background tasks | Processed only when idle |

---

## Prerequisites

| Requirement | Version | Notes |
|-------------|---------|-------|
| Go | 1.25+ | [Download](https://golang.org/dl/) |
| Ollama | Latest | [Download](https://ollama.com) — for LLM backend |
| ONNX Runtime | Bundled | Included via `sherpa-onnx-go` |
| Node.js | 18+ | For local npm packages (three, @pixiv/three-vrm) |

### Optional
- **NVIDIA GPU** — For CUDA acceleration of ASR/TTS
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

### 2. Install Frontend Dependencies

```bash
npm install three @pixiv/three-vrm @pixiv/three-vrm-animation
```

### 3. Verify Models

All required ONNX models are included in the repository:

| Component | Model | Path |
|-----------|-------|------|
| Wake Word | Zipformer Gigaspeech 3.3M | `sherpa-onnx-kws-zipformer-gigaspeech-3.3M-2024-01-01/` |
| VAD | Silero VAD | `silero_vad.onnx` |
| ASR | NeMo Streaming Fast Conformer | `sherpa-onnx-nemo-streaming-fast-conformer-ctc-en-480ms/` |
| ASR | Qwen3 Offline ASR | `sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25/` |
| TTS | Supertonic | `sherpa-onnx-supertonic-3-tts-int8-2026-05-11/` |
| TTS | Pocket | `sherpa-onnx-pocket-tts-2026-01-26/` |
| TTS | ZipVoice | `sherpa-onnx-zipvoice-distill-int8-zh-en-emilia/` |

> **Note**: If models are missing, download them from the [sherpa-onnx releases page](https://github.com/k2-fsa/sherpa-onnx/releases).

### 4. Configure

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` to match your preferences. Key sections:
- `audio`: Sample rate and buffer settings
- `kws`: Wake word sensitivity, cooldown, and GPU provider
- `vad`: Speech detection thresholds and GPU provider
- `asr`: Model type (`nemo`, `zipformer`, `qwen3`), decoding method, and GPU provider
- `tts`: Active voice model, speed, and GPU provider
- `llm`: Provider, model name, and system prompt
- `agentic`: Enable/disable agentic mode
- `server`: Companion web UI settings (port, token)
- `privacy`: Sensitive word detection for hybrid mode

### 5. Prepare LLM

Pull a recommended model via Ollama:

```bash
# Small, fast, capable (recommended for most hardware)
ollama pull gemma2:2b

# Or for higher quality with more RAM
ollama pull qwen2.5:3b

# Or for best multilingual support
ollama pull phi3:mini
```

### 6. Build & Run

```bash
go mod tidy
go build -o mai.exe ./cmd/mai

# Run in legacy mode (wake word)
./mai.exe

# Run with companion web UI
./mai.exe --companion

# Optional: use a custom config file
# ./mai.exe -config my-config.yaml
```

---

## Usage

### Wake Words
- **"Mai"** — Primary wake word
- **"Hey Mai"** — Alternative phrase

### Companion Mode
- Open `http://localhost:8080` in your browser
- Click the mic button to speak
- Type messages in the chat panel
- Watch Mai's character react to your words

### Example Interactions

```text
You: "Mai, what's the weather like?"
Mai: "I don't have internet access by default, but I can help you with offline tasks."

You: "Open Chrome"
Mai: "Alright."

You: "Tell me a joke"
Mai: "Why did the Go programmer go broke? Because he used up all his cache!"

You: "Play lo-fi beats on YouTube"
Mai: "Playing lo-fi beats on YouTube."  [emotion-adaptive TTS: warm, relaxed]

You: "I'm feeling stressed about the deadline"
Mai: "I understand. Let me help you prioritize. What's the most urgent task?"  [slower, calmer TTS]
```

### Companion Skills
Mai can route certain utterances to a **Companion Skill** before normal command/function/conversation routing.

- Skills are defined in: `data/skills.json`
- A skill matches when the user text **contains** one of the skill's `triggers` (case-insensitive substring).
- When matched, Mai executes the skill using the existing ReAct pipeline.

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
| **OpenRouter** | Cloud | Set `api_key` (200+ models) |
| **NVIDIA NIM** | Cloud | Set `api_key` |

### Hybrid Mode

Enable intelligent routing between local and cloud models:

```yaml
llm:
  provider: "openai"
  model: "gpt-4o-mini"
  url: "https://api.openai.com/v1/chat/completions"
  api_key: "sk-..."
  hybrid_mode: true

privacy:
  detection_enabled: true
  sensitive_words:
    - "password"
    - "secret"
    - "credit card"
```

---

## Tool Registry

Mai's agentic mode includes a universal tool registry with category-based discovery and semantic search. Built-in tools:

| Tool | Category | Description | Example |
|------|----------|-------------|---------|
| `shell_execute` | system | Run shell commands | `"List files in current directory"` |
| `open_application` | system | Launch apps by name | `"Open Chrome"` |
| `web_search` | web | Open browser search | `"Search for Go programming"` |
| `deep_search` | web | Research with results | `"Research quantum computing"` |
| `youtube_play` | media | Play YouTube videos | `"Play Perfect on YouTube"` |
| `whatsapp_send` | communication | Send WhatsApp messages | `"Send hello to Manu on WhatsApp"` |
| `get_system_time` | query | Get current time/date | `"What time is it?"` |
| `file_write` | file | Write to files | `"Save this note to todo.txt"` |
| `ui_automation` | automation | UI control (click, type) | `"Press Ctrl+F"` |

---

## Memory System

Mai implements a hierarchical memory architecture with optimized loading:

| Layer | Storage | Purpose | Optimization |
|-------|---------|---------|--------------|
| **Working Memory** | In-memory ring buffer | Short-term conversational context | Lock-free O(1) operations |
| **Episodic Memory** | SQLite (`data/memory/episodic.db`) | Conversation and event history | Indexed queries |
| **Semantic Memory** | JSON/JSONL vector store | Long-term facts with cosine similarity | Lazy loading + approximate search |
| **Procedural Memory** | JSON (`data/memory/procedural.json`) | Skills and tool usage patterns | In-memory map |
| **RAG Pipeline** | Semantic + Episodic → LLM | Retrieve-augmented generation | Context filtering |
| **User Profile** | JSON (`data/user_profile.json`) | Preferences, habits, frequent apps, topics | Persisted |

### Semantic Memory Optimizations
- **Lazy loading**: Vectors loaded on first query, not at startup
- **JSONL append-only**: `AddFact()` appends one line (O(1)) instead of rewriting entire JSON (O(n))
- **Approximate search**: For >500 vectors, samples 300 + refines neighbors instead of brute-force over all
- **Non-blocking session restore**: Startup not blocked by memory loading

---

## Technology Stack

- **Language**: Go 1.25+ (concurrency-first architecture)
- **Frontend**: Three.js, @pixiv/three-vrm, PixiJS, Web Audio API
- **Inference**: ONNX Runtime (CPU/CUDA/CoreML/OpenCL for speech/VAD/ASR)
- **Automation**: RobotGo (Cross-platform UI control)
- **Audio**: Malgo (C-bindings for miniaudio)
- **LLM Backends**: Ollama (default), llama.cpp, OpenAI, Gemini, Claude, OpenRouter, NVIDIA
- **Memory**: SQLite (episodic), JSON/JSONL vectors (semantic), JSON files (procedural, user profile)
- **Models**: NeMo CTC, Silero VAD, Supertonic TTS, Qwen/Gemma LLMs
- **Rendering**: WebGPU → WebGL fallback, PixiJS 2D background
- **Testing**: testify (assert/require), goleak (goroutine leak detection)

---

## Configuration Reference

### Companion Server
```yaml
server:
  enabled: true
  port: 8080
  token: ""           # Optional auth token
```

### Audio Settings
```yaml
audio:
  sample_rate: 16000
  capture_buffer_ms: 100
  playback_device: ""
  barge_in_enabled: true
  barge_in_threshold: 0.008
  thinking_chime: true
```

### GPU Provider
```yaml
kws:
  provider: "cpu"  # "cpu" | "cuda" | "coreml" | "opencl"
  num_threads: 2

vad:
  provider: "cpu"
  num_threads: 2

asr:
  provider: "cpu"
  num_threads: 2

tts:
  provider: "cpu"
  num_threads: 2
```

### TTS
```yaml
tts:
  active_model: "supertonic"
  voice_style: "soft"
  base_speed: 1.05
  num_threads: 2
  output_sample_rate: 44100
  supertonic:
    model_dir: "./sherpa-onnx-supertonic-3-tts-int8-2026-05-11"
    speed: 1.25
```

### LLM
```yaml
llm:
  provider: "ollama"
  model: "gemma4:e2b-it-qat"
  url: "http://localhost:11434/api/generate"
  auto_start: true
  hybrid_mode: false
  sampling:
    temperature: 0.55
    top_p: 0.85
    max_tokens: 400
```

---

## Development

### Project Structure

```
cmd/mai/
├── main.go          # Entry point, pipeline orchestration, companion server
├── audio.go         # Audio capture (malgo), playback, streaming TTS
├── automation.go    # UI automation via RobotGo
├── actions.go       # Regex-based action parser
└── vision.go        # Vision processing via Ollama
internal/
├── agent/           # Orchestrator, user model, proactive engine, interrupt manager
├── cognition/       # Unified prompt engine, ReAct loop (natural language reasoning)
├── personality/     # Emotion detector, prosody analyzer, TTS adapter
├── llm/             # Multi-provider LLM clients and factory
├── memory/          # Working (ring buffer), episodic (SQLite), semantic (lazy JSONL), procedural
├── perception/      # Audio and vision bridges
├── tools/           # Tool registry with categories and adapters
├── server/          # WebSocket hub, HTTP server, static file serving
├── events/          # Snapshot-based pub/sub event bus
└── observability/   # Metrics, logging, health checks
pkg/
├── interfaces/      # Core Go interfaces
└── models/          # Configuration structs (with GPU provider fields)
internal/server/static/
├── js/
│   ├── character.js     # VRM 3D character renderer with alive-ness system
│   ├── motion3-loader.js # Motion3.json animation parser
│   ├── app.js           # Main app orchestration
│   ├── audio.js         # AudioPlayer with streaming clock
│   ├── chat.js          # Chat UI
│   ├── ws-client.js     # WebSocket client with auto-reconnect
│   ├── settings.js      # Settings panel
│   └── utils.js         # Viseme scheduler, energy gate, helpers
├── css/             # Character, chat, main, settings styles
├── assets/          # VRM model, motion files, character layers
└── index.html       # Companion web UI
```

---

## Roadmap

| Phase | Feature | Status | Notes |
|-------|---------|--------|-------|
| 1 | Project Foundation | ✅ Complete | Go module, config system, audio I/O |
| 2 | Wake Word Detection | ✅ Complete | Zipformer KWS with cooldown |
| 3 | VAD Integration | ✅ Complete | Silero VAD with circular buffer |
| 4 | Streaming ASR | ✅ Complete | NeMo CTC + Zipformer + Qwen3 |
| 5 | TTS Integration | ✅ Complete | Supertonic / Pocket / ZipVoice |
| 6 | Voice Pipeline | ✅ Complete | State machine, follow-up, interruptible |
| 7 | LLM Integration | ✅ Complete | Multi-provider: Ollama, OpenAI, Gemini, Claude, OpenRouter, NVIDIA |
| 8 | Automation | ✅ Complete | WhatsApp, Telegram, YouTube, App control |
| 9 | Memory System | ✅ Complete | Working + Episodic + Semantic + Procedural + RAG |
| 10 | Emotion Engine | ✅ Complete | Text + prosody detection, adaptive TTS |
| 11 | Dynamic Prompts | ✅ Complete | Unified prompt engine with JARVIS personality |
| 12 | Function Calling | ✅ Complete | Structured JSON tool invocation |
| 13 | ReAct Reasoning | ✅ Complete | Natural language reasoning (not rigid JSON steps) |
| 14 | User Modeling | ✅ Complete | Preferences, habits, topics |
| 15 | Proactive Intel | ✅ Complete | Pattern learning, idle reminders |
| 16 | Companion UI | ✅ Complete | VRM character, streaming TTS, mic input, alive-ness |
| 17 | Motion3 System | ✅ Complete | VRoid parameter animation playback |
| 18 | Cognitive Optimization | ✅ Complete | Simplified routing (2 paths), unified prompts, lazy memory |
| 19 | Performance Tuning | ✅ Complete | Zero-alloc event bus, ring buffer, approximate search |
| 20 | GPU Offloading | ✅ Complete | CUDA/CoreML/OpenCL for KWS, VAD, ASR, TTS |
| 21 | Native Integrations | 🔜 Planned | Discord/Telegram bots, plugin architecture |
| 22 | Observability | 🔜 Planned | OpenTelemetry traces + metrics |

---

## Troubleshooting

### Companion UI not loading
- Ensure `--companion` flag is passed
- Check `server.port` in config (default: 8080)
- Verify `node_modules` exists (`npm install`)

### VRM character not rendering
- Check browser console for import errors
- Ensure `three` and `@pixiv/three-vrm` are installed locally
- Try refreshing with cache clear (Ctrl+Shift+R)

### Mic not working
- Grant microphone permission in browser
- Check WebSocket connection in DevTools Network tab
- Ensure `audio.input` messages are being sent

### "Failed to create keyword spotter"
- Verify model paths in `config.yaml` match actual directories

### No audio output
- Check `audio.playback_device` in config
- Verify Windows audio output is not muted

### Ollama connection refused
- Ensure Ollama is running: `ollama serve`
- Check `llm.url` matches Ollama's actual port

### GPU not activating
- Verify `provider: "cuda"` in config.yaml (not hardcoded "cpu")
- Ensure NVIDIA drivers and CUDA toolkit are installed
- Check sherpa-onnx was built with CUDA support

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
- [Three.js](https://threejs.org) — 3D rendering
- [@pixiv/three-vrm](https://github.com/pixiv/three-vrm) — VRM model loading and animation
- [PixiJS](https://pixijs.com) — 2D rendering for background
- [AIRI](https://airi.moeru.ai/) — Alive-ness system inspiration (CPT saccades, spring physics, lip sync)
- [testify](https://github.com/stretchr/testify) — Testing assertions and requirements
- [goleak](https://go.uber.org/goleak) — Goroutine leak detection
