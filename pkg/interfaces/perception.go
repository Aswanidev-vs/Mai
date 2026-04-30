package interfaces

import (
	"context"
)

// PerceptionEvent represents a sensory input event
type PerceptionEvent struct {
	Type     string                 `json:"type"` // "audio", "vision", "sensor"
	Source   string                 `json:"source"`
	Payload  map[string]interface{} `json:"payload"`
}

// PerceptionProvider defines the interface for all sensory inputs
type PerceptionProvider interface {
	Start(ctx context.Context) error
	Stop() error
	Subscribe() <-chan PerceptionEvent
}

// Sensor defines a specific environmental sensing device
type Sensor interface {
	ID() string
	Type() string
	Read(ctx context.Context) (interface{}, error)
}
