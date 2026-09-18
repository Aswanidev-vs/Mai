# Full-Duplex Voice in Mai — Implementation Guide

> How to bring OpenAI GPT-Live–style "listen while speaking" conversation to Mai's
> fully-offline, cascaded pipeline (KWS → VAD → ASR → LLM → TTS).
>
> **TL;DR:** True full-duplex is a *model architecture* property (one speech model that
> listens and speaks in the same forward pass). Mai can't replicate that exactly with
> separate VAD/ASR/LLM/TTS stages — but it can (and mostly already does) implement the
> *effective* full-duplex pattern that OpenAI's Realtime API ships to developers:
> open mic during playback → AEC → barge-in → truncate → hand off to ASR.
> This guide closes the remaining gaps.

---

## Table of Contents

1. [Background: two kinds of "full duplex"](#1-background-two-kinds-of-full-duplex)
2. [What Mai already has (current state)](#2-what-mai-already-has-current-state)
3. [Target architecture](#3-target-architecture)
4. [Phase 1 — Cancel the in-flight LLM generation on barge-in](#phase-1--cancel-the-in-flight-llm-generation-on-barge-in)
5. [Phase 2 — Truncate the response on interruption](#phase-2--truncate-the-response-on-interruption)
6. [Phase 3 — Semantic turn gating (the `semantic_vad` equivalent)](#phase-3--semantic-turn-gating-the-semantic_vad-equivalent)
7. [Phase 4 — Backchannel without aborting (the "GPT-Live feel")](#phase-4--backchannel-without-aborting-the-gpt-live-feel)
8. [Phase 5 — Companion/browser path parity](#phase-5--companionbrowser-path-parity)
9. [Phase 6 (optional endgame) — Native duplex speech model](#phase-6-optional-endgame--native-duplex-speech-model)
10. [Config reference](#config-reference)
11. [Testing & validation](#testing--validation)
12. [Tuning cheatsheet & troubleshooting](#tuning-cheatsheet--troubleshooting)
13. [References](#references)

---

## 1. Background: two kinds of "full duplex"

| | ChatGPT Voice (GPT-Live, 2026) | OpenAI Realtime API | Mai (target) |
|---|---|---|---|
| Duplex model? | ✅ Native — one streaming speech model listens & speaks simultaneously | ❌ Half-duplex model + server VAD | ❌ Cascaded stages |
| Interruption mechanism | Model itself senses speech mid-output | `server_vad` + `interrupt_response: true` → cancel + truncate | AEC residual RMS gate → stop playback → ASR |
| Turn detection | None (removed from audio path) | Server VAD / Semantic VAD | Silero VAD + (proposed) semantic gating |
| Context truncation on barge-in | Implicit (model state) | `conversation.item.truncate` event | **Phase 2 of this guide** |
| Backchanneling while speaking | Native | Not supported | **Phase 4 of this guide** |

**Key insight:** OpenAI's engineering post (*"How we built a realtime system for responsive
voice AI in six months"*, Aug 3 2026) states GPT-Live's voice model "is full-duplex, which
means it can listen and speak at the same time" — that requires the duplex model
architecture itself and cannot be reproduced by a pipeline. The Realtime API instead
*simulates* the experience: keep consuming user audio while the model talks, detect real
speech via VAD over the assistant's voice, cancel the in-flight response, and truncate the
transcript. Mai is already most of the way to the simulated pattern; this guide completes
it and sketches the native path as an optional endgame.

---

## 2. What Mai already has (current state)

Everything below **exists today** — verified against the codebase (branch `v1`):

| Capability | Where | Status |
|---|---|---|
| Mic stays open during TTS playback | `cmd/mai/main.go` `handleAudioFrame` (≈L1358) | ✅ |
| Echo reference ring of played samples | `cmd/mai/aec.go` `speakerRef` / `refBuffer` (L5–48, L161) | ✅ |
| NLMS acoustic echo cancellation | `cmd/mai/aec.go` `EchoCanceller.Process` (L97–159) | ✅ |
| Echo-coherence guard on barge-in (rejects her own leaked voice) | `cmd/mai/aec.go` `EchoCanceller.EchoCoherence` + `barge_in_echo_max` in `cmd/mai/main.go` | ✅ |
| Barge-in detection (warmup + sustained residual gate) | `cmd/mai/main.go` ≈L1380–1444 | ✅ |
| Stop playback on barge-in | `atomic.StoreInt32(&stopPlayback, 1)` (≈L1410) | ✅ |
| Cancel in-flight LLM turn | `orch.InterruptCurrent()` — wired `main.go:956`, called `main.go:1411`; impl `internal/agent/loop.go:1411` + `setTurnCancel` (loop.go:1404) | ✅ |
| Hand user's echo-free words to VAD+ASR | `cmd/mai/main.go` ≈L1422–1435 | ✅ |
| Truncated-turn history recording | `internal/agent/loop.go:763` `recordTruncatedTurn` | ✅ exists — needs wiring (Phase 2) |
| Streaming TTS per sentence | `internal/agent/loop.go` `publishTTS` (L1474), `handleConversation` (L787) | ✅ |
| AEC warmup / sustain / threshold config | `pkg/models/config.go` + `config.yaml` (`barge_in_*` keys) | ✅ |
| Post-TTS reverb suppression window | `cmd/mai/main.go` ≈L1450+ | ✅ |

**Remaining gaps (this guide):**

1. Verify every LLM provider stream actually aborts when its `ctx` is cancelled (Phase 1).
2. The assistant's unfinished text still lands in history as if it had been fully spoken (Phase 2).
3. Turn finalization is pure VAD silence — no semantic gating, no grace period (Phase 3).
4. Any speech over TTS aborts playback — even a 200 ms "mm-hm" (Phase 4).
5. The browser companion keeps playing queued audio after a local barge-in (Phase 5).

> Prior art: `.mimocode/plans/1784027441495-quick-wolf.md` sketches a compatible plan
> (stream opts + `InterruptCurrent()` + ASR handoff). This guide supersedes it where
> they overlap; reconcile before implementation.

## 3. Target architecture

```
                     ┌────────────────────────────────────────────────────────┐
                     │                AUDIO LOOP (fast path)                 │
  mic ──► AEC ──► RMS gate ──┬──► VAD ──► ASR ──► semantic gate ──► turn text │
           ▲                 │                                               │
           │                 │  (while TTS is playing:)                      │
 speaker   │     brief burst (< backchannel_ms)?  ──► NOD ONLY (keep talking)│
 ref       │     sustained (≥ sustain_ms)?        ──► BARGE-IN               │
 (aec.go)  │                 │        │                                      │
           └─────────────────│────────┘                                      │
                             ▼                                               │
                stopPlayback=1 ──► player halts ──► browser "audio_stop"     │
                             ▼                                               │
                InterruptCurrent() ──► turn ctx cancelled ──► stream aborted │
                             ▼                                               │
                capture spokenPrefix ──► recordTruncatedTurn(user, heard)    │
                      (Phase 2)                                              │
                     ┌────────────────────────────────────────────────────────┤
                     │            SLOW PATH (async, non-blocking)             │
                     │   LLM (Ollama / cloud) · tools · memory · RAG          │
                     └────────────────────────────────────────────────────────┘
```

This mirrors GPT-Live's split: a **dedicated fast audio lane** that never blocks on
reasoning, and an **asynchronous reasoning lane** whose latency cannot stall speech.

---

## Phase 1 — Cancel the in-flight LLM generation on barge-in

**Goal:** when barge-in fires, the LLM must stop generating immediately — not just the
audio player. Otherwise Mai keeps "thinking" and queues sentences the user already
cancelled.

**Current state:** `Orchestrator.setTurnCancel` (loop.go:1404) stores a per-turn
`context.CancelFunc`; `InterruptCurrent` (loop.go:1411) calls it. Barge-in already
invokes it (`main.go:1411–1413`). **What to verify/improve:**

1. **Every provider must respect `ctx`.** Audit `internal/llm/*.go`:
   - `ollama.go`, `openai.go`, `claude.go`, `gemini.go`, `hybrid.go` — each streaming
     call must pass the turn `ctx` into the HTTP request
     (`http.NewRequestWithContext`) and check `ctx.Err()` inside the token-read loop,
     returning promptly on cancellation.
   - If any provider spins its own `context.Background()`, the cancel is a no-op and
     generation continues in the dark — the classic silent bug.

2. **Drop sentences generated before cancellation.** In `handleConversation`
   (loop.go:787), the streaming loop speaks completed sentences as they arrive. After a
   cancel, the loop must stop flushing the sentence buffer:

   ```go
   // inside the token loop, after each sentence boundary:
   select {
   case <-ctx.Done():
       return // abandon buffered partial sentence too
   default:
   }
   ```

3. **Tag-and-drop stale TTS.** `publishTTS` already tags chunks with a monotonic
   `turnSeq` (see `Speak`, loop.go:1549–1552). The player must drop any queued chunk
   whose `turnSeq` is older than the current turn once `stopPlayback` is set —
   otherwise a sentence that raced the barge-in still plays.

4. **Unit test first** (mirroring `cmd/mai/audio_test.go` style):

   ```go
   func TestInterruptCurrent_StopsStream(t *testing.T) {
       // orchestrator with fake provider whose Stream blocks on read
       // call InterruptCurrent, assert:
       //  - Stream returns within ~50ms
       //  - no further publishTTS calls arrive
   }
   ```

**Files:** `internal/agent/loop.go`, `internal/llm/*.go`, `cmd/mai/main.go` (player).
**Risk:** low. Mostly verification + small guards.
**Definition of done:** speaking a long answer, interrupting mid-way → LLM request
aborts (visible in Ollama logs), silence within one sentence, ready to listen.

## Phase 2 — Truncate the response on interruption

**Goal:** the Realtime API's `conversation.item.truncate` equivalent. After a barge-in,
the conversation history must record **only what the user actually heard**, not the full
intended response — otherwise Mai believes she completed the sentence and may resume the
thought next turn.

**Current state:** `Orchestrator.recordTruncatedTurn` (loop.go:763) already exists
(`user`, `heard`, `chatMessages` params). It is not invoked by the barge-in path today.

**How to implement:**

1. **Track the spoken prefix.** The TTS player knows exactly which sentence chunks have
   *started* playback. When a sentence `S_k` begins playing, record the cumulative text
   spoken so far into a shared slot, e.g. in `cmd/mai/main.go`:

   ```go
   var spokenTextMu sync.Mutex
   var spokenTextSoFar string // what the user has actually heard
   ```

   Update it in the player loop as each sentence starts, and publish it to the
   orchestrator through the event bus (or a setter such as `orch.SetSpokenText(...)`).
   With browser playback there is local drift; when uncertain, **over-truncate** (record
   slightly *less* than was probably heard). Cutting the assistant's turn a bit shorter
   than reality is harmless; keeping text the user never heard teaches Mai a wrong
   memory.

2. **On barge-in, record the truncated turn.** In the barge-in block (main.go ≈L1403–1421),
   after `interruptCurrent()`:

   ```go
   spokenTextMu.Lock()
   heard := spokenTextSoFar
   spokenTextSoFar = ""          // reset for the next response
   spokenTextMu.Unlock()

   // heard is what was actually played; the LLM's full reply is discarded.
   if orch != nil {
       orch.RecordInterruptedTurn(userHeardSoFar, heard) // thin wrapper
   }
   ```

   The wrapper delegates to the existing `recordTruncatedTurn(user, heard, chatMessages)`
   and (optionally) publishes an event so the companion UI can show a "was cut off"
   badge. Note: `userHeardSoFar` may be empty at barge-in time — the user's actual
   utterance arrives later via VAD/ASR. Two options:
   - **Option A (simple):** record the truncated assistant turn immediately with the
     partial user text known so far; the interrupted user speech that follows becomes
     the *next* user turn via normal ASR flow (this is what the current code does with
     `sessionText`).
   - **Option B (accurate):** stash `(heard)` in a pending-truncation slot and let
     `finalizeTurn` attach it to the *completed* user turn that triggered the barge-in.
     More accurate memory, slightly more state.

3. **Persist nothing else.** Do **not** `storeResponse(fullReply)` when a turn was
   interrupted. Guard `storeResponse` (loop.go:653) with an "interrupted" flag so a
   cancelled stream never writes a full-response memory entry.

**Files:** `cmd/mai/main.go`, `internal/agent/loop.go`.
**Definition of done:** interrupt Mai mid-explanation, then ask "what was I saying?" —
she references the *truncated* turn, not the never-spoken tail.

---

## Phase 3 — Semantic turn gating (the `semantic_vad` equivalent)

**Goal:** OpenAI's `semantic_vad` uses a classifier to judge whether the user is *done*
rather than relying on raw silence. Mai can approximate it cheaply, fully offline:
a short post-silence "grace period" plus lexical/energy heuristics that decide whether
to respond immediately, wait, or ignore.

**Where:** turn finalization currently happens when Silero VAD declares end-of-segment
(`cmd/mai/main.go`, the `finalizeTurn` call site around L1349).

**Implementation sketch:**

1. **Grace period before firing the LLM.** After VAD end-of-speech, hold the turn for
   `semantic_gate_ms` (e.g. 300–500 ms). If speech resumes within the grace window, keep
   accumulating into the same turn instead of splitting it:

   ```go
   timer := time.NewTimer(semanticGate)
   select {
   case <-resumeCh:      // VAD re-triggered; continue same turn
   case <-timer.C:       // truly done → finalizeTurn()
   }
   ```

2. **Trailing-word heuristics.** If the transcript ends with continuation markers
   — `"and"`, `"but"`, `"so"`, `"uh"`, `"um"`, trailing comma-like prosody — extend the
   grace window (×2) and don't fire yet. Cheap, effective. The existing
   `ClassifyInterrupt` (internal/agent/interrupt.go:174) is a precedent for this
   keyword approach; a tiny `classifyContinuation(text) bool` helper belongs in the
   same file.

3. **Energy/prosody check (optional).** `internal/personality/prosody_analyzer.go`
   already computes prosody features — a rising-vs-flat terminal contour can gate
   whether the silence is a real end-of-utterance. Start lexical-only; add prosody
   only if needed.

4. **Config:** add `audio.semantic_gate_ms` (0 = disabled → current behavior). Keep the
   default modest (300 ms) so latency barely changes; the total response latency is
   dominated by LLM first-token time anyway.

**Files:** `cmd/mai/main.go` (finalize path), `internal/agent/interrupt.go` (helper).
**Definition of done:** saying "remind me to… um…" then pausing doesn't produce a
half-turn response; the same sentence completed 400 ms later is treated as one turn.

---

## Phase 4 — Backchannel without aborting (the "GPT-Live feel")

**Goal:** not every sound over Mai's voice is an interruption. A brief "mm-hm",
"yeah", or laugh should make Mai *acknowledge* (nod/viseme in the VRM UI) and **keep
talking** — only sustained speech aborts. This is what makes full-duplex feel human.

**Current state:** any residual burst ≥ `barge_in_sustain_ms` (150 ms) over TTS
triggers a full barge-in. There is no "short burst = backchannel" distinction.

**Implementation:**

1. **Classify bursts by duration.** In `cmd/mai/main.go`, split the barge-in gate:

   ```go
   const (
       backchannelMax = 450 * time.Millisecond // burst shorter than this ...
       // ... = backchannel; at/above it = real interruption
   )
   // sustain is already tracked via bargeStartNano; when held >= bargeInSustain:
   switch {
   case held >= backchannelMax:
       // real barge-in (existing path)
   case held >= bargeInSustain:
       // BACKCHANNEL: do not stop playback.
       //  - publish a "backchannel" event on the bus (UI nods / smiles)
       //  - rate-limit: max 1 per 2s, and never twice in a row
       //  - optionally record it (user model: user is engaged)
   }
   ```

2. **Never feed backchannel audio to ASR.** The residual that tripped the gate should
   be *dropped* in this branch (don't push to `vadBuffer`/`asrStream`), otherwise the
   "mm-hm" becomes a phantom user turn. Only the real barge-in branch keeps feeding
   ASR as it does today.

3. **UI side (companion):** handle the `backchannel` event in `docs/js` — trigger the
   existing spring-damper head-nod / blink pattern rather than a new animation.

4. **Tuning:** `backchannelMax` too low → "stop!" gets nodded at instead of obeyed.
   Start at 450 ms; the lexical check from Phase 3 (`stop|wait|cancel`) can override
   any backchannel classification once ASR completes — but that only helps *after*
   playback stopped... so the safest rule is: **duration decides live**, and short
   urgent words lose out. Document this trade-off in config comments.

**Files:** `cmd/mai/main.go`, `internal/server/protocol.go` (event), `docs/js` (UI).
**Definition of done:** saying "mm-hm" mid-answer → Mai keeps talking and nods; saying
"a full sentence" → she stops and listens.

---

## Phase 5 — Companion/browser path parity

**Goal:** the browser companion (WebSocket mic → `handleAudioFrame` → TTS chunks back)
must react to barge-in identically to the local path, and *its* interruption (user
speaks into the browser mic while Mai's voice plays in the browser) must stop local
playback too.

**Current state:** browser mic frames flow through the same `handleAudioFrame`, so
AEC + barge-in detection work the same — but playback stop is local-only.

**Implementation:**

1. **Stop signal to the browser.** When barge-in fires, the hub should emit an
   `audio_stop` event (like the `.mimocode` plan suggests: emit a "stop" chunk in the
   streaming `speak` branch). The browser client, on `audio_stop`, clears its audio
   queue and stops the current element immediately.

2. **Reciprocal barge-in.** If the browser plays Mai's voice and the *browser* mic
   detects sustained speech (same residual gate), the server must also stop local
   playback (`tts_play_local_always: true` means both paths play — so both must stop).
   The `stopPlayback` atomic already gates the local player; extend the browser stop
   event to the same trigger point.

3. **Truncation parity.** The `spokenTextSoFar` tracking from Phase 2 must count
   browser-played sentences too (the player publishes chunks to the browser *and*
   tracks them the same way), so truncation is correct regardless of which path
   played the audio.

4. **Latency:** browser → WS → server AEC → barge-in adds ~50–100 ms vs local mic.
   Not user-noticeable for interruption; acceptable.

**Files:** `internal/server/hub.go`, `internal/server/protocol.go`, `cmd/mai/main.go`,
`docs/js/audio.js` (client stop handler).
**Definition of done:** interrupt via browser mic while companion UI is open →
both browser audio and local speaker stop; truncated turn recorded once (not twice).

---

## Phase 6 (optional endgame) — Native duplex speech model

**Goal:** replace the cascaded voice front-end with a single full-duplex speech model
(Moshi/GLM-4-Voice class) that listens and speaks simultaneously, while keeping Mai's
existing agentic brain.

**Reality check:** this is a research-grade change. Current open duplex models are
substantially weaker than Mai's ASR (Qwen3/Nemotron) + LLM (Ollama) + TTS (Pocket TTS
cloning) stack on general knowledge and quality. Treat it as an experiment, not a
replacement. Two viable shapes:

**Option A — Duplex front-end + Mai's brain (the GPT-Live shape):**
```
mic ⇄ [duplex speech model] ⇄ user   ← handles timing, barge-in, backchannel natively
          │
          └─ when a complete user turn (or the model's "hand-off" token) is ready:
                → text goes to Ollama LLM (async, non-blocking)
                → LLM response streams back as text
                → duplex model speaks it (or hand off to Pocket TTS for the cloned voice)
```
- The duplex model owns turn-taking; the LLM owns intelligence — exactly how GPT-Live
  delegates to GPT-5.5.
- ONNX export of Moshi (kyutai) exists; sherpa-onnx has no native duplex support yet,
  so this would run via onnxruntime directly (the project already links it).

**Option B — Streaming everything, keep cascaded stages:**
- Streaming ASR (already have) → sentence-level LLM streaming (already have) →
  sentence-level TTS streaming (already have). This is Mai's current streaming stack;
  Phase 1–3 make it robust. The gap to GPT-Live is then only backchannel + native
  timing (Phase 4 / Phase 6-A).

**Recommendation:** finish Phases 1–5 first. Re-evaluate Phase 6-A when a strong open
duplex model with good ONNX support and zh/en coverage lands (track kyutai/Moshi and
GLM-4-Voice releases).

---

## Config reference

All keys under `audio:` in `config.yaml` (mirrored in `config.example.yaml`, fields in
`pkg/models/config.go`):

| Key | Default | Purpose |
|---|---|---|
| `barge_in_enabled` | `true` | Master switch for interruption during TTS |
| `barge_in_threshold` | `0.008` | Residual RMS gate — echo-cancelled mic energy |
| `barge_in_warmup_ms` | `400` | AEC convergence wait before detection arms |
| `barge_in_sustain_ms` | `150` | Sustained residual required to confirm real interruption |
| `tts_play_local_always` | `true` | Play locally even when companion UI is open |
| `capture_buffer_ms` | `100` | Mic frame size feeding the audio loop |

**Proposed additions (this guide):**

| Key | Proposed default | Purpose |
|---|---|---|
| `semantic_gate_ms` | `300` (0 = off) | Phase 3 — grace period before finalizing a turn |
| `backchannel_max_ms` | `450` | Phase 4 — bursts shorter than this are nods, not interruptions |
| `backchannel_enabled` | `true` | Phase 4 — master switch for the backchannel branch |
| `interrupt_truncate` | `true` | Phase 2 — record truncated turns in history |

Example block to append to `config.yaml`:

```yaml
audio:
  # ... existing keys ...
  semantic_gate_ms: 300
  backchannel_enabled: true
  backchannel_max_ms: 450
  interrupt_truncate: true
```

---

## Testing & validation

**Unit tests** (follow existing conventions in `cmd/mai/audio_test.go`, `internal/agent/loop_test.go`):

| Test | Asserts |
|---|---|
| `TestInterruptCurrent_StopsStream` | Stream returns < 50 ms after `InterruptCurrent()`; no further `publishTTS` |
| `TestRecordInterruptedTurn_TruncatesHistory` | History contains only the spoken prefix, not the full reply |
| `TestSemanticGate_HoldsTurn` | Turn with "…um…" tail + resumed speech stays one turn |
| `TestBackchannel_NoPlaybackStop` | 300 ms burst → `stopPlayback` stays 0, nod event published |
| `TestBargeIn_StopsPlayback` (exists) | Existing behavior preserved (regression guard) |

**Live validation checklist** (speaker + mic, after each phase):

1. **Interrupt mid-sentence:** Mai explains something long → speak over her → she stops
   within ≤ 1 sentence, listens, answers the new question. (Phases 1+2)
2. **No ghost continuation:** after interrupting, ask "what was I saying?" — she refers
   only to the part she actually said. (Phase 2)
3. **Hum trailing off:** say "remind me to… um…" then pause — no half-answer fires;
   complete the sentence → single response. (Phase 3)
4. **Backchannel:** say "mm-hm" while she speaks — she keeps talking and nods in the UI;
   full sentence → she stops. (Phase 4)
5. **Browser parity:** same interrupt test with companion UI open, audio in browser —
   both paths stop; no double truncation. (Phase 5)
6. **False-positive soak:** play music loudly during her speech — no barge-in fires
   (AEC + sustain gate hold).

Run:

```powershell
go test ./cmd/mai/ ./internal/agent/ ./internal/llm/ -run 'Interrupt|Barge|Backchannel|Semantic|Truncat' -v
go build -o mai_test_build.exe ./cmd/mai
./mai_test_build.exe --companion
```

---

## Tuning cheatsheet & troubleshooting

| Symptom | Likely cause | Fix |
|---|---|---|
| Mai interrupts herself (echo loop) | AEC not converged / reference misaligned | Raise `barge_in_warmup_ms` (400 → 600); check `refBuffer` push covers local *and* browser playback |
| Barge-in never fires | Threshold too high / quiet user | Lower `barge_in_threshold` (0.008 → 0.005); check mic level |
| Barge-in fires on her own voice | Residual leak through AEC | Raise the `bargeInMargin` (2.5) in `cmd/mai/main.go:138`; or `barge_in_sustain_ms` |
| "mm-hm" stops her (Phase 4) | `backchannel_max_ms` too high | Lower it (450 → 300) |
| "stop!" gets nodded at (Phase 4) | `backchannel_max_ms` too low | Raise it; accept the trade-off (live duration decides) |
| Responses to trailing "um…" cut the user off | `semantic_gate_ms` = 0 | Set 300–500; add continuation-word list |
| LLM keeps generating after barge-in | Provider ignores `ctx` | Phase 1 audit — `http.NewRequestWithContext` in all `internal/llm/*.go` |
| History shows unspoken text | Truncation not wired | Phase 2 — guard `storeResponse`, call `recordTruncatedTurn` |
| Browser keeps playing after barge-in | Missing stop event | Phase 5 — `audio_stop` on the hub + client handler |

---

## References

- OpenAI — *How we built a realtime system for responsive voice AI in six months*
  (Aug 3 2026, Justin Uberti & Zahan Malkani): GPT-Live architecture, full-duplex voice
  model, async delegation. https://openai.com/index/continuous-voice-interaction-with-gpt-live/
- OpenAI Realtime API docs — *Voice activity detection*: `server_vad`, `semantic_vad`,
  `interrupt_response`, `create_response`. https://developers.openai.com/api/docs/guides/realtime-vad
- OpenAI — *Introducing GPT-Live* (July 8 2026 product announcement).
- Independent analysis — *Inside GPT-Live: How OpenAI Rebuilt ChatGPT's Voice Stack for
  Full-Duplex Conversation* (dev.to, Aug 6 2026).
- Prior internal plan: `.mimocode/plans/1784027441495-quick-wolf.md` (superseded by this
  guide where they overlap).
- Moshi (Kyutai) — open duplex speech model: https://github.com/kyutai-labs/moshi
- Project docs: `E:\Mai\ARCHITECTURE.md`, `E:\Mai\README.md` (Companion Mode section).

---

*Guide for Mai (branch `v1`). Written against code verified on 2026-09-18; line numbers
are approximate and may shift — symbol names are authoritative.*

