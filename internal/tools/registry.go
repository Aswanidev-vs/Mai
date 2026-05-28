package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/user/mai/pkg/interfaces"
)

type Registry struct {
	mu       sync.RWMutex
	tools    map[string]interfaces.Tool
	categories map[interfaces.ToolCategory][]string
}

func NewRegistry() *Registry {
	return &Registry{
		tools:      make(map[string]interfaces.Tool),
		categories: make(map[interfaces.ToolCategory][]string),
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

	if metadata.Category != "" {
		r.categories[metadata.Category] = append(r.categories[metadata.Category], metadata.Name)
	}

	return nil
}

func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if tool, exists := r.tools[name]; exists {
		meta := tool.Metadata()
		if meta.Category != "" {
			tools := r.categories[meta.Category]
			for i, n := range tools {
				if n == name {
					r.categories[meta.Category] = append(tools[:i], tools[i+1:]...)
					break
				}
			}
		}
		delete(r.tools, name)
	}
}

func (r *Registry) Discover(ctx context.Context, description string) ([]interfaces.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []interfaces.Tool
	lower := strings.ToLower(description)

	for _, tool := range r.tools {
		meta := tool.Metadata()
		name := strings.ToLower(meta.Name)
		desc := strings.ToLower(meta.Description)

		if strings.Contains(name, lower) || strings.Contains(desc, lower) || strings.Contains(lower, name) {
			result = append(result, tool)
			continue
		}

		for _, kw := range meta.Keywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				result = append(result, tool)
				break
			}
		}
	}

	if len(result) == 0 {
		for _, tool := range r.tools {
			result = append(result, tool)
		}
	}

	return result, nil
}

func (r *Registry) DiscoverByCategory(category interfaces.ToolCategory) []interfaces.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []interfaces.Tool
	if names, ok := r.categories[category]; ok {
		for _, name := range names {
			if tool, exists := r.tools[name]; exists {
				result = append(result, tool)
			}
		}
	}
	return result
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

func (r *Registry) ListByCategory(category interfaces.ToolCategory) []interfaces.ToolMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []interfaces.ToolMetadata
	if names, ok := r.categories[category]; ok {
		for _, name := range names {
			if tool, exists := r.tools[name]; exists {
				result = append(result, tool.Metadata())
			}
		}
	}
	return result
}

func (r *Registry) GetMetadata(name string) (interfaces.ToolMetadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if tool, exists := r.tools[name]; exists {
		return tool.Metadata(), true
	}
	return interfaces.ToolMetadata{}, false
}

func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

func (r *Registry) Search(query string) []interfaces.ToolMetadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	query = strings.ToLower(query)
	words := strings.Fields(query)

	type scored struct {
		tool  interfaces.ToolMetadata
		score int
	}

	var results []scored
	for _, tool := range r.tools {
		meta := tool.Metadata()
		name := strings.ToLower(meta.Name)
		desc := strings.ToLower(meta.Description)
		score := 0

		for _, word := range words {
			if len(word) < 2 {
				continue
			}
			if strings.Contains(name, word) {
				score += 3
			}
			if strings.Contains(desc, word) {
				score += 2
			}
			for _, kw := range meta.Keywords {
				if strings.Contains(strings.ToLower(kw), word) {
					score += 1
				}
			}
		}

		if score > 0 {
			results = append(results, scored{meta, score})
		}
	}

	for i := 0; i < len(results); i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].score > results[i].score {
				results[i], results[j] = results[j], results[i]
			}
		}
	}

	var output []interfaces.ToolMetadata
	for _, r := range results {
		output = append(output, r.tool)
	}
	return output
}
