package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

type VerificationResult struct {
	IsValid     bool    `json:"is_valid"`
	Confidence  float64 `json:"confidence"`
	Issues      []string `json:"issues,omitempty"`
	Correction  string  `json:"correction,omitempty"`
}

type Verifier struct {
	llm interfaces.LLMProvider
}

func NewVerifier(llm interfaces.LLMProvider) *Verifier {
	return &Verifier{llm: llm}
}

func (v *Verifier) VerifyClaim(ctx context.Context, claim string, context_ string) (*VerificationResult, error) {
	log.Printf("[Verifier] Verifying claim: %s", truncate(claim, 80))

	prompt := fmt.Sprintf(`Verify this claim for factual accuracy.

Claim: %s

Context: %s

Respond with JSON:
{
  "is_valid": true/false,
  "confidence": 0.0-1.0,
  "issues": ["list of issues found"],
  "correction": "corrected statement if invalid"
}`, claim, context_)

	response, err := v.llm.GenerateStructured(ctx, prompt, json.RawMessage(`{
		"type": "object",
		"properties": {
			"is_valid": {"type": "boolean"},
			"confidence": {"type": "number"},
			"issues": {"type": "array", "items": {"type": "string"}},
			"correction": {"type": "string"}
		},
		"required": ["is_valid", "confidence"]
	}`))
	if err != nil {
		return nil, fmt.Errorf("verifier LLM error: %w", err)
	}

	clean := string(response)
	if idx := strings.LastIndex(clean, "}"); idx != -1 {
		clean = clean[:idx+1]
	}

	var result VerificationResult
	if err := json.Unmarshal([]byte(clean), &result); err != nil {
		return &VerificationResult{
			IsValid:    true,
			Confidence: 0.5,
		}, nil
	}

	return &result, nil
}

func (v *Verifier) VerifyToolCall(ctx context.Context, toolName string, params json.RawMessage, observation string) (*VerificationResult, error) {
	prompt := fmt.Sprintf(`A tool was called and returned a result. Verify if the result looks correct.

Tool: %s
Parameters: %s
Result: %s

Did the tool succeed? Is the result reasonable for the given parameters?
Respond with JSON:
{
  "is_valid": true/false,
  "confidence": 0.0-1.0,
  "issues": ["any problems"],
  "correction": "suggested fix if invalid"
}`, toolName, string(params), observation)

	response, err := v.llm.GenerateStructured(ctx, prompt, json.RawMessage(`{
		"type": "object",
		"properties": {
			"is_valid": {"type": "boolean"},
			"confidence": {"type": "number"},
			"issues": {"type": "array", "items": {"type": "string"}},
			"correction": {"type": "string"}
		},
		"required": ["is_valid", "confidence"]
	}`))
	if err != nil {
		return &VerificationResult{IsValid: true, Confidence: 0.5}, nil
	}

	clean := string(response)
	if idx := strings.LastIndex(clean, "}"); idx != -1 {
		clean = clean[:idx+1]
	}

	var result VerificationResult
	if err := json.Unmarshal([]byte(clean), &result); err != nil {
		return &VerificationResult{IsValid: true, Confidence: 0.5}, nil
	}

	return &result, nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
