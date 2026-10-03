package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
	"github.com/user/mai/pkg/models"
	"gopkg.in/yaml.v3"
)

// Diagnostic harness for the streaming (nemotron) ASR path. Every claim in the
// config comments about language, endpoint rules and partial timing is a number
// this file produced, not an inference from reading the source.
//
// It builds the recognizer through the same code path main.go uses, so what it
// measures is the shipped construction rather than a stand-in.
//
// Run with:
//
//	MAI_ASR_PROBE=1 go test -c -o asrprobe.exe ./cmd/mai && ./asrprobe.exe -test.run TestProbeStreamingASR -test.v -test.timeout 30m
//
// NOTE: the test binary must be built into the repository root. A stale
// onnxruntime.dll in C:\Windows\System32 shadows the 1.27.1 copy the project
// ships and every recognizer then dies inside ORT with an API-version fault that
// looks exactly like a broken model.

func asrProbeGate(t *testing.T) {
	t.Helper()
	if os.Getenv("MAI_ASR_PROBE") == "" {
		t.Skip("model-loading probe; set MAI_ASR_PROBE=1 to run")
	}
}

// asrProbeRepoRoot walks up from the working directory to the directory holding
// go.mod. The shipped binary relies on being launched from the repo root (every
// model_dir in config.yaml is relative to it), so the probe resolves paths the
// same way instead of depending on where the test binary happens to sit.
func asrProbeRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod above %s", dir)
		}
		dir = parent
	}
}

func asrProbeConfig(t *testing.T) models.Config {
	t.Helper()
	path := filepath.Join(asrProbeRepoRoot(t), "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no config.yaml: %v", err)
	}
	var cfg models.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return cfg
}

// newAsrProbeRecognizer mirrors main.go's "nemotron" branch field for field. The
// overrides let one binary measure several tunings against the identical model
// instead of guessing which one to ship.
func newAsrProbeRecognizer(t *testing.T, cfg models.Config, rule2 float32, language string) (*sherpa.OnlineRecognizer, *sherpa.OnlineStream) {
	t.Helper()
	root := asrProbeRepoRoot(t)
	resolve := func(rel string) string {
		if filepath.IsAbs(rel) {
			return rel
		}
		return filepath.Join(root, rel)
	}
	n := cfg.ASR.Nemotron
	c := sherpa.OnlineRecognizerConfig{}
	c.FeatConfig = sherpa.FeatureConfig{SampleRate: 16000, FeatureDim: 80}
	c.ModelConfig.Transducer.Encoder = resolve(join(n.ModelDir, n.Encoder))
	c.ModelConfig.Transducer.Decoder = resolve(join(n.ModelDir, n.Decoder))
	c.ModelConfig.Transducer.Joiner = resolve(join(n.ModelDir, n.Joiner))
	c.ModelConfig.Tokens = resolve(join(n.ModelDir, n.Tokens))
	c.ModelConfig.NumThreads = cfg.ASR.NumThreads
	c.ModelConfig.Provider = cfg.ASR.Provider
	c.DecodingMethod = n.DecodingMethod
	if c.DecodingMethod == "" {
		c.DecodingMethod = "greedy_search"
	}
	c.MaxActivePaths = n.MaxActivePaths
	c.EnableEndpoint = n.EnableEndpoint
	c.Rule1MinTrailingSilence = n.Rule1MinTrailingSilence
	c.Rule2MinTrailingSilence = n.Rule2MinTrailingSilence
	if rule2 > 0 {
		c.Rule2MinTrailingSilence = rule2
	}
	c.Rule3MinUtteranceLength = n.Rule3MinUtteranceLength

	for _, p := range []string{
		c.ModelConfig.Transducer.Encoder,
		c.ModelConfig.Transducer.Decoder,
		c.ModelConfig.Transducer.Joiner,
		c.ModelConfig.Tokens,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Skipf("model file missing (%v): %s", err, p)
		}
	}

	rec := sherpa.NewOnlineRecognizer(&c)
	if rec == nil {
		t.Fatalf("NewOnlineRecognizer returned nil (model_dir=%s)", n.ModelDir)
	}
	stream := sherpa.NewOnlineStream(rec)
	if stream == nil {
		t.Fatal("NewOnlineStream returned nil")
	}
	if language != "" {
		stream.SetOption("language", language)
	}
	return rec, stream
}

