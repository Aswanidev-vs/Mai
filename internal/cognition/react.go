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
		return "", fmt.Errorf("agent failed to provide action or final answer")
	}

	return "", fmt.Errorf("exceeded maximum ReAct iterations (%d)", r.maxIterations)
}

func (r *ReActLoop) buildPrompt(goal string, steps []ReActStep) string {
	tools := r.registry.List()
	toolsStr, _ := json.MarshalIndent(tools, "", "  ")

	system := fmt.Sprintf(`You are an AI agent. Your goal: %s

You MUST respond with a single JSON object. You have these tools:
%s

DECISION TABLE — pick the RIGHT tool:
- "play X" or "YouTube" → youtube_play with {"query":"X","browser":"..."}
- "open X" (app name) → open_application with {"app_name":"X"}
- "what time" or "date" → get_system_time with {}
- "search X" or "lookup X" → web_search with {"query":"X"}
- "write/save to file" → file_write with {"path":"...","content":"..."}
- "send WhatsApp" → whatsapp_send with {"message":"...","recipient":"..."}
- "type X" or "press key" → ui_automation with {"action":"type","value":"X"}
- "Ctrl+F" or shortcut → ui_automation with {"action":"shortcut","value":"f","modifier":"control"}

EXAMPLES:
User: "Play Perfect on YouTube"
→ {"thought":"User wants to play Perfect on YouTube.","action":"youtube_play","action_input":{"query":"Perfect"},"final_answer":""}

User: "What time is it?"
→ {"thought":"User wants the current time.","action":"get_system_time","action_input":{},"final_answer":""}

User: "Open WhatsApp"
→ {"thought":"User wants to open WhatsApp.","action":"open_application","action_input":{"app_name":"whatsapp"},"final_answer":""}

RULES:
1. You MUST call a tool. Do NOT skip to final_answer.
2. action_input MUST be a JSON object like {"key":"value"}, never a bare string.
3. Leave final_answer EMPTY ("") when using an action.
4. Only write final_answer AFTER you see an Observation from a tool.

Respond with ONLY this JSON: {"thought":"...","action":"...","action_input":{...},"final_answer":""}

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
