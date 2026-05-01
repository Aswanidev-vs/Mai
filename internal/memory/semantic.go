package memory

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/user/mai/pkg/interfaces"
)

type vectorEntry struct {
	Fact   interfaces.MemoryEntry `json:"fact"`
	Vector []float32              `json:"vector"`
}

type SemanticStore struct {
	mu       sync.RWMutex
	entries  []vectorEntry
	llm      interfaces.LLMProvider
	filePath string
}

func NewSemanticStore(llm interfaces.LLMProvider, dataDir string) *SemanticStore {
	if err := os.MkdirAll(dataDir, 0755); err == nil {
		// ok
	}

	store := &SemanticStore{
		entries:  make([]vectorEntry, 0),
		llm:      llm,
		filePath: filepath.Join(dataDir, "semantic_vectors.json"),
	}

	store.load()
	return store
}

func (s *SemanticStore) AddFact(entry interfaces.MemoryEntry) error {
	embedding, err := s.llm.Embed(context.Background(), entry.Content)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, vectorEntry{Fact: entry, Vector: embedding})
	return s.save()
}

func (s *SemanticStore) SearchFacts(query string, k int) ([]interfaces.MemoryEntry, error) {
	queryVec, err := s.llm.Embed(context.Background(), query)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.entries) == 0 {
		return []interfaces.MemoryEntry{}, nil
	}

	type scored struct {
		index int
		score float64
	}
	scores := make([]scored, len(s.entries))
	for i, e := range s.entries {
		scores[i] = scored{index: i, score: cosineSimilarity(queryVec, e.Vector)}
	}

	sort.Slice(scores, func(i, j int) bool { return scores[i].score > scores[j].score })

	var topK []interfaces.MemoryEntry
	for i := 0; i < k && i < len(scores); i++ {
		topK = append(topK, s.entries[scores[i].index].Fact)
	}

	return topK, nil
}

func (s *SemanticStore) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

func (s *SemanticStore) save() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *SemanticStore) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.entries)
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dotProduct, normA, normB float64
	for i := 0; i < len(a); i++ {
		dotProduct += float64(a[i] * b[i])
		normA += float64(a[i] * a[i])
		normB += float64(b[i] * b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}
