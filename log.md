Everything after `abc33ca` is gone (the untracked test files were cleaned too). Here's the complete list of what was lost.

## 1. Wake-word reliability — `config.yaml`

| Change | State |
|---|---|
| `kws.confidence_threshold: 0.55 → 0.25` | **SURVIVED** in your working tree |
| The comment block explaining it (measured: a clean human recording of this keyword peaks at probability 0.70, so 0.55 left only a 0.15 margin) | lost |
| `kws.keywords` → per-keyword thresholds + distinct display names: `"▁MA I @mai #0.25, ▁MY @my #0.45, ▁HE Y ▁MA I @heymai #0.20"` — "MY" needs a higher bar (common English word), "HEY MAI" a lower one, and distinct `@` names let the log say which fired | lost |

## 2. The follow-up fix (B) — `cmd/mai/main.go`, 4 hunks

- Measure the **VAD's segment** RMS, not the current 25 ms frame (the VAD only reports a segment *after* the speech ends, so the old measure read silence — measured 0.000000 vs 0.0495)
- Removed the duplicate `asrStream.AcceptWaveform` (the ASR was fed **twice per frame** while the window was open → the recogniser ran 2× ahead and the endpoint rules fired at half speed)
- `vadDetector.Pop()` on a rejected segment (an unpopped one blocked every newer segment)
- Removed `recognizer.Reset(asrStream)` at the transition (it threw away the speech the recogniser had already decoded → the turn would open on silence)
- Plus: `lastResponseTime` was written under `lastResponseMu` but **read without it** on another goroutine (a torn read of a 24-byte `time.Time`) — now read under the mutex
- Plus: `lastResponseTime` now starts at the zero value, so the follow-up window stays closed until she has actually spoken (no wake-word-free turn in the first 15 s after launch)
- Plus: the wake log now says which variant fired — `[WAKE] Detected "mai"!`

## 3. Always-on mode (wake word optional)

- `pkg/models/config.go` — 3 new KWS fields: `Mode`, `FollowUpSeconds`, `AlwaysMinGapMs`
- `config.yaml` — `mode: "always"`, `follow_up_seconds: 15`, `always_min_gap_ms: 5000`
- `cmd/mai/main.go` — `alwaysOn` plumbing; the VAD runs continuously; `turnIsUserInitiated` set at the 4 transition sites (wake word / barge-in / browser = never gated, bare speech = subject to the gap); `ambientTurnAnswerable` as a package-level function; the ambient-speech gap guard in `finalizeTurn`

## 4. The mic-button fix — `internal/server/static/js/app.js`

- `pending` + `gen` fields; a click while the device is opening now **cancels** instead of opening a second mic
- superseded async starts clean up after themselves instead of orphaning a microphone (measured: 2 microphones left running before, 0 after)
- `start()` is failure-safe — a failure half-way used to leave the button stuck on "listening" with no mic behind it
- `stop()` clears the stale "Listening…" pill
- `stop()` only tells the backend to stop if it was ever told to start

## 5. Seven new test/probe files (all gone)

`cmd/mai/followup_gate_test.go` (VAD regression), `cmd/mai/turn_policy_test.go` (always-on policy), `internal/server/static/js/mic.test.js` (mic regression), `cmd/mai/zz_vad_probe_test.go`, `cmd/mai/zz_kws_probe_test.go`, `cmd/mai/zz_kws_recall_test.go`, `cmd/mai/zz_kws_matrix_test.go`

## What is safely committed in `abc33ca` (untouched)

- ✅ The **audio-pipeline fix** — adaptive capture-queue lag budget (3 frames while playing / 16 while idle), 1.6 dropped frames/sec → 0
- ✅ The **VRM rebalance** — the corrected rendering (MToon `toony`/`matcap`/`outline` restored to VRoid's values, exposure 0.88, warm three-point rig, antialias on, the material-array fix)
- ✅ The original `kws.confidence_threshold: 0.55` in the committed config (your working tree has 0.25)

## Recovery

**Everything is restorable.** `app.js` is byte-for-byte saved at `/tmp/app.js.fixed` (verified: 69 lines differ from HEAD, containing `pending: false` and `gen: 0`). Everything else I can rewrite exactly — I have the full content of every edit and every new file in this session, so it won't be a re-derivation, it'll be the same code.

Say the word and I'll restore all of it, then re-run `npm test` and the Go suite to confirm. If you'd rather keep the reset and only take one piece, the mic fix is the one with a user-visible bug behind it, and it's the cheapest to put back.

One suggestion: if the reset was accidental, `git stash` is a safer undo than `git reset --hard` — it keeps the work in the stash while letting you inspect it.