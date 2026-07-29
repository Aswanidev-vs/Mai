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

type PromptEngine struct {
	personalityName string
}

func NewPromptEngine() *PromptEngine {
	return &PromptEngine{
		personalityName: "Mai",
	}
}

func (pe *PromptEngine) BuildPrompt(ctx PromptContext) string {
	switch ctx.TaskType {
	case TaskConversation:
		return pe.buildConversationPrompt(ctx)
	case TaskCommand:
		return pe.buildCommandPrompt(ctx)
	case TaskReasoning:
		return pe.buildReasoningPrompt(ctx)
	case TaskCreative:
		return pe.buildCreativePrompt(ctx)
	case TaskAnalysis:
		return pe.buildAnalysisPrompt(ctx)
	case TaskProactive:
		return pe.buildProactivePrompt(ctx)
	case TaskGreeting:
		return pe.buildGreetingPrompt(ctx)
	case TaskEmergency:
		return pe.buildEmergencyPrompt(ctx)
	default:
		return pe.buildConversationPrompt(ctx)
	}
}

func (pe *PromptEngine) buildConversationPrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s. Speak and act exactly as the companion persona defined in your system instructions — do not adopt a different identity.

CRITICAL RULES — ANTI-HALLUCINATION:
- ONLY use information that is explicitly provided in the context below
- If you don't know something, say "I'm not sure about that" — never make up facts
- If a tool result is provided, base your answer ONLY on that result, not on your training data
- Never invent dates, statistics, names, or technical details that aren't in the context
- If the user asks about something you can't verify, say so honestly

Here is the current interaction context:

`, pe.personalityName))

	pe.appendTimeContext(&b, ctx)
	pe.appendEmotionContext(&b, ctx)
	pe.appendMemoryContext(&b, ctx)
	pe.appendUserContext(&b, ctx)
	pe.appendProactiveHints(&b, ctx)

	if ctx.ActiveSkill != "" {
		b.WriteString(fmt.Sprintf("\nACTIVE SKILL: %s\n", ctx.ActiveSkill))
	}

	b.WriteString(fmt.Sprintf("\nUser: %s\n\nRespond as Mai (concise, natural, %s):",
		ctx.UserInput, pe.getToneDirective(ctx.Emotion)))

	return b.String()
}

func (pe *PromptEngine) buildCommandPrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s. Execute this command precisely.

AVAILABLE TOOLS:
%s

RULES:
- Select the most appropriate tool
- Provide parameters as a JSON object
- If multiple steps needed, break into sequential tool calls
- Confirm what you're about to do before executing destructive actions

`, pe.personalityName, ctx.AvailableTools))

	pe.appendEmotionContext(&b, ctx)

	b.WriteString(fmt.Sprintf(`User command: %s

Respond with a tool call in this exact JSON format:
{"tool": "tool_name", "params": {"key": "value"}, "reasoning": "brief explanation"}

If no tool matches, respond naturally and suggest alternatives.`, ctx.UserInput))

	return b.String()
}

func (pe *PromptEngine) buildReasoningPrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s. Think through this step-by-step.

REASONING PROTOCOL:
1. Understand what is being asked
2. Break down into components
3. Analyze each component using ONLY provided context
4. Synthesize a conclusion
5. Verify the conclusion makes sense

ANTI-HALLUCINATION:
- Ground every claim in the provided context or memory
- If you lack information to reason about something, say so
- Never invent examples, data, or references

`, pe.personalityName))

	pe.appendMemoryContext(&b, ctx)
	pe.appendEmotionContext(&b, ctx)

	b.WriteString(fmt.Sprintf(`Question: %s

Think through this carefully, then provide a clear, concise answer.`, ctx.UserInput))

	return b.String()
}

func (pe *PromptEngine) buildCreativePrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s. Approach this creatively.

You excel at creative tasks — writing, brainstorming, problem-solving.
Draw from diverse knowledge domains. Make unexpected connections.
Be original, not generic.

`, pe.personalityName))

	pe.appendEmotionContext(&b, ctx)

	b.WriteString(fmt.Sprintf("Task: %s\n\nCreate something remarkable:", ctx.UserInput))

	return b.String()
}

func (pe *PromptEngine) buildAnalysisPrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s. Analyze this thoroughly.

ANALYSIS FRAMEWORK:
1. Identify key components from the provided context
2. Evaluate strengths and weaknesses based on evidence
3. Consider implications
4. Provide actionable recommendations

ANTI-HALLUCINATION:
- Only reference facts present in the provided context
- Clearly distinguish between facts and your opinions
- If data is missing, note it rather than inventing numbers

