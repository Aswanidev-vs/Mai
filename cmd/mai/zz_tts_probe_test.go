package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/user/mai/pkg/models"
	"gopkg.in/yaml.v3"
)

// This file is a diagnostic harness (not a unit test suite) used to measure
// Pocket TTS synthesis speed and output level against Supertonic. It writes
// WAV files next to the repo root so the results can be listened to.
//
// Run with:
//   cd cmd/mai
//   go test -run TestProbeTTSLoudnessAndSpeed -v -timeout 30m .

func probeRepoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return filepath.Dir(filepath.Dir(wd))
}

// probeGate skips the engine-loading probes unless they were asked for
// explicitly. Each one loads a multi-hundred-megabyte model and takes tens of
// seconds, so they must not run as part of an ordinary `go test ./cmd/mai`.
//
// Run them with:
//
//	MAI_TTS_PROBE=1 go test -c -o zzprobe.exe ./cmd/mai && ./zzprobe.exe -test.run TestProbePocketFp32 -test.v
func probeGate(t *testing.T) {
	t.Helper()
	if os.Getenv("MAI_TTS_PROBE") == "" {
		t.Skip("model-loading probe; set MAI_TTS_PROBE=1 to run")
	}
}

func probeWriteWAV(t *testing.T, path string, samples []float32, sampleRate int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()

	dataLen := len(samples) * 2
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(36+dataLen))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:20], 16)
	binary.LittleEndian.PutUint16(hdr[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:24], 1) // mono
	binary.LittleEndian.PutUint32(hdr[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(hdr[28:32], uint32(sampleRate*2))
	binary.LittleEndian.PutUint16(hdr[32:34], 2)
	binary.LittleEndian.PutUint16(hdr[34:36], 16)
	copy(hdr[36:40], "data")
	binary.LittleEndian.PutUint32(hdr[40:44], uint32(dataLen))
	if _, err := f.Write(hdr); err != nil {
		t.Fatalf("write header: %v", err)
	}
	buf := make([]byte, dataLen)
	for i, s := range samples {
		switch {
		case s > 1:
			s = 1
		case s < -1:
			s = -1
		}
		v := int16(s * 32767)
		buf[i*2] = byte(v & 0xFF)
		buf[i*2+1] = byte(v >> 8)
	}
	if _, err := f.Write(buf); err != nil {
		t.Fatalf("write data: %v", err)
	}
}

// probeRefMono mirrors exactly what cmd/mai/main.go does for the voice-cloning
// reference: read the WAV, mix down to mono and honour max_reference_audio_len.
func probeRefMono(t *testing.T, path string, maxSeconds float32) ([]float32, int) {
	t.Helper()
	wav := sherpa.ReadWaveMultiChannel(path)
	if wav == nil {
		t.Fatalf("ReadWaveMultiChannel(%s) returned nil (file missing or unsupported)", path)
	}
	defer wav.Release()

	n := wav.SamplesPerChannel
	out := make([]float32, n)
	if wav.ChannelCount > 1 {
		for i := 0; i < n; i++ {
			var sum float32
			for ch := 0; ch < wav.ChannelCount; ch++ {
				sum += wav.Samples[ch*n+i]
			}
			out[i] = sum / float32(wav.ChannelCount)
		}
	} else {
		copy(out, wav.Samples[:n])
	}
	rate := wav.SampleRate
	if maxSeconds > 0 {
		if limit := int(float64(rate) * float64(maxSeconds)); limit > 0 && len(out) > limit {
			out = out[:limit]
		}
	}
	return out, rate
}

func probeStats(t *testing.T, label string, samples []float32, sampleRate int, wall time.Duration) {
	t.Helper()
	if len(samples) == 0 {
		t.Logf("[PROBE] %-22s -> EMPTY OUTPUT", label)
		return
	}
	var sumSq float64
	peak := 0.0
	for _, s := range samples {
		sumSq += float64(s) * float64(s)
		if a := math.Abs(float64(s)); a > peak {
			peak = a
		}
	}
	rms := math.Sqrt(sumSq / float64(len(samples)))
	dur := float64(len(samples)) / float64(sampleRate)
	t.Logf("[PROBE] %-22s sr=%5d out=%5.2fs rms=%.4f (%6.1f dBFS) peak=%.3f wall=%5.2fs rtf=%4.2fx",
		label, sampleRate, dur, rms, 20*math.Log10(rms), peak, wall.Seconds(), dur/wall.Seconds())
}

func probePocket(t *testing.T, label, dir string, int8 bool, ref []float32, refRate int, extra string) {
	t.Helper()
	sfx := ""
	if int8 {
		sfx = ".int8"
	}
	cfg := sherpa.OfflineTtsConfig{}
	cfg.Model.NumThreads = 6
	cfg.Model.Provider = "cpu"
	cfg.Model.Pocket.LmFlow = filepath.Join(dir, "lm_flow"+sfx+".onnx")
	cfg.Model.Pocket.LmMain = filepath.Join(dir, "lm_main"+sfx+".onnx")
	cfg.Model.Pocket.Encoder = filepath.Join(dir, "encoder"+sfx+".onnx")
	cfg.Model.Pocket.Decoder = filepath.Join(dir, "decoder"+sfx+".onnx")
	cfg.Model.Pocket.TextConditioner = filepath.Join(dir, "text_conditioner"+sfx+".onnx")
	cfg.Model.Pocket.VocabJson = filepath.Join(dir, "vocab.json")
	cfg.Model.Pocket.TokenScoresJson = filepath.Join(dir, "token_scores.json")

	tts := sherpa.NewOfflineTts(&cfg)
	if tts == nil {
		t.Fatalf("%s: NewOfflineTts returned nil", label)
	}
	defer sherpa.DeleteOfflineTts(tts)

	gen := &sherpa.GenerationConfig{
		Speed:               1.0,
		ReferenceAudio:      ref,
		ReferenceSampleRate: refRate,
	}
	if extra != "" {
		gen.Extra = json.RawMessage(extra)
	}

	const text = "I thought about it, and the answer is simpler than you are making it."
	var got []float32
	start := time.Now()
	tts.GenerateWithConfig(text, gen, func(samples []float32, _ float32) bool {
		got = append(got, samples...)
		return true
	})
	wall := time.Since(start)

	probeStats(t, label, got, tts.SampleRate(), wall)
	if len(got) > 0 {
		probeWriteWAV(t, filepath.Join(probeRepoRoot(), "probe_"+label+".wav"), got, tts.SampleRate())
	}
}

func probeSupertonic(t *testing.T, dir string) {
	t.Helper()
	cfg := sherpa.OfflineTtsConfig{}
	cfg.Model.NumThreads = 6
	cfg.Model.Provider = "cpu"
	cfg.Model.Supertonic.DurationPredictor = filepath.Join(dir, "duration_predictor.int8.onnx")
	cfg.Model.Supertonic.TextEncoder = filepath.Join(dir, "text_encoder.int8.onnx")
	cfg.Model.Supertonic.VectorEstimator = filepath.Join(dir, "vector_estimator.int8.onnx")
	cfg.Model.Supertonic.Vocoder = filepath.Join(dir, "vocoder.int8.onnx")
	cfg.Model.Supertonic.TtsJson = filepath.Join(dir, "tts.json")
	cfg.Model.Supertonic.UnicodeIndexer = filepath.Join(dir, "unicode_indexer.bin")
	cfg.Model.Supertonic.VoiceStyle = filepath.Join(dir, "voice.bin")

	tts := sherpa.NewOfflineTts(&cfg)
	if tts == nil {
		t.Fatalf("supertonic: NewOfflineTts returned nil")
	}
	defer sherpa.DeleteOfflineTts(tts)

	gen := &sherpa.GenerationConfig{Speed: 1.0, NumSteps: 5}
	gen.Extra = json.RawMessage(`{"lang": "en"}`)

	const text = "I thought about it, and the answer is simpler than you are making it."
	var got []float32
	start := time.Now()
	tts.GenerateWithConfig(text, gen, func(samples []float32, _ float32) bool {
		got = append(got, samples...)
		return true
	})
	wall := time.Since(start)

	probeStats(t, "supertonic-int8", got, tts.SampleRate(), wall)
	if len(got) > 0 {
		probeWriteWAV(t, filepath.Join(probeRepoRoot(), "probe_supertonic.wav"), got, tts.SampleRate())
	}
}

func probePocketTest(t *testing.T, label, dir string, int8 bool, extra string) {
	root := probeRepoRoot()
	refPath := filepath.Join(root, "mai-san-pcm.wav")
	// Reproduce the shipped configuration: a 10 s cap on a ~34.8 s stereo WAV.
	ref10, rate := probeRefMono(t, refPath, 10.0)
	probePocket(t, label, dir, int8, ref10, rate, extra)
}

func TestProbePocketFp32(t *testing.T) {
	probeGate(t)
	probePocketTest(t, "pocket-fp32-ref10", filepath.Join(probeRepoRoot(), "sherpa-onnx-pocket-tts-2026-01-26"), false, "")
}

// TestProbePocketFp32FullReference uses the whole ~34.8 s reference instead of
// the 10 s cap the shipped configuration applies.
func TestProbePocketFp32FullReference(t *testing.T) {
	probeGate(t)
	root := probeRepoRoot()
	ref, rate := probeRefMono(t, filepath.Join(root, "mai-san-pcm.wav"), 0)
	probePocket(t, "pocket-fp32-refall",
		filepath.Join(root, "sherpa-onnx-pocket-tts-2026-01-26"), false, ref, rate,
		`{"max_reference_audio_len": 60}`)
}

// TestProbeReferenceMixdown checks the stereo -> mono downmix main.go performs
// on the voice-cloning reference. Two anti-phase channels would cancel and
// destroy the timbre the voice encoder sees, which would explain a clone that
// does not sound like the reference.
func TestProbeReferenceMixdown(t *testing.T) {
	probeGate(t)
	root := probeRepoRoot()
	path := filepath.Join(root, "mai-san-pcm.wav")
	wav := sherpa.ReadWaveMultiChannel(path)
	if wav == nil {
		t.Skipf("cannot read %s", path)
	}
	defer wav.Release()

	n := wav.SamplesPerChannel
	t.Logf("[REF] %s: %d Hz, %d ch, %d frames (%.2fs)",
		filepath.Base(path), wav.SampleRate, wav.ChannelCount, n, float64(n)/float64(wav.SampleRate))

	channels := make([][]float32, wav.ChannelCount)
	for ch := 0; ch < wav.ChannelCount; ch++ {
		channels[ch] = make([]float32, n)
		copy(channels[ch], wav.Samples[ch*n:(ch+1)*n])
	}

	mono := make([]float32, n)
	for i := 0; i < n; i++ {
		var sum float32
		for ch := 0; ch < wav.ChannelCount; ch++ {
			sum += channels[ch][i]
		}
		mono[i] = sum / float32(wav.ChannelCount)
	}

	rmsOf := func(s []float32) float64 {
		var acc float64
		for _, v := range s {
			acc += float64(v) * float64(v)
		}
		return math.Sqrt(acc / float64(len(s)))
	}

	for ch := range channels {
		t.Logf("[REF] channel %d rms=%.4f (%.1f dBFS)", ch, rmsOf(channels[ch]), 20*math.Log10(rmsOf(channels[ch])))
	}
	monoRMS := rmsOf(mono)
	t.Logf("[REF] mono mix  rms=%.4f (%.1f dBFS)", monoRMS, 20*math.Log10(monoRMS))

	if len(channels) > 1 {
		var num, d0, d1 float64
		for i := 0; i < n; i++ {
			a, b := float64(channels[0][i]), float64(channels[1][i])
			num += a * b
			d0 += a * a
			d1 += b * b
		}
		corr := num / math.Sqrt(d0*d1)
		t.Logf("[REF] L/R correlation = %.4f (negative means the downmix cancels)", corr)
	}

	probeWriteWAV(t, filepath.Join(root, "probe_reference_mono.wav"), mono, wav.SampleRate)

	// Per-second energy profile: shows whether a max_reference_audio_len cap
	// would spend its budget on speech or on leading/trailing silence.
	sec := wav.SampleRate
	for s := 0; s+sec <= len(mono); s += sec {
		chunk := mono[s : s+sec]
		var acc float64
		for _, v := range chunk {
			acc += float64(v) * float64(v)
		}
		rms := math.Sqrt(acc / float64(len(chunk)))
		t.Logf("[REF] t=%2ds..%2ds rms=%.4f (%6.1f dBFS)", s/sec, s/sec+1, rms, 20*math.Log10(rms+1e-9))
	}
}

func TestProbePocketInt8Bundle(t *testing.T) {
	probeGate(t)
	probePocketTest(t, "pocket-int8-ref10", filepath.Join(probeRepoRoot(), "sherpa-onnx-pocket-tts-english_2026-04-2026-09-04"), true, "")
}

func TestProbeSupertonic(t *testing.T) {
	probeGate(t)
	probeSupertonic(t, filepath.Join(probeRepoRoot(), "sherpa-onnx-supertonic-3-tts-int8-2026-05-11"))
}

// TestProbePocketSentenceOverhead measures how Pocket's realtime factor varies
// with sentence length. A large fixed per-call cost means the short sentences
// the LLM streaming handoff produces are synthesized far slower than realtime,
// so the renderer cannot stay ahead of playback.
func TestProbePocketSentenceOverhead(t *testing.T) {
	probeGate(t)
	root := probeRepoRoot()
	ref, rate := probeRefMono(t, filepath.Join(root, "mai-san-pcm.wav"), 10.0)
	dir := filepath.Join(root, "sherpa-onnx-pocket-tts-2026-01-26")

	cfg := sherpa.OfflineTtsConfig{}
	cfg.Model.NumThreads = 6
	cfg.Model.Provider = "cpu"
	cfg.Model.Pocket.LmFlow = filepath.Join(dir, "lm_flow.onnx")
	cfg.Model.Pocket.LmMain = filepath.Join(dir, "lm_main.onnx")
	cfg.Model.Pocket.Encoder = filepath.Join(dir, "encoder.onnx")
	cfg.Model.Pocket.Decoder = filepath.Join(dir, "decoder.onnx")
	cfg.Model.Pocket.TextConditioner = filepath.Join(dir, "text_conditioner.onnx")
	cfg.Model.Pocket.VocabJson = filepath.Join(dir, "vocab.json")
	cfg.Model.Pocket.TokenScoresJson = filepath.Join(dir, "token_scores.json")

	tts := sherpa.NewOfflineTts(&cfg)
	if tts == nil {
		t.Fatal("NewOfflineTts returned nil")
	}
	defer sherpa.DeleteOfflineTts(tts)

	cases := []struct {
		name string
		text string
	}{
		{"6 chars", "Right."},
		{"24 chars", "That is fine, I suppose."},
		{"61 chars", "I looked at it again this morning, and nothing about it surprised me."},
		{"100 chars", "I looked at it again this morning, and honestly nothing about the whole situation surprised me at all, which is saying something."},
	}
	for _, c := range cases {
		gen := &sherpa.GenerationConfig{
			Speed:               1.0,
			ReferenceAudio:      ref,
			ReferenceSampleRate: rate,
		}
		var got []float32
		start := time.Now()
		tts.GenerateWithConfig(c.text, gen, func(samples []float32, _ float32) bool {
			got = append(got, samples...)
			return true
		})
		wall := time.Since(start)
		dur := float64(len(got)) / float64(tts.SampleRate())
		t.Logf("[OVERHEAD] %-10s audio=%5.2fs wall=%5.2fs rtf=%4.2fx", c.name, dur, wall.Seconds(), dur/wall.Seconds())
	}
}

// TestProbeConfigPocketKnobsLoad verifies the new Pocket/cloning knobs actually
// parse out of the real config.yaml â€” a wrong yaml tag would silently leave the
// fix inert, which is exactly how voice_cloning.seed went unused before.
func TestProbeConfigPocketKnobsLoad(t *testing.T) {
	path := filepath.Join(probeRepoRoot(), "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no config.yaml: %v", err)
	}
	var cfg models.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	t.Logf("[CFG] active_model=%q output_gain=%.2f voice_style=%q",
		cfg.TTS.ActiveModel, cfg.TTS.OutputGain, cfg.TTS.TTSVoiceStyle)
	t.Logf("[CFG] pocket.temperature=%.2f seed=%v batch=%v batch_max_chars=%d cache_capacity=%d",
		cfg.TTS.Pocket.Temperature, cfg.TTS.Pocket.Seed, cfg.TTS.Pocket.BatchSentences,
		cfg.TTS.Pocket.BatchMaxChars, cfg.TTS.Pocket.VoiceEmbeddingCacheCapacity)
	t.Logf("[CFG] voice_cloning.reference=%q max_len=%.1f",
		cfg.TTS.VoiceCloning.ReferenceAudio, cfg.TTS.VoiceCloning.MaxReferenceAudioLen)

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"tts.output_gain", cfg.TTS.OutputGain, float32(1.11)},
		{"tts.pocket.temperature", cfg.TTS.Pocket.Temperature, float32(0.7)},
		{"tts.pocket.batch_max_chars", cfg.TTS.Pocket.BatchMaxChars, 160},
		{"tts.pocket.voice_embedding_cache_capacity", cfg.TTS.Pocket.VoiceEmbeddingCacheCapacity, 50},
		{"tts.voice_cloning.max_reference_audio_len", cfg.TTS.VoiceCloning.MaxReferenceAudioLen, float32(30)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v (check the yaml tag)", c.name, c.got, c.want)
		}
	}
	if cfg.TTS.Pocket.Seed == nil {
		t.Error("tts.pocket.seed did not parse into a non-nil *int")
	} else if *cfg.TTS.Pocket.Seed != 42 {
		t.Errorf("tts.pocket.seed = %d, want 42", *cfg.TTS.Pocket.Seed)
	}
	if cfg.TTS.Pocket.BatchSentences == nil || !*cfg.TTS.Pocket.BatchSentences {
		t.Error("tts.pocket.batch_sentences should parse as true")
	}
	if cfg.TTS.Pocket.Seed == nil || cfg.TTS.Pocket.Temperature == 0 {
		t.Log("[CFG] NOTE: without seed+temperature Pocket samples a new noise draw per sentence")
	}
}

