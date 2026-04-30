package interfaces

import (
	"context"
	"encoding/json"
)

// GenerationOptions defines parameters for LLM text generation
type GenerationOptions struct {
	Temperature float64
	MaxTokens   int
	StopSequences []string
}

// LLMProvider defines the interface for all LLM backends
type LLMProvider interface {
	Generate(ctx context.Context, prompt string, opts GenerationOptions) (string, error)
	Stream(ctx context.Context, prompt string, callback func(chunk string)) error
	GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error)
	Embed(ctx context.Context, text string) ([]float32, error)
	HealthCheck(ctx context.Context) error
}
