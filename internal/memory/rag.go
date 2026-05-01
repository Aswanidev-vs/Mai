package memory

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

type RAGPipeline struct {
	semantic interfaces.SemanticStore
	episodic interfaces.EpisodicStore
	llm      interfaces.LLMProvider
	topK     int
}

func NewRAGPipeline(semantic interfaces.SemanticStore, episodic interfaces.EpisodicStore, llm interfaces.LLMProvider) *RAGPipeline {
	return &RAGPipeline{
		semantic: semantic,
		episodic: episodic,
		llm:      llm,
		topK:     5,
	}
}

type RAGResult struct {
	Answer      string               `json:"answer"`
	Sources     []interfaces.MemoryEntry `json:"sources"`
	Confidence  float64              `json:"confidence"`
}

func (r *RAGPipeline) Query(ctx context.Context, question string) (*RAGResult, error) {
	log.Printf("[RAG] Processing query: %s", question)

	// Step 1: Retrieve from semantic memory (vector search)
	semanticResults, err := r.semantic.SearchFacts(question, r.topK)
	if err != nil {
		log.Printf("[RAG] Semantic search failed: %v", err)
		semanticResults = []interfaces.MemoryEntry{}
	}

	// Step 2: Retrieve from episodic memory (keyword search)
	episodicResults, err := r.episodic.QueryEvents(question, r.topK)
	if err != nil {
		log.Printf("[RAG] Episodic search failed: %v", err)
		episodicResults = []interfaces.MemoryEntry{}
	}

	// Step 3: Merge and deduplicate results
	merged := r.mergeResults(semanticResults, episodicResults)

	if len(merged) == 0 {
		return &RAGResult{
			Answer:     "",
			Sources:    nil,
			Confidence: 0,
		}, nil
	}

	// Step 4: Build context from retrieved entries
	var contextParts []string
	for i, entry := range merged {
		contextParts = append(contextParts, fmt.Sprintf("[%d] (%s) %s", i+1, entry.Type, entry.Content))
	}
	retrievedContext := strings.Join(contextParts, "\n")

	// Step 5: Generate answer using LLM with retrieved context
	prompt := fmt.Sprintf(`Based on the following retrieved information, answer the user's question.
If the information is insufficient, say so honestly.

Retrieved Information:
%s

Question: %s

Answer concisely:`, retrievedContext, question)

	answer, err := r.llm.Generate(ctx, prompt, interfaces.GenerationOptions{Temperature: 0.3})
	if err != nil {
		return nil, fmt.Errorf("llm generation failed: %w", err)
	}

	// Step 6: Calculate confidence based on result quality
	confidence := r.calculateConfidence(merged, answer)

	return &RAGResult{
		Answer:     answer,
		Sources:    merged,
		Confidence: confidence,
	}, nil
}

func (r *RAGPipeline) Ingest(ctx context.Context, entry interfaces.MemoryEntry) error {
	// Store in episodic memory
	if err := r.episodic.StoreEvent(entry); err != nil {
		return fmt.Errorf("episodic store failed: %w", err)
	}

	// Store in semantic memory (generates embedding)
	if err := r.semantic.AddFact(entry); err != nil {
		log.Printf("[RAG] Semantic store failed (non-fatal): %v", err)
	}

	return nil
}

func (r *RAGPipeline) mergeResults(semantic, episodic []interfaces.MemoryEntry) []interfaces.MemoryEntry {
	seen := make(map[string]bool)
	var merged []interfaces.MemoryEntry

	// Prioritize semantic results (they're vector-matched)
	for _, entry := range semantic {
		key := entry.Content
		if !seen[key] {
			seen[key] = true
			merged = append(merged, entry)
		}
	}

	// Add episodic results that aren't duplicates
	for _, entry := range episodic {
		key := entry.Content
		if !seen[key] {
			seen[key] = true
			merged = append(merged, entry)
		}
	}

	return merged
}

func (r *RAGPipeline) calculateConfidence(sources []interfaces.MemoryEntry, answer string) float64 {
	if len(sources) == 0 {
		return 0
	}

	// Base confidence from number of sources
	confidence := float64(len(sources)) / float64(r.topK)
	if confidence > 1.0 {
		confidence = 1.0
	}

	// Reduce confidence if answer indicates uncertainty
	lower := strings.ToLower(answer)
	if strings.Contains(lower, "i don't") || strings.Contains(lower, "insufficient") || strings.Contains(lower, "not sure") {
		confidence *= 0.5
	}

	return confidence
}
