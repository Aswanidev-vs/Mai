package memory

import (
	"context"
	"math"
	"sync"

	"github.com/user/mai/pkg/interfaces"
)

// SemanticStore implements interfaces.SemanticStore using local vector storage
type SemanticStore struct {
	mu      sync.RWMutex
	facts   []interfaces.MemoryEntry
	vectors [][]float32
	llm     interfaces.LLMProvider
}

func NewSemanticStore(llm interfaces.LLMProvider) *SemanticStore {
	return &SemanticStore{
		facts:   make([]interfaces.MemoryEntry, 0),
		vectors: make([][]float32, 0),
		llm:     llm,
	}
}

func (s *SemanticStore) AddFact(entry interfaces.MemoryEntry) error {
	// 1. Generate embedding for the content
	embedding, err := s.llm.Embed(context.Background(), entry.Content)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.facts = append(s.facts, entry)
	s.vectors = append(s.vectors, embedding)
	return nil
}

func (s *SemanticStore) SearchFacts(query string, k int) ([]interfaces.MemoryEntry, error) {
	// 1. Generate embedding for the query
	queryVec, err := s.llm.Embed(context.Background(), query)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.facts) == 0 {
		return []interfaces.MemoryEntry{}, nil
	}

	// 2. Calculate cosine similarity
	type result struct {
		index float64
		score float64
	}
	var scores []result

	for i, vec := range s.vectors {
		score := cosineSimilarity(queryVec, vec)
		scores = append(scores, result{index: float64(i), score: score})
	}

	// 3. Sort and return top k
	// Simple bubble sort or similar for now
	for i := 0; i < len(scores); i++ {
		for j := i + 1; j < len(scores); j++ {
			if scores[i].score < scores[j].score {
				scores[i], scores[j] = scores[j], scores[i]
			}
		}
	}

	var topK []interfaces.MemoryEntry
	for i := 0; i < k && i < len(scores); i++ {
		topK = append(topK, s.facts[int(scores[i].index)])
	}

	return topK, nil
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
