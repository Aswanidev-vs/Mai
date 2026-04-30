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

type GeminiProvider struct {
	model        string
	apiKey       string
	systemPrompt string
	client       *http.Client
}

func NewGeminiProvider(model, apiKey, systemPrompt string) *GeminiProvider {
	return &GeminiProvider{
		model:        model,
		apiKey:       apiKey,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 2 * time.Minute},
	}
}

func (p *GeminiProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", p.model, p.apiKey)

	requestBody, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": p.systemPrompt + "\n\n" + prompt},
				},
			},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}
	
	body, _ := io.ReadAll(resp.Body)
	return "", fmt.Errorf("gemini error: %s", string(body))
}

func (p *GeminiProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error { return nil }
func (p *GeminiProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) { return nil, nil }
func (p *GeminiProvider) Embed(ctx context.Context, text string) ([]float32, error) { return nil, nil }
func (p *GeminiProvider) HealthCheck(ctx context.Context) error { return nil }
