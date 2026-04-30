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

type ClaudeProvider struct {
	model        string
	apiKey       string
	systemPrompt string
	client       *http.Client
}

func NewClaudeProvider(model, apiKey, systemPrompt string) *ClaudeProvider {
	return &ClaudeProvider{
		model:        model,
		apiKey:       apiKey,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 2 * time.Minute},
	}
}

func (p *ClaudeProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	url := "https://api.anthropic.com/v1/messages"

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":      p.model,
		"max_tokens": opts.MaxTokens,
		"system":     p.systemPrompt,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}
	
	body, _ := io.ReadAll(resp.Body)
	return "", fmt.Errorf("claude error: %s", string(body))
}

func (p *ClaudeProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error { return nil }
func (p *ClaudeProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) { return nil, nil }
func (p *ClaudeProvider) Embed(ctx context.Context, text string) ([]float32, error) { return nil, nil }
func (p *ClaudeProvider) HealthCheck(ctx context.Context) error { return nil }
