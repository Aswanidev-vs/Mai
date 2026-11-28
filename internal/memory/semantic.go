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

// maxSemanticEntries bounds both RAM and the on-disk snapshot. Older facts are
// dropped first; the episodic store keeps the verbatim history.
const maxSemanticEntries = 2000

const compactEvery = 256

type vectorEntry struct {
	Fact   interfaces.MemoryEntry `json:"fact"`
	Vector []float32              `json:"vector"`
}

type SemanticStore struct {
	mu        sync.RWMutex
	loadOnce  sync.Once
	entries   []vectorEntry
	llm       interfaces.LLMProvider
	filePath  string
	jsonlPath string
	loaded    bool
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
	// Loading writes entries; hold the same lock Compact uses so the first
	// query cannot race with another turn's write.
	s.loadOnce.Do(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.load()
		s.loaded = true
	})
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
	if err := s.appendJSONL(s.entries[len(s.entries)-1]); err != nil {
		return err
	}
	// Fold the tail into the snapshot periodically so the JSONL cannot grow
	// without bound and a restart does not have to replay thousands of lines.
	if len(s.entries)%compactEvery == 0 {
		return s.compactLocked()
	}
	if len(s.entries) > maxSemanticEntries {
		s.entries = append([]vectorEntry(nil), s.entries[len(s.entries)-maxSemanticEntries:]...)
		return s.compactLocked()
	}
	return nil
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
	// The snapshot used to win outright, so facts appended to the JSONL tail
	// were invisible until someone deleted the JSON. Merge both sources and
	// dedupe by ID instead.
	var merged []vectorEntry
	seen := make(map[string]bool)
	add := func(entry vectorEntry) {
		key := entry.Fact.ID
		if key == "" {
			key = entry.Fact.Content
		}
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		merged = append(merged, entry)
	}

	if data, err := os.ReadFile(s.filePath); err == nil && len(data) > 2 {
		var snapshot []vectorEntry
		if err := json.Unmarshal(data, &snapshot); err == nil {
			for _, entry := range snapshot {
				add(entry)
			}
		}
	}
	if f, err := os.Open(s.jsonlPath); err == nil {
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer
		for scanner.Scan() {
			var entry vectorEntry
			if err := json.Unmarshal(scanner.Bytes(), &entry); err == nil {
				add(entry)
			}
		}
		_ = f.Close()
	}

	if len(merged) > maxSemanticEntries {
		merged = append([]vectorEntry(nil), merged[len(merged)-maxSemanticEntries:]...)
	}
	s.entries = merged
	if len(s.entries) > 0 {
		return s.compactLocked()
	}
	return nil
}

// compactLocked folds the append-only tail into the snapshot and truncates it.
func (s *SemanticStore) compactLocked() error {
	if err := s.save(); err != nil {
		return err
	}
	return os.WriteFile(s.jsonlPath, nil, 0644)
}

// Compact rewrites the JSON from JSONL. Call periodically.
func (s *SemanticStore) Compact() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
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
