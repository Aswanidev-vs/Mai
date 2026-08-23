package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/free-llms-foundation/retrieval-go"
	"github.com/user/mai/pkg/interfaces"
)

// webResearchTimeout bounds the whole search+fetch operation.
// Research is inherently slower than chat, but the voice loop still wants it back quickly.
var (
	webResearchTimeout        = 18 * time.Second
	webResearchDefaultSources = 3
	webResearchMaxSources     = 5
	// Per-source condensed-passage cap. Total dossier is further clamped below
	// so the result fits Mai's 4096-token context window alongside the
	// system prompt and conversation history.
	webResearchPerSourceChars = 350
	webResearchTotalChars     = 1400
)

// WebResearchTool searches the live web, fetches and reads the top result pages,
// condenses the relevant passages, and returns a cited Markdown dossier.
type WebResearchTool struct {
	client *retrieval.Client
	initErr error
}

func NewWebResearchTool() *WebResearchTool {
	c, err := retrieval.New(retrieval.WithTimeout(webResearchTimeout))
	if err != nil {
		return &WebResearchTool{initErr: err}
	}
	return &WebResearchTool{client: c}
}

func (t *WebResearchTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name: "web_research",
		Description: "Researches a topic on the live web: runs a web search, fetches and reads the top result pages, " +
			"condenses the relevant passages, and returns a cited Markdown dossier (sources numbered [1]..[n] with URLs). " +
			"Use this for current events, factual lookups, or anything needing up-to-date or verified information. " +
			"Cite sources as [n] in text replies; in spoken replies attribute naturally (e.g. \"according to the docs\") without bracket markers.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": { "type": "string", "description": "The research question or topic" },
				"num_sources": { "type": "integer", "description": "How many result pages to fetch and read (1-5, default 3)", "default": 3 }
			},
			"required": ["query"]
		}`),
		Category: interfaces.ToolCategoryWeb,
		Keywords: []string{"research", "web", "search", "browse", "fetch", "cite", "lookup", "current events"},
	}
}

func (t *WebResearchTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	if t.client == nil {
		return interfaces.ToolResult{Error: fmt.Errorf("web_research unavailable: %v", t.initErr)}, nil
	}

	var args struct {
		Query      string `json:"query"`
		NumSources int    `json:"num_sources"`
	}
	if len(params) == 0 || string(params) == "{}" || string(params) == "null" {
		return interfaces.ToolResult{Error: fmt.Errorf("web_research requires a 'query' parameter")}, nil
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: fmt.Errorf("failed to parse web_research parameters: %v", err)}, nil
	}
	if strings.TrimSpace(args.Query) == "" {
		return interfaces.ToolResult{Error: fmt.Errorf("web_research requires a non-empty 'query'")}, nil
	}

	n := args.NumSources
	if n <= 0 {
		n = webResearchDefaultSources
	}
	if n > webResearchMaxSources {
		n = webResearchMaxSources
	}

	ctx, cancel := context.WithTimeout(ctx, webResearchTimeout)
	defer cancel()

	pages, err := t.client.SearchWithQuery(ctx, args.Query, "")
	if err != nil {
		return interfaces.ToolResult{Error: fmt.Errorf("web search failed: %w", err)}, nil
	}
	if len(pages) == 0 {
		return interfaces.ToolResult{Output: fmt.Sprintf("No web results found for '%s'.", args.Query)}, nil
	}
	// Over-fetch: pull more pages than requested so that, after some are
	// inevitably blocked (robots/403/rate-limit), we still emit N readable ones.
	fetchCap := n * 2
	if fetchCap > len(pages) {
		fetchCap = len(pages)
	}
	pages = pages[:fetchCap]

	type fetched struct {
		page retrieval.Page
		doc  *retrieval.Document
	}
	results := make([]fetched, len(pages))
	var wg sync.WaitGroup
	for i := range pages {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			doc, ferr := t.client.ParseContentFromLink(ctx, pages[i].Link, true)
			if ferr != nil {
				return
			}
			results[i] = fetched{page: pages[i], doc: doc}
		}(i)
	}
	wg.Wait()

	type sourceEntry struct {
		num     int
		title   string
		link    string
		passage string
	}
	entries := make([]sourceEntry, 0, n)
	failed := 0
	for _, r := range results {
		if r.doc == nil || strings.TrimSpace(r.doc.Content) == "" {
			failed++
			continue
		}
		passage := condense(r.doc.Content, args.Query, webResearchPerSourceChars)
		if passage == "" {
			failed++
			continue
		}
		entries = append(entries, sourceEntry{
			num:     len(entries) + 1,
			title:   r.page.Title,
			link:    r.page.Link,
			passage: passage,
		})
	}
	// Honor the requested count; over-fetch only guaranteed we had enough.
	if len(entries) > n {
		entries = entries[:n]
	}

	if len(entries) == 0 {
		return interfaces.ToolResult{Output: fmt.Sprintf("Found results for '%s' but could not read any pages (blocked, robots-denied, or empty).", args.Query)}, nil
	}

	// Emit body + Sources in lockstep, dropping trailing entries that would
	// overflow the context budget so [n] stays consistent across both.
	const sourcesHeader = "\nSources:\n"
	budget := webResearchTotalChars - len(sourcesHeader)
	var body strings.Builder
	body.WriteString(fmt.Sprintf("Web research for \"%s\":\n\n", args.Query))
	var srcLines []string
	for _, e := range entries {
		block := fmt.Sprintf("[%d] %s\n%s\n\n", e.num, e.title, e.passage)
		srcLine := fmt.Sprintf("[%d] %s — %s", e.num, e.title, e.link)
		if body.Len() > 0 && body.Len()+len(block)+len(sourcesHeader)+len(srcLine)+1 > budget {
			break
		}
		body.WriteString(block)
		srcLines = append(srcLines, srcLine)
	}
	out := body.String() + sourcesHeader + strings.Join(srcLines, "\n") + "\n"
	if failed > 0 {
		out += fmt.Sprintf("\nNote: %d of %d fetched pages were unreadable (blocked, robots-denied, or empty).\n", failed, len(pages))
	}
	return interfaces.ToolResult{Output: out}, nil
}

var webStopwords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "and": {}, "or": {}, "but": {}, "of": {}, "to": {},
	"in": {}, "on": {}, "for": {}, "with": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"be": {}, "this": {}, "that": {}, "these": {}, "those": {}, "it": {}, "its": {}, "as": {},
	"at": {}, "by": {}, "from": {}, "about": {}, "into": {}, "can": {}, "will": {}, "would": {},
	"should": {}, "may": {}, "might": {}, "has": {}, "have": {}, "had": {}, "do": {}, "does": {},
	"did": {}, "not": {}, "no": {}, "so": {}, "if": {}, "then": {}, "than": {}, "what": {},
	"which": {}, "who": {}, "whom": {}, "how": {}, "when": {}, "where": {}, "why": {}, "all": {},
	"any": {}, "both": {}, "each": {}, "more": {}, "most": {}, "other": {}, "some": {}, "such": {},
	"only": {}, "own": {}, "same": {}, "too": {}, "very": {}, "just": {}, "also": {}, "you": {},
	"your": {}, "we": {}, "our": {}, "they": {}, "their": {}, "he": {}, "she": {}, "his": {},
	"her": {},
}

func tokenize(s string) map[string]int {
	counts := make(map[string]int)
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		if len(w) < 2 {
			continue
		}
		if _, ok := webStopwords[w]; ok {
			continue
		}
		counts[w]++
	}
	return counts
}

// scoreChunk returns lexical overlap between a chunk and the query terms.
// Headings carry topic signal, so they get a boost.
func scoreChunk(chunk string, qTerms map[string]int) int {
	if len(qTerms) == 0 {
		return 1
	}
	c := tokenize(chunk)
	score := 0
	for term, qc := range qTerms {
		if cc, ok := c[term]; ok {
			score += cc * qc
		}
	}
	if strings.HasPrefix(strings.TrimSpace(chunk), "#") {
		score *= 2
	}
	return score
}

// condense keeps the most query-relevant chunks of a fetched page, in original
// reading order, up to maxChars. Falls back to the lead paragraph when nothing
// overlaps the query.
func condense(content, query string, maxChars int) string {
	qTerms := tokenize(query)
	chunks := strings.Split(content, "\n\n")

	type scored struct {
		text  string
		score int
		order int
	}
	var kept []scored
	for i, ch := range chunks {
		ch = strings.TrimSpace(ch)
		if len(ch) < 40 {
			continue
		}
		s := scoreChunk(ch, qTerms)
		if s <= 0 {
			continue
		}
		kept = append(kept, scored{text: ch, score: s, order: i})
	}

	if len(kept) == 0 {
		for _, ch := range chunks {
			ch = strings.TrimSpace(ch)
			if len(ch) >= 40 {
				kept = append(kept, scored{text: ch, score: 1, order: 0})
				break
			}
		}
	}

	sort.SliceStable(kept, func(i, j int) bool { return kept[i].score > kept[j].score })
	if len(kept) > 4 {
		kept = kept[:4]
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].order < kept[j].order })

	var sb strings.Builder
	for _, k := range kept {
		text := k.text
		if len(text) > maxChars {
			text = text[:maxChars]
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(text)
		if sb.Len() >= maxChars {
			break
		}
	}
	out := sb.String()
	if len(out) > maxChars {
		out = out[:maxChars]
	}
	return out
}
