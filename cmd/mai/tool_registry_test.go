package main

import (
	"strings"
	"testing"

	"github.com/user/mai/internal/tools"
)

// TestBuiltinToolRegistryIncludesGuardedWhatsAppSend verifies that the LLM can
// use WhatsApp as a fallback, while the metadata makes the user-turn and
// confirmation guards visible to the model.
func TestBuiltinToolRegistryIncludesGuardedWhatsAppSend(t *testing.T) {
	registry := tools.NewRegistry()
	registerBuiltinTools(registry, &Automation{})

	sawShell := false
	sawWhatsApp := false
	for _, meta := range registry.List() {
		if meta.Name == "whatsapp_send" {
			sawWhatsApp = true
			if !strings.Contains(meta.Description, "user turn") {
				t.Fatalf("whatsapp_send description must mention the user-turn guard: %q", meta.Description)
			}
			if !strings.Contains(meta.Description, "confirm") {
				t.Fatalf("whatsapp_send description must mention confirmation: %q", meta.Description)
			}
		}
		if meta.Name == "shell_execute" {
			sawShell = true
		}
	}
	if !sawShell {
		t.Fatal("expected the built-in registry to be populated (shell tool missing)")
	}
	if !sawWhatsApp {
		t.Fatal("expected whatsapp_send to be registered as a guarded fallback")
	}
}
