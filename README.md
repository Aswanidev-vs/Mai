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
| **Emotional Intelligence** | Prosody-aware STT + emotion-adaptive TTS | No emotional awareness |
| **Proactive Intelligence** | Pattern learning, anticipatory actions, idle reminders | Purely reactive |
| **User Modeling** | Learns preferences, habits, frequent apps, topics | Generic interactions |
| **Function Calling** | Structured JSON tool invocation from LLM | Raw text parsing only |
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
- **Dynamic Prompt Engine**: JARVIS personality with 8 task-tailored prompt templates (conversation, command, reasoning, creative, analysis, proactive, greeting, emergency)
- **Function Calling**: LLM outputs structured JSON tool calls (`{"tool":"name","params":{}}`) instead of raw text
- **Emotion-Aware Pipeline**: Prosody analysis from audio → emotion detection → adapted TTS speed/pitch/volume
- **Proactive Intelligence**: Pattern learning (time-of-day, frequency), anticipatory suggestions, idle reminders
- **User Modeling**: Learns preferences, tracks habits, extracts topics, persists to `data/user_profile.json`
- **Interrupt Hierarchy**: 4-level priority system (critical > high > normal > low) with queue management
- **Autonomous Proactive Monitoring**: Periodic self-reflection loops analyze context and decide if proactive assistance is needed
- **Multi-Step Goal Reasoning (ReAct)**: Breaks complex objectives into thought-action-observation cycles
- **Dual-Path Cognitive Routing**:
  - **Fast Path**: Sub-millisecond regex matching for direct commands
  - **Function Calling Path**: Structured JSON tool invocation via LLM
  - **Reasoning Path**: Deep ReAct cognitive loops for analytical problem-solving
- **Self-Correction (Reflexion)**: If a tool call fails, analyzes the error and adjusts strategy automatically
- **Hierarchical Memory**: Working (10-entry ring buffer) + Episodic (SQLite) + Semantic (vector search) + Procedural (skill patterns) + RAG pipeline

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

### Agentic Layer — Reasoning & Cognition

| Feature | Status | Description |
|---------|--------|-------------|
| **Dynamic Prompt Engine** | ✅ Ready | JARVIS personality with 8 task-tailored prompt templates, emotion-aware tone directives |
| **Function Calling** | ✅ Ready | Structured JSON tool invocation from LLM — single call and chain execution modes |
| **ReAct Reasoning Engine** | ✅ Ready | Multi-step thought → action → observation loops with anti-hallucination |
| **Task Planner** | ✅ Ready | LLM-based task decomposition with dependency tracking |
| **Fact Verifier** | ✅ Ready | Claim verification and tool call result validation |
| **Smart Routing** | ✅ Ready | Regex fast path → function calling → ReAct → planner → conversation |

### Agentic Layer — Memory & Knowledge

| Feature | Status | Description |
|---------|--------|-------------|
| **Working Memory** | ✅ Ready | In-memory short-term context buffer (10-entry ring buffer) |
| **Episodic Memory** | ✅ Ready | SQLite-backed conversation and event history |
| **Semantic Memory** | ✅ Ready | JSON vector store with cosine similarity search |
| **Procedural Memory** | ✅ Ready | Skill and tool usage pattern storage with success/failure tracking |
| **RAG Pipeline** | ✅ Ready | Semantic + episodic retrieval → LLM answer generation with confidence scoring |
| **Session Continuity** | ✅ Ready | Restores last 20 episodic entries into working memory on startup |

### Agentic Layer — Emotional Intelligence

