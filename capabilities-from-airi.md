# AIRI Capabilities to Implement in Mai — Actionable Blueprint

**Date:** 2025-07-28
**Purpose:** Identify AIRI features worth porting to Mai, with Go implementation details, effort estimates, and priority rankings.

---

## Priority Matrix

| Priority | Feature | Effort | Impact | Difficulty |
|:--------:|---------|--------|--------|:----------:|
| **P0** | Discord Bot (native) | 2 weeks | Very High | Medium |
| **P0** | Telegram Bot (native) | 2 weeks | Very High | Medium |
| **P0** | Plugin Architecture | 3 weeks | Very High | Medium |
| **P1** | Observability (OTEL) | 2 weeks | High | Low |
| **P1** | Smart Vector Memory (pgvector-style) | 2 weeks | High | Medium |
| **P1** | Game Integrations (Chess) | 1 week | Medium | Low |
| **P1** | Smart Home (Home Assistant) | 2 weeks | Medium | Low |
| **P2** | Browser Extension Bridge | 2 weeks | Medium | Medium |
| **P2** | VS Code Extension Bridge | 1 week | Medium | Low |
| **P2** | Computer Use (Desktop Automation) | 3 weeks | High | High |
| **P2** | Live2D Avatar Support | 2 weeks | Medium | High |
| **P3** | Image Generation (ComfyUI) | 1 week | Low | Low |
| **P3** | Multi-Platform (Electron wrapper) | 2 weeks | Medium | Medium |
| **P3** | Minecraft Bot | 4 weeks | Low | High |

---

## P0: Must-Have — These Transform Mai from Desktop App to Platform

### 1. Discord Bot (Native Integration)

**What AIRI does:** Full Discord bot with voice channels, slash commands, ASR/TTS, real-time chat bridging.

**Why it matters for Mai:** Users expect their AI companion on Discord. Currently Mai only has UI automation for WhatsApp/Telegram/Discord which is fragile and slow.

**AIRI Implementation Reference:**
- `services/discord-bot/src/adapters/airi-adapter.ts` — 337 lines, bridges Discord ↔ AIRI server
- `services/discord-bot/src/bots/discord/commands/summon.ts` — 579 lines, voice channel management
- Uses `discord.js` + `@discordjs/voice` + `opusscript`

**Go Implementation:**
```go
// internal/integrations/discord/
//   bot.go          — DiscordAdapter, message handling
//   voice.go        — VoiceManager, audio streaming
//   commands.go     — Slash commands (/ping, /summon, /ask)
//   audio.go        — Opus decode, WAV conversion for ASR

package discord

import (
    "github.com/bwmarrin/discordgo"
    "github.com/your/mai/internal/events"
    "github.com/your/mai/internal/llm"
)

type DiscordAdapter struct {
    session      *discordgo.Session
    eventBus     *events.Bus
    llmClient    llm.Provider
    voiceManager *VoiceManager
    config       DiscordConfig
}

type DiscordConfig struct {
    Token        string
    GuildID      string
    VoiceChannel string
    AllowedUsers []string  // whitelist
}

// Message handler — bridges Discord → Mai event bus
func (d *DiscordAdapter) handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
    // Ignore bot messages, check permissions
    if m.Author.Bot || !d.isAllowed(m.Author.ID) {
        return
    }

    // Only respond to mentions or DMs
    if !d.isMentioned(m) && m.GuildID != "" {
        return
    }

    // Strip mention, extract clean text
    content := d.stripMention(m.Content)

    // Publish to Mai event bus
    d.eventBus.Publish("discord.message", DiscordMessage{
        UserID:    m.Author.ID,
        ChannelID: m.ChannelID,
        GuildID:   m.GuildID,
        Content:   content,
        IsDM:      m.GuildID == "",
    })
}

// Voice channel — bridge audio to Mai's ASR pipeline
func (d *VoiceManager) joinChannel(channelID string) error {
    vc, err := d.session.ChannelVoiceJoin(
        d.config.GuildID, channelID, false, false,
    )
    if err != nil {
        return err
    }

    // Listen for speaking events
    go d.monitorVoice(vc)
    return nil
}

func (d *VoiceManager) monitorVoice(vc *discordgo.VoiceConnection) {
    for {
        select {
        case opus := <-vc.OpusRecv:
            // Decode opus → PCM → send to Mai ASR
            pcm := d.decodeOpus(opus)
            d.eventBus.Publish("discord.voice.frame", VoiceFrame{
                PCM:      pcm,
                UserID:   opus.UserID,
                GuildID:  vc.GuildID,
            })
        }
    }
}
```

