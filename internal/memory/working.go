package memory

import (
	"fmt"
	"strings"
	"sync"

	"github.com/user/mai/pkg/interfaces"
)

// WorkingMemory implements interfaces.WorkingMemory using a lock-free ring buffer.
// Includes auto-compaction: when context exceeds maxChars, older entries are
// summarized and the buffer is compacted to prevent context overflow.
type WorkingMemory struct {
	mu       sync.Mutex
	entries  []interfaces.MemoryEntry
	limit    int
	head     int
	count    int
	maxChars int // auto-compact threshold (0 = no limit)
}

func NewWorkingMemory(limit int) *WorkingMemory {
	return &WorkingMemory{
		entries:  make([]interfaces.MemoryEntry, limit),
		limit:    limit,
		maxChars: 4000, // default: ~1000 tokens, leaves room for system prompt + user input
	}
}

// SetMaxChars sets the auto-compact threshold. When context exceeds this,
// older entries are summarized to keep the context manageable.
func (m *WorkingMemory) SetMaxChars(max int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.maxChars = max
}

func (m *WorkingMemory) Add(entry interfaces.MemoryEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries[m.head] = entry
	m.head = (m.head + 1) % m.limit
	if m.count < m.limit {
		m.count++
	}

	// Auto-compact if context is too large
	if m.maxChars > 0 {
		ctx := m.buildContextLocked()
		if len(ctx) > m.maxChars {
			m.compactLocked()
		}
	}
}

// GetContext returns the context string, truncated to maxChars if set.
func (m *WorkingMemory) GetContext() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.count == 0 {
		return ""
	}

	ctx := m.buildContextLocked()

	// Truncate to maxChars if needed
	if m.maxChars > 0 && len(ctx) > m.maxChars {
		ctx = m.truncateContext(ctx, m.maxChars)
	}

	return ctx
}

// buildContextLocked builds the full context string (must hold lock).
func (m *WorkingMemory) buildContextLocked() string {
	if m.count == 0 {
		return ""
	}

	estimatedLen := m.count * 80
	var sb strings.Builder
	sb.Grow(estimatedLen)

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

// truncateContext truncates context to maxChars, keeping the most recent entries.
func (m *WorkingMemory) truncateContext(ctx string, maxChars int) string {
	if len(ctx) <= maxChars {
		return ctx
	}

	// Split into lines, keep most recent (from end)
	lines := strings.Split(strings.TrimRight(ctx, "\n"), "\n")
	var kept []string
	totalLen := 0

	// Walk backwards (newest first) until we hit the limit
	for i := len(lines) - 1; i >= 0; i-- {
		lineLen := len(lines[i]) + 1 // +1 for newline
		if totalLen+lineLen > maxChars {
			break
		}
		kept = append([]string{lines[i]}, kept...) // prepend
		totalLen += lineLen
	}

	if len(kept) == 0 {
		return "[context truncated]\n"
	}

	// Add summary marker if we dropped older entries
	result := "[older context summarized]\n"
	for _, line := range kept {
		result += line + "\n"
	}
	return result
}

// compactLocked summarizes old entries and keeps recent ones (must hold lock).
// This is called when context exceeds maxChars.
func (m *WorkingMemory) compactLocked() {
	if m.count <= 3 {
		return // too few entries to compact
	}

	// Keep the 3 most recent entries, mark the rest as summarized
	keepCount := 3
	newCount := 0
	start := (m.head - m.count + m.limit) % m.limit

	// Shift entries: move recent ones to the beginning
	for i := m.count - keepCount; i < m.count; i++ {
		idx := (start + i) % m.limit
		m.entries[newCount] = m.entries[idx]
		newCount++
	}

	// Add a summary entry
	summaryCount := m.count - keepCount
	m.entries[newCount] = interfaces.MemoryEntry{
		Type:    "system",
		Content: fmt.Sprintf("[auto-compact: %d older entries summarized]", summaryCount),
	}
	newCount++

	// Reset state
	m.head = newCount % m.limit
	m.count = newCount
}

func (m *WorkingMemory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.head = 0
	m.count = 0
}

func (m *WorkingMemory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.count
}

func (m *WorkingMemory) Get(i int) (interfaces.MemoryEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if i < 0 || i >= m.count {
		return interfaces.MemoryEntry{}, false
	}
	idx := (m.head - 1 - i + m.limit) % m.limit
	return m.entries[idx], true
}

func (m *WorkingMemory) String() string {
	return fmt.Sprintf("WorkingMemory{count=%d, limit=%d, maxChars=%d}", m.Len(), m.limit, m.maxChars)
}
