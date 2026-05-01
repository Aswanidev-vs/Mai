package agent

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/user/mai/internal/cognition"
	"github.com/user/mai/internal/memory"
	"github.com/user/mai/internal/personality"
	"github.com/user/mai/pkg/interfaces"
)

type Orchestrator struct {
	bus       interfaces.EventBus
	memory    *memory.Manager
	llm       interfaces.LLMProvider
	registry  interfaces.ToolRegistry
	react     *cognition.ReActLoop
	planner   *cognition.Planner
	goals     *GoalManager
	emotion   *personality.EmotionDetector
	meta      *MetaCognition

	status       interfaces.AgentStatus
	cancel       context.CancelFunc
	lastUserTime time.Time

	DirectAction func(text string) (bool, string, error)
}

func NewOrchestrator(
	bus interfaces.EventBus,
	mem *memory.Manager,
	llm interfaces.LLMProvider,
	registry interfaces.ToolRegistry,
	reactLoop *cognition.ReActLoop,
) *Orchestrator {
	return &Orchestrator{
		bus:       bus,
		memory:    mem,
		llm:       llm,
		registry:  registry,
		react:     reactLoop,
		planner:   cognition.NewPlanner(llm),
		goals:     NewGoalManager(),
		emotion:   personality.NewEmotionDetector(),
		meta:      NewMetaCognition(),
		status:    interfaces.StatusIdle,
	}
}

func (o *Orchestrator) Start(ctx context.Context) error {
	agentCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel

	o.bus.Subscribe("perception.audio.transcription", o.handleTranscription)
	o.bus.Subscribe("perception.vision.scene", o.handleVision)

	// Restore session continuity: load recent episodic entries into working memory
	o.restoreSession()

	// Proactive monitoring ticker — deterministic, no LLM calls
	proactiveTicker := time.NewTicker(2 * time.Minute)
	// Self-improvement analysis ticker
	improveTicker := time.NewTicker(10 * time.Minute)

	go func() {
		for {
			select {
			case <-proactiveTicker.C:
				o.proactiveMonitor(agentCtx)
			case <-improveTicker.C:
				o.selfImprove(agentCtx)
			case <-agentCtx.Done():
				proactiveTicker.Stop()
				improveTicker.Stop()
				return
			}
		}
	}()

	log.Println("[Agent] Orchestrator started")
	o.status = interfaces.StatusIdle

	<-agentCtx.Done()
	return nil
}

func (o *Orchestrator) Stop() error {
	if o.cancel != nil {
		o.cancel()
	}
	return nil
}

// --- GAP 4: PROACTIVE MONITORING ---
// Deterministic signal-based monitoring. No LLM calls. No hallucination.
func (o *Orchestrator) proactiveMonitor(ctx context.Context) {
	if o.status != interfaces.StatusIdle {
		return
	}

	// Check 1: User has been silent for 15+ minutes during active hours
	if !o.lastUserTime.IsZero() && time.Since(o.lastUserTime) > 15*time.Minute {
		hour := time.Now().Hour()
		if hour >= 9 && hour <= 22 {
			// Check if there are pending goals
			if o.goals.GetPendingCount() > 0 {
				o.publishTTS(fmt.Sprintf("You have %d pending tasks. Shall I continue?", o.goals.GetPendingCount()))
				return
			}
		}
	}

	// Check 2: Action success rate dropped below 50% — warn user
	report := o.meta.GetReport()
	if report.TotalActions >= 10 && report.ActionSuccessRate < 0.5 {
		o.publishTTS("I've been struggling with recent tasks. You may want to try rephrasing your commands.")
		return
	}

	// Check 3: High latency detected
	if op, ok := report.Operations["handle_input"]; ok {
		avg := op.TotalTime / time.Duration(op.Count)
		if avg > 30*time.Second && op.Count >= 5 {
			log.Printf("[Proactive] High average latency detected: %v", avg)
		}
	}
}