**Dependencies:**
- `github.com/bwmarrin/discordgo` — Discord API
- `github.com/bwmarrin/discordgo/opus` — Opus decoder
- `github.com/hraban/opus` — Opus Go bindings

**Files to Create:**
```
internal/integrations/discord/
    bot.go           — Adapter, message handling, event bridging
    voice.go         — Voice connection, audio streaming, Opus decode
    commands.go      — Slash command registration and handling
    config.go        — Config struct, validation
```

**Effort:** 2 weeks (1 week text + 1 week voice)

---

### 2. Telegram Bot (Native Integration)

**What AIRI does:** Full Telegram bot with LLM agent loop, sticker/photo interpretation, pgvector memory, action dispatching.

**Why it matters for Mai:** Telegram is the #1 messaging platform for AI bots. Native integration beats UI automation 100x.

**AIRI Implementation Reference:**
- `services/telegram-bot/src/bots/telegram/index.ts` — 543 lines, main bot logic
- Uses grammY framework, Drizzle ORM, pgvector, OpenTelemetry
- Agent loop: `loopIterationForChat()` → `handleLoopStep()` → `imagineAnAction()` → `dispatchAction()`

**Go Implementation:**
```go
// internal/integrations/telegram/
//   bot.go          — Main bot, message routing
//   agent.go        — LLM agent loop (imagine → dispatch)
//   actions.go      — Action types and execution
//   memory.go       — Vector memory search
//   stickers.go     — Sticker/photo interpretation

package telegram

import (
    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
    "github.com/your/mai/internal/llm"
    "github.com/your/mai/internal/memory"
)

type TelegramBot struct {
    api       *tgbotapi.BotAPI
    llm       llm.Provider
    memory    *memory.VectorStore
    chats     map[int64]*ChatContext
    config    TelegramConfig
}

type ChatContext struct {
    ChatID         int64
    Messages       []Message
    Actions        []ActionResult
    UnreadMessages []tgbotapi.Update
}

type Action struct {
    Type    string          `json:"type"` // "send_message", "send_sticker", "continue", "break", "sleep"
    Payload json.RawMessage `json:"payload"`
}

// Agent loop — mirrors AIRI's imagineAnAction pattern
func (b *TelegramBot) loopStep(ctx context.Context, chatCtx *ChatContext) error {
    // Build prompt with message history and action history
    prompt := b.buildAgentPrompt(chatCtx)

    // Ask LLM to imagine next action
    response, err := b.llm.GenerateStructured(ctx, prompt, &Action{})
    if err != nil {
        return err
    }

    // Dispatch action
    return b.dispatch(ctx, chatCtx, response)
}

func (b *TelegramBot) dispatch(ctx context.Context, chatCtx *ChatContext, action Action) error {
    switch action.Type {
    case "send_message":
        var payload struct{ Text string `json:"text"` }
        json.Unmarshal(action.Payload, &payload)
        msg := tgbotapi.NewMessage(chatCtx.ChatID, payload.Text)
        _, err := b.api.Send(msg)
        return err

    case "send_sticker":
        var payload struct{ FileID string `json:"file_id"` }
        json.Unmarshal(action.Payload, &payload)
        msg := tgbotapi.NewSticker(chatCtx.ChatID, payload.FileID)
        _, err := b.api.Send(msg)
        return err

    case "sleep":
        time.Sleep(30 * time.Second)
        return nil

    case "break":
        chatCtx.Messages = nil
        chatCtx.Actions = nil
        return nil
    }
    return nil
}
```

