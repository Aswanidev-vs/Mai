package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/user/mai/pkg/interfaces"
)

type WhatsAppTool struct{}

func (t *WhatsAppTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "whatsapp_send",
		Description: "Opens a WhatsApp chat window with a specific message. Use this to send messages to people.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"message": { "type": "string", "description": "The message to send" },
				"recipient": { "type": "string", "description": "Optional: Name or phone number (e.g., 'Manu' or '123456789')" }
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

	// 1. Open WhatsApp
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, `C:\Windows\System32\cmd.exe`, "/c", "start", "whatsapp:")
	} else {
		cmd = exec.CommandContext(ctx, "open", "whatsapp:")
	}

	if err := cmd.Start(); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	// 2. Wait for WhatsApp to open and gain focus
	time.Sleep(2000 * time.Millisecond)

	// 3. Automate the UI if a recipient is provided
	if args.Recipient != "" {
		// Ctrl+F to focus search
		robotgo.KeyTap("f", "control")
		time.Sleep(500 * time.Millisecond)

		// Type recipient name
		robotgo.TypeStr(args.Recipient)
		time.Sleep(1000 * time.Millisecond) // Wait for search results

		// Press Enter to select the contact
		robotgo.KeyTap("enter")
		time.Sleep(500 * time.Millisecond)
	}

	// 4. Type the message and send
	robotgo.TypeStr(args.Message)
	time.Sleep(200 * time.Millisecond)
	robotgo.KeyTap("enter")

	return interfaces.ToolResult{
		Output: fmt.Sprintf("Opened WhatsApp and automated sending message to '%s'.", args.Recipient),
	}, nil
}
