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

// OpenAppTool wraps the existing app launching logic
type OpenAppTool struct {
	// We might need to pass the existing Automation struct here or replicate logic
}

func (t *OpenAppTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "open_application",
		Description: "Opens a specific application by name",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"app_name": { "type": "string", "description": "Name of the app (e.g., 'chrome', 'notepad')" }
			},
			"required": ["app_name"]
		}`),
	}
}

func (t *OpenAppTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		AppName string `json:"app_name"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{}, err
	}

	// For now, use a simple shell command as an adapter
	// In the future, this should call the internal/tools/adapters/automation.go
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Use absolute path for cmd.exe and 'start' to handle protocols and apps reliably
		cmd = exec.CommandContext(ctx, `C:\Windows\System32\cmd.exe`, "/c", "start", "", args.AppName)
	} else {
		cmd = exec.CommandContext(ctx, "open", args.AppName)
	}

	err := cmd.Start()
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{Output: fmt.Sprintf("Successfully opened %s", args.AppName)}, nil
}
