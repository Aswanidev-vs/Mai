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

// Mock LLM provider for testing
type mockLLM struct {
	generateFunc func(prompt string, opts interfaces.GenerationOptions) (string, error)
}

func (m *mockLLM) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	if m.generateFunc != nil {
		return m.generateFunc(prompt, opts)
	}
	return "", fmt.Errorf("mock not configured")
}

func (m *mockLLM) Stream(ctx context.Context, prompt string, opts interfaces.GenerationOptions, onChunk func(string)) error {
	return fmt.Errorf("not implemented")
}

func (m *mockLLM) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	if m.generateFunc != nil {
		result, err := m.generateFunc(prompt, interfaces.GenerationOptions{})
		if err != nil {
			return nil, err
		}
		return json.RawMessage(result), nil
	}
	return nil, fmt.Errorf("mock not configured")
}

func (m *mockLLM) Embed(ctx context.Context, text string) ([]float32, error) {
	return nil, fmt.Errorf("not implemented")
}

func (m *mockLLM) HealthCheck(ctx context.Context) error {
	return nil
}

func TestVerifyClaim_Valid(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"is_valid": true, "confidence": 0.95, "issues": [], "correction": ""}`, nil
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyClaim(context.Background(), "The sky is blue", "Visual observation confirms sky color")

	require.NoError(t, err)
	assert.True(t, result.IsValid)
	assert.InDelta(t, 0.95, result.Confidence, 0.01)
}

func TestVerifyClaim_Invalid(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"is_valid": false, "confidence": 0.85, "issues": ["Contradicts known facts"], "correction": "The sky appears blue due to Rayleigh scattering"}`, nil
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyClaim(context.Background(), "The sky is green", "Standard atmosphere")

	require.NoError(t, err)
	assert.False(t, result.IsValid)
	assert.Contains(t, result.Issues, "Contradicts known facts")
}

func TestVerifyClaim_LLMParseError_FailClosed(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return "this is not valid json at all", nil
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyClaim(context.Background(), "test claim", "test context")

	require.NoError(t, err)
	// FAIL CLOSED: parse error should return IsValid=false
	assert.False(t, result.IsValid)
	assert.Equal(t, 0.3, result.Confidence)
	assert.NotEmpty(t, result.Issues)
}

func TestVerifyClaim_LLMError_FailClosed(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return "", fmt.Errorf("LLM connection failed")
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyClaim(context.Background(), "test claim", "test context")

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestVerifyToolCall_Success(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"is_valid": true, "confidence": 0.9, "issues": [], "correction": ""}`, nil
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyToolCall(context.Background(), "web_search", json.RawMessage(`{"query":"weather"}`), "Sunny, 75°F")

	require.NoError(t, err)
	assert.True(t, result.IsValid)
}

func TestVerifyToolCall_Failure(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return `{"is_valid": false, "confidence": 0.7, "issues": ["Tool returned empty output"], "correction": ""}`, nil
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyToolCall(context.Background(), "web_search", json.RawMessage(`{"query":"weather"}`), "")

	require.NoError(t, err)
	assert.False(t, result.IsValid)
}

func TestVerifyToolCall_ParseError_FailClosed(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return "not json", nil
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyToolCall(context.Background(), "test_tool", json.RawMessage(`{}`), "some output")

	require.NoError(t, err)
	// FAIL CLOSED
	assert.False(t, result.IsValid)
	assert.Equal(t, 0.3, result.Confidence)
}

func TestVerifyToolCall_LLMError_FailClosed(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			return "", fmt.Errorf("timeout")
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyToolCall(context.Background(), "test_tool", json.RawMessage(`{}`), "output")

	// VerifyToolCall FAIL CLOSED: returns a result (not error) with IsValid=false
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.False(t, result.IsValid)
	assert.Equal(t, 0.3, result.Confidence)
}

func TestVerifyClaim_JSONWithPreamble(t *testing.T) {
	mock := &mockLLM{
		generateFunc: func(prompt string, opts interfaces.GenerationOptions) (string, error) {
			// Simulate LLM returning JSON with preamble text
			return "Here is the verification result:\n{\"is_valid\": true, \"confidence\": 0.8}", nil
		},
	}

	verifier := NewVerifier(mock)
	result, err := verifier.VerifyClaim(context.Background(), "test", "context")

	require.NoError(t, err)
	// sanitizeJSON should handle the preamble
	assert.True(t, result.IsValid)
}