// asrProbeReadWAV returns ok=false rather than skipping the test, so one
// off-rate file in test_wavs cannot hide the rest of the measurement.
func asrProbeReadWAV(path string) (audio []float32, sampleRate int, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false
	}
	var bits int
	var pcm []byte
	for pos := 12; pos+8 <= len(data); {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		switch id {
		case "fmt ":
			sampleRate = int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
			bits = int(binary.LittleEndian.Uint16(data[pos+14 : pos+16]))
		case "data":
			pcm = data[pos : pos+size]
		}
		pos += size
		if size%2 != 0 {
			pos++
		}
	}
	if bits != 16 || len(pcm) == 0 {
		return nil, sampleRate, false
	}
	audio = make([]float32, len(pcm)/2)
	for i := range audio {
		audio[i] = float32(int16(binary.LittleEndian.Uint16(pcm[i*2:i*2+2]))) / 32768.0
	}
	return audio, sampleRate, true
}

// asrProbeWav16k returns the first 16 kHz recording in the model's test_wavs.
// The recognizer is 16 kHz, so off-rate files cannot drive it.
func asrProbeWav16k(t *testing.T, cfg models.Config) (name string, audio []float32) {
	t.Helper()
	dir := filepath.Join(asrProbeRepoRoot(t), cfg.ASR.Nemotron.ModelDir, "test_wavs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no test_wavs: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".wav" {
			continue
		}
		a, sr, ok := asrProbeReadWAV(filepath.Join(dir, e.Name()))
		if ok && sr == 16000 {
			return e.Name(), a
		}
	}
	t.Skip("no 16kHz wav in test_wavs")
	return "", nil
}

const asrProbeFrame = 512 // 32 ms at 16 kHz, representative of a capture period

// asrProbeFeed pushes one frame and drains every decode the recognizer is ready
// for — the same IsReady/Decode/GetResult sequence updateLiveASR runs per frame.
func asrProbeFeed(rec *sherpa.OnlineRecognizer, stream *sherpa.OnlineStream, frame []float32) string {
	stream.AcceptWaveform(16000, frame)
	for rec.IsReady(stream) {
		rec.Decode(stream)
	}
	return rec.GetResult(stream).Text
}

// asrProbeSpeechEnd returns the sample index just past the last frame above the
// noise floor, so "trailing silence" is measured from the real end of the
// utterance rather than the end of the file.
func asrProbeSpeechEnd(audio []float32) int {
	last := 0
	var sums []float64
	for i := 0; i+asrProbeFrame <= len(audio); i += asrProbeFrame {
		var sum float64
		for _, s := range audio[i : i+asrProbeFrame] {
			sum += float64(s) * float64(s)
		}
		sums = append(sums, sum/asrProbeFrame)
	}
	for i, v := range sums {
		if v > 0.0004 { // rms > 0.02
			last = (i + 1) * asrProbeFrame
		}
	}
	return last
}

