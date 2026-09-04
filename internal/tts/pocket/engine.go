// Package pocket implements the bundle format produced by the Pocket TTS
// ONNX exporter.  This is deliberately separate from Sherpa's Pocket model:
// the bundle has explicit recurrent state tensors and a tokenizer/model
// manifest that Sherpa's seven-path Pocket API does not consume.
package pocket

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	sp "github.com/tggo/goSentencePiece"
	ort "github.com/yalue/onnxruntime_go"
)

// Config describes a bundle-aware Pocket TTS export.
type Config struct {
	ModelDir        string
	Bundle          string
	Tokenizer       string
	BosBeforeVoice  string
	LmFlow          string
	LmMain          string
	Encoder         string
	Decoder         string
	TextConditioner string
	Precision       string
	Temperature     float32
	LSDSteps        int
	NumThreads      int
	Voice           string
}

type manifestEntry struct {
	Index      int     `json:"index"`
	DType      string  `json:"dtype"`
	Fill       string  `json:"fill"`
	InputName  string  `json:"input_name"`
	OutputName string  `json:"output_name"`
	Shape      []int64 `json:"shape"`
}

type bundleMetadata struct {
	SampleRate                     int             `json:"sample_rate"`
	FrameRate                      float32         `json:"frame_rate"`
	SamplesPerFrame                int             `json:"samples_per_frame"`
	LatentDim                      int             `json:"latent_dim"`
	ConditioningDim                int             `json:"conditioning_dim"`
	MaxTokenPerChunk               int             `json:"max_token_per_chunk"`
	InsertBOSBeforeVoice           bool            `json:"insert_bos_before_voice"`
	TokenizerFile                  string          `json:"tokenizer_file"`
	BOSBeforeVoiceFile             string          `json:"bos_before_voice_file"`
	PadWithSpacesForShortInputs    bool            `json:"pad_with_spaces_for_short_inputs"`
	RemoveSemicolons               bool            `json:"remove_semicolons"`
	ModelRecommendedFramesAfterEOS *int            `json:"model_recommended_frames_after_eos"`
	FlowStateManifest              []manifestEntry `json:"flow_lm_state_manifest"`
	MimiStateManifest              []manifestEntry `json:"mimi_state_manifest"`
}

type stateValue struct {
	entry manifestEntry
	f32   []float32
	i64   []int64
	bools []bool
}

// Engine is a CPU/GPU-provider-neutral bundle-aware Pocket TTS engine. The
// current Go ONNX wrapper uses the CPU provider, which is also the most
// portable path for this export.
type Engine struct {
	meta       bundleMetadata
	bundleDir  string
	tokenizer  *sp.Tokenizer
	flowMain   *ort.DynamicAdvancedSession
	flowNet    *ort.DynamicAdvancedSession
	textCond   *ort.DynamicAdvancedSession
	encoder    *ort.DynamicAdvancedSession
	decoder    *ort.DynamicAdvancedSession
	flowState  []stateValue
	voiceState []stateValue

	temperature float32
	lsdSteps    int
	maxTokens   int
	voicePath   string
	rng         *rand.Rand
	mu          sync.Mutex
}

