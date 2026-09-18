package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

// OllamaOptions configures runtime behavior shared across all Ollama calls.
// Zero values fall back to Ollama's own defaults.
type OllamaOptions struct {
	// MinP is the minimum token probability (0-1) — keeps low-end/local model
	// sampling from wandering into irrelevant tokens. 0 disables.
	MinP float64

	// NumCtx pins the context window. Ollama's VRAM-based default is 4096 on
	// small GPUs, which is tight for a ~1.9k-token persona plus history.
	// buildChatMessages trims history to stay inside whatever value is pinned,
	// so the persona always survives. 0 disables pinning.
	NumCtx int
}

// OllamaProvider implements interfaces.LLMProvider using the Ollama API
type OllamaProvider struct {
	model        string
	url          string
	systemPrompt string
	think        *bool // nil = use model default, false = disable thinking
	minP         float64
	numCtx       int
	client       *http.Client
}

func NewOllamaProvider(model, url, systemPrompt string, think *bool, opts OllamaOptions) *OllamaProvider {
	return &OllamaProvider{
		model:        model,
		url:          url,
		systemPrompt: systemPrompt,
		think:        think,
		minP:         opts.MinP,
		numCtx:       opts.NumCtx,
		client:       &http.Client{Timeout: 5 * time.Minute},
	}
}

// ollamaKeepAlive pins the model in memory across turns. Ollama's default
// 5-min unload makes every idle gap pay a multi-second cold load, which is
// fatal for voice latency.
const ollamaKeepAlive = "10m"

// applyTokenOptions adds min_p and num_ctx to an options map (creating it if
// nil). num_keep is deliberately not set: Ollama's overflow handling always
// discards from the front of the prompt regardless of it, so the persona is
// protected by trimming history in buildChatMessages instead.
func (p *OllamaProvider) applyTokenOptions(options map[string]interface{}) map[string]interface{} {
	if p.minP > 0 || p.numCtx > 0 {
		if options == nil {
			options = make(map[string]interface{}, 1)
		}
	}
	if p.minP > 0 {
		options["min_p"] = p.minP
	}
	if p.numCtx > 0 {
		options["num_ctx"] = p.numCtx
	}
	return options
}

// chatTemplateOverheadTokens approximates what Ollama's chat template adds
// around the message list (role markers plus the generation prompt).
const chatTemplateOverheadTokens = 32

// estimateTokens approximates a string's token count. English prose runs about
// four characters per token — precise enough for a safety budget.
func estimateTokens(s string) int { return (len(s) + 3) / 4 }

// messageTokens approximates what a message list costs, including the
// per-message role overhead Ollama's template adds.
func messageTokens(msgs []interfaces.ChatMessage) int {
	total := 0
	for _, m := range msgs {
		total += estimateTokens(m.Content) + 4
	}
	return total
}

// buildChatMessages returns the message list actually sent to /api/chat: the
// persona leads as a system message, and history is trimmed to fit the pinned
// window.
//
// Two Ollama behaviors drive this shape:
//
//   - /api/chat has no top-level "system" field (that parameter only exists on
//     /api/generate). A persona sent there is silently ignored, so the model
//     answered as a generic assistant — "I'm just a text model" — with Mai's
//     whole personality sitting unused in config.yaml.
//   - When a prompt overflows num_ctx, Ollama discards messages from the front
//     of the conversation, so an over-long history erased the persona
//     mid-session. Trimming here keeps the persona and the newest turns — the
//     context a reply actually needs.
func (p *OllamaProvider) buildChatMessages(messages []interfaces.ChatMessage, opts interfaces.GenerationOptions) []interfaces.ChatMessage {
	msgs := make([]interfaces.ChatMessage, 0, len(messages)+1)
	if p.systemPrompt != "" {
		msgs = append(msgs, interfaces.ChatMessage{Role: "system", Content: p.systemPrompt})
	}
	msgs = append(msgs, messages...)

	if p.numCtx <= 0 || len(msgs) <= 1 {
		return msgs
	}

	// Leave room for the reply itself, on top of the template's own tokens.
	reserve := opts.MaxTokens
	if reserve <= 0 {
		reserve = 256
	}
	budget := p.numCtx - reserve - chatTemplateOverheadTokens
	if budget <= 0 {
		return msgs // window too small to hold a reply at all — send as-is
	}

	// The persona and the current turn are kept; only the history between them
	// is dropped, oldest pair first.
	head := 0
	for head < len(msgs) && msgs[head].Role == "system" {
		head++
	}
	personaTokens := messageTokens(msgs[:head])
	body := msgs[head:]

	dropped := 0
	for len(body) > 1 && personaTokens+messageTokens(body)+chatTemplateOverheadTokens > budget {
		drop := 2
		if len(body)-drop < 1 {
			drop = len(body) - 1
		}
		body = body[drop:]
		dropped += drop
	}
	if dropped > 0 {
		log.Printf("[OLLAMA] Dropped %d oldest message(s) to keep the prompt inside num_ctx=%d (persona and current turn kept)", dropped, p.numCtx)
	}

	out := make([]interfaces.ChatMessage, 0, head+len(body))
	out = append(out, msgs[:head]...)
	return append(out, body...)
}