// TestProbeMainlineConfigPocketNeedsNothing draws attention to the one config
// combination that used to fail silently: Pocket with cloning disabled.
func TestProbeMainlineConfigPocketNeedsNothing(t *testing.T) {
	path := filepath.Join(probeRepoRoot(), "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no config.yaml: %v", err)
	}
	var cfg models.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if cfg.TTS.ActiveModel != "pocket" {
		t.Skip("active model is not pocket")
	}
	if !cfg.TTS.VoiceCloning.Enabled {
		t.Fatal("active model is pocket but voice_cloning.enabled is false: the official " +
			"Pocket export has no built-in voice, so main.go now fatals at startup instead of " +
			"going mute. Set voice_cloning.enabled: true with a PCM16 reference WAV.")
	}
	ref := filepath.Join(probeRepoRoot(), cfg.TTS.VoiceCloning.ReferenceAudio)
	if _, err := os.Stat(ref); err != nil {
		t.Fatalf("voice_cloning.reference_audio %q is not readable: %v. Pocket cannot "+
			"synthesize without it and main.go now refuses to start rather than going mute.", ref, err)
	}
	t.Logf("[CFG] pocket reference OK: %s", ref)
}

// TestProbePocketSeedDeterminism proves the seed plumbing works end to end:
// two requests with the same seed must produce identical audio, and two
// requests with different seeds must not. This is what stops the cloned voice
// drifting timbre and level sentence to sentence.
func TestProbePocketSeedDeterminism(t *testing.T) {
	probeGate(t)
	root := probeRepoRoot()
	dir := filepath.Join(root, "sherpa-onnx-pocket-tts-2026-01-26")
	ref, rate := probeRefMono(t, filepath.Join(root, "mai-san-pcm.wav"), 30)

	cfg := sherpa.OfflineTtsConfig{}
	cfg.Model.NumThreads = 6
	cfg.Model.Provider = "cpu"
	cfg.Model.Pocket.LmFlow = filepath.Join(dir, "lm_flow.onnx")
	cfg.Model.Pocket.LmMain = filepath.Join(dir, "lm_main.onnx")
	cfg.Model.Pocket.Encoder = filepath.Join(dir, "encoder.onnx")
	cfg.Model.Pocket.Decoder = filepath.Join(dir, "decoder.onnx")
	cfg.Model.Pocket.TextConditioner = filepath.Join(dir, "text_conditioner.onnx")
	cfg.Model.Pocket.VocabJson = filepath.Join(dir, "vocab.json")
	cfg.Model.Pocket.TokenScoresJson = filepath.Join(dir, "token_scores.json")

	tts := sherpa.NewOfflineTts(&cfg)
	if tts == nil {
		t.Fatal("NewOfflineTts returned nil")
	}
	defer sherpa.DeleteOfflineTts(tts)

	const text = "I thought about it, and the answer is simpler than you are making it."
	gen := func(seed int) []float32 {
		var got []float32
		tts.GenerateWithConfig(text, &sherpa.GenerationConfig{
			Speed:               1.0,
			ReferenceAudio:      ref,
			ReferenceSampleRate: rate,
			Extra:               json.RawMessage(fmt.Sprintf(`{"seed": %d}`, seed)),
		}, func(samples []float32, _ float32) bool {
			got = append(got, samples...)
			return true
		})
		return got
	}

	equal := func(a, b []float32) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	rms := func(s []float32) float64 {
		var acc float64
		for _, v := range s {
			acc += float64(v) * float64(v)
		}
		return math.Sqrt(acc / float64(len(s)))
	}

	a := gen(42)
	b := gen(42)
	c := gen(43)
	t.Logf("[SEED] seed=42 -> %d samples, rms=%.4f", len(a), rms(a))
	t.Logf("[SEED] seed=42 -> %d samples, rms=%.4f", len(b), rms(b))
	t.Logf("[SEED] seed=43 -> %d samples, rms=%.4f", len(c), rms(c))

	if len(a) == 0 {
		t.Fatal("seed=42 produced no audio")
	}
	if !equal(a, b) {
		t.Errorf("two runs with seed=42 differ (%d vs %d samples): Pocket is not deterministic, so the cloned voice will still drift per sentence",
			len(a), len(b))
	}
	if equal(a, c) {
		t.Log("[SEED] seed=42 and seed=43 gave identical audio â€” the seed may not be reaching the sampler")
	} else {
		t.Log("[SEED] seed=42 and seed=43 differ as expected, so the seed is reaching the sampler")
	}
}