**Dependencies:**
- `github.com/go-telegram-bot-api/telegram-bot-api/v5` — Telegram Bot API
- `github.com/jackc/pgx/v5` — PostgreSQL (for vector memory)

**Files to Create:**
```
internal/integrations/telegram/
    bot.go           — Bot initialization, update loop, message routing
    agent.go         — LLM agent loop, prompt construction
    actions.go       — Action types, dispatch logic
    memory.go        — pgvector search for context
    stickers.go      — Sticker/photo interpretation via vision LLM
    config.go        — Config struct
```

**Effort:** 2 weeks

---

### 3. Plugin Architecture

**What AIRI does:** Full plugin SDK with DI container, lifecycle hooks, permission model, hot-reload.

**Why it matters for Mai:** Currently Mai has a static tool registry. A plugin system enables community contributions and modular features.

**AIRI Implementation Reference:**
- `packages/plugin-sdk/src/plugin-host/core.ts` — 858 lines, ExtensionHost
- Extension lifecycle: `manifest → load → setup → kits.use() → modules.register() → ready → dispose`
- Two-layer permission model (extension ceiling + module intersection)

**Go Implementation:**
```go
// internal/plugins/
//   host.go         — PluginHost, loading, lifecycle
//   extension.go    — Extension interface, SetupContext
//   kit.go          — KitRef, KitRegistry
//   permission.go   — PermissionService
//   loader.go       — FileSystemLoader, Go plugin loading

package plugins

import "context"

// Extension is the author-facing contract
type Extension interface {
    ID() string
    Version() string
    Setup(ctx *SetupContext) error
    Stop() error
}

// SetupContext is host-provided to each extension
type SetupContext struct {
    ExtensionID string
    SessionID   string
    EventBus    EventBus
    Config      *Config
    Logger      *Logger
}

// KitRef defines an API surface
type KitRef[T any] struct {
    ID           string
    Version      string
    CreateClient func() T
}

// PluginHost orchestrates everything
type PluginHost struct {
    extensions   map[string]Extension
    sessions     map[string]*ExtensionSession
    permissions  *PermissionService
    eventBus     EventBus
    logger       *Logger
}

type ExtensionSession struct {
    ExtensionID  string
    SessionID    string
    CreatedAt    time.Time
    Disposables  []func()
    Subscriptions []func()
}

// Load loads an extension from a directory
func (h *PluginHost) Load(ctx context.Context, path string) error {
    // 1. Load manifest (plugin.json or Go plugin .so)
    manifest, err := h.loadManifest(path)
    if err != nil {
        return fmt.Errorf("load manifest: %w", err)
    }

    // 2. Resolve permissions
    perms := h.permissions.Resolve(manifest.Permissions)

    // 3. Create session
    session := &ExtensionSession{
        ExtensionID: manifest.ID,
        SessionID:   uuid.New().String(),
        CreatedAt:   time.Now(),
    }
    h.sessions[manifest.ID] = session

    // 4. Create setup context
    setupCtx := &SetupContext{
        ExtensionID: manifest.ID,
        SessionID:   session.SessionID,
        EventBus:    h.eventBus,
        Config:      h.config,
        Logger:      h.logger.With("extension", manifest.ID),
    }

    // 5. Call extension.Setup()
    ext := h.instantiate(manifest)
    if err := ext.Setup(setupCtx); err != nil {
        return fmt.Errorf("setup %s: %w", manifest.ID, err)
    }

    h.extensions[manifest.ID] = ext
    return nil
}

// LoadPlugin loads a Go native plugin (.so file)
func (h *PluginHost) LoadPlugin(ctx context.Context, path string) error {
    p, err := plugin.Open(path)
    if err != nil {
        return fmt.Errorf("open plugin: %w", err)
    }

    // Look for New() function that returns Extension
    newFn, err := p.Lookup("New")
    if err != nil {
        return fmt.Errorf("lookup New: %w", err)
    }

    ext := newFn.(func() Extension)()
    return h.registerExtension(ctx, ext)
}
```

