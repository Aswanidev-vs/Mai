# Implementation Plan: AIRI Companion Features → Mai

## Recommendation: Option B — TypeScript Companion UI + Go Backend via WebSocket

**Why not Go UI (Fyne/Wails)?**
- AIRI's rendering stack (Live2D, VRM/Three.js, Spine) is WebGL-native — reimplementing in Go means losing the entire ecosystem
- Vue/React component libraries for character rendering, chat, settings don't exist in Go
- Wails v2 uses a webview anyway — you'd write HTML/JS/CSS in Go wrappers, gaining nothing
- AIRI's 47 TS packages represent ~50k lines of rendering/UI code — porting is infeasible

**Why hybrid is wrong?**
- Two UI stacks creates double maintenance; pick one

**Why TypeScript frontend + Go backend is right:**
- Mai's Go backend is the differentiator (Sherpa-ONNX voice pipeline, agentic ReAct, offline inference) — keep it
- AIRI's UI layer is mature (Live2D, VRM, chat, settings, platform connectors) — adapt it
- WebSocket is the natural boundary: Mai already uses JSON events internally; expose them over WS
- The TS frontend can be web (browser), Electron (desktop), or Tauri (lighter alternative)
- Mai's event bus already decouples everything — adding a WS bridge is surgical

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────┐
│                  TypeScript Frontend                      │
│  (Vue 3 + Vite + Pinia — adapted from AIRI)             │
│                                                          │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌────────────┐ │
│  │ Live2D/  │ │ Chat     │ │ Settings │ │ Platform   │ │
│  │ VRM      │ │ History  │ │ Panel    │ │ Connectors │ │
│  │ Renderer │ │ Stream   │ │ Config   │ │ (Discord)  │ │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘ └─────┬──────┘ │
│       │             │            │              │         │
│       └─────────────┴────────────┴──────────────┘         │
│                          │                                │
│                   ┌──────┴──────┐                         │
│                   │ WS Client   │                         │
│                   │ (auto-recon)│                         │
│                   └──────┬──────┘                         │
└──────────────────────────┼──────────────────────────────┘
                           │ WebSocket (JSON)
                           │ Port 9800
┌──────────────────────────┼──────────────────────────────┐
│                  Go Backend (Mai)                         │
│                          │                                │
│                   ┌──────┴──────┐                         │
│                   │ WS Server   │  ← NEW                  │
│                   │ hub.go      │                         │
│                   └──────┬──────┘                         │
│                          │                                │
│              ┌───────────┼───────────┐                    │
│              │           │           │                    │
│        ┌─────┴────┐ ┌───┴───┐ ┌────┴─────┐              │
│        │ EventBus │ │ Agent │ │ Memory   │              │
│        │          │ │ Orch. │ │ Manager  │              │
│        └─────┬────┘ └───┬───┘ └──────────┘              │
│              │           │                                │
│        ┌─────┴────┐ ┌───┴───┐ ┌─────────┐              │
│        │Voice Pip.│ │ Tools │ │ Skills  │              │
│        │KWS/VAD/  │ │       │ │         │              │
│        │ASR/TTS   │ │       │ │         │              │
│        └──────────┘ └───────┘ └─────────┘              │
└─────────────────────────────────────────────────────────┘
```

---

## Phase 0: WebSocket Server Foundation (Week 1–2)

### Goal
Add a WebSocket server to Mai's Go backend that bridges the existing event bus to external clients.

### Files to Create

```
internal/server/
├── server.go          # HTTP/WebSocket server entry point
├── hub.go             # Connection hub (broadcast, per-session)
├── handler.go         # WS message router (JSON-RPC style)
├── auth.go            # Simple token-based auth
├── protocol.go        # Message type definitions
└── middleware.go       # Logging, rate limiting
```

### Protocol Design

All messages use JSON-RPC 2.0 over WebSocket:

```jsonc
// Client → Server
{ "jsonrpc": "2.0", "id": 1, "method": "chat.send", "params": { "text": "Hello" } }
{ "jsonrpc": "2.0", "method": "chat.voice", "params": { "audio": "base64..." } }
{ "jsonrpc": "2.0", "method": "state.sync", "params": {} }

