package skills

import (
	"context"
	"fmt"
	"time"

	"github.com/user/mai/internal/cognition"
	"github.com/user/mai/internal/memory"
	"github.com/user/mai/internal/personality"
	"github.com/user/mai/pkg/interfaces"
)

// Runner executes a matched skill using the existing cognition pipeline.
// v1 approach: route to ReAct with a skill-specific prompt.
type Runner struct {
	registry *Registry
	react    *cognition.ReActLoop
	llm      interfaces.LLMProvider
	memory   *memory.Manager
}

func NewRunner(registry *Registry, react *cognition.ReActLoop, llm interfaces.LLMProvider, mem *memory.Manager) *Runner {
	return &Runner{
		registry: registry,
		react:    react,
		llm:      llm,
		memory:   mem,
	}
}

func (r *Runner) TryRun(ctx context.Context, text string, emotion personality.EmotionState) (bool, string, error) {
	if r == nil || r.registry == nil || r.react == nil || r.memory == nil {
		return false, "", fmt.Errorf("skills runner not initialized")
	}

	skill, ok := r.registry.Match(text)
	if !ok {
		return false, "", nil
	}

	// Persist skill invocation for traceability.
	r.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID:        fmt.Sprintf("skill_%d", time.Now().UnixMilli()),
		Type:      "skill_invoked",
		Content:   fmt.Sprintf("%s (%s): %s", skill.Name, skill.ID, text),
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"skill_id": skill.ID},
	})

	// Skill-specific prompt framing.
	// We intentionally keep it simple and rely on existing ReAct execution.
	reactInput := fmt.Sprintf(
		`Skill: %s
Description: %s

User request: %s

Use your available tools and produce a concise, direct response.
If you need clarification, ask one targeted question.`, skill.Name, skill.Description, text,
	)

	// Execute via ReAct to reuse tool calling behavior.
	resp, err := r.react.Execute(ctx, reactInput)
	if err != nil {
		return true, "", err
	}

	if resp == "" {
		resp = "Done."
	}
	return true, resp, nil
}
