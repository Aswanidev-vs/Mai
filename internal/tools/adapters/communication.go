package adapters

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/user/mai/pkg/interfaces"
)

// WhatsAppSendFunc is the signature for the legacy SendMessage function.
// Injected from cmd/mai/automation.go at startup.
type WhatsAppSendFunc func(app, contact, text string) error

// WhatsAppTool wraps the legacy Automation.SendMessage as a tool.
type WhatsAppTool struct {
	Send WhatsAppSendFunc
}

func (t *WhatsAppTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "whatsapp_send",
		Description: "Sends a message to a contact on WhatsApp. The contact and message are required.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": { "type": "string", "description": "The message to send" },
				"recipient": { "type": "string", "description": "Contact name or phone number" }
			},
			"required": ["message"]
		}`),
	}
}

func (t *WhatsAppTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Message   string `json:"message"`
		Recipient string `json:"recipient"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	if t.Send == nil {
		return interfaces.ToolResult{Error: fmt.Errorf("whatsapp send function not configured")}, nil
	}

	err := t.Send("whatsapp", args.Recipient, args.Message)
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{
		Output: fmt.Sprintf("Sent message to '%s' on WhatsApp: %s", args.Recipient, args.Message),
	}, nil
}
