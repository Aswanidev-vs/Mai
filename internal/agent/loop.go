package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/mai/internal/cognition"
	"github.com/user/mai/internal/memory"
	"github.com/user/mai/internal/personality"
	"github.com/user/mai/internal/skills"
	"github.com/user/mai/pkg/interfaces"
)

// chatEntry is one recorded dialogue pair with its recording time, used to
// schedule buffer eviction into conversation lulls.
type chatEntry struct {
	user      string
	assistant string
	at        time.Time
}

type Orchestrator struct {
	bus      interfaces.EventBus
	memory   *memory.Manager
	llm      interfaces.LLMProvider
	registry interfaces.ToolRegistry
	react    *cognition.ReActLoop
	goals    *GoalManager
	emotion  *personality.EmotionDetector
	meta     *MetaCognition

	promptEngine *cognition.PromptEngine
	userModel    *UserModel
	proactive    *ProactiveEngine
	interrupts   *InterruptManager
	prosody      *personality.ProsodyAnalyzer
	ttsAdapter   *personality.TTSAdapter

	genOpts interfaces.GenerationOptions

	turnMu        sync.Mutex
	currentCancel context.CancelFunc // cancels the in-flight LLM stream (barge-in)

	status       interfaces.AgentStatus
	cancel       context.CancelFunc
	lastUserTime time.Time
	lastSpoken   string
	lastSpokenAt time.Time

	// recentUserInputs guards against the TTS→STT loop: if the mic picks up
	// Mai's own voice (or the user's own words echoed back) and it reaches ASR
	// a second time, we drop it instead of speaking it back.
	recentUserInputs []string

	// chatHistory is a verbatim ring buffer of recent user/assistant pairs,
	// sent to the chat API so the model hears the actual dialogue — exact
	// words, pronoun resolution, callbacks. Evicting a pair breaks the KV
	// prefix cache (one expensive re-prefill), so trimming only happens once
	// the evicted-off pair has gone silent — during a lull, never mid
	// rapid-fire dialogue. Working memory keeps the same turns as
	// summary-compressed context for the non-chat paths, and the episodic
	// store keeps them verbatim, so evicted pairs keep living on.
	chatMu         sync.Mutex
	chatHistory    []chatEntry
	chatHistoryMax int // pairs retained; <=0 defaults to 10

	// lastProsody is the most recent voice-derived emotion state, ingested by
	// the audio pipeline (main.go) after each user utterance segment. Merged
	// over text keywords in HandleInput — the voice is the stronger signal.
	prosodyMu     sync.Mutex
	lastProsody   personality.EmotionState
	lastProsodyAt time.Time

	DirectAction func(text string) (bool, string, error)
	TTSFunc      func(text string, speed float32, seq int64)

	// ttsSeq is a monotonic counter tagging every spoken sentence with the
	// turn it belongs to. The TTS player uses it to drop audio from a turn
	// that was superseded by an interruption (production-grade barge-in).
	ttsSeq atomic.Int64

	skillsRunner *skills.Runner
}

func NewOrchestrator(
	bus interfaces.EventBus,
	mem *memory.Manager,
	llm interfaces.LLMProvider,
	registry interfaces.ToolRegistry,
	reactLoop *cognition.ReActLoop,
	genOpts interfaces.GenerationOptions,
) *Orchestrator {
	userModel := NewUserModel("data")
	proactive := NewProactiveEngine(userModel)

	skillRegistry := skills.LoadRegistry()
	skillsRunner := skills.NewRunner(skillRegistry, reactLoop, llm, mem)

	o := &Orchestrator{
		bus:          bus,
		memory:       mem,
		llm:          llm,
		registry:     registry,
		react:        reactLoop,
		goals:        NewGoalManager(),
		emotion:      personality.NewEmotionDetector(),
		meta:         NewMetaCognition(),
		promptEngine: cognition.NewPromptEngine(),
		userModel:    userModel,
		proactive:    proactive,
		interrupts:   NewInterruptManager(),
		prosody:      personality.NewProsodyAnalyzer(),
		ttsAdapter:   personality.NewTTSAdapter(1.25, 1.0, 1.0),
		status:       interfaces.StatusIdle,
		skillsRunner: skillsRunner,
		genOpts:      genOpts,
	}

	// Real context compression: when working memory drops old entries, the
	// LLM summarizes them and the summary is re-injected, so long sessions
	// keep a thread of continuity instead of "[auto-compact: N summarized]".
	if wm, ok := mem.Working().(*memory.WorkingMemory); ok {
		wm.SetOnCompact(o.summarizeDropped)
	}

	// ReAct tool steps are separate LLM calls; re-inject session context
	// (recent conversation, user profile, emotion) on every step so the
	// persona doesn't go blank between tool calls.
	reactLoop.SetContextSnippet(o.sessionContextSnippet)

	return o
}

// sessionContextSnippet builds the compact session context injected into
// every ReAct step prompt: recent conversation, user profile, current emotion.
func (o *Orchestrator) sessionContextSnippet() string {
	const budget = 1400 // chars — keep tool-turn prompts lean on modest hardware

	var b strings.Builder
	if wm := o.memory.Working().GetContext(); wm != "" {
		if len(wm) > budget {
			wm = wm[len(wm)-budget:]
		}
		b.WriteString("RECENT CONVERSATION:\n")
		b.WriteString(wm)
		b.WriteByte('\n')
	}
	if profile := o.userModel.GetContextString(); profile != "" {
		b.WriteString("USER PROFILE:\n")
		b.WriteString(profile)
		b.WriteByte('\n')
	}
	if em := o.emotion.GetCurrent(); em.Type != personality.EmotionNeutral && em.Confidence >= 0.5 {
		b.WriteString("USER'S CURRENT MOOD: ")
		b.WriteString(string(em.Type))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// summarizeDropped compresses working-memory entries that were compacted away
// and re-injects the summary as a fresh entry. Runs on a background goroutine
// from WorkingMemory — never blocks the audio/turn hot path.
func (o *Orchestrator) summarizeDropped(entries []interfaces.MemoryEntry) {
	if o.llm == nil || len(entries) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("Summarize the following conversation history into a compact memory note (2-4 short lines, first person, plain text). Keep the user's name, topics, preferences, emotions, and unfinished tasks. Drop filler.\n\n")
	for _, e := range entries {
		if strings.TrimSpace(e.Content) == "" {
			continue
		}
		fmt.Fprintf(&b, "- [%s] %s\n", e.Type, e.Content)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := o.genOpts
	opts.Temperature = 0.2 // deterministic compression
	summary, err := o.llm.Generate(ctx, b.String(), opts)
	if err != nil {
		log.Printf("[Memory] Summarization failed (keeping marker): %v", err)
		return
	}
	summary = strings.TrimSpace(summary)
	if summary == "" || summary == "..." {
		return
	}
	log.Printf("[Memory] Working-memory compacted: %.100s...", summary)
	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "summary",
		Content:   summary,
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"source": "auto-compact"},
	})
}

