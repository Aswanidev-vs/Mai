# Mai → JARVIS/FRIDAY: Strategic Roadmap for High-Autonomy Multi-Modal AI Agent

## 0. Executive Summary: The North Star

> *"I am not a program. I am a companion."* — JARVIS

**Mai** is currently a reactive, offline voice assistant: wake word → speech recognition → LLM query → text-to-speech. It is competent but **stateless, reactive, and narrow**. This roadmap defines the architectural evolution required to transform Mai into a **high-autonomy, multi-modal artificial intelligence agent** comparable to **JARVIS** (Just A Rather Very Intelligent System) or **FRIDAY** from the Iron Man cinematic universe.

### What JARVIS/FRIDAY Represents Technically

| Cinematic Capability | Technical Equivalent | Current Mai | Target |
|---------------------|---------------------|-------------|--------|
| **Ambient Awareness** | Continuous multi-modal sensory fusion with attention filtering | ❌ 10s follow-up window | ✅ 24/7 environmental monitoring |
| **Proactive Interruption** | Predictive modeling + preemptive alerting | ❌ None | ✅ "Sir, you have a meeting in 10 minutes and traffic is heavy" |
| **Complex Task Autonomy** | Hierarchical planning + autonomous decomposition + error recovery | ❌ Regex → single action | ✅ "Prep the lab for the experiment" → 47 sub-tasks |
| **Perfect Memory** | Persistent long-term memory + episodic recall + relationship modeling | ❌ Stateless | ✅ "You mentioned this concern three years ago" |
| **Emotional Intelligence** | Tone detection + emotional prosody + adaptive personality | ❌ Static system prompt | ✅ "You sound stressed. Shall I reschedule?" |
| **Universal Interface** | Tool registry + autonomous API discovery + hardware control | ❌ 20 regex rules for 10 apps | ✅ "Interface with any system" |
| **Self-Improvement** | Meta-cognition + performance monitoring + strategy refinement | ❌ None | ✅ "I've noticed this approach fails 30% of the time; I'm trying an alternative" |

### Current Architecture at a Glance

```
┌─────────────┐     ┌─────────┐     ┌──────────┐     ┌─────────┐
│  Microphone │────▶│   VAD   │────▶│   KWS    │────▶│   ASR   │
└─────────────┘     └─────────┘     └──────────┘     └────┬────┘
                                                          │
                    ┌─────────────────────────────────────┘
                    ▼
            ┌─────────────────┐
            │  Regex Parser   │◄──── Optional bypass to LLM
            │  (20 hardcoded  │
            │   regex rules)  │
            └────────┬────────┘
                     │
        ┌────────────┼────────────┐
        ▼            ▼            ▼
   ┌─────────┐ ┌──────────┐ ┌──────────┐
   │ Action  │ │  Ollama  │ │   TTS    │
   │ Executor│ │  (HTTP)  │ │(Blocking)│
   │(robotgo)│ │(Blocking)│ │          │
   └─────────┘ └──────────┘ └──────────┘
```

**Critical Flaw**: This is a **pipeline**, not an **agent**. Data flows in one direction. There is no feedback loop, no planning layer, no memory persistence, and no proactive behavior.

---

## 1. Gap Analysis: Current Deficiencies vs. JARVIS/FRIDAY Benchmark

### 1.1 Reasoning Deficits — Severity: CRITICAL

| Deficiency | Evidence in Code | JARVIS Equivalent | Impact |
|------------|------------------|-------------------|--------|
| **No reasoning layer** | `main.go` (~line 420): ASR text goes directly to `generateOllamaResponse()` or `ParseAndExecute()` | JARVIS plans, validates assumptions, considers alternatives | Cannot handle ambiguity or multi-step goals |
| **No Chain-of-Thought** | LLM prompt is raw user text + static system prompt | JARVIS thinks step-by-step aloud | LLM produces surface-level answers without structured reasoning |
| **No ReAct loop** | No interleaving of Thought → Action → Observation | JARVIS reasons, acts, observes, reasons again | Cannot recover from action failure or adapt mid-task |
| **No task decomposition** | `actions.go`: Single regex match → single action execution | "Prep the suit" → 15 sub-tasks | Cannot handle compound requests ("Open Chrome and search for...") |
| **Static prompt engineering** | `config.yaml`: Single `system_prompt` string | JARVIS adapts tone, depth, and format to context | One-size-fits-all responses |

