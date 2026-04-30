package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-vgo/robotgo"
	"github.com/user/mai/pkg/interfaces"
)

type AutomationTool struct{}

func (t *AutomationTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "ui_automation",
		Description: "Performs keyboard and mouse actions. Use this to type messages, use shortcuts (ctrl, alt), or click buttons.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"action": { "type": "string", "description": "Type: 'type', 'key', 'shortcut', 'click', 'move'" },
				"value": { "type": "string", "description": "Text to type or key name (e.g., 'enter', 'f5', 'c') " },
				"modifier": { "type": "string", "description": "Modifier for shortcuts: 'control', 'alt', 'shift', 'command'" },
				"x": { "type": "integer", "description": "X coordinate for mouse actions" },
				"y": { "type": "integer", "description": "Y coordinate for mouse actions" }
			},
			"required": ["action"]
		}`),
	}
}

func (t *AutomationTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Action   string `json:"action"`
		Value    string `json:"value"`
		Modifier string `json:"modifier"`
		X        int    `json:"x"`
		Y        int    `json:"y"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	time.Sleep(200 * time.Millisecond) // Short breather for system focus

	switch args.Action {
	case "type":
		robotgo.TypeStr(args.Value)
	case "key":
		robotgo.KeyTap(args.Value)
	case "shortcut":
		if args.Modifier != "" {
			robotgo.KeyTap(args.Value, args.Modifier)
		} else {
			robotgo.KeyTap(args.Value)
		}
	case "click":
		if args.X != 0 || args.Y != 0 {
			robotgo.MoveClick(args.X, args.Y, "left", false)
		} else {
			robotgo.Click("left", false)
		}
	case "move":
		robotgo.Move(args.X, args.Y)
	default:
		return interfaces.ToolResult{Error: fmt.Errorf("unknown automation action: %s", args.Action)}, nil
	}

	return interfaces.ToolResult{
		Output: fmt.Sprintf("RobotGo executed %s: %s", args.Action, args.Value),
	}, nil
}