func (o *Orchestrator) Start(ctx context.Context) error {
	agentCtx, cancel := context.WithCancel(ctx)
	o.cancel = cancel

	o.bus.Subscribe("perception.audio.transcription", o.handleTranscription)
	o.bus.Subscribe("perception.vision.scene", o.handleVision)

	o.interrupts.SetCallbacks(
		func(message string) {
			// The user's words already reached HandleInput (via
			// handleTranscription) which generates the real reply, so we must
			// NOT parrot them back as TTS. The only job here is to abort the
			// previous turn's in-flight generation so a stale answer doesn't
			// keep streaming after the user takes over.
			log.Printf("[Interrupt] Interrupting current task: %s", message)
			o.InterruptCurrent()
			// Halt any sentence currently being spoken so the new reply (the
			// highest turn sequence) replaces it promptly. The TTS player
			// drops superseded-turn audio but always keeps the newest turn.
			if o.bus != nil {
				o.bus.Publish(interfaces.Event{
					Type:   "agent.interrupt",
					Source: "agent.orchestrator",
				})
			}
		},
		func(message string) {
			// A queued interrupt that was deferred until Mai went idle is a
			// genuine user turn — route it through the normal pipeline for a
			// real response instead of echoing the raw text.
			log.Printf("[Interrupt] Processing queued interrupt: %s", message)
			go o.HandleInput(context.Background(), map[string]interface{}{"text": message})
		},
	)

	go o.restoreSession() // non-blocking: don't delay startup

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

	inputLower := strings.ToLower(text)

	// --- Echo guard ---
	timeSinceTTS := time.Since(o.lastSpokenAt)
	nearTTS := timeSinceTTS < 2*time.Second

	if o.lastSpoken != "" {
		isEchoResult := false
		if nearTTS {
			isEchoResult = isEchoStrict(inputLower, o.lastSpoken)
		} else {
			isEchoResult = isEcho(inputLower, o.lastSpoken)
		}
		if isEchoResult {
			log.Printf("[Agent] Echo detected (nearTTS=%v) — ignoring: %q", nearTTS, text)
			return &interfaces.AgentResponse{Text: "", Success: true}, nil
		}
	}
	for _, prev := range o.recentUserInputs {
		if isEcho(inputLower, prev) {
			log.Printf("[Agent] Repeat input — ignoring: %q", text)
			return &interfaces.AgentResponse{Text: "", Success: true}, nil
		}
	}

	o.recentUserInputs = append(o.recentUserInputs, inputLower)
	if len(o.recentUserInputs) > 5 {
		o.recentUserInputs = o.recentUserInputs[1:]
	}

	// --- Action & Motion request: fire matching body/face motion alongside reply ---
	// Covers both voice and chat, since every turn funnels through HandleInput.
	if act := detectAction(inputLower); act != "" && o.bus != nil {
		if act == "dance" {
			o.bus.Publish(interfaces.Event{
				Type:   "companion.dance",
				Source: "agent.orchestrator",
			})
		}
		o.bus.Publish(interfaces.Event{
			Type:   "companion.action",
			Source: "agent.orchestrator",
			Payload: map[string]interface{}{
				"action":   act,
				"duration": 4.0,
			},
		})
	}

	// --- Greeting fast-path: instant response, no RAG/LLM ---
	if isGreeting(inputLower) {
		resp := pickGreetingResponse(inputLower)
		o.lastUserTime = time.Now()
		o.userModel.RecordInteraction(text, "")
		o.memory.Working().Add(interfaces.MemoryEntry{
			Type: "user_input", Content: text, Timestamp: time.Now().Unix(),
		})
		o.storeResponse(resp, text)
		// Let the normal transcription path publish this response to TTS and
		// the companion UI exactly once. Marking it spoken here made greetings
		// disappear because no audio event was emitted.
		return &interfaces.AgentResponse{Text: resp, Success: true}, nil
	}

	o.status = interfaces.StatusThinking
	o.lastUserTime = time.Now()
	startTime := time.Now()
	defer func() {
		o.status = interfaces.StatusIdle
		o.meta.RecordLatency("handle_input", time.Since(startTime))
	}()

	// --- Emotion & memory recording ---
	emotionState := o.emotion.DetectFromText(text)
	// The user's voice is a stronger emotional signal than their words
	// ("I'm fine" + stressed prosody = stressed). Merge when it's fresh.
	if ps, ok := o.recentProsody(); ok {
		emotionState = o.emotion.MergeProsody(emotionState, ps)
	}
	o.publishEmotion(emotionState)
	o.userModel.RecordInteraction(text, "")
	o.memory.Working().Add(interfaces.MemoryEntry{
		Type: "user_input", Content: text, Timestamp: time.Now().Unix(),
		Metadata: map[string]interface{}{"emotion": string(emotionState.Type)},
	})
	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID: fmt.Sprintf("user_%d", time.Now().UnixMilli()), Type: "user_input",
		Content: text, Timestamp: time.Now().Unix(),
		Metadata: map[string]interface{}{"emotion": string(emotionState.Type)},
	})
	o.proactive.RecordAction(text, text)

	// --- Interrupt handling ---
	if interruptLevel := ClassifyInterrupt(text); interruptLevel >= InterruptHigh {
		o.interrupts.RequestInterrupt(InterruptRequest{
			Level: interruptLevel, Source: "user", Message: text,
		})
	}

	// --- Skills check ---
	if o.skillsRunner != nil {
		if matched, skillResp, err := o.skillsRunner.TryRun(ctx, text, emotionState); err != nil {
			log.Printf("[Skills] Error: %v", err)
		} else if matched && skillResp != "" {
			o.storeResponse(skillResp, text)
			return &interfaces.AgentResponse{Text: o.adaptResponse(skillResp, emotionState), Success: true}, nil
		}
	}

	// --- PATH 1: Regex fast path (imperative commands) ---
	if o.DirectAction != nil {
		executed, feedback, err := o.DirectAction(text)
		if err != nil {
			o.meta.RecordActionResult(false)
			return &interfaces.AgentResponse{Text: fmt.Sprintf("Error: %v", err), Success: false}, nil
		}
		if executed {
			o.meta.RecordActionResult(true)
			o.userModel.RecordFrequentApp(text)
			log.Printf("[Agent] Regex fast path hit.")
			return &interfaces.AgentResponse{Text: feedback, Success: true}, nil
		}
	}

	// --- PATH 2: Route by task type ---
	// Conversational turns (small talk, greeting, storytelling) go through the
	// streaming companion path: persona + memory + emotion in the prompt,
	// sentence-by-sentence TTS so audio starts while the LLM is still talking.
	// Imperative/analytic turns keep using the ReAct tool loop.
	taskType := o.promptEngine.ClassifyTask(text, hasCommandTriggers(text))

	switch taskType {
	case cognition.TaskConversation, cognition.TaskGreeting, cognition.TaskCreative:
		return o.handleConversation(ctx, text, emotionState, taskType)
	}

	response, err := o.react.Execute(ctx, text)
	if err != nil {
		// Fallback: direct conversation if ReAct fails
		log.Printf("[Agent] ReAct failed: %v, falling back to conversation", err)
		return o.handleConversation(ctx, text, emotionState, taskType)
	}

	adapted := o.adaptResponse(response, emotionState)
	o.storeResponse(adapted, text)
	return &interfaces.AgentResponse{Text: adapted, Success: true}, nil
}

