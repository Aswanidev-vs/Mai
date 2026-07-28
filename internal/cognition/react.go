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

// containsHallucinationMarker checks if the response contains common
// hallucination markers that should be stripped or flagged.
func containsHallucinationMarker(s string) bool {
	lower := strings.ToLower(s)
	markers := []string{
		"i'm not sure but",
		"i think maybe",
		"i believe perhaps",
		"according to my knowledge",
		"as far as i know",
		"i don't have access to",
		"i cannot verify",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
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
	verifier      *Verifier
	maxIterations int
}

func NewReActLoop(llm interfaces.LLMProvider, registry interfaces.ToolRegistry, memory interfaces.WorkingMemory) *ReActLoop {
	return &ReActLoop{
		llm:           llm,
		registry:      registry,
		memory:        memory,
		verifier:      NewVerifier(llm),
		maxIterations: 3, // 3 is sufficient — most tasks resolve in 1-2 iterations
	}
}

func (r *ReActLoop) Execute(ctx context.Context, goal string) (string, error) {
	steps := []ReActStep{}
	toolCalled := false // Track whether ANY tool has been executed

	// Track tool call history to detect loops
	type toolCall struct {
		action string
		params string
	}
	callHistory := []toolCall{}

	// Apply per-iteration timeout — a single ReAct cycle should not hang forever
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
			if toolCalled && len(steps) > 0 {
				return steps[len(steps)-1].Observation, nil
			}
			return "", fmt.Errorf("ReAct loop cancelled: %w", iterCtx.Err())
		default:
		}
		// 1. Build the prompt
		prompt := r.buildPrompt(goal, steps)

		// 2. Generate the next step (structured JSON)
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

		// Robust JSON sanitizer: strip markdown fences, preamble, trailing garbage
		cleanResponse := sanitizeJSON(string(response))

		var step ReActStep
		if err := json.Unmarshal([]byte(cleanResponse), &step); err != nil {
			// Second attempt: try to extract just the final_answer or thought from raw text
			log.Printf("[ReAct] JSON parse failed (%v), attempting text extraction from: %.200s", err, string(response))
			rawText := strings.TrimSpace(string(response))
			// Strip any remaining markdown
			rawText = strings.TrimPrefix(rawText, "```json")
			rawText = strings.TrimPrefix(rawText, "```")
			rawText = strings.TrimSuffix(rawText, "```")
			rawText = strings.TrimSpace(rawText)
			if rawText != "" {
				return rawText, nil
			}
			return "", fmt.Errorf("failed to parse ReAct step: %w. Original: %.200s", err, string(response))
		}

		log.Printf("[ReAct] Thought: %s", step.Thought)

		// --- ANTI-HALLUCINATION LOGIC ---

		// RULE 1: If the LLM provided an action, ALWAYS execute it first,
		//         even if it also provided a final_answer. The action takes priority.
		if step.Action != "" {
			// Validate tool exists in registry before proceeding
			params, err := validateToolCall(r.registry, step.Action, step.ActionInput)
			if err != nil {
				log.Printf("[ReAct] Tool validation failed: %v. Treating as conversation.", err)
				step.Observation = fmt.Sprintf("Tool %q is not available. Error: %v", step.Action, err)
				step.Action = ""
				steps = append(steps, step)
				// Force the LLM to provide a final_answer instead
				continue
			}
			step.ActionInput = params

			// LOOP DETECTION: Check if this exact tool+params was already called
			paramsStr := string(step.ActionInput)
			currentCall := toolCall{action: step.Action, params: paramsStr}
			loopCount := 0
			for _, prev := range callHistory {
				if prev.action == currentCall.action && prev.params == currentCall.params {
					loopCount++
				}
			}

			if loopCount >= 1 {
				// Same tool called twice with same params — break the loop
				log.Printf("[ReAct] LOOP DETECTED: %s called %d times with same params. Breaking loop.", step.Action, loopCount+1)
				if toolCalled {
					return fmt.Sprintf("I've determined the %s. No further action needed.", step.Action), nil
				}
				return step.Thought, nil
			}

			// Also detect semantic loops: same tool with different params but
			// producing no new information (3+ calls to any tool = likely stuck)
			totalToolCalls := len(callHistory) + 1
			if totalToolCalls >= 3 {
				log.Printf("[ReAct] TOO MANY TOOL CALLS (%d). Breaking loop.", totalToolCalls)
				if toolCalled && len(steps) > 0 {
					return steps[len(steps)-1].Observation, nil
				}
				return step.Thought, nil
			}

			callHistory = append(callHistory, currentCall)

			log.Printf("[ReAct] Action: %s(%s)", step.Action, step.ActionInput)

			result, err := r.registry.Execute(ctx, step.Action, step.ActionInput)
			if err != nil {
				step.Observation = fmt.Sprintf("Error executing tool: %v", err)
				r.reflectOnFailure(ctx, step.Action, err)
			} else if result.Error != nil {
				step.Observation = fmt.Sprintf("Tool returned error: %v. Output: %s", result.Error, result.Output)
				r.reflectOnFailure(ctx, step.Action, result.Error)
			} else {
				step.Observation = result.Output
				// Verify the tool output for plausibility
				if result.Output != "" && len(result.Output) < 500 {
					verification, vErr := r.verifier.VerifyToolCall(ctx, step.Action, params, result.Output)
					if vErr == nil && verification != nil && !verification.IsValid && verification.Confidence > 0.6 {
						log.Printf("[ReAct] VERIFICATION FAILED: %s — %v", step.Action, verification.Issues)
						step.Observation = fmt.Sprintf("WARNING: Result may be unreliable — %s. Raw output: %s",
							strings.Join(verification.Issues, "; "), result.Output)
					}
				}
			}

			toolCalled = true
			step.FinalAnswer = "" // Clear any premature final answer
			log.Printf("[ReAct] Observation: %s", step.Observation)
			steps = append(steps, step)
			continue
		}

		// RULE 2: If only a final_answer is provided:
		if step.FinalAnswer != "" {
			// If no tool was called, accept the answer directly — the LLM
			// determined no tool was needed (per updated prompt rules).
			// If a tool WAS called previously, verify before accepting.
			if toolCalled {
				lastStep := steps[len(steps)-1]
				verification, vErr := r.verifier.VerifyClaim(ctx, step.FinalAnswer, lastStep.Observation)
				if vErr == nil && verification != nil && !verification.IsValid && verification.Confidence > 0.6 {
					log.Printf("[ReAct] FINAL ANSWER FAILED VERIFICATION: %v. Forcing retry.", verification.Issues)
					step.Action = "none"
					step.Observation = fmt.Sprintf("Your proposed answer appears incorrect: %v. Review the observation and try again.", strings.Join(verification.Issues, "; "))
					step.FinalAnswer = ""
					steps = append(steps, step)
					continue
				}
			}
			return step.FinalAnswer, nil
		}

		// RULE 3: No action AND no final_answer — the LLM is stuck.
		// If a tool was already called, treat the thought as the final answer.
		if toolCalled {
			return step.Thought, nil
		}
		return "", fmt.Errorf("agent failed to provide action or final answer")
	}

	return "", fmt.Errorf("exceeded maximum ReAct iterations (%d)", r.maxIterations)
}

