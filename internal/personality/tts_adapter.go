package personality

import (
	"log"
	"math"
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
	}
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
