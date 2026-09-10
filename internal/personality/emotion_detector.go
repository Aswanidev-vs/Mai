package personality

import (
	"log"
	"math"
	"strings"
	"sync"
	"time"
)

type EmotionType string

const (
	EmotionNeutral  EmotionType = "neutral"
	EmotionHappy    EmotionType = "happy"
	EmotionSad      EmotionType = "sad"
	EmotionStressed EmotionType = "stressed"
	EmotionExcited  EmotionType = "excited"
	EmotionFrustrated EmotionType = "frustrated"
	EmotionCalm     EmotionType = "calm"
)

type EmotionState struct {
	Type       EmotionType `json:"type"`
	Confidence float64     `json:"confidence"`
	Arousal    float64     `json:"arousal"`    // 0=calm, 1=agitated
	Valence    float64     `json:"valence"`    // 0=negative, 1=positive
	Timestamp  time.Time   `json:"timestamp"`
	Source     string      `json:"source"`     // "prosody", "text", "combined"
}

// keywordEntry pairs a trigger word with an intensity weight. Strong words
// (love, hate, amazing) weigh more than mild ones (good, nice, okay) so the
// resulting confidence reflects how strongly the user expressed the emotion.
type keywordEntry struct {
	word      string
	intensity float64
}

type EmotionDetector struct {
	mu           sync.RWMutex
	current      EmotionState
	history      []EmotionState
	maxHistory   int
	textKeywords map[EmotionType][]keywordEntry

	// Continuous-state smoothing / decay controls.
	blendFactor float64       // how much a new reading moves the current state (0..1)
	decayTime   time.Duration // after this idle period the emotion drifts toward neutral
	lastUpdate  time.Time     // timestamp of the last recorded / updated state
}

// negationWords suppress a nearby emotional keyword (e.g. "I'm not happy").
var negationWords = map[string]bool{
	"not": true, "no": true, "n't": true, "never": true, "don't": true,
	"isn't": true, "aren't": true, "won't": true, "can't": true,
	"didn't": true, "doesn't": true, "without": true,
}

// switchThreshold is the hysteresis band: an emotion only flips when a new
// reading is this much more confident than the current one.
const switchThreshold = 0.15

func NewEmotionDetector() *EmotionDetector {
	return &EmotionDetector{
		current: EmotionState{Type: EmotionNeutral, Confidence: 1.0, Valence: 0.5, Arousal: 0.3, Timestamp: time.Now(), Source: "default"},
		history: make([]EmotionState, 0, 100),
		maxHistory: 100,
		blendFactor: 0.5,
		decayTime: 30 * time.Second,
		lastUpdate: time.Now(),
		textKeywords: map[EmotionType][]keywordEntry{
			EmotionHappy: {
				{word: "love", intensity: 1.6},
				{word: "amazing", intensity: 1.5},
				{word: "incredible", intensity: 1.5},
				{word: "wonderful", intensity: 1.4},
				{word: "awesome", intensity: 1.4},
				{word: "fantastic", intensity: 1.4},
				{word: "excellent", intensity: 1.3},
				{word: "great", intensity: 1.2},
				{word: "happy", intensity: 1.2},
				{word: "good", intensity: 0.7},
				{word: "nice", intensity: 0.6},
				{word: "thanks", intensity: 0.6},
				{word: "thank", intensity: 0.6},
			},
			EmotionSad: {
				{word: "depressed", intensity: 1.5},
				{word: "lonely", intensity: 1.3},
				{word: "unhappy", intensity: 1.3},
				{word: "sad", intensity: 1.2},
				{word: "disappointed", intensity: 1.2},
				{word: "miss", intensity: 0.9},
				{word: "unfortunately", intensity: 0.7},
				{word: "sorry", intensity: 0.7},
			},
			EmotionStressed: {
				{word: "overwhelmed", intensity: 1.5},
				{word: "panic", intensity: 1.4},
				{word: "stressed", intensity: 1.3},
				{word: "anxious", intensity: 1.3},
				{word: "worried", intensity: 1.1},
				{word: "nervous", intensity: 1.1},
				{word: "deadline", intensity: 0.9},
				{word: "urgent", intensity: 0.9},
				{word: "hurry", intensity: 0.6},
			},
			EmotionExcited: {
				{word: "incredible", intensity: 1.5},
				{word: "amazing", intensity: 1.5},
				{word: "excited", intensity: 1.4},
				{word: "awesome", intensity: 1.4},
				{word: "brilliant", intensity: 1.4},
				{word: "can't wait", intensity: 1.4},
				{word: "wow", intensity: 1.3},
				{word: "cool", intensity: 0.8},
			},
			EmotionFrustrated: {
				{word: "hate", intensity: 1.6},
				{word: "angry", intensity: 1.4},
				{word: "frustrated", intensity: 1.3},
				{word: "doesn't work", intensity: 1.3},
				{word: "not working", intensity: 1.3},
				{word: "annoyed", intensity: 1.2},
				{word: "stupid", intensity: 1.2},
				{word: "broken", intensity: 1.1},
				{word: "failed", intensity: 1.1},
				{word: "error", intensity: 0.9},
			},
			EmotionCalm: {
				{word: "peaceful", intensity: 1.3},
				{word: "serene", intensity: 1.3},
				{word: "calm", intensity: 1.2},
				{word: "relaxed", intensity: 1.2},
				{word: "chill", intensity: 1.0},
				{word: "quiet", intensity: 0.8},
			},
		},
	}
}

