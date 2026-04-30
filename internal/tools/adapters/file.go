package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/user/mai/pkg/interfaces"
)

// FileWriteTool allows Mai to create and write to local files
type FileWriteTool struct{}

func (t *FileWriteTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "file_write",
		Description: "Creates or updates a local file with the specified content. Use this to write notes, save lists, or create scripts.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": { "type": "string", "description": "The absolute or relative path to the file (e.g., 'note.txt' or 'C:/Users/Name/Desktop/todo.txt')" },
				"content": { "type": "string", "description": "The text content to write into the file" },
				"append": { "type": "boolean", "description": "If true, appends to the file instead of overwriting" }
			},
			"required": ["path", "content"]
		}`),
	}
}

func (t *FileWriteTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
		Append  bool   `json:"append"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	// Expand home directory if needed
	if args.Path[:2] == "~/" {
		home, _ := os.UserHomeDir()
		args.Path = filepath.Join(home, args.Path[2:])
	}

	flags := os.O_CREATE | os.O_WRONLY
	if args.Append {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(args.Path, flags, 0644)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}
	defer f.Close()

	if _, err := f.WriteString(args.Content + "\n"); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{
		Output: fmt.Sprintf("Successfully wrote to file: %s", args.Path),
	}, nil
}
