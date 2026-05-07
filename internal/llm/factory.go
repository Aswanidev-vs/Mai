package llm

import (
	"context"
	"fmt"
	"log"

	"github.com/user/mai/internal/agent"
	"github.com/user/mai/pkg/interfaces"
	"github.com/user/mai/pkg/models"
)

type Factory struct {
	config models.Config
}

func NewFactory(cfg models.Config) *Factory {
	return &Factory{config: cfg}
}

// TestProvider sends a simple prompt to verify the provider works.
func (f *Factory) TestProvider(providerType string) error {
	provider, err := f.CreateProvider(providerType)
	if err != nil {
		return fmt.Errorf("create provider: %w", err)
	}

	ctx := context.Background()
	if err := provider.HealthCheck(ctx); err != nil {
		log.Printf("[LLM] Health check failed for %s: %v", providerType, err)
	}

	resp, err := provider.Generate(ctx, "Respond with exactly: OK", interfaces.GenerationOptions{
		Temperature: 0,
		MaxTokens:   10,
	})
	if err != nil {
		return fmt.Errorf("generate failed: %w", err)
	}

	log.Printf("[LLM] Test response from %s: %q", providerType, resp)
	return nil
}

// TestCloudProvider tests the cloud provider specifically.
func (f *Factory) TestCloudProvider() error {
	provider := f.config.LLM.Cloud.Provider
	if provider == "" {
		provider = f.config.LLM.Provider
	}
	model := f.config.LLM.Cloud.Model
	if model == "" {
		model = f.config.LLM.Model
	}
	url := f.config.LLM.Cloud.URL
	apiKey := f.config.LLM.Cloud.APIKey

	keyDisplay := "(empty)"
	if apiKey != "" {
		if len(apiKey) > 8 {
			keyDisplay = apiKey[:4] + "..." + apiKey[len(apiKey)-4:]
		} else {
			keyDisplay = "(set, " + fmt.Sprintf("%d", len(apiKey)) + " chars)"
		}
	}
	log.Printf("[TEST] Provider: %s, Model: %s, URL: %s, APIKey: %s", provider, model, url, keyDisplay)

	cloud, err := f.createCloudProvider(provider, model, url, apiKey)
	if err != nil {
		return fmt.Errorf("create cloud provider: %w", err)
	}

	ctx := context.Background()
	resp, err := cloud.Generate(ctx, "Respond with exactly: OK", interfaces.GenerationOptions{
		Temperature: 0,
		MaxTokens:   10,
	})
	if err != nil {
		return fmt.Errorf("generate failed: %w", err)
	}

	log.Printf("[TEST] Cloud response: %q", resp)
	return nil
}

func (f *Factory) CreateHybridProvider() (interfaces.LLMProvider, error) {
	// 1. Create the local provider (always Ollama)
	localModel := f.config.LLM.LocalModel
	if localModel == "" {
		localModel = f.config.LLM.Model
	}
	localURL := "http://localhost:11434/api/generate"
	local := NewOllamaProvider(localModel, localURL, f.config.LLM.SystemPrompt)

	// 2. If hybrid mode is off, just return local
	if !f.config.LLM.HybridMode {
		return local, nil
	}

	// 3. Create the cloud provider
	cloudProvider := f.config.LLM.Cloud.Provider
	if cloudProvider == "" {
		cloudProvider = f.config.LLM.Provider
	}

	cloudModel := f.config.LLM.Cloud.Model
	if cloudModel == "" {
		cloudModel = f.config.LLM.Model
	}

	cloudURL := f.config.LLM.Cloud.URL
	cloudKey := f.config.LLM.Cloud.APIKey

	cloud, err := f.createCloudProvider(cloudProvider, cloudModel, cloudURL, cloudKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud provider: %w", err)
	}

	// 4. Wrap in HybridProvider with PrivacyGuard
	guard := agent.NewPrivacyGuard(f.config.Privacy)
	return NewHybridProvider(local, cloud, guard), nil
}

func (f *Factory) createCloudProvider(provider, model, url, apiKey string) (interfaces.LLMProvider, error) {
	switch provider {
	case "ollama":
		if url == "" {
			url = "http://localhost:11434/api/generate"
		}
		return NewOllamaProvider(model, url, f.config.LLM.SystemPrompt), nil
	case "openai":
		if url == "" {
			url = "https://api.openai.com/v1/chat/completions"
		}
		return NewOpenAIProvider(model, url, apiKey, f.config.LLM.SystemPrompt), nil
	case "nvidia":
		if url == "" {
			url = "https://integrate.api.nvidia.com/v1/chat/completions"
		}
		return NewOpenAIProvider(model, url, apiKey, f.config.LLM.SystemPrompt), nil
	case "openrouter":
		if url == "" {
			url = "https://openrouter.ai/api/v1/chat/completions"
		}
		return NewOpenAIProvider(model, url, apiKey, f.config.LLM.SystemPrompt), nil
	case "llamacpp":
		if url == "" {
			url = "http://localhost:8080/v1/chat/completions"
		}
		return NewOpenAIProvider(model, url, apiKey, f.config.LLM.SystemPrompt), nil
	case "gemini":
		return NewGeminiProvider(model, apiKey, f.config.LLM.SystemPrompt), nil
	case "claude":
		return NewClaudeProvider(model, apiKey, f.config.LLM.SystemPrompt), nil
	default:
		return nil, fmt.Errorf("unsupported cloud provider: %s", provider)
	}
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