func (ed *EmotionDetector) DetectFromText(text string) EmotionState {
	return ed.record(ed.detectFromText(text))
}

// DetectFromTextNoRecord runs the same keyword detection as DetectFromText but
// returns the raw state WITHOUT recording it to the shared detector. It is used
// for Mai's OWN sentiment (her response text) so that her expression never
// overwrites the user's emotion state that drives empathetic response + TTS.
func (ed *EmotionDetector) DetectFromTextNoRecord(text string) EmotionState {
	return ed.detectFromText(text)
}

func (ed *EmotionDetector) detectFromText(text string) EmotionState {
	lower := strings.ToLower(text)

	scores := make(map[EmotionType]float64)
	for _, sentence := range splitSentences(lower) {
		tokens := tokenize(sentence)
		negated := markNegated(tokens)
		for emotion, entries := range ed.textKeywords {
			for _, entry := range entries {
				kwTokens := strings.Fields(entry.word)
				if len(kwTokens) == 0 || len(kwTokens) > len(tokens) {
					continue
				}
				// Keywords that themselves contain a negation word (e.g.
				// "not working") are already emotional signals and must not
				// be suppressed by the negation scanner.
				kwHasNegation := false
				for _, kt := range kwTokens {
					if negationWords[kt] {
						kwHasNegation = true
						break
					}
				}
				for i := 0; i+len(kwTokens) <= len(tokens); i++ {
					if !matchTokens(tokens[i:i+len(kwTokens)], kwTokens) {
						continue
					}
					// A negation word within the ~4-word window before the
					// keyword suppresses the emotion instead of reinforcing it.
					if !kwHasNegation && isNegated(negated, i, i+len(kwTokens)) {
						scores[emotion] += entry.intensity * 0.15
					} else {
						scores[emotion] += entry.intensity
					}
				}
			}
		}
	}

	// Find dominant emotion.
	var bestEmotion EmotionType = EmotionNeutral
	var bestScore float64 = 0
	for emotion, score := range scores {
		if score > bestScore {
			bestScore = score
			bestEmotion = emotion
		}
	}

	// Scale the intensity-weighted score into a confidence; stronger words
	// push the confidence higher than mild ones.
	confidence := math.Min(1.0, bestScore/2.0)
	if confidence < 0.2 {
		bestEmotion = EmotionNeutral
		confidence = 0.8
	}

	state := EmotionState{
		Type:       bestEmotion,
		Confidence: confidence,
		Arousal:    ed.calculateArousal(bestEmotion, confidence),
		Valence:    ed.calculateValence(bestEmotion, confidence),
		Timestamp:  time.Now(),
		Source:     "text",
	}

	return state
}

// splitSentences breaks text on sentence-ending punctuation so negation only
// applies within the same sentence.
func splitSentences(s string) []string {
	var sentences []string
	for _, part := range strings.FieldsFunc(s, func(r rune) bool {
		return r == '.' || r == '!' || r == '?' || r == ';'
	}) {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			sentences = append(sentences, trimmed)
		}
	}
	if len(sentences) == 0 {
		sentences = []string{s}
	}
	return sentences
}

// tokenize splits a sentence into punctuation-stripped, lowercased tokens.
func tokenize(sentence string) []string {
	raw := strings.Fields(sentence)
	tokens := make([]string, 0, len(raw))
	for _, t := range raw {
		t = strings.Trim(t, ".,!?;:'\"()[]-")
		if t != "" {
			tokens = append(tokens, t)
		}
	}
	return tokens
}

// markNegated flags tokens that fall within a ~4-word window after a negation
// word in the same sentence.
func markNegated(tokens []string) []bool {
	negated := make([]bool, len(tokens))
	for i, tok := range tokens {
		if negationWords[tok] {
			end := i + 5
			if end > len(tokens) {
				end = len(tokens)
			}
			for j := i + 1; j < end; j++ {
				negated[j] = true
			}
		}
	}
	return negated
}

