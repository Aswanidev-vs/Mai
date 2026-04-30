package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"

	"github.com/user/mai/pkg/interfaces"
)

// YouTubePlayTool specifically handles playing music/videos on YouTube
type YouTubePlayTool struct{}

func (t *YouTubePlayTool) Metadata() interfaces.ToolMetadata {
	return interfaces.ToolMetadata{
		Name:        "play_youtube",
		Description: "Plays a song or video on YouTube. Use this when the user says 'play', 'listen to', or 'watch' something on YouTube.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": { "type": "string", "description": "The name of the song, artist, or video to play" }
			},
			"required": ["query"]
		}`),
	}
}

func (t *YouTubePlayTool) Execute(ctx context.Context, params json.RawMessage) (interfaces.ToolResult, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(params, &args); err != nil {
		return interfaces.ToolResult{}, err
	}

	// Using the search list type often triggers immediate playback of the top result
	playURL := fmt.Sprintf("https://www.youtube.com/results?search_query=%s&autoplay=1", url.QueryEscape(args.Query))
	
	// Alternatively, for a more "player-like" experience:
	// playURL := fmt.Sprintf("https://www.youtube.com/embed?listType=search&list=%s", url.QueryEscape(args.Query))
	
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// Windows 'start' can open URLs directly
		cmd = exec.CommandContext(ctx, `C:\Windows\System32\cmd.exe`, "/c", "start", playURL)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", playURL)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", playURL)
	}

	err := cmd.Run()
	if err != nil {
		return interfaces.ToolResult{Error: err}, nil
	}

	return interfaces.ToolResult{Output: fmt.Sprintf("Playing %s on YouTube", args.Query)}, nil
}