// --- GAP 6: SELF-IMPROVEMENT LOOP ---
func (o *Orchestrator) selfImprove(ctx context.Context) {
	analysis := o.meta.AnalyzeStrategy()
	log.Printf("[SelfImprove] %s", analysis)

	report := o.meta.GetReport()
	if report.TotalActions == 0 {
		return
	}

	// Store the analysis as a memory entry for future reference
	o.memory.Store(ctx, interfaces.MemoryEntry{
		ID:        fmt.Sprintf("meta_%d", time.Now().Unix()),
		Type:      "meta_analysis",
		Content:   analysis,
		Timestamp: time.Now().Unix(),
		Metadata: map[string]interface{}{
			"success_rate": report.ActionSuccessRate,
			"total_actions": report.TotalActions,
		},
	})

	// If success rate is low, adjust strategy
	if report.ActionSuccessRate < 0.6 && report.TotalActions >= 10 {
		log.Printf("[SelfImprove] Low success rate (%.1f%%) — will prioritize regex parser and ask for clarification", report.ActionSuccessRate*100)
	}
}

// --- GAP 8: SESSION CONTINUITY ---
func (o *Orchestrator) restoreSession() {
	events, err := o.memory.Episodic().QueryEvents("", 20)
	if err != nil || len(events) == 0 {
		return
	}

	log.Printf("[Session] Restoring %d entries from episodic memory", len(events))
	for _, entry := range events {
		o.memory.Working().Add(entry)
	}
}

func (o *Orchestrator) HandleInput(ctx context.Context, input map[string]interface{}) (*interfaces.AgentResponse, error) {
	text, ok := input["text"].(string)
	if !ok {
		return nil, fmt.Errorf("input must contain 'text' field")
	}

	o.status = interfaces.StatusThinking
	o.lastUserTime = time.Now()
	startTime := time.Now()
	defer func() {
		o.status = interfaces.StatusIdle
		o.meta.RecordLatency("handle_input", time.Since(startTime))
	}()

	// --- EMOTION DETECTION ---
	emotionState := o.emotion.DetectFromText(text)
	log.Printf("[Agent] Detected emotion: %s (%.2f)", emotionState.Type, emotionState.Confidence)

	// --- STORE IN WORKING MEMORY ---
	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "user_input",
		Content:   text,
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"emotion": string(emotionState.Type)},
	})

	// --- PERSIST TO EPISODIC (session continuity) ---
	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID:        fmt.Sprintf("user_%d", time.Now().UnixMilli()),
		Type:      "user_input",
		Content:   text,
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"emotion": string(emotionState.Type)},
	})

	// --- SMART ROUTING ---

	// 1. Try legacy ActionExecutor first (regex — fastest, most reliable)
	if o.DirectAction != nil {
		executed, feedback, err := o.DirectAction(text)
		if err != nil {
			o.meta.RecordActionResult(false)
			return &interfaces.AgentResponse{Text: fmt.Sprintf("Error: %v", err), Success: false}, nil
		}
		if executed {
			o.meta.RecordActionResult(true)
			log.Printf("[Agent] Executed via regex parser.")
			return &interfaces.AgentResponse{Text: feedback, Success: true}, nil
		}
	}

	lowerText := strings.ToLower(text)

	// 2. Determine if this is a command, reasoning task, or conversation
	isLikelyCommand := false
	commandTriggers := []string{"send", "message", "play", "open", "close", "launch", "type", "press", "search", "find", "whatsapp", "youtube", "spotify", "set a", "remind", "schedule"}
	for _, cmd := range commandTriggers {
		if strings.Contains(lowerText, cmd) {
			isLikelyCommand = true
			break
		}
	}

	// Multi-step indicators → use planner
	multiStepIndicators := []string{"and then", "after that", "first", "also", "as well as", "do all", "prep ", "prepare", "set up"}
	isMultiStep := false
	for _, ind := range multiStepIndicators {
		if strings.Contains(lowerText, ind) {
			isMultiStep = true
			break
		}
	}

	if isMultiStep && isLikelyCommand {
		return o.handleMultiStep(ctx, text)
	}

	reasoningKeywords := []string{
		"invent", "create", "solve", "design", "think", "analyze", "plan",
		"research", "investigate", "calculate", "compare", "evaluate",
		"why is", "how does", "explain", "what if", "summarize", "write",
	}

	requiresReasoning := false
	if !isLikelyCommand {
		for _, kw := range reasoningKeywords {
			if strings.Contains(lowerText, kw) {
				requiresReasoning = true
				break
			}
		}
	}

	if requiresReasoning {
		log.Printf("[Agent] Engaging Reasoning Engine: %s", text)
		response, err := o.react.Execute(ctx, text)
		if err != nil {
			return nil, err
		}
		return &interfaces.AgentResponse{Text: o.adaptResponse(response, emotionState), Success: true}, nil
	}

	// 3. Conversational — use RAG + conversation history
	log.Printf("[Agent] Conversational input: %s", text)
	return o.handleConversation(ctx, text, emotionState)
}

