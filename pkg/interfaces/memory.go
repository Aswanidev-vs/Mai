package interfaces

import (
	"context"
)

// MemoryEntry represents a single unit of information in the memory system
type MemoryEntry struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"` // "episodic", "semantic", "procedural"
	Content   string                 `json:"content"`
	Metadata  map[string]interface{} `json:"metadata"`
	Timestamp int64                  `json:"timestamp"`
}

// MemoryManager defines the hierarchical memory system
type MemoryManager interface {
	Working() WorkingMemory
	Episodic() EpisodicStore
	Semantic() SemanticStore
	Procedural() ProceduralStore
	Retrieve(ctx context.Context, query string, k int) ([]MemoryEntry, error)
	Store(ctx context.Context, entry MemoryEntry) error
}

// WorkingMemory handles short-term context buffer
type WorkingMemory interface {
	Add(entry MemoryEntry)
	GetContext() string
	Clear()
}

// EpisodicStore handles long-term conversation and event history
type EpisodicStore interface {
	StoreEvent(entry MemoryEntry) error
	QueryEvents(query string, limit int) ([]MemoryEntry, error)
}

// SemanticStore handles vector-based knowledge and facts
type SemanticStore interface {
	AddFact(entry MemoryEntry) error
	SearchFacts(query string, k int) ([]MemoryEntry, error)
}

// ProceduralStore handles skills and tool usage patterns
type ProceduralStore interface {
	AddSkill(name string, pattern string) error
	GetSkill(name string) (string, error)
}
