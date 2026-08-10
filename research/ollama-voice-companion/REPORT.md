# REPORT: Go offline voice companion on Ollama — persona + long-context + ReAct loop

Date: 2026-08-09. Method: webfetch only (DDG/Bing/Mojeek all CAPTCHA-blocked; all sources fetched directly). Sources: Ollama docs, GitHub (ollama PRs #8938/#16639/#5760, llama.cpp server README), HuggingFace cards, arXiv papers, OpenAI/Anthropic/LangChain/LlamaIndex docs, Chroma research.

## 12 concrete recommendations

1. **Pin `num_ctx` explicitly — never trust defaults.** Ollama's FAQ says 4096, Modelfile says 2048, and the newer VRAM-scaled page defaults 4k/32k/256k by VRAM; all three live simultaneously. (https://docs.ollama.com/context-length, /faq, /modelfile). In Go: send `options: {num_ctx: N}` on every call or bake `PARAMETER num_ctx` into a custom Modelfile; the per-request path is being de-emphasized in favor of `OLLAMA_CONTEXT_LENGTH` (PR #8938, 2025-02-24).

2. **NUMP: size `num_ctx` per model class — 128k for 7B Qwen2.5/Gemma needs flash attention, 256k is an edge.** Qwen2.5-7B: 28 layers × 4 KV heads ⇒ ~56 KiB/token f16 KV ≈ 7 GiB @128k / 14 GiB @256k; Llama 3.1 8B class (8 KV heads) is ~16 GiB @128k. Model cards state ceilings: Llama 3.1 128k, Gemma 3 128k (output 8k), Qwen2.5-7B/14B 131k, 1M variants real ~256k. (https://huggingface.co/blog/kv-cache-quantization; cards; https://qwenlm.github.io/blog/qwen2.5-1m/). With f16 KV, 256k on a 7B is achievable only with quantized cache; budget ~2× the KV of 128k plus slower prefill.

3. **Set `OLLAMA_KV_CACHE_TYPE=q8_0` (or q4_0) + flash attention.** Halves/quarters KV RAM with "very small precision loss"; works only with FA; Qwen-class high-GQA models lose more precision. (https://docs.ollama.com/faq). Set env at server boot; verify split with `ollama ps` (CPU-offload means you've blown VRAM).

4. **The failure mode of big context is OOM/spill-to-CPU, not math.** Ollama's guidance: max context that fits, avoid offloading, check `PROCESSOR` split. Community fix for 16k-ctx OOM was lowering `num_batch` to 32 (https://github.com/ollama/ollama/issues/1800). In Go: probe with a warm-up request, read `prompt_eval_duration`, keep at most one model loaded.

5. **Keep `OLLAMA_NUM_PARALLEL=1`.** KV/RAM scales `parallel × context`; for single-user voice keep one full KV budget. (https://docs.ollama.com/faq).

6. **Keep the KV cache warm with `keep_alive`** (default 5m) or `OLLAMA_KEEP_ALIVE=-1`; after idle unload every next call re-evals the whole prompt. (https://github.com/ollama/ollama/blob/main/docs/api.md). Send keep_alive in each request in Go.

7. **The message-history cache is prefix-based and on by default; never mutate the prefix.** llama.cpp `cache_prompt` defaults true, `--cache-reuse` KV-shift; Ollama hardwires cache on (PR #16639; shift flag #1399/#1987); cache reuse survives only if the rendered prefix matches byte-for-byte. Changing system prompt/tools/middle history or `num_ctx` kills it. In Go: build messages in a strict append-only slice, template once, reuse the same raw structure for every step.

8. **But long output/loops: don't replay the whole history — trim at ~95% of window.** LangChain: agent state append-only; Claude Code auto-compacts >95% usage; LlamaIndex memory inserts a static identity block plus token-flushed short-term memory (`chat_history_token_ratio`), recursively-summarized ancient turns. (https://docs.langchain.com/oss/python/langchain/agents; https://www.langchain.com/blog/context-engineering-for-agents; LlamaIndex memory docs). In Go keep a fixed persona block + rolling window + one accumulated summary.

9. **Persona continuity beats fixed prompt alone on long timescales — pair a small frozen persona with a writable dynamic memory block.** MemGPT "static system prompt + working context (persona/preferences) + recursive summary of evicted messages," with output: 32.1→92.5% deep retrieval with GPT-4 on MultiSession (F3). Small-model evidence: AgentMemBench (Qwen2.5-7B 4-bit) dense retrieval 0.573 vs ≤0.005 for windowed/summaries on LoCoMo; MRPrompt lets Qwen3-8B match large closed models. The HBS Replika paper shows identity discontinuity → perceived loss/mourning, i.e., continuity is a product property. (https://ar5iv.labs.arxiv.org/html/2310.08560; https://arxiv.org/abs/2412.14190; AgentMemBench 2608.00009). Implement a Go memory module: writable persona/preference block in the prompt, plus a vector archive for episodic events.

10. **Skip RAG on low-intent turns.** Chroma context-rot: focused ~300-token prompts beat ~113k full prompts across 18 models; even one irrelevant chunk degrades answers (https://research.trychroma.com/context-rot PMDA). Route: explicit response to greetings/small talk; BM25 (lexical) for entity/ID hits vs dense for paraphrase; return authoritative tool data directly (https://docs.pinecone.io/guides/optimize/increase-relevance; https://www.anthropic.com/engineering/effective-context-engineering-for-ai-agents).

11. **Batch embeddings via `/api/embed` with an `input` array, `truncate:false`, use `dimensions` (Matryoshka dims) for RAM.** Batching amortizes load/HTTP once per request; nomic-embed 137M/768 dims (128-dim at 59.34 MTEB), needs `search_document`/`search_query` prefixes; bge-m3 needs no prefix but is 4.5× the size. (https://docs.ollama.com/api/embed; nomic/bge-m3 cards models).

12. **Reserve an output budget and size system/retrieval/history fractions.** Ollama `num_predict` defaults to -1 (no reserve); Anthropic reserves 13–32% of the window for output. On 7–14B modest HW, e.g., 64k window: system ~1k, retrieval up to 4k, rolling summary up to ~12k, reserve 2k output, rest as headroom; measure loss empirically (fix `num_ctx` first). (https://www.anthropic.com/engineering/effective-context-engineering…; https://docs.ollama.com/api).

## Top-5 shortlist of sources
- https://docs.ollama.com — context-length, FAQ (KV cache type, OLLAMA_CONTEXT_LENGTH), /api/generate + /api/embed — the primary ground truth that the app must pin/verify.
- https://arxiv.org/abs/2310.08560+full text (MemGPT) — the architect's proof for persona block + working context + recursive summary pattern.
- https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md — the cache/cach coupling behind Ollama's flags.
- https://platform.openai.com/docs/guides/prompt-caching + https://docs.anthropic.com/en/docs/build-with-claude/prompt-caching — loop ordering/cache rules transferable to local caching.
- https://huggingface.co/blog/kv-cache-quantization — KV cache sizing formula + prefill memory bottleneck for window planning.

Open questions: exact current Ollama default `num_ctx` at runtime (docs disagree); Speed of cache reuse vs prefix changes in a real ReAct loop — needs empirical on target hardware (prompt_eval telemetry).