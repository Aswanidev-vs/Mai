# MAI vs Refer Repo (hermes-agent) — Feature Gaps & Implementable Upgrades

## 1) What Hermes Agent brings (high-signal capabilities from `refer/hermes-agent`)
From the Hermes README/AGENTS.md, the standout capabilities include:

### A. Multi-surface UX + gateway
- A real CLI + a full TUI (Ink) and a messaging **gateway** (Telegram/Discord/Slack/WhatsApp/Signal/etc.) from one process.
- A PTY-backed terminal experience and a dashboard chat pane (embedded terminal UI).

### B. “Toolsets” + scalable tool architecture
- Toolsets as first-class bundles (many tool categories: browser/search/code_execution/tts/memory/etc.).
- Tool dispatch with a registry and schemas, plus MCP integration.
- Plugin system for tools, memory providers, and hooks (pre/post tool calls, pre/post LLM calls, session lifecycle).

### C. Skills system (and “optional skills”)
- Skills are distinct from tools: reusable high-level behaviors packaged with docs + tooling expectations.
- Skill hub / browsing and categorization.

### D. Delegation, parallelization, and budgets
- `delegate_task` spawns isolated subagents with constrained roles and max spawn depth.
- Supports batch/parallel tasks with capped concurrency.

### E. Cron + scheduled automations
- A scheduler with cron-like and natural-language “every X” formats.
- Jobs can inject stdout/context into prompts, run scripts, and deliver results to platforms.
- Cron intentionally can disable memory.

### F. Kanban multi-agent work queues (durable)
- SQLite-backed board with worker dispatch and task claiming.
- Failure limits, heartbeats, notifications, and multi-tenant isolation.

### G. Profiles (multi-instance) + hermetic paths
- Strong profile isolation via `HERMES_HOME`.
- Multiple profiles each with their own sessions/memory/skills.

### H. Safety/guardrails & approvals
- Multiple “message guards” in the gateway and explicit approval flows.
- Tool guardrails and tool result classification.

### I. Context/memory sophistication
- Persistent session DB with FTS5 search + summarization.
- Context compression and memory manager design.
- Agent-curated memory loops and “learning loop” concepts.

---

## 2) What MAI currently has (from this repo)
MAI already covers several foundational building blocks:

- **Event-driven architecture**: `internal/events/bus.go` + perception bridge `internal/perception/bridge.go`.
- **Agent orchestration**: `internal/agent/loop.go` subscribes to transcription + vision and triggers TTS (`action.tts.request`).
- **LLM abstraction**: `internal/llm/factory.go` + `internal/llm/hybrid.go` (privacy-based routing).
- **Tools**: `internal/tools/registry.go` + adapters + optional MCP client (`internal/tools/mcp/client.go`).
- **Cognition**:
  - ReAct tool loop: `internal/cognition/react.go`
  - Planner: `internal/cognition/planner.go`
  - Function calling (risk: file content appears inconsistent in the earlier read; should be validated/compiled)
- **Memory**:
  - Working, episodic (SQLite), semantic (vector JSON), and RAG pipeline.

So MAI is “tool + event + memory + agent loop” oriented already—Hermes adds higher-level systems (skills, cron, kanban, profiles, gateway, plugins) and production-grade safeguards.

---

## 3) Direct feature proposals: Jarvis/AI-companion capabilities we can implement in MAI (highest ROI)
This update focuses on “Jarvis-like” behavior: continuous help, proactive planning, reliable tool use, personal memory, and safe unattended automations—using Hermes’ *under-the-hood* mechanisms (toolset scoping, tool dispatch middleware, session persistence, and robust tool-call execution patterns).

Below are implementable upgrades in MAI, mapped to specific Hermes mechanisms seen in code such as:
- `run_agent.py` (core agent loop, session persistence, interrupts, tool execution hardening)
- `model_tools.py` (tool schema assembly, toolset filtering/scoping, tool-call dispatch + arg coercion, middleware/hooks integration)

