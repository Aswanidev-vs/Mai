package tts

import (
	"log"

	"github.com/user/mai/pkg/interfaces"
)

// Engine wraps the existing TTS generation logic
type Engine struct {
	bus      interfaces.EventBus
	generate func(text string) []float32 // Callback to the existing TTS generation
	play     func(samples []float32)     // Callback to the existing audio playback
}

func NewEngine(bus interfaces.EventBus, generate func(string) []float32, play func([]float32)) *Engine {
	return &Engine{
		bus:      bus,
		generate: generate,
		play:     play,
	}
}

func (e *Engine) Start() {
	e.bus.Subscribe("action.tts.request", e.handleTTSRequest)
}

func (e *Engine) handleTTSRequest(event interfaces.Event) {
	text, ok := event.Payload["text"].(string)
	if !ok {
		return
	}

	log.Printf("[TTS] Received request: %s", text)
	
	// Execute existing TTS logic via callbacks
	samples := e.generate(text)
	if samples != nil {
		e.play(samples)
	}
}
