# Mai → JARVIS/FRIDAY: Target State Reference Architecture

## Overview

This document defines the target architecture for evolving Mai from a reactive voice pipeline into a **cognitive agent architecture** capable of JARVIS/FRIDAY-level autonomy. The design follows principles of **modularity**, **loose coupling**, **interface-driven design**, and **event-driven communication**.

---

## Architectural Principles

1. **Interface-Driven Design**: Every core component is defined by an interface, not an implementation
2. **Event-Driven Architecture**: Components communicate via async events, not direct calls
3. **Hierarchical Memory**: Working → Episodic → Semantic → Procedural memory layers
4. **Cognitive Loop**: Perceive → Reason → Plan → Act → Learn → (repeat)
5. **Multi-Modal Fusion**: All sensory inputs converge in a unified representation space
6. **Tool Agnosticism**: Tools are discovered and invoked dynamically, not hardcoded
7. **Evolution, Not Revolution**: Existing code is preserved, wrapped, and extended

---

## System Architecture Diagram

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           AGENT ORCHESTRATOR                                │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐    │
│  │   Goal       │  │  Executive   │  │   Session    │  │   Meta-      │    │
│  │   Manager    │  │  Controller  │  │   Manager    │  │  Cognition   │    │
│  │              │  │              │  │              │  │  (Self-      │    │
│  │ - Priority   │  │ - BDI Loop   │  │ - Context    │  │   Improve)   │    │
│  │ - Scheduling │  │ - Interrupt  │  │   windows    │  │              │    │
│  │ - Conflict   │  │   handling   │  │ - User state │  │ - Performance│    │
│  │   resolution │  │ - Resource   │  │ - Emotion    │  │   analysis   │    │
│  │              │  │   allocation │  │   tracking   │  │ - Strategy   │    │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘  │   evolution  │    │
│         │                 │                 │          └──────────────┘    │
│         └─────────────────┴─────────────────┘                                │
│                           │                                                  │
└───────────────────────────┼──────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         COGNITIVE ENGINE (Core)                             │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                         REASONING LAYER                              │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌────────────┐ │    │
│  │  │  Chain-of-  │  │   ReAct     │  │  Reflection │  │  Verifier  │ │    │
│  │  │  Thought    │  │   Loop      │  │  (Self-Crit)│  │  (Facts)   │ │    │
│  │  │             │  │             │  │             │  │            │ │    │
│  │  │ Step-by-step│  │ Thought→Act │  │ "Was I      │  │ "Is this   │ │    │
│  │  │ reasoning   │  │ →Observe    │  │  correct?"  │  │  true?"    │ │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘  └────────────┘ │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                         PLANNING LAYER                               │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌────────────┐ │    │
│  │  │  Hierarchical│  │   Task      │  │   Temporal  │  │  Resource  │ │    │
│  │  │  Task Network│  │   Decomp    │  │   Planner   │  │  Allocator │ │    │
│  │  │  (HTN)       │  │             │  │             │  │            │ │    │
│  │  │              │  │ Goal→Subtask│  │ Scheduling  │  │ CPU/GPU/   │ │    │
│  │  │ If-then plans│  │ with retry  │  │ Deadlines   │  │ Network    │ │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘  └────────────┘ │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      LLM ABSTRACTION LAYER                           │    │
│  │                                                                      │    │
│  │   ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐           │    │
│  │   │  Ollama  │  │ llama.cpp│  │  OpenAI  │  │  Local   │           │    │
│  │   │  Client  │  │  Client  │  │  Client  │  │  vLLM    │           │    │
│  │   │          │  │          │  │ (opt-in) │  │          │           │    │
│  │   └──────────┘  └──────────┘  └──────────┘  └──────────┘           │    │
│  │                                                                      │    │
│  │   Unified Interface:                                                 │    │
│  │   - Generate(ctx, prompt, opts) → (string, error)                   │    │
│  │   - GenerateStructured(ctx, prompt, schema) → (JSON, error)         │    │
│  │   - Stream(ctx, prompt, callback) → error                           │    │
│  │   - Embed(ctx, text) → ([]float32, error)                           │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          MEMORY HIERARCHY                                   │
│                                                                              │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────────┐    ┌───────────┐ │
│  │   WORKING    │───▶│   EPISODIC   │───▶│   SEMANTIC   │───▶│ PROCEDURAL│ │
│  │   MEMORY     │    │   MEMORY     │    │   MEMORY     │    │  MEMORY   │ │
│  │              │    │              │    │              │    │           │ │
│  │ - Current    │    │ - Conversations│  │ - User facts │    │ - Skills  │ │
│  │   context    │    │ - Events     │    │ - World      │    │ - Tool    │ │
│  │ - Active     │    │ - Experiences│    │   knowledge  │    │   usage   │ │
│  │   goals      │    │ - Timestamps │    │ - Concepts   │    │   patterns│ │
│  │ - Attention  │    │              │    │              │    │           │ │
│  │   buffer     │    │ SQLite +     │    │ Vector DB    │    │ Compiled  │ │
│  │              │    │ Markdown     │    │ (Chroma/     │    │ programs  │ │
│  │ In-memory    │    │              │    │  Milvus)     │    │           │ │
│  │ (10-100 KB)  │    │ (10 MB-1 GB) │    │ (100 MB-10GB)│    │ (1-10 MB) │ │
│  └──────────────┘    └──────────────┘    └──────────────┘    └───────────┘ │
│                                                                              │
│  Retrieval-Augmented Generation (RAG) Pipeline:                              │
│  Query → Embedding → Vector Search → Re-rank → Inject into LLM Context       │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        PERCEPTION LAYER (Multi-Modal)                       │
│                                                                              │
│  ┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐              │
│  │     AUDIO       │  │     VISION      │  │   ENVIRONMENTAL │              │
│  │    PROCESSING   │  │    PROCESSING   │  │     SENSING     │              │
│  │                 │  │                 │  │                 │              │
│  │                 │  │                 │  │                 │              │
│  │ ┌─────────────┐ │  │ ┌─────────────┐ │  │ ┌─────────────┐ │              │
│  │ │  Wake Word  │ │  │ │  Screen     │ │  │ │  Presence   │ │              │
│  │ │  (KWS)      │ │  │ │  Capture    │ │  │ │  Detection  │ │              │
│  │ └─────────────┘ │  │ └─────────────┘ │  │ └─────────────┘ │              │
│  │ ┌─────────────┐ │  │ ┌─────────────┐ │  │ ┌─────────────┐ │              │
│  │ │  VAD        │ │  │ │  OCR /      │ │  │ │  Motion     │ │              │
│  │ │  (Silero)   │ │  │ │  Scene      │ │  │ │  Detection  │ │              │
│  │ └─────────────┘ │  │ │  Understanding│ │  │ └─────────────┘ │              │
│  │ ┌─────────────┐ │  │ └─────────────┘ │  │ ┌─────────────┐ │              │
│  │ │  ASR        │ │  │ ┌─────────────┐ │  │ │  Biometric  │ │              │
│  │ │  (Streaming)│ │  │ │  Object     │ │  │ │  (Heart rate,│ │              │
│  │ └─────────────┘ │  │ │  Detection  │ │  │ │  stress)    │ │              │
│  │ ┌─────────────┐ │  │ │  (YOLO/     │ │  │ └─────────────┘ │              │
│  │ │  Emotion    │ │  │ │  DETR)      │ │  │ ┌─────────────┐ │              │
│  │ │  Detection  │ │  │ └─────────────┘ │  │ │  System     │ │              │
│  │ │  (Prosody)  │ │  │ ┌─────────────┐ │  │ │  Monitoring │ │              │
│  │ └─────────────┘ │  │ │  Face       │ │  │ │  (CPU, RAM, │ │              │
│  │ ┌─────────────┐ │  │ │  Recognition│ │  │ │  Network)   │ │              │
│  │ │  Speaker    │ │  │ └─────────────┘ │  │ └─────────────┘ │              │
│  │ │  ID         │ │  │ ┌─────────────┐ │  │                 │              │
│  │ └─────────────┘ │  │ │  Gaze       │ │  │                 │              │
│  │                 │  │ │  Tracking   │ │  │                 │              │
│  │                 │  │ └─────────────┘ │  │                 │              │
│  └─────────────────┘  └─────────────────┘  └─────────────────┘              │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                    MULTI-MODAL FUSION ENGINE                         │    │
│  │                                                                      │    │
│  │  - Cross-modal attention (audio + vision alignment)                  │    │
│  │  - Temporal synchronization (event timestamps across sensors)        │    │
│  │  - Scene graph construction (objects, people, relationships)         │    │
│  │  - Unified embedding space (all modalities → common representation)  │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                        ACTION & TOOL LAYER                                  │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                      TOOL REGISTRY & DISCOVERY                       │    │
│  │                                                                      │    │
│  │  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐            │    │
│  │  │ System   │  │   Web    │  │ Hardware │  │ Custom   │            │    │
│  │  │ Tools    │  │  APIs    │  │  APIs    │  │ Plugins  │            │    │
│  │  │          │  │          │  │          │  │          │            │    │
│  │  │ - OpenApp│  │ - Search │  │ - Lights │  │ - User   │            │    │
│  │  │ - Type   │  │ - Weather│  │ - Locks  │  │   defined│            │    │
│  │  │ - Click  │  │ - Calendar│ │ - Thermo │  │   tools  │            │    │
│  │  │ - Screenshot│ - Email │  │ - Cameras│  │          │            │    │
│  │  │          │  │ - Maps   │  │ - Drones │  │          │            │    │
│  │  └──────────┘  └──────────┘  └──────────┘  └──────────┘            │    │
│  │                                                                      │    │
│  │  ┌─────────────────────────────────────────────────────────────┐    │    │
│  │  │              LEGACY ADAPTER LAYER (Preserved)                │    │
│  │  │                                                              │    │
│  │  │  Existing cmd/mai/ modules are NOT deleted. They are wrapped │    │
│  │  │  by interfaces and registered as tools in the Tool Registry. │    │
│  │  │                                                              │    │
│  │  │  • audio.go    → AudioDriver interface → Tool: audio_control │    │
│  │  │  • vision.go   → VisionDriver interface → Tool: screen_vision│    │
│  │  │  • automation.go→ AutomationDriver → Tools: ui_*             │    │
│  │  │  • actions.go  → ActionParser interface → Tool: parse_action │    │
│  │  │                                                              │    │
│  │  └─────────────────────────────────────────────────────────────┘    │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘

                            │
                            ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                     EVENT BUS & COMMUNICATION LAYER                         │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                    ASYNC MESSAGE BUS (pub/sub)                       │    │
