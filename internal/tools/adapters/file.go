package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

// FileWriteTool creates or updates files inside an allowed root. Model output
// is untrusted (web_research feeds page text into the prompt), so writes must
// not reach arbitrary paths on the host.
type FileWriteTool struct {
	AllowedRoots []string
	// Confirm, when set, must approve a write before it happens.
	Confirm func(path, content string) bool
}

func (t *FileWriteTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name: "file_write",
		Description: "Creates or updates a local file inside the workspace or home directory. " +
			"Use this to write notes, save lists, or create scripts.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"path": { "type": "string", "description": "Relative path, or a path under the workspace or home directory" },
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
	if strings.HasPrefix(args.Path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return interfaces.ToolResult{Error: err}, nil
		}
		args.Path = filepath.Join(home, args.Path[2:])
	}
	if args.Path == "" {
		return interfaces.ToolResult{Error: fmt.Errorf("file_write requires a non-empty path")}, nil
	}

	path, err := filepath.Abs(args.Path)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}
	if !t.allowed(path) {
		return interfaces.ToolResult{Error: fmt.Errorf("path %q is outside the allowed directories", path)}, nil
	}
	if t.Confirm != nil && !t.Confirm(path, args.Content) {
		return interfaces.ToolResult{Output: "Write cancelled: no confirmation."}, nil
	}

	flags := os.O_CREATE | os.O_WRONLY
	if args.Append {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0644)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}
	defer f.Close()

	if _, err := f.WriteString(args.Content + "\n"); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{
		Output: fmt.Sprintf("Successfully wrote to file: %s", path),
	}, nil
}

func (t *FileWriteTool) allowed(path string) bool {
	roots := t.AllowedRoots
	if len(roots) == 0 {
		roots = []string{".", "~"}
	}
	for _, root := range roots {
		if strings.HasPrefix(root, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				root = filepath.Join(home, root[2:])
			}
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(abs, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			continue
		}
		return true
	}
	return false
}
