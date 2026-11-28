package main

import (
	"github.com/user/mai/internal/tools"
	"github.com/user/mai/internal/tools/adapters"
	"github.com/user/mai/pkg/models"
)

// registerBuiltinTools registers the tools the LLM may call while handling a
// user turn.
//
// WhatsApp is a guarded fallback. The adapter can only prepare a draft while
// Orchestrator.HandleInput is active (see UserTurnStart/UserTurnEnd), so a
// startup timer or proactive task cannot open WhatsApp on its own. The regex
// parser remains the primary path, and PrepareMessage never presses Enter;
// ConfirmSend does that only after an explicit user confirmation.
func registerBuiltinTools(registry *tools.Registry, auto *Automation, cfg models.Config) {
	// Host-modifying tools are opt-in. A web page read by web_research can steer
	// the model, so registering these unconditionally turns prompt injection
	// into arbitrary code execution.
	if cfg.Agentic.AllowShell {
		registry.Register(&adapters.ShellTool{
			Allowed: []string{"cmd", "powershell", "python", "python3", "node", "git", "notepad"},
		})
	}
	registry.Register(&adapters.WebSearchTool{})
	registry.Register(&adapters.OpenAppTool{
		Open: func(name, browser string) error {
			return auto.OpenAppWithBrowser(name, browser)
		},
	})
	registry.Register(&adapters.YouTubeTool{})
	registry.Register(adapters.NewDeepSearchTool())
	registry.Register(adapters.NewWebResearchTool())
	if cfg.Agentic.AllowFileWrite {
		registry.Register(&adapters.FileWriteTool{})
	}
	registry.Register(&adapters.ClockTool{})
	registry.Register(&adapters.WhatsAppTool{
		Send: func(app, contact, text string) error {
			return auto.PrepareMessage(app, contact, text)
		},
	})
	registry.Register(&adapters.AutomationTool{})
}