│  │                                                                      │    │
│  │   Topics:                                                            │    │
│  │   • perception.audio.wake_word    • cognition.task.completed         │    │
│  │   • perception.audio.speech       • cognition.task.failed            │    │
│  │   • perception.vision.scene       • action.tool.executed             │    │
│  │   • perception.sensor.motion      • memory.new_episode               │    │
│  │                                                                      │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

### How New Components Coexist

```
┌─────────────────────────────────────────────────────────────────┐
│                     NEW AGENTIC LAYER (Added)                    │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐             │
│  │   Goal      │  │  Cognitive  │  │   Memory    │             │
│  │   Manager   │  │   Engine    │  │   System    │             │
│  └──────┬──────┘  └──────┬──────┘  └──────┬──────┘             │
│         │                │                │                      │
│         └────────────────┼────────────────┘                      │
│                          │                                       │
│              ┌───────────┴───────────┐                          │
│              ▼                       ▼                          │
│  ┌─────────────────────┐  ┌─────────────────────┐               │
│  │   NEW: Tool Registry │  │   NEW: Event Bus    │               │
│  │   (wraps existing)   │  │   (connects all)    │               │
│  └──────────┬──────────┘  └─────────────────────┘               │
│             │                                                    │
└─────────────┼────────────────────────────────────────────────────┘
              │
┌─────────────┼────────────────────────────────────────────────────┐
│             ▼                                                    │
│  ┌─────────────────────────────────────────────────────────────┐ │
│  │              EXISTING PIPELINE (Preserved)                   │ │
│  │                                                              │ │
│  │   ┌─────────┐    ┌─────────┐    ┌─────────┐    ┌────────┐  │ │
│  │   │  KWS    │───▶│  VAD    │───▶│  ASR    │───▶│  LLM   │  │ │
│  │   │(sherpa) │    │(silero) │    │(sherpa) │    │(ollama)│  │ │
│  │   └─────────┘    └─────────┘    └─────────┘    └───┬────┘  │ │
│  │                                                     │       │ │
│  │   ┌─────────┐    ┌─────────┐    ┌─────────┐        │       │ │
│  │   │ Actions │◄───│  Regex  │◄───│  Parse  │◄───────┘       │ │
│  │   │(robotgo)│    │ Parser  │    │         │                │ │
│  │   └─────────┘    └─────────┘    └─────────┘                │ │
│  │                                                              │ │
│  │   ┌─────────┐    ┌─────────┐                               │ │
│  │   │  TTS    │◄───│  Audio  │                               │ │
│  │   │(sherpa) │    │ Playback│                               │ │
│  │   └─────────┘    └─────────┘                               │ │
│  │                                                              │ │
│  └─────────────────────────────────────────────────────────────┘ │
│                                                                  │
│  Existing `main()` still works. New `agenticMain()` is optional. │
└──────────────────────────────────────────────────────────────────┘
```