// TestProbeStreamingASR measures partial timing and endpoint latency against
// real audio, and proves a Reset actually re-arms the stream for a second turn.
func TestProbeStreamingASR(t *testing.T) {
	asrProbeGate(t)
	cfg := asrProbeConfig(t)
	name, audio := asrProbeWav16k(t, cfg)
	speechEnd := asrProbeSpeechEnd(audio)
	t.Logf("[WAV] %s: %.2fs total, speech ends at %.2fs", name,
		float64(len(audio))/16000, float64(speechEnd)/16000)

	rec, stream := newAsrProbeRecognizer(t, cfg, 0, cfg.ASR.Nemotron.Language)
	defer sherpa.DeleteOnlineStream(stream)
	defer sherpa.DeleteOnlineRecognizer(rec)

	var (
		firstPartial = -1
		changes      int
		last         string
	)
	for i := 0; i+asrProbeFrame <= speechEnd; i += asrProbeFrame {
		text := asrProbeFeed(rec, stream, audio[i:i+asrProbeFrame])
		if text != last {
			if firstPartial < 0 && text != "" {
				firstPartial = i
				t.Logf("[PARTIAL] first partial at %.2fs of a %.2fs utterance: %q",
					float64(i)/16000, float64(speechEnd)/16000, text)
			}
			if text != "" {
				changes++
			}
			last = text
		}
	}
	t.Logf("[RESULT] %d partial revisions; final: %q", changes, last)

	// (a) partials must exist before the utterance ends — that is the whole
	// point of the streaming path over qwen3.
	if firstPartial < 0 {
		t.Fatalf("no partial before end of speech")
	}
	if firstPartial >= speechEnd {
		t.Errorf("first partial only at/after end of speech")
	}

	// (b) an endpoint must fire, and rule1 guarantees it even when nothing was
	// decoded — so a turn can never hang open.
	silence := make([]float32, asrProbeFrame)
	endpointAfter := -1.0
	for k := 0; k < 5*16000/asrProbeFrame; k++ {
		text := asrProbeFeed(rec, stream, silence)
		if text != "" && rec.IsEndpoint(stream) {
			endpointAfter = float64(k+1) * float64(asrProbeFrame) / 16000
			t.Logf("[ENDPOINT] fired after %.2fs of trailing silence: %q", endpointAfter, text)
			break
		}
	}
	if endpointAfter < 0 {
		t.Errorf("endpoint never fired within 5s of trailing silence")
	}

	// (c) the stream must be reusable: finalizeTurn Resets it every turn, and a
	// stream that cannot re-arm would stop Mai answering the second time.
	rec.Reset(stream)
	if got := rec.GetResult(stream).Text; got != "" {
		t.Errorf("Reset left stale text %q in the stream", got)
	}
	var second string
	for i := 0; i+asrProbeFrame <= speechEnd; i += asrProbeFrame {
		if text := asrProbeFeed(rec, stream, audio[i:i+asrProbeFrame]); text != "" {
			second = text
		}
	}
	t.Logf("[TURN-2] re-armed after Reset: %q", second)
	if second == "" {
		t.Errorf("stream produced no text on the second turn after Reset")
	}
}

// TestProbeStreamingASRRule2 measures how much trailing silence each rule2 value
// needs to end a turn. This is the number the config comment quotes.
func TestProbeStreamingASRRule2(t *testing.T) {
	asrProbeGate(t)
	cfg := asrProbeConfig(t)
	_, audio := asrProbeWav16k(t, cfg)
	speechEnd := asrProbeSpeechEnd(audio)

	for _, rule2 := range []float32{0.8, 1.2, 2.4} {
		rule2 := rule2
		t.Run(fmt.Sprintf("rule2=%.1fs", rule2), func(t *testing.T) {
			rec, stream := newAsrProbeRecognizer(t, cfg, rule2, cfg.ASR.Nemotron.Language)
			defer sherpa.DeleteOnlineStream(stream)
			defer sherpa.DeleteOnlineRecognizer(rec)

			for i := 0; i+asrProbeFrame <= speechEnd; i += asrProbeFrame {
				asrProbeFeed(rec, stream, audio[i:i+asrProbeFrame])
			}
			silence := make([]float32, asrProbeFrame)
			for k := 0; k < 5*16000/asrProbeFrame; k++ {
				text := asrProbeFeed(rec, stream, silence)
				if text != "" && rec.IsEndpoint(stream) {
					t.Logf("[LATENCY] rule2=%.1fs -> endpoint after %.2fs of trailing silence",
						rule2, float64(k+1)*float64(asrProbeFrame)/16000)
					return
				}
			}
			t.Errorf("rule2=%.1fs: endpoint never fired", rule2)
		})
	}
}

