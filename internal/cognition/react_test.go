package cognition

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/user/mai/pkg/interfaces"
)

// Mock types for testing

type mockTool struct {
	metadata interfaces.ToolMetadata
}

func (t *mockTool) Metadata() interfaces.ToolMetadata {
	return t.metadata
}

func (t *mockTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	return interfaces.ToolResult{Output: "mock result"}, nil
}

type mockToolRegistry struct {
	tools []interfaces.Tool
}

func (m *mockToolRegistry) Register(tool interfaces.Tool) error {
	m.tools = append(m.tools, tool)
	return nil
}

func (m *mockToolRegistry) List() []interfaces.ToolMetadata {
	var result []interfaces.ToolMetadata
	for _, t := range m.tools {
		result = append(result, t.Metadata())
	}
	return result
}

func (m *mockToolRegistry) Discover(ctx context.Context, description string) ([]interfaces.Tool, error) {
	return m.tools, nil
}

func (m *mockToolRegistry) Execute(ctx context.Context, name string, params json.RawMessage) (interfaces.ToolResult, error) {
	return interfaces.ToolResult{Output: "mock result"}, nil
}

func TestSanitizeJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "clean JSON object",
			input:    `{"thought":"hello","action":"test"}`,
			expected: `{"thought":"hello","action":"test"}`,
		},
		{
			name:     "JSON with markdown fences",
			input:    "```json\n{\"thought\":\"hello\"}\n```",
			expected: `{"thought":"hello"}`,
		},
		{
			name:     "JSON with preamble text",
			input:    "Here is the JSON response:\n{\"thought\":\"hello\"}",
			expected: `{"thought":"hello"}`,
		},
		{
			name:     "JSON with trailing garbage",
			input:    `{"thought":"hello"} some trailing text`,
			expected: `{"thought":"hello"}`,
		},
		{
			name:     "JSON with preamble and trailing garbage",
			input:    "Response:\n{\"thought\":\"hello\"}\nDone.",
			expected: `{"thought":"hello"}`,
		},
		{
			name:     "bare string without JSON",
			input:    "just some text",
			expected: "just some text",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "JSON array",
			input:    `[{"a":1},{"b":2}]`,
			expected: `[{"a":1},{"b":2}]`,
		},
		{
			name:     "nested JSON",
			input:    `{"outer":{"inner":"value"},"action":"test"}`,
			expected: `{"outer":{"inner":"value"},"action":"test"}`,
		},
		{
			name:     "JSON with extra newlines",
			input:    "\n\n{\"thought\":\"hello\"}\n\n",
			expected: `{"thought":"hello"}`,
		},
		{
			name:     "multiple code fences",
			input:    "```json\n```json\n{\"a\":1}\n```\n```",
			expected: `{"a":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sanitizeJSON(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSanitizeJSON_PreservesValidJSON(t *testing.T) {
	validJSON := `{"thought":"I need to search","action":"web_search","action_input":{"query":"weather today"}}`
	result := sanitizeJSON(validJSON)

	var parsed map[string]interface{}
	err := json.Unmarshal([]byte(result), &parsed)
	require.NoError(t, err, "sanitized JSON should be valid")
	assert.Equal(t, "I need to search", parsed["thought"])
	assert.Equal(t, "web_search", parsed["action"])
}

func TestValidateToolCall(t *testing.T) {
	registry := &mockToolRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "open_application", Description: "Open an app"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "get_system_time", Description: "Get current time"}},
		},
	}

	tests := []struct {
		name        string
		action      string
		params      json.RawMessage
		expectError bool
		expectJSON  bool
	}{
		{
			name:        "valid tool with object params",
			action:      "web_search",
			params:      json.RawMessage(`{"query":"weather"}`),
			expectError: false,
			expectJSON:  true,
		},
		{
			name:        "valid tool with null params",
			action:      "get_system_time",
			params:      json.RawMessage(`null`),
			expectError: false,
			expectJSON:  true,
		},
		{
			name:        "valid tool with empty params",
			action:      "get_system_time",
			params:      json.RawMessage(``),
			expectError: false,
			expectJSON:  true,
		},
		{
			name:        "valid tool with bare string params",
			action:      "web_search",
			params:      json.RawMessage(`"weather today"`),
			expectError: false,
			expectJSON:  true,
		},
		{
			name:        "invalid tool name",
			action:      "nonexistent_tool",
			params:      json.RawMessage(`{}`),
			expectError: true,
		},
		{
			name:        "invalid JSON params",
			action:      "web_search",
			params:      json.RawMessage(`{invalid`),
			expectError: true,
		},
		{
			name:        "tool name case insensitive match",
			action:      "Web_Search",
			params:      json.RawMessage(`{"q":"test"}`),
			expectError: false,
			expectJSON:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := validateToolCall(registry, tt.action, tt.params)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.expectJSON {
					var obj interface{}
					err := json.Unmarshal(result, &obj)
					assert.NoError(t, err, "result should be valid JSON")
				}
			}
		})
	}
}

func TestReActLoop_BuildPrompt_NaturalLanguage(t *testing.T) {
	defer goleak.VerifyNone(t)

	registry := &mockToolRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "get_system_time", Description: "Get current time"}},
		},
	}

	loop := NewReActLoop(nil, registry, nil)

	// Test that prompt uses natural language, not rigid JSON format
	prompt := loop.buildPrompt("What time is it?", nil)

	assert.Contains(t, prompt, "think through this naturally")
	assert.Contains(t, prompt, "What time is it?")
	assert.Contains(t, prompt, "get_system_time")
	// Should NOT contain rigid step labels
	assert.NotContains(t, prompt, "RULE 1:")
	assert.NotContains(t, prompt, "RULE 2:")
}

func TestReActLoop_BuildPrompt_WithHistory(t *testing.T) {
	defer goleak.VerifyNone(t)

	registry := &mockToolRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
		},
	}

	loop := NewReActLoop(nil, registry, nil)

	steps := []ReActStep{
		{Thought: "I should search", Action: "web_search", Observation: "Found results"},
	}

	prompt := loop.buildPrompt("What's the weather?", steps)

	assert.Contains(t, prompt, "WHAT HAPPENED SO FAR")
	assert.Contains(t, prompt, "web_search")
	assert.Contains(t, prompt, "Found results")
}

func TestReActLoop_BuildToolGuide(t *testing.T) {
	defer goleak.VerifyNone(t)

	registry := &mockToolRegistry{
		tools: []interfaces.Tool{
			&mockTool{metadata: interfaces.ToolMetadata{Name: "youtube_play", Description: "Play YouTube videos"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "open_application", Description: "Open an app"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "web_search", Description: "Search the web"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "get_system_time", Description: "Get time"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "whatsapp_send", Description: "Send WhatsApp message"}},
			&mockTool{metadata: interfaces.ToolMetadata{Name: "ui_automation", Description: "UI automation"}},
		},
	}

	loop := NewReActLoop(nil, registry, nil)
	guide := loop.buildToolGuide()

	assert.Contains(t, guide, "youtube_play")
	assert.Contains(t, guide, "open_application")
	assert.Contains(t, guide, "web_search")
	assert.Contains(t, guide, "get_system_time")
	assert.Contains(t, guide, "whatsapp_send")
	assert.Contains(t, guide, "ui_automation")
}
