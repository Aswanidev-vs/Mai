package memory

import (
	"context"

	"github.com/user/mai/pkg/interfaces"
)

// Manager implements interfaces.MemoryManager
type Manager struct {
	working    interfaces.WorkingMemory
	episodic   interfaces.EpisodicStore
	semantic   interfaces.SemanticStore
	procedural interfaces.ProceduralStore
}

func NewMemoryManager(w interfaces.WorkingMemory, e interfaces.EpisodicStore, s interfaces.SemanticStore, p interfaces.ProceduralStore) *Manager {
	return &Manager{
		working:    w,
		episodic:   e,
		semantic:   s,
		procedural: p,
	}
}

func (m *Manager) Working() interfaces.WorkingMemory {
	return m.working
}

func (m *Manager) Episodic() interfaces.EpisodicStore {
	return m.episodic
}

func (m *Manager) Semantic() interfaces.SemanticStore {
	return m.semantic
}

func (m *Manager) Procedural() interfaces.ProceduralStore {
	return m.procedural
}

func (m *Manager) Retrieve(ctx context.Context, query string, k int) ([]interfaces.MemoryEntry, error) {
	// For now, just episodic
	return m.episodic.QueryEvents(query, k)
}

func (m *Manager) Store(ctx context.Context, entry interfaces.MemoryEntry) error {
	return m.episodic.StoreEvent(entry)
}
