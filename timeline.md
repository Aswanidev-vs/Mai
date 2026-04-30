# Mai Implementation Timeline

This document tracks the progress of evolving Mai into a JARVIS/FRIDAY-class agent.

## Implementation Phases

| Phase | Goal | Status | Date |
|-------|------|--------|------|
| **Phase 0: Foundation** | Refactor existing code into interfaces, establish project structure | In Progress | 2026-04-30 |
| **Phase 1: Coexistence** | Event bus, persistent memory hierarchy, tool registry foundation | Pending | |
| **Phase 2: Cognitive Layer** | ReAct + CoT reasoning, goal management, agentic mode | Pending | |
| **Phase 3: Multi-Modal Fusion** | Continuous vision, emotional prosody, proactive monitoring | Pending | |
| **Phase 4: Ecosystem** | MCP tool discovery, autonomous API learning, self-improvement | Pending | |
| **Phase 5: Polish** | Optimization, latency reduction, edge case handling | Pending | |

## Task Log

### 2026-04-30
- [x] Initial analysis of `ROADMAP.md` and `ARCHITECTURE.md`.
- [x] Created `timeline.md` to track implementation progress.
- [x] Establish new project directory structure (`internal/`, `pkg/`).
- [x] Define core interfaces in `pkg/interfaces/`.
- [x] Implement initial Event Bus in `internal/events/`.
- [x] Implement initial Ollama LLM Provider in `internal/llm/`.
- [x] Implement Working and Episodic Memory in `internal/memory/`.
- [x] Implement initial Tool Registry in `internal/tools/`.
- [x] Implement initial Tool Adapters (Shell, OpenApp, WebSearch).
- [x] Implement ReAct Reasoning Loop in `internal/cognition/`.
- [x] Implement Agent Orchestrator (BDI Loop) in `internal/agent/`.
- [x] Implement Multi-Modal Perception adapters and Bridge in `internal/perception/`.
- [x] Bridge Legacy ASR/VAD/TTS with the new Agentic architecture in `cmd/mai/main.go`.
- [x] Implement Proactive Monitoring Loop (Phase 3).
- [x] Implement Semantic Memory (Vector Store) (Phase 3).
- [x] Implement Vision Processor Bridge (Phase 3).
- [x] Implement MCP Client stub for tool discovery (Phase 4).
- [x] Implement Reflexion (Self-Correction) in ReAct Loop (Phase 4).
- [ ] Performance Optimization and Polish (Phase 5).

---

## Technical Debt & Blockers
- [ ] Need to decide on Vector DB for Semantic Memory (Chroma vs. Milvus vs. local FAISS-like).
- [ ] Need to evaluate Ollama's structured output (JSON mode) performance for ReAct loops.