**Code Reference — The Missing Reasoning Layer:**
```go
// cmd/mai/main.go (~line 420)
// Current: Direct passthrough — no reasoning, no planning, no validation
response, err = generateOllamaResponse(cfg, task.Text)

// cmd/mai/actions.go (~line 350)
// Current: Regex match → immediate execution — no ambiguity resolution
action := e.parser.Parse(text)
feedback, err := e.Execute(action)
```

### 1.2 Long-Term Memory Deficits — Severity: CRITICAL

| Deficiency | Evidence | JARVIS Equivalent | Impact |
|------------|----------|-------------------|--------|
| **Zero persistent storage** | No database imports; no file writes for memory | "I remember everything, sir" | Cannot learn user preferences, habits, or history |
| **No session continuity** | `sessionText` and `sessionSamples` are local variables reset every utterance | Continuous conversational thread | User must restate context constantly |
| **No memory hierarchy** | PRD mentions SQLite + Markdown; zero implementation | Working, episodic, semantic, procedural memory | No distinction between "what happened" and "what matters" |
| **No RAG infrastructure** | No vector DB, no embedding model, no retrieval logic | Instant recall from years of logs | Cannot answer "What did we discuss last Tuesday?" |
| **No user modeling** | No user profile, no preference learning | "I know you prefer your coffee black" | Generic interactions; no personalization |

**Code Reference — Stateless by Design:**
```go
// cmd/mai/main.go (~line 380)
// sessionText is a local variable — dies with the utterance
var sessionText string
var sessionSamples []float32
// These are reset on every state transition:
sessionText = "" // Reset session text
```

### 1.3 Proactive Agency Deficits — Severity: CRITICAL

| Deficiency | Evidence | JARVIS Equivalent | Impact |
|------------|----------|-------------------|--------|
| **Purely reactive** | State machine only transitions on wake word or 10s follow-up | JARVIS initiates contact | System never says "Sir, you should know..." |
| **No goal system** | No goal representation, no priority queue, no scheduler | Autonomous goal management | Cannot manage long-running tasks |
| **No environmental monitoring** | No sensor inputs beyond microphone | Suits, house, lab monitoring | No awareness of user state or environment |
| **No predictive modeling** | No pattern analysis, no time-series forecasting | "Based on your calendar and traffic..." | Cannot anticipate needs |
| **No interrupt hierarchy** | `isSpeaking` boolean is the only interrupt logic | Context-aware interruption | Cannot distinguish "urgent alert" from "new request" |

**Code Reference — Reactive State Machine:**
```go
// cmd/mai/main.go (~line 390)
const (
    StateWakeWord State = iota
    StateListening
)
// Only two states. No "Monitoring", "Planning", "Executing", "Alerting" states.
```

### 1.4 Multi-Modal Integration Deficits — Severity: HIGH

| Deficiency | Evidence | JARVIS Equivalent | Impact |
|------------|----------|-------------------|--------|
| **Siloed modalities** | `vision.go` is standalone; never called from main pipeline except via automation hack | Unified sensory cortex | Vision and audio never inform each other |
| **No continuous vision** | Vision only triggered by `PlayMedia` → `TakeScreenshot` → `FindElement` | Constant environmental awareness | Cannot detect "user looks confused" |
| **No emotional STT** | ASR returns text only; no prosody, pitch, or stress analysis | "You sound tired, sir" | Misses emotional context in voice |
| **No emotional TTS** | TTS uses fixed speed/pitch per model | Tone adapts to urgency, empathy, teasing | Responses feel robotic |
| **No sensory fusion** | No cross-modal attention mechanism | "I see smoke and hear an alarm" | Cannot correlate events across senses |
| **Basic vision only** | `FindElement` returns [x, y]; no object detection, no OCR, no scene graph | Full scene understanding with object relationships | Cannot parse complex UIs or physical spaces |

