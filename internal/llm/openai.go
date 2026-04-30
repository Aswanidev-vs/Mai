package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

type OpenAIProvider struct {
	model        string
	url          string
	apiKey       string
	systemPrompt string
	client       *http.Client
}

func NewOpenAIProvider(model, url, apiKey, systemPrompt string) *OpenAIProvider {
	if url == "" {
		url = "https://api.openai.com/v1/chat/completions"
	}
	return &OpenAIProvider{
		model:        model,
		url:          url,
		apiKey:       apiKey,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 2 * time.Minute},
	}
}

func (p *OpenAIProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	messages := []map[string]string{
		{"role": "system", "content": p.systemPrompt},
		{"role": "user", "content": prompt},
	}

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":       p.model,
		"messages":    messages,
		"temperature": opts.Temperature,
		"max_tokens":  opts.MaxTokens,
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai error status: %d, body: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}
	return "", fmt.Errorf("no response from openai")
}

// Stream, GenerateStructured, Embed, HealthCheck would be implemented similarly
func (p *OpenAIProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error { return nil }
func (p *OpenAIProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	// OpenAI supports response_format: { "type": "json_object" }
	return nil, nil
}
func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) { return nil, nil }
func (p *OpenAIProvider) HealthCheck(ctx context.Context) error { return nil }
