package adapters

import (
	"context"
	"encoding/json"
	"time"

	"github.com/user/mai/pkg/interfaces"
)

// ClockTool allows Mai to see the current system time and date
type ClockTool struct{}

func (t *ClockTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "get_system_time",
		Description: "Returns the current local date and time. Use this whenever the user asks 'what time is it' or 'what is today's date'.",
		Parameters:  json.RawMessage(`{"type": "object", "properties": {}}`),
	}
}

func (t *ClockTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	now := time.Now()
	return interfaces.ToolResult{
		Output: now.Format("Monday, January 02, 2006, 15:04:05"),
	}, nil
}