**Code Reference — Isolated Vision:**
```go
// cmd/mai/vision.go
// Vision is a standalone struct NEVER integrated into the cognitive pipeline
func (v *Vision) FindElement(imagePath, description string) (int, int, error) {
    // Hardcoded prompt: "Return only [x, y] coordinates"
    // No scene description, no object relationships, no temporal tracking
}
```

### 1.5 Tool Use & Ecosystem Deficits — Severity: HIGH

| Deficiency | Evidence | JARVIS Equivalent | Impact |
|------------|----------|-------------------|--------|
| **Hardcoded action set** | `actions.go`: 11 ActionTypes as string constants | Universal tool registry | Adding a new capability requires code changes |
| **Regex-based parsing** | ~20 regex patterns in `ActionParser` | Natural language understanding of intent | Fragile; fails on paraphrase or complex commands |
| **No function calling** | LLM output is raw text; never structured JSON with tool calls | "Execute flight protocol alpha" | LLM cannot autonomously invoke capabilities |
| **No tool discovery** | `knownApps` is a hardcoded map | "I've discovered a new device on the network" | Cannot adapt to new software or APIs |
| **No cross-platform abstraction** | `automation.go` is Windows-centric (PowerShell, Win+S, registry paths) | Controls suits, labs, houses globally | Tightly coupled to Windows UI automation |
| **No API framework** | HTTP client only used for Ollama; no generic REST/GraphQL/gRPC client | Interfaces with any external system | Cannot call weather APIs, smart home hubs, etc. |

**Code Reference — Brittle Tool System:**
```go
// cmd/mai/actions.go (~line 50)
// 20 hardcoded regex rules — adding "schedule a meeting" requires new regex
rules: []parseRule{
    {pattern: regexp.MustCompile(`(?i)^(search|find|look\s*up)\s+(.+?)...`), actionType: ActionWebSearch},
    // ... 19 more regexes
}

// cmd/mai/automation.go (~line 55)
// Hardcoded app database — not extensible at runtime
var knownApps = map[string]appInfo{
    "chrome": {exeName: "chrome", windowTitle: "Google Chrome"},
    // ... 20 more apps
}
```

### 1.6 Architecture & Scalability Deficits — Severity: HIGH

| Deficiency | Evidence | JARVIS Equivalent | Impact |
|------------|----------|-------------------|--------|
| **Monolithic design** | Core logic in single `main.go` (~800 lines) | Modular, service-oriented architecture | Cannot scale development or testing |
| **No interfaces** | Direct struct usage; no swappable implementations | Plugin architecture | Cannot swap ASR, LLM, or TTS without code changes |
| **Tight coupling** | `Automation` depends on `Vision` depends on Ollama URL | Loose coupling via message bus | Changes cascade through codebase |
| **No event system** | `workerChan` is a single Go channel | Event-driven architecture with pub/sub | Components cannot communicate asynchronously |
| **No configuration hot-reload** | Config loaded once at startup | Dynamic capability adjustment | Requires restart for any config change |
| **No telemetry/observability** | `log.Printf` only; no metrics, tracing, or structured logging | Self-monitoring with performance reports | Cannot diagnose failures or optimize latency |

### 1.7 Maturity Scorecard

| Capability Domain | Current Maturity (1-10) | JARVIS Target (1-10) | Gap |
|-------------------|------------------------|----------------------|-----|
| Speech Recognition (ASR) | 7 | 8 | Small |
| Text-to-Speech (TTS) | 6 | 9 | Medium |
| Wake Word Detection | 6 | 7 | Small |
| Natural Language Understanding | 3 | 9 | **Critical** |
| Reasoning & Planning | 1 | 10 | **Severe** |
| Long-Term Memory | 0 | 10 | **Severe** |
| Multi-Modal Fusion | 2 | 9 | **Critical** |
| Tool Use & API Control | 3 | 10 | **Critical** |
| Proactive Agency | 0 | 10 | **Severe** |
| Emotional Intelligence | 0 | 8 | **Critical** |
| Self-Monitoring & Improvement | 0 | 7 | **Severe** |
| **Overall Agent Autonomy** | **2 / 10** | **10 / 10** | **Massive** |