// New loads a bundle and prepares its configured/default voice state. A
// bundle without a voice file is rejected because this Pocket export has no
// built-in voices.bin; callers must provide a reference WAV in Config.Voice.
func New(cfg Config) (*Engine, error) {
	if cfg.ModelDir == "" {
		return nil, errors.New("Pocket model_dir is empty")
	}
	bundleDir, err := filepath.Abs(cfg.ModelDir)
	if err != nil {
		return nil, fmt.Errorf("resolve Pocket model directory: %w", err)
	}
	bundleName := cfg.Bundle
	if bundleName == "" {
		bundleName = "bundle.json"
	}
	metaBytes, err := os.ReadFile(filepath.Join(bundleDir, bundleName))
	if err != nil {
		return nil, fmt.Errorf("read Pocket bundle metadata: %w", err)
	}
	var meta bundleMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return nil, fmt.Errorf("parse Pocket bundle metadata: %w", err)
	}
	if meta.SampleRate <= 0 || meta.FrameRate <= 0 || meta.LatentDim <= 0 || meta.ConditioningDim <= 0 {
		return nil, errors.New("Pocket bundle metadata has invalid audio/model dimensions")
	}
	if len(meta.FlowStateManifest) == 0 || len(meta.MimiStateManifest) == 0 {
		return nil, errors.New("Pocket bundle metadata is missing recurrent state manifests")
	}
	sort.Slice(meta.FlowStateManifest, func(i, j int) bool { return meta.FlowStateManifest[i].Index < meta.FlowStateManifest[j].Index })
	sort.Slice(meta.MimiStateManifest, func(i, j int) bool { return meta.MimiStateManifest[i].Index < meta.MimiStateManifest[j].Index })

	tokenizerName := cfg.Tokenizer
	if tokenizerName == "" {
		tokenizerName = meta.TokenizerFile
	}
	if tokenizerName == "" {
		return nil, errors.New("Pocket bundle does not specify tokenizer.model")
	}
	tokenizer, err := sp.NewTokenizer(filepath.Join(bundleDir, tokenizerName))
	if err != nil {
		return nil, fmt.Errorf("load Pocket tokenizer: %w", err)
	}

	if !ort.IsInitialized() {
		runtimePath := localRuntimePath()
		if runtimePath != "" {
			ort.SetSharedLibraryPath(runtimePath)
		}
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("initialize ONNX Runtime for Pocket: %w", err)
		}
	}

	threads := cfg.NumThreads
	if threads <= 0 {
		threads = 2
	}
	opts, err := ort.NewSessionOptions()
	if err != nil {
		return nil, fmt.Errorf("create Pocket session options: %w", err)
	}
	defer opts.Destroy()
	if err := opts.SetIntraOpNumThreads(threads); err != nil {
		return nil, fmt.Errorf("set Pocket intra-op threads: %w", err)
	}
	if err := opts.SetInterOpNumThreads(1); err != nil {
		return nil, fmt.Errorf("set Pocket inter-op threads: %w", err)
	}

	precision := strings.ToLower(cfg.Precision)
	if precision == "" {
		precision = "int8"
	}
	flowMainName := chooseQuantized(bundleDir, cfg.LmMain, precision)
	flowNetName := chooseQuantized(bundleDir, cfg.LmFlow, precision)
	encoderName := chooseQuantized(bundleDir, cfg.Encoder, precision)
	decoderName := chooseQuantized(bundleDir, cfg.Decoder, precision)
	textCondName := chooseQuantized(bundleDir, cfg.TextConditioner, precision)
	if flowMainName == "" || flowNetName == "" || decoderName == "" || encoderName == "" || textCondName == "" {
		return nil, errors.New("Pocket bundle model filenames are incomplete")
	}

	e := &Engine{
		meta:        meta,
		bundleDir:   bundleDir,
		tokenizer:   tokenizer,
		temperature: cfg.Temperature,
		lsdSteps:    cfg.LSDSteps,
		maxTokens:   meta.MaxTokenPerChunk,
		voicePath:   cfg.Voice,
		rng:         rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	if e.lsdSteps <= 0 {
		e.lsdSteps = 1
	}
	if e.maxTokens <= 0 {
		e.maxTokens = 50
	}

	flowInputs := []string{"sequence", "text_embeddings"}
	flowInputs = append(flowInputs, stateInputNames(meta.FlowStateManifest)...)
	flowOutputs := []string{"conditioning", "eos_logit"}
	flowOutputs = append(flowOutputs, stateOutputNames(meta.FlowStateManifest)...)
	e.flowMain, err = ort.NewDynamicAdvancedSession(filepath.Join(bundleDir, flowMainName), flowInputs, flowOutputs, opts)
	if err != nil {
		return nil, fmt.Errorf("load Pocket FlowLM main: %w", err)
	}
	e.flowNet, err = ort.NewDynamicAdvancedSession(filepath.Join(bundleDir, flowNetName), []string{"c", "s", "t", "x"}, []string{"flow_dir"}, opts)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("load Pocket FlowLM flow: %w", err)
	}
	e.textCond, err = ort.NewDynamicAdvancedSession(filepath.Join(bundleDir, textCondName), []string{"token_ids"}, []string{"embeddings"}, opts)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("load Pocket text conditioner: %w", err)
	}
	e.encoder, err = ort.NewDynamicAdvancedSession(filepath.Join(bundleDir, encoderName), []string{"audio"}, []string{"latents"}, opts)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("load Pocket voice encoder: %w", err)
	}
	decoderInputs := []string{"latent"}
	decoderInputs = append(decoderInputs, stateInputNames(meta.MimiStateManifest)...)
	decoderOutputs := []string{"audio_frame"}
	decoderOutputs = append(decoderOutputs, stateOutputNames(meta.MimiStateManifest)...)
	e.decoder, err = ort.NewDynamicAdvancedSession(filepath.Join(bundleDir, decoderName), decoderInputs, decoderOutputs, opts)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("load Pocket Mimi decoder: %w", err)
	}

	voicePath := cfg.Voice
	if voicePath != "" && !filepath.IsAbs(voicePath) {
		if _, err := os.Stat(voicePath); err != nil {
			voicePath = filepath.Join(bundleDir, voicePath)
		}
	}
	if voicePath == "" {
		return nil, errors.New("custom Pocket requires pocket.voice or a reference WAV")
	}
	audio, sampleRate, err := loadWAV(voicePath)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("load Pocket voice %q: %w", voicePath, err)
	}
	if sampleRate != meta.SampleRate {
		audio = resampleLinear(audio, sampleRate, meta.SampleRate)
	}
	embeddings, _, err := e.runEncoder(audio)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("encode Pocket voice %q: %w", voicePath, err)
	}
	if cfg.BosBeforeVoice != "" {
		bos, err := loadNpyFloat32(filepath.Join(bundleDir, cfg.BosBeforeVoice))
		if err != nil {
			e.Close()
			return nil, fmt.Errorf("load Pocket BOS voice tensor: %w", err)
		}
		embeddings = prependEmbeddings(bos, embeddings, meta.ConditioningDim)
	} else if meta.InsertBOSBeforeVoice && meta.BOSBeforeVoiceFile != "" {
		bos, err := loadNpyFloat32(filepath.Join(bundleDir, meta.BOSBeforeVoiceFile))
		if err != nil {
			e.Close()
			return nil, fmt.Errorf("load Pocket BOS voice tensor: %w", err)
		}
		embeddings = prependEmbeddings(bos, embeddings, meta.ConditioningDim)
	}
	e.voicePath = voicePath
	e.voiceState, err = e.conditionVoice(embeddings)
	if err != nil {
		e.Close()
		return nil, fmt.Errorf("condition Pocket voice: %w", err)
	}
	return e, nil
}

