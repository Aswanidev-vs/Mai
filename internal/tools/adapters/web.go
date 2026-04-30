package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"os/exec"

	"github.com/user/mai/pkg/interfaces"
)

// WebSearchTool opens a browser to search for something
type WebSearchTool struct{}

func (t *WebSearchTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "web_search",
		Description: "Searches the web for a query by opening the default browser",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": { "type": "string", "description": "The search query" }
			},
			"required": ["query"]
		}`),
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{}, err
	}

	searchURL := fmt.Sprintf("https://www.google.com/search?q=%s", url.QueryEscape(args.Query))
	
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, `C:\Windows\System32\cmd.exe`, "/c", "start", searchURL)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", searchURL)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", searchURL)
	}

	err := cmd.Run()
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{Output: fmt.Sprintf("Opened browser to search for: %s", args.Query)}, nil
}
