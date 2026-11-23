package agent

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/user/mai/pkg/interfaces"
)

type countingLLM struct {
	calls atomic.Int32
}

func (m *countingLLM) Generate(context.Context, string, interfaces.GenerationOptions) (string, error) {
	m.calls.Add(1)
	return "", nil
}
func (m *countingLLM) Stream(context.Context, string, interfaces.GenerationOptions, func(string)) error {
	return nil
}
func (m *countingLLM) GenerateStructured(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}
func (m *countingLLM) Embed(context.Context, string) ([]float32, error) { return nil, nil }
func (m *countingLLM) HealthCheck(context.Context) error                { return nil }

func TestIsIdleRequiresIdleStatusAndQuietPipeline(t *testing.T) {
	tests := []struct {
		name       string
		status     interfaces.AgentStatus
		speaking   bool
		processing bool
		want       bool
	}{
		{"idle and quiet", interfaces.StatusIdle, false, false, true},
		{"thinking", interfaces.StatusThinking, false, false, false},
		{"speaking", interfaces.StatusIdle, true, false, false},
		{"processing", interfaces.StatusIdle, false, true, false},
		{"speaking and processing", interfaces.StatusIdle, true, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &Orchestrator{interrupts: NewInterruptManager()}
			o.setStatus(tt.status)
			o.interrupts.SetState(tt.speaking, tt.processing)
			assert.Equal(t, tt.want, o.isIdle())
		})
	}
}

func TestIsIdleFallsBackToStatusWithoutInterruptManager(t *testing.T) {
	o := &Orchestrator{}
	o.setStatus(interfaces.StatusIdle)
	assert.True(t, o.isIdle())
	o.setStatus(interfaces.StatusThinking)
	assert.False(t, o.isIdle())
}

func TestSummarizeDroppedSkipsWhileBusy(t *testing.T) {
	entries := []interfaces.MemoryEntry{{Type: "user_input", Content: "remember this"}}
	tests := []struct {
		name       string
		status     interfaces.AgentStatus
		speaking   bool
		processing bool
		wantCalls  int32
	}{
		{"idle generates summary", interfaces.StatusIdle, false, false, 1},
		{"thinking skips", interfaces.StatusThinking, false, false, 0},
		{"speaking skips", interfaces.StatusIdle, true, false, 0},
		{"processing skips", interfaces.StatusIdle, false, true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llm := &countingLLM{}
			o := &Orchestrator{llm: llm, interrupts: NewInterruptManager()}
			o.setStatus(tt.status)
			o.interrupts.SetState(tt.speaking, tt.processing)

			o.summarizeDropped(entries)

			assert.Equal(t, tt.wantCalls, llm.calls.Load())
		})
	}
}

func TestNeedsWebLookup(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		// Locally answerable: must not pay for web_research.
		{"what time is it?", false},
		{"what's the time?", false},
		{"what's the current time?", false},
		{"what is the time?", false},
		{"what's today's date?", false},
		{"what day is it?", false},
		{"what is your name?", false},
		{"how are you?", false},
		{"what's for dinner?", false},
		{"what is love?", false},
		{"what does this mean?", false},
		// Genuine research triggers must survive.
		{"what's the weather in Paris?", true},
		{"who is the president of France?", true},
		{"latest news on the election", true},
		{"look up the population of Japan", true},
		{"tell me about the Roman Empire", true},
		{"when was the Eiffel Tower built?", true},
		{"current price of Bitcoin", true},
		{"where is the nearest hospital?", true},
		{"today's top song", true},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			assert.Equal(t, tt.want, needsWebLookup(tt.text))
		})
	}
}

func TestHasCommandTriggers(t *testing.T) {
	tests := []struct {
		text string
		want bool
	}{
		{"open chrome", true},
		{"set a timer for 5 minutes", true},
		{"send a message to Mom", true},
		{"restart the server", true},
		{"remove the file", true},
		{"close the door", true},
		{"run the tests", true},
		{"what time is it?", true},
		// Clock questions stay commands so they reach get_system_time,
		// while substring lookalikes must not route to the tool loop.
		{"display the photo", false},
		{"newspaper article", false},
		{"disclose the details", false},
		{"how is your day?", false},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			assert.Equal(t, tt.want, hasCommandTriggers(tt.text))
		})
	}
}
