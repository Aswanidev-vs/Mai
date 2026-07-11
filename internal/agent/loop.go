package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/user/mai/internal/cognition"
	"github.com/user/mai/internal/memory"
	"github.com/user/mai/internal/personality"
	"github.com/user/mai/internal/skills"
	"github.com/user/mai/pkg/interfaces"
)

type Orchestrator struct {
	bus      interfaces.EventBus
	memory   *memory.Manager
	llm      interfaces.LLMProvider
	registry interfaces.ToolRegistry
	react    *cognition.ReActLoop
	planner  *cognition.Planner
	goals    *GoalManager
	emotion  *personality.EmotionDetector
	meta     *MetaCognition

	promptEngine   *cognition.PromptEngine
	functionCaller *cognition.FunctionCaller
	userModel      *UserModel
	proactive      *ProactiveEngine
	interrupts     *InterruptManager
	prosody        *personality.ProsodyAnalyzer
	ttsAdapter     *personality.TTSAdapter

	status       interfaces.AgentStatus
	cancel       context.CancelFunc
	lastUserTime time.Time
	lastSpoken   string
	lastSpokenAt time.Time

	DirectAction func(text string) (bool, string, error)
	TTSFunc      func(text string, speed float32)

	skillsRunner *skills.Runner
}

func NewOrchestrator(
	bus interfaces.EventBus,
	mem *memory.Manager,
	llm interfaces.LLMProvider,
	registry interfaces.ToolRegistry,
	reactLoop *cognition.ReActLoop,
) *Orchestrator {
	userModel := NewUserModel("data")
	proactive := NewProactiveEngine(userModel)

	skillRegistry := skills.LoadRegistry()
	skillsRunner := skills.NewRunner(skillRegistry, reactLoop, llm, mem)

	return &Orchestrator{
		bus:            bus,
		memory:         mem,
		llm:            llm,
		registry:       registry,
		react:          reactLoop,
		planner:        cognition.NewPlanner(llm),
		goals:          NewGoalManager(),
		emotion:        personality.NewEmotionDetector(),
		meta:           NewMetaCognition(),
		promptEngine:   cognition.NewPromptEngine(),
		functionCaller: cognition.NewFunctionCaller(llm, registry),
		userModel:      userModel,
		proactive:      proactive,
		interrupts:     NewInterruptManager(),
		prosody:        personality.NewProsodyAnalyzer(),
		ttsAdapter:     personality.NewTTSAdapter(1.25, 1.0, 1.0),
		status:         interfaces.StatusIdle,
		skillsRunner:   skillsRunner,
	}
}