// isGreeting reports whether the input is a simple greeting that can be
// answered instantly without LLM inference.
func isGreeting(lower string) bool {
	lower = strings.TrimSpace(lower)
	greetings := []string{
		"hi", "hey", "hello", "yo", "sup", "hiya",
		"good morning", "good afternoon", "good evening", "good night",
		"morning", "afternoon", "evening", "night",
		"what's up", "whats up", "howdy", "howdy-do",
		"hi mai", "hey mai", "hello mai",
	}
	for _, g := range greetings {
		if lower == g {
			return true
		}
	}
	// "good morning/afternoon/evening" with trailing punctuation or name
	for _, g := range []string{"good morning", "good afternoon", "good evening", "good night"} {
		if strings.HasPrefix(lower, g) && len(lower) < len(g)+15 {
			return true
		}
	}
	return false
}

// pickGreetingResponse returns a cached, time-of-day appropriate greeting
// without hitting the LLM.
func pickGreetingResponse(input string) string {
	hour := time.Now().Hour()
	lower := strings.TrimSpace(input)

	// Match specific greetings to natural responses
	switch {
	case strings.Contains(lower, "good morning") || lower == "morning":
		pool := []string{
			"Morning. I'm here.",
			"Good morning. Sleep well?",
			"Morning. What's the plan today?",
		}
		return pool[time.Now().UnixNano()%int64(len(pool))]
	case strings.Contains(lower, "good afternoon") || lower == "afternoon":
		pool := []string{
			"Hey. How's the day going?",
			"Afternoon. I'm with you.",
			"What's on your mind?",
		}
		return pool[time.Now().UnixNano()%int64(len(pool))]
	case strings.Contains(lower, "good evening") || lower == "evening":
		pool := []string{
			"Evening. I'm here.",
			"Hey. What are we doing tonight?",
			"Evening. How was your day?",
		}
		return pool[time.Now().UnixNano()%int64(len(pool))]
	case strings.Contains(lower, "good night") || lower == "night":
		pool := []string{
			"Good night. Get some rest.",
			"Night. I'll be here if you need me.",
			"Sleep well.",
		}
		return pool[time.Now().UnixNano()%int64(len(pool))]
	}

	// Generic greeting pool, rotated by time of day
	var pool []string
	switch {
	case hour >= 5 && hour < 12:
		pool = []string{
			"Hey. What's up?",
			"Morning. What do you need?",
			"Hi. I'm here.",
		}
	case hour >= 12 && hour < 17:
		pool = []string{
			"Hey. What's on your mind?",
			"Hi. What do we have today?",
			"I'm here. What's up?",
		}
	case hour >= 17 && hour < 22:
		pool = []string{
			"Hey. What are we doing?",
			"Evening. I'm with you.",
			"What's up?",
		}
	default:
		pool = []string{
			"Still up? What's on your mind?",
			"Hey. What do you need?",
			"I'm here.",
		}
	}
	return pool[time.Now().UnixNano()%int64(len(pool))]
}

// hasCommandTriggers reports whether the utterance looks like a direct
// command that needs tool execution (ReAct) rather than plain conversation.
func hasCommandTriggers(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	triggers := []string{
		"open ", "launch ", "start ", "stop ", "close ", "quit ",
		"send ", "type ", "press ", "click ",
		"search ", "search for", "look up", "google", "weather", "news",
		"play ", "pause ", "youtube", "download ",
		"create ", "write a file", "save ", "delete ", "rename ", "move ",
		"install ", "run ", "execute ", "compile ", "build ",
		"set a timer", "remind me", "set an alarm", "volume", "brightness",
		"screenshot", "what time", "what's the time",
	}
	for _, t := range triggers {
		if strings.Contains(lower, t) {
			return true
		}
	}
	return false
}

// storeResponse records Mai's response in working and episodic memory, and
// pairs it with the triggering user input in the verbatim chat history so
// non-streaming paths (regex fast path, skills, ReAct) also keep the dialogue
// thread coherent.
func (o *Orchestrator) storeResponse(text string, userTurn ...string) {
	o.memory.Working().Add(interfaces.MemoryEntry{
		Type: "assistant_response", Content: text, Timestamp: time.Now().Unix(),
	})
	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID: fmt.Sprintf("mai_%d", time.Now().UnixMilli()), Type: "assistant_response",
		Content: text, Timestamp: time.Now().Unix(),
	})
	if len(userTurn) > 0 {
		o.recordChatTurn(userTurn[0], text)
	}
}