### Proposal 1 — “Companion Skills”: user-invokable behaviors + auto-classification
**Hermes equivalent:** Skills (distinct from tools) that package reusable multi-step workflows and are invoked consistently.

**Jarvis value:** Commands like “Jarvis, plan my day”, “Jarvis, summarize my notes”, “Jarvis, do a quick web check”, “Jarvis, prepare tonight’s automation” should become *one semantic action*, not repeated ad-hoc prompts.

**Implementation approach in MAI:**
- Create `internal/skills/`:
  - `skill_registry.go`: load/list skills from a JSON/YAML manifest (lightweight; no docs hub needed initially)
  - `skill_runner.go`: translate a skill invocation into:
    - cognition goal text
    - allowed toolsets (tool gating)
    - structured context seeds from memory
- Add skill routing in `internal/agent/loop.go`:
  - when transcription ends, classify into either:
    - direct tool execution
    - free-form agent response
    - `skill_runner.Run(skillName, args, memoryContext)`
- Extend `internal/cognition/prompt_engine.go`:
  - include the current “active skill” in the system prompt so ReAct/planner follow the intended workflow

**Where to integrate:**
- `internal/agent/loop.go`: new “skill routing” decision before ReAct starts.
- `internal/cognition/prompt_engine.go`: add skill-aware prompt framing.

---

### Proposal 2 — Add Toolset bundles + enable/disable via config (like Hermes toolsets)
**Hermes equivalent:** Toolsets as first-class; platforms inherit base toolsets.

**Why in MAI:** MAI already has a registry of adapters/tools. Toolsets would:
- constrain the ReAct/planner tool list (reduce hallucinated tool usage)
- let you ship “lite mode” (voice control only) vs “research mode” (web + file + deep search)

**Implementation approach in MAI:**
- Introduce `internal/tools/toolsets.go` (or config-driven mapping):
  - toolset name → list of tool names
- Modify tool registry:
  - `registry.List()` should become `registry.ListEnabled(toolsetFilter)` (or accept allowed toolsets)
- Update agent boot wiring in `cmd/mai/main.go`:
  - build registry with only those toolsets enabled

**Where to integrate:**
- `internal/cognition/react.go`: uses `registry.List()` to build prompts; constrain this list.
- `internal/cognition/planner.go` and `function_calling.go`: same.

---

### Proposal 3 — Add cron scheduling for unattended automation
**Hermes equivalent:** cronjobs with job scripts, context injection, and optional no-agent mode.

**Why in MAI:** MAI can already do automation via tools, but it has no scheduler.

**Implementation approach in MAI:**
- Add `internal/cron/`:
  - parse cron/natural language schedule from config
  - persist job definitions
  - spawn “agent runs” with isolated context (option: disable memory like Hermes)
- Add config section to `config.yaml`:
  - `cron.jobs[]` with schedule, goal text, optional toolset constraints, optional script stdout

**Where to integrate:**
- `cmd/mai/main.go`: start scheduler loop if enabled.
- `internal/agent/loop.go`: add “run background session” entry point.

---

### Proposal 4 — Add “Kanban-like” task queue for multi-agent collaboration
**Hermes equivalent:** durable SQLite board with claiming/blocking/heartbeats.

**Why in MAI:** Useful if you want:
- multiple profiles/workers to collaborate
- durable retries and failure limits
- a “dispatcher” process model

**Implementation approach in MAI:**
- Add `internal/kanban/` backed by SQLite:
  - tables: tasks, claims, comments, statuses, heartbeats
- Expose tools:
  - `kanban_create`, `kanban_list`, `kanban_claim`, `kanban_complete`, `kanban_block`, etc.
- Add a dispatcher routine:
  - periodically promote ready tasks and spawn agent workers (or let users trigger workers)

**Where to integrate:**
- `internal/tools/adapters/*`: register Kanban tools.
- `internal/agent/loop.go`: allow tasks to be executed from kanban claims.

---

### Proposal 5 — Profiles (multi-instance isolation) in MAI
**Hermes equivalent:** multi-profile `HERMES_HOME` isolation.