func (r *ReActLoop) buildPrompt(goal string, steps []ReActStep) string {
	tools := r.registry.List()
	toolsStr, _ := json.MarshalIndent(tools, "", "  ")

	system := fmt.Sprintf(`You are Mai's reasoning engine. Goal: %s

Available tools (use ONLY these exact names):
%s

RULES:
1. If the goal can be answered from your general knowledge (facts, explanations, casual conversation, opinions), provide final_answer directly WITHOUT calling any tool.
2. Only use tools when the goal requires: real-time data (weather, time, current events), performing actions (open app, send message, play media), or specific lookups (web search for recent info).
3. NEVER call the same tool twice with the same parameters.
4. action_input must be a JSON object: {"key":"value"}.
5. Leave final_answer empty ("") only if you NEED to call a tool first.
6. After one tool call + observation, provide final_answer immediately.
7. NEVER invent tool names — use ONLY the tools listed above.
8. NEVER make up facts or data — base your final_answer ONLY on tool observations or your training knowledge.
9. If a tool returns an error, acknowledge it honestly rather than guessing what the result "should" be.

Tool selection (use ONLY when truly needed):
- "play X on Y" → youtube_play {"query":"X","browser":"Y"}
- "open X" → open_application {"app_name":"X"}
- "what time" → get_system_time {}
- "search X" → web_search {"query":"X"}
- "send message" → whatsapp_send {"message":"...","recipient":"..."}
- "type/press" → ui_automation {"action":"type","value":"X"}

Respond: {"thought":"...","action":"tool_name","action_input":{...},"final_answer":""}
OR after observation: {"thought":"...","action":"","action_input":{},"final_answer":"your answer"}
OR if no tool needed: {"thought":"...","action":"","action_input":{},"final_answer":"your direct answer"}

Previous steps:
`, goal, string(toolsStr))

	history := ""
	for _, s := range steps {
		history += fmt.Sprintf("Thought: %s\nAction: %s\nObservation: %s\n", s.Thought, s.Action, s.Observation)
	}

	return system + history + "\nNext step (JSON only):"
}


func (r *ReActLoop) reflectOnFailure(ctx context.Context, action string, err error) {
	log.Printf("[Reflexion] Tool %s failed: %v. Adjusting strategy...", action, err)
	
	// In a real Phase 4 implementation, we would call the LLM specifically to 
	// analyze the failure and update the system prompt or working memory.
	reflection := fmt.Sprintf("I tried to use %s but it failed with: %v. I should try a different approach or check my parameters.", action, err)
	
	r.memory.Add(interfaces.MemoryEntry{
		Type:    "reflection",
		Content: reflection,
	})
}