| Feature | Status | Description |
|---------|--------|-------------|
| **Text Emotion Detection** | ✅ Ready | Keyword-based emotion scoring (happy, sad, stressed, excited, frustrated, calm) |
| **Prosody Analyzer** | ✅ Ready | Audio feature extraction: RMS energy, zero-crossing rate, spectral centroid, pitch, volume variance, pause ratio |
| **Emotion-Aware TTS** | ✅ Ready | Adapts speed, pitch, volume, emphasis, and pause scale per detected emotion |
| **Response Adaptation** | ✅ Ready | Shortens responses for stressed users, prefixes empathy for frustrated users |
| **Tone Directives** | ✅ Ready | Prompt engine injects emotion-specific directives (e.g., "be calm and efficient") |

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
| **Event Bus** | ✅ Ready | Async pub/sub communication between all components |
| **Tool Registry** | ✅ Ready | Dynamic tool discovery with categories, semantic search, runtime registration |
| **Interrupt Hierarchy** | ✅ Ready | 4-level priority system (critical > high > normal > low) with queue management |
| **Multi-Provider LLM** | ✅ Ready | Ollama, OpenAI, Gemini, Claude, OpenRouter, NVIDIA + Hybrid mode |
| **Privacy Guard** | ✅ Ready | Sensitive data detection for hybrid cloud/local routing |
| **Perception Bridge** | ✅ Ready | Audio transcription and vision event publishing |
| **Meta-Cognition** | ✅ Ready | Performance tracking, strategy analysis, and self-improvement |
| **MCP Client** | ✅ Ready | Model Context Protocol for external tool discovery |
| **Companion Skills (Jarvis-like)** | ✅ Ready | Skill routing via `data/skills.json` (trigger phrases → skill execution) |

---

## Emotion-Adaptive Pipeline

Mai detects user emotion from both text and audio prosody, then adapts its voice and responses:

```
User speaks → Audio samples → Prosody Analyzer → Emotion Detection
                                     ↓
Text transcript → Text Emotion Detection → Combined Emotion State
                                     ↓
                    ┌────────────────┼────────────────┐
                    ↓                ↓                ↓
            Prompt Engine      TTS Adapter      Response Adapter
         (tone directives)  (speed/pitch/vol)  (length/empathy)
```

**TTS Adaptation by Emotion:**

| Emotion | Speed | Pitch | Volume | Pauses | Style |
|---------|-------|-------|--------|--------|-------|
| **Stressed** | -15% | -5% | -10% | +30% | Calm, efficient |
| **Frustrated** | -10% | -2% | -5% | +20% | Patient, steady |
| **Sad** | -20% | -8% | -15% | +50% | Gentle, warm |
| **Excited** | +15% | +5% | +10% | -20% | Energetic, upbeat |
| **Happy** | +5% | +3% | +5% | -10% | Warm, positive |
| **Calm** | -5% | -2% | -5% | +10% | Relaxed, measured |

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
| TTS | Supertonic | `sherpa-onnx-supertonic-3-tts-int8-2026-05-11/` |
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

You: "Play lo-fi beats on YouTube"
Mai: "Playing lo-fi beats on YouTube."  [emotion-adaptive TTS: warm, relaxed]

You: "I'm feeling stressed about the deadline"
Mai: "I understand. Let me help you prioritize. What's the most urgent task?"  [slower, calmer TTS]
```

### Companion Skills (Proposal 1)
Mai can route certain utterances to a **Companion Skill** before normal command/function/conversation routing.

- Skills are defined in: `data/skills.json`
- A skill matches when the user text **contains** one of the skill’s `triggers` (case-insensitive substring).
- When matched, Mai:
  - executes the skill using the existing ReAct pipeline
  - stores an episodic memory entry: `Type: "skill_invoked"`

Example trigger phrases (built-in starter skills):
- “plan my day …” (Plan My Day)
- “summarize …” (Summarize)
- “weekly review …” (Weekly Review)

To add your own skill, edit `data/skills.json` (see `guide.md` → “How to add your own Companion Skill”).

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

Tools are dynamically discovered by category and keyword relevance. The LLM uses structured function calling to invoke tools with JSON parameters.

---

## Memory System

Mai implements a hierarchical memory architecture with RAG support:

| Layer | Storage | Purpose | Status |
|-------|---------|---------|--------|
| **Working Memory** | In-memory ring buffer (10 entries) | Short-term conversational context | ✅ Implemented |
| **Episodic Memory** | SQLite (`data/memory/episodic.db`) | Conversation and event history | ✅ Implemented |
| **Semantic Memory** | JSON vector store (`data/vector/`) | Long-term facts with cosine similarity search | ✅ Implemented |
| **Procedural Memory** | JSON (`data/memory/procedural.json`) | Skills and tool usage patterns with success rates | ✅ Implemented |
| **RAG Pipeline** | Semantic + Episodic → LLM | Retrieve-augmented generation with confidence scoring | ✅ Implemented |
| **User Profile** | JSON (`data/user_profile.json`) | Preferences, habits, frequent apps, topics | ✅ Implemented |

The memory manager provides unified retrieval across all layers for the ReAct loop and conversation handler.

---

## Architecture & Core Systems

Mai is built on a high-concurrency, event-driven architecture designed for low-latency offline interaction.

### Request Flow (Agentic Mode)

```
User Speech → ASR → Transcription
    ↓