**Why in MAI:** MAI already writes to `data/` and has memory DB at `data/memory/episodic.db`. Profiles would:
- isolate caches/memory/sessions/config
- allow running multiple personalities simultaneously

**Implementation approach in MAI:**
- Add `--profile` flag in `cmd/mai/main.go`.
- Replace hard-coded `data/` paths with profile-rooted paths:
  - `data/<profile>/...` or `profiles/<profile>/data/...`
- Ensure memory manager uses the profile base dir.

**Where to integrate:**
- `internal/memory/manager.go` and store constructors.
- `internal/agent/user_model.go` (currently uses `data`).

---

### Proposal 6 — Add “Plugin” system (hooks + providers) — lightweight version first
**Hermes equivalent:** plugin system with lifecycle hooks and tool/memory plugins.

**Why in MAI:** It’s the fastest path to ecosystem extensibility.

**Implementation approach in MAI (practical Go version):**
- Since Go plugins are painful cross-platform, implement a **plugin-like registry**:
  - plugin config file lists allowed extra adapters/tools
  - load extra tools via MCP only, or via “tool adapters” dynamic registration at startup
- Add lifecycle hooks interfaces:
  - `OnPreToolCall(ctx, toolName, args)`
  - `OnPostToolCall(ctx, toolName, result)`
  - `OnPreLLMCall(...)`
  - `OnSessionStart/End(...)`

**Where to integrate:**
- `internal/cognition/react.go` and `internal/llm/*` call sites
- `internal/events/bus.go`: publish lifecycle events.

---

### Proposal 7 — Add approval/guardrails for destructive actions (Hermes-style)
**Hermes equivalent:** explicit approval flows and tool guardrails.

**Why in MAI:** MAI currently has `Action.RequiresConfirm` (in `cmd/mai/actions.go`), but the agentic tool layer needs similar control.

**Implementation approach in MAI:**
- Add a “safety policy” middleware in ToolRegistry:
  - for tools marked destructive (or those with high risk), publish an `action.approval.request` event
  - block tool execution until user approves (or apply policy: always ask, ask-once, never)
- Extend event bus handlers:
  - `internal/agent/loop.go` should pause/resume around approvals.

**Where to integrate:**
- `internal/tools/registry.go`: add metadata `requires_approval`.
- `internal/cognition/react.go`: if tool requires approval, don’t execute directly.

---

## 4) Which gaps are “highest priority” for implementation
If implementing progressively, I’d order as:

1. **Cron scheduler** (unattended automation; MAI is already agent/tool capable)
2. **Toolset bundles** (constrain capability scope; improves reliability of ReAct/planner)
3. **Skills layer** (make advanced workflows reusable and documented)
4. **Profiles** (practical multi-instance workflow; reduces data collisions)
5. **Kanban queue** (enables collaboration/dispatch; bigger refactor)
6. **Approvals/guardrails** (safety hardening)
7. **Plugin-like hooks** (ecosystem/extensibility)

---

## 5) Concrete “MAI code areas” to touch for these features
- `cmd/mai/main.go`: startup flags, scheduler start, profile base dir selection.
- `internal/events/bus.go`: lifecycle/approval events.
- `internal/tools/registry.go` + `internal/tools/adapters/*`: tool metadata + toolsets + approval gating.
- `internal/cognition/react.go` / `planner.go`: ensure prompts/tool lists reflect enabled toolsets and approval gating.
- `internal/memory/*` + `internal/agent/user_model.go`: profile-rooted persistence.
- `internal/agent/loop.go`: route skill invocation, pause on approval, and dispatch cron runs.

---

## 6) Note on correctness before adding features
Your MAI repo analysis previously flagged that `internal/cognition/function_calling.go` may be inconsistent/broken, and `cmd/mai/main.go` may be truncated in-session in earlier excerpts. Before implementing new Hermes-derived systems, you should:
- run `go test ./...` and `go build ./cmd/mai`
- ensure tool execution contracts and event flows compile and behave deterministically

This prevents building new layers on top of a partially broken cognition path.
