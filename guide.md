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

## 6) How to add your own Companion Skill (Proposal 1)
Mai supports “Companion Skills” via a simple JSON manifest. A skill is triggered when your spoken/transcribed text **contains** one of the skill’s trigger phrases.

### Where to put your skill
Create/edit:
- `data/skills.json`

Mai will try to load this file at startup:
- if `data/skills.json` exists and is valid JSON, it will use it
- otherwise it falls back to built-in starter skills

### `data/skills.json` format
```json
{
  "skills": [
    {
      "id": "unique_skill_id",
      "name": "Skill Name",
      "description": "What this skill does.",
      "triggers": ["trigger phrase 1", "trigger phrase 2"],
      "prompt_seed": "Instructions to guide how the skill should respond."
    }
  ]
}
```

#### Important matching rule
- MAI matches skills by checking whether the user text **contains** a trigger string (case-insensitive substring match).
- Keep triggers specific (avoid very generic words like `"help"` or `"test"`).

### Example: add “Meeting Action Items”
Edit `data/skills.json` and add a skill like this:
```json
{
  "id": "meeting_action_items",
  "name": "Meeting Action Items",
  "description": "Extract action items, owners, and due dates from meeting notes.",
  "triggers": [
    "action items",
    "meeting action items",
    "extract tasks from meeting",
    "turn my notes into action items"
  ],
  "prompt_seed": "Extract action items with owner and due date if mentioned. If due date is missing, mark it as 'TBD'. If owner is missing, mark it as 'Unassigned'. Output as a numbered checklist."
}
```

### How to test your skill
1. Start Mai (restart after changing `data/skills.json`).
2. Speak a command that includes one trigger phrase, for example:
   - “Jarvis, extract tasks from meeting …”
3. If it matches, Mai will run the skill path and answer using the existing ReAct execution pipeline.

### Debugging / what gets saved
When a skill matches, Mai writes to episodic memory:
- `Type`: `"skill_invoked"`
- `Metadata.skill_id`: your skill’s `id`

You can inspect `data/memory/episodic.db` to confirm the entry was stored.

### Notes / v1 limitations
- This v1 “skill prompt seed” is stored in the manifest, but the initial runner framing is currently basic.
- The next iteration can improve how `prompt_seed` is injected to make skills more specialized.
