package llm

import (
	"context"
	"encoding/json"
	"log"
	"strings"

	"github.com/user/mai/internal/agent"
	"github.com/user/mai/pkg/interfaces"
)

var _ interfaces.ChatStreamer = (*HybridProvider)(nil)

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

func (p *HybridProvider) Stream(ctx context.Context, prompt string, opts interfaces.GenerationOptions, callback func(chunk string)) error {
	if p.guard.IsSensitive(prompt) {
		return p.local.Stream(ctx, prompt, opts, callback)
	}
	return p.cloud.Stream(ctx, prompt, opts, callback)
}

// StreamChat implements interfaces.ChatStreamer, routing by the same privacy
// rule as Generate/Stream. It must exist on the wrapper, not just on the
// providers behind it: the orchestrator asserts the interface on the provider
// it was handed, so a hybrid agent silently lost the chat path and every turn
// fell back to the flat prompt.
func (p *HybridProvider) StreamChat(ctx context.Context, messages []interfaces.ChatMessage, opts interfaces.GenerationOptions, callback func(chunk string)) error {
	target := p.cloud
	if p.guard.IsSensitive(chatText(messages)) {
		log.Println("[HYBRID] Sensitive conversation detected. Routing chat to local model.")
		target = p.local
	} else {
		log.Println("[HYBRID] Non-sensitive conversation. Routing chat to cloud model.")
	}
	// The chosen side may be a provider without a chat endpoint (any of the
	// OpenAI-compatible clouds); answer through its flat stream rather than
	// failing the turn. ctx is forwarded unchanged in both paths — barge-in
	// reaches the in-flight HTTP request through it.
	if cs, ok := target.(interfaces.ChatStreamer); ok {
		return cs.StreamChat(ctx, messages, opts, callback)
	}
	return target.Stream(ctx, flattenChat(messages), opts, callback)
}

// chatText is the text actually on the wire for a chat turn. The privacy guard
// scans a single string, so every message is included: one sensitive turn
// anywhere in the history keeps the whole conversation local.
func chatText(messages []interfaces.ChatMessage) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

// flattenChat renders a message list as a transcript for providers that only
// expose the flat completion endpoint.
func flattenChat(messages []interfaces.ChatMessage) string {
	var b strings.Builder
	for _, m := range messages {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		switch m.Role {
		case "user", "assistant", "system":
			b.WriteString(strings.ToUpper(m.Role[:1]) + m.Role[1:] + ": ")
		default:
			b.WriteString(m.Role + ": ")
		}
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
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
