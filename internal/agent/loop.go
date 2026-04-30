package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/user/mai/internal/cognition"
	"github.com/user/mai/pkg/interfaces"
)

// Orchestrator implements interfaces.Agent and manages the BDI loop
type Orchestrator struct {
	bus       interfaces.EventBus
	memory    interfaces.MemoryManager
	llm       interfaces.LLMProvider
	registry  interfaces.ToolRegistry
	cognition *cognition.ReActLoop // We use the implementation directly for now

	status interfaces.AgentStatus
	cancel context.CancelFunc

	DirectAction func(text string) (bool, string, error)
}

func NewOrchestrator(
	bus interfaces.EventBus,
	memory interfaces.MemoryManager,
	llm interfaces.LLMProvider,
	registry interfaces.ToolRegistry,
	cognition *cognition.ReActLoop,
) *Orchestrator {
	return &Orchestrator{
		bus:       bus,
		memory:    memory,
		llm:       llm,
		registry:  registry,
		cognition: cognition,
		status:    interfaces.StatusIdle,
	}
}

func (o *Orchestrator) Start(ctx context.Context) error {
	agentCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel

	// Subscribe to relevant events
	o.bus.Subscribe("perception.audio.transcription", o.handleTranscription)
	o.bus.Subscribe("perception.vision.scene", o.handleVision)

	// Proactive Monitoring Ticker (e.g., every 5 minutes)
	ticker := time.NewTicker(5 * time.Minute)
	go func() {
		for {
			select {
			case <-ticker.C:
				o.proactiveSelfReflect(agentCtx)
			case <-agentCtx.Done():
				ticker.Stop()
				return
			}
		}
	}()

	log.Println("[Agent] Orchestrator started with Proactive Monitoring")
	o.status = interfaces.StatusIdle

	<-agentCtx.Done()
	return nil
}

func (o *Orchestrator) proactiveSelfReflect(ctx context.Context) {
	if o.status != interfaces.StatusIdle {
		return // Don't interrupt active work
	}

	log.Println("[Agent] Periodic self-reflection triggered...")

	// Goal: "Check surroundings and status, decide if any proactive action is needed."
	o.HandleInput(ctx, map[string]interface{}{
		"text": "Self-Reflection: Analyze current context and surroundings. Is there anything proactive I should do for the user?",
	})
}

func (o *Orchestrator) Stop() error {
	if o.cancel != nil {
		o.cancel()
	}
	return nil
}

func (o *Orchestrator) HandleInput(ctx context.Context, input map[string]interface{}) (*interfaces.AgentResponse, error) {
	text, ok := input["text"].(string)
	if !ok {
		return nil, fmt.Errorf("input must contain 'text' field")
	}

	o.status = interfaces.StatusThinking
	defer func() { o.status = interfaces.StatusIdle }()

	// --- SMART ROUTING ---
	// 1. Try legacy ActionExecutor first for 100% reliable regex matching
	if o.DirectAction != nil {
		executed, feedback, err := o.DirectAction(text)
		if err != nil {
			log.Printf("[Agent] DirectAction error: %v", err)
			return &interfaces.AgentResponse{Text: fmt.Sprintf("Error executing command: %v", err), Success: false}, nil
		}
		if executed {
			log.Printf("[Agent] Command executed directly via regex parser.")
			return &interfaces.AgentResponse{Text: feedback, Success: true}, nil
		}
	}

	lowerText := strings.ToLower(text)

	// 2. Check if it requires the Reasoning Engine (creative / analytical tasks ONLY)
	// NOTE: Action commands (send, play, open, etc.) are handled by DirectAction above.
	// We only trigger ReAct for tasks that need multi-step reasoning or creativity.
	reasoningKeywords := []string{
		"invent", "create", "solve", "design", "think", "analyze", "plan",
		"research", "investigate", "calculate", "compare", "evaluate",
		"why is", "how does", "explain", "what if", "summarize",
	}
	requiresReasoning := false
	for _, kw := range reasoningKeywords {
		if strings.Contains(lowerText, kw) {
			requiresReasoning = true
			break
		}
	}

	if requiresReasoning {
		log.Printf("[Agent] Engaging Reasoning Engine for tool/complex task: %s", text)
		response, err := o.cognition.Execute(ctx, text)
		if err != nil {
			return nil, err
		}
		return &interfaces.AgentResponse{Text: response, Success: true}, nil
	}

	// 3. Conversational / Fast-track
	log.Printf("[Agent] Fast-tracking conversational input: %s", text)

	// Prepend an anti-hallucination boundary
	safePrompt := fmt.Sprintf(`You are Mai, a helpful AI assistant.
CRITICAL RULE: If the user asks why something failed or asks about an error, DO NOT invent a fake error (like a segmentation fault). Admit that you don't have the logs in your immediate memory.

User: %s`, text)

	response, err := o.llm.Generate(ctx, safePrompt, interfaces.GenerationOptions{})
	if err != nil {
		return nil, err
	}
	return &interfaces.AgentResponse{Text: response, Success: true}, nil
}

func (o *Orchestrator) SetGoal(ctx context.Context, goal interfaces.Goal) error {
	// Add goal to priority queue/goal manager
	return nil
}

func (o *Orchestrator) GetStatus() interfaces.AgentStatus {
	return o.status
}

func (o *Orchestrator) handleTranscription(event interfaces.Event) {
	text, ok := event.Payload["text"].(string)
	if !ok {
		return
	}

	// Store in working memory
	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:    "user_input",
		Content: text,
	})

	// Process input
	resp, err := o.HandleInput(context.Background(), map[string]interface{}{"text": text})
	if err != nil {
		log.Printf("[Agent] Error handling input: %v", err)
		return
	}

	// Publish response for TTS
	o.bus.Publish(interfaces.Event{
		Type:   "action.tts.request",
		Source: "agent.orchestrator",
		Payload: map[string]interface{}{
			"text": resp.Text,
		},
	})
}

func (o *Orchestrator) handleVision(event interfaces.Event) {
	// Store scene understanding in semantic memory or working memory
}