### Dual-Mode Operation

| Mode | Trigger | Behavior |
|------|---------|----------|
| **Legacy Mode** | `config.agentic.enabled: false` (default) | Exactly today's behavior. Wake word → ASR → regex/LLM → TTS. Zero cognitive overhead. |
| **Agentic Mode** | `config.agentic.enabled: true` | Full cognitive loop with memory, planning, and proactivity. Existing pipeline becomes one of many tools. |
| **Hybrid Mode** | Runtime toggle | Agentic layer handles complex requests; legacy pipeline handles simple commands for lower latency. |

---

## Package Structure

```
mai/
├── cmd/mai/                          # Entry points (existing + new)
│   ├── main.go                       # Existing: legacy pipeline boot
│   └── agent_main.go                 # NEW: agentic orchestrator boot
│
├── internal/                         # NEW: Core agentic packages
│   ├── agent/                        # NEW: BDI loop, goal manager, executive controller
│   │   ├── loop.go                   # NEW: Main agentic loop
│   │   ├── goals.go                  # NEW: Goal representation & priority queue
│   │   ├── executive.go              # NEW: Interrupt handling, resource allocation
│   │   └── meta.go                   # NEW: Self-monitoring & improvement
│   │
│   ├── cognition/                    # NEW: Reasoning & planning
│   │   ├── react.go                  # NEW: ReAct loop implementation
│   │   ├── cot.go                    # NEW: Chain-of-Thought generator
│   │   ├── reflexion.go              # NEW: Self-criticism & correction
│   │   ├── planner.go                # NEW: HTN task decomposition
│   │   └── verifier.go              # NEW: Fact-checking & hallucination detection
│   │
│   ├── memory/                       # NEW: Memory hierarchy
│   │   ├── working.go                # NEW: In-memory context buffer
│   │   ├── episodic.go               # NEW: SQLite conversation store
│   │   ├── semantic.go               # NEW: Vector DB (Chroma/Milvus) + embeddings
│   │   ├── procedural.go             # NEW: Skill & tool usage patterns
│   │   └── rag.go                    # NEW: Retrieval-Augmented Generation pipeline
│   │
│   ├── perception/                   # NEW: Multi-modal sensory fusion
│   │   ├── audio/                    # NEW: Audio perception wrapper
│   │   │   └── processor.go          # NEW: Wraps existing audio.go
│   │   ├── vision/                   # NEW: Vision perception wrapper
│   │   │   └── processor.go          # NEW: Wraps existing vision.go + adds YOLO/OCR
│   │   ├── environmental/            # NEW: Sensors, presence, biometrics
│   │   └── fusion.go                 # NEW: Cross-modal attention & scene graph
│   │
│   ├── llm/                          # NEW: Enhanced LLM abstraction
│   │   ├── client.go                 # NEW: Unified interface (Generate, Stream, Embed)
│   │   ├── structured.go             # NEW: JSON mode / function calling
│   │   ├── providers/                # NEW: Ollama, llama.cpp, OpenAI, vLLM
│   │   └── tokenizer.go              # NEW: Token counting for context management
│   │
│   ├── tools/                        # NEW: Universal tool registry
│   │   ├── registry.go               # NEW: Tool discovery & registration
│   │   ├── executor.go               # NEW: Safe execution with sandboxing
│   │   ├── mcp.go                    # NEW: Model Context Protocol client
│   │   └── adapters/                 # NEW: Wrappers for existing capabilities
│   │       ├── automation.go         # NEW: Wraps cmd/mai/automation.go
│   │       ├── actions.go            # NEW: Wraps cmd/mai/actions.go
│   │       └── system.go             # NEW: File, shell, network tools
│   │
│   ├── tts/                          # NEW: Enhanced TTS with emotion
│   │   ├── engine.go                 # NEW: TTS orchestrator
│   │   ├── prosody.go                # NEW: Emotional prosody control
│   │   └── voices.go                 # NEW: Voice selection & cloning
│   │
│   ├── events/                       # NEW: Event bus
│   │   ├── bus.go                    # NEW: Pub/sub event bus
│   │   ├── types.go                  # NEW: Event type definitions
│   │   └── subscribers.go            # NEW: Event consumers
│   │
│   └── config/                       # NEW: Dynamic configuration
│       ├── manager.go                # NEW: Hot-reload config
│       └── schema.go                 # NEW: Agentic config schema
│
├── cmd/mai/                          # EXISTING: Preserved as-is
│   ├── main.go                       # EXISTING: Legacy boot
│   ├── audio.go                      # EXISTING: malgo audio I/O
│   ├── vision.go                     # EXISTING: Ollama vision
│   ├── automation.go                 # EXISTING: robotgo automation
│   └── actions.go                    # EXISTING: regex action parser
│
├── pkg/                              # NEW: Public API packages
│   ├── interfaces/                   # NEW: All Go interfaces
│   │   ├── perception.go             # NEW: PerceptionProvider, Sensor
│   │   ├── cognition.go              # NEW: Reasoner, Planner, Memory
│   │   ├── llm.go                    # NEW: LLMProvider, Embedder
│   │   ├── tools.go                  # NEW: Tool, ToolRegistry, Executor
│   │   └── agent.go                  # NEW: Agent, Goal, Session
│   │
│   └── models/                       # NEW: Shared data models
│       ├── memory.go                 # NEW: Memory entry types
│       ├── events.go                 # NEW: Event payloads
│       └── actions.go                # NEW: Structured action types
│
├── data/                             # NEW: Runtime data
│   ├── memory/                       # NEW: Markdown long-term memory
│   ├── vector/                       # NEW: Vector DB files
│   └── cache/                        # NEW: Temporary caches
│
├── config.yaml                       # EXISTING: Extended with agentic section
├── go.mod                            # EXISTING: Dependencies added
└── models/                           # EXISTING: ONNX models (unchanged)
```

