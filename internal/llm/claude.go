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
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

func (p *ClaudeProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	maxTokens := opts.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	body := map[string]interface{}{
		"model":      p.model,
		"max_tokens": maxTokens,
		"system":     p.systemPrompt,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
		"stream": false,
	}
	if opts.Temperature > 0 {
		body["temperature"] = opts.Temperature
	}
	if len(opts.StopSequences) > 0 {
		body["stop_sequences"] = opts.StopSequences
	}

	requestBody, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(requestBody))
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("claude error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Content) > 0 {
		return result.Content[0].Text, nil
	}
	return "", fmt.Errorf("no response from claude")
}

func (p *ClaudeProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error {
	requestBody, _ := json.Marshal(map[string]interface{}{
		"model":      p.model,
		"max_tokens": 4096,
		"system":     p.systemPrompt,
		"messages": []map[string]interface{}{
			{"role": "user", "content": prompt},
		},
		"stream": true,
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewBuffer(requestBody))
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("claude stream error %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if event.Type == "content_block_delta" && event.Delta.Text != "" {
			callback(event.Delta.Text)
		}
	}

	return scanner.Err()
}

func (p *ClaudeProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	prompt = prompt + "\n\nYou MUST respond with valid JSON only. No markdown fences, no explanation."

	text, err := p.Generate(ctx, prompt, interfaces.GenerationOptions{Temperature: 0.1})
	if err != nil {
		return nil, err
	}

	// Strip markdown code fences if present
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		lines := strings.Split(text, "\n")
		var jsonLines []string
		inBlock := false
		for _, line := range lines {
			if strings.HasPrefix(line, "```") {
				inBlock = !inBlock
				continue
			}
			if inBlock {
				jsonLines = append(jsonLines, line)
			}
		}
		if len(jsonLines) > 0 {
			text = strings.Join(jsonLines, "\n")
		}
	}

	return json.RawMessage(text), nil
}

func (p *ClaudeProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	// Claude doesn't have a native embedding API.
	// Return an error so the system falls back to Ollama for embeddings.
	return nil, fmt.Errorf("claude does not support embeddings; use a local provider")
}

func (p *ClaudeProvider) HealthCheck(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.anthropic.com/v1/messages?limit=1", nil)
	p.setHeaders(req)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Anthropic returns 401 for invalid key, 405 for valid key on GET
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("invalid API key")
	}
	return nil
}

func (p *ClaudeProvider) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", p.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
}