// Server → Client (notifications)
{ "jsonrpc": "2.0", "method": "chat.chunk", "params": { "text": "Hi", "stream_id": "abc" } }
{ "jsonrpc": "2.0", "method": "chat.done", "params": { "stream_id": "abc", "emotion": "happy" } }
{ "jsonrpc": "2.0", "method": "state.update", "params": { "emotion": "happy", "tts_active": true } }
{ "jsonrpc": "2.0", "method": "tts.chunk", "params": { "audio": "base64...", "emotion": "happy" } }
```

### Event Types (Client → Server)

| Method | Description |
|--------|-------------|
| `chat.send` | Send text message (replaces voice input) |
| `chat.voice` | Send raw audio for ASR |
| `state.sync` | Request full state snapshot |
| `config.get` | Get current configuration |
| `config.set` | Update configuration |
| `tools.list` | List available tools |
| `tools.execute` | Execute a tool |
| `memory.search` | Search memory |

### Event Types (Server → Client)

| Method | Description |
|--------|-------------|
| `chat.chunk` | Streaming LLM text token |
| `chat.done` | Stream complete |
| `chat.error` | Error in processing |
| `tts.chunk` | Streaming TTS audio data |
| `tts.done` | TTS playback complete |
| `state.update` | State change (emotion, status, etc.) |
| `transcription` | ASR result from voice |
| `emotion.detected` | Emotion analysis result |
| `proactive.hint` | Proactive suggestion |

### Bridge to Event Bus

The WS hub subscribes to Mai's event bus and forwards events:

```go
// In hub.go
func (h *Hub) BridgeEventBus(bus interfaces.EventBus) {
    bus.Subscribe("perception.audio.transcription", func(e interfaces.Event) {
        h.Broadcast(EventNotification{
            Method: "transcription",
            Params: e.Payload,
        })
    })
    bus.Subscribe("action.tts.request", func(e interfaces.Event) {
        h.Broadcast(EventNotification{
            Method: "tts.chunk",
            Params: e.Payload,
        })
    })
    // ... more event mappings
}
```

### Files to Modify

| File | Change |
|------|--------|
| `cmd/mai/main.go` | Start WS server after agentic init |
| `pkg/models/config.go` | Add `Server` config section (port, auth token) |
| `config.yaml` | Add `server:` block |
| `internal/agent/loop.go` | Add WS notification hooks at key pipeline points |

### Config Addition

```yaml
server:
  enabled: true
  port: 9800
  auth_token: ""  # empty = no auth (local only)
  cors_origins:
    - "http://localhost:5173"   # Vite dev server
    - "http://localhost:3000"   # Production build
```

---

## Phase 1: Companion UI Foundation (Week 2–4)

### Goal
A working Vue 3 + Vite web app with chat interface, streaming text, and WebSocket connection to Mai.

### Stack

| Layer | Technology | Why |
|-------|-----------|-----|
| Framework | Vue 3 + Composition API | AIRI uses Vue; reuse patterns |
| State | Pinia | Same as AIRI; WS sync store |
| Build | Vite | Fast HMR, native TS |
| Styling | UnoCSS | AIRI's choice; utility-first |
| WebSocket | `better-ws` client (adapted from AIRI) | Auto-reconnect, heartbeat |
| Desktop | Tauri v2 (optional) | Rust-based, lighter than Electron |

### Files to Create

```
companion/
├── package.json
├── vite.config.ts
├── tsconfig.json
├── uno.config.ts
├── src/
│   ├── main.ts
│   ├── App.vue
│   ├── ws/
│   │   ├── client.ts          # WebSocket client (adapted from AIRI better-ws)
│   │   ├── codec.ts           # JSON-RPC encode/decode
│   │   └── reconnect.ts       # Exponential backoff reconnect
│   ├── stores/
│   │   ├── chat.ts            # Chat messages, streaming state
│   │   ├── ws.ts              # Connection state, auth
│   │   ├── config.ts          # Mai configuration
│   │   ├── emotion.ts         # Current emotion state
│   │   └── tools.ts           # Available tools
│   ├── components/
│   │   ├── ChatPanel.vue      # Message list + input
│   │   ├── MessageBubble.vue  # Individual message
│   │   ├── StreamingText.vue  # Typing indicator + streaming
│   │   ├── StatusBar.vue      # Mai state (listening/thinking/speaking)
│   │   └── SettingsPanel.vue  # Configuration UI
│   └── views/
│       ├── MainView.vue       # Primary companion view
│       └── SettingsView.vue   # Settings page
```

### Key Component: ChatPanel.vue

```vue
<script setup lang="ts">
const chat = useChatStore()
const ws = useWsStore()

