package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

type FunctionCall struct {
	Tool      string          `json:"tool"`
	Params    json.RawMessage `json:"params"`
	Reasoning string          `json:"reasoning,omitempty"`
}

type FunctionCallResult struct {
	Call   FunctionCall `json:"call"`
	Output string       `json:"output"`
	Error  string       `json:"error,omitempty"`
}

type FunctionCaller struct {
	llm      interfaces.LLMProvider
	registry interfaces.ToolRegistry
}

func NewFunctionCaller(llm interfaces.LLMProvider, registry interfaces.ToolRegistry) *FunctionCaller {
	return &FunctionCaller{
		llm:      llm,
		registry: registry,
	}
}

func (fc *FunctionCaller) Execute(ctx context.Context, userText string, emotionHint string) (string, []FunctionCallResult, error) {
	tools := fc.registry.List()
	toolsJSON, _ := json.MarshalIndent(tools, "", "  ")

	prompt := fmt.Sprintf(`You are Mai, a personal AI assistant. The user wants you to perform an action.

Available tools:
%s

User request: %s
%s
Select the best tool and provide parameters. If multiple tools are needed, call them one at a time.

IMPORTANT: Respond with ONLY a JSON object in this exact format:
{"tool": "tool_name", "params": {"key": "value"}, "reasoning": "brief explanation"}

If no tool matches the request, respond with:
{"tool": "none", "params": {}, "reasoning": "explanation why no tool matches"}`, string(toolsJSON), userText, emotionHint)

	response, err := fc.llm.GenerateStructured(ctx, prompt, json.RawMessage(`{
		"type": "object",
		"properties": {
			"tool": {"type": "string"},
			"params": {"type": "object"},
			"reasoning": {"type": "string"}
		},
		"required": ["tool"]
	}`))
	if err != nil {
		return "", nil, fmt.Errorf("LLM function call error: %w", err)
	}

	cleaned := string(response)
	if idx := strings.LastIndex(cleaned, "}"); idx != -1 {
		cleaned = cleaned[:idx+1]
	}

	var call FunctionCall
	if err := json.Unmarshal([]byte(cleaned), &call); err != nil {
		return "", nil, fmt.Errorf("failed to parse function call: %w", err)
	}

	if call.Tool == "none" || call.Tool == "" {
		return call.Reasoning, nil, nil
	}

	log.Printf("[FunctionCall] Tool: %s, Params: %s, Reasoning: %s", call.Tool, string(call.Params), call.Reasoning)

	if len(call.Params) == 0 || string(call.Params) == "null" {
		call.Params = json.RawMessage(`{}`)
	} else if call.Params[0] != '{' && call.Params[0] != '[' {
		call.Params = json.RawMessage(fmt.Sprintf(`{"query": %s}`, string(call.Params)))
	}

	result, err := fc.registry.Execute(ctx, call.Tool, call.Params)
	if err != nil {
		return "", []FunctionCallResult{{Call: call, Error: err.Error()}}, nil
	}

	return result.Output, []FunctionCallResult{{Call: call, Output: result.Output}}, nil
}

func (fc *FunctionCaller) ExecuteChain(ctx context.Context, userText string, maxSteps int) (string, []FunctionCallResult, error) {
	var allResults []FunctionCallResult
	var observations []string

	tools := fc.registry.List()
	toolsJSON, _ := json.MarshalIndent(tools, "", "  ")

	for step := 0; step < maxSteps; step++ {
		observationContext := ""
		if len(observations) > 0 {
			observationContext = "\nPrevious results:\n" + strings.Join(observations, "\n")
		}

		prompt := fmt.Sprintf(`You are Mai. Execute this task step by step.

Available tools:
%s
%s

User request: %s

Step %d of %d. What tool should be called next?
If the task is complete, respond with: {"tool": "done", "params": {}, "reasoning": "task complete"}
Otherwise: {"tool": "tool_name", "params": {"key": "value"}, "reasoning": "what and why"}`,
			string(toolsJSON), observationContext, userText, step+1, maxSteps)

		response, err := fc.llm.GenerateStructured(ctx, prompt, json.RawMessage(`{
			"type": "object",
			"properties": {
				"tool": {"type": "string"},
				"params": {"type": "object"},
				"reasoning": {"type": "string"}
			},
			"required": ["tool"]
		}`))
		if err != nil {
			return "", allResults, fmt.Errorf("step %d LLM error: %w", step+1, err)
		}

		cleaned := string(response)
		if idx := strings.LastIndex(cleaned, "}"); idx != -1 {
			cleaned = cleaned[:idx+1]
		}

		var call FunctionCall
		if err := json.Unmarshal([]byte(cleaned), &call); err != nil {
			return "", allResults, fmt.Errorf("step %d parse error: %w", step+1, err)
		}

		if call.Tool == "done" || call.Tool == "" {
			break
		}

		log.Printf("[FunctionChain] Step %d: %s(%s)", step+1, call.Tool, string(call.Params))

		if len(call.Params) == 0 || string(call.Params) == "null" {
			call.Params = json.RawMessage(`{}`)
		}

		result, err := fc.registry.Execute(ctx, call.Tool, call.Params)
		callResult := FunctionCallResult{Call: call}

		if err != nil {
			callResult.Error = err.Error()
			observations = append(observations, fmt.Sprintf("FAILED %s: %v", call.Tool, err))
		} else {
			callResult.Output = result.Output
			observations = append(observations, fmt.Sprintf("SUCCESS %s: %s", call.Tool, result.Output))
		}

		allResults = append(allResults, callResult)
	}

	finalPrompt := fmt.Sprintf(`Summarize what was accomplished based on these tool results:
%s

User's original request: %s

Provide a brief, natural summary (1-2 sentences):`, strings.Join(observations, "\n"), userText)

	summary, err := fc.llm.Generate(ctx, finalPrompt, interfaces.GenerationOptions{Temperature: 0.3})
	if err != nil {
		summary = fmt.Sprintf("Completed %d steps.", len(allResults))
	}

	return summary, allResults, nil
}