**Plugin manifest format (JSON):**
```json
{
    "id": "mai-plugin-discord",
    "version": "1.0.0",
    "name": "Discord Integration",
    "description": "Native Discord bot integration",
    "author": "mai-community",
    "permissions": {
        "events": ["read", "subscribe"],
        "memory": ["read", "write"],
        "llm": ["invoke"]
    },
    "entry": "plugin.so"
}
```

**Files to Create:**
```
internal/plugins/
    host.go          — PluginHost, Load/Start/Stop/HotReload
    extension.go     — Extension interface, SetupContext
    kit.go           — KitRef, KitRegistry
    permission.go    — PermissionService, two-layer model
    loader.go        — FileSystemLoader, Go plugin (.so) loading
    manifest.go      — Manifest parsing, validation
    lifecycle.go     — DisposableStore, cleanup hooks
```

**Effort:** 3 weeks

---

## P1: High Value — Significant Capability Boost

### 4. Observability Stack (OpenTelemetry)

**What AIRI does:** Full OTEL integration with traces, metrics, logs → Grafana dashboards.

**Go Implementation:**
```go
// internal/observability/
//   telemetry.go    — OTEL setup, tracer/meter providers
//   middleware.go   — HTTP/gRPC middleware for tracing

package observability

import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
    "go.opentelemetry.io/otel/sdk/trace"
    "go.opentelemetry.io/otel/sdk/metric"
    "go.opentelemetry.io/otel/metric"
)

func Init(serviceName string) (func(), error) {
    // Trace exporter → Jaeger/Tempo
    traceExporter, _ := otlptracegrpc.New(ctx,
        otlptracegrpc.WithEndpoint("localhost:4317"),
        otlptracegrpc.WithInsecure(),
    )
    tp := trace.NewTracerProvider(
        trace.WithBatcher(traceExporter),
        trace.WithSampler(trace.AlwaysSample()),
    )
    otel.SetTracerProvider(tp)

    // Metric exporter → Prometheus
    metricExporter, _ := prometheus.New()
    mp := metric.NewMeterProvider(
        metric.WithReader(metric.NewPeriodicReader(metricExporter)),
    )
    otel.SetMeterProvider(mp)

    // Return cleanup function
    return func() {
        tp.Shutdown(ctx)
        mp.Shutdown(ctx)
    }, nil
}

// Usage in any module:
func (a *Agent) HandleInput(ctx context.Context, input string) {
    ctx, span := a.tracer.Start(ctx, "agent.HandleInput")
    defer span.End()

    span.SetAttributes(attribute.String("input.length", strconv.Itoa(len(input))))

    // ... agent logic ...

    span.AddEvent("llm.call.started")
    response, err := a.llm.Generate(ctx, prompt)
    span.AddEvent("llm.call.completed",
        attribute.Int("tokens", response.Tokens),
    )
}
```

**Dependencies:**
- `go.opentelemetry.io/otel` — Core OTEL
- `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` — Trace export
- `go.opentelemetry.io/otel/exporters/prometheus` — Metric export

**Effort:** 2 weeks

---

### 5. Smart Vector Memory (PostgreSQL + pgvector style)

**What AIRI does:** PostgreSQL + pgvector with HNSW indexes, memory types (working/short_term/long_term/muscle), importance scoring, emotional impact tracking.

