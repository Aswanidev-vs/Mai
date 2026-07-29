# Mai vs AIRI — Comprehensive Capability Comparison Report

**Date:** 2025-07-28
**Purpose:** Analyze the entire Mai codebase, compare with the AIRI personal companion project (cloned in `refer/airi`), identify capability gaps, and suggest implementation paths.

---

## Executive Summary

**Mai** is a Go-based, privacy-first, fully offline JARVIS-class AI companion with a 3D VRM character, sophisticated voice pipeline, and hierarchical memory system. **AIRI** is a TypeScript monorepo that recreates a Neuro-sama-like AI virtual character with multi-platform support (web, desktop, mobile), 30+ LLM providers, game integrations, and a rich plugin architecture.

| Dimension | Mai | AIRI |
|-----------|-----|------|
| Language | Go | TypeScript |
| Architecture | Monolithic binary | Monorepo (pnpm + Turborepo) |
| Platforms | Windows (primary) | Web, Desktop (Electron), Mobile (Capacitor) |
| Avatar | 3D VRM (Three.js) | 3D VRM + Live2D + Spine + Godot |
| Offline | 100% local inference | Partial (local TTS via kokoro-js) |
| LLM Providers | 8 (Ollama, OpenAI, Gemini, Claude, OpenRouter, NVIDIA, llama.cpp, Hybrid) | 30+ providers via xsAI |
| Maturity | Active development (Phase 18/18) | Production-grade, deployed |

---

## 1. Capability Matrix — What Each Project Has

### 1.1 Voice Pipeline

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **Wake Word Detection** | Yes (Zipformer KWS) | No | Mai has dedicated wake word model |
| **VAD (Voice Activity Detection)** | Yes (Silero ONNX) | Yes (VAD-Web) | Both have VAD |
| **ASR (Speech Recognition)** | Yes — Qwen3 0.6B, NeMo CTC, Zipformer (all offline) | Yes — Web Speech API, Aliyun NLS, OpenAI-compatible | Mai is fully offline; AIRI is cloud-dependent for ASR |
| **TTS (Text-to-Speech)** | Yes — Supertonic 3, Pocket TTS, ZipVoice, Kokoro (all offline) | Yes — ElevenLabs, Azure, OpenAI, Alibaba, Kokoro (local) | Mai fully offline; AIRI has local kokoro-js option |
| **Voice Cloning** | Yes (zero-shot, 3-10s samples) | No | Mai advantage |
| **Acoustic Echo Cancellation** | Yes (NLMS adaptive filter) | No | Mai advantage |
| **Barge-in (interrupt while speaking)** | Yes (VAD-confirmed 4-frame sustained) | No | Mai advantage |
| **Streaming TTS** | Yes (word-level viseme scheduling) | Yes (streaming speech generation) | Both support streaming |
| **Lip-Sync** | Yes (VRM viseme + RMS amplitude) | Yes (wlipsync + Live2D) | Both support lip-sync |
| **Emotion-Adaptive TTS** | Yes (speed/pitch/volume/emphasis per emotion) | Partial (SSML with pitch/rate) | Mai has richer emotion-to-voice mapping |
| **Multi-Language** | Partial (Kokoro multi-lang) | Yes (Crowdin i18n, multi-language docs) | AIRI has better i18n infrastructure |
| **Audio Processing** | miniaudio/malgo, PCM conversion, ring buffers | Web Audio API, AudioWorklet, libsamplerate | Different approaches per platform |

