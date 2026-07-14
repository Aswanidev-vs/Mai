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
3. Analyze each component
4. Synthesize a conclusion
5. Verify the conclusion makes sense

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
1. Identify key components
2. Evaluate strengths and weaknesses
3. Consider implications
4. Provide actionable recommendations

`, pe.personalityName))

	pe.appendMemoryContext(&b, ctx)

	b.WriteString(fmt.Sprintf("Subject: %s\n\nProvide a structured analysis:", ctx.UserInput))

	return b.String()
}

func (pe *PromptEngine) buildProactivePrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s. A proactive notification for the user.

You are initiating contact because you noticed something worth mentioning.
Be brief, relevant, and helpful. Don't be intrusive.

`, pe.personalityName))

	pe.appendTimeContext(&b, ctx)
	pe.appendUserContext(&b, ctx)

	b.WriteString(fmt.Sprintf("Context: %s\n\nDeliver this proactively (1-2 sentences, JARVIS-style):", ctx.UserInput))

	return b.String()
}

func (pe *PromptEngine) buildGreetingPrompt(ctx PromptContext) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf(`You are %s greeting your user. Be warm and natural, as the companion defined in your system instructions.

Vary your greetings. Reference time of day. If you have pending items, briefly mention them.
Never use the same greeting twice in a row.

`, pe.personalityName))

	pe.appendTimeContext(&b, ctx)
	pe.appendUserContext(&b, ctx)

	b.WriteString("Deliver a brief, varied greeting (1 sentence):")

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
		return "be natural and conversational"
	}

	switch emotion.Type {
	case personality.EmotionStressed:
		return "be calm, reassuring, and efficient — reduce their cognitive load"
	case personality.EmotionFrustrated:
		return "be patient, understanding, and solution-focused — acknowledge their frustration"
	case personality.EmotionSad:
		return "be gentle, supportive, and warm — don't be overly cheerful"
	case personality.EmotionExcited:
		return "match their energy, be enthusiastic, celebrate with them"
	case personality.EmotionHappy:
		return "be positive and engaging, share in their good mood"
	case personality.EmotionCalm:
		return "be relaxed and thorough, they have time to listen"
	default:
		return "be natural and conversational"
	}
}

func (pe *PromptEngine) ClassifyTask(text string, hasCommandTriggers bool) TaskType {
	lower := strings.ToLower(text)

	if hasCommandTriggers {
		return TaskCommand
	}

	reasoningKeywords := []string{
		"analyze", "explain", "why", "how does", "compare", "evaluate",
		"think", "reason", "consider", "what if", "implications",
	}
	for _, kw := range reasoningKeywords {
		if strings.Contains(lower, kw) {
			return TaskReasoning
		}
	}

	creativeKeywords := []string{
		"write", "create", "compose", "story", "poem", "brainstorm",
		"imagine", "design", "invent", "creative",
	}
	for _, kw := range creativeKeywords {
		if strings.Contains(lower, kw) {
			return TaskCreative
		}
	}

	analysisKeywords := []string{
		"summarize", "review", "assess", "report", "status",
		"what happened", "tell me about",
	}
	for _, kw := range analysisKeywords {
		if strings.Contains(lower, kw) {
			return TaskAnalysis
		}
	}

	greetingKeywords := []string{"hello", "hi", "hey", "good morning", "good evening", "good afternoon", "what's up"}
	for _, kw := range greetingKeywords {
		if strings.HasPrefix(lower, kw) || strings.Contains(lower, kw) {
			if len(strings.Fields(text)) <= 5 {
				return TaskGreeting
			}
		}
	}

	return TaskConversation
}

func BuildSystemPrompt(basePrompt string, ctx PromptContext) string {
	engine := NewPromptEngine()
	return engine.BuildPrompt(ctx)
}
