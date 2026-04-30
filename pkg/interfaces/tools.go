package interfaces

import (
	"context"
	"encoding/json"
)

// ToolMetadata describes a tool for discovery
type ToolMetadata struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// ToolResult represents the output of a tool execution
type ToolResult struct {
	Output string `json:"output"`
	Error  error  `json:"error,omitempty"`
}

// Tool defines an executable capability
type Tool interface {
	Metadata() ToolMetadata
	Execute(ctx context.Context, params json.RawMessage) (ToolResult, error)
}

// ToolRegistry handles tool discovery and execution
type ToolRegistry interface {
	Register(tool Tool) error
	Discover(ctx context.Context, description string) ([]Tool, error)
	Execute(ctx context.Context, toolName string, params json.RawMessage) (ToolResult, error)
	List() []ToolMetadata
}