**Go Implementation:**
```go
// internal/memory/vector.go

package memory

import (
    "github.com/jackc/pgx/v5/pgxpool"
    "github.com/pgvector/pgvector-go"
)

type VectorStore struct {
    pool *pgxpool.Pool
}

type MemoryFragment struct {
    ID              uuid.UUID
    Content         string
    MemoryType      string    // working, short_term, long_term, muscle
    Category        string    // chat, relationships, people, life
    Importance      int       // 1-10
    EmotionalImpact int       // -10 to 10
    CreatedAt       time.Time
    LastAccessed    time.Time
    AccessCount     int
    Metadata        map[string]interface{}
    ContentVector   pgvector.Vector
}

// Search finds memories semantically similar to query
func (s *VectorStore) Search(ctx context.Context, queryVec []float32, memoryType string, limit int) ([]MemoryFragment, error) {
    rows, err := s.pool.Query(ctx, `
        SELECT id, content, memory_type, category, importance, emotional_impact,
               created_at, last_accessed, access_count, metadata,
               content_vector <=> $1 AS distance
        FROM memory_fragments
        WHERE memory_type = $2 AND deleted_at IS NULL
        ORDER BY distance
        LIMIT $3
    `, pgvector.NewVector(queryVec), memoryType, limit)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var results []MemoryFragment
    for rows.Next() {
        var f MemoryFragment
        err := rows.Scan(&f.ID, &f.Content, &f.MemoryType, &f.Category,
            &f.Importance, &f.EmotionalImpact, &f.CreatedAt, &f.LastAccessed,
            &f.AccessCount, &f.Metadata, &f.ContentVector)
        if err != nil {
            return nil, err
        }
        results = append(results, f)
    }
    return results, nil
}

// Store saves a memory with auto-embedding
func (s *VectorStore) Store(ctx context.Context, fragment MemoryFragment, embedder Embedder) error {
    vec, err := embedder.Embed(ctx, fragment.Content)
    if err != nil {
        return err
    }
    fragment.ContentVector = pgvector.NewVector(vec)

    _, err = s.pool.Exec(ctx, `
        INSERT INTO memory_fragments (id, content, memory_type, category, importance,
            emotional_impact, created_at, last_accessed, access_count, metadata, content_vector)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
    `, fragment.ID, fragment.Content, fragment.MemoryType, fragment.Category,
        fragment.Importance, fragment.EmotionalImpact, fragment.CreatedAt,
        fragment.LastAccessed, fragment.AccessCount, fragment.Metadata, fragment.ContentVector)
    return err
}
```

**SQL Schema:**
```sql
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE memory_fragments (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    content TEXT NOT NULL,
    memory_type VARCHAR(20) NOT NULL DEFAULT 'short_term',
    category VARCHAR(50),
    importance INTEGER DEFAULT 5,
    emotional_impact INTEGER DEFAULT 0,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    last_accessed TIMESTAMPTZ DEFAULT NOW(),
    access_count INTEGER DEFAULT 0,
    metadata JSONB DEFAULT '{}',
    deleted_at TIMESTAMPTZ,
    content_vector vector(1536)
);

CREATE INDEX idx_memory_fragments_vector ON memory_fragments
    USING hnsw (content_vector vector_cosine_ops);
CREATE INDEX idx_memory_fragments_type ON memory_fragments(memory_type);
CREATE INDEX idx_memory_fragments_importance ON memory_fragments(importance);
```

**Dependencies:**
- `github.com/jackc/pgx/v5` — PostgreSQL driver
- `github.com/pgvector/pgvector-go` — pgvector bindings
- `github.com/google/uuid` — UUID generation

**Effort:** 2 weeks

---

### 6. Chess Game Integration

**What AIRI does:** Stockfish engine + LLM strategic decision making.

**Go Implementation:**
```go
// internal/tools/games/chess.go

package chess

import (
    "github.com/notnil/chess"
    "os/exec"
    "github.com/your/mai/internal/llm"
)

type ChessGame struct {
    game       *chess.Game
    stockfish  *StockfishEngine
    llm        llm.Provider
}

type StockfishEngine struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout *bufio.Scanner
}

func NewStockfish(path string) (*StockfishEngine, error) {
    cmd := exec.Command(path)
    stdin, _ := cmd.StdinPipe()
    stdout := bufio.NewScanner(cmd.Stdout)
    cmd.Start()

    // Init UCI protocol
    fmt.Fprintf(stdin, "uci\n")
    stdout.Scan() // "uciok"
    fmt.Fprintf(stdin, "isready\n")
    stdout.Scan() // "readyok"

    return &StockfishEngine{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (s *StockfishEngine) BestMove(fen string, depth int) (string, error) {
    fmt.Fprintf(s.stdin, "position fen %s\n", fen)
    fmt.Fprintf(s.stdin, "go depth %d\n", depth)

    for s.stdout.Scan() {
        line := s.stdout.Text()
        if strings.HasPrefix(line, "bestmove") {
            return strings.Fields(line)[1], nil
        }
    }
    return "", fmt.Errorf("no bestmove received")
}

func (g *ChessGame) GetAIMove(ctx context.Context) (string, error) {
    // LLM provides strategic context
    strategy, _ := g.llm.Generate(ctx, fmt.Sprintf(
        "You are playing chess. Current position FEN: %s. What's your strategy?",
        g.game.FEN(),
    ))

    // Stockfish provides tactical best move
    move, err := g.stockfish.BestMove(g.game.FEN(), 20)
    if err != nil {
        return "", err
    }

    // Apply move
    g.game.MoveStr(move)
    return move, nil
}
```