Orchestrator.HandleInput()
    ├── Echo Detection (ignore TTS echo)
    ├── Emotion Detection (text keywords)
    ├── User Model Recording (interaction, topics, patterns)
    ├── Prosody Analysis (if audio samples available)
    ├── Interrupt Classification (critical/high/normal/low)
    ├── Memory Storage (working + episodic)
    │
    ├── SMART ROUTING:
    │   ├── Regex Fast Path → DirectAction (legacy, most reliable)
    │   ├── Knowledge Request → ReAct Loop (deep_search)
    │   ├── Multi-Step Command → Planner → Sequential Execution
    │   ├── Command → Function Calling (structured JSON tool calls)
    │   ├── Reasoning → ReAct Loop (think → act → observe)
    │   └── Conversation → Prompt Engine → LLM (JARVIS personality)
    │
    ├── Response Adaptation (emotion-aware truncation, prefix)
    └── TTS with Emotion-Adaptive Parameters (speed, pitch, volume)
```

### 1. Perception Layer (`internal/perception`)
- **Audio Bridge**: Captures microphone input via `malgo` (miniaudio) and routes it through VAD (Silero) and ASR (NeMo/Zipformer/Qwen).
- **Vision Bridge**: Performs periodic or on-demand screen understanding using local Vision LLMs (via Ollama).
- **Event Bus**: An in-process pub/sub bus that decouples perception from cognition.

### 2. Cognitive Layer (`internal/cognition`)
- **Prompt Engine**: Dynamic, context-aware prompt generation with JARVIS personality. 8 task types with tailored templates. Emotion-aware tone directives.
- **Function Caller**: LLM structured JSON tool invocation — single calls and chain execution.
- **ReAct Engine**: Reasoning + Acting loop with anti-hallucination, loop detection, and reflexion.
- **Planner**: LLM-based task decomposition with dependency tracking and sequential execution.
- **Verifier**: Claim verification and tool call result validation.

### 3. Agent Layer (`internal/agent`)
- **Orchestrator**: Central brain — routes inputs, manages state, coordinates all subsystems.
- **User Model**: Profile persistence, preference learning, habit tracking, topic extraction.
- **Proactive Engine**: Pattern analysis (time-of-day, frequency), anticipatory actions, idle reminders.
- **Interrupt Manager**: 4-level priority system with queue management.
- **Goal Manager**: Priority queue with heap for long-running task management.
- **Meta-Cognition**: Performance tracking, strategy analysis, self-improvement loop.
- **Privacy Guard**: Sensitive data detection for hybrid cloud/local routing.

### 4. Emotional Layer (`internal/personality`)
- **Emotion Detector**: Text-based keyword scoring + prosody-based arousal/valence mapping.
- **Prosody Analyzer**: Audio feature extraction (RMS, ZCR, spectral centroid, pitch, volume variance, pause ratio) with template matching.
- **TTS Adapter**: Emotion-aware parameter control — speed, pitch, volume, emphasis, pause scale.

### 5. Action Layer (`internal/tools` & `cmd/mai`)
- **Tool Registry**: Dynamic discovery with categories, semantic search, runtime registration/unregistration.
- **Action Executor**: High-reliability legacy system for precise UI control.
- **RobotGo Automation**: Direct OS-level control for typing, shortcut execution, and application management.

---

## Technical Package Breakdown

| Package | Responsibility |
|---------|----------------|
| `cmd/mai/` | Entry point, audio drivers, and the high-reliability legacy automation core |
| `internal/agent/` | Orchestrator, user model, proactive engine, interrupt manager, goal manager, meta-cognition, privacy guard |
| `internal/cognition/` | Prompt engine, function caller, ReAct loop, planner, verifier |
| `internal/personality/` | Emotion detector, prosody analyzer, TTS adapter |
| `internal/llm/` | Multi-provider LLM client (Ollama, OpenAI, Gemini, Claude, OpenRouter, NVIDIA, Hybrid) |
| `internal/memory/` | Hierarchical memory system (Working, Episodic, Semantic, Procedural, RAG) |
| `internal/tools/` | Tool registry with category-based discovery and adapters (Shell, Web, YouTube, WhatsApp, etc.) |
| `internal/perception/` | Bridges for ASR, VAD, and Vision data |
| `internal/events/` | Async pub/sub event bus for decoupled communication |
| `internal/observability/` | Metrics collector, structured logger, health checker |
| `pkg/interfaces/` | Core interface definitions ensuring modularity and testability |
| `pkg/models/` | Configuration structs (YAML mapping) |

---

## Technology Stack

- **Language**: Go 1.25+ (concurrency-first architecture)
- **Inference**: ONNX Runtime (CPU-optimized for speech/VAD/ASR)
- **Automation**: RobotGo (Cross-platform UI control)
- **Audio**: Malgo (C-bindings for miniaudio)
- **LLM Backends**: Ollama (default), llama.cpp, OpenAI, Gemini, Claude, OpenRouter, NVIDIA
- **Memory**: SQLite (episodic), JSON vectors (semantic), JSON files (procedural, user profile)
- **Models**: NeMo CTC, Silero VAD, Supertonic TTS, Qwen/Gemma LLMs

---

## Performance Targets

| Metric | Target | Actual (Optimized) |
|--------|--------|-------------------|
| **Fast Path Latency** | < 100ms | ~20-50ms (Regex matching) |
| **Function Calling Latency** | < 3s | ~1.5-2.5s (LLM structured output) |
| **Reasoning Latency** | < 5s | ~2-4s (ReAct multi-step) |
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
    model_dir: "./sherpa-onnx-supertonic-3-tts-int8-2026-05-11"
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
  provider: "ollama"        # "ollama", "openai", "gemini", "claude", "openrouter", "nvidia", "llamacpp"
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
├── agent/           # Orchestrator, user model, proactive engine, interrupt manager, goals, meta-cognition
├── cognition/       # Prompt engine, function caller, ReAct loop, planner, verifier
├── personality/     # Emotion detector, prosody analyzer, TTS adapter
├── llm/             # Multi-provider LLM clients and factory
├── memory/          # Working, episodic, semantic, procedural memory + RAG pipeline
├── perception/      # Audio and vision bridges
├── tools/           # Tool registry with categories and adapters
├── events/          # Pub/sub event bus
└── observability/   # Metrics, logging, health checks
pkg/
├── interfaces/      # Core Go interfaces (agent, cognition, llm, memory, tools, events)
└── models/          # Configuration structs
data/
├── memory/          # SQLite databases, procedural.json, user_profile.json
├── vector/          # Semantic vector store (JSON)
└── cache/           # Temporary caches
config.example.yaml  # Configuration template
go.mod / go.sum      # Go module definitions
prd.md              # Product Requirements Document
ROADMAP.md          # Implementation roadmap
progress.md         # Implementation progress tracker
```