// StreamChat implements interfaces.ChatStreamer via Ollama's /api/chat endpoint.
// The persona leads the message list as a system message (see
// buildChatMessages), and prior turns are sent verbatim — measured on a
// GTX 1650: a later turn prefills only the new tail tokens (~270 ms) because
// llama-server reuses the KV cache of the identical prefix. That is what keeps
// long conversations as fast as turn two.
func (p *OllamaProvider) StreamChat(ctx context.Context, messages []interfaces.ChatMessage, opts interfaces.GenerationOptions, callback func(chunk string)) error {
	// Graceful degradation: if the pinned num_ctx is larger than the model
	// supports (or the KV cache doesn't fit), Ollama fails the load with an
	// allocation error instead of clamping. As long as nothing has streamed
	// yet, retry once without the pin so Ollama auto-sizes the window and the
	// conversation still works — instead of erroring mid-session on a model
	// swap.
	streamed := false
	err := p.streamChatOnce(ctx, messages, opts, func(chunk string) {
		streamed = true
		callback(chunk)
	}, true)
	if err != nil && !streamed && ctx.Err() == nil && p.numCtx > 0 && isContextAllocError(err) {
		log.Printf("[OLLAMA] Context allocation failed with num_ctx=%d (%v); retrying with Ollama's auto-sized context", p.numCtx, err)
		return p.streamChatOnce(ctx, messages, opts, callback, false)
	}
	return err
}

func (p *OllamaProvider) streamChatOnce(ctx context.Context, messages []interfaces.ChatMessage, opts interfaces.GenerationOptions, callback func(chunk string), pinCtx bool) error {
	options := p.applyTokenOptions(map[string]interface{}{
		"temperature": opts.Temperature,
	})
	if opts.TopP > 0 {
		options["top_p"] = opts.TopP
	}
	if opts.MaxTokens > 0 {
		options["num_predict"] = opts.MaxTokens
	}
	if !pinCtx {
		delete(options, "num_ctx")
		delete(options, "num_keep")
	}

	chatURL := p.url
	if strings.HasSuffix(chatURL, "/api/generate") {
		chatURL = strings.TrimSuffix(chatURL, "/api/generate") + "/api/chat"
	}

	reqBody := map[string]interface{}{
		"model":      p.model,
		"messages":   p.buildChatMessages(messages, opts),
		"stream":     true,
		"keep_alive": ollamaKeepAlive,
		"options":    options,
	}
	if p.think != nil {
		reqBody["think"] = *p.think
	}
	requestBody, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", chatURL, bytes.NewBuffer(requestBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("ollama chat error status: %d %s", resp.StatusCode, body)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done bool `json:"done"`
		}
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		if chunk.Message.Content != "" {
			callback(chunk.Message.Content)
		}
		if chunk.Done {
			break
		}
	}

	return nil
}

// isContextAllocError reports whether err looks like Ollama failing to
// allocate the pinned context window (model max < num_ctx, or insufficient
// memory for the KV cache). Failure shapes observed empirically:
// "llama-server reported out-of-memory during startup" and
// "failed to allocate buffer of size ...".
func isContextAllocError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{"out of memory", "out-of-memory", "failed to allocate", "insufficient memory"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (p *OllamaProvider) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	options := p.applyTokenOptions(map[string]interface{}{
		"temperature": opts.Temperature,
	})
	if opts.TopP > 0 {
		options["top_p"] = opts.TopP
	}
	if opts.MaxTokens > 0 {
		options["num_predict"] = opts.MaxTokens
	}
	if len(opts.StopSequences) > 0 {
		options["stop"] = opts.StopSequences
	}
	reqBody := map[string]interface{}{
		"model":      p.model,
		"prompt":     prompt,
		"system":     p.systemPrompt,
		"stream":     false,
		"keep_alive": ollamaKeepAlive,
		"options":    options,
	}
	if p.think != nil {
		reqBody["think"] = *p.think
		log.Printf("[OLLAMA] think mode: %v", *p.think)
	}
	requestBody, err := json.Marshal(reqBody)
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", err
	}

	return result.Response, nil
}

func (p *OllamaProvider) Stream(ctx context.Context, prompt string, opts interfaces.GenerationOptions, callback func(chunk string)) error {
	options := p.applyTokenOptions(map[string]interface{}{
		"temperature": opts.Temperature,
	})
	if opts.TopP > 0 {
		options["top_p"] = opts.TopP
	}
	if opts.MaxTokens > 0 {
		options["num_predict"] = opts.MaxTokens
	}
	reqBody := map[string]interface{}{
		"model":      p.model,
		"prompt":     prompt,
		"system":     p.systemPrompt,
		"stream":     true,
		"keep_alive": ollamaKeepAlive,
		"options":    options,
	}
	if p.think != nil {
		reqBody["think"] = *p.think
	}
	requestBody, err := json.Marshal(reqBody)
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
	// Ollama supports format: "json" (or a full JSON schema object).
	// options carry temperature/num_predict so structured calls aren't
	// unbounded and don't run at the model's default temperature.
	options := p.applyTokenOptions(map[string]interface{}{})
	options["temperature"] = 0.2 // low temp keeps tool calls deterministic
	options["num_predict"] = 1500
	reqBody := map[string]interface{}{
		"model":      p.model,
		"prompt":     prompt,
		"system":     p.systemPrompt,
		"stream":     false,
		"format":     formatFromSchema(schema),
		"keep_alive": ollamaKeepAlive,
		"options":    options,
	}
	if p.think != nil {
		reqBody["think"] = *p.think
	}
	requestBody, err := json.Marshal(reqBody)
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

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}

	return json.RawMessage(result.Response), nil
}

// formatFromSchema returns the Ollama `format` value for a JSON schema.
// Ollama accepts either the literal "json" or a full JSON Schema object; we
// pass the schema through when available for deterministic structured output.
func formatFromSchema(schema json.RawMessage) interface{} {
	if len(schema) > 0 {
		var v interface{}
		if json.Unmarshal(schema, &v) == nil {
			return v
		}
	}
	return "json"
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

	body := map[string]interface{}{
		"model":  p.model,
		"prompt": text,
	}
	if opts := p.applyTokenOptions(nil); opts != nil {
		body["options"] = opts
	}
	requestBody, err := json.Marshal(body)
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
