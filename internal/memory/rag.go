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
	Answer     string                  `json:"answer"`
	Sources    []interfaces.MemoryEntry `json:"sources"`
	Confidence float64                 `json:"confidence"`
}

func (r *RAGPipeline) Query(ctx context.Context, question string) (*RAGResult, error) {
	log.Printf("[RAG] Processing query: %s", truncateStr(question, 80))

	// Step 1: Retrieve from semantic memory (vector search)
	semanticResults, err := r.semantic.SearchFacts(question, r.topK)
	if err != nil {
		log.Printf("[RAG] Semantic search failed: %v", err)
		semanticResults = []interfaces.MemoryEntry{}
	}

	// Step 2: Retrieve from episodic memory (latest events as fallback context)
	episodicResults, err := r.episodic.QueryEvents("", r.topK)
	if err != nil {
		log.Printf("[RAG] Episodic search failed: %v", err)
		episodicResults = []interfaces.MemoryEntry{}
	}

	// Step 3: Merge and deduplicate
	merged := r.mergeResults(semanticResults, episodicResults)

	if len(merged) == 0 {
		log.Printf("[RAG] No relevant context found")
		return &RAGResult{Answer: "", Sources: nil, Confidence: 0}, nil
	}

	// Step 4: Filter out low-quality entries (user input fragments, short entries)
	var filtered []interfaces.MemoryEntry
	for _, entry := range merged {
		// Skip entries that are just user commands (not knowledge)
		if entry.Type == "user_input" && len(entry.Content) < 100 {
			continue
		}
		// Skip very short entries
		if len(entry.Content) < 20 {
			continue
		}
		filtered = append(filtered, entry)
	}

	if len(filtered) == 0 {
		log.Printf("[RAG] All retrieved entries were low-quality after filtering")
		return &RAGResult{Answer: "", Sources: nil, Confidence: 0}, nil
	}

	// Step 5: Build context from filtered entries
	var contextParts []string
	for i, entry := range filtered {
		if i >= 3 { // Limit to top 3 to avoid context pollution
			break
		}
		contextParts = append(contextParts, fmt.Sprintf("[%s] %s", entry.Type, truncateStr(entry.Content, 300)))
	}
	retrievedContext := strings.Join(contextParts, "\n---\n")

	// Step 6: Only use RAG if the context looks relevant
	// If top result is just conversation fragments, skip the LLM call
	if !r.isContextRelevant(question, filtered) {
		log.Printf("[RAG] Retrieved context not relevant enough to question")
		return &RAGResult{Answer: "", Sources: nil, Confidence: 0}, nil
	}

	// Step 7: Generate answer using LLM with retrieved context
	prompt := fmt.Sprintf(`Based on the following retrieved information, answer the user's question.
If the information does not contain the answer, respond with "NO_ANSWER".
Do NOT make up information. Only answer if the retrieved data actually contains the answer.

Retrieved Information:
%s

Question: %s

Answer (or NO_ANSWER):`, retrievedContext, question)

	answer, err := r.llm.Generate(ctx, prompt, interfaces.GenerationOptions{Temperature: 0.1})
	if err != nil {
		return nil, fmt.Errorf("llm generation failed: %w", err)
	}

	// Step 8: Check if LLM said there's no answer
	if strings.Contains(strings.ToUpper(answer), "NO_ANSWER") || len(answer) < 5 {
		log.Printf("[RAG] LLM determined no answer available in context")
		return &RAGResult{Answer: "", Sources: nil, Confidence: 0}, nil
	}

	confidence := r.calculateConfidence(filtered, answer)

	log.Printf("[RAG] Found answer with confidence %.2f", confidence)
	return &RAGResult{
		Answer:     answer,
		Sources:    filtered,
		Confidence: confidence,
	}, nil
}

func (r *RAGPipeline) Ingest(ctx context.Context, entry interfaces.MemoryEntry) error {
	if err := r.episodic.StoreEvent(entry); err != nil {
		return fmt.Errorf("episodic store failed: %w", err)
	}
	if entry.Content != "" && len(entry.Content) > 20 {
		if err := r.semantic.AddFact(entry); err != nil {
			log.Printf("[RAG] Semantic store failed (non-fatal): %v", err)
		}
	}
	return nil
}

func (r *RAGPipeline) isContextRelevant(question string, entries []interfaces.MemoryEntry) bool {
	questionLower := strings.ToLower(question)
	questionWords := strings.Fields(questionLower)

	// Count how many question words appear in the retrieved context
	matchCount := 0
	for _, word := range questionWords {
		if len(word) < 3 {
			continue
		}
		for _, entry := range entries {
			if strings.Contains(strings.ToLower(entry.Content), word) {
				matchCount++
				break
			}
		}
	}

	// Require at least 30% of question words to appear in context
	needed := len(questionWords) * 3 / 10
	if needed < 1 {
		needed = 1
	}
	return matchCount >= needed
}

func (r *RAGPipeline) mergeResults(semantic, episodic []interfaces.MemoryEntry) []interfaces.MemoryEntry {
	seen := make(map[string]bool)
	var merged []interfaces.MemoryEntry

	for _, entry := range semantic {
		key := entry.Content[:minInt(len(entry.Content), 100)]
		if !seen[key] {
			seen[key] = true
			merged = append(merged, entry)
		}
	}

	for _, entry := range episodic {
		key := entry.Content[:minInt(len(entry.Content), 100)]
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

	confidence := float64(len(sources)) / float64(r.topK)
	if confidence > 1.0 {
		confidence = 1.0
	}

	lower := strings.ToLower(answer)
	if strings.Contains(lower, "i don't") || strings.Contains(lower, "insufficient") || strings.Contains(lower, "not sure") {
		confidence *= 0.5
	}

	return confidence
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
