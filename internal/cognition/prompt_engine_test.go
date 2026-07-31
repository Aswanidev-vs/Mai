package cognition

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/mai/internal/personality"
)

func TestClassifyTask(t *testing.T) {
	engine := NewPromptEngine()

	tests := []struct {
		name          string
		text          string
		hasCmdTrigger bool
		expected      TaskType
	}{
		// Greetings
		{"simple hi", "hi", false, TaskGreeting},
		{"hello", "hello", false, TaskGreeting},
		{"hey", "hey there", false, TaskGreeting},
		{"good morning", "good morning", false, TaskGreeting},
		{"good evening", "good evening", false, TaskGreeting},
		{"what's up", "what's up", false, TaskGreeting},
		{"howdy", "howdy", false, TaskGreeting},
		{"long greeting not greeting", "hello how are you doing today my friend", false, TaskConversation},

		// Commands
		{"explicit command trigger", "open notepad", true, TaskCommand},
		{"play command", "play some music", true, TaskCommand},
		{"send message", "send message to john", true, TaskCommand},

		// Reasoning (explicit analytical requests only)
		{"analyze this", "analyze the pros and cons of remote work", false, TaskReasoning},
		{"compare and contrast", "compare and contrast React and Vue", false, TaskReasoning},
		{"evaluate pros and cons", "evaluate the pros and cons of remote work", false, TaskReasoning},
		{"step by step", "reason through this step by step", false, TaskReasoning},
		{"critical examination", "critically examine this argument", false, TaskReasoning},

		// Creative
		{"write story", "write a story about a robot", false, TaskCreative},
		{"write poem", "write a poem about nature", false, TaskCreative},
		{"brainstorm", "brainstorm ideas for a app", false, TaskCreative},
		{"create", "create a design for a logo", false, TaskCreative},

		// Analysis
		{"summarize", "summarize the report", false, TaskAnalysis},
		{"review", "review the code changes", false, TaskAnalysis},
		{"assess", "assess the situation", false, TaskAnalysis},

		// Conversation (default)
		{"casual question", "what do you think about pizza?", false, TaskConversation},
		{"statement", "I had a great day today", false, TaskConversation},
		{"short question", "why is the sky blue?", false, TaskConversation},
		{"open ended", "tell me about yourself", false, TaskConversation},
		{"opinion request", "what's your opinion on this?", false, TaskConversation},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.ClassifyTask(tt.text, tt.hasCmdTrigger)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestBuildPrompt_Conversation(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskConversation,
		UserInput: "What's the weather like?",
		Emotion:   personality.EmotionState{Type: personality.EmotionNeutral, Confidence: 0},
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "Mai")
	assert.Contains(t, prompt, "What's the weather like?")
	assert.Contains(t, prompt, "don't know")
	assert.Contains(t, prompt, "never make up facts")
}

func TestBuildPrompt_ConversationWithEmotion(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskConversation,
		UserInput: "I'm so frustrated with this code",
		Emotion:   personality.EmotionState{Type: personality.EmotionFrustrated, Confidence: 0.8},
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "frustrated")
	assert.Contains(t, prompt, "solution-focused")
}

func TestBuildPrompt_ConversationWithMemory(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:      TaskConversation,
		UserInput:     "What did we discuss earlier?",
		WorkingMemory: "User asked about Go channels",
		RAGContext:    "Go channels are used for goroutine communication",
		UserProfile:   "User is a Go developer",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "Go channels")
	assert.Contains(t, prompt, "Go developer")
}

func TestBuildPrompt_ContextBudget(t *testing.T) {
	engine := NewPromptEngine()

	// Create very large context that exceeds the 3000 char budget
	largeWM := ""
	for i := 0; i < 100; i++ {
		largeWM += "This is a long working memory entry that adds up. "
	}
	largeRAG := ""
	for i := 0; i < 100; i++ {
		largeRAG += "This is a long RAG context entry that also adds up. "
	}

	ctx := PromptContext{
		TaskType:      TaskConversation,
		UserInput:     "Hello",
		WorkingMemory: largeWM,
		RAGContext:    largeRAG,
	}

	prompt := engine.BuildPrompt(ctx)

	// Total prompt should be reasonable (under ~5000 chars including system prompt)
	assert.Less(t, len(prompt), 6000, "prompt should respect context budget")
	// Should still contain some context (not empty)
	assert.Contains(t, prompt, "RECENT CONTEXT")
	assert.Contains(t, prompt, "RELEVANT INFO")
}

func TestBuildPrompt_Reasoning(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskReasoning,
		UserInput: "Analyze the trade-offs between microservices and monolith",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "think something through")
	assert.Contains(t, prompt, "step by step")
}

func TestBuildPrompt_Analysis(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskAnalysis,
		UserInput: "Summarize the quarterly report",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "analysis")
	assert.Contains(t, prompt, "thorough")
}

func TestBuildPrompt_Command(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskCommand,
		UserInput: "Open Chrome and search for Go tutorials",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "DO something")
	assert.Contains(t, prompt, "Open Chrome")
}

func TestBuildPrompt_Creative(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskCreative,
		UserInput: "Write a short story about time travel",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "creative")
	assert.Contains(t, prompt, "original")
}

func TestBuildPrompt_Greeting(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskGreeting,
		UserInput: "hello",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "greets you")
	assert.Contains(t, prompt, "vary it")
}

func TestBuildPrompt_Emergency(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskEmergency,
		UserInput: "System overload detected",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "URGENT")
	assert.Contains(t, prompt, "concise")
	assert.Contains(t, prompt, "safety")
}

func TestBuildPrompt_Proactive(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:  TaskProactive,
		UserInput: "User has 3 pending tasks",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "initiating contact")
	assert.Contains(t, prompt, "brief")
}

func TestBuildPrompt_ActiveSkill(t *testing.T) {
	engine := NewPromptEngine()

	ctx := PromptContext{
		TaskType:   TaskConversation,
		UserInput:  "Plan my day",
		ActiveSkill: "Plan My Day",
	}

	prompt := engine.BuildPrompt(ctx)

	assert.Contains(t, prompt, "ACTIVE SKILL: Plan My Day")
}

func TestBuildPrompt_EmotionContextInjection(t *testing.T) {
	engine := NewPromptEngine()

	// High confidence emotion should inject context
	ctx := PromptContext{
		TaskType:  TaskConversation,
		UserInput: "I'm stressed about work",
		Emotion:   personality.EmotionState{Type: personality.EmotionStressed, Confidence: 0.8},
	}
	prompt := engine.BuildPrompt(ctx)
	assert.Contains(t, prompt, "stressed")

	// Low confidence should NOT inject emotion context
	ctx2 := PromptContext{
		TaskType:  TaskConversation,
		UserInput: "Hello",
		Emotion:   personality.EmotionState{Type: personality.EmotionStressed, Confidence: 0.2},
	}
	prompt2 := engine.BuildPrompt(ctx2)
	assert.NotContains(t, prompt2, "stressed")
}

func TestBuildSystemPrompt(t *testing.T) {
	ctx := PromptContext{
		TaskType:  TaskConversation,
		UserInput: "Hello",
	}

	prompt := BuildSystemPrompt("base prompt", ctx)

	require.NotEmpty(t, prompt)
	assert.True(t, strings.Contains(prompt, "Mai"))
}
