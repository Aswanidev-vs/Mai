package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/mai/pkg/interfaces"
)

// Full mock registry for function calling tests
type fullMockRegistry struct {
	tools   []interfaces.Tool
	execute func(ctx context.Context, name string, params json.RawMessage) (interfaces.ToolResult, error)
}

func (r *fullMockRegistry) Register(tool interfaces.Tool) error {
	r.tools = append(r.tools, tool)
	return nil
}

func (r *fullMockRegistry) List() []interfaces.ToolMetadata {
	var result []interfaces.ToolMetadata
	for _, t := range r.tools {
		result = append(result, t.Metadata())
	}
	return result
}

func (r *fullMockRegistry) Discover(ctx context.Context, description string) ([]interfaces.Tool, error) {
	return r.tools, nil
}

func (r *fullMockRegistry) Execute(ctx context.Context, name string, params json.RawMessage) (interfaces.ToolResult, error) {
	if r.execute != nil {
		return r.execute(ctx, name, params)
	}
	return interfaces.ToolResult{Output: "mock executed"}, nil
}

func TestFunctionCaller_Execute_ToolFound(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"tool": "web_search", "params": {"query": "weather"}, "reasoning": "User wants weather info"}`, nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "open_application", Description: "Open an app"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	output, results, err := caller.Execute(context.Background(), "What's the weather?", "")

	require.NoError(t, err)
	assert.NotEmpty(t, output)
	assert.Len(t, results, 1)
	assert.Equal(t, "web_search", results[0].Call.Tool)
}

func TestFunctionCaller_Execute_ToolNotFound(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"tool": "magic_wand", "params": {"wish": "rain"}, "reasoning": "Use magic"}`, nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	output, results, err := caller.Execute(context.Background(), "Make it rain", "")

	require.NoError(t, err)
	assert.Contains(t, output, "not available")
	assert.Empty(t, results)
}

func TestFunctionCaller_Execute_NoToolMatched(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"tool": "none", "params": {}, "reasoning": "This is a conversational question"}`, nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	output, results, err := caller.Execute(context.Background(), "How are you?", "")

	require.NoError(t, err)
	assert.Equal(t, "This is a conversational question", output)
	assert.Nil(t, results)
}

func TestFunctionCaller_Execute_JSONParseError(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return "this is not json", nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	_, _, err := caller.Execute(context.Background(), "test", "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse function call")
}

func TestFunctionCaller_Execute_JSONWithPreamble(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return "Here's the tool call:\n```json\n{\"tool\": \"get_system_time\", \"params\": {}, \"reasoning\": \"Get time\"}\n```", nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "get_system_time", Description: "Get current time"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	output, results, err := caller.Execute(context.Background(), "What time is it?", "")

	require.NoError(t, err)
	assert.NotEmpty(t, output)
	assert.Len(t, results, 1)
}

func TestFunctionCaller_Execute_ExecError(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"tool": "web_search", "params": {"query": "test"}, "reasoning": "search"}`, nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
		execute: func(ctx context.Context, name string, params json.RawMessage) (interfaces.ToolResult, error) {
			return interfaces.ToolResult{}, fmt.Errorf("network timeout")
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	_, results, err := caller.Execute(context.Background(), "search for test", "")

	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.NotEmpty(t, results[0].Error)
}

func TestFunctionCaller_ExecuteChain(t *testing.T) {
	callCount := 0
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			callCount++
			if callCount <= 2 {
				return fmt.Sprintf(`{"tool": "web_search", "params": {"query": "step %d"}, "reasoning": "step %d"}`, callCount, callCount), nil
			}
			return `{"tool": "done", "params": {}, "reasoning": "task complete"}`, nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	output, results, err := caller.ExecuteChain(context.Background(), "Multi-step research", 3)

	require.NoError(t, err)
	assert.NotEmpty(t, output)
	assert.Len(t, results, 2)
}

func TestFunctionCaller_ExecuteChain_MaxSteps(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"tool": "web_search", "params": {"query": "loop"}, "reasoning": "keep going"}`, nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	_, results, err := caller.ExecuteChain(context.Background(), "Infinite loop test", 2)

	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestFunctionCaller_Execute_ToolNameCaseInsensitive(t *testing.T) {
	mockLLM := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"tool": "Web_Search", "params": {"query": "test"}, "reasoning": "search"}`, nil
		},
	}

	registry := &fullMockRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
	}

	caller := NewFunctionCaller(mockLLM, registry)
	_, results, err := caller.Execute(context.Background(), "search for test", "")

	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "web_search", results[0].Call.Tool)
}
