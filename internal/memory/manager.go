package memory

import (
	"context"
	"log"

	"github.com/user/mai/pkg/interfaces"
)

type Manager struct {
	working    interfaces.WorkingMemory
	episodic   interfaces.EpisodicStore
	semantic   interfaces.SemanticStore
	procedural interfaces.ProceduralStore
	rag        *RAGPipeline
}

func NewMemoryManager(w interfaces.WorkingMemory, e interfaces.EpisodicStore, s interfaces.SemanticStore, p interfaces.ProceduralStore) *Manager {
	m := &Manager{
		working:    w,
		episodic:   e,
		semantic:   s,
		procedural: p,
	}

	// Initialize RAG pipeline if all components available
	if s != nil && e != nil {
		// RAG needs an LLM provider for answer generation; use nil for now
		// Will be set up properly in main.go via SetRAGProvider
		log.Println("[Memory] Manager initialized (RAG provider pending)")
	}

	return m
}

func (m *Manager) SetRAGProvider(llm interfaces.LLMProvider) {
	if m.semantic != nil && m.episodic != nil && llm != nil {
		m.rag = NewRAGPipeline(m.semantic, m.episodic, llm)
		log.Println("[Memory] RAG pipeline initialized")
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

func (m *Manager) RAG() *RAGPipeline {
	return m.rag
}

func (m *Manager) Retrieve(ctx context.Context, query string, k int) ([]interfaces.MemoryEntry, error) {
	// Use RAG pipeline if available
	if m.rag != nil {
		result, err := m.rag.Query(ctx, query)
		if err == nil && result != nil && len(result.Sources) > 0 {
			return result.Sources, nil
		}
	}

	// Fallback: query semantic then episodic
	var results []interfaces.MemoryEntry

	if m.semantic != nil {
		semanticResults, err := m.semantic.SearchFacts(query, k)
		if err == nil {
			results = append(results, semanticResults...)
		}
	}

	if len(results) < k && m.episodic != nil {
		episodicResults, err := m.episodic.QueryEvents(query, k-len(results))
		if err == nil {
			results = append(results, episodicResults...)
		}
	}

	return results, nil
}

func (m *Manager) Store(ctx context.Context, entry interfaces.MemoryEntry) error {
	// Store in episodic
	if m.episodic != nil {
		if err := m.episodic.StoreEvent(entry); err != nil {
			return err
		}
	}

	// Store in semantic (generates embedding)
	if m.semantic != nil && entry.Content != "" {
		if err := m.semantic.AddFact(entry); err != nil {
			log.Printf("[Memory] Semantic store failed (non-fatal): %v", err)
		}
	}

	return nil
}
