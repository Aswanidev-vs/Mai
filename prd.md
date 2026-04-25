Here’s your **complete, updated PRD** with everything integrated cleanly:

* ✅ Offline LLM (Ollama + llama.cpp)
* ✅ Web search (optional)
* ✅ Vision (on-demand only)
* ✅ Automation (RoboGo + messaging)
* ✅ Emotion system (Mai personality)
* ✅ **Proper memory system (SQLite + Markdown/Obsidian)**

---

# 📄 Product Requirements Document (PRD)

## Project: **Mai – Offline AI Assistant with Context-Aware Automation**

---

# 1. 🧠 Overview

**Mai** is a fully offline, voice-driven AI assistant built in Go that:

* Uses local LLMs for reasoning and conversation
* Maintains a calm, grounded, emotionally adaptive personality
* Executes system and UI-level automation
* Supports messaging via UI simulation
* Uses vision only when required
* Supports optional real-time web search
* Maintains both short-term and long-term memory

---

# 2. 🎯 Goals

### Primary

* Fully offline conversational AI
* Real-time voice interaction
* Reliable automation (apps, typing, messaging)
* Natural, non-cringe personality

### Secondary

* Context-aware screen understanding
* Multi-LLM backend support
* Persistent memory system
* Optional real-time web knowledge

---

# 3. 🚫 Non-Goals

* No mandatory internet dependency
* No continuous OCR/vision processing
* No exaggerated assistant personality
* No blind or unsafe automation
* No dependency on external UI frameworks

---

# 4. 🧩 Core Features

---

## 4.1 🎤 Voice Interaction

### Stack

* ASR + wake word: Sherpa ONNX
* VAD: Silero VAD

### Requirements

* Real-time streaming ASR
* Interruptible interaction
* Low latency (<500ms response start)

---

## 4.2 🧠 LLM Engine (Multi-Provider)

---

### Supported Providers

* Ollama
* llama.cpp

---

### Requirements

* Unified interface abstraction
* Config-based provider switching
* Streaming response support
* Fully offline capability

---

### Interface

```go id="prd_llm"
type LLM interface {
    Generate(prompt string) (string, error)
    Stream(prompt string, cb func(string)) error
}
```

---

## 4.3 ⚙️ Automation System

Using:

* RoboGo

---

### Capabilities

#### 🖥️ System Control

* Open/close applications
* Keyboard/mouse control

#### 💬 Messaging Automation

* Type and send messages
* Works across:

  * WhatsApp Web
  * Telegram Desktop
  * Any text-based UI

---

### Requirements

* UI-based automation (no API dependency)
* Window focus handling
* Reliable typing + submission
* Minimal verbal confirmation

---

## 4.4 🧩 Action System

---

### Schema

```json id="prd_action"
{
  "type": "action_type",
  "confidence": 0.0,
  "requires_vision": false,
  "params": {},
  "meta": {
    "requires_confirmation": false
  }
}
```

---

### Example Actions

```json id="prd_action_ex1"
{
  "type": "system.open_app",
  "params": { "name": "chrome" }
}
```

```json id="prd_action_ex2"
{
  "type": "message.send",
  "params": {
    "app": "whatsapp",
    "text": "Hey, are you free?"
  }
}
```

```json id="prd_action_ex3"
{
  "type": "web.search",
  "params": { "query": "latest AI news" }
}
```

---

### Requirements

* Separate execution from spoken response
* Confidence-based execution
* Safe handling of destructive actions

---

## 4.5 🧠 Command Parser (Fallback Intelligent)

---

### Multi-layer Parsing

1. Rule-based
2. LLM-based
3. Fallback clarification

---

### Requirements

* Handle vague inputs (“this”, “that”)
* Assign confidence scores
* Prevent unsafe execution

---

## 4.6 👁️ Vision System (On-Demand Only)

---

### Components

* Screen capture via RoboGo
* OCR via Tesseract OCR

---

### Trigger Logic

Vision is triggered only when:

* Contextual references are used
* Screen understanding is required

---

### Processing Levels

| Level | Description         |
| ----- | ------------------- |
| None  | No vision           |
| OCR   | Text extraction     |
| Full  | OCR + LLM reasoning |

---

### Requirements

* Lazy execution (post parsing)
* Short-term caching
* No continuous scanning

---

## 4.7 🌐 Web Search (Optional)

---

### Design

* Disabled by default
* Enabled via config or user intent