// SetChatHistoryTurns sets how many verbatim user/assistant pairs are kept and
// sent to the chat API. <=0 keeps the default (6 — sized for an 8k window).
func (o *Orchestrator) SetChatHistoryTurns(pairs int) {
	o.chatMu.Lock()
	defer o.chatMu.Unlock()
	if pairs <= 0 {
		pairs = 6
	}
	o.chatHistoryMax = pairs
}

// recordChatTurn appends one user/assistant pair to the verbatim ring buffer.
// Only a runaway safety trim happens here (2x the max) — normal eviction is
// deferred to chatTurns so it lands after a lull, not during fast dialogue.
func (o *Orchestrator) recordChatTurn(user, assistant string) {
	if strings.TrimSpace(user) == "" || strings.TrimSpace(assistant) == "" {
		return
	}
	o.chatMu.Lock()
	defer o.chatMu.Unlock()
	if o.chatHistoryMax <= 0 {
		o.chatHistoryMax = 10
	}
	o.chatHistory = append(o.chatHistory, chatEntry{
		user: user, assistant: assistant, at: time.Now(),
	})
	if hard := o.chatHistoryMax * 2; len(o.chatHistory) > hard {
		o.chatHistory = o.chatHistory[len(o.chatHistory)-hard:]
	}
}

// timeOfDayLine gives the model basic orientation. It rides inside the last
// user message (the volatile tail of the prompt), never in the stable prefix,
// so the per-minute change cannot break KV-cache reuse across turns.
func (o *Orchestrator) timeOfDayLine() (string, bool) {
	now := time.Now()
	timeOfDay := "night"
	switch hour := now.Hour(); {
	case hour >= 5 && hour < 12:
		timeOfDay = "morning"
	case hour >= 12 && hour < 17:
		timeOfDay = "afternoon"
	case hour >= 17 && hour < 21:
		timeOfDay = "evening"
	}
	return fmt.Sprintf("[time: %s, %s]", now.Format("Monday Jan 2, 3:04 PM"), timeOfDay), true
}

// chatTurns returns the verbatim history as chat messages, trimming pairs that
// have gone idle first. A trim changes the prompt prefix (one KV cache miss,
// ~1–2 s reprefill), so it is scheduled here: only once the oldest pair is
// 90+ seconds old when the buffer is only slightly over — the cost of that
// cache break lands on the first reply of a new conversation segment instead
// of mid rapid-fire exchange. Hard over-limit pairs are trimmed immediately.
func (o *Orchestrator) chatTurns() []interfaces.ChatMessage {
	o.chatMu.Lock()
	defer o.chatMu.Unlock()
	for len(o.chatHistory) > o.chatHistoryMax {
		if len(o.chatHistory) <= o.chatHistoryMax+2 && time.Since(o.chatHistory[0].at) < 90*time.Second {
			break // rapid-fire dialogue — keep the full buffer, cache intact
		}
		o.chatHistory = o.chatHistory[1:]
	}
	out := make([]interfaces.ChatMessage, 0, len(o.chatHistory)*2)
	for _, e := range o.chatHistory {
		out = append(out,
			interfaces.ChatMessage{Role: "user", Content: e.user},
			interfaces.ChatMessage{Role: "assistant", Content: e.assistant},
		)
	}
	return out
}

// clampLen returns n clamped into [0, max].
func clampLen(n, max int) int {
	if n < 0 {
		return 0
	}
	if n > max {
		return max
	}
	return n
}

// firstN returns the first n runes of s for log lines.
func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// recordTruncatedTurn stores an interrupted turn: the user message as sent,
// and the assistant reply truncated to what was actually spoken. Working
// memory, episodic storage, and verbatim chat history all get the truncated
// version, so the next turn's context reflects reality ("she got cut off
// mid-sentence here") instead of a missing turn.
func (o *Orchestrator) recordTruncatedTurn(user string, heard string, chatMessages []interfaces.ChatMessage) {
	o.memory.Working().Add(interfaces.MemoryEntry{
		Type:      "assistant_response",
		Content:   heard,
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"interrupted": true},
	})
	o.memory.Episodic().StoreEvent(interfaces.MemoryEntry{
		ID:        fmt.Sprintf("mai_%d", time.Now().UnixMilli()),
		Type:      "assistant_response",
		Content:   heard,
		Timestamp: time.Now().Unix(),
		Metadata:  map[string]interface{}{"interrupted": true},
	})
	if len(chatMessages) > 0 {
		// The last element is this turn's decorated user message — record the
		// exact bytes the model prefilled so the next turn's prefix still hits
		// the KV cache.
		o.recordChatTurn(chatMessages[len(chatMessages)-1].Content, heard)
	} else {
		o.recordChatTurn(user, heard)
	}
}

