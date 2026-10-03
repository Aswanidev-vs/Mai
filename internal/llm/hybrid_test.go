package llm

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/user/mai/internal/agent"
	"github.com/user/mai/pkg/interfaces"
	"github.com/user/mai/pkg/models"
)

// stubProvider records which method the router called. It deliberately has NO
// StreamChat: it models the OpenAI-compatible clouds, whose only streaming
// endpoint is the flat one.
type stubProvider struct {
	name string

	mu     sync.Mutex
	calls  []string
	chunks []string
}

func newStub(name string) *stubProvider {
	return &stubProvider{name: name, chunks: []string{"hello ", "there"}}
}

func (s *stubProvider) methodCalls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *stubProvider) record(call string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call)
}

func (s *stubProvider) Generate(context.Context, string, interfaces.GenerationOptions) (string, error) {
	s.record("Generate")
	return s.name, nil
}

func (s *stubProvider) Stream(_ context.Context, _ string, _ interfaces.GenerationOptions, callback func(chunk string)) error {
	s.record("Stream")
	for _, c := range s.chunks {
		callback(c)
	}
	return nil
}

func (s *stubProvider) GenerateStructured(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (s *stubProvider) Embed(context.Context, string) ([]float32, error) { return nil, nil }

func (s *stubProvider) HealthCheck(context.Context) error { return nil }

// chatStub adds the chat endpoint and can be made to block until its context
// dies, standing in for an in-flight HTTP read during barge-in.
type chatStub struct {
	*stubProvider
	waitForCx bool
}

func newChatStub(name string) *chatStub {
	return &chatStub{stubProvider: newStub(name)}
}

func (c *chatStub) StreamChat(ctx context.Context, _ []interfaces.ChatMessage, _ interfaces.GenerationOptions, callback func(chunk string)) error {
	c.record("StreamChat")
	for _, chunk := range c.chunks {
		if c.waitForCx {
			<-ctx.Done()
			return ctx.Err()
		}
		callback(chunk)
	}
	return nil
}

func privacyGuard() *agent.PrivacyGuard {
	return agent.NewPrivacyGuard(models.Privacy{
		DetectionEnabled: true,
		SensitiveWords:   []string{"salary", "passport"},
	})
}

func hybrid(local, cloud interfaces.LLMProvider) *HybridProvider {
	return NewHybridProvider(local, cloud, privacyGuard())
}

func TestHybridProvider_StreamChatRoutesByPrivacy(t *testing.T) {
	local, cloud := newChatStub("local"), newChatStub("cloud")
	p := hybrid(local, cloud)
	msgs := []interfaces.ChatMessage{{Role: "user", Content: "what's the weather?"}}

	require.NoError(t, p.StreamChat(context.Background(), msgs, interfaces.GenerationOptions{}, func(string) {}))
	assert.Equal(t, []string{"StreamChat"}, cloud.methodCalls())
	assert.Empty(t, local.methodCalls())

	// A sensitive word anywhere in the thread keeps the WHOLE conversation
	// local, not just the turn that carries it.
	local2, cloud2 := newChatStub("local"), newChatStub("cloud")
	p2 := hybrid(local2, cloud2)
	sensitive := []interfaces.ChatMessage{
		{Role: "user", Content: "my passport is expiring"},
		{Role: "assistant", Content: "then renew it"},
		{Role: "user", Content: "and my salary?"},
	}

	require.NoError(t, p2.StreamChat(context.Background(), sensitive, interfaces.GenerationOptions{}, func(string) {}))
	assert.Equal(t, []string{"StreamChat"}, local2.methodCalls())
	assert.Empty(t, cloud2.methodCalls())
}

func TestHybridProvider_StreamChatForwardsChunksVerbatim(t *testing.T) {
	p := hybrid(newChatStub("local"), newChatStub("cloud"))
	var out string

	require.NoError(t, p.StreamChat(context.Background(),
		[]interfaces.ChatMessage{{Role: "user", Content: "hi"}},
		interfaces.GenerationOptions{},
		func(chunk string) { out += chunk }))

	assert.Equal(t, "hello there", out)
}

// A hybrid pairing whose chosen side has no chat endpoint must still answer
// instead of failing the turn.
func TestHybridProvider_StreamChatFallsBackToFlatStream(t *testing.T) {
	local, cloud := newStub("local"), newStub("cloud")
	p := hybrid(local, cloud)
	var out string

	require.NoError(t, p.StreamChat(context.Background(),
		[]interfaces.ChatMessage{{Role: "user", Content: "hi"}},
		interfaces.GenerationOptions{},
		func(chunk string) { out += chunk }))

	assert.Equal(t, []string{"Stream"}, cloud.methodCalls())
	assert.Equal(t, "hello there", out)
}

func TestFlattenChat(t *testing.T) {
	got := flattenChat([]interfaces.ChatMessage{
		{Role: "user", Content: "who are you?"},
		{Role: "assistant", Content: "mai"},
		{Role: "user", Content: "  "},
	})

	assert.Equal(t, "User: who are you?\nAssistant: mai", got)
}

// Barge-in relies on ctx: cancelling the turn must abort the request mid-stream
// instead of letting the answer run to completion.
func TestHybridProvider_StreamChatPropagatesCancellation(t *testing.T) {
	tests := []struct {
		name      string
		sensitive string
	}{
		{"local route", "my passport number"},
		{"cloud route", "hello there"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			local, cloud := newChatStub("local"), newChatStub("cloud")
			local.waitForCx, cloud.waitForCx = true, true
			p := hybrid(local, cloud)

			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- p.StreamChat(ctx,
					[]interfaces.ChatMessage{{Role: "user", Content: tc.sensitive}},
					interfaces.GenerationOptions{},
					func(string) { t.Error("chunk delivered after cancellation") })
			}()
			cancel()

			err := <-done
			require.Error(t, err)
			assert.True(t, errors.Is(err, context.Canceled), "got %v", err)
		})
	}
}
