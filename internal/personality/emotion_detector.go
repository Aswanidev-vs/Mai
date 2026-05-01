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

type EmotionDetector struct {
	mu             sync.RWMutex
	current        EmotionState
	history        []EmotionState
	maxHistory     int
	textKeywords   map[EmotionType][]string
}

func NewEmotionDetector() *EmotionDetector {
	return &EmotionDetector{
		current: EmotionState{Type: EmotionNeutral, Confidence: 1.0, Valence: 0.5, Arousal: 0.3, Timestamp: time.Now(), Source: "default"},
		history: make([]EmotionState, 0, 100),
		maxHistory: 100,
		textKeywords: map[EmotionType][]string{
			EmotionHappy:      {"happy", "great", "awesome", "love", "wonderful", "excellent", "amazing", "fantastic", "good", "nice", "thanks", "thank"},
			EmotionSad:        {"sad", "unfortunately", "sorry", "miss", "lonely", "depressed", "unhappy", "disappointed"},
			EmotionStressed:   {"stressed", "anxious", "worried", "nervous", "overwhelmed", "deadline", "urgent", "panic", "hurry"},
			EmotionExcited:    {"excited", "can't wait", "amazing", "incredible", "wow", "awesome", "brilliant", "cool"},
			EmotionFrustrated: {"frustrated", "annoyed", "angry", "hate", "stupid", "broken", "doesn't work", "not working", "failed", "error"},
			EmotionCalm:       {"calm", "relaxed", "peaceful", "quiet", "serene", "chill"},
		},
	}
}

func (ed *EmotionDetector) DetectFromText(text string) EmotionState {
	lower := strings.ToLower(text)

	scores := make(map[EmotionType]float64)
	for emotion, keywords := range ed.textKeywords {
		for _, kw := range keywords {
			if strings.Contains(lower, kw) {
				scores[emotion] += 0.3
			}
		}
	}

	// Find dominant emotion
	var bestEmotion EmotionType = EmotionNeutral
	var bestScore float64 = 0
	for emotion, score := range scores {
		if score > bestScore {
			bestScore = score
			bestEmotion = emotion
		}
	}

	if bestScore > 1.0 {
		bestScore = 1.0
	}
	if bestScore < 0.2 {
		bestEmotion = EmotionNeutral
		bestScore = 0.8
	}

	state := EmotionState{
		Type:       bestEmotion,
		Confidence: bestScore,
		Arousal:    ed.calculateArousal(bestEmotion, bestScore),
		Valence:    ed.calculateValence(bestEmotion, bestScore),
		Timestamp:  time.Now(),
		Source:     "text",
	}

	ed.record(state)
	return state
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

	ed.record(state)
	return state
}

func (ed *EmotionDetector) GetCurrent() EmotionState {
	ed.mu.RLock()
	defer ed.mu.RUnlock()
	return ed.current
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

func (ed *EmotionDetector) record(state EmotionState) {
	ed.mu.Lock()
	defer ed.mu.Unlock()

	ed.current = state
	ed.history = append(ed.history, state)

	if len(ed.history) > ed.maxHistory {
		ed.history = ed.history[len(ed.history)-ed.maxHistory:]
	}

	log.Printf("[Emotion] Detected: %s (confidence: %.2f, arousal: %.2f, valence: %.2f, source: %s)",
		state.Type, state.Confidence, state.Arousal, state.Valence, state.Source)
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