func (o *Orchestrator) handleConversation(ctx context.Context, text string, emotion personality.EmotionState, taskType cognition.TaskType) (*interfaces.AgentResponse, error) {
	lowerText := strings.ToLower(text)
	if strings.TrimSpace(text) == "" {
		return &interfaces.AgentResponse{Text: "", Success: true}, nil
	}
	var contextParts []string

	wm := o.memory.Working().GetContext()

	// Skip RAG and procedural lookups only for genuinely trivial short
	// questions (greetings, "what is X" encyclopedic asks). Correctness: the
	// old code set isSimpleQuestion = wordCount < 15 and never turned it off,
	// so NO short turn ever retrieved memory. Now a short *statement* that is
	// a real request ("I feel terrible about tomorrow's interview") does pull
	// history, which is where companionship lives.
	wordCount := len(strings.Fields(text))
	simpleQuestionPrefixes := []string{"what ", "how ", "when ", "where ", "who ", "is ", "are ", "can ", "do ", "does ", "have "}
	isSimpleQuestion := wordCount < 3
	if wordCount < 15 && !isSimpleQuestion {
		for _, p := range simpleQuestionPrefixes {
			if strings.HasPrefix(lowerText, p) {
				isSimpleQuestion = true
				break
			}
		}
	}
	needsContext := wordCount >= 5 && !isSimpleQuestion
	if needsContext {
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
	}

	// Real-time/entity grounding: the user added web_research yesterday — use
	// it whenever the turn needs external, up-to-date, or named-entity
	// knowledge, so Mai answers from the web instead of confabulating.
	if facts := o.lookupFacts(ctx, lowerText); facts != "" {
		contextParts = append(contextParts,
			"Web search results (answer from these; do not guess beyond them):\n"+facts)
	}

	var fullPrompt string
	var chatMessages []interfaces.ChatMessage
	useChat := false
	if cs, ok := o.llm.(interfaces.ChatStreamer); ok && cs != nil {
		// Verbatim chat path: the model hears the actual dialogue (exact
		// words, pronoun resolution, callbacks). The message prefix is
		// append-only and byte-stable turn-over-turn, so llama-server reuses
		// its KV cache and only the new tail is prefilled (~270 ms measured).
		//
		// Working memory is deliberately NOT injected here: it is a summary
		// that changes every turn and would break the cache prefix, while the
		// history already carries the recent dialogue verbatim. Long-range
		// recall still rides RAG (relevant ingredients below) and episodic
		// storage.
		useChat = true
		chatMessages = o.chatTurns()

		var userContent strings.Builder
		if profile, _ := o.timeOfDayLine(); profile != "" {
			userContent.WriteString(profile)
			userContent.WriteByte('\n')
		}
		if o.userModel != nil {
			if profile := o.userModel.GetContextString(); profile != "" {
				userContent.WriteString("USER PROFILE:\n" + profile + "\n\n")
			}
		}
		if len(contextParts) > 0 {
			userContent.WriteString(strings.Join(contextParts, "\n---\n") + "\n\n")
		}
		if taskType != cognition.TaskConversation {
			userContent.WriteString(fmt.Sprintf("[task: %s]\n", taskType))
		}
		hint := o.promptEngine.NoteForEmotion(emotion)
		if hint != "" {
			userContent.WriteString(hint)
		}
		userContent.WriteString(text)
		// Per-turn persona anchor. Small models drift back to assistant mode
		// late in long chats even with a strong system prompt; a short
		// imperative at the tail of each user message keeps the voice steady.
		// Constant across turns, so it never breaks the KV prefix cache.
		userContent.WriteString("\n\n(reply as Mai: plain spoken sentences, no lists, no markdown, no emojis; vary your rhythm, use contractions, and add a brief spoken reaction like 'hmm' or 'oh' only when it genuinely fits; never use stage directions)")
		userContent.WriteString("\nAnswer only what's being asked right now. Never invent names, titles, or facts; if you're unsure, say so briefly.")
		chatMessages = append(chatMessages, interfaces.ChatMessage{Role: "user", Content: userContent.String()})
	} else {
		// Flat-prompt fallback (non-chat providers): keeps the legacy layout.
		promptCtx := cognition.PromptContext{
			TaskType:       taskType,
			UserInput:      text,
			Emotion:        emotion,
			WorkingMemory:  wm, // already computed above — don't rebuild it
			RAGContext:     "",
			UserProfile:    "",
			AvailableTools: "",
		}
		if len(contextParts) > 0 {
			promptCtx.RAGContext = strings.Join(contextParts, "\n---\n")
		}
		// The persona ("behaves like someone who has known the user a long time")
		// only works when the profile is actually present. Inject it every turn —
		// it's compact (topics, frequent apps, preferences).
		promptCtx.UserProfile = o.userModel.GetContextString()
		fullPrompt = o.promptEngine.BuildPrompt(promptCtx)
	}

	// Stream the reply token-by-token and speak each completed sentence as it
	// is produced, so the first audio starts long before the full answer exists.
	ctx, cancel := context.WithCancel(ctx)
	o.setTurnCancel(cancel)
	defer func() {
		cancel()
		o.setTurnCancel(nil)
	}()

	var full strings.Builder
	var pending strings.Builder
	var flushedSentences []string // complete sentences already sent to TTS
	// spokenChars tracks text already handed to TTS — approximates what the
	// user will actually hear. On barge-in it drives truncation: only what
	// was spoken is kept in conversation history, like OpenAI realtime's
	// conversation.item.truncate.
	var spokenChars int

	onChunk := func(chunk string) {
		full.WriteString(chunk)
		pending.WriteString(chunk)
		// The spoken transcript is the single transcript source: publishTTS
		// below emits each cleaned sentence, which is what the browser
		// (and the lip-sync schedule) must match against the real audio.
		// Publishing raw tokens here too would double every sentence in the
		// transcript and make the viseme schedule 2x the true audio length.
		for {
			seg, ok := takeSentence(&pending)
			if !ok {
				break
			}
			sentence := cleanResponse(seg)
			if strings.TrimSpace(sentence) != "" {
				// Strip hedging BEFORE TTS so what she says matches the
				// transcript ("I'm not sure, but..." never reaches the mic).
				spoken := stripHallucinationHedging(sentence)
				spokenChars += len(spoken)
				flushedSentences = append(flushedSentences, spoken)
				o.publishTTS(spoken)
			}
		}
	}

	var streamErr error
	if useChat {
		streamErr = o.llm.(interfaces.ChatStreamer).StreamChat(ctx, chatMessages, o.genOpts, onChunk)
	} else {
		streamErr = o.llm.Stream(ctx, fullPrompt, o.genOpts, onChunk)
	}
	if streamErr != nil && ctx.Err() == nil {
		// Finalize the browser transcript even on failure so the companion
		// UI doesn't stay stuck in streaming state.
		if o.bus != nil {
			o.bus.Publish(interfaces.Event{
				Type:   "chat.response",
				Source: "agent.orchestrator",
				Payload: map[string]interface{}{
					"text": "",
					"done": true,
				},
			})
		}
		return nil, streamErr
	}
	if ctx.Err() != nil {
		// Interrupted mid-answer (barge-in). ChatGPT-Voice semantics: the
		// assistant turn still enters history, truncated to what was actually
		// heard. That keeps her context coherent — she "remembers" saying the
		// first part and continues naturally, instead of the turn silently
		// vanishing as if she never started.
		heard := strings.TrimSpace(full.String()[:clampLen(spokenChars, full.Len())])
		if heard == "" {
			heard = strings.TrimSpace(strings.Join(flushedSentences, " "))
		}
		if heard != "" {
			o.recordTruncatedTurn(text, heard, chatMessages)
			log.Printf("[Agent] Interrupted after %q — history keeps the heard portion", firstN(heard, 60))
		}
		// Finalize the browser transcript so a barge-in doesn't leave the
		// companion UI stuck in streaming state.
		if o.bus != nil {
			o.bus.Publish(interfaces.Event{
				Type:   "chat.response",
				Source: "agent.orchestrator",
				Payload: map[string]interface{}{
					"text": "",
					"done": true,
				},
			})
		}
		return &interfaces.AgentResponse{Text: strings.TrimSpace(full.String()), Success: true, Spoken: false}, nil
	}
	// Flush any trailing text that didn't end on a sentence boundary.
	if rem := cleanResponse(pending.String()); strings.TrimSpace(rem) != "" {
		o.publishTTS(stripHallucinationHedging(rem))
	}
	// Signal stream completion so the browser companion finalizes the transcript.
	if o.bus != nil {
		o.bus.Publish(interfaces.Event{
			Type:   "chat.response",
			Source: "agent.orchestrator",
			Payload: map[string]interface{}{
				"text": "",
				"done": true,
			},
		})
	}

	response := full.String()
	if strings.TrimSpace(response) == "" {
		response = "..."
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
				return &interfaces.AgentResponse{Text: feedback, Success: true, Spoken: true}, nil
			}
		}

		log.Printf("[Agent] DirectAction failed for [ACTION] %q, routing to ReAct", actionCmd)
		reactResp, err := o.react.Execute(ctx, actionCmd)
		if err == nil {
			o.meta.RecordActionResult(true)
			return &interfaces.AgentResponse{Text: reactResp, Success: true, Spoken: true}, nil
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
	if useChat {
		// Record what was actually SENT (the decorated user message) so the
		// history prefix next turn matches what this turn prefilled — that is
		// what makes the KV cache hit byte-for-byte instead of re-prefilling
		// the previous pair every turn.
		o.recordChatTurn(chatMessages[len(chatMessages)-1].Content, adapted)
	} else {
		o.recordChatTurn(text, adapted)
	}
	return &interfaces.AgentResponse{Text: adapted, Success: true, Spoken: true}, nil
}

