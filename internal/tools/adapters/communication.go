package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/user/mai/pkg/interfaces"
)

// WhatsAppSendFunc is the callback used by the adapter. Production wiring must
// point it at Automation.PrepareMessage, which only prepares a draft and waits
// for explicit confirmation.
type WhatsAppSendFunc func(app, contact, text string) error

// WhatsAppTool is the guarded LLM fallback for message requests the regex
// parser does not recognize. It is registered with the LLM tool registry, but
// Automation.PrepareMessage rejects calls unless an explicit ASR/chat turn is
// active (see Orchestrator.UserTurnStart/UserTurnEnd). The adapter never
// presses Enter; the user must confirm first.
type WhatsAppTool struct {
	Send WhatsAppSendFunc
}

func (t *WhatsAppTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "whatsapp_send",
		Description: "Prepares a WhatsApp draft for a contact when the user explicitly asks to send a message. Only callable during an explicit user turn; the user must confirm before it is sent.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": { "type": "string", "description": "The draft message to prepare" },
				"recipient": { "type": "string", "description": "Contact name or phone number from the user's request" }
			},
			"required": ["message", "recipient"]
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
	if strings.TrimSpace(args.Recipient) == "" {
		return interfaces.ToolResult{Error: fmt.Errorf("recipient is required")}, nil
	}
	if strings.TrimSpace(args.Message) == "" {
		return interfaces.ToolResult{Error: fmt.Errorf("message is required")}, nil
	}

	// Send is wired to Automation.PrepareMessage: it opens the chat and types
	// the draft, but deliberately waits for the user's confirmation before
	// pressing Enter.
	if err := t.Send("whatsapp", args.Recipient, args.Message); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{
		Output: fmt.Sprintf("Prepared a WhatsApp message for '%s'. The user must confirm before it is sent.", args.Recipient),
	}, nil
}