// SampleRate returns the generated audio sample rate.
func (e *Engine) SampleRate() int { return e.meta.SampleRate }

// Close releases model sessions. The process-wide ONNX Runtime environment is
// intentionally left alive because Mai's Sherpa recognizers may still use it.
func (e *Engine) Close() {
	if e == nil {
		return
	}
	if e.decoder != nil {
		_ = e.decoder.Destroy()
		e.decoder = nil
	}
	if e.encoder != nil {
		_ = e.encoder.Destroy()
		e.encoder = nil
	}
	if e.textCond != nil {
		_ = e.textCond.Destroy()
		e.textCond = nil
	}
	if e.flowNet != nil {
		_ = e.flowNet.Destroy()
		e.flowNet = nil
	}
	if e.flowMain != nil {
		_ = e.flowMain.Destroy()
		e.flowMain = nil
	}
}

// Generate streams decoded audio chunks to cb. The custom Pocket bundle does
// not expose Sherpa's speed field; Mai's existing playback pipeline remains
// responsible for resampling the returned 24 kHz audio.
func (e *Engine) Generate(text string, _ float32, cb func([]float32) bool) error {
	if cb == nil {
		return errors.New("Pocket audio callback is nil")
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	prepared := prepareText(text, e.meta.RemoveSemicolons)
	if prepared == "" {
		return nil
	}
	ids, err := e.tokenizer.Encode(prepared)
	if err != nil {
		return fmt.Errorf("tokenize Pocket text: %w", err)
	}
	if len(ids) == 0 {
		return nil
	}
	if e.maxTokens > 0 && len(ids) > e.maxTokens {
		// Mai already sends sentence-sized requests. Keep the hard guard here so
		// a direct caller cannot silently create an unbounded recurrent context.
		ids = ids[:e.maxTokens]
	}
	textEmbeddings, err := e.runTextConditioner(ids)
	if err != nil {
		return err
	}
	state := cloneState(e.voiceState)
	emptySequence := make([]float32, 0)
	textFrames := len(textEmbeddings) / e.meta.ConditioningDim
	if textFrames <= 0 || len(textEmbeddings)%e.meta.ConditioningDim != 0 {
		return errors.New("Pocket text conditioner returned an invalid embedding shape")
	}
	_, _, state, err = e.runFlowMain(emptySequence, []int64{1, 0, int64(e.meta.LatentDim)}, textEmbeddings, []int64{1, int64(textFrames), int64(e.meta.ConditioningDim)}, state)
	if err != nil {
		return fmt.Errorf("condition Pocket text: %w", err)
	}

	decoderState := initialState(e.meta.MimiStateManifest)
	pending := make([]float32, 0, 12*e.meta.LatentDim)
	pendingFrames := 0
	flush := func(force bool) error {
		if pendingFrames == 0 || (!force && pendingFrames < 12) {
			return nil
		}
		audio, nextState, err := e.runDecoder(pending, pendingFrames, decoderState)
		if err != nil {
			return err
		}
		decoderState = nextState
		pending = pending[:0]
		pendingFrames = 0
		if len(audio) > 0 && !cb(audio) {
			return ErrGenerationStopped
		}
		return nil
	}

	wordCount := len(strings.Fields(prepared))
	framesAfterEOS := 3
	if wordCount > 4 {
		framesAfterEOS = 1
	}
	if e.meta.ModelRecommendedFramesAfterEOS != nil {
		framesAfterEOS = *e.meta.ModelRecommendedFramesAfterEOS
	}
	frameLimit := int(math.Ceil((float64(len(ids))/3.0 + 2.0) * float64(e.meta.FrameRate)))
	if frameLimit < framesAfterEOS+1 {
		frameLimit = framesAfterEOS + 1
	}
	var curr []float32
	eosStep := -1
	for step := 0; step < frameLimit; step++ {
		sequence := curr
		sequenceShape := []int64{1, 1, int64(e.meta.LatentDim)}
		if len(sequence) == 0 {
			sequence = make([]float32, e.meta.LatentDim)
			for i := range sequence {
				sequence[i] = float32(math.NaN())
			}
		}
		emptyText := make([]float32, 0)
		conditioning, eos, nextState, err := e.runFlowMain(sequence, sequenceShape, emptyText, []int64{1, 0, int64(e.meta.ConditioningDim)}, state)
		if err != nil {
			return fmt.Errorf("generate Pocket latent %d: %w", step, err)
		}
		state = nextState
		if eos > -4 && eosStep < 0 {
			eosStep = step
		}
		if eosStep >= 0 && step >= eosStep+framesAfterEOS {
			break
		}

		x := make([]float32, e.meta.LatentDim)
		if e.temperature > 0 {
			for i := range x {
				x[i] = float32(e.rng.NormFloat64() * math.Sqrt(float64(e.temperature)))
			}
		}
		dt := float32(1.0 / float64(e.lsdSteps))
		for flowStep := 0; flowStep < e.lsdSteps; flowStep++ {
			s := float32(float64(flowStep) / float64(e.lsdSteps))
			t := s + dt
			flow, err := e.runFlowNet(conditioning, s, t, x)
			if err != nil {
				return fmt.Errorf("generate Pocket flow %d/%d: %w", flowStep+1, e.lsdSteps, err)
			}
			for i := range x {
				x[i] += flow[i] * dt
			}
		}
		pending = append(pending, x...)
		pendingFrames++
		curr = x
		if err := flush(false); err != nil {
			return err
		}
	}
	if err := flush(true); err != nil {
		return err
	}
	return nil
}

// ErrGenerationStopped is returned when the audio callback asks generation to
// stop, for example when Mai detects barge-in.
var ErrGenerationStopped = errors.New("Pocket generation stopped by caller")

func (e *Engine) conditionVoice(embeddings []float32) ([]stateValue, error) {
	state := initialState(e.meta.FlowStateManifest)
	sequence := make([]float32, 0)
	_, _, state, err := e.runFlowMain(sequence, []int64{1, 0, int64(e.meta.LatentDim)}, embeddings, []int64{1, int64(len(embeddings) / e.meta.ConditioningDim), int64(e.meta.ConditioningDim)}, state)
	return state, err
}

func (e *Engine) runTextConditioner(ids []int) ([]float32, error) {
	data := make([]int64, len(ids))
	for i, id := range ids {
		data[i] = int64(id)
	}
	in, err := ort.NewTensor(ort.NewShape(1, int64(len(data))), data)
	if err != nil {
		return nil, fmt.Errorf("create Pocket token tensor: %w", err)
	}
	defer in.Destroy()
	outputs := []ort.Value{nil}
	if err := e.textCond.Run([]ort.Value{in}, outputs); err != nil {
		return nil, err
	}
	embeddings, err := takeFloatOutput(outputs[0])
	destroyValues(outputs)
	return embeddings, err
}

func (e *Engine) runEncoder(audio []float32) ([]float32, int, error) {
	in, err := newFloatTensor([]int64{1, 1, int64(len(audio))}, audio)
	if err != nil {
		return nil, 0, err
	}
	defer in.Destroy()
	outputs := []ort.Value{nil}
	if err := e.encoder.Run([]ort.Value{in}, outputs); err != nil {
		return nil, 0, err
	}
	data, shape, err := takeFloatOutputWithShape(outputs[0])
	destroyValues(outputs)
	if err != nil {
		return nil, 0, err
	}
	if len(shape) < 3 || shape[len(shape)-1] != int64(e.meta.ConditioningDim) {
		return nil, 0, fmt.Errorf("voice encoder returned unexpected shape %v", shape)
	}
	return data, int(shape[1]), nil
}

func (e *Engine) runFlowMain(sequence []float32, sequenceShape []int64, text []float32, textShape []int64, state []stateValue) ([]float32, float32, []stateValue, error) {
	seq, err := newFloatTensor(sequenceShape, sequence)
	if err != nil {
		return nil, 0, nil, err
	}
	defer seq.Destroy()
	textTensor, err := newFloatTensor(textShape, text)
	if err != nil {
		return nil, 0, nil, err
	}
	defer textTensor.Destroy()
	inputs := []ort.Value{seq, textTensor}
	stateInputs, err := makeStateInputs(state)
	if err != nil {
		return nil, 0, nil, err
	}
	defer destroyValues(stateInputs)
	inputs = append(inputs, stateInputs...)
	outputs := make([]ort.Value, 2+len(stateOutputNames(e.meta.FlowStateManifest)))
	if err := e.flowMain.Run(inputs, outputs); err != nil {
		destroyValues(outputs)
		return nil, 0, nil, err
	}
	conditioning, err := takeFloatOutput(outputs[0])
	if err != nil {
		destroyValues(outputs)
		return nil, 0, nil, err
	}
	eosData, err := takeFloatOutput(outputs[1])
	if err != nil {
		destroyValues(outputs)
		return nil, 0, nil, err
	}
	if len(eosData) == 0 {
		destroyValues(outputs)
		return nil, 0, nil, errors.New("Pocket FlowLM returned empty EOS logit")
	}

	next := cloneState(state)
	outputPos := 2
	for i := range next {
		if outputPos >= len(outputs) {
			destroyValues(outputs)
			return nil, 0, nil, errors.New("Pocket FlowLM returned too few state outputs")
		}
		value, err := stateFromOutput(outputs[outputPos], next[i].entry)
		if err != nil {
			destroyValues(outputs)
			return nil, 0, nil, err
		}
		next[i] = value
		outputPos++
	}
	destroyValues(outputs)
	return conditioning, eosData[0], next, nil
}

func (e *Engine) runFlowNet(conditioning []float32, s, t float32, x []float32) ([]float32, error) {
	c, err := newFloatTensor([]int64{1, int64(e.meta.ConditioningDim)}, conditioning)
	if err != nil {
		return nil, err
	}
	defer c.Destroy()
	sTensor, err := newFloatTensor([]int64{1, 1}, []float32{s})
	if err != nil {
		return nil, err
	}
	defer sTensor.Destroy()
	tTensor, err := newFloatTensor([]int64{1, 1}, []float32{t})
	if err != nil {
		return nil, err
	}
	defer tTensor.Destroy()
	xTensor, err := newFloatTensor([]int64{1, int64(e.meta.LatentDim)}, x)
	if err != nil {
		return nil, err
	}
	defer xTensor.Destroy()
	outputs := []ort.Value{nil}
	if err := e.flowNet.Run([]ort.Value{c, sTensor, tTensor, xTensor}, outputs); err != nil {
		return nil, err
	}
	data, err := takeFloatOutput(outputs[0])
	destroyValues(outputs)
	return data, err
}

func (e *Engine) runDecoder(latents []float32, frames int, state []stateValue) ([]float32, []stateValue, error) {
	latent, err := newFloatTensor([]int64{1, int64(frames), int64(e.meta.LatentDim)}, latents)
	if err != nil {
		return nil, nil, err
	}
	defer latent.Destroy()
	stateInputs, err := makeStateInputs(state)
	if err != nil {
		return nil, nil, err
	}
	defer destroyValues(stateInputs)
	inputs := append([]ort.Value{latent}, stateInputs...)
	outputs := make([]ort.Value, 1+len(stateOutputNames(e.meta.MimiStateManifest)))
	if err := e.decoder.Run(inputs, outputs); err != nil {
		destroyValues(outputs)
		return nil, nil, err
	}
	audio, err := takeFloatOutput(outputs[0])
	if err != nil {
		destroyValues(outputs)
		return nil, nil, err
	}
	next := cloneState(state)
	outputPos := 1
	for i := range next {
		if outputPos >= len(outputs) {
			destroyValues(outputs)
			return nil, nil, errors.New("Pocket decoder returned too few state outputs")
		}
		value, err := stateFromOutput(outputs[outputPos], next[i].entry)
		if err != nil {
			destroyValues(outputs)
			return nil, nil, err
		}
		next[i] = value
		outputPos++
	}
	destroyValues(outputs)
	return audio, next, nil
}

func chooseQuantized(dir, name, precision string) string {
	if name == "" {
		return ""
	}
	if precision != "int8" || strings.Contains(name, ".int8.") {
		return name
	}
	if strings.HasSuffix(name, ".onnx") {
		candidate := strings.TrimSuffix(name, ".onnx") + ".int8.onnx"
		if _, err := os.Stat(filepath.Join(dir, candidate)); err == nil {
			return candidate
		}
	}
	return name
}

func stateInputNames(entries []manifestEntry) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.InputName)
	}
	return result
}

