# Research brief: Go offline voice companion on local LLMs (Ollama)

- **Date**: 2026-08-09
- **Question**: How to build a Go offline voice companion using LOCAL LLMs (Ollama; 165k-256k context models on modest hardware) with goals of: persona consistency + efficient long-context prompting + ReAct tool-loop context retention.
- **Audience**: Go developer building a local voice companion app.
- **Constraints**: research uses webfetch only (no dedicated search tool); report ~600-800 words, ~12 concrete recommendations, each = (what to do / why with URL / how to apply in Go), plus Top-5 shortlist.
- **Depth mode**: standard (5 sub-agents, 1 follow-up round max, 15+ sources).

## Angles (one sub-agent each)

- F1: Long-context prompt architecture for local/Ollama LLMs: does bigger num_ctx hurt inference/KV cache? Practical num_ctx by model class (Qwen2.5-7b/14b, Gemma3, llama3 class); Ollama default num_ctx vs user-set; RAM + prompt eval speed implications.
- F2: Persona consistency across tool-call loops: should system prompt be re-sent on every ReAct step? LangChain / OpenAI / Anthropic docs on tool-calling + system prompt.
- F3: Emotional/personality continuity across turns: mood summaries vs fixed system prompt; companion-AI research.
- F4: Ollama cache_prompt / keep_alive / num_ctx interaction; llama.cpp context efficiency; does Ollama reuse prompt cache across turns?
- F5: RAG efficiency on low-end hardware: batch embed vs per-call; when to skip RAG; token budget split (system/prompt/tools/output) for small models.

## Out of scope
- ASR/TTS engine choice (voice I/O), vector DB selection specifics, GUI.

## Deliverable
- REPORT.md (main report) — the spawning agent receives the report text in the final message.