func (o *Orchestrator) Start(ctx context.Context) error {
	agentCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel

	o.bus.Subscribe("perception.audio.transcription", o.handleTranscription)
	o.bus.Subscribe("perception.vision.scene", o.handleVision)

	o.interrupts.SetCallbacks(
		func(message string) {
			log.Printf("[Interrupt] Interrupting current task: %s", message)
			o.publishTTS(message)
		},
		func(message string) {
			o.publishTTS(message)
		},
	)

	o.restoreSession()

	proactiveTicker := time.NewTicker(2 * time.Minute)
	improveTicker := time.NewTicker(10 * time.Minute)
	patternTicker := time.NewTicker(5 * time.Minute)

	go func() {
		for {
			select {
			case <-proactiveTicker.C:
				o.proactiveMonitor(agentCtx)
			case <-improveTicker.C:
				o.selfImprove(agentCtx)
			case <-patternTicker.C:
				o.analyzePatterns(agentCtx)
			case <-agentCtx.Done():
				proactiveTicker.Stop()
				improveTicker.Stop()
				patternTicker.Stop()
				return
			}
		}
	}()

	log.Println("[Agent] Orchestrator started with JARVIS-level capabilities")
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

func (o *Orchestrator) analyzePatterns(ctx context.Context) {
	events := o.proactive.AnalyzePatterns()
	for _, event := range events {
		if event.Priority >= 2 {
			log.Printf("[Proactive] Pattern detected: %s", event.Message)
		}
	}
}

func (o *Orchestrator) proactiveMonitor(ctx context.Context) {
	if o.status != interfaces.StatusIdle {
		return
	}

	if !o.lastUserTime.IsZero() && time.Since(o.lastUserTime) > 15*time.Minute {
		hour := time.Now().Hour()
		if hour >= 9 && hour <= 22 {
			if o.goals.GetPendingCount() > 0 {
				o.publishTTS(fmt.Sprintf("You have %d pending tasks. Shall I continue?", o.goals.GetPendingCount()))
				return
			}
		}
	}

	report := o.meta.GetReport()
	if report.TotalActions >= 10 && report.ActionSuccessRate < 0.5 {
		o.publishTTS("I've been struggling with recent tasks. You may want to try rephrasing your commands.")
		return
	}

	if op, ok := report.Operations["handle_input"]; ok {
		avg := op.TotalTime / time.Duration(op.Count)
		if avg > 30*time.Second && op.Count >= 5 {
			log.Printf("[Proactive] High average latency detected: %v", avg)
		}
	}

	events := o.proactive.GetPendingEvents()
	for _, event := range events {
		if event.Priority >= 2 {
			msg := o.proactive.GenerateProactiveMessage(ctx, event)
			o.publishTTS(msg)
			break
		}
	}
}

func (o *Orchestrator) selfImprove(ctx context.Context) {
	analysis := o.meta.AnalyzeStrategy()
	log.Printf("[SelfImprove] %s", analysis)

	report := o.meta.GetReport()
	if report.TotalActions == 0 {
		return
	}

	o.memory.Store(ctx, interfaces.MemoryEntry{
		ID:        fmt.Sprintf("meta_%d", time.Now().Unix()),
		Type:      "meta_analysis",
		Content:   analysis,
		Timestamp: time.Now().Unix(),
		Metadata: map[string]interface{}{
			"success_rate":  report.ActionSuccessRate,
			"total_actions": report.TotalActions,
		},
	})

	if report.ActionSuccessRate < 0.6 && report.TotalActions >= 10 {
		log.Printf("[SelfImprove] Low success rate (%.1f%%) — will prioritize regex parser and ask for clarification", report.ActionSuccessRate*100)
	}
}

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

	if time.Since(o.lastSpokenAt) < 5*time.Second && o.lastSpoken != "" {
		inputLower := strings.ToLower(text)
		if isEcho(inputLower, o.lastSpoken) {
			log.Printf("[Agent] Echo detected — ignoring: %q", text)
			return &interfaces.AgentResponse{Text: "", Success: true}, nil
		}
	}

	o.status = interfaces.StatusThinking
	o.lastUserTime = time.Now()
	startTime := time.Now()
	defer func() {
		o.status = interfaces.StatusIdle
		o.meta.RecordLatency("handle_input", time.Since(startTime))
	}()

	emotionState := o.emotion.DetectFromText(text)
	log.Printf("[Agent] Detected emotion: %s (%.2f)", emotionState.Type, emotionState.Confidence)

	o.userModel.RecordInteraction(text, "")

	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "user_input",
		Content:   text,
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"emotion": string(emotionState.Type)},
	})

	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID:        fmt.Sprintf("user_%d", time.Now().UnixMilli()),
		Type:      "user_input",
		Content:   text,
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"emotion": string(emotionState.Type)},
	})

	o.proactive.RecordAction(text, text)

	if interruptLevel := ClassifyInterrupt(text); interruptLevel >= InterruptHigh {
		o.interrupts.RequestInterrupt(InterruptRequest{
			Level:   interruptLevel,
			Source:  "user",
			Message: text,
		})
	}

	// Companion Skills (Proposal 1): attempt skill routing before command/function execution.
	if o.skillsRunner != nil {
		// Note: runner uses its own matching; if it matches, return immediately.
		if matched, skillResp, err := o.skillsRunner.TryRun(ctx, text, emotionState); err != nil {
			log.Printf("[Skills] Error while executing skill: %v", err)
			// fall back to normal pipeline
		} else if matched && skillResp != "" {
			o.memory.Working().Add(interfaces.MemoryEntry{
				Type:      "assistant_response",
				Content:   skillResp,
				Timestamp: time.Now().Unix(),
			})

			o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
				ID:        fmt.Sprintf("mai_%d", time.Now().UnixMilli()),
				Type:      "assistant_response",
				Content:   skillResp,
				Timestamp: time.Now().Unix(),
			})

			return &interfaces.AgentResponse{Text: o.adaptResponse(skillResp, emotionState), Success: true}, nil
		}
	}

	lowerText := strings.ToLower(text)

	searchPlatforms := []string{"google", "bing", "yahoo", "duckduckgo", "wikipedia", "youtube"}
	isSearchWithoutPlatform := false
	if strings.HasPrefix(lowerText, "search ") || strings.HasPrefix(lowerText, "find ") || strings.HasPrefix(lowerText, "look up ") || strings.HasPrefix(lowerText, "look ") {
		hasPlatform := false
		for _, p := range searchPlatforms {
			if strings.Contains(lowerText, " on "+p) || strings.Contains(lowerText, " using "+p) {
				hasPlatform = true
				break
			}
		}
		if !hasPlatform {
			isSearchWithoutPlatform = true
		}
	}

	if o.DirectAction != nil && !isSearchWithoutPlatform {
		executed, feedback, err := o.DirectAction(text)
		if err != nil {
			o.meta.RecordActionResult(false)
			return &interfaces.AgentResponse{Text: fmt.Sprintf("Error: %v", err), Success: false}, nil
		}
		if executed {
			o.meta.RecordActionResult(true)
			o.userModel.RecordFrequentApp(text)
			log.Printf("[Agent] Executed via regex parser.")
			return &interfaces.AgentResponse{Text: feedback, Success: true}, nil
		}
	} else if isSearchWithoutPlatform {
		log.Printf("[Agent] Search without platform detected — routing to deep_search via ReAct")
		response, err := o.react.Execute(ctx, text)
		if err != nil {
			return nil, err
		}
		return &interfaces.AgentResponse{Text: response, Success: true}, nil
	}

	isLikelyCommand := false
	commandTriggers := []string{"send", "message", "play", "open", "close", "launch", "type", "press", "search", "find", "whatsapp", "youtube", "spotify", "set a", "remind", "schedule"}
	for _, cmd := range commandTriggers {
		if strings.Contains(lowerText, cmd) {
			isLikelyCommand = true
			break
		}
	}

	knowledgeTriggers := []string{"list me", "list the", "top ", "best ", "show me", "recommend", "what are the", "what is the best", "what are some"}
	isKnowledgeRequest := false
	for _, kw := range knowledgeTriggers {
		if strings.Contains(lowerText, kw) {
			isKnowledgeRequest = true
			break
		}
	}
	if isKnowledgeRequest && !isLikelyCommand {
		log.Printf("[Agent] Knowledge request detected, routing to ReAct: %s", text)
		response, err := o.react.Execute(ctx, text)
		if err != nil {
			return nil, err
		}
		return &interfaces.AgentResponse{Text: response, Success: true}, nil
	}

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

	taskType := o.promptEngine.ClassifyTask(text, isLikelyCommand)

	if taskType == cognition.TaskCommand && isLikelyCommand {
		log.Printf("[Agent] Command detected, using function calling: %s", text)
		return o.handleFunctionCall(ctx, text, emotionState)
	}

	reasoningKeywords := []string{
		"invent", "create", "solve", "design", "think", "analyze", "plan",
		"research", "investigate", "calculate", "compare", "evaluate",
		"why is", "how does", "explain", "what if", "summarize", "write",
		"what time", "what date", "what day", "tell me the time", "current time", "what is the date",
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

	log.Printf("[Agent] Conversational input: %s", text)
	return o.handleConversation(ctx, text, emotionState, taskType)
}

