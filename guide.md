# Mai: Agentic Mode Guide (JARVIS-Class)

Welcome to the autonomous era of Mai. This guide explains how to use the high-autonomy features and the reasoning engine.

## 1. Enabling Agentic Mode
To switch from a simple reactive assistant to a cognitive agent, update your `config.yaml`:

```yaml
agentic:
  enabled: true
```

## 2. Key Features
*   **Reasoning (ReAct)**: Mai doesn't just answer; she thinks. You will see `Thought -> Action -> Observation` cycles in your terminal.
*   **Universal Tool Use**: She can autonomously decide to search the web, open applications, or play music on YouTube.
*   **Memory Integration**:
    *   **Working Memory**: Remembers the current conversation context.
    *   **Episodic Memory**: Remembers past interactions across sessions using a local SQLite database.
*   **Proactive Agency**: Mai periodically self-reflects and checks her environment to see if you need help without being asked.

## 3. Privacy Guardrails (Hybrid Mode)
Mai is designed to be privacy-first.
*   **Local Processing**: By default, sensitive data (passwords, bank info) is handled by local models (Ollama).
*   **Cloud Boost**: Non-sensitive tasks can be routed to high-performance cloud models (OpenAI, Gemini) for faster reasoning.
*   **Toggle**: Use `llm.hybrid_mode: true` to enable this, or `false` to stay 100% local.

## 4. Voice Commands (JARVIS Feel)
*   **The Wake Word**: Say "Mai" to wake her up. She will respond with a quick greeting ("Yes?", "At your service") to acknowledge she is listening.
*   **Complex Goals**: Instead of simple commands, give her goals like:
    *   *"Mai, find the latest news on SpaceX and tell me about it."*
    *   *"Play some interstellar soundtrack on YouTube and then tell me my schedule."*
    *   *"Open VS Code and help me write a Go function."*

## 5. Advanced Configuration
*   **CUDA Support**: Set `provider: "cuda"` in the `asr`, `vad`, and `tts` sections to use your NVIDIA GPU for hearing and speaking.
*   **Sensitive Words**: Add your own confidential terms to the `privacy.sensitive_words` list in `config.yaml`.

---
*Created by Antigravity AI for the Mai Agentic Evolution Project.*