### 1.2 Avatar & Visual

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **3D VRM Avatars** | Yes (Three.js + @pixiv/three-vrm) | Yes (Three.js + @tresjs + @pixiv/three-vrm) | Both support VRM |
| **Live2D Avatars** | No | Yes (Pixi.js + pixi-live2d-display) | AIRI advantage |
| **Spine 2D Animation** | No | Yes (Spine WebGL 4.0/4.1) | AIRI advantage |
| **Godot Native 3D** | No | Yes (Godot 4.6 C# with MToon shader) | AIRI advantage |
| **Auto-Blink** | Yes | Yes | Both |
| **Eye Tracking** | Yes (mouse-based) | Yes (auto-look-at) | Both |
| **Head Physics** | Yes (spring-damper, stiffness=120, damping=16) | Yes | Both have physics |
| **Breathing Animation** | Yes (0.75Hz) | Yes | Both |
| **Idle Behaviors** | Yes (stretch, glance, adjust, breatheDeep, headNod, shoulderShrug) | Yes | Both |
| **Micro-Expressions** | Yes | Partial | Mai advantage |
| **Spontaneous Smiling** | Yes | No explicit | Mai advantage |
| **Motion Capture** | No | Yes (MediaPipe, experimental) | AIRI advantage |
| **Image Generation** | No | Yes (ComfyUI workflows) | AIRI advantage |
| **Background Rendering** | Yes (PixiJS 2D with dust particles) | Yes (various) | Both |

### 1.3 AI Cognition & Memory

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **ReAct Reasoning Loop** | Yes (thought→action→observe, anti-hallucination, 30s timeout) | Yes (core-agent orchestration) | Both |
| **Function Calling** | Yes (structured JSON tool calls) | Yes (MCP, tool use) | Both |
| **Task Planner** | Yes (LLM-based decomposition with dependency tracking) | Partial (response categorization) | Mai has explicit planner |
| **Prompt Engine** | Yes (8 task types: conversation, command, reasoning, creative, analysis, proactive, greeting, emergency) | Yes (character pipeline with segmentation, emotion, delay) | Different approaches |
| **Verifier** | Yes (claim and tool call result verification) | No explicit verifier | Mai advantage |
| **Smart Routing** | Yes (regex fast path → function calling → ReAct → planner → conversation) | Yes (chat orchestrator with response categorization) | Mai has more explicit routing |
| **Self-Correction** | Yes (reflexion on tool failure, strategy analysis) | No explicit | Mai advantage |
| **Meta-Cognition** | Yes (self-improvement loop every 10min) | No explicit | Mai advantage |
| **Working Memory** | Yes (ring buffer, 10 entries) | Yes (in-memory) | Both |
| **Episodic Memory** | Yes (SQLite with LIKE queries) | Yes (DuckDB WASM, PGLite) | Both, different backends |
| **Semantic Memory** | Yes (JSON vector store, cosine similarity) | Yes (pgvector) | AIRI has proper vector DB |
| **Procedural Memory** | Yes (skill/tool usage patterns with success/failure tracking) | No explicit | Mai advantage |
| **RAG Pipeline** | Yes (semantic + episodic → LLM, confidence scoring) | Yes (memory Alaya, WIP) | Mai is more mature |
| **User Profile** | Yes (preferences, habits, frequent apps, topics) | Partial (character context) | Mai has richer user modeling |
| **Session Continuity** | Yes (restores last 20 entries on startup) | Yes (server-side chat sync) | Both |

### 1.4 Tools & Automation

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **Tool Registry** | Yes (9 adapters, categories, semantic search, runtime registration) | Yes (plugin system) | Different architectures |
| **Shell Execution** | Yes | Yes (MCP) | Both |
| **UI Automation** | Yes (RobotGo — click, type, key press, scroll, screenshot) | Yes (MCP computer-use, Playwright) | AIRI is more modern (Playwright) |
| **App Launch** | Yes (6-strategy cascade with fuzzy matching) | No explicit | Mai advantage |
| **Web Search** | Yes (browser search + deep research) | Yes (via tools) | Both |
| **File Operations** | Yes (file_write) | Yes (via tools) | Both |
| **Messaging** | Yes (WhatsApp, Telegram, Discord via UI automation) | Yes (Discord, Telegram, Bilibili, Satori — native bots) | AIRI has native integrations |
| **YouTube** | Yes (search + click automation) | No explicit | Mai advantage |
| **MCP Client** | Yes (external tool discovery) | Yes (MCP SDK) | Both |
| **Home Assistant** | No | Yes (smart home plugin) | AIRI advantage |
| **Game Playing** | No | Yes (Minecraft, Factorio, Chess, Kerbal Space Program) | AIRI advantage |
| **Twitter/X** | No | Yes (via MCP + Playwright) | AIRI advantage |

### 1.5 Platform & Deployment

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **Windows Desktop** | Yes (primary) | Yes (Electron) | Both |
| **macOS** | Partial (fallback paths) | Yes (Electron, Homebrew) | AIRI better |
| **Linux** | Partial (fallback paths) | Yes (Electron, Nix) | AIRI better |
| **Web App** | Yes (companion UI) | Yes (PWA with service worker) | Both |
| **Mobile (iOS/Android)** | No | Yes (Capacitor + native code) | AIRI advantage |
| **Browser Extension** | No | Yes (WXT framework) | AIRI advantage |
| **VS Code Extension** | No | Yes (editor context bridging) | AIRI advantage |
| **Docker** | No | Yes (.dockerignore, deployable) | AIRI advantage |
| **Cloud Deploy** | No | Yes (Railway, Cloudflare) | AIRI advantage |

### 1.6 Plugin & Extension System

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **Skills System** | Yes (JSON manifest, trigger matching, ReAct-backed runner) | Yes (skills-lock.json, 8 locked skills from GitHub) | Different approaches |
| **Plugin Architecture** | No (static tool registry) | Yes (plugin-sdk, plugin-host with DI) | AIRI has proper plugin system |
| **Runtime Registration** | Yes (tools can register/unregister at runtime) | Yes (injeca DI) | Both support dynamic registration |
| **External Skills** | No (3 hardcoded starters) | Yes (8 GitHub-sourced skills) | AIRI advantage |
| **Hook System** | No | Yes (lifecycle hooks) | AIRI advantage |

### 1.7 Observability & DevOps

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **Performance Metrics** | Yes (internal/observability) | Yes (OpenTelemetry) | AIRI is more comprehensive |
| **Logging** | Basic | Yes (structured, OTEL) | AIRI better |
| **Tracing** | No | Yes (OpenTelemetry + Grafana) | AIRI advantage |
| **Analytics** | No | Yes (PostHog, Langfuse) | AIRI advantage |
| **CI/CD** | GitHub Actions (basic) | GitHub Actions + Turborepo | AIRI more mature |
| **Testing** | No tests found | Yes (Vitest — unit, browser, coverage) | AIRI advantage |
| **Linting** | go vet | ESLint + oxlint | Both |
| **Type Safety** | Go (inherent) | Strict TypeScript (no `any`) | Both good |

### 1.8 Privacy & Security

| Feature | Mai | AIRI | Notes |
|---------|:---:|:----:|-------|
| **Fully Offline Mode** | Yes (100% local inference) | Partial (local TTS only) | Mai advantage |
| **Hybrid Mode** | Yes (sensitive → local, non-sensitive → cloud) | No | Mai advantage |
| **PrivacyGuard** | Yes (sensitive words + PII detection) | No | Mai advantage |
| **Local Embeddings** | Yes (always local) | No (cloud-dependent) | Mai advantage |
| **Data Sovereignty** | All data on local disk | Server-side (PostgreSQL + Redis) | Mai advantage |

---

## 2. Capability Gap Analysis — What AIRI Has That Mai Doesn't

### 2.1 Critical Gaps (High Impact)

| Gap | AIRI Feature | Impact on Mai | Priority |
|-----|-------------|---------------|----------|
| **Multi-Platform Support** | Web + Desktop (Electron) + Mobile (Capacitor) | Mai is Windows-primary only | HIGH |
| **Plugin Architecture** | plugin-sdk with DI container, lifecycle hooks, protocol | Mai has static tool registry, no true plugin system | HIGH |
| **Live2D Avatar Support** | Pixi.js + pixi-live2d-display | Mai only supports VRM 3D avatars | HIGH |
| **Native Chat Platform Bots** | Discord, Telegram, Bilibili, Satori — full native bots | Mai only has UI automation for WhatsApp/Telegram/Discord | HIGH |
| **Game Integrations** | Minecraft, Factorio, Chess, Kerbal Space Program | Mai has no game integrations | MEDIUM |
| **Smart Home Control** | Home Assistant plugin | Mai has no IoT integration | MEDIUM |
| **Testing Framework** | Vitest with unit, browser, and coverage testing | Mai has zero tests | HIGH |
| **Observability Stack** | OpenTelemetry, Grafana, Langfuse, PostHog | Mai has basic metrics only | MEDIUM |
| **Mobile App** | Capacitor with native Kotlin/Swift | Mai has no mobile presence | MEDIUM |
| **Browser Extension** | WXT-based Chrome/Firefox extension | Mai has no browser integration | MEDIUM |
| **VS Code Integration** | Editor context bridging to AI | Mai has no IDE integration | LOW |
| **Proper Vector Database** | pgvector for semantic memory | Mai uses JSON files for vectors | MEDIUM |
| **Image Generation** | ComfyUI workflow integration | Mai has no image generation | LOW |
| **Motion Capture** | MediaPipe-based (experimental) | Mai has no motion capture | LOW |
| **Twitter/Social Media** | MCP + Playwright integration | Mai has no social media integration | LOW |
| **Computer Use** | macOS desktop orchestration via MCP | Mai has RobotGo but less sophisticated | LOW |
| **i18n Infrastructure** | Crowdin, multi-language READMEs, vue-i18n | Mai has no i18n | LOW |

### 2.2 What Mai Has That AIRI Doesn't

| Mai Feature | Description | Why It Matters |
|-------------|-------------|----------------|
| **100% Offline Operation** | All speech, LLM, TTS run locally | Privacy, no internet dependency |
| **Acoustic Echo Cancellation** | NLMS adaptive filter | Clean voice interaction in any room |
| **Barge-in with VAD Confirmation** | Interrupt while speaking with 4-frame sustained check | Natural conversation flow |
| **Wake Word Detection** | Zipformer KWS dedicated model | Hands-free activation |
| **Voice Cloning** | Zero-shot with 3-10s samples | Personalized voice output |
| **Emotion-Adaptive TTS** | 6 voice styles, prosody-aware parameter scaling | Emotionally intelligent speech |
| **Task Planner** | LLM-based decomposition with dependency tracking | Multi-step task execution |
| **Verifier** | Claim and tool call result verification | Trustworthy AI responses |
| **Meta-Cognition** | Self-improvement loop analyzing strategy performance | Self-evolving agent |
| **Procedural Memory** | Tracks skill/tool success rates | Learns what works |
| **Hybrid Privacy Mode** | Routes sensitive data locally, non-sensitive to cloud | Best of both worlds |
| **6-Strategy App Launch** | Protocol URI → AppID → Direct exe → PATH → Windows Search → Web fallback | Reliable desktop automation |
| **Fuzzy Matching for ASR Errors** | Phonetic alias correction for misrecognized words | Handles real-world speech |
| **Regex Fast Path** | Sub-millisecond command routing without LLM | Fast responses for common commands |
| **Spring-Damper Head Physics** | Stiffness=120, damping=16 with CPT eye saccades | More lifelike avatar |

---

## 3. Implementation Roadmap for Mai

### Phase 1: Foundation (Weeks 1-4)

#### 3.1 Testing Infrastructure
**Gap:** Zero tests in Mai.
**Implementation:**
- Add `go test` framework with table-driven tests
- Unit tests for: memory system, emotion detector, tool registry, skill runner, prompt engine
- Integration tests for: LLM providers, ASR pipeline, TTS pipeline
- Target: 60% code coverage minimum
- Files to create: `*_test.go` alongside each source file

#### 3.2 Plugin Architecture
**Gap:** Static tool registry vs AIRI's plugin-sdk with DI.
**Implementation:**
```
internal/plugins/
    registry.go      -- Plugin lifecycle management
    loader.go        -- Dynamic .so/.dll loading or Go plugin interface
    hooks.go         -- Pre/post hooks for tool execution
    protocol.go      -- Plugin protocol definitions
```
- Define `Plugin` interface with `Init()`, `Start()`, `Stop()`, `OnToolCall()`, `OnEvent()`
- Support Go plugins (`plugin` package) for native extensions
- Add plugin manifest format (similar to AIRI's `plugin.json`)
- Implement hot-reload via file watcher

#### 3.3 Observability Stack
**Gap:** Basic metrics vs AIRI's OpenTelemetry.
**Implementation:**
- Add OpenTelemetry Go SDK (`go.opentelemetry.io/otel`)
- Instrument: LLM calls, ASR processing, TTS generation, tool executions, memory operations
- Export traces to Jaeger or Grafana Tempo
- Export metrics to Prometheus
- Add structured logging with `slog` or `zerolog`

### Phase 2: Platform Expansion (Weeks 5-8)

#### 3.4 Multi-Platform Architecture
**Gap:** Windows-primary vs AIRI's Web + Desktop + Mobile.
**Implementation:**
- **Web Companion:** Already has WebSocket server — extend to full PWA with service worker, offline caching
- **Electron Wrapper:** Use `webview` or `lorca` to wrap existing companion UI as native desktop app
- **Mobile (Future):** Flutter or React Native wrapper around core API via gRPC

#### 3.5 Live2D Avatar Support
**Gap:** VRM only vs AIRI's multiple avatar formats.
**Implementation:**
```
internal/avatar/
    vrm.go           -- Existing VRM support
    live2d.go        -- Live2D Cubism SDK integration
    manager.go       -- Format detection and switching
```
- Integrate Live2D Cubism SDK (C bindings similar to sherpa-onnx)
- Add Live2D model loading, parameter control, lip-sync
- Add avatar format selection in config
- Provide fallback: Live2D for low-end devices, VRM for high-end

#### 3.6 Native Chat Platform Bots
**Gap:** UI automation only vs AIRI's native Discord/Telegram bots.
**Implementation:**
```
internal/integrations/
    discord/
        bot.go       -- Discord bot with voice + text
        voice.go     -- Voice channel audio streaming
    telegram/
        bot.go       -- Telegram bot with ASR/TTS
```
- Discord: Use `discordgo` library, connect to voice channels, stream ASR/TTS
- Telegram: Use `telebot` or `gotd/tdlib`, handle voice messages, respond with TTS
- Share the same event bus and memory system as the main agent
- Add per-platform config sections

### Phase 3: Intelligence Enhancement (Weeks 9-12)

#### 3.7 Proper Vector Database
**Gap:** JSON file vectors vs AIRI's pgvector.
**Implementation:**
- Option A: Embed SQLite vector extension (`sqlite-vec`)
- Option B: Use `pgvector` with embedded PostgreSQL (`pglite` style)
- Option C: Use Milvus Lite (embedded vector DB)
- Recommendation: `sqlite-vec` for simplicity (stays in Go ecosystem, no external DB)
- Migrate existing JSON vectors to vector DB
- Add HNSW indexing for faster similarity search

#### 3.8 Game Integrations
**Gap:** No game support vs AIRI's Minecraft/Factorio/Chess.
**Implementation:**
```
internal/tools/games/
    minecraft.go     -- mineflayer-style Go client or MCP bridge
    chess.go         -- Stockfish integration via UCI protocol
```
- **Chess:** Integrate Stockfish (UCI protocol via stdin/stdout), add LLM player for moves
- **Minecraft:** Use `go-minecraft` or bridge to AIRI's MCP service
- Add game state to memory system for context

#### 3.9 Smart Home Integration
**Gap:** No IoT vs AIRI's Home Assistant plugin.
**Implementation:**
```
internal/tools/smarthome/
    homeassistant.go -- REST API + WebSocket client
    discovery.go     -- mDNS/SSDP device discovery
```
- Implement Home Assistant REST API client
- Add device discovery and control tools
- Voice commands: "turn on living room lights", "set thermostat to 72"
- Add to tool registry with `smarthome` category

### Phase 4: Ecosystem (Weeks 13-16)

#### 3.10 Browser Extension
**Gap:** No browser integration vs AIRI's WXT extension.
**Implementation:**
- Create Chrome extension with content script, background service worker
- Capture: current page URL, selected text, page content summary
- Send to Mai via WebSocket or HTTP API
- Enable commands: "summarize this page", "add to my notes", "search for this"

#### 3.11 VS Code Extension
**Gap:** No IDE integration vs AIRI's editor context bridging.
**Implementation:**
- VS Code extension that captures: active file, selection, errors/warnings, terminal output
- Send context to Mai server for code assistance
- Enable: "explain this error", "refactor this function", "write tests for this"

#### 3.12 Image Generation
**Gap:** No image creation vs AIRI's ComfyUI workflows.
**Implementation:**
- Add ComfyUI client (HTTP API to local ComfyUI server)
- Add `image_generate` tool with prompt, style, size parameters
- Store generated images in `data/generated/`
- Return image URL/path to user

---

## 4. Technical Architecture Recommendations

### 4.1 Monorepo Migration (Optional, Long-term)
AIRI uses pnpm + Turborepo. For Mai (Go), consider:
- Keep Go as core engine
- Add `web/` directory for Vue/Svelte companion UI (instead of embedded static files)
- Add `integrations/` for platform-specific code (Discord, Telegram, etc.)
- Use Go workspace (`go.work`) to manage modules

### 4.2 Event Bus Enhancement
Current Mai event bus is in-process. For multi-platform:
- Add NATS or Redis Pub/Sub backend option
- Enable cross-process communication (Electron ↔ Go core)
- Add event persistence for replay

### 4.3 API Layer
For mobile/web clients:
- Add REST API or gRPC alongside WebSocket
- OpenAPI spec for client generation
- JWT authentication (AIRI uses Better Auth)

---

## 5. Implementation Priority Matrix

| Priority | Task | Effort | Impact | AIRI Reference |
|:--------:|------|--------|--------|----------------|
| **P0** | Testing infrastructure | 2 weeks | High (prevents regressions) | vitest.config.ts |
| **P0** | Plugin architecture | 3 weeks | High (extensibility) | packages/plugin-sdk/ |
| **P1** | Observability (OTEL) | 2 weeks | Medium (debugging, monitoring) | apps/server observability |
| **P1** | Native Discord/Telegram bots | 3 weeks | High (reach, usability) | services/discord-bot, telegram-bot |
| **P1** | Proper vector DB | 1 week | Medium (memory quality) | packages/memory-pgvector |
| **P2** | Live2D support | 2 weeks | Medium (avatar variety) | packages/stage-ui-live2d |
| **P2** | Web PWA enhancement | 2 weeks | Medium (accessibility) | apps/stage-web |
| **P2** | Game integrations (Chess) | 2 weeks | Low-Medium (fun factor) | plugins/airi-plugin-game-chess |
| **P3** | Smart home integration | 2 weeks | Low-Medium (IoT use case) | plugins/airi-plugin-homeassistant |
| **P3** | Browser extension | 2 weeks | Low (context gathering) | airi-plugin-web-extension |
| **P3** | VS Code extension | 1 week | Low (developer workflow) | integrations/vscode-airi |
| **P3** | Image generation | 1 week | Low (creative output) | core-character artistry |

---

## 6. Summary

### Mai's Unique Strengths (Keep & Enhance)
1. **100% offline operation** — no other project matches this
2. **Production voice pipeline** — AEC, barge-in, wake word, voice cloning
3. **Emotion-adaptive TTS** — 6 voice styles with prosody analysis
4. **Privacy-first design** — hybrid mode, PrivacyGuard, local embeddings
5. **Meta-cognition** — self-improvement loop, procedural memory
6. **Regex fast path** — sub-millisecond responses without LLM

### Critical Gaps to Address
1. **Testing** — zero tests is the #1 risk to reliability
2. **Plugin architecture** — limits extensibility and community contributions
3. **Multi-platform bots** — Discord/Telegram native bots are expected by users
4. **Observability** — essential for debugging production issues
5. **Vector DB** — JSON vectors won't scale

### AIRI's Lessons for Mai
1. **Monorepo structure** enables parallel development and code sharing
2. **Plugin SDK with DI** makes third-party extensions easy
3. **Multi-avatar formats** (VRM + Live2D + Spine) maximize device compatibility
4. **Game integrations** differentiate from generic chatbots
5. **Observability-first** design saves debugging time

---

*Report generated by analyzing Mai codebase (~10,000-12,000 lines Go) and AIRI monorepo (47 packages, 6 apps, TypeScript).*
