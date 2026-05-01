package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
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
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

func (p *OpenAIProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	messages := []map[string]string{
		{"role": "system", "content": p.systemPrompt},
		{"role": "user", "content": prompt},
	}

	body := map[string]interface{}{
		"model":       p.model,
		"messages":    messages,
		"temperature": opts.Temperature,
		"stream":      false,
	}
	if opts.MaxTokens > 0 {
		body["max_tokens"] = opts.MaxTokens
	}
	if len(opts.StopSequences) > 0 {
		body["stop"] = opts.StopSequences
	}

	requestBody, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(requestBody))
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("openai error %d: %s", resp.StatusCode, string(respBody))
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

func (p *OpenAIProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error {
	messages := []map[string]string{
		{"role": "system", "content": p.systemPrompt},
		{"role": "user", "content": prompt},
	}

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":    p.model,
		"messages": messages,
		"stream":   true,
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(requestBody))
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai stream error %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			callback(chunk.Choices[0].Delta.Content)
		}
	}

	return scanner.Err()
}

func (p *OpenAIProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	messages := []map[string]string{
		{"role": "system", "content": p.systemPrompt + "\n\nYou MUST respond with valid JSON only. No markdown, no explanation."},
		{"role": "user", "content": prompt},
	}

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":       p.model,
		"messages":    messages,
		"temperature": 0.1,
		"stream":      false,
		"response_format": map[string]string{"type": "json_object"},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", p.url, bytes.NewBuffer(requestBody))
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai structured error %d: %s", resp.StatusCode, string(body))
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
		return json.RawMessage(result.Choices[0].Message.Content), nil
	}
	return nil, fmt.Errorf("no response from openai")
}

func (p *OpenAIProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	embedURL := p.url
	if strings.Contains(embedURL, "/chat/completions") {
		embedURL = strings.Replace(embedURL, "/chat/completions", "/embeddings", 1)
	} else {
		embedURL = strings.TrimRight(embedURL, "/") + "/embeddings"
	}

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model": p.model,
		"input": text,
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", embedURL, bytes.NewBuffer(requestBody))
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openai embed error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Data) > 0 {
		return result.Data[0].Embedding, nil
	}
	return nil, fmt.Errorf("no embedding returned")
}

func (p *OpenAIProvider) HealthCheck(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", strings.Replace(p.url, "/chat/completions", "/models", 1), nil)
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health check failed: %d", resp.StatusCode)
	}
	return nil
}

func (p *OpenAIProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}
