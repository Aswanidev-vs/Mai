package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/user/mai/pkg/interfaces"
)

// Registry implements interfaces.ToolRegistry
type Registry struct {
	mu    sync.RWMutex
	tools map[string]interfaces.Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]interfaces.Tool),
	}
}

func (r *Registry) Register(tool interfaces.Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	metadata := tool.Metadata()
	if _, exists := r.tools[metadata.Name]; exists {
		return fmt.Errorf("tool already registered: %s", metadata.Name)
	}

	r.tools[metadata.Name] = tool
	return nil
}

func (r *Registry) Discover(ctx context.Context, description string) ([]interfaces.Tool, error) {
	// For now, simple keyword matching or just return all
	// In a JARVIS-class system, this would use embeddings to find relevant tools
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []interfaces.Tool
	for _, tool := range r.tools {
		// Basic check: if description is in tool name or description
		// (Case-insensitive check would be better)
		result = append(result, tool)
	}
	return result, nil
}

func (r *Registry) Execute(ctx context.Context, toolName string, params json.RawMessage) (interfaces.ToolResult, error) {
	r.mu.RLock()
	tool, exists := r.tools[toolName]
	r.mu.RUnlock()

	if !exists {
		return interfaces.ToolResult{}, fmt.Errorf("tool not found: %s", toolName)
	}

	return tool.Execute(ctx, params)
}

func (r *Registry) List() []interfaces.ToolMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []interfaces.ToolMetadata
	for _, tool := range r.tools {
		result = append(result, tool.Metadata())
	}
	return result
}