**Dependencies:**
- `github.com/notnil/chess` — Chess game logic
- Stockfish binary (download separately)

**Effort:** 1 week

---

### 7. Smart Home (Home Assistant)

**What AIRI does:** Device discovery, control, voice commands via Home Assistant REST API.

**Go Implementation:**
```go
// internal/tools/smarthome/homeassistant.go

package smarthome

import (
    "net/http"
    "encoding/json"
)

type HomeAssistant struct {
    client  *http.Client
    baseURL string
    token   string
}

type Entity struct {
    EntityID    string                 `json:"entity_id"`
    State       string                 `json:"state"`
    Attributes map[string]interface{} `json:"attributes"`
    LastChanged time.Time             `json:"last_changed"`
}

func NewHomeAssistant(baseURL, token string) *HomeAssistant {
    return &HomeAssistant{
        client:  &http.Client{},
        baseURL: baseURL,
        token:   token,
    }
}

func (ha *HomeAssistant) GetStates(ctx context.Context) ([]Entity, error) {
    req, _ := http.NewRequestWithContext(ctx, "GET", ha.baseURL+"/api/states", nil)
    req.Header.Set("Authorization", "Bearer "+ha.token)

    resp, err := ha.client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    var entities []Entity
    json.NewDecoder(resp.Body).Decode(&entities)
    return entities, nil
}

func (ha *HomeAssistant) CallService(ctx context.Context, domain, service string, data map[string]interface{}) error {
    body, _ := json.Marshal(data)
    req, _ := http.NewRequestWithContext(ctx, "POST",
        fmt.Sprintf("%s/api/services/%s/%s", ha.baseURL, domain, service),
        bytes.NewReader(body),
    )
    req.Header.Set("Authorization", "Bearer "+ha.token)
    req.Header.Set("Content-Type", "application/json")

    resp, err := ha.client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    return nil
}

// Voice command examples:
// "turn on living room lights" → CallService("light", "turn_on", {entity_id: "light.living_room"})
// "set thermostat to 72" → CallService("climate", "set_temperature", {entity_id: "climate.thermostat", temperature: 72})
// "lock front door" → CallService("lock", "lock", {entity_id: "lock.front_door"})
```

**Tool Registration:**
```go
// Add to internal/tools/registry.go
registry.Register(Tool{
    ID:          "homeassistant_get_states",
    Name:        "Get Home Devices",
    Category:    "smarthome",
    Description: "List all smart home devices and their states",
    Execute:     homeAssistantTool.GetStates,
})

registry.Register(Tool{
    ID:          "homeassistant_call_service",
    Name:        "Control Smart Device",
    Category:    "smarthome",
    Description: "Turn on/off lights, set thermostat, lock doors, etc.",
    Execute:     homeAssistantTool.CallService,
})
```

**Dependencies:**
- `net/http` — REST API client
- Home Assistant with Long-Lived Access Token

**Effort:** 2 weeks

---

## P2: Medium Value — Nice Features

### 8. Browser Extension Bridge

**What AIRI does:** Chrome extension captures page context, video subtitles, screenshots → sends to AIRI server via WebSocket.

