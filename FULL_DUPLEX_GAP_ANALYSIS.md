# Full-Duplex Gap Analysis

**Date:** 2026-09-26
**Scope:** Feasibility audit of the proposed full-duplex architecture against the current codebase.
**Method:** Read-only static audit of `cmd/mai/`, `internal/`, `pkg/`, `config.yaml`, and the pinned
`sherpa-onnx-go` / `sherpa-onnx-go-windows@v1.13.5` bindings. No code was modified.

> **Note on existing docs:** `README.md`, `FULL_DUPLEX_GUIDE.md`, and `DUPLEX_IMPLEMENTATION_PLAN.md`
> contain several claims that do not survive verification against the code. Where they conflict, this
> document cites the code. Specific contradictions are listed in
> [Corrections to existing docs](#corrections-to-existing-docs).

---

## Verdict

**Yes, feasible — but not as written.** Roughly 70% of the proposed architecture already exists in
working code, and in several places the existing implementation is *better* than the design doc
assumes. There is one hard model-level blocker, one config-level blocker, and one architectural
blocker that the project's own plan file already names as a non-goal.

The output half (streaming, turn IDs, supersession, cancellation, async TTS pipeline) is solid and
does not need rebuilding. The input half (session manager, state machine, off-callback pipeline,
partial-transcript transport) is net-new work.

---

## Requirements matrix

What the architecture doc claims, versus what the code actually does.

| Requirement | Status | Evidence |
|---|---|---|
| Concurrent capture + playback | **Already works** | Separate malgo contexts/devices, separate threads — `audio.go:33` vs `audio.go:150` |
| Echo cancellation | **Already works, better than expected** | Real NLMS adaptive filter, 4096 taps — `aec.go:136-221`; coherence double-talk detector `aec.go:247` |
| Ring buffer / pre-roll | **Already works** | 4 s echo ring `aec.go:292`; 1.5 s lookback `main.go:231`; 10 s VAD circular buffer `main.go:294` |
| DeepFilterNet 8 streaming | **Implemented** | sherpa `OnlineSpeechDenoiser`, incremental via `FrameShiftInSamples()` — `denoise.go:202-245` |
| Barge-in detection + halt | **Already works** | Warmup + sustain + coherence gate `main.go:1760-1847`; sets `stopPlayback` `main.go:1819`; cancels LLM `main.go:1820` |
| Event bus | **Already exists** | `internal/events/bus.go` — pub/sub, unsubscribe, panic isolation, concurrency-tested |
| LLM token streaming | **Already exists** | All four providers; `pkg/interfaces/llm.go:19` |
| Per-turn cancellation | **Already exists** | `loop.go:1457-1471`; ctx-cancellable HTTP in every provider |
| Incremental TTS from LLM stream | **Works at sentence granularity** | `onChunk` → `takeSentence` → `publishTTS` — `loop.go:939-964` |
| **Partial / interim transcripts** | ❌ **Blocked today** | `config.yaml:67` sets `active_model: "qwen3"` → `OfflineRecognizer` |
| **Token-in / frame-out streaming TTS** | ❌ **Blocked by the model** | See [Blocker 1](#blocker-1--streaming-tts-is-a-model-property) |
| **Session manager + state machine** | ❌ **Does not exist** | Two local states only — `main.go:1512-1519`; `StatusSpeaking`/`StatusListening` declared, never assigned |
| **Off-callback audio front end** | ❌ **Does not exist** | Mic→AEC→VAD→ASR runs serially on the miniaudio callback thread under `sherpaMu` — `main.go:1996`, `main.go:1721-1722` |

---

## The three blockers

### Blocker 1 — Streaming TTS is a model property

The live TTS is `sherpa.NewOfflineTts` with Pocket (`config.yaml:134`, `main.go:526`). The binding
only exposes `Generate` / `GenerateWithCallback` / `GenerateWithConfig` — full text in, complete
audio out. There is no push/feed/stateful-text entry point and `OfflineTtsConfig` holds no stream
state.

Underneath, `offline-tts-pocket-impl.h:308` encodes the *entire* sentence up front via
`GetTextEmbedding`, and LM state is re-initialized per sentence (`:310`). The progress callback fires
only in the decoder loop, in fixed 15-frame chunks (`:400`, `:412-439`).

**Consequence:** the proposed `TTS interface { Synthesize(ctx, text <-chan string) ... }` —
synthesizing text the LLM has not finished writing — cannot be honored by this engine at any effort
level. Sentence-level chunking is the ceiling, and it is already implemented.

This is acceptable in practice: it bounds latency to *time to first sentence* rather than *time to
complete response*, which is where most of the perceived-speed win lives anyway.

### Blocker 2 — Partial transcripts are one config line away

`config.yaml:67` sets `active_model: "qwen3"` → `sherpa.NewOfflineRecognizer` (`main.go:308-326`),
which requires a complete utterance and emits one final transcript.

The streaming plumbing **already exists and works** — `updateLiveASR` (`main.go:1569-1582`) runs
`IsReady → Decode → GetResult` against `asrStream`, and both
`sherpa-onnx-nemotron-3.5-asr-streaming-0.6b-560ms-int8-2026-06-11` and
`sherpa-onnx-streaming-zipformer-en-2023-02-21` are already downloaded locally. The `nemotron` and
`transducer` branches are complete in code (`main.go:328-361`, `main.go:379-409`) but dead under the
shipped config.

**The catch:** partials currently reach only `log.Printf` (`main.go:1579`). There is no
partial-transcript transport — `internal/server/protocol.go:22-40` defines no such message type. So
this is a config change **plus** a small real addition, not a toggle.

### Blocker 3 — The input half runs on the audio callback thread

`capture.onSamples = func(samples){ handleAudioFrame(samples, false) }` (`main.go:1996`) executes
AEC → denoise gate → VAD → ASR decode → state machine inline, serialized by `sherpaMu`
(`main.go:1721-1722`).

Any slow call stalls capture and drops audio. During playback this is exactly when the mic most needs
to stay alive. `DUPLEX_IMPLEMENTATION_PLAN.md:47` explicitly lists this as a non-goal — which is
precisely the thing that makes true duplex hard.

---

## The five properties that define full duplex

1. **Input never stalls** regardless of what output is doing
2. **One authoritative turn owner** arbitrates overlap between the two sides
3. **Output is revocable at any instant** — not merely "stops soon"
4. **You know what the user is saying while you're still talking**
5. **The assistant can never hear itself** ← already done (`aec.go:136`, `aec.go:247`)

Every item in the gap list below maps to one of these.

---

## 🔴 Tier 0 — Keystones

Nothing else works until these land.

### 1. Move the audio front end off the miniaudio callback thread

- **Missing:** `main.go:1996` — the entire capture→AEC→VAD→ASR chain runs inline under `sherpaMu`.
- **Why it blocks duplex:** when a decode stalls for 300 ms, the mic *misses audio*. You cannot barge
  in on speech that was never captured. Any slow call becomes a dropped frame.
- **Shape:** capture goroutine → bounded `chan []float32` → a single processing goroutine owning
  AEC/VAD/ASR. A ring buffer between them absorbs jitter.
- **Fix in the same pass:** frame size is never set (`PeriodSizeInFrames` / `FramesPerBlock` have zero
  call sites), so capture runs at miniaudio's ~1024-frame default (~64 ms @ 16 kHz) against a
  denoiser hop of ~160 samples (`denoise.go:227`). Set an explicit period.
- **Size:** largest single item. Every other duplex behavior depends on it.

### 2. A session manager that owns turn identity

- **Missing:** no turn object exists. IDs are scattered across `ttsSeq` (`loop.go:100`),
  `latestTurn` (`main.go:788`), and bare atomics `isSpeaking` / `ttsPlaying` / `stopPlayback`
  (`main.go:114-123`).
- **Why it blocks duplex:** when user and assistant overlap, *whose turn is it?* Without one owner
  there is nowhere to hang the cancel func, and no way to classify audio as echo, barge-in, or
  backchannel. Both barge-in and partial transcripts depend on this.
- **Shape:** a `Turn{ID, Ctx, Cancel, StartedAt, State}` with input and output workers both
  subscribing to it. The doc's `internal/session/` is the right idea — but it must **replace** the
  atomics, not sit beside them.
- **Do not** build the doc's fresh `internal/bus/` + `internal/pipeline/` + `internal/asr/` tree.
  `internal/events/bus.go` already exists and already carries the voice path.

### 3. Real playback flush

- **Missing:** interruption is a cooperative flag, not a flush. `audio.go:170-173` fills silence and
  keeps the device running; `buf` is never cleared; there is no `device.Stop()`.
- **Why it blocks duplex:** anything already queued keeps playing after the user cuts in. The
  assistant talks over the first ~0.6 s of the user's sentence — which reads as "it didn't hear me,"
  the exact failure barge-in exists to prevent.
- **Shape:** clear the staging buffer plus a device-level flush/reopen. Then bound the buffer to
  ~200 ms and accept a ~1-frame join seam.

---

## 🟡 Tier 1 — The duplex features themselves

### 4. Partial transcripts with a real transport

- **Missing:** live ASR emits only to `log.Printf` (`main.go:1579`). No partial-transcript message
  type exists — `internal/server/protocol.go:22-40`.
- **Why it blocks duplex:** without partials the assistant can only respond to what the user
  *finished* saying. That is half-duplex with a fast turn.
- **Prerequisite:** flip `config.yaml:67` `active_model: "qwen3"` → `"nemotron"`. The model and the
  streaming code are already on disk.
- **Then:** publish partials as bus events, and add semantic turn gating so a partial does not
  trigger a reply on every "um".
- **Watch:** the qwen3 and streaming branches have different turn-finalization semantics — do not
  assume `finalizeTurn` (`main.go:1532-1562`) works unchanged.

### 5. In-flight TTS cancellation

- **Missing:** with `streaming: true`, `streamAudio` is true (`main.go:705`), so the `stopPlayback`
  check at `main.go:710` sits in a dead branch, and `renderSentence` deliberately ignores
  `stopPlayback` (`main.go:858-863`). In-flight synthesis is **not** cancelled — the CPU is paid and
  the output discarded later (`main.go:1152`).
- **Why it matters:** barge-in during a long response keeps burning TTS CPU and can bleed into the
  next turn.
- **Shape:** thread a `context.Context` through `synthesize` (it currently takes none, `main.go:700`)
  and have the callback return false on cancel. Granularity stays ~0.6 s per sentence — that ceiling
  is the model, not the code.
- **Cheap win:** also return false on `stopPlayback`, not only on turn-ID supersession
  (`main.go:864-866`).

### 6. Backchannel vs. barge-in

- **Missing:** every burst that clears the gate is a full barge-in (`main.go:1812-1822`). "Mm-hmm" and
  "wait, no" are handled identically.
- **Why it blocks duplex:** true duplex means overlapping *without* the assistant abandoning
  everything. A short-interjection path is needed that does not cancel the turn.

---

## 🟢 Tier 2 — Quality

Not blocking, but these are felt.

- **Put the denoiser on the critical path.** `g.run()` (`denoise.go:397`) keeps only the speech verdict
  and **discards the denoised samples** — ASR still receives raw mic. It is a gate, not a suppressor.
  It is also disabled entirely during playback (`main.go:1751`), i.e. off exactly when needed.
- **Don't `Reset()` the AEC on barge-in** (`main.go:1823`). That discards filter convergence at the
  precise moment double-talk is hardest to separate.
- **Publish from a goroutine.** The bus dispatches synchronously on the caller's goroutine
  (`bus.go:75-77`); `SubscribeAsync` exists but has **zero call sites**. Anything published from the
  audio callback inherits inline handler execution. (Today the code is saved only by a manual `go` at
  `main.go:1546`.)
- **Fix `HybridProvider.StreamChat`** — it is missing (`hybrid.go:27-61`), so the `ChatStreamer`
  type assertion at `loop.go:853` always fails, `useChat` is always false, and the KV-prefix-cache
  chat path is dead in production. Every turn takes the flat `BuildPrompt` route (`loop.go:896-913`).
- **Fix the `o.status` data race** (`loop.go:1649`, read cross-goroutine by
  `internal/server/bridge.go:150` with no synchronization) before adding more states on top of it.
- **Instrument before optimizing.** `RecordLatency` is called exactly once, for the LLM
  (`loop.go:464`). No ASR/VAD/TTS timing exists, so there is currently no way to tell which stage is
  the bottleneck. Cheap, and should come early.
- **Add real barge-in test coverage.** `bargein_test.go` tests the AEC/coherence heuristic against
  *hand-copied* thresholds (`bargein_test.go:30` duplicating `cfg.Audio.BargeInThreshold`), and never
  calls `handleAudioFrame`. No test covers warmup, sustain, `stopPlayback`, or LLM cancellation. It
  also mutates the package-global `refBuffer` (`aec.go:292`).

---

## ⛔ Blocked or not worth attempting

- **True token-in / frame-out streaming TTS.** Blocked by the model, not the code. See
  [Blocker 1](#blocker-1--streaming-tts-is-a-model-property). Requires a different engine.
- **Sub-300 ms end-to-end.** Bounded by the ASR model choice, not by architecture. Offline Qwen3
  alone is 50-500 ms on a *completed* utterance — the doc's illustrative ~340 ms budget is not
  reachable without switching models. A realistic target with streaming ASR is roughly 700 ms–1.2 s.

---

## Suggested order

```
1. Off-callback front end  (keystone)
2. Session manager / turn object
3. Playback flush
4. Partial transcripts      (parallel — only needs a config flip)
5. In-flight TTS cancellation
6. Backchannel vs. barge-in
```

**Reasoning:** #1 is the keystone — every other duplex behavior depends on the mic not dropping
frames. #2 provides the place to hang cancellation, which #3 and #5 then use. #4 is nearly free and
can start immediately in parallel, since it does not touch pipeline threading.

---

## Corrections to existing docs

Verified contradictions between written docs and code:

| Claim | Reality |
|---|---|
| `DUPLEX_IMPLEMENTATION_PLAN.md:15` — "LLM interface accepts text and returns text (`pkg/interfaces/llm.go:16-23`)" | **Wrong.** `LLMProvider` has had a `Stream(... callback func(chunk string))` method — `pkg/interfaces/llm.go:19`. The plan contradicts itself at its own line 97. |
| `FULL_DUPLEX_GUIDE.md:68` — truncated-turn history "✅ exists — needs wiring" | **Stale.** Already wired — `loop.go:993-1000` calls `recordTruncatedTurn` on `ctx.Err() != nil`. |
| `FULL_DUPLEX_GUIDE.md:75` — "verify every provider stream aborts on ctx cancel" | **Verified true.** All providers use `http.NewRequestWithContext` (`ollama.go:218`, `openai.go:117`, `claude.go:108`, `gemini.go:102`); `HybridProvider.Stream` forwards the caller's ctx unchanged (`hybrid.go:35-40`). |
| `FULL_DUPLEX_GUIDE.md:392` / `README.md:150` — "Streaming ASR (already have)" | **Misleading.** `config.yaml:67` selects offline Qwen3. Streaming code exists but is dead config. |
| `ARCHITECTURE.md:504` — "Add `internal/events/` event bus (no consumers yet)" | **Stale.** The bus now has 8+ subscribers, including the voice path (`loop.go:236-237`, `main.go:1361-1408`). |
| Design doc — "your existing stack is enough" | **True with caveat.** True for everything except true token-in streaming TTS. |
| Design doc — proposed `internal/session/`, `internal/bus/`, `internal/pipeline/` layout | **Would duplicate live code.** The bus and pipeline already exist; building the new tree in parallel creates two competing pipelines. |
| Design doc — state machine as an addition | **It is a replacement.** State is already carried implicitly by `isSpeaking` / `ttsPlaying` / `stopPlayback`. |
| Design doc — ~340 ms latency budget | **Not reachable** with offline Qwen3 ASR. |

---

## Related correctness risks

Found during the audit. These are bugs, not features, and they would bite during a duplex refactor.

- **`HybridProvider` has no `StreamChat`** (`hybrid.go:27-61`) → `loop.go:853` assertion always fails
  → KV-prefix-cache chat path is dead in production.
- **`o.status` data race** — unsynchronized field written at `loop.go:459` / `loop.go:463`, read at
  `loop.go:1649` → `internal/server/bridge.go:150`.
- **`SubscribeAsync` has zero call sites** (`bus.go:112`) → bus dispatch is effectively synchronous for
  everything the voice path publishes.
- **`fallbackOfflineRecognizer` is allocated then discarded** — `main.go:416-443` allocates,
  `main.go:445` discards with `_ = fallbackOfflineRecognizer`. No consumer exists.
- **`InterruptManager` is inert** (`interrupt.go`) — `SetState` (`interrupt.go:49`) is never called
  anywhere, so `isSpeaking`/`isProcessing` are always false and `CanInterrupt` takes the permissive
  idle branch (`interrupt.go:104-108`). Interrupts also only fire on keyword classification *after*
  ASR (`loop.go:490-493`), which is far too late to stop playback.
- **`stopPlayback` has multiple writers** — cleared by the player (`main.go:1487`), set by capture
  (`main.go:1819`), with no ownership discipline.
- **Silero VAD state is never reset** — no `Clear()` / `Reset()` / `Flush()` call exists anywhere;
  silence is drained only by popping queued segments (`main.go:1616-1618`, `1666-1709`).
- **Malformed-response footgun** — a bare `"..."` makes Pocket emit its 40 s maximum (~30 s CPU),
  per `loop.go:1559-1562`.

---

## What is already working — do not rebuild

- Real NLMS AEC with coherence-based double-talk detection (`aec.go:136`, `aec.go:247`). This is the
  hardest single component in full duplex and it is already correct. Most projects fail here.
- Concurrent capture and playback on separate device threads (`audio.go:33`, `audio.go:150`).
- Ring buffers throughout: 4 s echo reference, 1.5 s pre-roll lookback, 10 s VAD circular buffer.
- Per-turn LLM cancellation with truncated-turn history (`loop.go:919-924`, `loop.go:993-1000`,
  `loop.go:1457-1471`).
- Genuinely async three-stage TTS output path: renderer (prefetch depth 4) → player → device
  (`main.go:1018-1085`, `main.go:1094-1178`), with backpressure and barge-in halt.
- Token streaming in all four LLM providers, ctx-cancellable.
- Off-thread denoiser with a verdict gate (`denoise.go:339-455`).
- Working event bus with panic isolation and concurrency tests (`internal/events/bus.go`,
  `bus_test.go:115`).


Here's the gap list, ordered by dependency. "Full duplex" here means: both sides can be live simultaneously and the system never has to *choose* between listening and speaking.

---

## The five properties that define full duplex

1. **Input never stalls** regardless of what output is doing
2. **One authoritative turn owner** arbitrates overlap between the two sides
3. **Output is revocable at any instant** — not just "stops soon"
4. **You know what the user is saying while you're still talking**
5. **The assistant can never hear itself** ← already done

Everything below maps to one of these.

---

## 🔴 Tier 0 — Keystones (nothing else works until these land)

### 1. Move the audio front end off the miniaudio callback thread
- **Missing:** `capture.onSamples = func(samples){ handleAudioFrame(samples,false) }` (`main.go:1996`) runs AEC → denoise gate → VAD → ASR decode → state machine inline, serialized by `sherpaMu` (`main.go:1721-1722`).
- **Why it blocks duplex:** the moment a decode stalls for 300 ms, the mic *misses audio*. You cannot barge in on speech that was never captured. Right now any slow call is a dropped frame, and during playback this is exactly when you most need the mic alive.
- **Shape:** capture goroutine → bounded `chan []float32` → a single processing goroutine owning AEC/VAD/ASR. Ring buffer between them absorbs jitter.
- **Also fix while you're there:** frame size is unset, so you're on miniaudio's ~1024-frame default (~64 ms), mismatched against the denoiser's ~160-sample hop (`denoise.go:227`). Set an explicit period.
- **Size:** largest single item. Everything duplex-related depends on it.

### 2. A session manager that owns turn identity
- **Missing:** no turn object exists. IDs are scattered across `ttsSeq` (`loop.go:100`), `latestTurn` (`main.go:788`), and bare atomics `isSpeaking`/`ttsPlaying`/`stopPlayback` (`main.go:114-123`).
- **Why it blocks duplex:** when user and assistant overlap, *whose turn is it?* Without one owner there's no place to hang the cancel func, no way to know whether audio belongs to echo, barge-in, or backchannel. Both barge-in and partial transcripts need this.
- **Shape:** a `Turn{ID, Ctx, Cancel, StartedAt, State}` with the input and output workers both subscribing to it. Your doc's `internal/session/` is the right idea — but have it *replace* the atomics, not sit beside them.
- **Note:** don't build your doc's fresh `internal/bus/` + `internal/pipeline/` + `internal/asr/` tree. `internal/events/bus.go` already exists and already carries the voice path.

### 3. Real playback flush
- **Missing:** interruption is a cooperative flag (`stopPlayback`), not a flush. `audio.go:170-173` fills silence and keeps the device running; `buf` is never cleared; no `device.Stop()`.
- **Why it blocks duplex:** anything already queued keeps playing after the user cuts in. The assistant talks over the first ~0.6 s of the user's sentence — which reads as "it didn't hear me," the exact failure barge-in exists to prevent.
- **Shape:** clear the staging buffer + a device-level flush/reopen. Then bound the buffer to ~200 ms and accept a ~1-frame join seam.

---

## 🟡 Tier 1 — The duplex features themselves

### 4. Partial transcripts with a real transport
- **Missing:** live ASR emits only to `log.Printf` (`main.go:1579`). No partial-transcript message type exists — `internal/server/protocol.go:22-40` has none.
- **Why it blocks duplex:** without partials the assistant can't respond to what you're *currently saying*, only to what you finished saying. That's half-duplex with a fast turn.
- **Prerequisite:** flip `config.yaml:67` `active_model: "qwen3"` → `"nemotron"`. Streaming code (`updateLiveASR`, `main.go:1569-1582`) and the model are already on disk — just dead config.
- **Then:** publish partials as bus events, and add semantic turn gating so a partial doesn't trigger a reply on every "um".
- **Watch:** the qwen3 branch and the streaming branch have different turn-finalization semantics; don't assume `finalizeTurn` works unchanged.

### 5. In-flight TTS cancellation
- **Missing:** with `streaming: true`, `streamAudio` is true (`main.go:705`), so the `stopPlayback` check at `main.go:710` sits in a dead branch, and `renderSentence` deliberately ignores `stopPlayback` (`main.go:858-863`). In-flight synthesis is **not** cancelled — the CPU is paid and the output discarded later (`main.go:1152`).
- **Why it matters:** barge-in during a long response keeps burning TTS and can bleed into the next turn.
- **Shape:** thread a `context.Context` through `synthesize` (it currently takes none, `main.go:700`) and have the callback return false on cancel. Granularity stays ~0.6 s per sentence — that ceiling is the model, not your code.
- **Cheap win available:** also return false on `stopPlayback`, not just on turn-ID supersession.

### 6. Backchannel vs. barge-in
- **Missing:** every burst that clears the gate is a full barge-in (`main.go:1812-1822`). "Mm-hmm" and "wait, no" are treated identically.
- **Why it blocks duplex:** true duplex means overlapping without the assistant abandoning everything. You need a short-interjection path that doesn't cancel the turn.

---

## 🟢 Tier 2 — Quality (not blocking, but you'll feel them)

- **Put the denoiser on the critical path.** Right now `g.run()` (`denoise.go:397`) keeps only the speech verdict and **throws the denoised samples away** — ASR still receives raw mic. It's a gate, not a suppressor. It's also switched off entirely during playback (`main.go:1751`), i.e. off exactly when you need it.
- **Don't `Reset()` the AEC on barge-in** (`main.go:1823`). That discards convergence at the precise moment double-talk is hardest to separate.
- **Publish from a goroutine.** The bus dispatches synchronously on the caller's goroutine (`bus.go:75-77`); `SubscribeAsync` exists but has **zero call sites**. Anything you publish from the audio callback inherits inline execution.
- **Fix `HybridProvider.StreamChat`** — it's missing (`hybrid.go:27-61`) so the `ChatStreamer` assertion at `loop.go:853` always fails and the KV-prefix-cache chat path is dead in production.
- **Fix the `o.status` data race** (`loop.go:1649` read from `bridge.go:150` without synchronization) before you add more states on top of it.
- **Instrument before optimizing.** `RecordLatency` is called exactly once, for the LLM (`loop.go:464`). No ASR/VAD/TTS timing exists, so you currently can't tell which stage to fix. This is cheap and should come early — right now you'd be guessing.
- **Add real barge-in coverage.** `bargein_test.go` tests the AEC/coherence heuristic against hand-copied thresholds (`bargein_test.go:30`), never calling `handleAudioFrame`. No test covers warmup, sustain, `stopPlayback`, or LLM cancellation.

---

## ⛔ Do not attempt — blocked or not worth it

- **True token-in / frame-out streaming TTS.** Sherpa's Pocket TTS needs the complete text; `offline-tts-pocket-impl.h:308` encodes whole sentences up front and re-initializes LM state per sentence. Requires a different engine, not different code. Your sentence-granular chunking is the ceiling.
- **Sub-300 ms end-to-end.** Bounded by the ASR model, not your architecture. Offline Qwen3 alone is 50-500 ms on a completed utterance.

---

## Suggested order

**#1 → #2 → #3 → (#4 in parallel, it only needs a config flip) → #5 → #6.**

The reasoning: #1 is the keystone — every other duplex behavior depends on the mic not dropping frames. #2 gives you somewhere to hang cancellation, which #3 and #5 then use. #4 is nearly free and can start immediately in parallel since it doesn't touch the pipeline threading.

One correction to your doc worth flagging: it presents the AEC/session layer as net-new work. Your NLMS AEC with coherence-based double-talk detection (`aec.go:136`, `aec.go:247`) is the hardest thing in this whole list, and you already have it. Don't rebuild it — protect it.