package memory

import (
	"bufio"
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
	jsonlPath string
	loaded   bool
}

func NewSemanticStore(llm interfaces.LLMProvider, dataDir string) *SemanticStore {
	if err := os.MkdirAll(dataDir, 0755); err == nil {
		// ok
	}

	jsonPath := filepath.Join(dataDir, "semantic_vectors.json")
	return &SemanticStore{
		entries:   make([]vectorEntry, 0),
		llm:       llm,
		filePath:  jsonPath,
		jsonlPath: jsonPath + ".jsonl",
		loaded:    false,
	}
}

func (s *SemanticStore) ensureLoaded() {
	if s.loaded {
		return
	}
	s.load()
	s.loaded = true
}

func (s *SemanticStore) AddFact(entry interfaces.MemoryEntry) error {
	s.ensureLoaded()

	embedding, err := s.llm.Embed(context.Background(), entry.Content)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = append(s.entries, vectorEntry{Fact: entry, Vector: embedding})

	// Append-only write — O(1) instead of O(n)
	return s.appendJSONL(s.entries[len(s.entries)-1])
}

func (s *SemanticStore) SearchFacts(query string, k int) ([]interfaces.MemoryEntry, error) {
	s.ensureLoaded()

	queryVec, err := s.llm.Embed(context.Background(), query)
	if err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.entries) == 0 {
		return []interfaces.MemoryEntry{}, nil
	}

	// Small store: brute force is fast enough
	if len(s.entries) < 500 {
		return s.topKSearch(queryVec, k), nil
	}

	// Large store: approximate search (sample + refine)
	return s.approximateSearch(queryVec, k), nil
}

func (s *SemanticStore) topKSearch(queryVec []float32, k int) []interfaces.MemoryEntry {
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
	return topK
}

// approximateSearch: sample 300 vectors, find top candidates, refine neighbors.
func (s *SemanticStore) approximateSearch(queryVec []float32, k int) []interfaces.MemoryEntry {
	n := len(s.entries)
	sampleSize := 300
	if sampleSize > n {
		sampleSize = n
	}

	type scored struct {
		index int
		score float64
	}

	// Sample evenly
	sampled := make([]scored, sampleSize)
	step := n / sampleSize
	for i := 0; i < sampleSize; i++ {
		idx := i * step
		if idx >= n {
			idx = n - 1
		}
		sampled[i] = scored{index: idx, score: cosineSimilarity(queryVec, s.entries[idx].Vector)}
	}
	sort.Slice(sampled, func(i, j int) bool { return sampled[i].score > sampled[j].score })

	// Gather neighbors of top candidates
	candidateSet := make(map[int]bool)
	for i := 0; i < k*3 && i < len(sampled); i++ {
		center := sampled[i].index
		for j := center - 20; j <= center+20; j++ {
			if j >= 0 && j < n {
				candidateSet[j] = true
			}
		}
	}

	// Score all candidates
	candidates := make([]scored, 0, len(candidateSet))
	for idx := range candidateSet {
		candidates = append(candidates, scored{index: idx, score: cosineSimilarity(queryVec, s.entries[idx].Vector)})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].score > candidates[j].score })

	var topK []interfaces.MemoryEntry
	for i := 0; i < k && i < len(candidates); i++ {
		topK = append(topK, s.entries[candidates[i].index].Fact)
	}
	return topK
}

func (s *SemanticStore) Count() int {
	s.ensureLoaded()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

// --- Persistence ---

func (s *SemanticStore) appendJSONL(entry vectorEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	f, err := os.OpenFile(s.jsonlPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(data)
	return err
}

func (s *SemanticStore) save() error {
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.filePath, data, 0644)
}

func (s *SemanticStore) load() error {
	// Try main JSON first (fast path)
	data, err := os.ReadFile(s.filePath)
	if err == nil && len(data) > 2 {
		if err := json.Unmarshal(data, &s.entries); err == nil && len(s.entries) > 0 {
			return nil
		}
	}

	// Fallback: load from JSONL append file
	s.entries = make([]vectorEntry, 0)
	f, err := os.Open(s.jsonlPath)
	if err != nil {
		return nil // no data yet — not an error
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
	for scanner.Scan() {
		var entry vectorEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
			s.entries = append(s.entries, entry)
		}
	}

	// Consolidate to JSON for next load
	if len(s.entries) > 0 {
		_ = s.save()
	}
	return nil
}

// Compact rewrites the JSON from JSONL. Call periodically.
func (s *SemanticStore) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.load()
	return s.save()
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
