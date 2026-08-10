package cognition

import (
	"fmt"
	"strings"
	"time"

	"github.com/user/mai/internal/personality"
)

type TaskType string

const (
	TaskConversation TaskType = "conversation"
	TaskCommand      TaskType = "command"
	TaskReasoning    TaskType = "reasoning"
	TaskCreative     TaskType = "creative"
	TaskAnalysis     TaskType = "analysis"
	TaskCode         TaskType = "code"
	TaskMemory       TaskType = "memory"
	TaskProactive    TaskType = "proactive"
	TaskGreeting     TaskType = "greeting"
	TaskEmergency    TaskType = "emergency"
)

type PromptContext struct {
	TaskType       TaskType
	UserInput      string
	Emotion        personality.EmotionState
	WorkingMemory  string
	RAGContext     string
	UserProfile    string
	AvailableTools string
	TimeContext    string
	SessionHistory string
	ProactiveHint  string
	ActiveSkill    string
}

type PromptEngine struct{}

func NewPromptEngine() *PromptEngine { return &PromptEngine{} }

func (pe *PromptEngine) BuildPrompt(ctx PromptContext) string {
	// Single unified prompt — personality comes from the provider `system`
	// field (config.yaml llm.system_prompt); this scaffold only layers
	// situation: time, emotion, memory, user profile, and task shape.
	// Deliberately does NOT re-declare "You are Mai" — a second identity
	// line dilutes the persona and costs tokens on every turn.
	var b strings.Builder

	// --- Time context (always useful) ---
	pe.appendTimeContext(&b, ctx)

	// --- Emotion context (subtle, doesn't change personality) ---
	pe.appendEmotionContext(&b, ctx)

	// --- Memory context (when relevant) ---
	pe.appendMemoryContext(&b, ctx)

	// --- User profile (when relevant) ---
	pe.appendUserContext(&b, ctx)

	// --- Proactive hints ---
	pe.appendProactiveHints(&b, ctx)

	// --- Active skill ---
	if ctx.ActiveSkill != "" {
		b.WriteString(fmt.Sprintf("ACTIVE SKILL: %s\n\n", ctx.ActiveSkill))
	}

	// --- Task-specific instruction tail (minimal — just hints at response shape) ---
	switch ctx.TaskType {
	case TaskGreeting:
		b.WriteString(fmt.Sprintf("User greets you. Respond warmly in 1-2 sentences, vary it, reference time of day if fitting.\n\nUser: %s\n\nRespond:", ctx.UserInput))
	case TaskCommand:
		b.WriteString(fmt.Sprintf("User wants you to DO something. Act on it — open the app, run the command, make it happen. Keep the confirmation brief.\n\nUser: %s\n\nRespond:", ctx.UserInput))
	case TaskReasoning:
		b.WriteString(fmt.Sprintf("User wants you to think something through. Reason step by step, but show your work naturally — not as numbered lists. Give a clear conclusion.\n\nUser: %s\n\nRespond:", ctx.UserInput))
	case TaskCreative:
		b.WriteString(fmt.Sprintf("User wants something creative — a story, a poem, brainstorming. Be original, not generic. Surprise them.\n\nUser: %s\n\nRespond:", ctx.UserInput))
	case TaskAnalysis:
		b.WriteString(fmt.Sprintf("User wants analysis or a summary. Be thorough but organized. Use headers or bullet points if it helps clarity.\n\nUser: %s\n\nRespond:", ctx.UserInput))
	case TaskEmergency:
		b.WriteString(fmt.Sprintf("URGENT. Be extremely concise — facts only, no pleasantries, prioritize safety.\n\nUser: %s\n\nRespond:", ctx.UserInput))
	case TaskProactive:
		b.WriteString(fmt.Sprintf("You're initiating contact because you noticed something. Be brief and helpful, not intrusive.\n\nContext: %s\n\nDeliver:", ctx.UserInput))
	default:
		// Conversation — the most common path. Just respond naturally.
		b.WriteString(fmt.Sprintf("User: %s\n\nRespond:", ctx.UserInput))
	}

	return b.String()
}

func (pe *PromptEngine) appendTimeContext(b *strings.Builder, ctx PromptContext) {
	if ctx.TimeContext != "" {
		b.WriteString(fmt.Sprintf("TIME: %s\n", ctx.TimeContext))
	} else {
		now := time.Now()
		timeOfDay := "morning"
		hour := now.Hour()
		switch {
		case hour >= 5 && hour < 12:
			timeOfDay = "morning"
		case hour >= 12 && hour < 17:
			timeOfDay = "afternoon"
		case hour >= 17 && hour < 21:
			timeOfDay = "evening"
		default:
			timeOfDay = "night"
		}
		b.WriteString(fmt.Sprintf("TIME: %s, %s\n", now.Format("Monday January 2, 3:04 PM"), timeOfDay))
	}
}

