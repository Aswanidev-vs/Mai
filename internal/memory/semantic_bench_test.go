package memory

import (
	"fmt"
	"testing"

	"github.com/user/mai/pkg/interfaces"
)

func BenchmarkSemanticStore_SearchFacts_100(b *testing.B) {
	benchmarkSearch(b, 100)
}

func BenchmarkSemanticStore_SearchFacts_500(b *testing.B) {
	benchmarkSearch(b, 500)
}

func BenchmarkSemanticStore_SearchFacts_1000(b *testing.B) {
	benchmarkSearch(b, 1000)
}

func benchmarkSearch(b *testing.B, n int) {
	dir := b.TempDir()
	llm := &mockLLM{}
	store := NewSemanticStore(llm, dir)

	for i := 0; i < n; i++ {
		store.AddFact(interfaces.MemoryEntry{
			ID:      fmt.Sprintf("fact-%d", i),
			Type:    "fact",
			Content: fmt.Sprintf("This is test fact number %d about various topics", i),
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store.SearchFacts("test topic", 5)
	}
}

func BenchmarkCosineSimilarity_8(b *testing.B) {
	a := []float32{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
	b2 := []float32{0.8, 0.7, 0.6, 0.5, 0.4, 0.3, 0.2, 0.1}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cosineSimilarity(a, b2)
	}
}

func BenchmarkCosineSimilarity_384(b *testing.B) {
	a := make([]float32, 384)
	b2 := make([]float32, 384)
	for i := range a {
		a[i] = float32(i) / 384.0
		b2[i] = float32(384-i) / 384.0
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cosineSimilarity(a, b2)
	}
}
