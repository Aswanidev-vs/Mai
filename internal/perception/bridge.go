package perception

import (
	"github.com/user/mai/pkg/interfaces"
)

// Bridge allows the existing legacy pipeline to publish events to the new architecture
type Bridge struct {
	bus interfaces.EventBus
}

func NewBridge(bus interfaces.EventBus) *Bridge {
	return &Bridge{bus: bus}
}

func (b *Bridge) PublishTranscription(text string) {
	b.bus.Publish(interfaces.Event{
		Type:   "perception.audio.transcription",
		Source: "legacy.pipeline",
		Payload: map[string]interface{}{
			"text": text,
		},
	})
}

func (b *Bridge) PublishScene(description string) {
	b.bus.Publish(interfaces.Event{
		Type:   "perception.vision.scene",
		Source: "legacy.vision",
		Payload: map[string]interface{}{
			"description": description,
		},
	})
}