---

## Event Bus & Inter-Component Communication

Components do NOT call each other directly. All communication flows through the **Event Bus**.

```go
// pkg/interfaces/events.go
type EventBus interface {
    Publish(event Event) error
    Subscribe(eventType string, handler EventHandler) Subscription
    SubscribeAsync(eventType string, handler EventHandler) Subscription
    RequestResponse(request Event, timeout time.Duration) (Event, error)
}

type Event struct {
    Type      string                 `json:"type"`
    Source    string                 `json:"source"`      // Component ID
    Timestamp time.Time              `json:"timestamp"`
    Payload   map[string]interface{} `json:"payload"`
    Priority  EventPriority          `json:"priority"`    // Low, Normal, High, Critical
    SessionID string                 `json:"session_id"`  // For tracing
}
```

### Event Flow Example: "Open Chrome and Search"

```
[AudioPerception] ──"open chrome and search for weather"──▶ [EventBus]
                                                                    │
                                                                    ▼
[ASRCompletedEvent] ──────────────────────────────────────────▶ [CognitiveEngine]
                                                                    │
                                                                    ▼
[ReActLoop] ──THOUGHT: "User wants to open Chrome and search"──▶ [Planner]
                                                                    │
                                                                    ▼
[PlanCreatedEvent] ──[OpenChrome, FocusWindow, TypeQuery, PressEnter]──▶ [ToolExecutor]
                                                                    │
                                                                    ▼
[ToolExecutionEvent: OpenChrome] ─────────────────────────────▶ [AutomationAdapter]
                                                                    │
                                                                    ▼
[ActionCompletedEvent] ───────────────────────────────────────▶ [CognitiveEngine]
                                                                    │
                                                                    ▼
[TTSRequestEvent: "Done"] ────────────────────────────────────▶ [TTSAdapter]
                                                                    │
                                                                    ▼
[AudioPlaybackEvent] ─────────────────────────────────────────▶ [AudioPerception]
```

