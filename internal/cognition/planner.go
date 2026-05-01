package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

type TaskNode struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Tool        string     `json:"tool,omitempty"`
	ToolInput   json.RawMessage `json:"tool_input,omitempty"`
	Children    []TaskNode `json:"children,omitempty"`
	Status      string     `json:"status"` // "pending", "active", "completed", "failed"
	DependsOn   []string   `json:"depends_on,omitempty"`
}

type Plan struct {
	Goal    string     `json:"goal"`
	Root    []TaskNode `json:"root"`
	Flat    []TaskNode `json:"flat"`
}

type Planner struct {
	llm interfaces.LLMProvider
}

func NewPlanner(llm interfaces.LLMProvider) *Planner {
	return &Planner{llm: llm}
}

func (p *Planner) Decompose(ctx context.Context, goal string, availableTools []interfaces.ToolMetadata) (*Plan, error) {
	log.Printf("[Planner] Decomposing goal: %s", goal)

	toolsStr, _ := json.MarshalIndent(availableTools, "", "  ")

	prompt := fmt.Sprintf(`You are a task planner. Decompose this goal into sub-tasks.

Goal: %s

Available tools:
%s

Return a JSON array of sub-tasks. Each sub-task:
- "description": what to do
- "tool": tool name to use (or "" if no tool needed)
- "tool_input": JSON params for the tool (or {})
- "depends_on": array of sub-task descriptions that must complete first (or [])

Rules:
- Maximum 8 sub-tasks
- Each sub-task should be actionable and specific
- Order them sequentially (dependencies reference earlier tasks)
- Use ONLY the tools listed above

Respond with ONLY the JSON array:
[{"description":"...","tool":"...","tool_input":{},"depends_on":[]}]`, goal, string(toolsStr))

	response, err := p.llm.GenerateStructured(ctx, prompt, json.RawMessage(`{
		"type": "array",
		"items": {
			"type": "object",
			"properties": {
				"description": {"type": "string"},
				"tool": {"type": "string"},
				"tool_input": {"type": "object"},
				"depends_on": {"type": "array", "items": {"type": "string"}}
			},
			"required": ["description"]
		}
	}`))
	if err != nil {
		return nil, fmt.Errorf("planner LLM error: %w", err)
	}

	// Sanitize JSON
	clean := string(response)
	if idx := strings.LastIndex(clean, "]"); idx != -1 {
		clean = clean[:idx+1]
	}

	var rawTasks []struct {
		Description string          `json:"description"`
		Tool        string          `json:"tool"`
		ToolInput   json.RawMessage `json:"tool_input"`
		DependsOn   []string        `json:"depends_on"`
	}

	if err := json.Unmarshal([]byte(clean), &rawTasks); err != nil {
		return nil, fmt.Errorf("failed to parse plan: %w. Raw: %s", err, string(response))
	}

	plan := &Plan{Goal: goal}
	for i, raw := range rawTasks {
		node := TaskNode{
			ID:          fmt.Sprintf("task_%d", i+1),
			Description: raw.Description,
			Tool:        raw.Tool,
			ToolInput:   raw.ToolInput,
			Status:      "pending",
			DependsOn:   raw.DependsOn,
		}
		if node.ToolInput == nil {
			node.ToolInput = json.RawMessage(`{}`)
		}
		plan.Root = append(plan.Root, node)
		plan.Flat = append(plan.Flat, node)
	}

	log.Printf("[Planner] Created plan with %d sub-tasks", len(plan.Root))
	return plan, nil
}

func (p *Planner) GetNextTasks(plan *Plan) []TaskNode {
	var ready []TaskNode
	completed := make(map[string]bool)

	for _, task := range plan.Flat {
		if task.Status == "completed" {
			completed[task.Description] = true
		}
	}

	for _, task := range plan.Flat {
		if task.Status != "pending" {
			continue
		}
		allDepsMet := true
		for _, dep := range task.DependsOn {
			if !completed[dep] {
				allDepsMet = false
				break
			}
		}
		if allDepsMet {
			ready = append(ready, task)
		}
	}

	return ready
}

func (p *Planner) MarkCompleted(plan *Plan, taskID string) {
	for i := range plan.Flat {
		if plan.Flat[i].ID == taskID {
			plan.Flat[i].Status = "completed"
			break
		}
	}
	for i := range plan.Root {
		if plan.Root[i].ID == taskID {
			plan.Root[i].Status = "completed"
			break
		}
	}
}

func (p *Planner) MarkFailed(plan *Plan, taskID string) {
	for i := range plan.Flat {
		if plan.Flat[i].ID == taskID {
			plan.Flat[i].Status = "failed"
			break
		}
	}
	for i := range plan.Root {
		if plan.Root[i].ID == taskID {
			plan.Root[i].Status = "failed"
			break
		}
	}
}