// TestProbePocketBatchDrain exercises the real drainTTSBatch (tts_batch.go)
// against the same queue shape the orchestrator's streaming handoff produces:
// short same-turn sentences plus the end-of-turn marker. Pure Go, race-tested.
func TestProbePocketBatchDrain(t *testing.T) {
	var latestTurn atomic.Int64
	latestTurn.Store(7)

	// Feed the queue the way the orchestrator does: N same-turn sentences, then
	// the end-of-turn marker. The renderer consumes the first sentence before
	// draining, so do the same here.
	feed := func(texts ...string) chan ttsItem {
		ch := make(chan ttsItem, 64)
		for _, s := range texts {
			ch <- ttsItem{text: s, seq: 7}
		}
		ch <- ttsItem{seq: 7, final: true}
		return ch
	}

	cases := [][]string{
		{"Right.", "I already looked at it.", "Nothing about it surprises me."},
		{"Yeah.", "Sure.", "Let me know."},
		{"I thought about it, and the answer is simpler than you are making it."},
	}
	want := []string{
		"Right. I already looked at it. Nothing about it surprises me.",
		"Yeah. Sure. Let me know.",
		"I thought about it, and the answer is simpler than you are making it.",
	}

	for i, texts := range cases {
		ch := feed(texts...)
		first := <-ch
		merged, next, open := drainTTSBatch(ch, first, 160, &latestTurn)

		if merged != want[i] {
			t.Errorf("case %d: merged %q, want %q", i, merged, want[i])
		}
		if !open {
			t.Errorf("case %d: drain reported the queue closed", i)
		}
		if next == nil || !next.final {
			t.Errorf("case %d: end-of-turn marker not handed back (next=%v); the player would never close the turn", i, next)
			continue
		}
		// The marker is handed back via `deferred`, not re-queued (re-queuing
		// would reorder the queue), so the channel itself must now be empty and
		// the caller's next action is to process `next`.
		if n := len(ch); n != 0 {
			t.Errorf("case %d: %d leftover item(s) in the queue; the drain must consume everything up to the marker", i, n)
		}
		t.Logf("[BATCH] case %d: %d sentence(s) -> %d chars: %.60s", i, len(texts), len(merged), merged)
	}

	// A newer turn must never be absorbed into an older turn's batch.
	ch := make(chan ttsItem, 8)
	ch <- ttsItem{text: "Right.", seq: 7}
	ch <- ttsItem{text: "New turn.", seq: 8}
	latestTurn.Store(8)
	first := <-ch
	merged, next, open := drainTTSBatch(ch, first, 160, &latestTurn)
	if merged != "Right." {
		t.Errorf("superseded sentence was absorbed: %q, want %q", merged, "Right.")
	}
	if !open || next == nil || next.text != "New turn." {
		t.Errorf("newer turn not handed back: open=%v next=%v", open, next)
	}
	t.Log("[BATCH] newer turn left intact for the next read")

	// Barge-in mid-turn: the superseded sentences are dropped, the marker survives.
	ch = make(chan ttsItem, 8)
	ch <- ttsItem{text: "Part one.", seq: 7}
	ch <- ttsItem{text: "Part two.", seq: 7}
	ch <- ttsItem{seq: 7, final: true}
	latestTurn.Store(9)
	first = <-ch
	merged, next, open = drainTTSBatch(ch, first, 160, &latestTurn)
	if merged != "Part one." {
		t.Errorf("superseded tail was merged: %q", merged)
	}
	if next == nil || !next.final || !open {
		t.Errorf("marker lost on barge-in: open=%v next=%v", open, next)
	}
	t.Log("[BATCH] barge-in tail dropped, marker preserved")
}

// TTS resampler used to lift Pocket's 24 kHz output to the 44.1 kHz playback
// rate. Pure Go â€” no ONNX runtime involved.
func TestProbeResamplerResponse(t *testing.T) {
	const inRate, outRate = 24000, 44100
	for _, freq := range []float64{500, 1000, 4000, 6000, 8000, 10000, 11000} {
		var in []float32
		const inSeconds = 1.0
		n := int(inRate * inSeconds)
		for i := 0; i < n; i++ {
			in = append(in, float32(math.Sin(2*math.Pi*freq*float64(i)/float64(inRate))))
		}
		r := newResampler(inRate, outRate)
		out := r.resample(in)
		// Measure in steady state (skip the first output sample where the
		// resampler seeds its previous-sample state).
		peak := 0.0
		for _, s := range out[64:] {
			if a := math.Abs(float64(s)); a > peak {
				peak = a
			}
		}
		t.Logf("[RESAMPLE] %5.0f Hz -> %.3f (%.2f dB) @ %d->%d Hz, %d in -> %d out",
			freq, peak, 20*math.Log10(peak), inRate, outRate, len(in), len(out))
	}
}
