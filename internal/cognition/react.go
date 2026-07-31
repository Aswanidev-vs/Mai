package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

// sanitizeJSON strips trailing garbage after the last valid closing brace and
// removes any leading non-JSON preamble (e.g. markdown fences, "Here is the JSON:").
func sanitizeJSON(raw string) string {
	// Strip markdown code fences
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	s = strings.TrimSpace(s)

	// Find the first opening brace — everything before it is preamble
	startIdx := strings.IndexAny(s, "{[")
	if startIdx == -1 {
		return s
	}
	s = s[startIdx:]

	// Find the last closing brace — everything after it is trailing garbage
	endIdx := strings.LastIndexAny(s, "}]")
	if endIdx != -1 {
		s = s[:endIdx+1]
	}
	return s
}

// validateToolCall checks that the tool name exists in the registry and that
// action_input is a valid JSON object. Returns a cleaned-up version.
func validateToolCall(registry interfaces.ToolRegistry, action string, params json.RawMessage) (json.RawMessage, error) {
	// Check tool exists
	tools := registry.List()
	found := false
	for _, t := range tools {
		if strings.EqualFold(t.Name, action) {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("tool %q not found in registry", action)
	}

	// Normalize params
	if len(params) == 0 || string(params) == "null" {
		params = json.RawMessage(`{}`)
	} else if params[0] != '{' && params[0] != '[' {
		// LLM sent a bare string — wrap it
		params = json.RawMessage(fmt.Sprintf(`{"query": %q}`, string(params)))
	}

	// Validate it's valid JSON
	var obj interface{}
	if err := json.Unmarshal(params, &obj); err != nil {
		return nil, fmt.Errorf("invalid action_input JSON: %w", err)
	}

	return params, nil
}

// ReActStep represents a single iteration in the ReAct loop
type ReActStep struct {
	Thought     string          `json:"thought"`
	Action      string          `json:"action,omitempty"`
	ActionInput json.RawMessage `json:"action_input,omitempty"`
	Observation string          `json:"observation,omitempty"`
	FinalAnswer string          `json:"final_answer,omitempty"`
}

// ReActLoop implements the Reasoning and Acting logic
type ReActLoop struct {
	llm           interfaces.LLMProvider
	registry      interfaces.ToolRegistry
	memory        interfaces.WorkingMemory
	maxIterations int
}

func NewReActLoop(llm interfaces.LLMProvider, registry interfaces.ToolRegistry, memory interfaces.WorkingMemory) *ReActLoop {
	return &ReActLoop{
		llm:           llm,
		registry:      registry,
		memory:        memory,
		maxIterations: 3,
	}
}

func (r *ReActLoop) Execute(ctx context.Context, goal string) (string, error) {
	steps := []ReActStep{}

	// Track tool call history to detect loops
	type toolCall struct {
		action string
		params string
	}
	callHistory := []toolCall{}

	// Apply per-iteration timeout
	iterCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		iterCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	for i := 0; i < r.maxIterations; i++ {
		// Check context cancellation
		select {
		case <-iterCtx.Done():
			if len(steps) > 0 {
				last := steps[len(steps)-1]
				if last.Observation != "" {
					return last.Observation, nil
				}
			}
			return "", fmt.Errorf("ReAct loop cancelled: %w", iterCtx.Err())
		default:
		}

		// Build the prompt — natural language, not rigid JSON format
		prompt := r.buildPrompt(goal, steps)

		// Generate the next step
		response, err := r.llm.GenerateStructured(ctx, prompt, json.RawMessage(`{
			"type": "object",
			"properties": {
				"thought": { "type": "string" },
				"action": { "type": "string" },
				"action_input": { "type": "object" },
				"final_answer": { "type": "string" }
			},
			"required": ["thought"]
		}`))
		if err != nil {
			return "", fmt.Errorf("llm error: %w", err)
		}

		cleanResponse := sanitizeJSON(string(response))

		var step ReActStep
		if err := json.Unmarshal([]byte(cleanResponse), &step); err != nil {
			// If JSON parsing fails, treat the entire response as the final answer
			rawText := strings.TrimSpace(string(response))
			rawText = strings.TrimPrefix(rawText, "```json")
			rawText = strings.TrimPrefix(rawText, "```")
			rawText = strings.TrimSuffix(rawText, "```")
			rawText = strings.TrimSpace(rawText)
			if rawText != "" {
				return rawText, nil
			}
			return "", fmt.Errorf("failed to parse ReAct step: %w", err)
		}

		log.Printf("[ReAct] Step %d — thought: %.120s", i+1, step.Thought)

		// --- ACTION HANDLING ---

		if step.Action != "" {
			// Validate tool exists
			params, err := validateToolCall(r.registry, step.Action, step.ActionInput)
			if err != nil {
				log.Printf("[ReAct] Tool validation failed: %v", err)
				step.Observation = fmt.Sprintf("Tool %q is not available.", step.Action)
				step.Action = ""
				steps = append(steps, step)
				continue
			}
			step.ActionInput = params

			// Loop detection: same tool+params called twice
			paramsStr := string(step.ActionInput)
			currentCall := toolCall{action: step.Action, params: paramsStr}
			for _, prev := range callHistory {
				if prev.action == currentCall.action && prev.params == currentCall.params {
					log.Printf("[ReAct] LOOP DETECTED: %s with same params. Breaking.", step.Action)
					if len(steps) > 0 {
						return steps[len(steps)-1].Thought, nil
					}
					return step.Thought, nil
				}
			}

			// Max 3 tool calls per request — humans don't chain more than that either
			if len(callHistory) >= 2 {
				log.Printf("[ReAct] TOO MANY TOOL CALLS (%d). Wrapping up.", len(callHistory)+1)
				break
			}

			callHistory = append(callHistory, currentCall)
			log.Printf("[ReAct] Action: %s(%s)", step.Action, step.ActionInput)

			result, err := r.registry.Execute(ctx, step.Action, step.ActionInput)
			if err != nil {
				step.Observation = fmt.Sprintf("Error: %v", err)
				log.Printf("[ReAct] Tool error: %v", err)
			} else if result.Error != nil {
				step.Observation = fmt.Sprintf("Error: %v", result.Error)
			} else {
				step.Observation = result.Output
			}

			step.FinalAnswer = ""
			steps = append(steps, step)
			continue
		}

		// --- FINAL ANSWER ---

		if step.FinalAnswer != "" {
			return step.FinalAnswer, nil
		}

		// No action AND no final_answer — use the thought as the answer
		if len(steps) > 0 {
			return step.Thought, nil
		}
		return step.Thought, nil
	}

	// Max iterations reached — synthesize from what we have
	if len(steps) > 0 {
		last := steps[len(steps)-1]
		if last.Observation != "" {
			return last.Observation, nil
		}
		return last.Thought, nil
	}

	return "", fmt.Errorf("ReAct loop produced no output")
}

func (r *ReActLoop) buildPrompt(goal string, steps []ReActStep) string {
	tools := r.registry.List()
	toolsJSON, _ := json.MarshalIndent(tools, "", "  ")

	// Build tool usage guide — shorter, more direct
	toolGuide := r.buildToolGuide()

	var b strings.Builder
	b.WriteString(fmt.Sprintf(`You are Mai — think through this naturally, like a smart assistant would.

GOAL: %s

TOOLS (only use when the goal REQUIRES real-time data or an action):
%s

%s
THINKING APPROACH:
- If you already know the answer from general knowledge → answer directly, no tools needed
- If the goal needs current info (weather, time, news) or an action (open app, search web) → use a tool
- If a tool failed → acknowledge it honestly, don't guess what the result "should" be
- Never call the same tool twice with the same parameters
- After getting a tool result, synthesize it into a natural answer

`, goal, string(toolsJSON), toolGuide))

	// Append conversation history naturally, not as rigid steps
	if len(steps) > 0 {
		b.WriteString("WHAT HAPPENED SO FAR:\n")
		for _, s := range steps {
			if s.Action != "" {
				b.WriteString(fmt.Sprintf("- Tried %s → %s\n", s.Action, s.Observation))
			}
		}
		b.WriteString("\n")
	}

	b.WriteString(`RESPOND WITH JSON:
{"thought": "your reasoning", "action": "tool_name", "action_input": {...}, "final_answer": ""}

OR if you have the answer:
{"thought": "your reasoning", "action": "", "final_answer": "your answer"}

JSON only — no markdown, no extra text.`)

	return b.String()
}

func (r *ReActLoop) buildToolGuide() string {
	tools := r.registry.List()
	var guide strings.Builder
	guide.WriteString("TOOL USAGE:\n")

	for _, t := range tools {
		name := strings.ToLower(t.Name)
		switch {
		case strings.Contains(name, "youtube") || strings.Contains(name, "play"):
			guide.WriteString(fmt.Sprintf("- \"play X on Y\" → %s {\"query\":\"X\"}\n", t.Name))
		case strings.Contains(name, "open") || strings.Contains(name, "application"):
			guide.WriteString(fmt.Sprintf("- \"open X\" → %s {\"app_name\":\"X\"}\n", t.Name))
		case strings.Contains(name, "search"):
			guide.WriteString(fmt.Sprintf("- \"search X\" → %s {\"query\":\"X\"}\n", t.Name))
		case strings.Contains(name, "time") || strings.Contains(name, "clock"):
			guide.WriteString(fmt.Sprintf("- \"what time\" → %s {}\n", t.Name))
		case strings.Contains(name, "whatsapp") || strings.Contains(name, "send"):
			guide.WriteString(fmt.Sprintf("- \"send message\" → %s {\"message\":\"...\",\"recipient\":\"...\"}\n", t.Name))
		case strings.Contains(name, "automation") || strings.Contains(name, "ui"):
			guide.WriteString(fmt.Sprintf("- \"type/press\" → %s {\"action\":\"type\",\"value\":\"X\"}\n", t.Name))
		}
	}

	guide.WriteString("\n")
	return guide.String()
}
