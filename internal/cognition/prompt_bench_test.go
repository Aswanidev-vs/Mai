package cognition

import (
	"testing"

	"github.com/user/mai/internal/personality"
)

func BenchmarkPromptEngine_BuildPrompt_Conversation(b *testing.B) {
	engine := NewPromptEngine()
	ctx := PromptContext{
		TaskType:  TaskConversation,
		UserInput: "What's the weather like today?",
		Emotion:   personality.EmotionState{Type: personality.EmotionNeutral, Confidence: 0},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.BuildPrompt(ctx)
	}
}

func BenchmarkPromptEngine_BuildPrompt_WithMemory(b *testing.B) {
	engine := NewPromptEngine()
	ctx := PromptContext{
		TaskType:      TaskConversation,
		UserInput:     "What did we discuss earlier?",
		WorkingMemory: "User asked about Go channels and concurrency patterns",
		RAGContext:    "Go channels are used for goroutine communication",
		UserProfile:   "User is a Go developer who works on distributed systems",
		Emotion:       personality.EmotionState{Type: personality.EmotionNeutral, Confidence: 0},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.BuildPrompt(ctx)
	}
}

func BenchmarkPromptEngine_BuildPrompt_WithEmotion(b *testing.B) {
	engine := NewPromptEngine()
	ctx := PromptContext{
		TaskType:  TaskConversation,
		UserInput: "I'm so frustrated with this bug",
		Emotion:   personality.EmotionState{Type: personality.EmotionFrustrated, Confidence: 0.8},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.BuildPrompt(ctx)
	}
}

func BenchmarkPromptEngine_ClassifyTask(b *testing.B) {
	engine := NewPromptEngine()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.ClassifyTask("open chrome and search for Go tutorials", false)
	}
}