func (o *Orchestrator) handleConversation(ctx context.Context, text string, emotion personality.EmotionState) (*interfaces.AgentResponse, error) {
	// Build context from memory
	var contextParts []string

	// Working memory (recent conversation)
	if wm := o.memory.Working().GetContext(); wm != "" {
		contextParts = append(contextParts, "Recent conversation:\n"+wm)
	}

	// RAG retrieval (relevant past knowledge)
	if o.memory.RAG() != nil {
		ragResult, err := o.memory.RAG().Query(ctx, text)
		if err == nil && ragResult != nil && ragResult.Answer != "" && ragResult.Confidence > 0.3 {
			contextParts = append(contextParts, "Relevant memory:\n"+ragResult.Answer)
		}
	}

	// Procedural memory (learned patterns)
	if procStore, ok := o.memory.Procedural().(*memory.ProceduralStore); ok {
		if pattern, score := procStore.GetBestPattern(text); pattern != "" && score > 0.7 {
			contextParts = append(contextParts, "Learned pattern:\n"+pattern)
		}
	}

	// Emotion-aware prompt
	emotionHint := ""
	if emotion.Type != personality.EmotionNeutral && emotion.Confidence > 0.4 {
		emotionHint = fmt.Sprintf("\nUser appears %s. Adapt your tone accordingly.", emotion.Type)
	}

	// Build final prompt
	var fullPrompt string
	if len(contextParts) > 0 {
		fullPrompt = fmt.Sprintf("Context:\n%s\n%s\n\nUser: %s", strings.Join(contextParts, "\n---\n"), emotionHint, text)
	} else {
		fullPrompt = fmt.Sprintf("%s\n\nUser: %s", emotionHint, text)
	}

	// Action escape hatch
	actionPrompt := fmt.Sprintf(`If the user's message is a command (open, play, send, search, etc.), respond ONLY with:
[ACTION] <command>

Examples:
- "[ACTION] play interstellar on brave on youtube"
- "[ACTION] open chrome"
- "[ACTION] send hello to manu on whatsapp"

Otherwise, respond naturally as Mai.%s`, fullPrompt)

	response, err := o.llm.Generate(ctx, actionPrompt, interfaces.GenerationOptions{})
	if err != nil {
		return nil, err
	}

	// Check for [ACTION] escape hatch
	if strings.Contains(response, "[ACTION]") {
		parts := strings.Split(response, "[ACTION]")
		actionCmd := strings.TrimSpace(parts[len(parts)-1])
		if idx := strings.Index(actionCmd, "\n"); idx != -1 {
			actionCmd = strings.TrimSpace(actionCmd[:idx])
		}

		if o.DirectAction != nil {
			executed, feedback, err := o.DirectAction(actionCmd)
			if err == nil && executed {
				o.meta.RecordActionResult(true)
				return &interfaces.AgentResponse{Text: feedback, Success: true}, nil
			}
		}
	}

	// Store response in working memory
	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "assistant_response",
		Content:   response,
		Timestamp: time.Now().Unix(),
	})

	// Persist response to episodic (session continuity)
	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID:        fmt.Sprintf("mai_%d", time.Now().UnixMilli()),
		Type:      "assistant_response",
		Content:   response,
		Timestamp: time.Now().Unix(),
	})

	adapted := o.adaptResponse(response, emotion)
	return &interfaces.AgentResponse{Text: adapted, Success: true}, nil
}

