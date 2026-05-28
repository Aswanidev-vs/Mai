package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

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
		Category: interfaces.ToolCategoryWeb,
		Keywords: []string{"search", "web", "find", "lookup", "research", "query"},
	}
}

func (t *DeepSearchTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Query string `json:"query"`
	}
	if len(params) == 0 || string(params) == "{}" || string(params) == "null" {
		return interfaces.ToolResult{Error: fmt.Errorf("deep_search requires a 'query' parameter")}, nil
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: fmt.Errorf("failed to parse deep_search parameters: %v", err)}, nil
	}

	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(args.Query))

	req, _ := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")

	resp, err := t.client.Do(req)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	results := t.extractResults(content)

	if len(results) == 0 {
		return interfaces.ToolResult{Output: fmt.Sprintf("No results found for '%s'.", args.Query)}, nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search results for '%s':\n\n", args.Query))

	limit := 5
	if len(results) < limit {
		limit = len(results)
	}
	for i := 0; i < limit; i++ {
		r := results[i]
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, r.Title))
		if r.Snippet != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", r.Snippet))
		}
		if r.URL != "" {
			sb.WriteString(fmt.Sprintf("   URL: %s\n", r.URL))
		}
		sb.WriteString("\n")
	}

	output := sb.String()
	if len(output) > 3000 {
		output = output[:3000]
	}

	return interfaces.ToolResult{Output: output}, nil
}

type searchResult struct {
	Title   string
	Snippet string
	URL     string
}

var (
	resultBlockRe = regexp.MustCompile(`<div[^>]*class="[^"]*result[^"]*"[^>]*>([\s\S]*?)</div>\s*</div>`)
	resultLinkRe  = regexp.MustCompile(`<a[^>]*class="result__a"[^>]*href="([^"]*)"[^>]*>([\s\S]*?)</a>`)
	snippetRe     = regexp.MustCompile(`<a[^>]*class="result__snippet"[^>]*>([\s\S]*?)</a>`)
	tagRe         = regexp.MustCompile(`<[^>]+>`)
	spaceRe       = regexp.MustCompile(`\s+`)
)

func (t *DeepSearchTool) extractResults(html string) []searchResult {
	var results []searchResult

	blocks := resultBlockRe.FindAllStringSubmatch(html, -1)
	for _, block := range blocks {
		blockHTML := block[0]

		linkMatch := resultLinkRe.FindStringSubmatch(blockHTML)
		if linkMatch == nil {
			continue
		}

		rawURL := linkMatch[1]
		title := t.stripTags(linkMatch[2])

		snippet := ""
		snippetMatch := snippetRe.FindStringSubmatch(blockHTML)
		if snippetMatch != nil {
			snippet = t.stripTags(snippetMatch[1])
		}

		if decoded, err := url.QueryUnescape(rawURL); err == nil {
			rawURL = decoded
		}

		if strings.HasPrefix(rawURL, "//duckduckgo.com/l/") {
			if after, ok := strings.CutPrefix(rawURL, "//duckduckgo.com/l/?uddg="); ok {
				if decoded, err := url.QueryUnescape(after); err == nil {
					rawURL = decoded
				}
			}
		}

		title = strings.TrimSpace(title)
		snippet = strings.TrimSpace(snippet)

		if title != "" {
			results = append(results, searchResult{
				Title:   title,
				Snippet: snippet,
				URL:     rawURL,
			})
		}
	}

	if len(results) == 0 {
		liRe := regexp.MustCompile(`<li[^>]*class="[^"]*result[^"]*"[^>]*>([\s\S]*?)</li>`)
		blocks := liRe.FindAllStringSubmatch(html, -1)
		for _, block := range blocks {
			blockHTML := block[0]
			linkMatch := resultLinkRe.FindStringSubmatch(blockHTML)
			if linkMatch == nil {
				continue
			}
			rawURL := linkMatch[1]
			title := t.stripTags(linkMatch[2])

			snippet := ""
			snippetMatch := snippetRe.FindStringSubmatch(blockHTML)
			if snippetMatch != nil {
				snippet = t.stripTags(snippetMatch[1])
			}

			if decoded, err := url.QueryUnescape(rawURL); err == nil {
				rawURL = decoded
			}
			if strings.HasPrefix(rawURL, "//duckduckgo.com/l/") {
				if after, ok := strings.CutPrefix(rawURL, "//duckduckgo.com/l/?uddg="); ok {
					if decoded, err := url.QueryUnescape(after); err == nil {
						rawURL = decoded
					}
				}
			}

			title = strings.TrimSpace(title)
			snippet = strings.TrimSpace(snippet)
			if title != "" {
				results = append(results, searchResult{
					Title:   title,
					Snippet: snippet,
					URL:     rawURL,
				})
			}
		}
	}

	return results
}

func (t *DeepSearchTool) stripTags(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&quot;", "\"")
	s = strings.ReplaceAll(s, "&#x27;", "'")
	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = spaceRe.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