function sendMessage(text: string) {
  ws.call('chat.send', { text })
}

// Listen for streaming chunks
ws.on('chat.chunk', (params) => chat.appendChunk(params.stream_id, params.text))
ws.on('chat.done', (params) => chat.finalizeMessage(params.stream_id, params.emotion))
ws.on('transcription', (params) => chat.addUserMessage(params.text))
</script>
```

### Key Store: chat.ts

```typescript
export const useChatStore = defineStore('chat', () => {
  const messages = ref<Message[]>([])
  const streaming = ref<Map<string, string>>(new Map())

  function appendChunk(streamId: string, text: string) {
    const existing = streaming.value.get(streamId) || ''
    streaming.value.set(streamId, existing + text)
  }

  function finalizeMessage(streamId: string, emotion?: string) {
    const text = streaming.value.get(streamId) || ''
    messages.value.push({ role: 'assistant', text, emotion, timestamp: Date.now() })
    streaming.value.delete(streamId)
  }

  return { messages, streaming, appendChunk, finalizeMessage }
})
```

---

## Phase 2: Visual Companion — Live2D (Week 4–8)

### Goal
Add a Live2D character that reacts to speech and emotions.

### Approach
Adapt AIRI's `packages/stage-ui-live2d` — extract the core composables and Vue components into the Mai companion app.

### Files to Create/Adapt

```
companion/src/
├── character/
│   ├── live2d/
│   │   ├── Live2DScene.vue       # From AIRI Live2DScene.vue
│   │   ├── useLive2D.ts          # Core loading/animation
│   │   ├── useExpression.ts      # Emotion → expression mapping
│   │   ├── useLipSync.ts         # TTS audio → mouth movement
│   │   ├── useEyeTracking.ts     # Saccades, idle eye motion
│   │   └── model-loader.ts       # ZIP/OPFS model loading
│   └── types.ts                  # Shared character types
```

### Emotion → Expression Mapping

Mai already detects emotions (keyword-based in `personality/emotion.go`). The WS bridge sends emotion events:

```
Mai detects "happy" → WS emits { method: "emotion.detected", params: { emotion: "happy", intensity: 0.8 } }
                    → Vue store updates → Live2D expression controller → model shows smile
```

### Lip Sync Integration

Mai's TTS streams audio via WS. The frontend can:
1. Receive audio chunks via `tts.chunk`
2. Use Web Audio API to play them
3. Analyze amplitude in real-time for viseme-driven lip sync (simpler approach)
4. OR use `wlipsync` phoneme extraction (AIRI's approach) for precise lip sync

### Dependencies

```json
{
  "pixi-live2d-display": "^0.4.0",
  "pixi.js": "^7.3.0",
  "@anthropic-ai/sdk": "optional"
}
```

---

## Phase 3: VRM 3D Companion (Week 8–12)

### Goal
Support VRM (Three.js) as an alternative to Live2D, enabling full 3D characters.

### Files to Adapt from AIRI

```
companion/src/character/vrm/
├── VRMScene.vue           # Three.js scene
├── useVRM.ts              # Core VRM lifecycle
├── useVRMExpression.ts    # Emotion → VRM blend shapes
├── useVRMLipSync.ts       # Phoneme-based lip sync
├── useVRMAnimation.ts     # Idle/talking animations
└── model-loader.ts        # VRM file loading
```

### Key Adaptations
- AIRI uses `@pixiv/three-vrm` v3.5.2 + `@tresjs/core` — keep these
- Port AIRI's expression system (happy=0.7, sad=0.7, angry=0.7, etc. with cubic easing)
- Port the 5-phoneme lip sync driver (`aa`, `ee`, `ih`, `oh`, `ou`)

---

## Phase 4: Multi-Window Desktop Experience (Week 10–14)

### Goal
Desktop companion overlay + separate chat/settings windows.

### Approach: Tauri v2

```
companion/
├── src-tauri/
│   ├── tauri.conf.json
│   ├── src/
│   │   ├── main.rs           # Tauri app entry
│   │   └── commands.rs       # Tauri IPC commands (if needed)
│   └── capabilities/
│       └── overlay.json      # Overlay window permissions
```

### Window Types (from AIRI's Electron app)

| Window | Purpose |
|--------|---------|
| `main` | Primary companion view (character + chat) |
| `overlay` | Transparent always-on-top character |
| `chat` | Separate chat window |
| `settings` | Configuration panel |
| `caption` | Subtitles for speech |
| `notice` | Notification popups |

### Desktop Overlay Window

```rust
// Tauri: create overlay window
WindowBuilder::new(app, "overlay", WindowUrl::App("/overlay".into()))
    .decorations(false)
    .transparent(true)
    .always_on_top(true)
    .resizable(false)
    .inner_size(400.0, 500.0)
    .build()?;