func (o *Orchestrator) handleFunctionCall(ctx context.Context, text string, emotion personality.EmotionState) (*interfaces.AgentResponse, error) {
	emotionHint := ""
	if emotion.Type != personality.EmotionNeutral && emotion.Confidence > 0.3 {
		emotionHint = fmt.Sprintf("User appears %s.", emotion.Type)
	}

	output, results, err := o.functionCaller.Execute(ctx, text, emotionHint)
	if err != nil {
		log.Printf("[FunctionCall] Error: %v, falling back to ReAct", err)
		response, reactErr := o.react.Execute(ctx, text)
		if reactErr != nil {
			return nil, reactErr
		}
		return &interfaces.AgentResponse{Text: response, Success: true}, nil
	}

	if len(results) > 0 {
		o.meta.RecordActionResult(results[0].Error == "")
	}

	if output == "" {
		output = "Done."
	}

	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "assistant_response",
		Content:   output,
		Timestamp: time.Now().Unix(),
	})

	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID:        fmt.Sprintf("mai_%d", time.Now().UnixMilli()),
		Type:      "assistant_response",
		Content:   output,
		Timestamp: time.Now().Unix(),
	})

	return &interfaces.AgentResponse{Text: o.adaptResponse(output, emotion), Success: true}, nil
}

