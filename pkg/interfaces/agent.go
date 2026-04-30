package interfaces

import (
	"context"
)

// AgentStatus represents the current state of the agent
type AgentStatus string

const (
	StatusIdle      AgentStatus = "idle"
	StatusThinking  AgentStatus = "thinking"
	StatusActing    AgentStatus = "acting"
	StatusListening AgentStatus = "listening"
	StatusSpeaking  AgentStatus = "speaking"
)

// Goal represents a high-level objective for the agent
type Goal struct {
	ID          string   `json:"id"`
	Description string   `json:"description"`
	Priority    int      `json:"priority"`
	Status      string   `json:"status"` // "pending", "active", "completed", "failed"
	SubTasks    []string `json:"sub_tasks"`
}

// AgentResponse is the result of an input processing
type AgentResponse struct {
	Text     string `json:"text"`
	Success  bool   `json:"success"`
}

// Agent defines the main orchestrator interface
type Agent interface {
	Start(ctx context.Context) error
	Stop() error
	HandleInput(ctx context.Context, input map[string]interface{}) (*AgentResponse, error)
	SetGoal(ctx context.Context, goal Goal) error
	GetStatus() AgentStatus
}

// CognitionEngine defines reasoning and planning capabilities
type CognitionEngine interface {
	Reason(ctx context.Context, goal string, context string) (string, error)
	Plan(ctx context.Context, goal string) ([]string, error)
}