func stateOutputNames(entries []manifestEntry) []string {
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		result = append(result, entry.OutputName)
	}
	return result
}

func initialState(entries []manifestEntry) []stateValue {
	result := make([]stateValue, len(entries))
	for i, entry := range entries {
		value := stateValue{entry: entry}
		count := shapeSize(entry.Shape)
		switch entry.DType {
		case "float32":
			value.f32 = make([]float32, count)
			if entry.Fill == "nan" {
				for j := range value.f32 {
					value.f32[j] = float32(math.NaN())
				}
			}
		case "int64":
			value.i64 = make([]int64, count)
		case "bool":
			value.bools = make([]bool, count)
			if entry.Fill == "ones" {
				for j := range value.bools {
					value.bools[j] = true
				}
			}
		}
		result[i] = value
	}
	return result
}

func cloneState(source []stateValue) []stateValue {
	result := make([]stateValue, len(source))
	for i, value := range source {
		result[i] = value
		result[i].entry.Shape = append([]int64(nil), value.entry.Shape...)
		result[i].f32 = append([]float32(nil), value.f32...)
		result[i].i64 = append([]int64(nil), value.i64...)
		result[i].bools = append([]bool(nil), value.bools...)
	}
	return result
}

func makeStateInputs(state []stateValue) ([]ort.Value, error) {
	result := make([]ort.Value, len(state))
	for i, value := range state {
		v, err := newStateTensor(value)
		if err != nil {
			destroyValues(result)
			return nil, fmt.Errorf("create Pocket state %s: %w", value.entry.InputName, err)
		}
		result[i] = v
	}
	return result, nil
}