### Existing Code Integration

Existing `cmd/mai/main.go` publishes events by wrapping its state machine:

```go
// internal/perception/audio/processor.go
type AudioProcessor struct {
    capture *audioCapture  // Existing cmd/mai/audio.go
    bus     EventBus
}

func (p *AudioProcessor) onSamples(samples []float32) {
    // Existing audio processing...
    p.bus.Publish(Event{
        Type:   "audio.samples",
        Source: "audio.perception",
        Payload: map[string]interface{}{
            "samples": samples,
            "rms": calculateRMS(samples),
        },
    })
}
```

---

## Observability & Telemetry

A JARVIS-level agent must monitor itself.

```go
// internal/agent/meta.go
type MetaCognition struct {
    PerformanceLog  PerformanceTracker
    StrategyEvolver StrategyEvolver
    FailureAnalyzer FailureAnalyzer
}

type PerformanceTracker interface {
    RecordLatency(operation string, duration time.Duration)
    RecordAccuracy(operation string, success bool)
    GetMetrics(window time.Duration) MetricsReport
}

type MetricsReport struct {
    ASRLatency      time.Duration `json:"asr_latency"`
    LLMLatency      time.Duration `json:"llm_latency"`
    TTSLatency      time.Duration `json:"tts_latency"`
    EndToEndLatency time.Duration `json:"e2e_latency"`
    ActionSuccessRate float64     `json:"action_success_rate"`
    MemoryHitRate   float64       `json:"memory_hit_rate"`
    CPUUsage        float64       `json:"cpu_usage"`
    RAMUsageMB      int           `json:"ram_usage_mb"`
}
```

### Self-Improvement Loop

```
┌─────────────────┐     ┌─────────────────┐     ┌─────────────────┐
│  Execute Task   │────▶│  Record Result  │────▶│  Analyze Pattern │
│                 │     │  (success/fail) │     │                 │
└─────────────────┘     └─────────────────┘     └────────┬────────┘
                                                         │
                              ┌──────────────────────────┘
                              ▼
                    ┌─────────────────┐
                    │  Strategy       │
                    │  Evolution      │
                    │                 │
                    │ - Adjust prompt │
                    │ - Retry with    │
                    │   different tool│
                    │ - Escalate to   │
                    │   user          │
                    └─────────────────┘
```

---

## Migration Path: From Mai to JARVIS

### Phase 0: Foundation (Weeks 1-2) — No Behavior Change
- **Refactor existing code into interfaces** without changing behavior
- Create `pkg/interfaces/` with abstractions for audio, vision, automation, LLM
- Existing `main.go` continues to work exactly as before
- Add `internal/legacy/` package that wraps existing code

### Phase 1: Side-by-Side (Weeks 3-6) — New Code Alongside Old
- Add `internal/events/` event bus (no consumers yet)
- Add `internal/memory/` with SQLite working memory
- Add `internal/llm/` structured output client
- Add `internal/tools/` registry with existing actions as first tools
- **Key:** New packages are imported but not wired into main loop yet

### Phase 2: Cognitive Layer (Weeks 7-12) — Optional Agentic Mode
- Implement `internal/cognition/` ReAct + CoT
- Implement `internal/agent/` BDI loop
- Add `cmd/mai/agent_main.go` as alternative entry point
- User can switch between legacy and agentic mode via config flag
- Existing `main.go` untouched

