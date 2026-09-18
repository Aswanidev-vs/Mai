package llm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/mai/pkg/interfaces"
)

func newProvider(systemPrompt string, numCtx int) *OllamaProvider {
	return NewOllamaProvider("test-model", "http://localhost:11434/api/generate", systemPrompt, nil, OllamaOptions{NumCtx: numCtx})
}

// The persona must ride a system MESSAGE: Ollama's /api/chat has no top-level
// "system" field, so a persona sent there is silently dropped and the model
// answers as a generic assistant.
func TestBuildChatMessages_PersonaLeadsAsSystemMessage(t *testing.T) {
	p := newProvider("You are Mai.", 0)
	history := []interfaces.ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hey"},
		{Role: "user", Content: "can you sing?"},
	}

	got := p.buildChatMessages(history, interfaces.GenerationOptions{})

	require.Len(t, got, len(history)+1)
	assert.Equal(t, "system", got[0].Role)
	assert.Equal(t, "You are Mai.", got[0].Content)
	assert.Equal(t, history, got[1:])
}

func TestBuildChatMessages_NoSystemPrompt(t *testing.T) {
	p := newProvider("", 0)
	history := []interfaces.ChatMessage{{Role: "user", Content: "hello"}}

	got := p.buildChatMessages(history, interfaces.GenerationOptions{})

	assert.Equal(t, history, got)
}

// Without a pinned window there is nothing to budget against.
func TestBuildChatMessages_UnpinnedKeepsHistory(t *testing.T) {
	p := newProvider("persona", 0)
	history := make([]interfaces.ChatMessage, 0, 40)
	for i := 0; i < 20; i++ {
		history = append(history,
			interfaces.ChatMessage{Role: "user", Content: strings.Repeat("context ", 50)},
			interfaces.ChatMessage{Role: "assistant", Content: "sure"},
		)
	}

	got := p.buildChatMessages(history, interfaces.GenerationOptions{MaxTokens: 280})

	assert.Len(t, got, len(history)+1)
}

// An over-long history must lose its oldest pairs — never the persona, and
// never the turn being answered: Ollama drops messages from the front of the
// prompt when it overflows, which would otherwise erase the persona.
func TestBuildChatMessages_TrimsOldestHistory(t *testing.T) {
	p := newProvider("You are Mai.", 1024)
	history := []interfaces.ChatMessage{{Role: "user", Content: "current question"}}
	for i := 0; i < 20; i++ {
		history = append([]interfaces.ChatMessage{
			{Role: "user", Content: strings.Repeat("older turn ", 40)},
			{Role: "assistant", Content: "reply"},
		}, history...)
	}

	got := p.buildChatMessages(history, interfaces.GenerationOptions{MaxTokens: 280})

	require.NotEmpty(t, got)
	assert.Equal(t, "system", got[0].Role)
	assert.Equal(t, "You are Mai.", got[0].Content)
	last := got[len(got)-1]
	assert.Equal(t, "current question", last.Content)
	assert.Less(t, len(got), len(history)+1)
	assert.LessOrEqual(t, messageTokens(got)+chatTemplateOverheadTokens+280, 1024)
}

// The current turn is kept even when it alone is larger than the budget: it is
// the only part of the conversation the reply can be about.
func TestBuildChatMessages_KeepsOversizedCurrentTurn(t *testing.T) {
	p := newProvider("persona", 512)
	msg := interfaces.ChatMessage{Role: "user", Content: strings.Repeat("long ", 400)}

	got := p.buildChatMessages([]interfaces.ChatMessage{msg}, interfaces.GenerationOptions{MaxTokens: 280})

	require.Len(t, got, 2)
	assert.Equal(t, msg, got[len(got)-1])
}

func TestEstimateTokens(t *testing.T) {
	assert.Equal(t, 0, estimateTokens(""))
	assert.Equal(t, 1, estimateTokens("abc"))
	assert.Equal(t, 1, estimateTokens("abcd"))
	assert.Equal(t, 3, estimateTokens(strings.Repeat("a", 10)))
}