func (o *Orchestrator) handleConversation(ctx context.Context, text string, emotion personality.EmotionState, taskType cognition.TaskType) (*interfaces.AgentResponse, error) {
	var contextParts []string

	if wm := o.memory.Working().GetContext(); wm != "" {
		contextParts = append(contextParts, "Recent conversation:\n"+wm)
	}

	if o.memory.RAG() != nil {
		ragResult, err := o.memory.RAG().Query(ctx, text)
		if err == nil && ragResult != nil && ragResult.Answer != "" && ragResult.Confidence > 0.3 {
			contextParts = append(contextParts, "Relevant memory:\n"+ragResult.Answer)
		}
	}

	if procStore, ok := o.memory.Procedural().(*memory.ProceduralStore); ok {
		if pattern, score := procStore.GetBestPattern(text); pattern != "" && score > 0.7 {
			contextParts = append(contextParts, "Learned pattern:\n"+pattern)
		}
	}

	toolsJSON := ""
	if o.registry != nil {
		tools := o.registry.List()
		if len(tools) > 0 {
			toolsData, _ := json.Marshal(tools)
			toolsJSON = string(toolsData)
		}
	}

	promptCtx := cognition.PromptContext{
		TaskType:       taskType,
		UserInput:      text,
		Emotion:        emotion,
		WorkingMemory:  o.memory.Working().GetContext(),
		RAGContext:     "",
		UserProfile:    o.userModel.GetContextString(),
		AvailableTools: toolsJSON,
	}

	if len(contextParts) > 0 {
		promptCtx.RAGContext = strings.Join(contextParts, "\n---\n")
	}

	fullPrompt := o.promptEngine.BuildPrompt(promptCtx)

	response, err := o.llm.Generate(ctx, fullPrompt, interfaces.GenerationOptions{Temperature: 0.4})
	if err != nil {
		return nil, err
	}

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

		log.Printf("[Agent] DirectAction failed for [ACTION] %q, routing to ReAct", actionCmd)
		response, err := o.react.Execute(ctx, actionCmd)
		if err == nil {
			o.meta.RecordActionResult(true)
			return &interfaces.AgentResponse{Text: response, Success: true}, nil
		}
	}

	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "assistant_response",
		Content:   response,
		Timestamp: time.Now().Unix(),
	})

	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID:        fmt.Sprintf("mai_%d", time.Now().UnixMilli()),
		Type:      "assistant_response",
		Content:   response,
		Timestamp: time.Now().Unix(),
	})

	adapted := o.adaptResponse(response, emotion)
	return &interfaces.AgentResponse{Text: adapted, Success: true}, nil
}