---

## 2. Core Architectural Upgrades: From Reactive Pipeline to Proactive Agent

### 2.1 The Paradigm Shift

Current Mai is a **stimulus-response system**. JARVIS is a **belief-desire-intention (BDI) agent** with continuous environmental embedding.

**Required Transition:**

```
REACTIVE MODEL                    PROACTIVE AGENTIC MODEL
─────────────────                 ───────────────────────
Wake Word ──▶ ASR ──▶ LLM ──▶ TTS    Perception Loop ──▶┐
     ▲                                   │                │
     └───────────────────────────────────┘                │
                                                          ▼
                                                  ┌─────────────┐
                                                  │  Cognitive  │
                                                  │   Engine    │
                                                  │ (ReAct+CoT) │
                                                  └──────┬──────┘
                                                         │
                           ┌─────────────────────────────┼─────────────────────────────┐
                           ▼                             ▼                             ▼
                    ┌─────────────┐              ┌─────────────┐              ┌─────────────┐
                    │   Memory    │              │   Planner   │              │   Action    │
                    │   System    │◄────────────►│  (HTN/LLM)  │◄────────────►│   Executor  │
                    └─────────────┘              └──────┬──────┘              └──────┬──────┘
                                                        │                            │
                           ┌────────────────────────────┘                            │
                           ▼                                                         ▼
                    ┌─────────────┐                                           ┌─────────────┐
                    │   Goal      │                                           │   Tool      │
                    │   Manager   │                                           │   Registry  │
                    └─────────────┘                                           └─────────────┘
```

### 2.2 ReAct (Reasoning and Acting) Integration

**What ReAct Adds:**
ReAct interleaves reasoning traces with action execution, allowing the agent to:
1. **Think** about the current state and goal
2. **Act** by invoking tools or generating output
3. **Observe** the results
4. **Repeat** until the goal is achieved

**Implementation Architecture:**

```go
// pkg/cognition/react.go
type ReActLoop struct {
    LLM          LLMProvider
    Memory       MemoryManager
    Tools        ToolRegistry
    MaxIterations int
}

type ReActStep struct {
    Thought    string            `json:"thought"`
    Action     *ToolCall         `json:"action,omitempty"`
    Observation string           `json:"observation,omitempty"`
    IsComplete bool              `json:"is_complete"`
}

func (r *ReActLoop) Execute(ctx context.Context, goal string) (*ReActResult, error) {
    steps := []ReActStep{}
    
    for i := 0; i < r.MaxIterations; i++ {
        // 1. Build context from memory + previous steps
        context := r.Memory.GetRelevantContext(goal, steps)
        
        // 2. Generate next thought + action
        prompt := buildReActPrompt(goal, context, steps)
        response, err := r.LLM.GenerateStructured(ctx, prompt, ReActStep{})
        if err != nil { return nil, err }
        
        step := parseReActResponse(response)
        steps = append(steps, step)
        
        // 3. If complete, return
        if step.IsComplete {
            return &ReActResult{Steps: steps, FinalAnswer: step.Thought}, nil
        }
        
        // 4. Execute action and observe
        if step.Action != nil {
            observation, err := r.Tools.Execute(ctx, step.Action)
            if err != nil {
                observation = fmt.Sprintf("Error: %v", err)
            }
            steps = append(steps, ReActStep{Observation: observation})
        }
    }
    
    return &ReActResult{Steps: steps, FinalAnswer: "Max iterations reached"}, nil
}
```

**JARVIS Example with ReAct:**
```
User: "Prep the presentation for the board meeting"

[THOUGHT] The user needs a presentation for a board meeting. I should:
1. Find existing presentation templates or recent related work
2. Check the calendar for meeting time and attendees
3. Gather relevant data from recent projects
4. Create or update the presentation file
5. Open it for review