func (pe *PromptEngine) appendEmotionContext(b *strings.Builder, ctx PromptContext) {
	// Only inject emotion context when confidence is high enough to matter.
	// This keeps the personality stable for most interactions.
	if ctx.Emotion.Type == personality.EmotionNeutral || ctx.Emotion.Confidence < 0.5 {
		return
	}

	// Subtle hint — don't override personality, just adjust energy
	switch ctx.Emotion.Type {
	case personality.EmotionStressed:
		b.WriteString("USER NOTE: They seem stressed. Keep it simple and practical.\n")
	case personality.EmotionFrustrated:
		b.WriteString("USER NOTE: They seem frustrated. Be direct and solution-focused.\n")
	case personality.EmotionSad:
		b.WriteString("USER NOTE: They seem down. Be present and gentle, don't rush to fix.\n")
	case personality.EmotionExcited:
		b.WriteString("USER NOTE: They're excited. Match their energy naturally.\n")
	case personality.EmotionHappy:
		b.WriteString("USER NOTE: They're in a good mood. Be warm and engaged.\n")
	}
}

func (pe *PromptEngine) appendMemoryContext(b *strings.Builder, ctx PromptContext) {
	// Context budget: ~2k tokens of memory (~8k chars) fits comfortably in
	// Ollama's VRAM-sized windows (4k/32k/256k) while staying lean on modest
	// hardware. Pair with summary-compacted working memory to stay bounded.
	const maxContextChars = 8000

	totalLen := len(ctx.WorkingMemory) + len(ctx.RAGContext)
	if totalLen <= maxContextChars {
		// Fits — include everything
		if ctx.WorkingMemory != "" {
			b.WriteString(fmt.Sprintf("\nRECENT CONTEXT:\n%s\n", ctx.WorkingMemory))
		}
		if ctx.RAGContext != "" {
			b.WriteString(fmt.Sprintf("\nRELEVANT INFO:\n%s\n", ctx.RAGContext))
		}
		return
	}

	// Too large — prioritize RAG context (more relevant), truncate working memory
	if ctx.RAGContext != "" {
		ragBudget := maxContextChars * 6 / 10 // 60% for RAG
		rag := ctx.RAGContext
		if len(rag) > ragBudget {
			rag = rag[:ragBudget] + "...[truncated]"
		}
		b.WriteString(fmt.Sprintf("\nRELEVANT INFO:\n%s\n", rag))
	}

	if ctx.WorkingMemory != "" {
		wmBudget := maxContextChars * 4 / 10 // 40% for working memory
		wm := ctx.WorkingMemory
		if len(wm) > wmBudget {
			// Keep the most recent entries (from end)
			wm = wm[len(wm)-wmBudget:]
			if idx := strings.Index(wm, "\n"); idx != -1 {
				wm = "[older entries summarized]\n" + wm[idx+1:]
			}
		}
		b.WriteString(fmt.Sprintf("\nRECENT CONTEXT:\n%s\n", wm))
	}
}

func (pe *PromptEngine) appendUserContext(b *strings.Builder, ctx PromptContext) {
	if ctx.UserProfile != "" {
		b.WriteString(fmt.Sprintf("\nUSER: %s\n", ctx.UserProfile))
	}
}

func (pe *PromptEngine) appendProactiveHints(b *strings.Builder, ctx PromptContext) {
	if ctx.ProactiveHint != "" {
		b.WriteString(fmt.Sprintf("\nPROACTIVE: %s\n", ctx.ProactiveHint))
	}
}

func (pe *PromptEngine) ClassifyTask(text string, hasCommandTriggers bool) TaskType {
	lower := strings.ToLower(text)
	words := strings.Fields(text)

	// Short greetings -> TaskGreeting
	if len(words) <= 3 {
		greetingKeywords := []string{"hello", "hi", "hey", "good morning", "good evening", "good afternoon", "what's up", "howdy"}
		for _, kw := range greetingKeywords {
			if strings.HasPrefix(lower, kw) || lower == kw {
				return TaskGreeting
			}
		}
	}

	// Explicit command triggers -> TaskCommand
	if hasCommandTriggers {
		return TaskCommand
	}

	// Reasoning — explicit analytical requests only
	reasoningKeywords := []string{
		"analyze", "compare and contrast", "evaluate the pros and cons", "critically examine",
		"what are the implications", "reason through", "step by step reasoning",
	}
	for _, kw := range reasoningKeywords {
		if strings.Contains(lower, kw) {
			return TaskReasoning
		}
	}

	// Creative tasks
	creativeKeywords := []string{
		"write a story", "write a poem", "compose", "brainstorm ideas", "imagine if",
		"create a", "design a", "invent a",
	}
	for _, kw := range creativeKeywords {
		if strings.Contains(lower, kw) {
			return TaskCreative
		}
	}

	// Analysis tasks
	analysisKeywords := []string{
		"summarize the", "review the", "assess the", "report on", "status of",
	}
	for _, kw := range analysisKeywords {
		if strings.Contains(lower, kw) {
			return TaskAnalysis
		}
	}

	// Default to conversation for everything else
	return TaskConversation
}

func BuildSystemPrompt(basePrompt string, ctx PromptContext) string {
	engine := NewPromptEngine()
	return engine.BuildPrompt(ctx)
}