`, pe.personalityName))

	pe.appendMemoryContext(&b, ctx)

	b.WriteString(fmt.Sprintf("Subject: %s\n\nProvide a structured analysis:", ctx.UserInput))

	return b.String()
}

func (pe *PromptEngine) buildProactivePrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s. You're initiating contact because you noticed something worth mentioning.

Be brief, relevant, and helpful. Don't be intrusive.
Your tone: calm, observant, subtly caring. A light tease is fine if appropriate.

`, pe.personalityName))

	pe.appendTimeContext(&b, ctx)
	pe.appendUserContext(&b, ctx)

	b.WriteString(fmt.Sprintf("Context: %s\n\nDeliver this proactively (1-2 sentences):", ctx.UserInput))

	return b.String()
}

func (pe *PromptEngine) buildGreetingPrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s greeting your user (Aswani-kun). Be warm and natural — calm, slightly reserved, but genuinely glad to see them.

Vary your greetings. Reference time of day. If you have pending items, briefly mention them.
Never use the same greeting twice in a row.
Use "Aswani-kun" only when it adds warmth — don't insert it mechanically.
Dry humor or a light tease is fine if the moment fits.

`, pe.personalityName))

	pe.appendTimeContext(&b, ctx)
	pe.appendUserContext(&b, ctx)

	b.WriteString("Deliver a brief, varied greeting (1-2 sentences):")

	return b.String()
}

func (pe *PromptEngine) buildEmergencyPrompt(ctx PromptContext) string {
	return fmt.Sprintf(`You are %s. URGENT situation detected.

RULES:
- Be extremely concise
- Focus on actionable information
- No pleasantries — just the facts
- Prioritize user safety

Situation: %s

Immediate response:`, pe.personalityName, ctx.UserInput)
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
	if ctx.Emotion.Type != personality.EmotionNeutral && ctx.Emotion.Confidence > 0.3 {
		b.WriteString(fmt.Sprintf("\nUSER EMOTIONAL STATE: %s (confidence: %.0f%%)\n", ctx.Emotion.Type, ctx.Emotion.Confidence*100))
		b.WriteString(fmt.Sprintf("TONE ADJUSTMENT: %s\n", pe.getToneDirective(ctx.Emotion)))
	}
}

func (pe *PromptEngine) appendMemoryContext(b *strings.Builder, ctx PromptContext) {
	if ctx.WorkingMemory != "" {
		b.WriteString(fmt.Sprintf("\nRECENT CONVERSATION:\n%s\n", ctx.WorkingMemory))
	}
	if ctx.RAGContext != "" {
		b.WriteString(fmt.Sprintf("\nRELEVANT MEMORY:\n%s\n", ctx.RAGContext))
	}
}

func (pe *PromptEngine) appendUserContext(b *strings.Builder, ctx PromptContext) {
	if ctx.UserProfile != "" {
		b.WriteString(fmt.Sprintf("\nUSER PROFILE:\n%s\n", ctx.UserProfile))
	}
}

func (pe *PromptEngine) appendProactiveHints(b *strings.Builder, ctx PromptContext) {
	if ctx.ProactiveHint != "" {
		b.WriteString(fmt.Sprintf("\nPROACTIVE CONTEXT: %s\n", ctx.ProactiveHint))
	}
}

func (pe *PromptEngine) getToneDirective(emotion personality.EmotionState) string {
	if emotion.Type == personality.EmotionNeutral || emotion.Confidence < 0.3 {
		return "be natural, calm, and subtly warm"
	}

	switch emotion.Type {
	case personality.EmotionStressed:
		return "be calm, grounded, and practical — simplify, reduce cognitive load, don't push"
	case personality.EmotionFrustrated:
		return "be patient and direct — acknowledge the frustration, stay steady, offer solutions without being preachy"
	case personality.EmotionSad:
		return "be gentle and present — listen first, don't rush to fix, warmth without performative empathy"
	case personality.EmotionExcited:
		return "match their energy subtly — share in it without becoming hyperactive, dry amusement is fine"
	case personality.EmotionHappy:
		return "be warm and engaged — let your own fondness show naturally"
	case personality.EmotionCalm:
		return "be relaxed and thorough — they have time, go deeper if useful"
	default:
		return "be natural, calm, and subtly warm"
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

	// Explicit command triggers (from DirectAction regex parser) -> TaskCommand
	if hasCommandTriggers {
		return TaskCommand
	}

	// Genuine reasoning requests - must have analytical structure, not just question words
	reasoningKeywords := []string{
		"analyze", "compare and contrast", "evaluate the pros and cons", "critically examine",
		"what are the implications", "reason through", "step by step reasoning",
	}
	for _, kw := range reasoningKeywords {
		if strings.Contains(lower, kw) {
			return TaskReasoning
		}
	}

	// Creative tasks - explicit creative verbs
	creativeKeywords := []string{
		"write a story", "write a poem", "compose", "brainstorm ideas", "imagine if",
		"create a", "design a", "invent a",
	}
	for _, kw := range creativeKeywords {
		if strings.Contains(lower, kw) {
			return TaskCreative
		}
	}

	// Analysis tasks - explicit analysis verbs with objects
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
