package memory

import (
	"testing"

	"github.com/user/mai/pkg/interfaces"
)

func BenchmarkWorkingMemory_Add(b *testing.B) {
	mem := NewWorkingMemory(100)
	entry := interfaces.MemoryEntry{Type: "user_input", Content: "benchmark test entry"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mem.Add(entry)
	}
}

func BenchmarkWorkingMemory_GetContext(b *testing.B) {
	mem := NewWorkingMemory(100)
	for i := 0; i < 50; i++ {
		mem.Add(interfaces.MemoryEntry{
			Type:    "user_input",
			Content: "test entry content for benchmark",
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mem.GetContext()
	}
}

func BenchmarkWorkingMemory_Get(b *testing.B) {
	mem := NewWorkingMemory(100)
	for i := 0; i < 50; i++ {
		mem.Add(interfaces.MemoryEntry{
			Type:    "user_input",
			Content: "test",
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mem.Get(0)
	}
}

func BenchmarkWorkingMemory_Len(b *testing.B) {
	mem := NewWorkingMemory(100)
	for i := 0; i < 50; i++ {
		mem.Add(interfaces.MemoryEntry{Type: "user_input", Content: "test"})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mem.Len()
	}
}