```

---

## Phase 5: Platform Integrations (Week 12–16)

### Goal
Connect Mai to Discord, Telegram, and other platforms.

### Architecture

Platform bots connect as WS clients to Mai's backend, just like the companion UI:

```
Discord Bot (TS) ──WS──┐
Telegram Bot (TS) ──WS──┤── Mai Go Backend
Companion UI (TS) ──WS──┘
```

### Discord Bot

Adapt from `services/discord-bot` in AIRI:
- Uses `discord.js` v14 + `@discordjs/voice`
- Connects to Mai via WS
- Receives `chat.chunk`/`chat.done` events → posts to Discord
- Sends Discord messages as `chat.send` to Mai
- Supports voice channels via Discord voice + Mai TTS

### Telegram Bot

Adapt from `services/telegram-bot` in AIRI:
- Uses `grammy` v1.42
- Connects to Mai via WS
- Bidirectional chat relay

### Files to Create

```
platforms/
├── discord/
│   ├── package.json
│   ├── src/
│   │   ├── index.ts           # Bot entry
│   │   ├── ws-client.ts       # Mai connection
│   │   └── handlers.ts        # Message/voice handlers
│   └── docker-compose.yaml
├── telegram/
│   ├── package.json
│   ├── src/
│   │   ├── index.ts
│   │   ├── ws-client.ts
│   │   └── handlers.ts
│   └── docker-compose.yaml
```

---

## Phase 6: Plugin System (Week 16–20)

### Goal
Allow third-party extensions (games, tools, integrations).

### Approach

Adapt AIRI's three-layer plugin architecture (protocol → SDK → host) but simplified:

```
internal/plugins/
├── protocol.go       # Plugin protocol (Go types)
├── host.go           # Plugin host (manages lifecycle)
└── registry.go       # Plugin registry

plugins/              # Plugin directory (TS or Go)
├── chess/
│   ├── plugin.json   # Manifest
│   └── main.go       # Go plugin OR
│   └── index.ts      # TS plugin (communicates via WS)
└── homeassistant/
    ├── plugin.json
    └── index.ts
```

### Plugin Protocol

Plugins register with Mai's WS server as authenticated peers (like AIRI's module system):

```json
{ "method": "plugin.announce", "params": { "name": "chess", "version": "1.0", "capabilities": ["game"] } }
{ "method": "plugin.ready", "params": { "tools": ["chess.move", "chess.analyze"] } }
```

---

## Phase 7: Game Integrations (Week 18–22)

### Goal
Minecraft bot, chess, and other game integrations.

### Files to Adapt from AIRI

```
games/
├── minecraft/
│   ├── package.json
│   ├── src/
│   │   ├── bot-runtime.ts     # From AIRI minecraft-bot-runtime.ts
│   │   ├── skills/            # Combat, movement, crafting
│   │   └── plugins/           # Echo, follow, pathfinder
├── chess/
│   ├── package.json
│   └── src/
│       ├── game.ts            # chess.js + stockfish
│       └── ws-client.ts
```

### Minecraft Integration Details
- Uses `mineflayer` + plugins (same as AIRI)
- Bot connects to Mai via WS
- Mai's ReAct loop makes game decisions
- Voice commands: "go to the village", "attack the zombie", "craft a pickaxe"
- Game events feed into Mai's memory (episodic + semantic)

---

## Phase 8: Autonomous Visual Generation (Week 20–24)

### Goal
Context-aware visual generation (screenshots, scene descriptions).

### Files to Adapt from AIRI

```
companion/src/artistry/
├── ArtistryPanel.vue        # Image display
├── useArtistry.ts           # Generation pipeline
└── providers/
    ├── comfyui.ts           # Local ComfyUI integration
    └── replicate.ts         # Cloud fallback
```

### Integration with Mai
- Mai's vision module can request image generation via tool calling
- New tool: `generate_image` in tool registry
- Generated images displayed in companion UI

---

## Go Backend Modifications Summary

### New Files

```
internal/server/
├── server.go              # net/http + gorilla/websocket server
├── hub.go                 # Connection management, broadcast
├── handler.go             # JSON-RPC method router
├── auth.go                # Token auth
├── protocol.go            # Message type definitions
├── bridge.go              # Event bus ↔ WS bridge
└── middleware.go           # CORS, rate limiting, logging