// lookupFacts runs the web_research tool for turns that need external,
// up-to-date, or entity-specific knowledge, so Mai grounds her answer instead
// of confabulating. Returns "" when the turn doesn't need a lookup, the tool
// isn't registered, or the search produced nothing.
func (o *Orchestrator) lookupFacts(ctx context.Context, lowerText string) string {
	if o.registry == nil || !needsWebLookup(lowerText) {
		return ""
	}
	params, err := json.Marshal(map[string]string{
		"query": strings.TrimSpace(lowerText),
	})
	if err != nil {
		return ""
	}
	result, err := o.registry.Execute(ctx, "web_research", params)
	if err != nil || result.Error != nil || strings.TrimSpace(result.Output) == "" {
		return ""
	}
	log.Printf("[Agent] web_research grounded the answer for: %.60s", lowerText)
	return strings.TrimSpace(result.Output)
}

// needsWebLookup reports whether a turn should consult the web before answering:
// factual entity questions and current/time-sensitive topics the small local
// model can't verify. Social/personal questions are excluded so small talk
// stays instant.
func needsWebLookup(lower string) bool {
	lower = strings.ToLower(strings.TrimSpace(lower))
	for _, social := range []string{
		"on your mind", "you thinking", "how are you", "what do you",
		"you love", "you like", "are you", "do you like", "what about you",
		"you feel", "you mad", "you sad", "you happy", "you doing",
	} {
		if strings.Contains(lower, social) {
			return false
		}
	}
	for _, kw := range []string{
		"trending", "latest", "current", "right now", "as of", "today",
		"news", "this week", "this month", "this year", "released",
		"came out", "score", "weather", "top song", "popular in", "in vogue",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	for _, p := range []string{
		"who is", "who's", "who was", "who played", "who plays",
		"what is", "what's", "what are", "what does", "which ",
		"where is", "when did", "when was", "tell me about",
		"look up", "search for", "google ",
	} {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// wantsDance reports whether the user asked Mai to dance.
func wantsDance(lower string) bool {
	for _, kw := range []string{"dance", "dancing", "bust a move", "do a jig", "groove"} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// detectAction checks whether the user explicitly requested an action/motion.
// Returns the action key or empty string if no action intent is detected.
func detectAction(lower string) string {
	if wantsDance(lower) {
		return "dance"
	}

	type actionRule struct {
		name     string
		keywords []string
	}

	rules := []actionRule{
		{
			name: "crying",
			keywords: []string{
				"cry", "crying", "shed a tear", "weep", "sob",
			},
		},
		{
			name: "pouting",
			keywords: []string{
				"pout", "pouting", "sulking",
			},
		},
		{
			name: "blushing",
			keywords: []string{
				"blush", "blushing", "turn red",
			},
		},
		{
			name: "flustered",
			keywords: []string{
				"fluster", "flustered", "get shy", "act shy",
			},
		},
		{
			name: "depression",
			keywords: []string{
				"depress", "depressed", "depression", "slump", "despair", "look down",
			},
		},
		{
			name: "smile",
			keywords: []string{
				"smile", "smiling", "give me a smile", "show me a smile", "grin",
			},
		},
		{
			name: "happy",
			keywords: []string{
				"be happy", "look happy", "cheer up", "act happy", "show happiness",
			},
		},
		{
			name: "sad",
			keywords: []string{
				"be sad", "look sad", "frown", "act sad",
			},
		},
		{
			name: "angry",
			keywords: []string{
				"get angry", "be angry", "look angry", "act angry", "rage",
			},
		},
		{
			name: "surprised",
			keywords: []string{
				"look surprised", "be surprised", "act surprised", "gasp",
			},
		},
		{
			name: "thinking",
			keywords: []string{
				"think about it", "ponder", "hmm", "let me think", "wonder",
			},
		},
		{
			name: "wave",
			keywords: []string{
				"wave", "waving", "say bye", "wave goodbye", "wave hello", "wave at me",
			},
		},
		{
			name: "nod",
			keywords: []string{
				"nod", "nodding", "nod your head",
			},
		},
		{
			name: "headshake",
			keywords: []string{
				"shake your head", "shake head", "headshake",
			},
		},
	}

	for _, rule := range rules {
		for _, kw := range rule.keywords {
			if strings.Contains(lower, kw) {
				return rule.name
			}
		}
	}
	return ""
}

// takeSentence extracts the next complete sentence from buf, mutating buf to
// remove what it took. A sentence ends at .!? or a newline; if the buffer grows
// past maxLen without a boundary it cuts at the last clause break. Returns
// ("", false) when no sentence is ready yet.
func takeSentence(buf *strings.Builder) (string, bool) {
	s := buf.String()
	const maxLen = 100

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '!' || c == '?' || c == '\n' {
			return consumeSentence(buf, s, i+1)
		}
		if c == '.' {
			// Keep ellipses together so a thoughtful pause is one spoken chunk,
			// rather than three tiny sentences sent to TTS separately.
			if i+1 < len(s) && s[i+1] == '.' {
				continue
			}
			// Don't split on common abbreviations like "Dr." or "e.g.".
			if i > 0 && i+1 < len(s) && isLowerByte(s[i-1]) && s[i+1] == ' ' {
				continue
			}
			return consumeSentence(buf, s, i+1)
		}
	}

	if len(s) >= maxLen {
		cut := strings.LastIndex(s[:maxLen], ",")
		if cut < 0 {
			cut = strings.LastIndex(s[:maxLen], ";")
		}
		if cut < 0 {
			cut = strings.LastIndex(s[:maxLen], " ")
		}
		if cut < 0 {
			cut = maxLen
		}
		return consumeSentence(buf, s, cut+1)
	}

	// Early flush at 60 chars on clause breaks (comma/semicolon + space) to
	// reduce first-audio latency without waiting for the full maxLen.
	if len(s) >= 60 {
		for i := len(s) - 1; i >= 50; i-- {
			if (s[i] == ',' || s[i] == ';') && i+1 < len(s) && s[i+1] == ' ' {
				return consumeSentence(buf, s, i+2)
			}
		}
	}

	return "", false
}

func consumeSentence(buf *strings.Builder, s string, end int) (string, bool) {
	rem := strings.TrimLeft(s[end:], " \n\t")
	buf.Reset()
	buf.WriteString(rem)
	return strings.TrimRight(s[:end], " \n\t"), true
}

func isLowerByte(b byte) bool { return b >= 'a' && b <= 'z' }

func (o *Orchestrator) adaptResponse(response string, emotion personality.EmotionState) string {
	response = cleanResponse(response)

	// Strip common hallucination hedging that adds no value
	response = stripHallucinationHedging(response)

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

// stripHallucinationHedging removes low-confidence hedging phrases that
// make responses sound uncertain and unhelpful.
func stripHallucinationHedging(s string) string {
	hedges := []struct {
		old string
		new string
	}{
		{"I'm not sure, but ", ""},
		{"I think maybe ", ""},
		{"I believe perhaps ", ""},
		{"It's possible that ", ""},
		{"It seems like ", ""},
		{"I'm not certain, but ", ""},
		{"I don't have confirmed information, but ", ""},
		{"According to my training data, ", ""},
		{"As far as I know, ", ""},
		{"If I recall correctly, ", ""},
	}
	for _, h := range hedges {
		s = strings.Replace(s, h.old, h.new, 1)
	}
	return s
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

// SetTTSBaseSpeed overrides the baseline speech rate (warmth), applied after voice style.
func (o *Orchestrator) SetTTSBaseSpeed(speed float32) {
	o.ttsAdapter.SetBaseSpeed(speed)
}

// setTurnCancel stores the cancel func for the current generation turn so a
// barge-in can abort the in-flight LLM stream.
func (o *Orchestrator) setTurnCancel(c context.CancelFunc) {
	o.turnMu.Lock()
	defer o.turnMu.Unlock()
	o.currentCancel = c
}

// InterruptCurrent cancels the in-flight LLM stream (called on barge-in).
func (o *Orchestrator) InterruptCurrent() {
	o.turnMu.Lock()
	c := o.currentCancel
	o.turnMu.Unlock()
	if c != nil {
		c()
	}
}

func (o *Orchestrator) AnalyzeProsody(samples []float32, sampleRate int) personality.EmotionState {
	features := o.prosody.Analyze(samples, sampleRate)
	return o.prosody.DetectEmotion(features)
}

// IngestProsody records a voice-derived emotion state, produced by
// AnalyzeProsody in the audio pipeline after a user utterance segment ends.
func (o *Orchestrator) IngestProsody(state personality.EmotionState) {
	o.prosodyMu.Lock()
	defer o.prosodyMu.Unlock()
	o.lastProsody = state
	o.lastProsodyAt = time.Now()
}

// recentProsody returns the last voice-derived emotion if it is recent enough
// to belong to the current turn (< 3s — matches the utterance-to-ASR window).
func (o *Orchestrator) recentProsody() (personality.EmotionState, bool) {
	o.prosodyMu.Lock()
	defer o.prosodyMu.Unlock()
	if o.lastProsodyAt.IsZero() || time.Since(o.lastProsodyAt) > 3*time.Second {
		return personality.EmotionState{}, false
	}
	return o.lastProsody, true
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

// publishEmotion lets the companion choose an empathetic response expression
// from the user's current mood. The browser maps user frustration/stress to a
// calm face instead of mirroring it as anger.
func (o *Orchestrator) publishEmotion(state personality.EmotionState) {
	if o.bus == nil {
		return
	}
	_ = o.bus.Publish(interfaces.Event{
		Type:   "emotion.detected",
		Source: "agent.orchestrator",
		Payload: map[string]interface{}{
			"emotion":   string(state.Type),
			"intensity": state.Confidence,
		},
	})
}

func (o *Orchestrator) publishTTS(text string) {
	seq := o.ttsSeq.Add(1)
	o.lastSpoken = strings.ToLower(text)
	o.lastSpokenAt = time.Now()

	// Terminal transcript of everything Mai says (streamed sentences,
	// greetings, proactive messages). The live path otherwise produces no
	// console output for the response.
	log.Printf("[Mai] %s", text)

	emotion := o.emotion.GetCurrent()
	ttsParams := o.GetTTSParams(emotion, text)

	// Always publish a transcript event so the browser companion gets
	// progressive text even when TTSFunc is wired (bypassing the bus for audio).
	if o.bus != nil {
		o.bus.Publish(interfaces.Event{
			Type:   "chat.response",
			Source: "agent.orchestrator",
			Payload: map[string]interface{}{
				"text": text,
				"done": false,
			},
		})
	}

	// If a direct TTS sink is wired (streaming sentence queue), use it so the
	// bus subscribers don't double-play. Otherwise fall back to the event bus.
	if o.TTSFunc != nil {
		o.TTSFunc(text, ttsParams.Speed, seq)
		return
	}

	o.bus.Publish(interfaces.Event{
		Type:   "action.tts.request",
		Source: "agent.orchestrator",
		Payload: map[string]interface{}{
			"text":  text,
			"speed": ttsParams.Speed,
			"pitch": ttsParams.Pitch,
			"seq":   seq,
		},
	})
}

func (o *Orchestrator) SetGoal(ctx context.Context, goal interfaces.Goal) error {
	o.goals.AddGoal(goal)
	return nil
}

// Speak streams text to TTS, tagging it with the orchestrator's monotonic
// turn sequence so the player can drop it if a newer turn supersedes it.
func (o *Orchestrator) Speak(text string) {
	o.publishTTS(text)
}

func (o *Orchestrator) GetStatus() interfaces.AgentStatus {
	return o.status
}

func (o *Orchestrator) handleTranscription(event interfaces.Event) {
	text, ok := event.Payload["text"].(string)
	if !ok {
		return
	}

	// NOTE: no Working().Add here — HandleInput records the user input once
	// (working + episodic). Adding again here would double every voice turn
	// in the ring and trigger compaction prematurely.
	resp, err := o.HandleInput(context.Background(), map[string]interface{}{"text": text})
	if err != nil {
		log.Printf("[Agent] Error handling input: %v", err)
		return
	}

	// If the handler already streamed the reply to TTS (Spoken), don't repeat it.
	if !resp.Spoken {
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

// echoWordSet is a stack-allocated set for echo detection.
// Avoids heap allocation for the common case (< 64 words).
type echoWordSet struct {
	small [16]string // stack-allocated for small sets
	big   map[string]bool
	n     int
}

func newEchoWordSet(words []string) echoWordSet {
	s := echoWordSet{n: len(words)}
	if len(words) <= 16 {
		copy(s.small[:], words)
	} else {
		s.big = make(map[string]bool, len(words))
		for _, w := range words {
			s.big[w] = true
		}
	}
	return s
}

func (s *echoWordSet) has(word string) bool {
	if s.big != nil {
		return s.big[word]
	}
	for i := 0; i < s.n && i < 16; i++ {
		if s.small[i] == word {
			return true
		}
	}
	return false
}

func isEcho(input, spoken string) bool {
	inputWords := strings.Fields(input)
	spokenWords := strings.Fields(spoken)

	if len(inputWords) == 0 || len(spokenWords) == 0 {
		return false
	}

	if len(inputWords) <= 1 {
		return false
	}

	spokenSet := newEchoWordSet(spokenWords)

	matchCount := 0
	for _, w := range inputWords {
		if spokenSet.has(w) {
			matchCount++
		}
	}

	matchRatio := float64(matchCount) / float64(len(inputWords))
	return matchRatio > 0.6 && len(inputWords) <= len(spokenWords)
}

func isEchoStrict(input, spoken string) bool {
	inputWords := strings.Fields(input)
	spokenWords := strings.Fields(spoken)

	if len(inputWords) == 0 || len(spokenWords) == 0 {
		return false
	}

	spokenSet := newEchoWordSet(spokenWords)

	matchCount := 0
	for _, w := range inputWords {
		if spokenSet.has(w) {
			matchCount++
		}
	}

	matchRatio := float64(matchCount) / float64(len(inputWords))
	return matchRatio > 0.4
}

func cleanResponse(s string) string {
	// Strip XML-like tags (thought tags, etc.)
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

	// Strip markdown code fences
	s = strings.ReplaceAll(s, "```json", "")
	s = strings.ReplaceAll(s, "```", "")

	// Strip [ACTION] markers and everything after
	if idx := strings.Index(s, "[ACTION]"); idx != -1 {
		s = s[:idx]
	}

	// Strip common LLM artifacts
	s = strings.ReplaceAll(s, "Here is the JSON:", "")
	s = strings.ReplaceAll(s, "Here is the response:", "")
	s = strings.ReplaceAll(s, "Here's the response:", "")
	s = strings.ReplaceAll(s, "Here's my answer:", "")
	s = strings.ReplaceAll(s, "Here is my answer:", "")
	s = strings.ReplaceAll(s, "Here's what I found:", "")
	s = strings.ReplaceAll(s, "Here is what I found:", "")

	// Strip JSON preamble if the response starts with { but has leading text
	s = strings.TrimSpace(s)
	if idx := strings.IndexByte(s, '{'); idx > 0 {
		preamble := strings.TrimSpace(s[:idx])
		// Only strip if preamble looks like "Here is..." or similar
		if strings.HasPrefix(strings.ToLower(preamble), "here") ||
			strings.HasPrefix(strings.ToLower(preamble), "the ") ||
			strings.HasPrefix(strings.ToLower(preamble), "this ") {
			s = s[idx:]
		}
	}

	// Spoken-voice scrub: small models occasionally drift into assistant-style
	// formatting (bullets, headings, emoji) despite the persona prompt. Strip
	// it here so even a drifted reply never reaches the voice as lists or
	// unpronounceable glyphs.
	s = stripFormattingForVoice(s)

	// Collapse excessive newlines
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}

	return strings.TrimSpace(s)
}

// stripFormattingForVoice removes markdown-ish artifacts a drifting model may
// emit: heading hashes, bullet/numbered-list markers, bold markers, emoji.
func stripFormattingForVoice(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		t = strings.TrimLeft(t, "# ")
		for _, prefix := range []string{"* ", "- ", "• "} {
			t = strings.TrimPrefix(t, prefix)
		}
		if dot := strings.Index(t, ". "); dot > 0 && dot <= 3 {
			digits := true
			for _, c := range t[:dot] {
				if c < '0' || c > '9' {
					digits = false
					break
				}
			}
			if digits {
				t = t[dot+2:]
			}
		}
		lines[i] = strings.ReplaceAll(t, "**", "")
	}
	s = strings.Join(lines, "\n")

	var b strings.Builder
	for _, r := range s {
		// Emoji blocks, dingbats/symbols, variation selector and ZWJ.
		if r >= 0x1F000 && r <= 0x1FAFF || r >= 0x2600 && r <= 0x27BF ||
			r == 0xFE0F || r == 0x200D {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
