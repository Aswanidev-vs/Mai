package llm

import (
	"fmt"

	"github.com/user/mai/internal/agent"
	"github.com/user/mai/pkg/interfaces"
	"github.com/user/mai/pkg/models"
)

// Factory creates the appropriate LLM provider based on config
type Factory struct {
	config models.Config
}

func NewFactory(cfg models.Config) *Factory {
	return &Factory{config: cfg}
}

func (f *Factory) CreateHybridProvider() (interfaces.LLMProvider, error) {
	// 1. Create the local provider (default fallback)
	local, err := f.CreateProvider("ollama") // Use Ollama as default local
	if err != nil {
		return nil, fmt.Errorf("failed to create local provider: %w", err)
	}

	// 2. If Hybrid mode is off, just return the local provider
	if !f.config.LLM.HybridMode {
		return local, nil
	}

	// 3. Create the cloud provider
	cloud, err := f.CreateProvider(f.config.LLM.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud provider: %w", err)
	}

	// 4. Wrap them in the HybridProvider with the PrivacyGuard
	guard := agent.NewPrivacyGuard(f.config.Privacy)
	return NewHybridProvider(local, cloud, guard), nil
}

func (f *Factory) CreateProvider(providerType string) (interfaces.LLMProvider, error) {
	switch providerType {
	case "ollama":
		return NewOllamaProvider(f.config.LLM.Model, f.config.LLM.URL, f.config.LLM.SystemPrompt), nil
	case "openai", "nvidia", "openrouter", "llamacpp":
		return NewOpenAIProvider(f.config.LLM.Model, f.config.LLM.URL, f.config.LLM.APIKey, f.config.LLM.SystemPrompt), nil
	case "gemini":
		return NewGeminiProvider(f.config.LLM.Model, f.config.LLM.APIKey, f.config.LLM.SystemPrompt), nil
	case "claude":
		return NewClaudeProvider(f.config.LLM.Model, f.config.LLM.APIKey, f.config.LLM.SystemPrompt), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerType)
	}
}