### Phase 3: Multi-Modal & Proactivity (Weeks 13-20)
- Add `internal/perception/` with vision, environmental sensors
- Implement proactive monitoring loop
- Add emotional prosody to TTS
- Enable RAG over accumulated memory

### Phase 4: Ecosystem & Self-Improvement (Weeks 21-30)
- MCP tool discovery
- Autonomous API learning
- Meta-cognition and strategy evolution
- Cross-platform automation adapters

### Phase 5: Polish & Optimization (Weeks 31-36)
- Performance tuning
- Latency optimization
- Memory compression
- Edge case handling

---

## Interface Definitions (Core Contracts)

```go
// pkg/interfaces/agent.go
type Agent interface {
    Start(ctx context.Context) error
    Stop() error
    HandleInput(ctx context.Context, input MultiModalInput) (*AgentResponse, error)
    SetGoal(ctx context.Context, goal Goal) error
    GetStatus() AgentStatus
}

// pkg/interfaces/cognition.go
type CognitionEngine interface {
    Reason(ctx context.Context, goal string, context ContextSnapshot) (*ReasoningResult, error)
    Plan(ctx context.Context, goal string, reasoning *ReasoningResult) (*Plan, error)
    Reflexion(ctx context.Context, execution *ExecutionResult) (*ReflexionResult, error)
}

// pkg/interfaces/memory.go
type MemoryManager interface {
    Working() WorkingMemory
    Episodic() EpisodicStore
    Semantic() SemanticStore
    Procedural() ProceduralStore
    Retrieve(ctx context.Context, query string, k int) ([]MemoryEntry, error)
    Store(ctx context.Context, entry MemoryEntry) error
}

// pkg/interfaces/perception.go
type PerceptionProvider interface {
    Start() error
    Stop() error
    Subscribe(eventType string) <-chan PerceptionEvent
}

// pkg/interfaces/tools.go
type ToolRegistry interface {
    Register(tool Tool) error
    Discover(ctx context.Context, description string) ([]Tool, error)
    Execute(ctx context.Context, toolName string, params map[string]interface{}) (ToolResult, error)
    List() []ToolMetadata
}
```

---

## Summary

This architecture enables Mai to evolve from a **reactive voice pipeline** into a **proactive cognitive agent** while:

1. **Preserving every existing capability** — No code deletion, no breaking changes
2. **Adding modular cognitive layers** — Memory, reasoning, planning, proactivity
3. **Enabling gradual adoption** — Legacy mode works forever; agentic mode is opt-in
4. **Supporting multi-modal fusion** — Audio, vision, environmental sensors unified
5. **Providing universal tool use** — Any API, any hardware, any software becomes accessible
6. **Achieving self-improvement** — The agent monitors, analyzes, and evolves its own strategies

> *"The suit and I are one."* — Tony Stark
> 
> Mai and her capabilities are one. Every existing skill is preserved. New cognitive powers are added. The result is not replacement, but **transcendence**.



---

## Evolution Strategy: Preserve Existing, Add Alongside

> **Core Principle**: The existing Mai codebase is NOT replaced. It is **preserved, wrapped, and extended**.
> Every current capability continues to work while new agentic layers are added around it.

### What Stays Completely Unchanged

| Current File | Current Capability | Preservation Strategy |
|-------------|-------------------|----------------------|
| `cmd/mai/audio.go` | Microphone capture & playback via malgo | **Kept as-is**. Wrapped by `AudioPerception` adapter that adds emotion detection pipeline alongside existing raw audio path |
| `cmd/mai/automation.go` | UI automation via robotgo | **Kept as-is**. Registered as `SystemToolProvider` in Tool Registry; all existing app launchers, keyboard/mouse, messaging remain functional |
| `cmd/mai/actions.go` | Regex-based action parser | **Kept as-is**. Fallback parser when LLM-based parsing fails or is offline; fuzzy matching continues to work |
| `cmd/mai/vision.go` | Coordinate extraction via Ollama vision | **Kept as-is**. Upgraded to `VisionPerception` module; `FindElement` becomes one tool among many vision capabilities |
| `cmd/mai/main.go` | State machine, pipeline orchestration | **Gradually refactored**. Existing state machine preserved as `LegacyPipeline` mode; new `AgenticOrchestrator` runs alongside |

### New Packages Added Alongside