---

### Trigger Conditions

* “search this”, “latest”, “look up”
* Time-sensitive queries

---

### Pipeline

```txt id="prd_web"
User Query
   ↓
Decision Layer
   ↓
 ├── No → LLM only
 └── Yes → Fetch Web Results
             ↓
        Inject into Prompt
             ↓
        LLM Response
```

---

### Requirements

* Must not break offline mode
* Must be optional
* Must not over-trigger

---

## 4.8 💖 Emotion Engine

---

### Features

* Detect user emotional tone
* Adjust response phrasing
* Map to voice style

---

### Emotion Types

```txt id="prd_emotions"
neutral, caring, teasing, serious, soft
```

---

### Requirements

* Subtle tone shifts
* No exaggerated behavior
* Personality consistency

---

## 4.9 🔊 TTS System

---

### Modes

* Fast (system responses)
* Emotional (Mai personality)

---

### Requirements

* Interruptible playback
* Emotion-aware voice
* Low latency

---

## 4.10 🧠 Memory System (Short-Term + Long-Term)

---

### ⚡ Short-Term Memory

Using:

* SQLite

---

#### Purpose

* Chat history
* Session context
* Action logs

---

#### Requirements

* Fast read/write
* Queryable
* Limited retention

---

---

### 🧠 Long-Term Memory

Using:

* Markdown (`.md`) files
* Viewed via Obsidian

---

#### Important

* No direct Obsidian integration
* System writes `.md` files only

---

#### Structure

```txt id="prd_memory"
data/memory/
  personality.md
  user_profile.md
  memories/
```

---

#### Requirements

* Human-readable
* Linkable (`[[notes]]`)
* Editable outside system

---

---

### 🔄 Memory Flow

```txt id="prd_mem_flow"
Interaction
   ↓
Importance Check
   ↓
 ├── Low → SQLite
 └── High → SQLite + Markdown
```

---

---

### Memory Usage in LLM

* Inject recent context (SQLite)
* Inject user profile (Markdown)

---

# 5. 🏗️ System Architecture

---

## Full Pipeline

```txt id="prd_full"
Mic Input
   ↓
VAD
   ↓
Wake Word
   ↓
Streaming ASR
   ↓
Command Parser
   ↓
Decision Layer
   ├── Vision Needed?
   ├── Web Needed?
   ↓
Context Aggregation
   ├── Screen Context
   ├── Web Results
   ├── Memory Context
   ↓
LLM (Ollama / llama.cpp)
   ↓
Emotion Engine
   ↓
 ├── Action Executor (RoboGo)
 └── TTS Output
```

---

# 6. ⚙️ Technical Stack

---

## LLM

* Ollama
* llama.cpp

---

## Speech

* Sherpa ONNX
* Silero VAD

---

## Automation

* RoboGo

---

## Vision

* Tesseract OCR

---

## Storage

* SQLite
* Markdown files + Obsidian

---

# 7. ⚡ Performance Requirements

* Wake word: <200ms
* ASR: real-time
* LLM response: <2s
* Action execution: <300ms
* Vision (on-demand): <1s
* No unnecessary CPU usage

---

# 8. 🧠 Intelligence & Safety

---

## Confidence Handling

```go id="prd_conf_final"
if action.confidence < 0.7 {
    request_clarification()
}
```

---

## Safety Rules

* No destructive actions without confirmation
* No ambiguous execution
* Context validation required

---

# 9. 💬 Interaction Design

---

## Principles

* Minimal
* Natural
* Quietly capable

---

## Examples

“Open Chrome” → “Alright.”
“Send hello” → “Done.”
“Fix this” → “…you missed something there.”

---

# 10. 🚀 Future Enhancements

---

* Multi-step task planning
* RAG over Markdown memory
* UI object detection (YOLO)
* Multi-user voice recognition
* Cross-device automation

---

# 11. ✅ Success Criteria

* Fully offline capable
* Multi-LLM support works seamlessly
* Vision used only when needed
* Memory improves interaction quality
* Automation works reliably
* Personality feels natural

---

# 🧠 Final Note

This system should feel like:

> Not something you command…
> But something that understands, remembers, and acts quietly alongside you

---

If you want next, I can turn this into:

* 📁 **Complete starter repo (actual working code)**
* 🧪 **Module-by-module implementation roadmap**
* 🧠 **Task planning system (Jarvis-level multi-step reasoning)**