**Go Implementation:**
```go
// internal/integrations/browser/
//   server.go       — WebSocket server for extension connections
//   context.go      — Page context processing

package browser

type PageContext struct {
    URL         string `json:"url"`
    Title       string `json:"title"`
    Description string `json:"description"`
    Language    string `json:"language"`
}

type VideoContext struct {
    URL          string  `json:"url"`
    Title        string  `json:"title"`
    Channel      string  `json:"channel"`
    DurationSec  float64 `json:"durationSec"`
    CurrentTime  float64 `json:"currentTimeSec"`
    IsPlaying    bool    `json:"isPlaying"`
    PlaybackRate float64 `json:"playbackRate"`
}

type BrowserBridge struct {
    eventBus *events.Bus
}

func (b *BrowserBridge) HandlePageUpdate(ctx context.Context, page PageContext) {
    // Update user model with browsing context
    b.eventBus.Publish("browser.page", page)

    // If user asks "what am I watching?", this context is available
}

func (b *BrowserBridge) HandleSubtitle(ctx context.Context, sub SubtitlePayload) {
    // Feed subtitles to memory system
    b.eventBus.Publish("browser.subtitle", sub)
}
```

**Chrome Extension (JS):**
```javascript
// content.js — captures page context
chrome.runtime.sendMessage({
    type: 'page:update',
    payload: {
        url: location.href,
        title: document.title,
        description: document.querySelector('meta[name="description"]')?.content,
    }
});

// For video sites — capture subtitles
const video = document.querySelector('video');
if (video) {
    const track = video.textTracks[0];
    track.oncuechange = () => {
        const cues = Array.from(track.activeCues);
        chrome.runtime.sendMessage({
            type: 'subtitle:update',
            payload: { text: cues.map(c => c.text).join(' ') }
        });
    };
}
```

**Effort:** 2 weeks

---

### 9. VS Code Extension Bridge

**What AIRI does:** Captures active file, cursor position, selection, git status → sends to AIRI.

**Go Implementation:**
```go
// internal/integrations/vscode/
//   bridge.go       — WebSocket handler for VS Code context

package vscode

type CodingContext struct {
    File struct {
        Path       string `json:"path"`
        LanguageID string `json:"languageId"`
        FileName   string `json:"fileName"`
    } `json:"file"`
    Cursor struct {
        Line      int `json:"line"`
        Character int `json:"character"`
    } `json:"cursor"`
    Selection *struct {
        Text  string `json:"text"`
        Start int    `json:"start"`
        End   int    `json:"end"`
    } `json:"selection,omitempty"`
    CurrentLine struct {
        LineNumber int    `json:"lineNumber"`
        Text       string `json:"text"`
    } `json:"currentLine"`
    Context struct {
        Before []string `json:"before"`
        After  []string `json:"after"`
    } `json:"context"`
    Git *struct {
        Branch string `json:"branch"`
        IsDirty bool  `json:"isDirty"`
    } `json:"git,omitempty"`
}

type VSCodeBridge struct {
    eventBus *events.Bus
}

func (b *VSCodeBridge) HandleContext(ctx context.Context, codingCtx CodingContext) {
    // Build context string for LLM
    text := fmt.Sprintf(
        "User opened file: %s (path: %s), cursor at line %d, char %d.\n\nContext:\n%s\n%s\n%s",
        codingCtx.File.FileName, codingCtx.File.Path,
        codingCtx.Cursor.Line+1, codingCtx.Cursor.Character+1,
        strings.Join(codingCtx.Context.Before, "\n"),
        codingCtx.CurrentLine.Text,
        strings.Join(codingCtx.Context.After, "\n"),
    )

    b.eventBus.Publish("vscode.context", ContextUpdate{
        Text:     text,
        Language: codingCtx.File.LanguageID,
        Git:      codingCtx.Git,
    })
}
```

**Effort:** 1 week

---

### 10. Computer Use (Desktop Automation Enhancement)

**What AIRI does:** macOS desktop orchestration via MCP — screenshot, click, type, terminal, browser control with workflow engine.

