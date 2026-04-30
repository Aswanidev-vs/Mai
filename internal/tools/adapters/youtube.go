package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

type YouTubeTool struct{}

func (t *YouTubeTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "youtube_play",
		Description: "Directly plays a specific song or video on YouTube. You can specify a browser like 'brave', 'chrome', or 'edge' if the user requests it.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": { "type": "string", "description": "The song or video title" },
				"browser": { "type": "string", "description": "Optional: 'brave', 'chrome', 'edge', or 'firefox'" }
			},
			"required": ["query"]
		}`),
	}
}

func (t *YouTubeTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Query   string `json:"query"`
		Browser string `json:"browser"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	searchURL := fmt.Sprintf("https://www.youtube.com/results?search_query=%s&autoplay=1", url.QueryEscape(args.Query))
	
	// Browser path map
	browsers := map[string][]string{
		"brave": {
			`C:\Program Files\BraveSoftware\Brave-Browser\Application\brave.exe`,
			`C:\Program Files (x86)\BraveSoftware\Brave-Browser\Application\brave.exe`,
			os.Getenv("LocalAppData") + `\BraveSoftware\Brave-Browser\Application\brave.exe`,
		},
		"chrome": {
			`C:\Program Files\Google\Chrome\Application\chrome.exe`,
			`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
		},
		"edge": {
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		},
		"firefox": {
			`C:\Program Files\Mozilla Firefox\firefox.exe`,
		},
	}

	var cmd *exec.Cmd
	foundBrowser := false

	// If a specific browser was requested, try to find it
	targetBrowser := strings.ToLower(args.Browser)
	if targetBrowser == "google" {
		targetBrowser = "chrome"
	}

	if targetBrowser != "" {
		if paths, ok := browsers[targetBrowser]; ok {
			for _, path := range paths {
				if _, err := os.Stat(path); err == nil {
					cmd = exec.CommandContext(ctx, path, searchURL)
					foundBrowser = true
					break
				}
			}
		}
	}

	// Default to system "start" if no browser requested or found
	if !foundBrowser {
		if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(ctx, `C:\Windows\System32\cmd.exe`, "/c", "start", searchURL)
		} else {
			cmd = exec.CommandContext(ctx, "open", searchURL)
		}
	}

	if err := cmd.Start(); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	msg := fmt.Sprintf("Playing '%s' on YouTube.", args.Query)
	if foundBrowser {
		msg = fmt.Sprintf("Playing '%s' on YouTube via %s.", args.Query, args.Browser)
	}

	return interfaces.ToolResult{Output: msg}, nil
}
