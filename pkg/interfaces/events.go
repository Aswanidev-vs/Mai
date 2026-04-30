package interfaces

import (
	"time"
)

// EventPriority defines the urgency of an event
type EventPriority int

const (
	PriorityLow EventPriority = iota
	PriorityNormal
	PriorityHigh
	PriorityCritical
)

// Event represents a single message on the event bus
type Event struct {
	Type      string                 `json:"type"`
	Source    string                 `json:"source"`      // Component ID
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload"`
	Priority  EventPriority          `json:"priority"`    // Low, Normal, High, Critical
	SessionID string                 `json:"session_id"`  // For tracing
}

// EventHandler is a function that processes an event
type EventHandler func(Event)

// Subscription represents a listener on the event bus
type Subscription interface {
	Unsubscribe()
}

// EventBus defines the decoupled communication layer
type EventBus interface {
	Publish(event Event) error
	Subscribe(eventType string, handler EventHandler) Subscription
	SubscribeAsync(eventType string, handler EventHandler) Subscription
	RequestResponse(request Event, timeout time.Duration) (Event, error)
}