func newStateTensor(value stateValue) (ort.Value, error) {
	shape := ort.NewShape(value.entry.Shape...)
	switch value.entry.DType {
	case "float32":
		return newFloatTensor(shape, value.f32)
	case "int64":
		data := value.i64
		if len(data) == 0 {
			data = []int64{0}
		}
		return ort.NewTensor(shape, data)
	case "bool":
		data := value.bools
		if len(data) == 0 {
			data = []bool{false}
		}
		return ort.NewTensor(shape, data)
	default:
		return nil, fmt.Errorf("unsupported state dtype %q", value.entry.DType)
	}
}

func stateFromOutput(output ort.Value, entry manifestEntry) (stateValue, error) {
	value := stateValue{entry: entry}
	if output == nil {
		return value, errors.New("Pocket state output is nil")
	}
	value.entry.Shape = append([]int64(nil), output.GetShape()...)
	switch entry.DType {
	case "float32":
		t, ok := output.(*ort.Tensor[float32])
		if !ok {
			return value, errors.New("Pocket state output type is not float32")
		}
		value.f32 = append([]float32(nil), t.GetData()...)
	case "int64":
		t, ok := output.(*ort.Tensor[int64])
		if !ok {
			return value, errors.New("Pocket state output type is not int64")
		}
		value.i64 = append([]int64(nil), t.GetData()...)
	case "bool":
		t, ok := output.(*ort.Tensor[bool])
		if !ok {
			return value, errors.New("Pocket state output type is not bool")
		}
		value.bools = append([]bool(nil), t.GetData()...)
	default:
		return value, fmt.Errorf("unsupported Pocket state dtype %q", entry.DType)
	}
	return value, nil
}