internal/plugins/
├── protocol.go            # Plugin lifecycle types
├── host.go                # Plugin manager
└── registry.go            # Plugin registry
```

### Modified Files

| File | Change |
|------|--------|
| `cmd/mai/main.go` | Initialize WS server, bridge to event bus |
| `cmd/mai/audio.go` | Stream TTS audio chunks via WS |
| `internal/agent/loop.go` | Emit WS notifications for state changes |
| `internal/cognition/react.go` | Emit tool call/result events via WS |
| `internal/personality/emotion.go` | Emit emotion events via WS |
| `internal/memory/manager.go` | Expose memory search via WS handler |
| `pkg/models/config.go` | Add `Server` config section |
| `config.yaml` | Add server configuration |
| `go.mod` | Add `gorilla/websocket` dependency |

### Event Bus → WS Bridge Events

| Mai Event | WS Notification | Direction |
|-----------|-----------------|-----------|
| `perception.audio.transcription` | `transcription` | → Client |
| `emotion.detected` | `emotion.detected` | → Client |
| `action.tts.request` | `tts.chunk` / `tts.done` | → Client |
| LLM streaming | `chat.chunk` / `chat.done` | → Client |
| Agent status change | `state.update` | → Client |
| Proactive suggestion | `proactive.hint` | → Client |
| Tool execution | `tool.result` | → Client |
| Client input | `chat.send` / `chat.voice` | → Mai |
| Config change | `config.get` / `config.set` | Bidirectional |

---

## Technology Stack Summary

| Component | Technology | Origin |
|-----------|-----------|--------|
| Go backend | Mai (existing) | - |
| WebSocket server | gorilla/websocket | New |
| Frontend framework | Vue 3 + Composition API | Adapted from AIRI |
| State management | Pinia | Adapted from AIRI |
| Build tool | Vite | Adapted from AIRI |
| CSS framework | UnoCSS | Adapted from AIRI |
| Live2D rendering | pixi-live2d-display + PixiJS | Adapted from AIRI |
| VRM rendering | @pixiv/three-vrm + Three.js + TresJS | Adapted from AIRI |
| Spine rendering | @esotericsoftware/spine-webgl | Adapted from AIRI |
| WebSocket client | better-ws (adapted) | Adapted from AIRI |
| Desktop wrapper | Tauri v2 | New (replaces Electron) |
| Discord bot | discord.js v14 | Adapted from AIRI |
| Telegram bot | grammy v1.42 | Adapted from AIRI |
| Chess game | chess.js + stockfish | Adapted from AIRI |
| Minecraft bot | mineflayer | Adapted from AIRI |
| Image generation | ComfyUI / Replicate | Adapted from AIRI |
| Plugin system | Custom (Go host + WS protocol) | New (inspired by AIRI) |

---

## Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| WS performance under load | Use gorilla/websocket with write buffers; broadcast only to interested clients |
| Memory duplication (Go + TS) | Mai owns authoritative state; TS mirrors via WS sync |
| AIRI code adaptation complexity | Start with chat-only UI (Phase 1), add character rendering incrementally |
| Platform-specific issues | Tauri v2 has solid cross-platform support; test early on all 3 OS |
| Auth security | Token-based auth for WS; CORS restrictions; local-only default |
| TTS audio streaming latency | Send small audio chunks (100ms) over WS; use Web Audio API for gapless playback |

---

## Timeline Summary

| Phase | Weeks | Deliverable |
|-------|-------|-------------|
| 0: WS Foundation | 1–2 | Mai WS server, event bridge |
| 1: Companion UI | 2–4 | Vue app with chat, streaming, settings |
| 2: Live2D | 4–8 | Animated character reacts to emotion/speech |
| 3: VRM 3D | 8–12 | Full 3D character option |
| 4: Desktop App | 10–14 | Tauri overlay + multi-window |
| 5: Platforms | 12–16 | Discord + Telegram bots |
| 6: Plugins | 16–20 | Extension system |
| 7: Games | 18–22 | Minecraft + Chess |
| 8: Visual Gen | 20–24 | Autonomous image generation |

**Total: ~24 weeks (6 months) for full feature parity with AIRI + Mai's unique capabilities.**