**Go Implementation (Windows):**
```go
// internal/automation/computeruse.go

package computeruse

type ComputerUseServer struct {
    executor DesktopExecutor
    terminal *TerminalRunner
}

type DesktopExecutor interface {
    TakeScreenshot() ([]byte, error)
    GetWindowList() ([]Window, error)
    Click(x, y int) error
    TypeText(text string) error
    PressKeys(keys ...string) error
    Scroll(x, y int, direction string) error
    OpenApp(name string) error
    FocusApp(name string) error
}

// Windows executor using RobotGo (already in Mai) + enhancements
type WindowsExecutor struct{}

func (e *WindowsExecutor) TakeScreenshot() ([]byte, error) {
    img, _ := robotgo.CaptureScreen()
    // Convert to PNG
    // ...
    return pngBytes, nil
}

func (e *WindowsExecutor) Click(x, y int) error {
    robotgo.Move(x, y)
    robotgo.Click("left")
    return nil
}

// Terminal runner using os/exec
type TerminalRunner struct {
    sessions map[string]*exec.Cmd
}

func (t *TerminalRunner) Exec(command string) (string, error) {
    cmd := exec.Command("cmd", "/C", command)
    output, err := cmd.CombinedOutput()
    return string(output), err
}
```

**MCP Server:**
```go
// Register as MCP tool server
func (s *ComputerUseServer) RegisterMCP(mcp *MCPServer) {
    mcp.RegisterTool("screenshot", s.handleScreenshot)
    mcp.RegisterTool("click", s.handleClick)
    mcp.RegisterTool("type_text", s.handleTypeText)
    mcp.RegisterTool("press_keys", s.handlePressKeys)
    mcp.RegisterTool("scroll", s.handleScroll)
    mcp.RegisterTool("open_app", s.handleOpenApp)
    mcp.RegisterTool("terminal_exec", s.handleTerminalExec)
    mcp.RegisterTool("observe_windows", s.handleObserveWindows)
}
```

**Effort:** 3 weeks

---

### 11. Live2D Avatar Support

**What AIRI does:** Pixi.js + pixi-live2d-display for 2D animated avatars.

**Go Implementation:**
- Go backend stays the same (serves events via WebSocket)
- Frontend changes: add Live2D rendering alongside VRM
- Use Cubism SDK for Web (JavaScript)

**Frontend changes (in companion web UI):**
```javascript
// Add Live2D renderer alongside Three.js VRM
import { Live2DModel } from 'pixi-live2d-display';

// Detect avatar format and switch renderer
if (avatarFormat === 'live2d') {
    const model = await Live2DModel.from('path/to/model.model3.json');
    app.stage.addChild(model);
    // Wire lip-sync, eye tracking, etc.
}
```

**Effort:** 2 weeks (mostly frontend)

---

## P3: Lower Priority — Future Features

### 12. Image Generation (ComfyUI)
- Add `image_generate` tool that calls local ComfyUI API
- Effort: 1 week

### 13. Electron Wrapper
- Wrap existing companion web UI with Electron for native desktop
- Effort: 2 weeks

### 14. Minecraft Bot
- Port AIRI's 3-layer cognitive architecture (perception → reflex → brain)
- Use `github.com/Tnze/go-mc` for Minecraft protocol
- Effort: 4 weeks

---

## Implementation Order

```
Week 1-2:  Discord Bot (text + voice)
Week 3-4:  Telegram Bot (agent loop + memory)
Week 5-7:  Plugin Architecture (host, loader, permissions)
Week 8-9:  Observability (OTEL traces + metrics)
Week 10-11: Vector Memory (pgvector-style)
Week 12:   Chess Integration
Week 13-14: Home Assistant Smart Home
Week 15-16: Browser Extension Bridge
Week 17:   VS Code Extension Bridge
Week 18-20: Computer Use Enhancement
Week 21-22: Live2D Avatar Support
Week 23-24: Image Generation + Electron
```

---

## Key Takeaway

The highest-ROI features from AIRI to port are:

1. **Native Discord/Telegram bots** — transforms Mai from desktop-only to multi-platform
2. **Plugin architecture** — enables community and modular growth
3. **Proper vector memory** — scales beyond JSON files
4. **Observability** — essential for debugging production issues
5. **Game/Smart Home integrations** — differentiates from generic chatbots

All of these are implementable in Go with the existing Mai architecture. The event bus, tool registry, and LLM provider abstractions already exist — these features plug into them.
