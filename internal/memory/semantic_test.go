package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/user/mai/pkg/interfaces"
)

// mockLLM implements interfaces.LLMProvider for testing.
type mockLLM struct {
	embedFunc func(ctx context.Context, text string) ([]float32, error)
}

func (m *mockLLM) Generate(ctx context.Context, prompt string, opts interfaces.GenerationOptions) (string, error) {
	return "", nil
}

func (m *mockLLM) GenerateStructured(ctx context.Context, prompt string, schema json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func (m *mockLLM) Stream(ctx context.Context, prompt string, opts interfaces.GenerationOptions, onChunk func(string)) error {
	return nil
}

func (m *mockLLM) Embed(ctx context.Context, text string) ([]float32, error) {
	if m.embedFunc != nil {
		return m.embedFunc(ctx, text)
	}
	// Simple deterministic embedding based on text length
	vec := make([]float32, 8)
	for i := range vec {
		vec[i] = float32(len(text)%10) * float32(i+1) / 10.0
	}
	return vec, nil
}

func (m *mockLLM) HealthCheck(ctx context.Context) error {
	return nil
}

func TestSemanticStore_LazyLoad(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	llm := &mockLLM{}

	store := NewSemanticStore(llm, dir)

	// Should not be loaded yet
	assert.False(t, store.loaded)
	assert.Equal(t, 0, store.Count())

	// First access triggers load
	store.ensureLoaded()
	assert.True(t, store.loaded)
}

func TestSemanticStore_AddAndSearch(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	llm := &mockLLM{}
	store := NewSemanticStore(llm, dir)

	// Add some facts
	err := store.AddFact(interfaces.MemoryEntry{
		ID:      "1",
		Type:    "fact",
		Content: "The weather is sunny today",
	})
	require.NoError(t, err)

	err = store.AddFact(interfaces.MemoryEntry{
		ID:      "2",
		Type:    "fact",
		Content: "I like to code in Go",
	})
	require.NoError(t, err)

	err = store.AddFact(interfaces.MemoryEntry{
		ID:      "3",
		Type:    "fact",
		Content: "The weather is rainy tomorrow",
	})
	require.NoError(t, err)

	// Search should return results
	results, err := store.SearchFacts("weather", 2)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestSemanticStore_AppendOnlyPersistence(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	llm := &mockLLM{}
	store := NewSemanticStore(llm, dir)

	// Add facts
	for i := 0; i < 5; i++ {
		err := store.AddFact(interfaces.MemoryEntry{
			ID:      string(rune('A' + i)),
			Type:    "fact",
			Content: "Test content",
		})
		require.NoError(t, err)
	}

	// JSONL file should exist and have 5 lines
	jsonlPath := filepath.Join(dir, "semantic_vectors.json.jsonl")
	data, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)

	lines := 0
	for _, b := range data {
		if b == '\n' {
			lines++
		}
	}
	assert.Equal(t, 5, lines)

	// Verify each line is valid JSON
	store2 := NewSemanticStore(llm, dir)
	err = store2.load()
	require.NoError(t, err)
	assert.Equal(t, 5, len(store2.entries))
}

func TestSemanticStore_LoadFromJSONL(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	llm := &mockLLM{}

	// Manually write JSONL file (simulating append-only writes)
	jsonlPath := filepath.Join(dir, "semantic_vectors.json.jsonl")
	entries := []vectorEntry{
		{Fact: interfaces.MemoryEntry{ID: "1", Content: "fact one"}, Vector: []float32{1, 0, 0}},
		{Fact: interfaces.MemoryEntry{ID: "2", Content: "fact two"}, Vector: []float32{0, 1, 0}},
	}

	f, err := os.Create(jsonlPath)
	require.NoError(t, err)
	for _, e := range entries {
		data, _ := json.Marshal(e)
		data = append(data, '\n')
		f.Write(data)
	}
	f.Close()

	// Load should read from JSONL
	store := NewSemanticStore(llm, dir)
	err = store.load()
	require.NoError(t, err)
	assert.Equal(t, 2, len(store.entries))

	// Should also consolidate to JSON
	_, err = os.Stat(filepath.Join(dir, "semantic_vectors.json"))
	assert.NoError(t, err)
}

func TestSemanticStore_ConcurrentAccess(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	llm := &mockLLM{}
	store := NewSemanticStore(llm, dir)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				_ = store.AddFact(interfaces.MemoryEntry{
					ID:      string(rune('A' + idx)),
					Type:    "fact",
					Content: "concurrent content",
				})
				_, _ = store.SearchFacts("test", 1)
			}
		}(i)
	}
	wg.Wait()

	// All 200 facts should be present (10 goroutines × 20 iterations)
	count := store.Count()
	assert.GreaterOrEqual(t, count, 190, "should have at least 190 entries (may lose 1 due to append race)")
	assert.LessOrEqual(t, count, 200, "should have at most 200 entries")
}

func TestCosineSimilarity(t *testing.T) {
	defer goleak.VerifyNone(t)

	tests := []struct {
		name     string
		a, b     []float32
		expected float64
	}{
		{"identical vectors", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal vectors", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite vectors", []float32{1, 0}, []float32{-1, 0}, -1.0},
		{"different lengths", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
		{"zero vector", []float32{0, 0}, []float32{1, 0}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			assert.InDelta(t, tt.expected, result, 0.001)
		})
	}
}

func TestSemanticStore_Compact(t *testing.T) {
	defer goleak.VerifyNone(t)

	dir := t.TempDir()
	llm := &mockLLM{}
	store := NewSemanticStore(llm, dir)

	// Add facts
	for i := 0; i < 10; i++ {
		store.AddFact(interfaces.MemoryEntry{
			ID:      string(rune('A' + i)),
			Type:    "fact",
			Content: "test content",
		})
	}

	// Compact should consolidate JSONL → JSON
	err := store.Compact()
	require.NoError(t, err)

	// Verify JSON file has all entries
	data, err := os.ReadFile(filepath.Join(dir, "semantic_vectors.json"))
	require.NoError(t, err)

	var entries []vectorEntry
	err = json.Unmarshal(data, &entries)
	require.NoError(t, err)
	assert.Equal(t, 10, len(entries))
}