// matchTokens reports whether the token slice equals the keyword token slice.
func matchTokens(have, want []string) bool {
	if len(have) != len(want) {
		return false
	}
	for i := range have {
		if have[i] != want[i] {
			return false
		}
	}
	return true
}

// isNegated reports whether any token in [start, end) is negated.
func isNegated(negated []bool, start, end int) bool {
	for i := start; i < end; i++ {
		if negated[i] {
			return true
		}
	}
	return false
}

func (ed *EmotionDetector) DetectFromProsody(rmsEnergy, pitch, speechRate float64) EmotionState {
	arousal := math.Min(1.0, rmsEnergy*5.0)
	valence := 0.5

	if pitch > 200 {
		valence += 0.2
		arousal += 0.1
	} else if pitch < 100 && pitch > 0 {
		valence -= 0.2
	}

	if speechRate > 4.0 {
		arousal += 0.2
	} else if speechRate < 2.0 && speechRate > 0 {
		arousal -= 0.1
	}

	arousal = math.Max(0, math.Min(1, arousal))
	valence = math.Max(0, math.Min(1, valence))

	var emotion EmotionType
	switch {
	case arousal > 0.7 && valence > 0.6:
		emotion = EmotionExcited
	case arousal > 0.7 && valence < 0.4:
		emotion = EmotionStressed
	case arousal < 0.3 && valence > 0.6:
		emotion = EmotionCalm
	case arousal < 0.3 && valence < 0.4:
		emotion = EmotionSad
	case valence > 0.6:
		emotion = EmotionHappy
	case valence < 0.4:
		emotion = EmotionFrustrated
	default:
		emotion = EmotionNeutral
	}

	confidence := math.Abs(valence-0.5)*2 * 0.5 + math.Abs(arousal-0.5)*2*0.5

	state := EmotionState{
		Type:       emotion,
		Confidence: math.Min(1, confidence),
		Arousal:    arousal,
		Valence:    valence,
		Timestamp:  time.Now(),
		Source:     "prosody",
	}

	return ed.record(state)
}

func (ed *EmotionDetector) GetCurrent() EmotionState {
	ed.mu.RLock()
	defer ed.mu.RUnlock()
	return ed.current
}

// MergeProsody blends a prosody-derived state over the text-derived state.
// How someone *says* something ("I'm fine" + stressed prosody) is a stronger
// emotional signal than the literal words, so a confident non-neutral
// prosody result wins. Returns the merged state and records it as current.
func (ed *EmotionDetector) MergeProsody(text, prosody EmotionState) EmotionState {
	if prosody.Type == "" || prosody.Type == EmotionNeutral || prosody.Confidence < 0.5 {
		return text
	}
	merged := prosody
	merged.Source = "combined"
	// Explicit text emotion with higher confidence still wins (e.g. "I'm so
	// excited!" said flatly) — words can carry intent the voice hides.
	if text.Type != EmotionNeutral && text.Confidence > prosody.Confidence {
		merged.Type = text.Type
	}
	return ed.record(merged)
}

func (ed *EmotionDetector) GetHistory(n int) []EmotionState {
	ed.mu.RLock()
	defer ed.mu.RUnlock()

	if n > len(ed.history) {
		n = len(ed.history)
	}
	return ed.history[len(ed.history)-n:]
}

func (ed *EmotionDetector) GetDominantEmotion(window time.Duration) EmotionType {
	ed.mu.RLock()
	defer ed.mu.RUnlock()

	cutoff := time.Now().Add(-window)
	counts := make(map[EmotionType]int)

	for _, e := range ed.history {
		if e.Timestamp.After(cutoff) {
			counts[e.Type]++
		}
	}

	var best EmotionType = EmotionNeutral
	var bestCount int = 0
	for emotion, count := range counts {
		if count > bestCount {
			bestCount = count
			best = emotion
		}
	}

	return best
}

func (ed *EmotionDetector) record(state EmotionState) EmotionState {
	ed.mu.Lock()
	defer ed.mu.Unlock()

	blended := ed.blendLocked(state)
	ed.current = blended
	ed.history = append(ed.history, blended)
	if len(ed.history) > ed.maxHistory {
		ed.history = ed.history[len(ed.history)-ed.maxHistory:]
	}
	ed.lastUpdate = time.Now()

	log.Printf("[Emotion] Detected: %s (confidence: %.2f, arousal: %.2f, valence: %.2f, source: %s)",
		blended.Type, blended.Confidence, blended.Arousal, blended.Valence, blended.Source)
	return blended
}

