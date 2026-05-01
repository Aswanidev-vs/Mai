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
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

func (p *GeminiProvider) baseURL() string {
	return fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s", p.model)
}

func (p *GeminiProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	url := p.baseURL() + ":generateContent?key=" + p.apiKey

	body := map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": p.systemPrompt + "\n\n" + prompt},
				},
			},
		},
	}
	if opts.Temperature > 0 {
		body["generationConfig"] = map[string]interface{}{
			"temperature": opts.Temperature,
		}
	}

	requestBody, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("gemini error %d: %s", resp.StatusCode, string(respBody))
	}

	return p.parseGenerateResponse(resp.Body)
}

func (p *GeminiProvider) Stream(ctx context.Context, prompt string, callback func(chunk string)) error {
	url := p.baseURL() + ":streamGenerateContent?alt=sse&key=" + p.apiKey

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
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gemini stream error %d: %s", resp.StatusCode, string(body))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var chunk struct {
			Candidates []struct {
				Content struct {
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"content"`
			} `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
			callback(chunk.Candidates[0].Content.Parts[0].Text)
		}
	}

	return scanner.Err()
}

func (p *GeminiProvider) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	url := p.baseURL() + ":generateContent?key=" + p.apiKey

	requestBody, _ := json.Marshal(map[string]interface{}{
		"contents": []map[string]interface{}{
			{
				"parts": []map[string]interface{}{
					{"text": p.systemPrompt + "\n\n" + prompt + "\n\nRespond with valid JSON only."},
				},
			},
		},
		"generationConfig": map[string]interface{}{
			"responseMimeType": "application/json",
			"temperature":      0.1,
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini structured error %d: %s", resp.StatusCode, string(body))
	}

	text, err := p.parseGenerateResponse(resp.Body)
	if err != nil {
		return nil, err
	}

	return json.RawMessage(text), nil
}

func (p *GeminiProvider) Embed(ctx context.Context, text string) ([]float32, error) {
	model := "text-embedding-004"
	if strings.Contains(p.model, "embedding") {
		model = p.model
	}
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:embedContent?key=%s", model, p.apiKey)

	requestBody, _ := json.Marshal(map[string]interface{}{
		"model": model,
		"content": map[string]interface{}{
			"parts": []map[string]interface{}{{"text": text}},
		},
	})

	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("gemini embed error %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Embedding struct {
			Values []float32 `json:"values"`
		} `json:"embedding"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Embedding.Values) > 0 {
		return result.Embedding.Values, nil
	}
	return nil, fmt.Errorf("no embedding returned from gemini")
}

func (p *GeminiProvider) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", p.apiKey)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gemini health check failed: %d", resp.StatusCode)
	}
	return nil
}

func (p *GeminiProvider) parseGenerateResponse(body io.Reader) (string, error) {
	var result struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(body).Decode(&result); err != nil {
		return "", err
	}

	if len(result.Candidates) > 0 && len(result.Candidates[0].Content.Parts) > 0 {
		return result.Candidates[0].Content.Parts[0].Text, nil
	}
	return "", fmt.Errorf("no response from gemini")
}