func takeFloatOutput(output ort.Value) ([]float32, error) {
	data, _, err := takeFloatOutputWithShape(output)
	return data, err
}

func takeFloatOutputWithShape(output ort.Value) ([]float32, []int64, error) {
	if output == nil {
		return nil, nil, errors.New("Pocket ONNX output is nil")
	}
	t, ok := output.(*ort.Tensor[float32])
	if !ok {
		return nil, nil, errors.New("Pocket ONNX output type is not float32")
	}
	data := append([]float32(nil), t.GetData()...)
	shape := append([]int64(nil), t.GetShape()...)
	return data, shape, nil
}

func newFloatTensor(shape []int64, data []float32) (*ort.Tensor[float32], error) {
	return ort.NewTensor(ort.NewShape(shape...), data)
}

func destroyValues(values []ort.Value) {
	for _, value := range values {
		if value != nil {
			_ = value.Destroy()
		}
	}
}

func hasZeroDimension(shape []int64) bool {
	for _, dim := range shape {
		if dim == 0 {
			return true
		}
	}
	return false
}

func shapeSize(shape []int64) int {
	if hasZeroDimension(shape) {
		return 0
	}
	size := int64(1)
	for _, dim := range shape {
		if dim <= 0 {
			return 0
		}
		size *= dim
	}
	return int(size)
}

