package interfaces

import (
	"context"
	"encoding/json"
)

// GenerationOptions defines parameters for LLM text generation
type GenerationOptions struct {
	Temperature   float64
	TopP          float64
	MaxTokens     int
	StopSequences []string
}

// LLMProvider defines the interface for all LLM backends
type LLMProvider interface {
	Generate(ctx context.Context, prompt string, opts GenerationOptions) (string, error)
	Stream(ctx context.Context, prompt string, opts GenerationOptions, callback func(chunk string)) error
	GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error)
	Embed(ctx context.Context, text string) ([]float32, error)
	HealthCheck(ctx context.Context) error
}

// ChatMessage is one entry of a verbatim conversation thread.
type ChatMessage struct {
	Role    string `json:"role"` // "user" | "assistant"
	Content string `json:"content"`
}

// ChatStreamer is implemented by providers that support a multi-turn chat API
// (Ollama /api/chat). Optional: the orchestrator type-asserts and falls back
// to the flat prompt path when absent. Verbatim message history gives the
// model the actual dialogue, and lets the server reuse the cached KV prefix
// across turns — only the new tail is prefilled.
type ChatStreamer interface {
	StreamChat(ctx context.Context, messages []ChatMessage, opts GenerationOptions, callback func(chunk string)) error
}
