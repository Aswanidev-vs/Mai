# STT/TTS Fault Report — Mai AI Assistant

## Scope
Reviewed the current runtime audio pipeline and TTS delivery for Mai:
- `cmd/mai/main.go` (wake word → VAD → streaming/offline ASR; TTS playback; ASR mute/feedback prevention)
- `cmd/mai/audio.go` (microphone capture; PCM16 → float32 conversion)
- `internal/tts/engine.go` (agentic-mode TTS event handler)
- `internal/personality/tts_adapter.go` (emotion → TTS parameter scaling)

This report focuses on likely causes of STT failures, repeated triggers, transcription truncation, and TTS/ASR feedback issues.

---

## 1) Highest-Risk Faults (likely to break STT/TTS reliability)

### 1.1 Data race / inconsistent gating around `ttsPlaying` (feedback loop risk) — **FIXED**
**Where:** `cmd/mai/main.go`  
- `ttsPlaying` is now treated as an `int32` and read/written via `atomic` operations in the gating paths used by the audio callback and TTS/playback handlers.

**What was addressed:**
- Removed the previously reported unsafe non-synchronized access pattern that could allow assistant audio to leak into ASR (or suppress user speech).

**What remains to verify at runtime:**
- Whether all relevant gating sites (wake-word follow-up, StateListening feeding, and any other audio capture callbacks) consistently use the atomic gate (should be re-validated under load).

---

### 1.2 ASR finalization timing vs VAD end detection (missing last words)
**Where:** `cmd/mai/main.go` (StateListening, end-of-segment block)

**Current behavior:**
- During speech, it loops:
  - `recognizer.Decode(asrStream)` while `recognizer.IsReady(asrStream)`
- When VAD indicates the segment ended:
  - it pops VAD frames
  - then calls `recognizer.GetResult(asrStream).Text` *without an explicit drain/ready loop right at segment end*.

**Why it’s a fault:**
- VAD segment end can occur slightly earlier than ASR finalization.
- The `GetResult()` call can return **partial** transcription.

**Symptoms:**
- users say “stop” / last phrase, but assistant responds as if it was never said
- occasional missing trailing words consistently at end-of-utterance

**Fix direction:**
- At segment end, do a “drain decode”:
  - push any necessary trailing frames (if available)
  - or run `for recognizer.IsReady(asrStream) { recognizer.Decode(asrStream) }` before `GetResult()`

---

### 1.3 Fixed echo tail sleep is fragile (STT feedback or latency)
**Where:** `cmd/mai/main.go`  
- After any TTS playback (both agentic and legacy paths) it uses:
  - `time.Sleep(1500 * time.Millisecond)`

**Why it’s a fault:**
- Room echo time varies widely.
- Too short:
  - mic hears assistant again → wake word re-triggers
- Too long:
  - assistant feels unresponsive (user speech ignored after TTS)

**Fix direction:**
- Replace fixed delay with dynamic detection:
  - monitor mic RMS after playback until “silence” threshold is reached
  - then resume ASR
- Or use a playback completion callback rather than a timer.

---

## 2) Medium-Risk Faults

### 2.1 Offline ASR buffering can grow if VAD never finalizes
**Where:** `cmd/mai/main.go`  
In the offline branch (`asrStream == nil`):
- `sessionSamples = append(sessionSamples, samples...)` until VAD ends the segment.
- If VAD thresholds are too strict or audio quality is noisy, segment end may not happen quickly.

**Symptoms:**
- memory growth over time
- delayed/very late responses

**Fix direction:**
- Add safety caps:
  - max buffered samples
  - max segment duration (force decode & clear)
- Add timeout-based finalization.

---

### 2.2 TTS parameter propagation is inconsistent / possibly unused
**Where:** `cmd/mai/main.go` vs `internal/tts/engine.go`

- `cmd/mai/main.go` directly calls `tts.Generate(text, sid, speed)` and passes only `speed`.
- `internal/tts/engine.go` ignores `speed`/`pitch` and calls `e.generate(text)` without applying pitch.

**Why it matters:**
- If `internal/tts/engine.go` is used in any configuration, emotion scaling for pitch/speed might be partially ignored.
- Current architecture may already bypass it in agentic mode, but it’s still a correctness risk.

**Fix direction:**
- Ensure only one TTS path is active.
- If keeping the event-driven TTS engine, update it to accept and forward speed/pitch (if supported by sherpa-onnx).

---

## 3) Low-Risk / Verified Areas

### 3.1 Audio PCM conversion appears consistent (S16 little-endian assumption)
**Where:** `cmd/mai/audio.go`
- Capture config uses `malgo.FormatS16`
- Conversion reads:
  - `int16(pSample[2*i]) | int16(pSample[2*i+1])<<8`

This is consistent with typical little-endian S16 stream expectations.

---

## 4) Recommended Verification Checklist (what to test next)

### Must-do
1. Run race detector:
   - `go test -race ./...`
   - specifically validate no race on `ttsPlaying`, `isSpeaking`, and any shared buffers.
2. Manual STT/TTS loop test:
   - Speak short commands while assistant speaks.
   - Confirm no wake-word repeats.
3. End-of-sentence correctness:
   - say a sentence and ensure the last word is always captured.

### Useful
4. Test multiple room conditions:
   - small room vs large room; microphone distance.
5. Test both:
   - Agentic mode ON
   - Legacy mode ON
6. Test offline ASR conditions:
   - VAD threshold extremes; long utterances.

---

## 5) Summary of Top Fix Priorities
1. **Fix `ttsPlaying` synchronization** (data race + incorrect gating)
2. **Drain ASR on VAD segment end** to avoid truncated last words
3. **Replace fixed 1500ms echo sleep** with mic-based silence detection
4. Add **buffer caps** for offline ASR to prevent growth