```
cmd/mai/                              # Existing entry point (preserved)
├── main.go                           # Minimal wiring; delegates to orchestrator
├── audio.go                          # PRESERVED — malgo audio I/O
├── automation.go                     # PRESERVED — robotgo automation
├── actions.go                        # PRESERVED — regex action parser
├── vision.go                         # PRESERVED — Ollama vision

internal/                             # NEW — agentic architecture
├── orchestrator/                     # NEW — agentic loop & BDI engine
│   ├── orchestrator.go               # Main cognitive loop
│   ├── goal_manager.go               # Goal queue, prioritization, scheduling
│   ├── executive_controller.go       # Interrupt handling, resource allocation
│   └── session_manager.go            # User state, emotion tracking
│
├── cognition/                        # NEW — reasoning & planning
│   ├── react.go                      # ReAct loop implementation
│   ├── cot.go                        # Chain-of-Thought generator
│   ├── reflexion.go                  # Self-criticism & strategy adjustment
│   ├── htn_planner.go                # Hierarchical Task Network planner
│   └── verifier.go                   # Fact-checking & hallucination detection
│
├── memory/                           # NEW — persistent memory hierarchy
│   ├── working_memory.go             # In-memory context buffer
│   ├── episodic_store.go             # SQLite + Markdown conversation history
│   ├── semantic_store.go             # Vector DB (Chroma/Milvus) for RAG
│   ├── procedural_store.go           # Compiled skill patterns
│   └── user_model.go                 # Preference learning, relationship model
│
├── perception/                       # NEW — multi-modal sensory fusion
│   ├── audio_perception.go           # Wraps audio.go + adds emotion/stress detection
│   ├── vision_perception.go          # Wraps vision.go + adds scene understanding, OCR, face detection
│   ├── environmental_sensing.go      # System metrics, presence detection, IoT sensors
│   └── fusion_engine.go              # Cross-modal attention, temporal alignment, scene graph
│
├── llm/                              # NEW — LLM abstraction layer
│   ├── provider.go                   # Interface: Generate, Stream, Embed, GenerateStructured
│   ├── ollama_client.go              # Enhanced Ollama client with JSON mode
│   ├── openai_client.go              # OpenAI-compatible client
│   ├── function_caller.go            # Structured function calling framework
│   └── rag.go                        # Retrieval-Augmented Generation pipeline
│
├── tools/                            # NEW — universal tool registry
│   ├── registry.go                   # Tool registration & discovery
│   ├── system_tools.go               # Wraps automation.go capabilities
│   ├── api_tools.go                  # Generic REST/GraphQL/gRPC tool builder
│   ├── hardware_tools.go             # IoT, smart home, robotics interfaces
│   └── mcp_adapter.go                # Model Context Protocol adapter
│
├── personality/                      # NEW — emotional intelligence
│   ├── emotion_detector.go           # Prosody analysis, text sentiment
│   ├── emotion_mapper.go             # Maps detected emotion to TTS parameters
│   ├── personality_engine.go         # Adaptive persona based on user relationship
│   └── voice_style_manager.go        # Dynamic TTS speed/pitch/prosody control
│
├── events/                           # NEW — event-driven communication
│   ├── bus.go                        # Central event bus (pub/sub)
│   ├── topics.go                     # Event topic definitions
│   └── handlers.go                   # Cross-module event handlers
│
└── observability/                    # NEW — monitoring & telemetry
    ├── metrics.go                    # Latency, throughput, error rates
    ├── tracing.go                    # Distributed tracing for agentic loops
    ├── logger.go                     # Structured logging
    └── health.go                     # Component health checks
```

---

## Interface Definitions

### Core Abstractions

```go
// LLMProvider — unified interface for all LLM backends
type LLMProvider interface {
    Generate(ctx context.Context, prompt string, opts GenerationOptions) (string, error)
    Stream(ctx context.Context, prompt string, callback func(chunk string)) error
    GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error)
    Embed(ctx context.Context, text string) ([]float32, error)
    HealthCheck(ctx context.Context) error
}

// MemoryStore — interface for all memory layers
type MemoryStore interface {
    Store(ctx context.Context, entry MemoryEntry) error
    Retrieve(ctx context.Context, query string, opts RetrievalOptions) ([]MemoryEntry, error)
    Forget(ctx context.Context, filter ForgetFilter) error
}

// PerceptionModule — interface for all sensory inputs
type PerceptionModule interface {
    Start(ctx context.Context) error
    Stop() error
    Subscribe(events chan<- PerceptionEvent)
    GetCapabilities() []Capability
}

// Tool — interface for all executable capabilities
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage // JSON Schema for parameters
    Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

// AgenticLoop — core cognitive loop interface
type AgenticLoop interface {
    Start(ctx context.Context) error
    Stop() error
    SubmitGoal(ctx context.Context, goal Goal) (GoalID, error)
    CancelGoal(id GoalID) error
    GetStatus() LoopStatus
}
```

---

## Event Bus: Decoupled Communication

All modules communicate via async events rather than direct calls:

```go
// Event bus enables loose coupling between components
type EventBus interface {
    Publish(topic string, payload interface{})
    Subscribe(topic string, handler EventHandler) Subscription
}

// Core event topics
const (
    TopicAudioDetected     = "perception.audio.detected"
    TopicWakeWord          = "perception.audio.wakeword"
    TopicSpeechRecognized  = "perception.audio.transcription"
    TopicVisionScene       = "perception.vision.scene"
    TopicEmotionDetected   = "perception.emotion.detected"
    TopicGoalCreated       = "cognition.goal.created"
    TopicPlanUpdated       = "cognition.plan.updated"
    TopicActionExecuted    = "action.executed"
    TopicActionFailed      = "action.failed"
    TopicMemoryStored      = "memory.stored"
    TopicLLMResponse       = "llm.response"
    TopicTTSStarted        = "audio.tts.started"
    TopicTTSCompleted      = "audio.tts.completed"
    TopicUserAbsent        = "session.user.absent"
    TopicUserReturned      = "session.user.returned"
    TopicSystemAlert       = "system.alert"
)
```