func (o *Orchestrator) handleMultiStep(ctx context.Context, text string) (*interfaces.AgentResponse, error) {
	log.Printf("[Agent] Multi-step task detected, engaging planner...")

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
			response, err := o.llm.Generate(ctx, task.Description, interfaces.GenerationOptions{Temperature: 0.4})
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

func (o *Orchestrator) selectRelevantTools(text string) []interfaces.ToolMetadata {
	allTools := o.registry.List()
	lower := strings.ToLower(text)

	var relevant []interfaces.ToolMetadata
	for _, tool := range allTools {
		name := strings.ToLower(tool.Name)
		desc := strings.ToLower(tool.Description)

		if name == "get_system_time" || name == "ui_automation" {
			relevant = append(relevant, tool)
			continue
		}

		keywords := strings.Fields(name)
		for _, kw := range keywords {
			if len(kw) > 2 && strings.Contains(lower, kw) {
				relevant = append(relevant, tool)
				break
			}
		}

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

	if len(relevant) == 0 {
		return allTools
	}

	log.Printf("[Agent] Contextual tool selection: %d/%d tools relevant", len(relevant), len(allTools))
	return relevant
}

func (o *Orchestrator) adaptResponse(response string, emotion personality.EmotionState) string {
	response = cleanResponse(response)

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
	case personality.EmotionSad:
		if !strings.Contains(strings.ToLower(response), "here") {
			return response
		}
	}

	return response
}

func (o *Orchestrator) GetTTSParams(emotion personality.EmotionState, text string) personality.TTSParams {
	isQuestion := strings.Contains(text, "?")
	return o.ttsAdapter.AdaptToContext(emotion, len(text), isQuestion)
}

// SetTTSVoiceStyle applies a voice style preset to the TTS adapter.
// Called at startup to match the system prompt tone.
func (o *Orchestrator) SetTTSVoiceStyle(style string) {
	o.ttsAdapter.SetVoiceStyle(style)
}

func (o *Orchestrator) AnalyzeProsody(samples []float32, sampleRate int) personality.EmotionState {
	features := o.prosody.Analyze(samples, sampleRate)
	return o.prosody.DetectEmotion(features)
}

func (o *Orchestrator) GetUserModel() *UserModel {
	return o.userModel
}

func (o *Orchestrator) GetProactiveEngine() *ProactiveEngine {
	return o.proactive
}

func (o *Orchestrator) GetInterruptManager() *InterruptManager {
	return o.interrupts
}

func (o *Orchestrator) publishTTS(text string) {
	o.lastSpoken = strings.ToLower(text)
	o.lastSpokenAt = time.Now()

	emotion := o.emotion.GetCurrent()
	ttsParams := o.GetTTSParams(emotion, text)

	o.bus.Publish(interfaces.Event{
		Type:   "action.tts.request",
		Source: "agent.orchestrator",
		Payload: map[string]interface{}{
			"text":  text,
			"speed": ttsParams.Speed,
			"pitch": ttsParams.Pitch,
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

	emotion := o.emotion.GetCurrent()
	ttsParams := o.GetTTSParams(emotion, resp.Text)

	o.bus.Publish(interfaces.Event{
		Type:   "action.tts.request",
		Source: "agent.orchestrator",
		Payload: map[string]interface{}{
			"text":  resp.Text,
			"speed": ttsParams.Speed,
			"pitch": ttsParams.Pitch,
		},
	})
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

func isEcho(input, spoken string) bool {
	inputWords := strings.Fields(input)
	spokenWords := strings.Fields(spoken)

	if len(inputWords) == 0 || len(spokenWords) == 0 {
		return false
	}

	spokenSet := make(map[string]bool)
	for _, w := range spokenWords {
		spokenSet[w] = true
	}

	matchCount := 0
	for _, w := range inputWords {
		if spokenSet[w] {
			matchCount++
		}
	}

	matchRatio := float64(matchCount) / float64(len(inputWords))
	return matchRatio > 0.5
}

func cleanResponse(s string) string {
	for {
		start := strings.Index(s, "<")
		end := strings.Index(s, ">")
		if start == -1 || end == -1 || end <= start {
			break
		}
		tag := s[start : end+1]
		if len(tag) > 1 && (tag[1] >= 'a' && tag[1] <= 'z' || tag[1] >= 'A' && tag[1] <= 'Z' || tag[1] == '/') {
			s = s[:start] + s[end+1:]
		} else {
			break
		}
	}

	s = strings.ReplaceAll(s, "```json", "")
	s = strings.ReplaceAll(s, "```", "")

	if idx := strings.Index(s, "[ACTION]"); idx != -1 {
		s = s[:idx]
	}

	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(s)
}
