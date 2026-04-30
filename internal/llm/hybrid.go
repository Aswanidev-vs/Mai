package llm

import (
	"context"
	"encoding/json"
	"log"

	"github.com/user/mai/internal/agent"
	"github.com/user/mai/pkg/interfaces"
)

// HybridProvider wraps two providers and switches based on privacy rules
type HybridProvider struct {
	local  interfaces.LLMProvider
	cloud  interfaces.LLMProvider
	guard  *agent.PrivacyGuard
	active bool
}

func NewHybridProvider(local, cloud interfaces.LLMProvider, guard *agent.PrivacyGuard) *HybridProvider {
	return &HybridProvider{
		local:  local,
		cloud:  cloud,
		guard:  guard,
		active: true,
	}
}

func (p *HybridProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	if p.guard.IsSensitive(prompt) {
		log.Println("[HYBRID] Sensitive prompt detected. Routing to local model.")
		return p.local.Generate(ctx, prompt, opts)
	}
	log.Println("[HYBRID] Non-sensitive prompt. Routing to cloud model.")
	return p.cloud.Generate(ctx, prompt, opts)
}

func (p *HybridProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error {
	if p.guard.IsSensitive(prompt) {
		return p.local.Stream(ctx, prompt, callback)
	}
	return p.cloud.Stream(ctx, prompt, callback)
}

func (p *HybridProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	// For structured reasoning (agentic loop), we might prefer local even if not sensitive 
	// to avoid latency, but here we follow the sensitivity rule.
	if p.guard.IsSensitive(prompt) {
		return p.local.GenerateStructured(ctx, prompt, schema)
	}
	return p.cloud.GenerateStructured(ctx, prompt, schema)
}

func (p *HybridProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	// Embeddings usually stay local for semantic search
	return p.local.Embed(ctx, text)
}

func (p *HybridProvider) HealthCheck(ctx context.Context) error {
	return p.local.HealthCheck(ctx)
}
