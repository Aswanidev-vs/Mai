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

// OllamaProvider implements interfaces.LLMProvider using the Ollama API
type OllamaProvider struct {
	model        string
	url          string
	systemPrompt string
	client       *http.Client
}

func NewOllamaProvider(model, url, systemPrompt string) *OllamaProvider {
	return &OllamaProvider{
		model:        model,
		url:          url,
		systemPrompt: systemPrompt,
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

func (p *OllamaProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	requestBody, err := json.Marshal(map[string]interface{}{
		"model":  p.model,
		"prompt": prompt,
		"system": p.systemPrompt,
		"stream": false,
		"options": map[string]interface{}{
			"temperature": opts.Temperature,
			"stop":        opts.StopSequences,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(requestBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama error status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	return result.Response, nil
}

func (p *OllamaProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error {
	requestBody, err := json.Marshal(map[string]interface{}{
		"model":  p.model,
		"prompt": prompt,
		"system": p.systemPrompt,
		"stream": true,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Response string `json:"response"`
			Done     bool   `json:"done"`
		}
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		callback(chunk.Response)
		if chunk.Done {
			break
		}
	}

	return nil
}

func (p *OllamaProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	// Ollama support for format: "json"
	requestBody, err := json.Marshal(map[string]interface{}{
		"model":  p.model,
		"prompt": prompt,
		"system": p.systemPrompt,
		"stream": false,
		"format": "json",
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	return json.RawMessage(result.Response), nil
}

func (p *OllamaProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	// Ollama embeddings endpoint is usually /api/embeddings
	// We might need a different URL for this if url is /api/generate
	// For now, let's assume url is the base URL or we infer it.
	// Typically: http://localhost:11434/api/generate -> http://localhost:11434/api/embeddings
	baseURL := p.url
	if len(baseURL) > 9 && baseURL[len(baseURL)-9:] == "/generate" {
		baseURL = baseURL[:len(baseURL)-9] + "/embeddings"
	}

	requestBody, err := json.Marshal(map[string]interface{}{
		"model":  p.model,
		"prompt": text,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", baseURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Embedding []float32 `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return result.Embedding, nil
}

func (p *OllamaProvider) HealthCheck(ctx context.Context) error {
	// Simple check using /api/tags or similar
	baseURL := p.url
	if len(baseURL) > 9 && baseURL[len(baseURL)-9:] == "/generate" {
		baseURL = baseURL[:len(baseURL)-9] + "/tags"
	}
	
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL, nil)
	if err != nil {
		return err
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama health check failed: %d", resp.StatusCode)
	}
	return nil
}