### Build Commands

```bash
# Standard build
go build -o mai.exe ./cmd/mai

# With optimizations
go build -ldflags="-s -w" -o mai.exe ./cmd/mai

# Run tests
go test ./...

# Static analysis
go vet ./cmd/mai ./internal/... ./pkg/...
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
| 7 | LLM Integration | ✅ Complete | Multi-provider: Ollama, OpenAI, Gemini, Claude, OpenRouter, NVIDIA, Hybrid |
| 7b | Command Parser & Action System | ✅ Complete | High-reliability regex (Fast Path) + LLM fallback |
| 8 | Automation (RobotGo) | ✅ Complete | WhatsApp, Telegram, YouTube, and System App control |
| 9 | Memory System | ✅ Complete | Working + Episodic + Semantic + Procedural + RAG pipeline |
| 10 | Vision (Screen OCR) | 🚧 Partial | Vision bridge exists; continuous monitoring needs work |
| 11 | Emotion Engine | ✅ Complete | Text emotion detection, prosody analysis, emotion-adaptive TTS |
| 12 | Dynamic Prompt Engine | ✅ Complete | JARVIS personality, 8 task types, emotion-aware tone directives |
| 13 | Function Calling | ✅ Complete | Structured JSON tool invocation from LLM |
| 14 | Multi-step Task Planning | ✅ Complete | ReAct reasoning engine + LLM planner for complex sequences |
| 15 | User Modeling | ✅ Complete | Preference learning, habit tracking, topic extraction, persistent profile |
| 16 | Proactive Intelligence | ✅ Complete | Pattern learning, anticipatory actions, idle reminders |
| 17 | Interrupt Hierarchy | ✅ Complete | 4-level priority system with queue management |
| 18 | Polish & Performance Tuning | 🚧 In Progress | Routing optimization, latency reduction, stability fixes |

See [`ROADMAP.md`](ROADMAP.md) for detailed implementation tasks. See [`progress.md`](progress.md) for implementation tracking.

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


## Recent Optimizations & Bug Fixes

### Two-Way Communication — Barge-In, Thinking Chime, Reduced Dead Time
- **Barge-in with VAD confirmation**: Speak during TTS playback to interrupt Mai. Uses a two-layer detector: RMS threshold as the first filter, then Silero VAD as the second layer to confirm the sound is actually human speech (not echo, fans, or door slams). This provides true noise immunity. Configurable via `audio.barge_in_enabled` and `audio.barge_in_threshold`.
- **Thinking chime**: A subtle 80ms tone plays when LLM processing starts, so you know you've been heard. No more dead air. Configurable via `audio.thinking_chime`.
- **`waitForMicSilence` reduced** from 3s max to 500ms max with 3 consecutive checks for faster turn-taking. On barge-in, skipped entirely since the user is already speaking.
- **200ms pre-TTS sleep removed** — eliminates the defensive delay before every utterance.
- **`playAudio` interruptible** — supports mid-stream cancellation via context + stop flag, checked in both the audio callback and poll loop.
- **Audio callback restructured**: RMS computed once at callback entry, shared between barge-in detection and silence wait.
- **TTS Voice Style**: The system prompt's tone (calm, warm, energetic, etc.) now automatically influences TTS speed, pitch, and volume. If your prompt says "be calm and composed", TTS will speak slower and softer. Override via `tts.voice_style` in config.

### Supertonic TTS v2 → v3 Migration
- Upgraded from `sherpa-onnx-supertonic-tts-int8-2026-03-06` (Supertonic 2) to `sherpa-onnx-supertonic-3-tts-int8-2026-05-11` (Supertonic 3)
- Zero code changes required — model files are fully backward-compatible
- Supertonic 3 reduces repeat/skip failures and improves voice quality

### Bug Fixes
- **Panic recovery**: Added `defer/recover` guards in the audio callback and event bus dispatch — a panic in either no longer crashes the process
- **C memory leak**: VAD circular buffer now properly freed before reallocation on each wake-word trigger
- **`sessionSamples` reset**: Streaming ASR path properly resets the sample buffer at VAD segment boundaries
- **`lastResponseTime` initialization**: Fixed from zero-value (which caused a ~17000-year startup follow-up window bug) to `time.Now()`
- **Episodic QueryEvents**: Now uses `LIKE` query instead of ignoring the parameter — enables proper context retrieval in the RAG pipeline
- **Prosody SpeechRate**: `SpeechRate` feature is now computed via syllable-like energy peak detection (was always 0)
- **Spectral centroid**: Normalized to 0-1 range and clamped at Nyquist frequency for more accurate feature matching
- **Pitch estimation**: Clamped to human speech range (50-500 Hz) and normalized to 0-1
- **MCP client race condition**: Added `sync.RWMutex` protection around `sessionID` field for concurrent access safety

### Hallucination Reduction
- **Verifier integration**: The `Verifier` is now wired into the `ReActLoop` — tool call results and final answers are verified for plausibility before acceptance
- **ReAct timeout**: Added 30-second per-iteration context timeout — prevents stalled tool calls from hanging indefinitely
- **Reduced max iterations**: From 5 to 3 — most tasks resolve in 1-2 iterations, reducing latency

### Performance
- **`waitForMicSilence()` helper**: Extracted 3 duplicated silence-wait loops (30 lines each) into a shared closure — reduces code duplication and improves maintainability
- **Personality fully wired**: `publishTTS()` now includes emotion-adapted speed and pitch params from `TTSAdapter`, and `handleTranscription()` already included them — emotion-aware voice is active in all TTS paths

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