// --- GAP 5: MULTI-STEP EXECUTION ---
func (o *Orchestrator) handleMultiStep(ctx context.Context, text string) (*interfaces.AgentResponse, error) {
	log.Printf("[Agent] Multi-step task detected, engaging planner...")

	// --- GAP 7: CONTEXTUAL TOOL SELECTION ---
	// Filter tools by relevance before sending to planner
	relevantTools := o.selectRelevantTools(text)
	plan, err := o.planner.Decompose(ctx, text, relevantTools)
	if err != nil {
		log.Printf("[Agent] Planning failed, falling back to ReAct: %v", err)
		response, err := o.react.Execute(ctx, text)
		if err != nil {
			return nil, err
		}
		return &interfaces.AgentResponse{Text: response, Success: true}, nil
	}

	log.Printf("[Agent] Plan created with %d steps", len(plan.Root))

	var results []string
	for _, task := range plan.Root {
		log.Printf("[Agent] Executing: %s", task.Description)

		if task.Tool != "" {
			result, err := o.registry.Execute(ctx, task.Tool, task.ToolInput)
			if err != nil {
				results = append(results, fmt.Sprintf("FAILED (%s): %v", task.Description, err))
				o.planner.MarkFailed(plan, task.ID)
				o.meta.RecordActionResult(false)
			} else {
				results = append(results, fmt.Sprintf("DONE (%s): %s", task.Description, result.Output))
				o.planner.MarkCompleted(plan, task.ID)
				o.meta.RecordActionResult(true)
			}
		} else {
			response, err := o.llm.Generate(ctx, task.Description, interfaces.GenerationOptions{})
			if err == nil {
				results = append(results, fmt.Sprintf("DONE (%s): %s", task.Description, response))
				o.planner.MarkCompleted(plan, task.ID)
			}
		}
	}

	summaryPrompt := fmt.Sprintf("Summarize these task results concisely:\n%s", strings.Join(results, "\n"))
	summary, err := o.llm.Generate(ctx, summaryPrompt, interfaces.GenerationOptions{Temperature: 0.3})
	if err != nil {
		summary = fmt.Sprintf("Completed %d/%d steps.", len(results), len(plan.Root))
	}

	return &interfaces.AgentResponse{Text: summary, Success: true}, nil
}

// --- GAP 7: CONTEXTUAL TOOL SELECTION ---
// Filters tools by keyword relevance instead of dumping all tools to the LLM.
func (o *Orchestrator) selectRelevantTools(text string) []interfaces.ToolMetadata {
	allTools := o.registry.List()
	lower := strings.ToLower(text)

	var relevant []interfaces.ToolMetadata
	for _, tool := range allTools {
		name := strings.ToLower(tool.Name)
		desc := strings.ToLower(tool.Description)

		// Always include utility tools
		if name == "get_system_time" || name == "ui_automation" {
			relevant = append(relevant, tool)
			continue
		}

		// Check if tool name or description keywords match the input
		keywords := strings.Fields(name)
		for _, kw := range keywords {
			if len(kw) > 2 && strings.Contains(lower, kw) {
				relevant = append(relevant, tool)
				break
			}
		}

		// Check description keywords (first 5 words)
		descWords := strings.Fields(desc)
		for i, kw := range descWords {
			if i >= 5 {
				break
			}
			if len(kw) > 3 && strings.Contains(lower, kw) {
				relevant = append(relevant, tool)
				break
			}
		}
	}

	// If filtering was too aggressive, return all
	if len(relevant) == 0 {
		return allTools
	}

	log.Printf("[Agent] Contextual tool selection: %d/%d tools relevant", len(relevant), len(allTools))
	return relevant
}

// --- EMOTIONAL ADAPTATION ---
func (o *Orchestrator) adaptResponse(response string, emotion personality.EmotionState) string {
	if emotion.Type == personality.EmotionNeutral || emotion.Confidence < 0.4 {
		return response
	}

	switch emotion.Type {
	case personality.EmotionStressed:
		if len(response) > 200 {
			lines := strings.Split(response, "\n")
			if len(lines) > 3 {
				return strings.Join(lines[:3], "\n")
			}
		}
	case personality.EmotionFrustrated:
		if !strings.Contains(strings.ToLower(response), "understand") && !strings.Contains(strings.ToLower(response), "sorry") {
			return "I understand. " + response
		}
	}

	return response
}

func (o *Orchestrator) publishTTS(text string) {
	o.bus.Publish(interfaces.Event{
		Type:   "action.tts.request",
		Source: "agent.orchestrator",
		Payload: map[string]interface{}{
			"text": text,
		},
	})
}

func (o *Orchestrator) SetGoal(ctx context.Context, goal interfaces.Goal) error {
	o.goals.AddGoal(goal)
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

	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "user_input",
		Content:   text,
		Timestamp: time.Now().Unix(),
	})

	resp, err := o.HandleInput(context.Background(), map[string]interface{}{"text": text})
	if err != nil {
		log.Printf("[Agent] Error handling input: %v", err)
		return
	}

	o.publishTTS(resp.Text)
}

func (o *Orchestrator) handleVision(event interfaces.Event) {
	if scene, ok := event.Payload["description"].(string); ok && scene != "" {
		o.memory.Store(context.Background(), interfaces.MemoryEntry{
			Type:      "vision",
			Content:   scene,
			Timestamp: time.Now().Unix(),
		})
	}
}