// TestProbeStreamingASRMidSentencePause inserts an internal pause into a real
// recording and reports whether the endpoint fires inside the utterance. An
// endpoint here is the user being cut off mid-sentence, which is worse than a
// slower turn.
func TestProbeStreamingASRMidSentencePause(t *testing.T) {
	asrProbeGate(t)
	cfg := asrProbeConfig(t)
	_, audio := asrProbeWav16k(t, cfg)
	speechEnd := asrProbeSpeechEnd(audio)
	if speechEnd < 2*16000 {
		t.Skip("recording too short")
	}
	split := (speechEnd / 2) / asrProbeFrame * asrProbeFrame
	firstHalf := audio[:split]
	t.Logf("[PAUSE] %.2fs of speech split into %.2fs + %.2fs", float64(speechEnd)/16000,
		float64(len(firstHalf))/16000, float64(speechEnd-split)/16000)

	for _, rule2 := range []float32{0.8, 1.2} {
		rule2 := rule2
		t.Run(fmt.Sprintf("rule2=%.1fs", rule2), func(t *testing.T) {
			rec, stream := newAsrProbeRecognizer(t, cfg, rule2, cfg.ASR.Nemotron.Language)
			defer sherpa.DeleteOnlineStream(stream)
			defer sherpa.DeleteOnlineRecognizer(rec)

			fired := -1
			feed := func(buf []float32, offset int) {
				for i := 0; i+asrProbeFrame <= len(buf); i += asrProbeFrame {
					text := asrProbeFeed(rec, stream, buf[i:i+asrProbeFrame])
					if fired < 0 && text != "" && rec.IsEndpoint(stream) {
						fired = offset + i
					}
				}
			}
			pauseSamples := 1.2 * 16000
			feed(firstHalf, 0)
			feed(make([]float32, int(pauseSamples)), len(firstHalf))
			feed(audio[split:speechEnd], len(firstHalf)+int(pauseSamples))

			cut := fired >= 0 && fired < len(firstHalf)
			if cut {
				t.Logf("[CUT] rule2=%.1fs ended the turn at %.2fs, before the 1.2s pause at %.2fs",
					rule2, float64(fired)/16000, float64(len(firstHalf))/16000)
			} else {
				t.Logf("[OK] rule2=%.1fs: no mid-sentence cut (endpoint at %.2fs)",
					rule2, float64(fired)/16000)
			}

			// Only the shipped value is an invariant. 0.8 is measured purely to
			// document why it is not the one in config.yaml.
			if rule2 == cfg.ASR.Nemotron.Rule2MinTrailingSilence && cut {
				t.Errorf("shipped rule2=%.1fs cuts the user off mid-sentence", rule2)
			}
		})
	}
}

// TestProbeStreamingASRLanguage shows why config.yaml ships language: "auto".
// This model is prompt-based: pinning a language the speaker is not using
// returns an EMPTY transcript, not a mislabelled one — silence with no error.
func TestProbeStreamingASRLanguage(t *testing.T) {
	asrProbeGate(t)
	cfg := asrProbeConfig(t)
	_, audio := asrProbeWav16k(t, cfg)

	for _, lang := range []string{"", "auto", "en"} {
		lang := lang
		t.Run("language="+lang, func(t *testing.T) {
			rec, stream := newAsrProbeRecognizer(t, cfg, 0, lang)
			defer sherpa.DeleteOnlineStream(stream)
			defer sherpa.DeleteOnlineRecognizer(rec)

			var final string
			for i := 0; i+asrProbeFrame <= len(audio); i += asrProbeFrame {
				if text := asrProbeFeed(rec, stream, audio[i:i+asrProbeFrame]); text != "" {
					final = text
				}
			}
			t.Logf("[LANG] %-5q -> %d chars: %q", lang, len(final), final)
			if lang != "en" && final == "" {
				t.Errorf("no transcript with language=%q", lang)
			}
		})
	}
}