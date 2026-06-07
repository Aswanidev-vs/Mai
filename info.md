# MAI Codebase Analysis — Improvements & Fixes

## 1) High-impact architecture observations
- **Entry point**: `cmd/mai/main.go` orchestrates config loading, audio pipeline, and agentic/non-agentic response routing.
- **Agentic runtime** (event-driven):
  - **Event bus**: `internal/events/bus.go` provides publish/subscribe and request/response.
  - **Orchestrator**: `internal/agent/loop.go` subscribes to perception events and drives agent responses + TTS.
  - **Perception bridge**: `internal/perception/bridge.go` maps legacy perception outputs to the agent bus.
  - **Cognition**:
    - `internal/cognition/react.go`: ReAct-style tool execution loop.
    - `internal/cognition/planner.go`: LLM task decomposition.
    - `internal/cognition/function_calling.go`: Tool selection / chain execution (appears inconsistent; see below).
    - `internal/cognition/verifier.go`: Verification guardrails for claims and tool results.
  - **Memory**:
    - Working: `internal/memory/working.go`
    - Episodic (SQLite): `internal/memory/episodic.go`
    - Semantic vectors (JSON + embeddings): `internal/memory/semantic.go`
    - RAG pipeline: `internal/memory/rag.go`
- **LLM abstraction**:
  - `internal/llm/factory.go` builds providers and optionally a hybrid router.
  - `internal/llm/hybrid.go` uses `internal/agent/privacy.go` to decide local vs cloud routing.

## 2) Critical issues found (must-fix)
### A) `internal/cognition/function_calling.go` is very likely broken
**Why**: The file content appears truncated/merged and references identifiers that are not defined in the shown scope (e.g., `maxSteps`, `observations`, `allResults`). This can cause:
- compilation failures, or
- incorrect runtime behavior if the code actually exists differently than displayed.

**Fix**
1. Re-open the full file and verify:
   - imports
   - all variables referenced exist in the function scope
   - the control flow matches the intended spec (single tool call vs multi-step chain)
2. Decide on **one** of these implementations:
   - **Option 1 (simple)**: single-shot tool selection: LLM -> tool -> return output string + results.
   - **Option 2 (chain)**: multi-step plan: maintain `observations` and `allResults` arrays explicitly and enforce a step cap.
3. Add basic compile-time checks by running `go test ./...` and `go build ./cmd/mai`.

### B) `internal/cognition/verifier.go` fails open on JSON parsing
**Why**: On `json.Unmarshal` failure it returns:
- `IsValid: true`
- `Confidence: 0.5`

This means verification guardrails can silently degrade and allow incorrect content.

**Fix**
- Change the default parse-failure behavior to fail closed:
  - `IsValid: false`
  - `Confidence: 0.0`
- Or, if you want “keep running” behavior, at least:
  - set `IsValid: false`
  - include an `issues` entry like `"invalid_verifier_json"`

### C) `cmd/mai/main.go` excerpt appears corrupted/truncated in-session
**Why**: The snippet we saw included odd/incorrect identifiers (e.g., a call like `makNaNtext(...requestBody...)`) suggesting the displayed file content may be incomplete due to chat truncation or earlier tool issues.

**Fix**
- Re-read the **full** `cmd/mai/main.go` from disk.
- Ensure:
  - variables (`requestBody`, `client`, etc.) are declared correctly
  - audio/ASR/TTS state machine compiles and is race-safe
- Finally run `go build ./cmd/mai` to confirm.

## 3) Correctness & safety improvements (recommended)
### D) Privacy/hybrid routing: prompt sensitivity vs structured payloads
**Observation**
- `internal/llm/hybrid.go` checks `guard.IsSensitive(prompt)`.
- Sensitive content can still appear in **tool params**, **tool observations**, or **structured action inputs** if they are concatenated into the prompt after the check.

**Fix**
- Apply privacy checks to **the fully constructed prompt actually sent to LLM** (including tool/observation context).
- For structured generation, ensure routing checks include the entire schema and context prompt body.

### E) RAG filtering is simplistic and may remove useful memory
**Observation**
- `internal/memory/rag.go` filters:
  - skips `user_input` entries < 100 chars
  - skips entries < 20 chars
  - “context relevant” uses word overlap heuristic

**Fix**
- Consider:
  - a type-aware filter (e.g., allow short “facts”)
  - normalize and stem question words
  - compute relevance based on embedding similarity score thresholds, not only word overlap.

## 4) Performance improvements (optional)
### F) `internal/memory/semantic.go` persists embeddings on every insert
**Observation**
- `AddFact()` calls `save()` which writes the whole `semantic_vectors.json` every time.

**Fix**
- Batch writes:
  - write every N facts
  - or flush on shutdown / periodic timer
- Or switch to a lightweight DB format for vectors.

### G) `internal/events/bus.go` request/response is not correlated beyond response type
**Observation**
- `RequestResponse()` subscribes to `request.Type + ".response"` and returns the first matching event.
- There is no request ID correlation.

**Fix**
- Add a `request_id` field:
  - publish `request_id`
  - only accept response events with the same request_id

## 5) Suggested next engineering steps (practical)
1. **Make the project compile**:
   - `go test ./...`
   - `go build ./cmd/mai`
2. **Fix function_calling.go** so it compiles and matches intended behavior.
3. **Harden verifier** fail behavior (fail closed or include issues).
4. **Re-read cmd/mai/main.go** and confirm runtime wires are correct.
5. Add minimal smoke tests (no heavy mocks required):
   - planner JSON parsing
   - ReAct loop tool call execution contract (mock LLM + mock ToolRegistry)
   - event bus request/response correlation (after adding request_id)

## 6) Where to look in the code (quick index)
- Runtime wiring / control:
  - `cmd/mai/main.go`
  - `cmd/mai/actions.go`
  - `internal/agent/loop.go`
- Cognition:
  - `internal/cognition/react.go`
  - `internal/cognition/planner.go`
  - `internal/cognition/function_calling.go`
  - `internal/cognition/prompt_engine.go`
  - `internal/cognition/verifier.go`
- LLM:
  - `internal/llm/factory.go`
  - `internal/llm/hybrid.go`
  - `internal/llm/{ollama,openai,gemini,claude}.go`
- Memory:
  - `internal/memory/{working,episodic,semantic,rag,manager}.go`
- Tools:
  - `internal/tools/registry.go`
  - `internal/tools/adapters/*`
  - `internal/tools/mcp/client.go`
- Perception/personality/tts:
  - `internal/perception/{bridge,vision}.go`
  - `internal/personality/{emotion_detector,prosody_analyzer,tts_adapter}.go`
  - `internal/tts/engine.go`

---

## Summary of most important fixes
1. **Fix/repair** `internal/cognition/function_calling.go` (appears inconsistent/truncated).
2. **Change verifier** default behavior to not fail open on JSON parse errors.
3. **Re-validate** `cmd/mai/main.go` full content and compile the project.

These changes will immediately improve reliability, compilation confidence, and safety of the agent loop.
