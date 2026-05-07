package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"

	"github.com/user/mai/pkg/interfaces"
)

// ShellTool provides shell execution capabilities
type ShellTool struct{}

func (t *ShellTool) Metadata() interfaces.ToolMetadata {
	params := json.RawMessage(`{
		"type": "object",
		"properties": {
			"command": {
				"type": "string",
				"description": "The shell command to execute"
			},
			"args": {
				"type": "array",
				"items": { "type": "string" },
				"description": "Arguments for the command"
			}
		},
		"required": ["command"]
	}`)

	return interfaces.ToolMetadata{
		Name:        "shell_execute",
		Description: "Executes a shell command on the host system",
		Parameters:  params,
	}
}

func (t *ShellTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}

	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{}, err
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		fullArgs := append([]string{"/c", args.Command}, args.Args...)
		cmd = exec.CommandContext(ctx, "cmd", fullArgs...)
	} else {
		cmd = exec.CommandContext(ctx, args.Command, args.Args...)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return interfaces.ToolResult{
			Output: string(output),
			Error:  err,
		}, nil
	}

	return interfaces.ToolResult{
		Output: string(output),
	}, nil
}

// OpenAppFunc is the signature for the legacy OpenAppWithBrowser function.
type OpenAppFunc func(name, browser string) error

// OpenAppTool wraps the legacy Automation.OpenAppWithBrowser as a tool.
type OpenAppTool struct {
	Open OpenAppFunc
}

func (t *OpenAppTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "open_application",
		Description: "Opens an application by name. If the app is not installed, opens its web version in the browser. Optionally specify a browser.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"app_name": { "type": "string", "description": "Name of the app (e.g., 'spotify', 'notepad', 'instagram')" },
				"browser": { "type": "string", "description": "Optional browser for web fallback: 'chrome', 'brave', 'edge', 'firefox'" }
			},
			"required": ["app_name"]
		}`),
	}
}

func (t *OpenAppTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		AppName string `json:"app_name"`
		Browser string `json:"browser"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	if t.Open == nil {
		return interfaces.ToolResult{Error: fmt.Errorf("open app function not configured")}, nil
	}

	err := t.Open(args.AppName, args.Browser)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	if args.Browser != "" {
		return interfaces.ToolResult{Output: fmt.Sprintf("Opened %s in %s", args.AppName, args.Browser)}, nil
	}
	return interfaces.ToolResult{Output: fmt.Sprintf("Opened %s", args.AppName)}, nil
}