// Update blends a newly detected state toward the current state using
// hysteresis (no flip on small confidence changes) and records the update time.
func (ed *EmotionDetector) Update(state EmotionState) EmotionState {
	ed.mu.Lock()
	defer ed.mu.Unlock()

	blended := ed.blendLocked(state)
	ed.current = blended
	ed.history = append(ed.history, blended)
	if len(ed.history) > ed.maxHistory {
		ed.history = ed.history[len(ed.history)-ed.maxHistory:]
	}
	ed.lastUpdate = time.Now()

	log.Printf("[Emotion] Updated: %s (confidence: %.2f, arousal: %.2f, valence: %.2f, source: %s)",
		blended.Type, blended.Confidence, blended.Arousal, blended.Valence, blended.Source)
	return blended
}

// Decay drifts the current emotion toward neutral when no new reading has
// arrived within decayTime, so stale emotions fade out over time.
func (ed *EmotionDetector) Decay() EmotionState {
	ed.mu.Lock()
	defer ed.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(ed.lastUpdate)
	if elapsed <= ed.decayTime {
		return ed.current
	}

	// decayFactor grows from 0 at decayTime toward 1 as more time passes.
	decayFactor := math.Min(1.0, elapsed.Seconds()/ed.decayTime.Seconds())

	cur := ed.current
	cur.Confidence *= (1 - 0.5*decayFactor)
	cur.Arousal = cur.Arousal*(1-0.5*decayFactor) + 0.3*0.5*decayFactor
	cur.Valence = cur.Valence*(1-0.5*decayFactor) + 0.5*0.5*decayFactor
	if cur.Confidence < 0.2 {
		cur.Type = EmotionNeutral
		cur.Confidence = 0.5
	}
	cur.Timestamp = now
	ed.current = cur
	ed.lastUpdate = now

	log.Printf("[Emotion] Decayed: %s (confidence: %.2f)", cur.Type, cur.Confidence)
	return cur
}

// blendLocked implements hysteresis-based smoothing. It must be called with
// ed.mu held.
func (ed *EmotionDetector) blendLocked(state EmotionState) EmotionState {
	cur := ed.current
	if cur.Type == "" {
		cur = EmotionState{Type: EmotionNeutral, Confidence: 0.5, Valence: 0.5, Arousal: 0.3}
	}

	blended := state
	blended.Timestamp = time.Now()

	switch {
	case state.Type == cur.Type:
		// Same emotion: smooth toward the new reading.
		blended.Confidence = ed.blendFactor*state.Confidence + (1-ed.blendFactor)*cur.Confidence
		blended.Arousal = ed.blendFactor*state.Arousal + (1-ed.blendFactor)*cur.Arousal
		blended.Valence = ed.blendFactor*state.Valence + (1-ed.blendFactor)*cur.Valence
	case cur.Type == EmotionNeutral || cur.Confidence < 0.3:
		// No strong current emotion: adopt the new reading (still smoothed).
		blended.Confidence = ed.blendFactor*state.Confidence + (1-ed.blendFactor)*cur.Confidence
	case state.Confidence > cur.Confidence+switchThreshold:
		// Confident new emotion: switch, keeping some continuity in confidence.
		blended.Confidence = ed.blendFactor*state.Confidence + (1-ed.blendFactor)*cur.Confidence
	default:
		// Small confidence change: don't flip. Keep the current emotion but
		// pull its confidence toward the new reading so repeated signals can
		// eventually overcome the hysteresis band.
		blended = cur
		blended.Confidence = ed.blendFactor*state.Confidence + (1-ed.blendFactor)*cur.Confidence
		blended.Timestamp = time.Now()
	}

	return blended
}

func (ed *EmotionDetector) calculateArousal(emotion EmotionType, confidence float64) float64 {
	base := map[EmotionType]float64{
		EmotionNeutral:    0.3,
		EmotionHappy:      0.6,
		EmotionSad:        0.2,
		EmotionStressed:   0.8,
		EmotionExcited:    0.9,
		EmotionFrustrated: 0.7,
		EmotionCalm:       0.1,
	}
	if b, ok := base[emotion]; ok {
		return b*confidence + 0.3*(1-confidence)
	}
	return 0.3
}

func (ed *EmotionDetector) calculateValence(emotion EmotionType, confidence float64) float64 {
	base := map[EmotionType]float64{
		EmotionNeutral:    0.5,
		EmotionHappy:      0.8,
		EmotionSad:        0.2,
		EmotionStressed:   0.3,
		EmotionExcited:    0.9,
		EmotionFrustrated: 0.2,
		EmotionCalm:       0.7,
	}
	if b, ok := base[emotion]; ok {
		return b*confidence + 0.5*(1-confidence)
	}
	return 0.5
}
