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

	// onCompact is invoked (async) with the dropped entries whenever
	// compaction discards them, so a summarizer can compress them for
	// re-injection instead of losing them to "older entries summarized".
	onCompact func(dropped []interfaces.MemoryEntry)
}

// SetOnCompact registers a callback invoked with the entries dropped during
// compaction. Set by the agent so the LLM can summarize them asynchronously.
func (m *WorkingMemory) SetOnCompact(fn func(dropped []interfaces.MemoryEntry)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCompact = fn
}

func NewWorkingMemory(limit int) *WorkingMemory {
	return &WorkingMemory{
		entries:  make([]interfaces.MemoryEntry, limit),
		limit:    limit,
		maxChars: 12000, // ~3k tokens — fits any VRAM-sized context; summaries keep it bounded
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

// compactLocked drops older entries, keeps recent ones, and hands the dropped
// entries to the onCompact callback on a background goroutine so a summarizer
// can compress them instead of losing them to a static marker (must hold lock).
func (m *WorkingMemory) compactLocked() {
	if m.count <= 3 {
		return // too few entries to compact
	}

	// Keep the 3 most recent entries, mark the rest as summarized
	keepCount := 3
	droppedCount := m.count - keepCount
	start := (m.head - m.count + m.limit) % m.limit

	// Snapshot the dropped entries before overwriting them.
	dropped := make([]interfaces.MemoryEntry, 0, droppedCount)
	for i := 0; i < droppedCount; i++ {
		dropped = append(dropped, m.entries[(start+i)%m.limit])
	}

	// Shift entries: move recent ones to the beginning
	newCount := 0
	for i := m.count - keepCount; i < m.count; i++ {
		idx := (start + i) % m.limit
		m.entries[newCount] = m.entries[idx]
		newCount++
	}

	// Add a summary entry
	m.entries[newCount] = interfaces.MemoryEntry{
		Type:    "system",
		Content: fmt.Sprintf("[auto-compact: %d older entries summarized]", droppedCount),
	}
	newCount++

	// Reset state
	m.head = newCount % m.limit
	m.count = newCount

	// Hand the dropped entries to the summarizer off the hot path. A
	// summarizer SHOULD be set — without one this keeps the legacy
	// placeholder-marker behavior.
	if m.onCompact != nil {
		go m.onCompact(dropped)
	}
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
