package memory

import (
	"fmt"
	"strings"
	"sync"

	"github.com/user/mai/pkg/interfaces"
)

// WorkingMemory implements interfaces.WorkingMemory using a lock-free ring buffer
// for the common case (single writer) and mutex for concurrent access.
type WorkingMemory struct {
	mu      sync.Mutex
	entries []interfaces.MemoryEntry
	limit   int
	head    int
	count   int
}

func NewWorkingMemory(limit int) *WorkingMemory {
	return &WorkingMemory{
		entries: make([]interfaces.MemoryEntry, limit),
		limit:   limit,
	}
}

func (m *WorkingMemory) Add(entry interfaces.MemoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[m.head] = entry
	m.head = (m.head + 1) % m.limit
	if m.count < m.limit {
		m.count++
	}
}

func (m *WorkingMemory) GetContext() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.count == 0 {
		return ""
	}

	// Pre-calculate capacity to avoid growing builder
	estimatedLen := m.count * 80 // rough estimate per entry
	var sb strings.Builder
	sb.Grow(estimatedLen)

	// Read entries in order (oldest to newest)
	start := (m.head - m.count + m.limit) % m.limit
	for i := 0; i < m.count; i++ {
		idx := (start + i) % m.limit
		entry := m.entries[idx]
		sb.WriteString("[")
		sb.WriteString(entry.Type)
		sb.WriteString("]: ")
		sb.WriteString(entry.Content)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (m *WorkingMemory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.head = 0
	m.count = 0
}

// Len returns the current number of entries (no allocation).
func (m *WorkingMemory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

// Get returns the i-th most recent entry (0 = most recent). No allocation.
func (m *WorkingMemory) Get(i int) (interfaces.MemoryEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if i < 0 || i >= m.count {
		return interfaces.MemoryEntry{}, false
	}
	idx := (m.head - 1 - i + m.limit) % m.limit
	return m.entries[idx], true
}

// String returns a debug representation.
func (m *WorkingMemory) String() string {
	return fmt.Sprintf("WorkingMemory{count=%d, limit=%d}", m.Len(), m.limit)
}
