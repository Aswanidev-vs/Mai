package personality

import (
	"log"
	"math"
	"strings"
)

type TTSParams struct {
	Speed      float32 `json:"speed"`
	Pitch      float32 `json:"pitch"`
	Volume     float32 `json:"volume"`
	Emphasis   float32 `json:"emphasis"`
	PauseScale float32 `json:"pause_scale"`
}

type TTSAdapter struct {
	baseSpeed  float32
	basePitch  float32
	baseVolume float32
	styleName  string
}

// Voice style presets that map to TTSAdapter base values
var voiceStyles = map[string]struct {
	speed  float32
	pitch  float32
	volume float32
}{
	"calm":       {0.9, 0.95, 0.9},    // Composed, measured
	"warm":       {1.0, 1.05, 1.05},   // Gentle, friendly
	"energetic":  {1.15, 1.08, 1.1},   // Bright, fast
	"serious":    {0.95, 0.95, 1.0},   // Even, authoritative
	"soft":       {0.85, 0.95, 0.85},  // Quiet, gentle
	"neutral":    {1.0, 1.0, 1.0},     // Default
}

// ParseVoiceStyle extracts a TTS voice style keyword from the system prompt.
// It scans for known style keywords in the prompt text and returns the first match.
// If nothing is found, returns "neutral".
func ParseVoiceStyle(systemPrompt string) string {
	lower := strings.ToLower(systemPrompt)
	for style := range voiceStyles {
		if style == "neutral" {
			continue
		}
		// Check for explicit style indicators
		if strings.Contains(lower, "speak "+style) ||
			strings.Contains(lower, "voice "+style) ||
			strings.Contains(lower, "tone "+style) ||
			strings.Contains(lower, "be "+style) ||
			strings.Contains(lower, style+" voice") {
			return style
		}
	}
	// Also check for implicit style cues
	if strings.Contains(lower, "quiet") || strings.Contains(lower, "gentle") || strings.Contains(lower, "softly") {
		return "soft"
	}
	if strings.Contains(lower, "warm") || strings.Contains(lower, "friendly") {
		return "warm"
	}
	if strings.Contains(lower, "calm") || strings.Contains(lower, "composed") || strings.Contains(lower, "steady") {
		return "calm"
	}
	if strings.Contains(lower, "energetic") || strings.Contains(lower, "bright") || strings.Contains(lower, "upbeat") {
		return "energetic"
	}
	return "neutral"
}

func NewTTSAdapter(baseSpeed, basePitch, baseVolume float32) *TTSAdapter {
	if baseSpeed <= 0 {
		baseSpeed = 1.0
	}
	if basePitch <= 0 {
		basePitch = 1.0
	}
	if baseVolume <= 0 {
		baseVolume = 1.0
	}
	return &TTSAdapter{
		baseSpeed:  baseSpeed,
		basePitch:  basePitch,
		baseVolume: baseVolume,
		styleName:  "neutral",
	}
}

// SetVoiceStyle selects a voice style preset that modifies the base TTS parameters.
// The style name is matched case-insensitively against known presets.
func (ta *TTSAdapter) SetVoiceStyle(style string) {
	preset, ok := voiceStyles[strings.ToLower(style)]
	if !ok {
		log.Printf("[TTS] Unknown voice style %q, keeping neutral", style)
		return
	}
	ta.baseSpeed = preset.speed
	ta.basePitch = preset.pitch
	ta.baseVolume = preset.volume
	ta.styleName = style
	log.Printf("[TTS] Voice style set to %q (speed=%.2f, pitch=%.2f, volume=%.2f)",
		style, ta.baseSpeed, ta.basePitch, ta.baseVolume)
}

func (ta *TTSAdapter) GetStyleName() string {
	return ta.styleName
}

func (ta *TTSAdapter) AdaptToEmotion(emotion EmotionState) TTSParams {
	params := TTSParams{
		Speed:      ta.baseSpeed,
		Pitch:      ta.basePitch,
		Volume:     ta.baseVolume,
		Emphasis:   1.0,
		PauseScale: 1.0,
	}

	if emotion.Type == EmotionNeutral || emotion.Confidence < 0.3 {
		return params
	}

	switch emotion.Type {
	case EmotionStressed:
		params.Speed = ta.baseSpeed * 0.85
		params.Pitch = ta.basePitch * 0.95
		params.Volume = ta.baseVolume * 0.9
		params.Emphasis = 0.8
		params.PauseScale = 1.3
		log.Printf("[TTS] Stressed mode: slower, calmer, lower pitch")

	case EmotionFrustrated:
		params.Speed = ta.baseSpeed * 0.9
		params.Pitch = ta.basePitch * 0.98
		params.Volume = ta.baseVolume * 0.95
		params.Emphasis = 0.9
		params.PauseScale = 1.2
		log.Printf("[TTS] Frustrated mode: patient, steady")

	case EmotionSad:
		params.Speed = ta.baseSpeed * 0.8
		params.Pitch = ta.basePitch * 0.92
		params.Volume = ta.baseVolume * 0.85
		params.Emphasis = 0.7
		params.PauseScale = 1.5
		log.Printf("[TTS] Sad mode: gentle, slower, softer")

	case EmotionExcited:
		params.Speed = ta.baseSpeed * 1.15
		params.Pitch = ta.basePitch * 1.05
		params.Volume = ta.baseVolume * 1.1
		params.Emphasis = 1.3
		params.PauseScale = 0.8
		log.Printf("[TTS] Excited mode: energetic, faster, higher")

	case EmotionHappy:
		params.Speed = ta.baseSpeed * 1.05
		params.Pitch = ta.basePitch * 1.03
		params.Volume = ta.baseVolume * 1.05
		params.Emphasis = 1.1
		params.PauseScale = 0.9
		log.Printf("[TTS] Happy mode: warm, slightly upbeat")

	case EmotionCalm:
		params.Speed = ta.baseSpeed * 0.95
		params.Pitch = ta.basePitch * 0.98
		params.Volume = ta.baseVolume * 0.95
		params.Emphasis = 0.9
		params.PauseScale = 1.1
		log.Printf("[TTS] Calm mode: relaxed, measured")
	}

	params.Speed = float32(math.Max(0.5, math.Min(2.0, float64(params.Speed))))
	params.Pitch = float32(math.Max(0.5, math.Min(2.0, float64(params.Pitch))))
	params.Volume = float32(math.Max(0.3, math.Min(1.5, float64(params.Volume))))

	return params
}

func (ta *TTSAdapter) AdaptToContext(emotion EmotionState, textLength int, isQuestion bool) TTSParams {
	params := ta.AdaptToEmotion(emotion)

	if textLength > 200 {
		params.Speed *= 0.95
		params.PauseScale *= 1.1
	}

	if isQuestion {
		params.Pitch *= 1.02
		params.Emphasis *= 1.1
	}

	return params
}

func (ta *TTSAdapter) GetDefaultParams() TTSParams {
	return TTSParams{
		Speed:      ta.baseSpeed,
		Pitch:      ta.basePitch,
		Volume:     ta.baseVolume,
		Emphasis:   1.0,
		PauseScale: 1.0,
	}
}