func localRuntimePath() string {
	if exe, err := os.Executable(); err == nil {
		path := filepath.Join(filepath.Dir(exe), "onnxruntime.dll")
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	if path, err := filepath.Abs("onnxruntime.dll"); err == nil {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func prepareText(text string, removeSemicolons bool) string {
	text = strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(text))
	if text == "" {
		return ""
	}
	if removeSemicolons {
		text = strings.ReplaceAll(text, ";", ",")
	}
	runes := []rune(text)
	if len(runes) > 0 && !unicode.IsUpper(runes[0]) {
		runes[0] = unicode.ToUpper(runes[0])
		text = string(runes)
	}
	last := []rune(text)
	if len(last) > 0 && (unicode.IsLetter(last[len(last)-1]) || unicode.IsDigit(last[len(last)-1])) {
		text += "."
	}
	return text
}

func prependEmbeddings(bos, embeddings []float32, dim int) []float32 {
	if dim <= 0 || len(bos) == 0 {
		return embeddings
	}
	if len(bos)%dim != 0 || len(embeddings)%dim != 0 {
		return embeddings
	}
	result := make([]float32, 0, len(bos)+len(embeddings))
	result = append(result, bos...)
	result = append(result, embeddings...)
	return result
}

func loadNpyFloat32(path string) ([]float32, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < 12 || string(data[:6]) != "\x93NUMPY" {
		return nil, errors.New("invalid NPY header")
	}
	major := data[6]
	headerLen := 0
	offset := 0
	switch major {
	case 1:
		if len(data) < 10 {
			return nil, errors.New("truncated NPY header")
		}
		headerLen = int(binary.LittleEndian.Uint16(data[8:10]))
		offset = 10
	case 2, 3:
		if len(data) < 12 {
			return nil, errors.New("truncated NPY header")
		}
		headerLen = int(binary.LittleEndian.Uint32(data[8:12]))
		offset = 12
	default:
		return nil, fmt.Errorf("unsupported NPY version %d", major)
	}
	if offset+headerLen > len(data) {
		return nil, errors.New("truncated NPY data")
	}
	header := string(data[offset : offset+headerLen])
	if !strings.Contains(header, "'<f4'") && !strings.Contains(header, "\"<f4\"") {
		return nil, errors.New("BOS NPY is not little-endian float32")
	}
	payload := data[offset+headerLen:]
	if len(payload)%4 != 0 {
		return nil, errors.New("invalid float32 NPY payload")
	}
	result := make([]float32, len(payload)/4)
	for i := range result {
		result[i] = math.Float32frombits(binary.LittleEndian.Uint32(payload[i*4:]))
	}
	return result, nil
}

func loadWAV(path string) ([]float32, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, errors.New("unsupported WAV container")
	}
	var sampleRate, channels, bits int
	var pcm []byte
	for pos := 12; pos+8 <= len(data); {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		pos += 8
		if size < 0 || pos+size > len(data) {
			return nil, 0, errors.New("truncated WAV chunk")
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return nil, 0, errors.New("invalid WAV format chunk")
			}
			format := binary.LittleEndian.Uint16(data[pos : pos+2])
			channels = int(binary.LittleEndian.Uint16(data[pos+2 : pos+4]))
			sampleRate = int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
			bits = int(binary.LittleEndian.Uint16(data[pos+14 : pos+16]))
			if format != 1 {
				return nil, 0, errors.New("only PCM WAV voice references are supported")
			}
		case "data":
			pcm = data[pos : pos+size]
		}
		pos += size
		if size%2 != 0 {
			pos++
		}
	}
	if sampleRate <= 0 || channels <= 0 || bits != 16 || len(pcm) == 0 {
		return nil, 0, errors.New("WAV must contain PCM16 audio")
	}
	frames := len(pcm) / (channels * 2)
	audio := make([]float32, frames)
	for i := 0; i < frames; i++ {
		var sum float32
		for ch := 0; ch < channels; ch++ {
			off := (i*channels + ch) * 2
			sum += float32(int16(binary.LittleEndian.Uint16(pcm[off:off+2]))) / 32768.0
		}
		audio[i] = sum / float32(channels)
	}
	return audio, sampleRate, nil
}

func resampleLinear(input []float32, from, to int) []float32 {
	if from <= 0 || to <= 0 || from == to || len(input) == 0 {
		return input
	}
	length := int(math.Round(float64(len(input)) * float64(to) / float64(from)))
	if length < 1 {
		length = 1
	}
	output := make([]float32, length)
	for i := range output {
		pos := float64(i) * float64(from) / float64(to)
		left := int(pos)
		if left >= len(input)-1 {
			output[i] = input[len(input)-1]
			continue
		}
		frac := float32(pos - float64(left))
		output[i] = input[left]*(1-frac) + input[left+1]*frac
	}
	return output
}
