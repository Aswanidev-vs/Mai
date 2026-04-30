package memory

import (
	"fmt"
	"strings"
	"sync"

	"github.com/user/mai/pkg/interfaces"
)

// WorkingMemory implements interfaces.WorkingMemory
type WorkingMemory struct {
	mu      sync.RWMutex
	entries []interfaces.MemoryEntry
	limit   int
}

func NewWorkingMemory(limit int) *WorkingMemory {
	return &WorkingMemory{
		entries: make([]interfaces.MemoryEntry, 0),
		limit:   limit,
	}
}

func (m *WorkingMemory) Add(entry interfaces.MemoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = append(m.entries, entry)
	if len(m.entries) > m.limit {
		m.entries = m.entries[len(m.entries)-m.limit:]
	}
}

func (m *WorkingMemory) GetContext() string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var sb strings.Builder
	for _, entry := range m.entries {
		sb.WriteString(fmt.Sprintf("[%s]: %s\n", entry.Type, entry.Content))
	}
	return sb.String()
}

func (m *WorkingMemory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = make([]interfaces.MemoryEntry, 0)
}
