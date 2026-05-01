package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

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
		maxIterations: 5,
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

	for i := 0; i < r.maxIterations; i++ {
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

		// JSON Sanitizer: Strip trailing garbage after the last valid closing brace.
		cleanResponse := string(response)
		if idx := strings.LastIndex(cleanResponse, "}"); idx != -1 {
			cleanResponse = cleanResponse[:idx+1]
		}

		var step ReActStep
		if err := json.Unmarshal([]byte(cleanResponse), &step); err != nil {
			return "", fmt.Errorf("failed to parse ReAct step: %w. Original: %s", err, string(response))
		}

		log.Printf("[ReAct] Thought: %s", step.Thought)

		// --- ANTI-HALLUCINATION LOGIC ---

		// RULE 1: If the LLM provided an action, ALWAYS execute it first,
		//         even if it also provided a final_answer. The action takes priority.
		if step.Action != "" {
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

			callHistory = append(callHistory, currentCall)

			log.Printf("[ReAct] Action: %s(%s)", step.Action, step.ActionInput)

			// Handle "naked" strings (LLM sending a bare string instead of a JSON object)
			params := step.ActionInput
			if len(params) == 0 || string(params) == "null" {
				params = json.RawMessage(`{}`)
			} else if params[0] != '{' && params[0] != '[' {
				params = json.RawMessage(fmt.Sprintf(`{"query": %q}`, string(params)))
			}

			result, err := r.registry.Execute(ctx, step.Action, params)
			if err != nil {
				step.Observation = fmt.Sprintf("Error executing tool: %v", err)
				r.reflectOnFailure(ctx, step.Action, err)
			} else if result.Error != nil {
				step.Observation = fmt.Sprintf("Tool returned error: %v. Output: %s", result.Error, result.Output)
				r.reflectOnFailure(ctx, step.Action, result.Error)
			} else {
				step.Observation = result.Output
			}

			toolCalled = true
			step.FinalAnswer = "" // Clear any premature final answer
			log.Printf("[ReAct] Observation: %s", step.Observation)
			steps = append(steps, step)
			continue
		}

		// RULE 2: If only a final_answer is provided:
		if step.FinalAnswer != "" {
			// Block it if no tool has ever been called — the LLM is hallucinating.
			if !toolCalled {
				log.Printf("[ReAct] BLOCKED hallucinated final_answer (no tool was called). Forcing retry.")
				// Inject a fake "observation" to steer the LLM toward using a tool.
				step.Action = "none"
				step.Observation = "ERROR: You must use a tool before providing a final_answer. Review the available tools and pick one."
				step.FinalAnswer = ""
				steps = append(steps, step)
				continue
			}
			// A tool WAS called previously, so this final_answer is legitimate.
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

Available tools:
%s

RULES:
1. Pick the correct tool. Call it. Wait for the Observation. Then provide final_answer.
2. NEVER call the same tool twice with the same parameters.
3. action_input must be a JSON object: {"key":"value"}.
4. Leave final_answer empty ("") until you have an Observation.
5. After one tool call + observation, provide final_answer immediately.

Tool selection:
- "play X on Y" → youtube_play {"query":"X","browser":"Y"}
- "open X" → open_application {"app_name":"X"}
- "what time" → get_system_time {}
- "search X" → web_search {"query":"X"}
- "send message" → whatsapp_send {"message":"...","recipient":"..."}
- "type/press" → ui_automation {"action":"type","value":"X"}

Respond: {"thought":"...","action":"tool_name","action_input":{...},"final_answer":""}
OR after observation: {"thought":"...","action":"","action_input":{},"final_answer":"your answer"}

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