**Example Event Flow:**
```
[AudioPerception] ──"perception.audio.wakeword"──▶ [EventBus]
                                                      │
                              ┌───────────────────────┼───────────────────────┐
                              ▼                       ▼                       ▼
                        [Orchestrator]          [SessionManager]         [Memory]
                              │                       │                       │
                              ▼                       ▼                       ▼
                        Creates Goal            Updates User State       Stores Context
                              │                                               │
                              ▼                                               ▼
                        "cognition.goal.created"                    "memory.stored"
                              │                                               │
                              └───────────────────────┬───────────────────────┘
                                                      ▼
                                              [Subscribers React]
```

---

## Observability & Telemetry

### Metrics Collected

| Metric | Source | Purpose |
|--------|--------|---------|
| `asr_latency_ms` | AudioPerception | Track speech recognition speed |
| `llm_time_to_first_token_ms` | LLMProvider | Measure LLM responsiveness |
| `tts_generation_ms` | TTS module | Monitor synthesis latency |
| `action_success_rate` | ToolRegistry | Track automation reliability |
| `memory_retrieval_accuracy` | SemanticStore | RAG quality monitoring |
| `goal_completion_rate` | GoalManager | Agent effectiveness |
| `emotion_detection_confidence` | EmotionDetector | Calibration tracking |

### Structured Logging

```go
// Every component logs with consistent context
logger.Info("goal.completed",
    "goal_id", goal.ID,
    "duration_ms", elapsed,
    "steps_count", len(goal.Steps),
    "success", true,
    "user_emotion", session.CurrentEmotion,
)
```

---

## Migration Path: From Pipeline to Agent

### Phase 1: Coexistence (No Breaking Changes)
- Existing `main.go` state machine continues to run unchanged
- New `AgenticOrchestrator` initialized alongside
- Feature flag `AGENTIC_MODE` defaults to `false`
- When `AGENTIC_MODE=false`: 100% legacy behavior
- When `AGENTIC_MODE=true`: orchestrator takes over, but all existing code paths remain functional

### Phase 2: Gradual Handover
- Wake word detection still uses existing KWS → triggers both legacy pipeline AND agentic loop
- LLM responses routed through new abstraction layer but still call existing `generateOllamaResponse`
- Actions still executed through existing `ActionExecutor` but now also logged to memory system
- TTS still uses existing `tts.Generate()` but now with optional emotion-aware parameter injection

### Phase 3: Full Agentic Mode
- Legacy pipeline becomes "fast path" for simple commands
- Agentic loop handles complex multi-step goals
- Both paths share: audio capture, TTS playback, tool registry
- User can toggle between modes via voice command: "Mai, switch to assistant mode" / "Mai, switch to agent mode"

### Backward Compatibility Guarantee

```go
// cmd/mai/main.go — minimal change to existing entry point
func main() {
    // ... existing initialization ...
    
    // NEW: Initialize agentic components (additive, not replacing)
    if cfg.Agentic.Enabled {
        agenticOrchestrator := orchestrator.New(cfg)
        go agenticOrchestrator.Start(ctx)
        defer agenticOrchestrator.Stop()
    }
    
    // EXISTING: Original pipeline continues unchanged
    runLegacyPipeline(cfg, spotter, vadDetector, ...)
}
```

---

## Summary: The Evolved Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                    EXISTING MAI (PRESERVED)                     │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │  KWS     │  │   VAD    │  │   ASR    │  │   TTS    │        │
│  │  (sherpa)│  │ (Silero) │  │ (sherpa) │  │ (sherpa) │        │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                      │
│  │  Audio   │  │Automation│  │  Vision  │                      │
│  │ (malgo)  │  │(robotgo) │  │ (Ollama) │                      │
│  └──────────┘  └──────────┘  └──────────┘                      │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼ (Wrapped by Adapters)
┌─────────────────────────────────────────────────────────────────┐
│                 NEW AGENTIC LAYERS (ADDED)                      │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐        │
│  │Perception│  │Cognition │  │  Memory  │  │   Tools  │        │
│  │ (Fusion) │  │(ReAct+CoT│  │ (RAG)    │  │(Registry)│        │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘        │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐                      │
│  │Orchestr. │  │Personality│  │  Events  │                      │
│  │(BDI Loop)│  │(Emotion) │  │  (Bus)   │                      │
│  └──────────┘  └──────────┘  └──────────┘                      │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

**The Result**: Mai becomes a JARVIS/FRIDAY-class agent while every original line of code remains functional, tested, and maintainable.

---

*This architecture document is a living specification. As components are implemented, this document should be updated with links to implementation PRs and performance benchmarks.*
