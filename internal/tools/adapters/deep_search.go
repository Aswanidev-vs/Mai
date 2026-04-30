package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

// DeepSearchTool actually fetches search results and returns text to the LLM
type DeepSearchTool struct {
	client *http.Client
}

func NewDeepSearchTool() *DeepSearchTool {
	return &DeepSearchTool{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (t *DeepSearchTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "deep_search",
		Description: "Actually searches the web AND returns the text results so you can summarize them for the user. Use this when the user asks a question about current events or general knowledge.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": { "type": "string", "description": "The search query" }
			},
			"required": ["query"]
		}`),
	}
}

func (t *DeepSearchTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Query string `json:"query"`
	}
	if len(params) == 0 || string(params) == "{}" || string(params) == "null" {
		return interfaces.ToolResult{Error: fmt.Errorf("deep_search requires a 'query' parameter. Please provide a query in the action_input.")}, nil
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: fmt.Errorf("failed to parse deep_search parameters: %v", err)}, nil
	}

	// We use a privacy-respecting search engine like DuckDuckGo or a scraper for demo purposes
	// For a professional JARVIS, we would use Google Search API or Serper.dev
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(args.Query))

	req, _ := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := t.client.Do(req)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	// Basic text extraction from HTML (very simplified)
	// In a real system, we would use a proper HTML parser
	content := string(body)
	if len(content) > 2000 {
		content = content[:2000] // Limit context for the LLM
	}

	return interfaces.ToolResult{
		Output: fmt.Sprintf("Search results for '%s' (truncated):\n%s", args.Query, content),
	}, nil
}
